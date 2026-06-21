package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

type cmdResult struct {
	code   int
	stdout string
	stderr string
}

func runCmd(store *registry.Store, args ...string) cmdResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithStore(args, store, &stdout, &stderr)
	return cmdResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func newTestStore(t *testing.T) *registry.Store {
	t.Helper()
	return registry.NewStore(filepath.Join(t.TempDir(), "servers"))
}

func mustCurrentExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current executable: %v", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve abs executable path: %v", err)
	}
	return abs
}

func writeTestExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows command fixture handling not needed in this test environment")
	}
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write test executable: %v", err)
	}
	return path
}

func writeSkillFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	return path
}

func TestRunWithStoreLifecycleCommands(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop")
	if result.code != 0 {
		t.Fatalf("add command failed with code %d, stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "list")
	if result.code != 0 {
		t.Fatalf("list command failed with code %d, stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "stewreads") {
		t.Fatalf("expected list output to contain server name, got: %s", result.stdout)
	}

	result = runCmd(store, "disable", "stewreads")
	if result.code != 0 {
		t.Fatalf("disable command failed with code %d, stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "disabled") {
		t.Fatalf("expected disable output, got: %s", result.stdout)
	}

	result = runCmd(store, "enable", "stewreads")
	if result.code != 0 {
		t.Fatalf("enable command failed with code %d, stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "enabled") {
		t.Fatalf("expected enable output, got: %s", result.stdout)
	}

	result = runCmd(store, "remove", "stewreads")
	if result.code != 0 {
		t.Fatalf("remove command failed with code %d, stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "removed") {
		t.Fatalf("expected remove output, got: %s", result.stdout)
	}
}

func TestRunWithStoreAddArgumentCoverage(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(
		store,
		"add", "stewreads",
		"--command", commandPath,
		"--description", "ebook converter",
		"--disabled",
		"--arg", "--stdio",
		"--arg", "--debug",
		"--client", "claude-desktop",
		"--client", "cursor",
		"--env", "STEWREADS_CONFIG_PATH=~/.config/stewreads/config.toml",
		"--env", "STEWREADS_PROFILE=personal",
		"--required-env", "STEWREADS_GMAIL_APP_PASSWORD",
	)
	if result.code != 0 {
		t.Fatalf("add command failed with code %d, stderr=%s", result.code, result.stderr)
	}

	manifest, err := store.Get("stewreads")
	if err != nil {
		t.Fatalf("expected manifest to exist: %v", err)
	}

	if manifest.Command != commandPath {
		t.Fatalf("expected command path to be persisted, got: %q", manifest.Command)
	}
	if manifest.Description != "ebook converter" {
		t.Fatalf("expected description to be saved, got: %q", manifest.Description)
	}
	if manifest.Enabled {
		t.Fatalf("expected manifest.Enabled=false with --disabled")
	}
	if len(manifest.Args) != 2 || manifest.Args[0] != "--stdio" || manifest.Args[1] != "--debug" {
		t.Fatalf("expected args to be saved, got: %#v", manifest.Args)
	}
	if len(manifest.Clients) != 2 {
		t.Fatalf("expected two clients, got: %#v", manifest.Clients)
	}
	if manifest.Env["STEWREADS_CONFIG_PATH"] == "" || manifest.Env["STEWREADS_PROFILE"] != "personal" {
		t.Fatalf("expected env vars to be saved, got: %#v", manifest.Env)
	}
	if len(manifest.RequiredEnv.Keys) != 1 || manifest.RequiredEnv.Keys[0] != "STEWREADS_GMAIL_APP_PASSWORD" {
		t.Fatalf("expected required env key to be saved, got: %#v", manifest.RequiredEnv.Keys)
	}
}

func TestRunWithStoreAddResolvesCommandFromPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH executable test is for unix-like environments")
	}
	store := newTestStore(t)
	dir := t.TempDir()
	_ = writeTestExecutable(t, dir, "fake-mcp")
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	result := runCmd(store, "add", "stewreads", "--command", "fake-mcp", "--client", "claude-desktop")
	if result.code != 0 {
		t.Fatalf("expected add with PATH command to succeed, got stderr=%s", result.stderr)
	}

	manifest, err := store.Get("stewreads")
	if err != nil {
		t.Fatalf("load stored manifest: %v", err)
	}
	if !filepath.IsAbs(manifest.Command) {
		t.Fatalf("expected resolved absolute command path, got: %q", manifest.Command)
	}
	if !strings.HasPrefix(manifest.Command, dir+string(filepath.Separator)) {
		t.Fatalf("expected resolved path in temp dir, got: %q", manifest.Command)
	}
}

func TestRunWithStoreAddRejectsMissingCommandBinary(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "add", "stewreads", "--command", "__definitely_missing_madari_command__", "--client", "claude-desktop")
	if result.code == 0 {
		t.Fatalf("expected add to fail for missing command")
	}
	if !strings.Contains(result.stderr, "not found in PATH") {
		t.Fatalf("expected not-found error, got: %s", result.stderr)
	}
}

func TestRunWithStoreAddValidatesRequiredFlags(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "missing name",
			args:     []string{"add"},
			expected: "usage: madari add",
		},
		{
			name:     "missing command",
			args:     []string{"add", "stewreads", "--client", "claude-desktop"},
			expected: "--command is required",
		},
		{
			name:     "missing client",
			args:     []string{"add", "stewreads", "--command", commandPath},
			expected: "--client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 {
				t.Fatalf("expected command to fail")
			}
			if !strings.Contains(result.stderr, tt.expected) {
				t.Fatalf("expected stderr to contain %q, got: %s", tt.expected, result.stderr)
			}
		})
	}
}

func TestRunWithStoreAddValidatesEnvAssignments(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name: "invalid env assignment",
			args: []string{
				"add", "stewreads", "--command", commandPath, "--client", "claude-desktop",
				"--env", "BROKEN",
			},
			expected: "invalid env assignment",
		},
		{
			name: "duplicate env key",
			args: []string{
				"add", "stewreads", "--command", commandPath, "--client", "claude-desktop",
				"--env", "A=1", "--env", "A=2",
			},
			expected: "duplicate env key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 {
				t.Fatalf("expected command to fail")
			}
			if !strings.Contains(result.stderr, tt.expected) {
				t.Fatalf("expected stderr to contain %q, got: %s", tt.expected, result.stderr)
			}
		})
	}
}

func TestRunWithStoreAddRejectsUnexpectedPositionals(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(
		store,
		"add", "stewreads",
		"--command", commandPath,
		"--client", "claude-desktop",
		"extra",
	)
	if result.code == 0 {
		t.Fatalf("expected command to fail")
	}
	if !strings.Contains(result.stderr, "unexpected positional arguments") {
		t.Fatalf("unexpected stderr: %s", result.stderr)
	}
}

func TestRunWithStoreSkillLifecycleCommands(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	content := "# Release\n\nCut a patch release.\n"
	path := writeSkillFile(t, tmp, "release.md", content)

	result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow")
	if result.code != 0 {
		t.Fatalf("skill add failed with code %d, stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "added skill release") {
		t.Fatalf("expected add output, got: %s", result.stdout)
	}

	skill, err := store.GetSkill("release")
	if err != nil {
		t.Fatalf("expected skill to exist: %v", err)
	}
	if skill.Description != "Release workflow" {
		t.Fatalf("expected description to be saved, got: %q", skill.Description)
	}
	storedContent, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("expected skill content to exist: %v", err)
	}
	if string(storedContent) != content {
		t.Fatalf("expected managed copy to match source, got: %q", storedContent)
	}

	result = runCmd(store, "skill", "list")
	if result.code != 0 {
		t.Fatalf("skill list failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "release") || !strings.Contains(result.stdout, "Release workflow") {
		t.Fatalf("expected list output to include skill, got: %s", result.stdout)
	}

	result = runCmd(store, "skill", "show", "release")
	if result.code != 0 {
		t.Fatalf("skill show failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "name: release") || !strings.Contains(result.stdout, "content:") {
		t.Fatalf("expected show output, got: %s", result.stdout)
	}

	result = runCmd(store, "skill", "render", "release")
	if result.code != 0 {
		t.Fatalf("skill render failed: %s", result.stderr)
	}
	if result.stdout != content {
		t.Fatalf("expected exact render content %q, got %q", content, result.stdout)
	}

	updatedContent := "# Release\n\nUpdated steps.\n"
	updatedPath := writeSkillFile(t, tmp, "release-updated.md", updatedContent)
	result = runCmd(store, "skill", "update", "release", "--file", updatedPath)
	if result.code != 0 {
		t.Fatalf("skill update failed: %s", result.stderr)
	}
	skill, err = store.GetSkill("release")
	if err != nil {
		t.Fatalf("load updated skill: %v", err)
	}
	if skill.Description != "Release workflow" {
		t.Fatalf("expected update without --description to preserve description, got: %q", skill.Description)
	}
	result = runCmd(store, "skill", "update", "release", "--file", updatedPath, "--description", "Updated workflow")
	if result.code != 0 {
		t.Fatalf("skill update with description failed: %s", result.stderr)
	}
	skill, err = store.GetSkill("release")
	if err != nil {
		t.Fatalf("load updated skill metadata: %v", err)
	}
	if skill.Description != "Updated workflow" {
		t.Fatalf("expected description update, got: %q", skill.Description)
	}
	result = runCmd(store, "skill", "render", "release")
	if result.code != 0 {
		t.Fatalf("skill render after update failed: %s", result.stderr)
	}
	if result.stdout != updatedContent {
		t.Fatalf("expected updated render content %q, got %q", updatedContent, result.stdout)
	}

	result = runCmd(store, "skill", "remove", "release")
	if result.code != 0 {
		t.Fatalf("skill remove failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed skill release") {
		t.Fatalf("expected remove output, got: %s", result.stdout)
	}
	if _, err := store.GetSkill("release"); !errors.Is(err, registry.ErrSkillNotFound) {
		t.Fatalf("expected skill removal, got: %v", err)
	}
}

func TestRunWithStoreSkillJSONOutputs(t *testing.T) {
	store := newTestStore(t)
	path := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")

	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	listResult := runCmd(store, "skill", "list", "--json")
	if listResult.code != 0 {
		t.Fatalf("skill list --json failed: %s", listResult.stderr)
	}
	var listPayload skillListJSON
	if err := json.Unmarshal([]byte(listResult.stdout), &listPayload); err != nil {
		t.Fatalf("parse skill list json: %v\n%s", err, listResult.stdout)
	}
	if listPayload.SchemaVersion != jsonSchemaVersion || listPayload.Command != "skill list" {
		t.Fatalf("unexpected list json envelope: %+v", listPayload)
	}
	if len(listPayload.Skills) != 1 || listPayload.Skills[0].Name != "release" || listPayload.Skills[0].ContentPath != "" {
		t.Fatalf("unexpected skill list json: %+v", listPayload.Skills)
	}

	showResult := runCmd(store, "skill", "show", "release", "--json")
	if showResult.code != 0 {
		t.Fatalf("skill show --json failed: %s", showResult.stderr)
	}
	var showPayload skillShowJSON
	if err := json.Unmarshal([]byte(showResult.stdout), &showPayload); err != nil {
		t.Fatalf("parse skill show json: %v\n%s", err, showResult.stdout)
	}
	if showPayload.SchemaVersion != jsonSchemaVersion || showPayload.Command != "skill show" {
		t.Fatalf("unexpected show json envelope: %+v", showPayload)
	}
	if showPayload.Skill.Name != "release" ||
		showPayload.Skill.Description != "Release workflow" ||
		!strings.HasSuffix(showPayload.Skill.ContentPath, filepath.Join("skills", "release.md")) {
		t.Fatalf("unexpected skill show json: %+v", showPayload.Skill)
	}
}

func TestRunWithStoreSkillClientRenderNormalizesFrontmatter(t *testing.T) {
	store := newTestStore(t)
	source := `---
name: old-release
description: Source release workflow
allowed-tools:
  - Read
---

# Release
Cut a patch release.
`
	path := writeSkillFile(t, t.TempDir(), "release.md", source)
	if result := runCmd(store, "skill", "add", "release", "--file", path); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	result := runCmd(store, "skill", "render", "release", "--client", "codex")
	if result.code != 0 {
		t.Fatalf("skill render --client failed: %s", result.stderr)
	}
	for _, want := range []string{
		`name: "release"`,
		`description: "Source release workflow"`,
		"allowed-tools:\n  - Read",
		"# Release\nCut a patch release.",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("expected %q in client render:\n%s", want, result.stdout)
		}
	}
	if strings.Contains(result.stdout, "old-release") || strings.Count(result.stdout, "description:") != 1 {
		t.Fatalf("expected normalized frontmatter, got:\n%s", result.stdout)
	}

	generic := runCmd(store, "skill", "render", "release")
	if generic.code != 0 {
		t.Fatalf("generic render failed: %s", generic.stderr)
	}
	if generic.stdout != source {
		t.Fatalf("generic render should remain exact, got:\n%s", generic.stdout)
	}
}

func TestRunWithStoreSkillAttachDryRunWritesNothing(t *testing.T) {
	store := newTestStore(t)
	path := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	skillsDir := filepath.Join(t.TempDir(), "skills")
	result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir, "--dry-run")
	if result.code != 0 {
		t.Fatalf("skill attach --dry-run failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "dry-run: true") || !strings.Contains(result.stdout, "added: release") {
		t.Fatalf("expected dry-run add summary, got:\n%s", result.stdout)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "release", "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run should not write skill file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.ServersDir()), "state", "codex-skills-managed.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run should not write state file, stat err=%v", err)
	}
}

func TestRunWithStoreSkillAttachLifecycle(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	skillsDir := filepath.Join(tmp, "skills")
	result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code != 0 {
		t.Fatalf("skill attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "added: release") {
		t.Fatalf("expected add summary, got:\n%s", result.stdout)
	}
	skillPath := filepath.Join(skillsDir, "release", "SKILL.md")
	payload, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("expected attached skill file: %v", err)
	}
	if !strings.Contains(string(payload), `name: "release"`) || !strings.Contains(string(payload), "# Release") {
		t.Fatalf("unexpected attached skill file:\n%s", payload)
	}

	result = runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code != 0 {
		t.Fatalf("second skill attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "unchanged: release") {
		t.Fatalf("expected unchanged summary, got:\n%s", result.stdout)
	}

	updatedPath := writeSkillFile(t, tmp, "release-updated.md", "# Release\n\nUpdated.\n")
	if result := runCmd(store, "skill", "update", "release", "--file", updatedPath); result.code != 0 {
		t.Fatalf("skill update failed: %s", result.stderr)
	}
	result = runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code != 0 {
		t.Fatalf("skill attach update failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "updated: release") {
		t.Fatalf("expected updated summary, got:\n%s", result.stdout)
	}
	payload, err = os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read updated attached skill: %v", err)
	}
	if !strings.Contains(string(payload), "Updated.") {
		t.Fatalf("expected attached skill update, got:\n%s", payload)
	}
}

func TestRunWithStoreSkillAttachAllowsSameSkillAcrossRoots(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	rootA := filepath.Join(tmp, "project-a", "skills")
	rootB := filepath.Join(tmp, "project-b", "skills")
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", rootA); result.code != 0 {
		t.Fatalf("first skill attach failed: %s", result.stderr)
	}
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", rootB); result.code != 0 {
		t.Fatalf("second root skill attach failed: %s", result.stderr)
	}

	statePath := filepath.Join(filepath.Dir(store.ServersDir()), "state", "codex-skills-managed.json")
	state, err := loadSkillAttachmentState(statePath)
	if err != nil {
		t.Fatalf("load skill attachment state: %v", err)
	}
	if got := len(skillAttachmentsByName(state, "release")); got != 2 {
		t.Fatalf("expected two release attachments, got %d: %+v", got, state)
	}

	result := runCmd(store, "skill", "detach", "release", "codex", "--skills-dir", rootA)
	if result.code != 0 {
		t.Fatalf("detach first root failed: %s", result.stderr)
	}
	if _, err := os.Stat(filepath.Join(rootA, "release", "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected first root skill removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(rootB, "release", "SKILL.md")); err != nil {
		t.Fatalf("expected second root skill to remain: %v", err)
	}
}

func TestRunWithStoreSkillAttachVibeUserHonorsVibeHome(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	vibeHome := filepath.Join(tmp, "vibe-home")
	t.Setenv("VIBE_HOME", vibeHome)

	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	result := runCmd(store, "skill", "attach", "release", "vibe", "--scope", "user", "--dry-run")
	if result.code != 0 {
		t.Fatalf("skill attach dry-run failed: %s", result.stderr)
	}
	want := "skills_dir: " + filepath.Join(vibeHome, "skills")
	if !strings.Contains(result.stdout, want) {
		t.Fatalf("expected VIBE_HOME skill root %q, got:\n%s", want, result.stdout)
	}
}

func TestRunWithStoreSkillAttachRollsBackAddedFileWhenStateWriteFails(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	original := saveSkillAttachmentStateFunc
	saveSkillAttachmentStateFunc = func(string, map[string]skillAttachmentEntry) error {
		return errors.New("state save failed")
	}
	t.Cleanup(func() {
		saveSkillAttachmentStateFunc = original
	})

	skillsDir := filepath.Join(tmp, "skills")
	result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code == 0 || !strings.Contains(result.stderr, "state save failed") {
		t.Fatalf("expected state save failure, code=%d stderr=%s", result.code, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "release", "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected added skill file rolled back, stat err=%v", err)
	}
}

func TestRunWithStoreSkillAttachRestoresUpdatedFileWhenStateWriteFails(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	skillsDir := filepath.Join(tmp, "skills")
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir); result.code != 0 {
		t.Fatalf("initial skill attach failed: %s", result.stderr)
	}
	skillPath := filepath.Join(skillsDir, "release", "SKILL.md")
	originalContent, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read initial skill file: %v", err)
	}

	updatedPath := writeSkillFile(t, tmp, "release-updated.md", "# Release\n\nUpdated.\n")
	if result := runCmd(store, "skill", "update", "release", "--file", updatedPath); result.code != 0 {
		t.Fatalf("skill update failed: %s", result.stderr)
	}
	original := saveSkillAttachmentStateFunc
	saveSkillAttachmentStateFunc = func(string, map[string]skillAttachmentEntry) error {
		return errors.New("state save failed")
	}
	t.Cleanup(func() {
		saveSkillAttachmentStateFunc = original
	})

	result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code == 0 || !strings.Contains(result.stderr, "state save failed") {
		t.Fatalf("expected state save failure, code=%d stderr=%s", result.code, result.stderr)
	}
	restored, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read restored skill file: %v", err)
	}
	if string(restored) != string(originalContent) {
		t.Fatalf("expected original skill file restored:\nwant:\n%s\ngot:\n%s", originalContent, restored)
	}
}

func TestRunWithStoreSkillAttachRefusesUnmanagedFile(t *testing.T) {
	store := newTestStore(t)
	path := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}

	skillsDir := filepath.Join(t.TempDir(), "skills")
	skillPath := filepath.Join(skillsDir, "release", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("mkdir unmanaged skill dir: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# Unmanaged\n"), 0o644); err != nil {
		t.Fatalf("write unmanaged skill: %v", err)
	}
	result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir)
	if result.code == 0 || !strings.Contains(result.stderr, "refusing to overwrite unmanaged skill file") {
		t.Fatalf("expected unmanaged conflict, code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreSkillDetachRemovesOwnedFileOnly(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	skillsDir := filepath.Join(tmp, "skills")
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir); result.code != 0 {
		t.Fatalf("skill attach failed: %s", result.stderr)
	}
	skillDir := filepath.Join(skillsDir, "release")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	assetPath := filepath.Join(skillDir, "reference.md")
	if err := os.WriteFile(assetPath, []byte("supporting file\n"), 0o644); err != nil {
		t.Fatalf("write supporting file: %v", err)
	}

	result := runCmd(store, "skill", "detach", "release", "codex", "--skills-dir", skillsDir)
	if result.code != 0 {
		t.Fatalf("skill detach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed: release") {
		t.Fatalf("expected removed summary, got:\n%s", result.stdout)
	}
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected SKILL.md removed, stat err=%v", err)
	}
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("supporting file should remain: %v", err)
	}

	result = runCmd(store, "skill", "detach", "release", "codex", "--skills-dir", skillsDir)
	if result.code != 0 || !strings.Contains(result.stdout, "unchanged: release") {
		t.Fatalf("expected idempotent detach, code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
}

func TestRunWithStoreSkillDetachRefusesModifiedFile(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	skillsDir := filepath.Join(tmp, "skills")
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", skillsDir); result.code != 0 {
		t.Fatalf("skill attach failed: %s", result.stderr)
	}
	skillPath := filepath.Join(skillsDir, "release", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# User edit\n"), 0o644); err != nil {
		t.Fatalf("modify attached skill: %v", err)
	}

	result := runCmd(store, "skill", "detach", "release", "codex", "--skills-dir", skillsDir)
	if result.code == 0 || !strings.Contains(result.stderr, "refusing to remove modified skill file") {
		t.Fatalf("expected modified-file refusal, code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreSkillRemoveRefusesAttachedSkill(t *testing.T) {
	store := newTestStore(t)
	path := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", path, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	if result := runCmd(store, "skill", "attach", "release", "codex", "--skills-dir", filepath.Join(t.TempDir(), "skills")); result.code != 0 {
		t.Fatalf("skill attach failed: %s", result.stderr)
	}

	result := runCmd(store, "skill", "remove", "release")
	if result.code == 0 || !strings.Contains(result.stderr, "skill \"release\" is still attached") || !strings.Contains(result.stderr, "madari skill detach release codex") {
		t.Fatalf("expected attached-skill removal refusal, code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreSkillValidatesInputs(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	path := writeSkillFile(t, tmp, "release.md", "# Release\n")
	emptyPath := writeSkillFile(t, tmp, "empty.md", "  \n")

	if result := runCmd(store, "skill", "add", "release", "--file", path); result.code != 0 {
		t.Fatalf("setup skill add failed: %s", result.stderr)
	}

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "duplicate add",
			args:     []string{"skill", "add", "release", "--file", path},
			expected: "already exists",
		},
		{
			name:     "update missing skill",
			args:     []string{"skill", "update", "missing", "--file", path},
			expected: "skill \"missing\" not found",
		},
		{
			name:     "add empty content",
			args:     []string{"skill", "add", "empty", "--file", emptyPath},
			expected: "is empty",
		},
		{
			name:     "add missing file flag",
			args:     []string{"skill", "add", "nofile"},
			expected: "--file is required",
		},
		{
			name:     "render missing skill",
			args:     []string{"skill", "render", "missing"},
			expected: "skill \"missing\" not found",
		},
		{
			name:     "list extra positional",
			args:     []string{"skill", "list", "extra"},
			expected: "unexpected positional arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 {
				t.Fatalf("expected command to fail")
			}
			if !strings.Contains(result.stderr, tt.expected) {
				t.Fatalf("expected stderr to contain %q, got: %s", tt.expected, result.stderr)
			}
		})
	}
}

func TestRunWithStoreCommandUsageValidation(t *testing.T) {
	store := newTestStore(t)

	tests := []struct {
		name        string
		args        []string
		expected    string
		helpCommand string
	}{
		{name: "install missing package", args: []string{"install"}, expected: "usage: madari install <package>", helpCommand: "install"},
		{name: "list with arg", args: []string{"list", "oops"}, expected: "usage: madari list"},
		{name: "remove missing name", args: []string{"remove"}, expected: "usage: madari remove <name>"},
		{name: "enable missing name", args: []string{"enable"}, expected: "usage: madari enable <name>"},
		{name: "disable missing name", args: []string{"disable"}, expected: "usage: madari disable <name>"},
		{name: "sync missing target", args: []string{"sync"}, expected: "usage: madari sync <client>", helpCommand: "sync"},
		{name: "sync unsupported target", args: []string{"sync", "cursor"}, expected: "unsupported sync target", helpCommand: "sync"},
		{name: "sync extra positionals", args: []string{"sync", "claude-desktop", "extra"}, expected: "unexpected positional arguments", helpCommand: "sync"},
		{name: "clients extra positionals", args: []string{"clients", "extra"}, expected: "unexpected positional arguments"},
		{name: "doctor unknown client-config target", args: []string{"doctor", "--client-config", "cursor=/tmp/x"}, expected: "unknown client config target", helpCommand: "doctor"},
		{name: "status unknown client-config target", args: []string{"status", "--client-config", "cursor=/tmp/x"}, expected: "unknown client config target", helpCommand: "status"},
		{name: "doctor extra positionals", args: []string{"doctor", "extra"}, expected: "unexpected positional arguments", helpCommand: "doctor"},
		{name: "status extra positionals", args: []string{"status", "extra"}, expected: "unexpected positional arguments", helpCommand: "status"},
		{name: "export extra positionals", args: []string{"export", "extra"}, expected: "unexpected positional arguments", helpCommand: "export"},
		{name: "import missing file", args: []string{"import"}, expected: "--file is required", helpCommand: "import"},
		{name: "import extra positionals", args: []string{"import", "--file", "snapshot.json", "extra"}, expected: "unexpected positional arguments", helpCommand: "import"},
		{name: "skill missing subcommand", args: []string{"skill"}, expected: "usage: madari skill", helpCommand: "skill"},
		{name: "skill unknown subcommand", args: []string{"skill", "bogus"}, expected: "unknown skill subcommand", helpCommand: "skill"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 {
				t.Fatalf("expected command to fail")
			}
			if !strings.Contains(result.stderr, tt.expected) {
				t.Fatalf("expected stderr to contain %q, got: %s", tt.expected, result.stderr)
			}
			if tt.helpCommand != "" && !strings.Contains(result.stderr, "madari help "+tt.helpCommand) {
				t.Fatalf("expected stderr to mention command help for %q, got: %s", tt.helpCommand, result.stderr)
			}
		})
	}
}

func TestRunWithStoreCommandFlagParsingShowsHelpHint(t *testing.T) {
	store := newTestStore(t)

	tests := []struct {
		name        string
		args        []string
		helpCommand string
	}{
		{name: "install unknown flag", args: []string{"install", "stewreads-mcp", "--bogus"}, helpCommand: "install"},
		{name: "sync unknown flag", args: []string{"sync", "claude-desktop", "--bogus"}, helpCommand: "sync"},
		{name: "doctor unknown flag", args: []string{"doctor", "--bogus"}, helpCommand: "doctor"},
		{name: "status unknown flag", args: []string{"status", "--bogus"}, helpCommand: "status"},
		{name: "export unknown flag", args: []string{"export", "--bogus"}, helpCommand: "export"},
		{name: "import unknown flag", args: []string{"import", "--bogus"}, helpCommand: "import"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(store, tt.args...)
			if result.code == 0 {
				t.Fatalf("expected command to fail")
			}
			if !strings.Contains(result.stderr, "flag provided but not defined") {
				t.Fatalf("expected unknown flag error, got: %s", result.stderr)
			}
			if !strings.Contains(result.stderr, "madari help "+tt.helpCommand) {
				t.Fatalf("expected help hint for %q, got: %s", tt.helpCommand, result.stderr)
			}
		})
	}
}

func TestRunHelpSubcommandOutput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{name: "help clients", args: []string{"help", "clients"}, contains: "madari clients"},
		{name: "help help", args: []string{"help", "help"}, contains: "madari help [command]"},
		{name: "help install", args: []string{"help", "install"}, contains: "madari install <package>"},
		{name: "help add", args: []string{"help", "add"}, contains: "madari add <name>"},
		{name: "help sync", args: []string{"help", "sync"}, contains: "madari sync <client>"},
		{name: "help ring", args: []string{"help", "ring"}, contains: "madari ring <subcommand>"},
		{name: "help skill", args: []string{"help", "skill"}, contains: "madari skill <subcommand>"},
		{name: "help list", args: []string{"help", "list"}, contains: "madari list"},
		{name: "help doctor", args: []string{"help", "doctor"}, contains: "madari doctor"},
		{name: "help status", args: []string{"help", "status"}, contains: "madari status"},
		{name: "help export", args: []string{"help", "export"}, contains: "madari export"},
		{name: "help import", args: []string{"help", "import"}, contains: "madari import"},
		{name: "help version", args: []string{"help", "version"}, contains: "madari version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected help to succeed, code=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.contains) {
				t.Fatalf("expected help output to contain %q, got: %s", tt.contains, stdout.String())
			}
		})
	}
}

func TestRunHelpSubcommandUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"help", "unknown"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected unknown subcommand help to fail")
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got: %s", stderr.String())
	}
}

func TestRunTopLevelHelpAndVersionArgumentValidation(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantCode    int
		stdoutMatch string
		stderrMatch string
	}{
		{
			name:        "help help flag",
			args:        []string{"help", "--help"},
			wantCode:    0,
			stdoutMatch: "madari help [command]",
		},
		{
			name:        "version help flag",
			args:        []string{"version", "--help"},
			wantCode:    0,
			stdoutMatch: "madari version",
		},
		{
			name:        "version extra arg",
			args:        []string{"version", "extra"},
			wantCode:    1,
			stderrMatch: "usage: madari version",
		},
		{
			name:        "short version extra arg",
			args:        []string{"-v", "extra"},
			wantCode:    1,
			stderrMatch: "usage: madari -v",
		},
		{
			name:        "long version extra arg",
			args:        []string{"--version", "extra"},
			wantCode:    1,
			stderrMatch: "usage: madari --version",
		},
		{
			name:        "top level help extra arg",
			args:        []string{"--help", "extra"},
			wantCode:    1,
			stderrMatch: "usage: madari help [command]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("expected code %d, got %d stdout=%s stderr=%s", tt.wantCode, code, stdout.String(), stderr.String())
			}
			if tt.stdoutMatch != "" && !strings.Contains(stdout.String(), tt.stdoutMatch) {
				t.Fatalf("expected stdout to contain %q, got: %s", tt.stdoutMatch, stdout.String())
			}
			if tt.stderrMatch != "" && !strings.Contains(stderr.String(), tt.stderrMatch) {
				t.Fatalf("expected stderr to contain %q, got: %s", tt.stderrMatch, stderr.String())
			}
		})
	}
}

func TestRunWithStoreSubcommandHelpFlags(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari add <name>") {
		t.Fatalf("expected add --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "install", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari install <package>") {
		t.Fatalf("expected install --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "sync", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari sync <client>") {
		t.Fatalf("expected sync --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "skill", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari skill <subcommand>") {
		t.Fatalf("expected skill --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "skill", "add", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari skill add <name>") {
		t.Fatalf("expected skill add --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "list", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari list") {
		t.Fatalf("expected list --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "remove", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari remove <name>") {
		t.Fatalf("expected remove --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "enable", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari enable <name>") {
		t.Fatalf("expected enable --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "disable", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari disable <name>") {
		t.Fatalf("expected disable --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "clients", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari clients") {
		t.Fatalf("expected clients --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "doctor", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari doctor") {
		t.Fatalf("expected doctor --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "status", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari status") {
		t.Fatalf("expected status --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "export", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari export") {
		t.Fatalf("expected export --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "import", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari import") {
		t.Fatalf("expected import --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}

	// Make sure normal add still works after help coverage.
	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("expected add after help checks to work, stderr=%s", result.stderr)
	}
}

func TestRunWithStoreInstallSkipInstallAndNoSync(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(
		store,
		"install", "stewreads-mcp",
		"--skip-install",
		"--no-sync",
		"--command", commandPath,
	)
	if result.code != 0 {
		t.Fatalf("install failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "skipped package install: stewreads-mcp") {
		t.Fatalf("expected skip-install output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "added stewreads") {
		t.Fatalf("expected derived manifest name output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "sync skipped") {
		t.Fatalf("expected no-sync output, got: %s", result.stdout)
	}

	manifest, err := store.Get("stewreads")
	if err != nil {
		t.Fatalf("expected manifest to exist: %v", err)
	}
	if manifest.Command != commandPath {
		t.Fatalf("expected command path %q, got: %q", commandPath, manifest.Command)
	}
	if len(manifest.Clients) != 1 || manifest.Clients[0] != "claude-desktop" {
		t.Fatalf("expected default client claude-desktop, got: %#v", manifest.Clients)
	}
}

func TestRunWithStoreInstallDerivesNameFromDottedPackage(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(
		store,
		"install", "awslabs.core-mcp-server",
		"--skip-install",
		"--no-sync",
		"--command", commandPath,
	)
	if result.code != 0 {
		t.Fatalf("install failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "added awslabs.core-mcp-server") {
		t.Fatalf("expected derived manifest name output, got: %s", result.stdout)
	}

	manifest, err := store.Get("awslabs.core-mcp-server")
	if err != nil {
		t.Fatalf("expected manifest to exist: %v", err)
	}
	if manifest.Command != commandPath {
		t.Fatalf("expected command path %q, got: %q", commandPath, manifest.Command)
	}

	removeResult := runCmd(store, "remove", "awslabs.core-mcp-server")
	if removeResult.code != 0 {
		t.Fatalf("remove failed: %s", removeResult.stderr)
	}
	if !strings.Contains(removeResult.stdout, "removed awslabs.core-mcp-server") {
		t.Fatalf("expected remove output, got: %s", removeResult.stdout)
	}
}

func TestRunWithStoreInstallRunsUVInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture for uv installer test is unix-specific")
	}

	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "uv-args.log")
	uvPath := filepath.Join(binDir, "uv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + logPath + "'\n"
	if err := os.WriteFile(uvPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake uv: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	result := runCmd(
		store,
		"install", "stewreads-mcp",
		"--no-sync",
		"--command", commandPath,
	)
	if result.code != 0 {
		t.Fatalf("install with uv failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "installed package: stewreads-mcp") {
		t.Fatalf("expected install output, got: %s", result.stdout)
	}

	argsPayload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake uv args log: %v", err)
	}
	argsText := string(argsPayload)
	if !strings.Contains(argsText, "tool") || !strings.Contains(argsText, "install") || !strings.Contains(argsText, "stewreads-mcp") {
		t.Fatalf("expected uv args to include `tool install stewreads-mcp`, got: %s", argsText)
	}
}

func TestRunWithStoreInstallRunsNPMInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture for npm installer test is unix-specific")
	}

	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "npm-args.log")
	npmPath := filepath.Join(binDir, "npm")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + logPath + "'\n"
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	packageName := "@modelcontextprotocol/server-sequential-thinking"
	result := runCmd(
		store,
		"install", packageName,
		"--manager", "npm",
		"--no-sync",
		"--command", commandPath,
	)
	if result.code != 0 {
		t.Fatalf("install with npm failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "installed package: "+packageName) {
		t.Fatalf("expected install output, got: %s", result.stdout)
	}

	argsPayload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake npm args log: %v", err)
	}
	argsText := string(argsPayload)
	if !strings.Contains(argsText, "install") || !strings.Contains(argsText, "-g") || !strings.Contains(argsText, packageName) {
		t.Fatalf("expected npm args to include `install -g %s`, got: %s", packageName, argsText)
	}
}

func TestRunWithStoreInstallNPMRequiresCommand(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "install", "stewreads-mcp", "--manager", "npm", "--no-sync")
	if result.code == 0 {
		t.Fatalf("expected install to fail when npm command is omitted")
	}
	if !strings.Contains(result.stderr, "--command is required when --manager npm") {
		t.Fatalf("expected npm command requirement error, got: %s", result.stderr)
	}
}

func TestRunWithStoreInstallRejectsUnsupportedManager(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(store, "install", "stewreads-mcp", "--manager", "pipx", "--no-sync", "--command", commandPath)
	if result.code == 0 {
		t.Fatalf("expected install to fail for unsupported manager")
	}
	if !strings.Contains(result.stderr, "unsupported package manager \"pipx\" (supported: uv, npm)") {
		t.Fatalf("expected unsupported manager error, got: %s", result.stderr)
	}
}

func TestRunWithStoreInstallErrorsWhenUVMissing(t *testing.T) {
	store := newTestStore(t)
	t.Setenv("PATH", "")

	result := runCmd(store, "install", "stewreads-mcp", "--no-sync")
	if result.code == 0 {
		t.Fatalf("expected install to fail when uv is missing")
	}
	if !strings.Contains(result.stderr, "uv not found in PATH") {
		t.Fatalf("expected uv missing error, got: %s", result.stderr)
	}
}

func TestRunWithStoreInstallErrorsWhenNPMMissing(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	t.Setenv("PATH", "")

	result := runCmd(store, "install", "stewreads-mcp", "--manager", "npm", "--no-sync", "--command", commandPath)
	if result.code == 0 {
		t.Fatalf("expected install to fail when npm is missing")
	}
	if !strings.Contains(result.stderr, "npm not found in PATH") {
		t.Fatalf("expected npm missing error, got: %s", result.stderr)
	}
}

func TestRunWithStoreInstallAutoSyncByDefault(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"weather":{"command":"uv"}}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(
		store,
		"install", "stewreads-mcp",
		"--skip-install",
		"--command", commandPath,
		"--config-path", configPath,
	)
	if result.code != 0 {
		t.Fatalf("install auto-sync failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "sync target: claude-desktop") {
		t.Fatalf("expected sync output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after install sync: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected synced config to include stewreads server, got: %s", string(after))
	}
}

func TestRunWithStoreInstallAutoSyncClaudeCode(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"weather":{"command":"uv"}}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(
		store,
		"install", "stewreads-mcp",
		"--skip-install",
		"--command", commandPath,
		"--client", "claude-code",
		"--config-path", configPath,
	)
	if result.code != 0 {
		t.Fatalf("install auto-sync failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "sync target: claude-code") {
		t.Fatalf("expected claude-code sync output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after install sync: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected synced config to include stewreads server, got: %s", string(after))
	}
}

func TestRunWithStoreSyncDryRun(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	original := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv"
    }
  }
}
`)
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "claude-desktop", "--dry-run", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync dry-run failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: dry-run") {
		t.Fatalf("expected dry-run mode output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "added: stewreads") {
		t.Fatalf("expected add plan output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after dry-run: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected dry-run to preserve config file")
	}

	statePath := filepath.Join(filepath.Dir(store.ServersDir()), "state", "claude-desktop-managed.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected no state file write on dry-run, got err=%v", err)
	}
}

func TestRunWithStoreSyncApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"weather":{"command":"uv"}}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "claude-desktop", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync apply failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: applied") {
		t.Fatalf("expected applied mode output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after sync: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected synced config to include stewreads server, got: %s", string(after))
	}
	if !strings.Contains(string(after), "\"weather\"") {
		t.Fatalf("expected synced config to preserve existing weather server, got: %s", string(after))
	}
}

func TestRunWithStoreSyncClaudeCodeApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"weather":{"command":"uv"}}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync apply failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: applied") {
		t.Fatalf("expected applied mode output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "sync target: claude-code") {
		t.Fatalf("expected claude-code target output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after sync: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected synced config to include stewreads server, got: %s", string(after))
	}
	if !strings.Contains(string(after), "\"weather\"") {
		t.Fatalf("expected synced config to preserve existing weather server, got: %s", string(after))
	}
}

func TestRunWithStoreSyncGeminiApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "gemini")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"weather":{"command":"uv"}}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "gemini", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync apply failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: applied") {
		t.Fatalf("expected applied mode output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "sync target: gemini") {
		t.Fatalf("expected gemini target output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after sync: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected synced config to include stewreads server, got: %s", string(after))
	}
	if !strings.Contains(string(after), "\"weather\"") {
		t.Fatalf("expected synced config to preserve existing weather server, got: %s", string(after))
	}
}

func TestRunWithStoreSyncCodexApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "codex")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5\"\n\n[mcp_servers.weather]\ncommand = \"uv\"\n"), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "codex", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync apply failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: applied") {
		t.Fatalf("expected applied mode output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "sync target: codex") {
		t.Fatalf("expected codex target output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after sync: %v", err)
	}
	for _, want := range []string{
		"model = ",
		"gpt-5",
		"[mcp_servers.weather]",
		"[mcp_servers.stewreads]",
		commandPath,
	} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("expected synced Codex config to contain %q, got: %s", want, after)
		}
	}
}

func TestRunWithStoreSyncVibeApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	addResult := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "vibe")
	if addResult.code != 0 {
		t.Fatalf("setup add failed: %s", addResult.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(`active_model = "dev"

[[mcp_servers]]
name = "weather"
transport = "http"
url = "http://localhost:8000"
`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "vibe", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync apply failed with stderr: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "mode: applied") {
		t.Fatalf("expected applied mode output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "sync target: vibe") {
		t.Fatalf("expected vibe target output, got: %s", result.stdout)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after sync: %v", err)
	}
	for _, want := range []string{
		"active_model",
		"dev",
		"weather",
		"stewreads",
		"transport = 'stdio'",
		commandPath,
	} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("expected synced Vibe config to contain %q, got: %s", want, after)
		}
	}
}

func TestRunWithStoreSyncSkipsMissingExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture handling not needed in this test environment")
	}
	store := newTestStore(t)
	binDir := t.TempDir()

	goodPath := writeTestExecutable(t, binDir, "good-mcp")
	badPath := writeTestExecutable(t, binDir, "bad-mcp")

	if result := runCmd(store, "add", "good", "--command", goodPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add good failed: %s", result.stderr)
	}
	if result := runCmd(store, "add", "bad", "--command", badPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add bad failed: %s", result.stderr)
	}

	if err := os.Remove(badPath); err != nil {
		t.Fatalf("remove bad executable fixture: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "claude-desktop", "--dry-run", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync should not fail when one executable is missing, stderr=%s", result.stderr)
	}
	if !strings.Contains(result.stdout, "added: good") {
		t.Fatalf("expected valid server to be included in add plan, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "skipped: bad") {
		t.Fatalf("expected missing executable server to be skipped, got: %s", result.stdout)
	}
	if !strings.Contains(result.stderr, "warning: skipped bad") {
		t.Fatalf("expected warning for skipped server, got: %s", result.stderr)
	}
}

func TestRunWithStoreExportStdout(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	result := runCmd(store, "export")
	if result.code != 0 {
		t.Fatalf("export failed: %s", result.stderr)
	}

	snapshot, err := registry.ParseSnapshotJSON([]byte(result.stdout))
	if err != nil {
		t.Fatalf("parse export output: %v\noutput:\n%s", err, result.stdout)
	}
	if snapshot.Version != registry.SnapshotVersion {
		t.Fatalf("expected snapshot version %d, got %d", registry.SnapshotVersion, snapshot.Version)
	}
	if len(snapshot.Servers) != 1 || snapshot.Servers[0].Name != "stewreads" {
		t.Fatalf("unexpected snapshot servers: %+v", snapshot.Servers)
	}
	if len(snapshot.Rings) != 0 {
		t.Fatalf("unexpected snapshot rings: %+v", snapshot.Rings)
	}
	if len(snapshot.Skills) != 0 {
		t.Fatalf("unexpected snapshot skills: %+v", snapshot.Skills)
	}
}

func TestRunWithStoreExportFile(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("setup ring failed: %s", result.stderr)
	}

	path := filepath.Join(t.TempDir(), "snapshot.json")
	result := runCmd(store, "export", "--file", path)
	if result.code != 0 {
		t.Fatalf("export --file failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "exported 1 server(s), 1 ring(s)") {
		t.Fatalf("expected export summary output, got: %s", result.stdout)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	snapshot, err := registry.ParseSnapshotJSON(payload)
	if err != nil {
		t.Fatalf("parse export file payload: %v", err)
	}
	if len(snapshot.Servers) != 1 || snapshot.Servers[0].Name != "stewreads" {
		t.Fatalf("unexpected snapshot servers: %+v", snapshot.Servers)
	}
	if len(snapshot.Rings) != 1 || snapshot.Rings[0].Name != "research" {
		t.Fatalf("unexpected snapshot rings: %+v", snapshot.Rings)
	}
}

func TestRunWithStoreImportDryRunAndApply(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "alpha", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	snapshot := registry.Snapshot{
		Version: registry.SnapshotVersion,
		Servers: []registry.Manifest{
			{
				Name:    "alpha",
				Command: "/bin/echo",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
			{
				Name:    "beta",
				Command: "/usr/bin/env",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
		},
		Rings: []registry.Ring{
			{
				Name:    "research",
				Members: []string{"alpha", "beta"},
			},
		},
		Skills: []registry.SnapshotSkill{
			{
				Name:        "release",
				Description: "Release workflow",
				Content:     "# Release\n",
			},
		},
	}
	payload, err := registry.MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot payload: %v", err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write snapshot fixture: %v", err)
	}

	dryRun := runCmd(store, "import", "--file", path)
	if dryRun.code != 0 {
		t.Fatalf("import dry-run failed: %s", dryRun.stderr)
	}
	if !strings.Contains(dryRun.stdout, "mode: dry-run") {
		t.Fatalf("expected dry-run output, got: %s", dryRun.stdout)
	}
	if !strings.Contains(dryRun.stdout, "added: beta") || !strings.Contains(dryRun.stdout, "updated: alpha") {
		t.Fatalf("expected dry-run diff output, got: %s", dryRun.stdout)
	}
	if !strings.Contains(dryRun.stdout, "rings added: research") {
		t.Fatalf("expected dry-run ring diff output, got: %s", dryRun.stdout)
	}
	if !strings.Contains(dryRun.stdout, "skills added: release") {
		t.Fatalf("expected dry-run skill diff output, got: %s", dryRun.stdout)
	}
	alphaAfterDryRun, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after dry-run: %v", err)
	}
	if alphaAfterDryRun.Command != commandPath {
		t.Fatalf("expected dry-run to preserve alpha command, got: %q", alphaAfterDryRun.Command)
	}
	if _, err := store.Get("beta"); err == nil {
		t.Fatalf("expected dry-run not to create beta")
	}
	if _, err := store.GetRing("research"); !errors.Is(err, registry.ErrRingNotFound) {
		t.Fatalf("expected dry-run not to create research ring, got: %v", err)
	}
	if _, err := store.GetSkill("release"); !errors.Is(err, registry.ErrSkillNotFound) {
		t.Fatalf("expected dry-run not to create release skill, got: %v", err)
	}

	apply := runCmd(store, "import", "--file", path, "--apply")
	if apply.code != 0 {
		t.Fatalf("import apply failed: %s", apply.stderr)
	}
	if !strings.Contains(apply.stdout, "mode: applied") {
		t.Fatalf("expected apply output, got: %s", apply.stdout)
	}
	alphaAfterApply, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after apply: %v", err)
	}
	if alphaAfterApply.Command != "/bin/echo" {
		t.Fatalf("expected alpha command update, got: %q", alphaAfterApply.Command)
	}
	if _, err := store.Get("beta"); err != nil {
		t.Fatalf("expected beta after apply: %v", err)
	}
	if _, err := store.GetRing("research"); err != nil {
		t.Fatalf("expected research ring after apply: %v", err)
	}
	if _, err := store.GetSkill("release"); err != nil {
		t.Fatalf("expected release skill after apply: %v", err)
	}
	releaseContent, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("expected release skill content after apply: %v", err)
	}
	if string(releaseContent) != "# Release\n" {
		t.Fatalf("unexpected release skill content: %q", releaseContent)
	}
}

func TestRunWithStoreClientsListsAllAdapters(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "clients")
	if result.code != 0 {
		t.Fatalf("clients expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "claude-desktop") {
		t.Fatalf("expected claude-desktop in clients output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "claude-code") {
		t.Fatalf("expected claude-code in clients output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "codex") {
		t.Fatalf("expected codex in clients output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "gemini") {
		t.Fatalf("expected gemini in clients output, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "vibe") {
		t.Fatalf("expected vibe in clients output, got: %s", result.stdout)
	}
}

func TestClientTargetRegistryDefinesCurrentCapabilities(t *testing.T) {
	wantTargets := []string{"claude-code", "claude-desktop", "codex", "gemini", "vibe"}
	if got := sortedClientTargetNames(); !reflect.DeepEqual(got, wantTargets) {
		t.Fatalf("unexpected client targets: got %#v want %#v", got, wantTargets)
	}
	if got := supportedSyncTargets(); !reflect.DeepEqual(got, wantTargets) {
		t.Fatalf("unexpected sync targets: got %#v want %#v", got, wantTargets)
	}
	if got := supportedRingRenderTargets(); !reflect.DeepEqual(got, wantTargets) {
		t.Fatalf("unexpected ring render targets: got %#v want %#v", got, wantTargets)
	}
	if got := userScopedSyncTargets(); !reflect.DeepEqual(got, []string{"claude-code", "gemini"}) {
		t.Fatalf("unexpected user-scoped targets: got %#v", got)
	}
	if got := supportedSkillTargets(); !reflect.DeepEqual(got, []string{"claude-code", "codex", "gemini", "vibe"}) {
		t.Fatalf("unexpected skill targets: got %#v", got)
	}
	if got := defaultInstallClientTarget(); got != "claude-desktop" {
		t.Fatalf("unexpected default install client target: %q", got)
	}
}

func TestRunWithStoreClientsShowsHeaderAndStatus(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "clients")
	if result.code != 0 {
		t.Fatalf("clients expected success, got code=%d stderr=%s", result.code, result.stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
	if len(lines) < 3 {
		// header + at least 2 adapter rows
		t.Fatalf("expected header + adapter rows, got: %s", result.stdout)
	}
	if !strings.HasPrefix(lines[0], "CLIENT") {
		t.Fatalf("expected header row first, got: %s", lines[0])
	}
	// Every non-header row must carry a bracketed status token.
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		// Detail lines (indented) are allowed to not have a status token.
		if strings.HasPrefix(line, "\t") {
			continue
		}
		if !strings.Contains(line, "[ready]") && !strings.Contains(line, "[warn]") && !strings.Contains(line, "[error]") {
			t.Fatalf("expected status token in client row, got: %s", line)
		}
	}
}

func TestRunWithStoreClientsRejectsPositionals(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "clients", "extra")
	if result.code == 0 {
		t.Fatalf("expected clients to fail with positional argument")
	}
	if !strings.Contains(result.stderr, "unexpected positional arguments") {
		t.Fatalf("expected positional argument error, got: %s", result.stderr)
	}
}

func TestRunWithStoreDoctorHealthy(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "claude-desktop="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "summary: total=1") || !strings.Contains(result.stdout, "ready=1") {
		t.Fatalf("unexpected doctor summary: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorReturnsErrorForInvalidConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "claude-desktop="+configPath)
	if result.code == 0 {
		t.Fatalf("doctor expected failure for invalid config")
	}
	if !strings.Contains(result.stderr, "doctor found") {
		t.Fatalf("expected doctor error summary in stderr, got: %s", result.stderr)
	}
}

func TestRunWithStoreDoctorReturnsErrorForInvalidClaudeCodeConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	claudeCodeConfigPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(claudeCodeConfigPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write invalid Claude Code config fixture: %v", err)
	}

	result := runCmd(
		store,
		"doctor",
		"--client-config", "claude-code="+claudeCodeConfigPath,
	)
	if result.code == 0 {
		t.Fatalf("doctor expected failure for invalid Claude Code config")
	}
	if !strings.Contains(result.stderr, "doctor found") {
		t.Fatalf("expected doctor error summary in stderr, got: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "claude-code config:") {
		t.Fatalf("expected doctor output to include claude-code config details, got: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorAndStatusInspectGeminiConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "gemini"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write Gemini config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "gemini="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "gemini config:") {
		t.Fatalf("expected doctor output to include gemini config details, got: %s", result.stdout)
	}

	result = runCmd(store, "status", "--client-config", "gemini="+configPath)
	if result.code != 0 {
		t.Fatalf("status expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "gemini-config: ready") {
		t.Fatalf("expected status output to include gemini config readiness, got: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorAndStatusInspectCodexConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "codex"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[mcp_servers]\n"), 0o644); err != nil {
		t.Fatalf("write Codex config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "codex="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "codex config:") {
		t.Fatalf("expected doctor output to include codex config details, got: %s", result.stdout)
	}

	result = runCmd(store, "status", "--client-config", "codex="+configPath)
	if result.code != 0 {
		t.Fatalf("status expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "codex-config: ready") {
		t.Fatalf("expected status output to include codex config readiness, got: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorAndStatusInspectVibeConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "vibe"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[[mcp_servers]]\nname = \"weather\"\ntransport = \"http\"\nurl = \"http://localhost:8000\"\n"), 0o644); err != nil {
		t.Fatalf("write Vibe config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "vibe="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "vibe config:") {
		t.Fatalf("expected doctor output to include vibe config details, got: %s", result.stdout)
	}

	result = runCmd(store, "status", "--client-config", "vibe="+configPath)
	if result.code != 0 {
		t.Fatalf("status expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "vibe-config: ready") {
		t.Fatalf("expected status output to include vibe config readiness, got: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorChecksVibeTarget(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "portable", "--command", commandPath,
		"--client", "vibe",
		"--required-env", "PORTABLE_TOKEN"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("mcp_servers = []\n"), 0o644); err != nil {
		t.Fatalf("write Vibe config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--client-config", "vibe="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor with Vibe warning should exit 0, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "portable [warn]") || !strings.Contains(result.stdout, "missing required env key PORTABLE_TOKEN") {
		t.Fatalf("expected Vibe target to receive server diagnostics, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "summary: total=1 ready=0 warn=1 error=0 skipped=0") {
		t.Fatalf("expected Vibe target not to be skipped, got: %s", result.stdout)
	}
}

func TestRunWithStoreStatusHealthy(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "status", "--client-config", "claude-desktop="+configPath)
	if result.code != 0 {
		t.Fatalf("status expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "madari: total=1") {
		t.Fatalf("expected concise status summary, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "hint: run `madari doctor`") {
		t.Fatalf("expected doctor hint, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "`madari clients`") {
		t.Fatalf("expected clients hint, got: %s", result.stdout)
	}
}

func TestRunWithStoreListAndStatusShowManagedSources(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	result := runCmd(store, "list")
	if result.code != 0 {
		t.Fatalf("list failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "SOURCES") {
		t.Fatalf("expected SOURCES column header, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "stewreads\tenabled\t"+commandPath+"\tclaude-desktop\t-") {
		t.Fatalf("expected unsynced server to show '-' sources, got: %s", result.stdout)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if result := runCmd(store, "sync", "claude-desktop", "--config-path", configPath); result.code != 0 {
		t.Fatalf("sync failed: %s", result.stderr)
	}

	result = runCmd(store, "list")
	if result.code != 0 {
		t.Fatalf("list after sync failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "stewreads\tenabled\t"+commandPath+"\tclaude-desktop\tstandalone") {
		t.Fatalf("expected synced server to show standalone source, got: %s", result.stdout)
	}

	result = runCmd(store, "status", "--client-config", "claude-desktop="+configPath)
	if result.code != 0 {
		t.Fatalf("status failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "claude-desktop-managed: entries=1 sources=standalone") {
		t.Fatalf("expected managed summary line, got: %s", result.stdout)
	}
}

func decodeJSONObject(t *testing.T, payload string) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("expected valid JSON on stdout, got error %v; stdout=%s", err, payload)
	}
	return decoded
}

func assertJSONKeys(t *testing.T, payload map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(payload))
	for key := range payload {
		got = append(got, key)
	}
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("JSON schema drift: want keys %v, got %v", sorted, got)
	}
}

func TestRunWithStoreListJSONEmpty(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "list", "--json")
	if result.code != 0 {
		t.Fatalf("list --json failed: %s", result.stderr)
	}

	expected := `{
  "schema_version": 1,
  "command": "list",
  "servers": []
}
`
	if result.stdout != expected {
		t.Fatalf("list --json schema drift:\nwant:\n%s\ngot:\n%s", expected, result.stdout)
	}
}

func TestRunWithStoreListJSON(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if result := runCmd(store, "sync", "claude-desktop", "--config-path", configPath); result.code != 0 {
		t.Fatalf("sync failed: %s", result.stderr)
	}

	result := runCmd(store, "list", "--json")
	if result.code != 0 {
		t.Fatalf("list --json failed: %s", result.stderr)
	}

	expected := fmt.Sprintf(`{
  "schema_version": 1,
  "command": "list",
  "servers": [
    {
      "name": "stewreads",
      "enabled": true,
      "command": %q,
      "clients": [
        "claude-desktop"
      ],
      "sources": [
        "standalone"
      ]
    }
  ]
}
`, commandPath)
	if result.stdout != expected {
		t.Fatalf("list --json schema drift:\nwant:\n%s\ngot:\n%s", expected, result.stdout)
	}
}

func TestRunWithStoreSyncDryRunJSON(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result := runCmd(store, "sync", "claude-code", "--dry-run", "--json", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("sync --dry-run --json failed: %s", result.stderr)
	}

	expected := fmt.Sprintf(`{
  "schema_version": 1,
  "command": "sync",
  "target": "claude-code",
  "config_path": %q,
  "dry_run": true,
  "added": [
    "stewreads"
  ],
  "updated": [],
  "removed": [],
  "unchanged": [],
  "skipped": [],
  "refused": []
}
`, configPath)
	if result.stdout != expected {
		t.Fatalf("sync --json schema drift:\nwant:\n%s\ngot:\n%s", expected, result.stdout)
	}
}

func TestRunWithStoreSyncJSONRequiresDryRun(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "sync", "claude-code", "--json")
	if result.code == 0 {
		t.Fatalf("expected sync --json without --dry-run to fail")
	}
	if !strings.Contains(result.stderr, "--json requires --dry-run") {
		t.Fatalf("expected dry-run requirement error, got: %s", result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("expected no stdout on input error, got: %s", result.stdout)
	}
}

func TestRunWithStoreStatusJSON(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if result := runCmd(store, "sync", "claude-desktop", "--config-path", configPath); result.code != 0 {
		t.Fatalf("sync failed: %s", result.stderr)
	}

	result := runCmd(store, "status", "--json", "--client-config", "claude-desktop="+configPath)
	if result.code != 0 {
		t.Fatalf("status --json failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}

	payload := decodeJSONObject(t, result.stdout)
	assertJSONKeys(t, payload, "schema_version", "command", "summary", "client_configs", "managed", "manifest_errors", "drift")
	if payload["schema_version"].(float64) != 1 {
		t.Fatalf("unexpected schema_version: %v", payload["schema_version"])
	}
	if payload["command"].(string) != "status" {
		t.Fatalf("unexpected command: %v", payload["command"])
	}

	summary := payload["summary"].(map[string]any)
	assertJSONKeys(t, summary, "total", "ready", "warning", "error", "skipped")
	if summary["total"].(float64) != 1 || summary["ready"].(float64) != 1 {
		t.Fatalf("unexpected summary: %v", summary)
	}

	configs := payload["client_configs"].([]any)
	if len(configs) == 0 {
		t.Fatalf("expected client config entries, got none")
	}
	assertJSONKeys(t, configs[0].(map[string]any), "target", "status")

	managed := payload["managed"].([]any)
	var desktop map[string]any
	for _, item := range managed {
		entry := item.(map[string]any)
		assertJSONKeys(t, entry, "target", "scope", "entries", "sources")
		if entry["target"] == "claude-desktop" {
			desktop = entry
		}
	}
	if desktop == nil {
		t.Fatalf("expected claude-desktop managed summary, got: %v", managed)
	}
	if desktop["entries"].(float64) != 1 {
		t.Fatalf("expected one managed entry for claude-desktop, got: %v", desktop)
	}
	sources := desktop["sources"].([]any)
	if len(sources) != 1 || sources[0] != "standalone" {
		t.Fatalf("expected standalone source, got: %v", sources)
	}
}

func TestRunWithStoreDoctorJSONReportsErrorsAndExitCode(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	result := runCmd(store, "doctor", "--json", "--client-config", "claude-code="+configPath)
	if result.code == 0 {
		t.Fatalf("expected doctor to exit non-zero for invalid config")
	}
	if !strings.Contains(result.stderr, "doctor found") {
		t.Fatalf("expected error summary on stderr, got: %s", result.stderr)
	}

	payload := decodeJSONObject(t, result.stdout)
	assertJSONKeys(t, payload, "schema_version", "command", "servers_dir", "servers", "manifest_errors", "client_configs", "drift", "ring_issues", "summary")
	if payload["command"].(string) != "doctor" {
		t.Fatalf("unexpected command: %v", payload["command"])
	}

	summary := payload["summary"].(map[string]any)
	if summary["error"].(float64) < 1 {
		t.Fatalf("expected at least one error in summary, got: %v", summary)
	}

	servers := payload["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("expected one server entry, got: %v", servers)
	}
	assertJSONKeys(t, servers[0].(map[string]any), "name", "enabled", "clients", "command", "status", "issues")

	configs := payload["client_configs"].([]any)
	if len(configs) == 0 {
		t.Fatalf("expected client config entries, got none")
	}
	assertJSONKeys(t, configs[0].(map[string]any), "target", "path", "exists", "status", "message")
}

func TestRunWithStoreSyncScopeValidation(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "sync", "claude-desktop", "--scope", "user")
	if result.code == 0 {
		t.Fatalf("expected --scope on claude-desktop to fail")
	}
	if !strings.Contains(result.stderr, "--scope is only supported for claude-code, gemini") {
		t.Fatalf("expected scope support error, got: %s", result.stderr)
	}

	result = runCmd(store, "sync", "claude-code", "--scope", "global")
	if result.code == 0 {
		t.Fatalf("expected unknown scope to fail")
	}
	if !strings.Contains(result.stderr, "unknown scope") {
		t.Fatalf("expected unknown-scope error, got: %s", result.stderr)
	}
}

func TestRunWithStoreSecretEnvPlacementFlow(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "vault", "--command", commandPath, "--client", "claude-code",
		"--env", "VAULT_TOKEN=shhh", "--secret-env", "VAULT_TOKEN"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	projectConfig := filepath.Join(t.TempDir(), ".mcp.json")
	result := runCmd(store, "sync", "claude-code", "--config-path", projectConfig)
	if result.code != 0 {
		t.Fatalf("expected per-entry refusal to keep sync successful, stderr=%s", result.stderr)
	}
	if !strings.Contains(result.stdout, "refused: vault") {
		t.Fatalf("expected refused summary line, got: %s", result.stdout)
	}
	if !strings.Contains(result.stderr, "refused vault") || !strings.Contains(result.stderr, "--scope user") {
		t.Fatalf("expected refusal warning with user-scope guidance, got: %s", result.stderr)
	}
	payload, err := os.ReadFile(projectConfig)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if strings.Contains(string(payload), "shhh") {
		t.Fatalf("expected secret value to stay out of repo-scoped config, got: %s", payload)
	}

	userConfig := filepath.Join(t.TempDir(), "claude.json")
	result = runCmd(store, "sync", "claude-code", "--scope", "user", "--config-path", userConfig)
	if result.code != 0 {
		t.Fatalf("user-scope sync failed: %s", result.stderr)
	}

	result = runCmd(store, "list")
	if result.code != 0 {
		t.Fatalf("list failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "vault\tenabled\t"+commandPath+"\tclaude-code\tstandalone") {
		t.Fatalf("expected user-scope managed sources in list, got: %s", result.stdout)
	}

	// status counts user-scope managed entries (no exit-code assertion:
	// user-scope drift inspects the machine's real user config).
	result = runCmd(store, "status", "--client-config", "claude-code="+projectConfig)
	if !strings.Contains(result.stdout, "claude-code-user-managed: entries=1 sources=standalone") {
		t.Fatalf("expected user-scope managed summary in status, got: %s", result.stdout)
	}
}

func TestRunWithStoreStatusReportsDrift(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "driftee", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if result := runCmd(store, "sync", "claude-code", "--config-path", configPath); result.code != 0 {
		t.Fatalf("sync failed: %s", result.stderr)
	}

	// No drift right after sync.
	result := runCmd(store, "status", "--client-config", "claude-code="+configPath)
	if result.code != 0 {
		t.Fatalf("status failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if strings.Contains(result.stdout, "-drift:") {
		t.Fatalf("expected no drift line right after sync, got: %s", result.stdout)
	}

	// Disabling the server orphans its materialized entry.
	if result := runCmd(store, "disable", "driftee"); result.code != 0 {
		t.Fatalf("disable failed: %s", result.stderr)
	}

	result = runCmd(store, "status", "--client-config", "claude-code="+configPath)
	if result.code != 0 {
		t.Fatalf("status with drift should stay exit 0 (warning), got code=%d stderr=%s", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "claude-code-drift: stale=0 missing=0 orphaned=1 (fix: madari sync claude-code)") {
		t.Fatalf("expected drift line with fix hint, got: %s", result.stdout)
	}

	// Doctor JSON carries the same drift report.
	result = runCmd(store, "doctor", "--json", "--client-config", "claude-code="+configPath)
	if result.code != 0 {
		t.Fatalf("doctor --json failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	payload := decodeJSONObject(t, result.stdout)
	drift := payload["drift"].([]any)
	if len(drift) != 1 {
		t.Fatalf("expected one drift entry, got: %v", drift)
	}
	entry := drift[0].(map[string]any)
	assertJSONKeys(t, entry, "target", "scope", "config_path", "status", "stale", "missing", "orphaned", "issue")
	if entry["target"] != "claude-code" || entry["status"] != "warn" {
		t.Fatalf("unexpected drift entry: %v", entry)
	}
	orphaned := entry["orphaned"].([]any)
	if len(orphaned) != 1 || orphaned[0] != "driftee" {
		t.Fatalf("expected driftee orphaned, got: %v", orphaned)
	}
}

func TestRunWithStoreStatusReturnsErrorForInvalidConfig(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	result := runCmd(store, "status", "--client-config", "claude-desktop="+configPath)
	if result.code == 0 {
		t.Fatalf("status expected failure for invalid config")
	}
	if !strings.Contains(result.stderr, "status found") {
		t.Fatalf("expected status error summary in stderr, got: %s", result.stderr)
	}
}

func TestRunWithStoreStatusShowsClaudeCodeConfigWhenTargetPresent(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}

	claudeCodeConfigPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(claudeCodeConfigPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write Claude Code config fixture: %v", err)
	}

	result := runCmd(
		store,
		"status",
		"--client-config", "claude-code="+claudeCodeConfigPath,
	)
	if result.code != 0 {
		t.Fatalf("status expected success, got code=%d stderr=%s stdout=%s", result.code, result.stderr, result.stdout)
	}
	if !strings.Contains(result.stdout, "claude-code-config: ready") {
		t.Fatalf("expected status output to include claude-code-config readiness, got: %s", result.stdout)
	}
}

func TestRunHelpMentionsConfigDefaults(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected help to succeed, got code=%d stderr=%s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Default config directory:") {
		t.Fatalf("expected help output to mention default config directory, got: %s", output)
	}
	if !strings.Contains(output, "Default servers directory:") {
		t.Fatalf("expected help output to mention default servers directory, got: %s", output)
	}
	if !strings.Contains(output, "MADARI_CONFIG_DIR") {
		t.Fatalf("expected help output to mention MADARI_CONFIG_DIR override, got: %s", output)
	}
}

func TestRunVersionCommandsUseDefaultBuildVersion(t *testing.T) {
	tests := [][]string{
		{"version"},
		{"--version"},
		{"-v"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := run(args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected version command to succeed, got code=%d stderr=%s", code, stderr.String())
			}
			if got := strings.TrimSpace(stdout.String()); got != "0.0.0-dev" {
				t.Fatalf("expected default build version, got %q", got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %s", stderr.String())
			}
		})
	}
}

func TestDeriveServerName(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		expected    string
	}{
		{
			name:        "strips mcp suffix",
			packageName: "stewreads-mcp",
			expected:    "stewreads",
		},
		{
			name:        "preserves dots",
			packageName: "awslabs.core-mcp-server",
			expected:    "awslabs.core-mcp-server",
		},
		{
			name:        "normalizes underscores and preserves dots",
			packageName: "foo_bar.baz",
			expected:    "foo-bar.baz",
		},
		{
			name:        "uses final slash segment",
			packageName: "@modelcontextprotocol/server-sequential-thinking",
			expected:    "server-sequential-thinking",
		},
		{
			name:        "collapses repeated separators",
			packageName: "foo...__bar",
			expected:    "foo.bar",
		},
		{
			name:        "returns empty when no valid characters",
			packageName: "._/@",
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := deriveServerName(tt.packageName)
			if actual != tt.expected {
				t.Fatalf("deriveServerName(%q) = %q, expected %q", tt.packageName, actual, tt.expected)
			}
		})
	}
}

func TestRunWithStoreRingCreate(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	for _, name := range []string{"stewreads", "arxiv"} {
		if result := runCmd(store, "add", name, "--command", commandPath, "--client", "claude-code"); result.code != 0 {
			t.Fatalf("setup add %s failed: %s", name, result.stderr)
		}
	}

	result := runCmd(store, "ring", "create", "research", "--member", "stewreads", "--member", "arxiv", "--description", "Research helpers")
	if result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "created ring research with 2 member(s)") {
		t.Fatalf("expected creation confirmation, got: %s", result.stdout)
	}

	// Duplicate refused.
	result = runCmd(store, "ring", "create", "research", "--member", "stewreads")
	if result.code == 0 || !strings.Contains(result.stderr, "already exists") {
		t.Fatalf("expected duplicate-ring error, got code=%d stderr=%s", result.code, result.stderr)
	}

	// Unknown member refused.
	result = runCmd(store, "ring", "create", "broken", "--member", "ghost")
	if result.code == 0 || !strings.Contains(result.stderr, "unknown servers: ghost") {
		t.Fatalf("expected unknown-member error, got code=%d stderr=%s", result.code, result.stderr)
	}

	// Member required.
	result = runCmd(store, "ring", "create", "empty")
	if result.code == 0 || !strings.Contains(result.stderr, "at least one member is required") {
		t.Fatalf("expected member-required error, got code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreRingDispatch(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "ring")
	if result.code == 0 || !strings.Contains(result.stderr, "usage: madari ring") {
		t.Fatalf("expected ring usage error, got code=%d stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "ring", "explode")
	if result.code == 0 || !strings.Contains(result.stderr, "unknown ring subcommand") {
		t.Fatalf("expected unknown-subcommand error, got code=%d stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "ring", "--help")
	if result.code != 0 || !strings.Contains(result.stdout, "madari ring <subcommand>") {
		t.Fatalf("expected ring help, got code=%d stdout=%s", result.code, result.stdout)
	}
}

func TestRunWithStoreRingListAndShow(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(store, "ring", "list")
	if result.code != 0 || !strings.Contains(result.stdout, "no rings configured") {
		t.Fatalf("expected empty ring list, got code=%d stdout=%s", result.code, result.stdout)
	}

	for _, name := range []string{"stewreads", "arxiv"} {
		if result := runCmd(store, "add", name, "--command", commandPath, "--client", "claude-code"); result.code != 0 {
			t.Fatalf("setup add %s failed: %s", name, result.stderr)
		}
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads", "--member", "arxiv", "--description", "Research helpers"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result = runCmd(store, "ring", "list")
	if result.code != 0 {
		t.Fatalf("ring list failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "NAME\tMEMBERS\tDESCRIPTION") {
		t.Fatalf("expected ring list header, got: %s", result.stdout)
	}
	if !strings.Contains(result.stdout, "research\tarxiv,stewreads\tResearch helpers") {
		t.Fatalf("expected ring row with sorted members, got: %s", result.stdout)
	}

	result = runCmd(store, "ring", "show", "research")
	if result.code != 0 {
		t.Fatalf("ring show failed: %s", result.stderr)
	}
	for _, want := range []string{"name: research", "description: Research helpers", "  - arxiv", "  - stewreads"} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("expected %q in ring show output, got: %s", want, result.stdout)
		}
	}

	result = runCmd(store, "ring", "show", "ghost")
	if result.code == 0 || !strings.Contains(result.stderr, `ring "ghost" not found`) {
		t.Fatalf("expected not-found error, got code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreRingDelete(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "ring", "delete", "research", "--bogus")
	if result.code == 0 || !strings.Contains(result.stderr, "flag provided but not defined") {
		t.Fatalf("expected unknown-flag error, got code=%d stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "ring", "delete")
	if result.code == 0 || !strings.Contains(result.stderr, "usage: madari ring delete <name>") {
		t.Fatalf("expected usage error, got code=%d stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "ring", "delete", "--help")
	if result.code != 0 || !strings.Contains(result.stdout, "madari ring delete <name>") {
		t.Fatalf("expected delete help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}

	result = runCmd(store, "ring", "delete", "ghost")
	if result.code == 0 || !strings.Contains(result.stderr, `ring "ghost" not found`) {
		t.Fatalf("expected not-found error, got code=%d stderr=%s", result.code, result.stderr)
	}

	result = runCmd(store, "ring", "delete", "research")
	if result.code != 0 {
		t.Fatalf("delete failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "deleted ring research") {
		t.Fatalf("expected delete confirmation, got: %s", result.stdout)
	}
	if _, err := store.GetRing("research"); !errors.Is(err, registry.ErrRingNotFound) {
		t.Fatalf("expected ring removed from registry, got: %v", err)
	}
}

func TestRunWithStoreRingDeleteRefusesAttachedScopes(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	projectConfig := filepath.Join(t.TempDir(), ".mcp.json")
	userConfig := filepath.Join(t.TempDir(), "claude.json")
	if result := runCmd(store, "ring", "attach", "research", "claude-code", "--config-path", projectConfig); result.code != 0 {
		t.Fatalf("project attach failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "attach", "research", "claude-code", "--scope", "user", "--config-path", userConfig); result.code != 0 {
		t.Fatalf("user attach failed: %s", result.stderr)
	}

	result := runCmd(store, "ring", "delete", "research")
	if result.code == 0 {
		t.Fatalf("expected attached delete to fail")
	}
	for _, want := range []string{
		`ring "research" is attached and cannot be deleted; detach it first:`,
		"claude-code: `madari ring detach research claude-code`",
		"claude-code (user scope): `madari ring detach research claude-code --scope user`",
		"pass --config-path if the ring was attached to a custom config",
	} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("expected %q in refusal, got: %s", want, result.stderr)
		}
	}
	if _, err := store.GetRing("research"); err != nil {
		t.Fatalf("delete refusal should leave ring file untouched: %v", err)
	}
}

func TestRunWithStoreRingListJSON(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	result := runCmd(store, "ring", "list", "--json")
	if result.code != 0 {
		t.Fatalf("ring list --json failed: %s", result.stderr)
	}
	expectedEmpty := `{
  "schema_version": 1,
  "command": "ring list",
  "rings": []
}
`
	if result.stdout != expectedEmpty {
		t.Fatalf("ring list --json schema drift:\nwant:\n%s\ngot:\n%s", expectedEmpty, result.stdout)
	}

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result = runCmd(store, "ring", "list", "--json")
	if result.code != 0 {
		t.Fatalf("ring list --json failed: %s", result.stderr)
	}
	expected := `{
  "schema_version": 1,
  "command": "ring list",
  "rings": [
    {
      "name": "research",
      "members": [
        "stewreads"
      ],
      "description": ""
    }
  ]
}
`
	if result.stdout != expected {
		t.Fatalf("ring list --json schema drift:\nwant:\n%s\ngot:\n%s", expected, result.stdout)
	}

	result = runCmd(store, "ring", "show", "research", "--json")
	if result.code != 0 {
		t.Fatalf("ring show --json failed: %s", result.stderr)
	}
	payload := decodeJSONObject(t, result.stdout)
	assertJSONKeys(t, payload, "schema_version", "command", "ring")
	if payload["command"] != "ring show" {
		t.Fatalf("unexpected command field: %v", payload["command"])
	}
	ring := payload["ring"].(map[string]any)
	assertJSONKeys(t, ring, "name", "members", "description")
	if ring["name"] != "research" {
		t.Fatalf("unexpected ring payload: %v", ring)
	}
}

func TestRunWithStoreRingAttachDetachFlow(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	for _, name := range []string{"stewreads", "arxiv"} {
		if result := runCmd(store, "add", name, "--command", commandPath, "--client", "claude-code"); result.code != 0 {
			t.Fatalf("setup add %s failed: %s", name, result.stderr)
		}
	}
	if result := runCmd(store, "ring", "create", "r1", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create r1 failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "r2", "--member", "stewreads", "--member", "arxiv"); result.code != 0 {
		t.Fatalf("ring create r2 failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")

	// Attach unknown ring fails.
	result := runCmd(store, "ring", "attach", "ghost", "claude-code", "--config-path", configPath)
	if result.code == 0 || !strings.Contains(result.stderr, `ring "ghost" not found`) {
		t.Fatalf("expected unknown-ring error, got code=%d stderr=%s", result.code, result.stderr)
	}

	// Attach both rings (overlapping member).
	result = runCmd(store, "ring", "attach", "r1", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach r1 failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "added: stewreads") {
		t.Fatalf("expected stewreads added, got: %s", result.stdout)
	}
	result = runCmd(store, "ring", "attach", "r2", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach r2 failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "added: arxiv") {
		t.Fatalf("expected arxiv added, got: %s", result.stdout)
	}

	// list shows ring sources.
	result = runCmd(store, "list")
	if !strings.Contains(result.stdout, "stewreads\tenabled\t"+commandPath+"\tclaude-code\tring:r1,ring:r2") {
		t.Fatalf("expected overlapping ring sources in list, got: %s", result.stdout)
	}

	// Detach r1: shared member survives via r2.
	result = runCmd(store, "ring", "detach", "r1", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("detach r1 failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "no changes") {
		t.Fatalf("expected no config changes while r2 owns shared member, got: %s", result.stdout)
	}

	// Detach r2: everything leaves the config.
	result = runCmd(store, "ring", "detach", "r2", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("detach r2 failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed: arxiv,stewreads") {
		t.Fatalf("expected both members removed, got: %s", result.stdout)
	}

	// Re-detach is a friendly no-op.
	result = runCmd(store, "ring", "detach", "r2", "claude-code", "--config-path", configPath)
	if result.code != 0 || !strings.Contains(result.stdout, "not attached") {
		t.Fatalf("expected no-op notice, got code=%d stdout=%s", result.code, result.stdout)
	}
}

func TestRunWithStoreRingAttachDetachGemini(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "gemini"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".gemini", "settings.json")
	result := runCmd(store, "ring", "attach", "research", "gemini", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "sync target: gemini") || !strings.Contains(result.stdout, "added: stewreads") {
		t.Fatalf("expected gemini attach output, got: %s", result.stdout)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Gemini config after attach: %v", err)
	}
	if !strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected attached ring member in Gemini config, got: %s", after)
	}

	result = runCmd(store, "ring", "detach", "research", "gemini", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("detach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed: stewreads") {
		t.Fatalf("expected gemini detach removal, got: %s", result.stdout)
	}
	after, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Gemini config after detach: %v", err)
	}
	if strings.Contains(string(after), "\"stewreads\"") {
		t.Fatalf("expected detached ring member removed from Gemini config, got: %s", after)
	}
}

func TestRunWithStoreRingAttachDetachCodex(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "codex"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	result := runCmd(store, "ring", "attach", "research", "codex", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "sync target: codex") || !strings.Contains(result.stdout, "added: stewreads") {
		t.Fatalf("expected codex attach output, got: %s", result.stdout)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Codex config after attach: %v", err)
	}
	if !strings.Contains(string(after), "[mcp_servers.stewreads]") {
		t.Fatalf("expected attached ring member in Codex config, got: %s", after)
	}

	result = runCmd(store, "ring", "detach", "research", "codex", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("detach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed: stewreads") {
		t.Fatalf("expected codex detach removal, got: %s", result.stdout)
	}
	after, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Codex config after detach: %v", err)
	}
	if strings.Contains(string(after), "[mcp_servers.stewreads]") {
		t.Fatalf("expected detached ring member removed from Codex config, got: %s", after)
	}
}

func TestRunWithStoreRingAttachDetachVibe(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "vibe"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	result := runCmd(store, "ring", "attach", "research", "vibe", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "sync target: vibe") || !strings.Contains(result.stdout, "added: stewreads") {
		t.Fatalf("expected vibe attach output, got: %s", result.stdout)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Vibe config after attach: %v", err)
	}
	if !strings.Contains(string(after), "stewreads") || !strings.Contains(string(after), "[[mcp_servers]]") {
		t.Fatalf("expected attached ring member in Vibe config, got: %s", after)
	}

	result = runCmd(store, "ring", "detach", "research", "vibe", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("detach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "removed: stewreads") {
		t.Fatalf("expected vibe detach removal, got: %s", result.stdout)
	}
	after, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Vibe config after detach: %v", err)
	}
	if strings.Contains(string(after), "stewreads") {
		t.Fatalf("expected detached ring member removed from Vibe config, got: %s", after)
	}
}

func TestRunWithStoreRingAttachWarnsDisabledMember(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "r1", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}
	if result := runCmd(store, "disable", "stewreads"); result.code != 0 {
		t.Fatalf("disable failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	result := runCmd(store, "ring", "attach", "r1", "claude-code", "--config-path", configPath)
	if result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}
	if !strings.Contains(result.stderr, "stewreads is disabled; ownership recorded") {
		t.Fatalf("expected disabled-member warning, got: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "no changes") {
		t.Fatalf("expected no materialization, got: %s", result.stdout)
	}
}

func TestRunWithStoreRingRender(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code",
		"--arg", "--stdio",
		"--env", "STEWREADS_CONFIG_PATH=~/.config/stewreads/config.toml",
		"--env", "STEWREADS_API_KEY=shhh",
		"--secret-env", "STEWREADS_API_KEY"); result.code != 0 {
		t.Fatalf("setup add stewreads failed: %s", result.stderr)
	}
	if result := runCmd(store, "add", "desktop-only", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add desktop-only failed: %s", result.stderr)
	}
	if result := runCmd(store, "add", "sleepy", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add sleepy failed: %s", result.stderr)
	}
	if result := runCmd(store, "disable", "sleepy"); result.code != 0 {
		t.Fatalf("disable failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research",
		"--member", "stewreads", "--member", "desktop-only", "--member", "sleepy"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "ring", "render", "research", "--client", "claude-code")
	if result.code != 0 {
		t.Fatalf("ring render failed: %s", result.stderr)
	}

	expected := fmt.Sprintf(`{
  "mcpServers": {
    "stewreads": {
      "command": %q,
      "args": [
        "--stdio"
      ],
      "env": {
        "STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml"
      }
    }
  }
}
`, commandPath)
	if result.stdout != expected {
		t.Fatalf("render output drift:\nwant:\n%s\ngot:\n%s", expected, result.stdout)
	}

	for _, want := range []string{
		"secret env values omitted (STEWREADS_API_KEY)",
		"sleepy is disabled; omitted",
		"desktop-only does not target claude-code; omitted",
	} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("expected warning %q, got stderr: %s", want, result.stderr)
		}
	}

	// Render mutates nothing: no state directory appears.
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.ServersDir()), "state")); !os.IsNotExist(err) {
		t.Fatalf("expected render to write no state, err=%v", err)
	}

	// Unknown ring and missing --client fail loudly.
	result = runCmd(store, "ring", "render", "ghost", "--client", "claude-code")
	if result.code == 0 || !strings.Contains(result.stderr, `ring "ghost" not found`) {
		t.Fatalf("expected not-found error, got code=%d stderr=%s", result.code, result.stderr)
	}
	result = runCmd(store, "ring", "render", "research")
	if result.code == 0 || !strings.Contains(result.stderr, "--client is required") {
		t.Fatalf("expected required-client error, got code=%d stderr=%s", result.code, result.stderr)
	}
	result = runCmd(store, "ring", "render", "research", "--client", "unknown-client")
	if result.code == 0 || !strings.Contains(result.stderr, `unsupported render target "unknown-client"`) || !strings.Contains(result.stderr, "codex") || !strings.Contains(result.stderr, "vibe") {
		t.Fatalf("expected unsupported render-target error, got code=%d stderr=%s", result.code, result.stderr)
	}
}

func TestRunWithStoreRingRenderJSONTargets(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "portable", "--command", commandPath,
		"--client", "claude-desktop",
		"--client", "gemini",
		"--arg", "--stdio",
		"--env", "PORTABLE_MODE=1"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "portable-ring", "--member", "portable"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	expected := map[string]map[string]renderedServer{
		"mcpServers": {
			"portable": {
				Command: commandPath,
				Args:    []string{"--stdio"},
				Env:     map[string]string{"PORTABLE_MODE": "1"},
			},
		},
	}
	for _, target := range []string{"claude-desktop", "gemini"} {
		result := runCmd(store, "ring", "render", "portable-ring", "--client", target)
		if result.code != 0 {
			t.Fatalf("ring render %s failed: %s", target, result.stderr)
		}
		if result.stderr != "" {
			t.Fatalf("expected no render warnings for %s, got: %s", target, result.stderr)
		}
		var got map[string]map[string]renderedServer
		if err := json.Unmarshal([]byte(result.stdout), &got); err != nil {
			t.Fatalf("parse render output for %s: %v\n%s", target, err, result.stdout)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("render output for %s drift:\nwant: %#v\ngot: %#v", target, expected, got)
		}
	}
}

func TestRunWithStoreRingRenderTOMLTargets(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "portable", "--command", commandPath,
		"--client", "codex",
		"--client", "vibe",
		"--arg", "--stdio",
		"--env", "PORTABLE_MODE=1",
		"--required-env", "PORTABLE_TOKEN",
		"--secret-env", "PORTABLE_SECRET"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "portable-ring", "--member", "portable"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	tests := []struct {
		target string
		want   string
	}{
		{
			target: "codex",
			want: fmt.Sprintf(`[mcp_servers.portable]
command = %s
args = ["--stdio"]
env_vars = ["PORTABLE_SECRET", "PORTABLE_TOKEN"]

[mcp_servers.portable.env]
PORTABLE_MODE = "1"
`, tomlString(commandPath)),
		},
		{
			target: "vibe",
			want: fmt.Sprintf(`[[mcp_servers]]
name = "portable"
transport = "stdio"
command = %s
args = ["--stdio"]
env = { PORTABLE_MODE = "1" }
`, tomlString(commandPath)),
		},
	}

	for _, tt := range tests {
		result := runCmd(store, "ring", "render", "portable-ring", "--client", tt.target)
		if result.code != 0 {
			t.Fatalf("ring render %s failed: %s", tt.target, result.stderr)
		}
		if result.stderr != "" {
			t.Fatalf("expected no render warnings for %s, got: %s", tt.target, result.stderr)
		}
		if result.stdout != tt.want {
			t.Fatalf("render output for %s drift:\nwant:\n%s\ngot:\n%s", tt.target, tt.want, result.stdout)
		}
	}
}

func TestRunWithStoreRingRenderCodexEnvVarsForRuntimeEnv(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "vault", "--command", commandPath,
		"--client", "codex",
		"--required-env", "VAULT_ACCOUNT",
		"--secret-env", "VAULT_TOKEN",
		"--env", "VAULT_TOKEN=shhh"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "vault-ring", "--member", "vault"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "ring", "render", "vault-ring", "--client", "codex")
	if result.code != 0 {
		t.Fatalf("ring render codex failed: %s", result.stderr)
	}
	want := fmt.Sprintf(`[mcp_servers.vault]
command = %s
env_vars = ["VAULT_ACCOUNT", "VAULT_TOKEN"]
`, tomlString(commandPath))
	if result.stdout != want {
		t.Fatalf("render output for codex drift:\nwant:\n%s\ngot:\n%s", want, result.stdout)
	}
	if strings.Contains(result.stdout, "shhh") {
		t.Fatalf("expected static secret value to stay out of codex render output, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stderr, "secret env values omitted (VAULT_TOKEN)") {
		t.Fatalf("expected secret omission warning, got: %s", result.stderr)
	}
}

func TestRunWithStoreRingStatus(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	for _, name := range []string{"stewreads", "arxiv"} {
		if result := runCmd(store, "add", name, "--command", commandPath, "--client", "claude-code"); result.code != 0 {
			t.Fatalf("setup add %s failed: %s", name, result.stderr)
		}
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads", "--member", "arxiv"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	// Nothing attached yet.
	result := runCmd(store, "ring", "status")
	if result.code != 0 || !strings.Contains(result.stdout, "claude-code: no managed entries") {
		t.Fatalf("expected empty status, got code=%d stdout=%s", result.code, result.stdout)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if result := runCmd(store, "ring", "attach", "research", "claude-code", "--config-path", configPath); result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}

	result = runCmd(store, "ring", "status")
	if result.code != 0 {
		t.Fatalf("ring status failed: %s", result.stderr)
	}
	for _, want := range []string{
		"claude-code:",
		"research [ok] members=2 owned=2",
		"arxiv: ring:research",
		"stewreads: ring:research",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("expected %q in status output, got: %s", want, result.stdout)
		}
	}

	// JSON shape.
	result = runCmd(store, "ring", "status", "--json")
	if result.code != 0 {
		t.Fatalf("ring status --json failed: %s", result.stderr)
	}
	payload := decodeJSONObject(t, result.stdout)
	assertJSONKeys(t, payload, "schema_version", "command", "targets")
	if payload["command"] != "ring status" {
		t.Fatalf("unexpected command: %v", payload["command"])
	}
	targets := payload["targets"].([]any)
	var ccode map[string]any
	for _, item := range targets {
		entry := item.(map[string]any)
		assertJSONKeys(t, entry, "target", "scope", "rings", "servers")
		if entry["target"] == "claude-code" && entry["scope"] == "default" {
			ccode = entry
		}
	}
	if ccode == nil {
		t.Fatalf("expected claude-code default target, got: %v", targets)
	}
	rings := ccode["rings"].([]any)
	if len(rings) != 1 {
		t.Fatalf("expected one attached ring, got: %v", rings)
	}
	ring := rings[0].(map[string]any)
	assertJSONKeys(t, ring, "name", "exists", "members", "owned", "pending", "stale", "missing_members")
	if ring["name"] != "research" || ring["exists"] != true {
		t.Fatalf("unexpected ring attachment: %v", ring)
	}

	// Delete the ring file out from under the attachment: status flags it,
	// doctor reports an error-level ring issue.
	if err := os.Remove(filepath.Join(store.RingsDir(), "research.toml")); err != nil {
		t.Fatalf("remove ring file: %v", err)
	}

	result = runCmd(store, "ring", "status")
	if result.code != 0 {
		t.Fatalf("ring status failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "research [missing] ring file not found; release with `madari ring detach research claude-code`") {
		t.Fatalf("expected missing-ring flag with detach guidance, got: %s", result.stdout)
	}

	result = runCmd(store, "doctor", "--json", "--client-config", "claude-code="+configPath)
	if result.code == 0 {
		t.Fatalf("expected doctor to exit non-zero for missing ring file")
	}
	payload = decodeJSONObject(t, result.stdout)
	issues := payload["ring_issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("expected one ring issue, got: %v", issues)
	}
	issue := issues[0].(map[string]any)
	assertJSONKeys(t, issue, "target", "scope", "ring", "severity", "message")
	if issue["ring"] != "research" || issue["severity"] != "error" {
		t.Fatalf("unexpected ring issue: %v", issue)
	}

	// Detach by name still works and clears the issue.
	if result := runCmd(store, "ring", "detach", "research", "claude-code", "--config-path", configPath); result.code != 0 {
		t.Fatalf("detach failed: %s", result.stderr)
	}
	result = runCmd(store, "ring", "status")
	if !strings.Contains(result.stdout, "claude-code: no managed entries") {
		t.Fatalf("expected clean status after detach, got: %s", result.stdout)
	}
}

func TestRunWithStoreDoctorFlagsDanglingRingMember(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}
	// Remove the member manifest after ring creation.
	if result := runCmd(store, "remove", "stewreads"); result.code != 0 {
		t.Fatalf("remove failed: %s", result.stderr)
	}

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	result := runCmd(store, "doctor", "--client-config", "claude-code="+configPath)
	if !strings.Contains(result.stdout, "ring research: [warn] member stewreads no longer exists in the registry") {
		t.Fatalf("expected dangling-member warning, got: %s", result.stdout)
	}
}

func TestRunWithStoreRingStatusFlagsStaleAndScopedPending(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	for _, name := range []string{"stewreads", "arxiv"} {
		if result := runCmd(store, "add", name, "--command", commandPath, "--client", "claude-code"); result.code != 0 {
			t.Fatalf("setup add %s failed: %s", name, result.stderr)
		}
	}
	if result := runCmd(store, "ring", "create", "research", "--member", "stewreads"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	// Attach in USER scope with a custom config path.
	configPath := filepath.Join(t.TempDir(), "claude.json")
	if result := runCmd(store, "ring", "attach", "research", "claude-code", "--scope", "user", "--config-path", configPath); result.code != 0 {
		t.Fatalf("attach failed: %s", result.stderr)
	}

	// Edit the ring: stewreads removed (becomes a stale owner), arxiv added
	// (becomes pending until the next sync).
	edited := []byte("name = \"research\"\nmembers = [\"arxiv\"]\n")
	if err := os.WriteFile(filepath.Join(store.RingsDir(), "research.toml"), edited, 0o644); err != nil {
		t.Fatalf("rewrite ring file: %v", err)
	}

	result := runCmd(store, "ring", "status")
	if result.code != 0 {
		t.Fatalf("ring status failed: %s", result.stderr)
	}
	if !strings.Contains(result.stdout, "research [out-of-sync] members=1 owned=0 pending=arxiv stale=stewreads (run `madari sync claude-code --scope user`; pass --config-path if attached to a custom config)") {
		t.Fatalf("expected out-of-sync line with scoped hint, got: %s", result.stdout)
	}

	// JSON carries the stale owners too.
	result = runCmd(store, "ring", "status", "--json")
	if result.code != 0 {
		t.Fatalf("ring status --json failed: %s", result.stderr)
	}
	payload := decodeJSONObject(t, result.stdout)
	for _, item := range payload["targets"].([]any) {
		entry := item.(map[string]any)
		if entry["target"] != "claude-code" || entry["scope"] != "user" {
			continue
		}
		rings := entry["rings"].([]any)
		if len(rings) != 1 {
			t.Fatalf("expected one ring, got: %v", rings)
		}
		ring := rings[0].(map[string]any)
		stale := ring["stale"].([]any)
		if len(stale) != 1 || stale[0] != "stewreads" {
			t.Fatalf("expected stewreads stale, got: %v", ring)
		}
		pending := ring["pending"].([]any)
		if len(pending) != 1 || pending[0] != "arxiv" {
			t.Fatalf("expected arxiv pending, got: %v", ring)
		}
		return
	}
	t.Fatalf("expected claude-code user target in: %s", result.stdout)
}

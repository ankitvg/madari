package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/proctree"
	"github.com/ankitvg/madari/internal/registry"
)

type runExecutor func(context.Context, cliApp, runLaunchPlan) (proctree.Result, error)

func runCodex(ctx context.Context, a cliApp, plan runLaunchPlan) (proctree.Result, error) {
	if plan.Artifact == nil {
		return proctree.Result{}, fmt.Errorf("immutable Codex launch artifact is missing")
	}
	// This host-level safety gate can only block a compiled artifact. It never
	// rereads or adds registry capabilities, and it prevents a newly installed
	// system skill from silently broadening the pending run.
	if err := validateCodexRunPlan(); err != nil {
		return proctree.Result{}, err
	}
	if err := plan.Artifact.VerifyClientBinary(); err != nil {
		return proctree.Result{}, err
	}

	runRoot, err := os.MkdirTemp("", "madari-codex-run-*")
	if err != nil {
		return proctree.Result{}, fmt.Errorf("create isolated codex run directory: %w", err)
	}
	defer os.RemoveAll(runRoot)

	prepared, err := plan.Artifact.Prepare(runRoot)
	if err != nil {
		return proctree.Result{}, err
	}
	if _, err := materializeRunSkills(plan.Artifact, runRoot); err != nil {
		return proctree.Result{}, err
	}

	args := []string{"exec", "--strict-config", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", plan.Artifact.Execution().Sandbox, "--cd", runRoot}
	for _, override := range prepared.CodexOverrides() {
		args = append(args, "-c", override)
	}
	args = append(args, "--", plan.Artifact.Prompt())

	cmd := exec.Command(plan.Artifact.ClientPath(), args...)
	cmd.Dir = runRoot
	cmd.Env = prepared.Env()
	cmd.Stdout = a.stdout
	cmd.Stderr = a.stderr
	result, err := proctree.Run(ctx, cmd, plan.Artifact.MaxDuration())
	if err != nil {
		return result, fmt.Errorf("run codex exec: %w", err)
	}
	return result, nil
}

var codexAdminSkillRoots = []string{
	"/etc/codex/skills",
	"/opt/codex/skills",
}

func validateCodexRunPlan() error {
	return ensureCodexRunNoAdminSkillRoots(codexAdminSkillRoots)
}

func codexRunSourceHome() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome != "" {
		expanded, err := syncshared.ExpandHome(codexHome)
		if err != nil {
			return "", fmt.Errorf("expand CODEX_HOME: %w", err)
		}
		return filepath.Clean(expanded), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve current home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func readCodexRunAuthSnapshot() ([]byte, error) {
	sourceCodexHome, err := codexRunSourceHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(sourceCodexHome, "auth.json")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect Codex auth state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Codex auth state %s is not a regular file", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex auth state: %w", err)
	}
	return payload, nil
}

func ensureCodexRunNoAdminSkillRoots(roots []string) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect codex admin skill root %s: %w", root, err)
		}
		var untrusted []string
		for _, entry := range entries {
			// Codex distributions may ship their own built-in skills in the
			// reserved .system directory. Every other entry remains fail-closed.
			if entry.Name() == ".system" && entry.IsDir() {
				continue
			}
			untrusted = append(untrusted, entry.Name())
		}
		if len(untrusted) > 0 {
			return fmt.Errorf("codex admin skill root %s contains skills; cannot guarantee ring-only skill isolation", root)
		}
	}
	return nil
}

func codexRunServerPlanIssues(manifest registry.Manifest) []string {
	if manifest.IsRemote() {
		if manifest.RequiresBearerTokenEnv() && codexGeneratedEnvKey(manifest.BearerTokenEnvVar) {
			return []string{fmt.Sprintf("bearer token env %s is reserved for an isolated Codex run path and cannot carry a credential", manifest.BearerTokenEnvVar)}
		}
		return nil
	}
	var issues []string
	if keys := sortedIntersectingEnvKeys(manifest.SecretEnv.Keys, codexIsolatedEnvKeySet()); len(keys) > 0 {
		issues = append(issues, fmt.Sprintf("secret env %s cannot be forwarded by codex run because Codex isolates %s; move it to required_env or remove it from this server", strings.Join(keys, ", "), strings.Join(keys, ", ")))
	}
	staticKeys := make([]string, 0, len(manifest.Env))
	for key := range manifest.Env {
		staticKeys = append(staticKeys, key)
	}
	if keys := sortedIntersectingEnvKeys(staticKeys, codexIsolatedEnvKeySet()); len(keys) > 0 {
		issues = append(issues, fmt.Sprintf("static env %s cannot override Codex run isolated home paths", strings.Join(keys, ", ")))
	}
	return issues
}

func codexIsolatedEnvKeys() []string {
	keys := []string{"CODEX_HOME", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "TMP"}
	if runtime.GOOS == "windows" {
		keys = append(keys, "APPDATA", "LOCALAPPDATA")
	}
	return keys
}

func codexIsolatedEnvKeySet() map[string]bool {
	keys := codexIsolatedEnvKeys()
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[codexEnvironmentKey(key)] = true
	}
	return set
}

func sortedIntersectingEnvKeys(keys []string, allowed map[string]bool) []string {
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" && allowed[codexEnvironmentKey(key)] {
			seen[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

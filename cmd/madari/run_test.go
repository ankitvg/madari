package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

func TestRunWithStoreCommandUsageValidation(t *testing.T) {
	store := newTestStore(t)

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{name: "list with arg", args: []string{"list", "oops"}, expected: "usage: madari list"},
		{name: "remove missing name", args: []string{"remove"}, expected: "usage: madari remove <name>"},
		{name: "enable missing name", args: []string{"enable"}, expected: "usage: madari enable <name>"},
		{name: "disable missing name", args: []string{"disable"}, expected: "usage: madari disable <name>"},
		{name: "sync missing target", args: []string{"sync"}, expected: "usage: madari sync <client>"},
		{name: "sync unsupported target", args: []string{"sync", "cursor"}, expected: "unsupported sync target"},
		{name: "sync extra positionals", args: []string{"sync", "claude-desktop", "extra"}, expected: "unexpected positional arguments"},
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

func TestRunHelpSubcommandOutput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{name: "help add", args: []string{"help", "add"}, contains: "madari add <name>"},
		{name: "help sync", args: []string{"help", "sync"}, contains: "madari sync claude-desktop"},
		{name: "help list", args: []string{"help", "list"}, contains: "madari list"},
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

func TestRunWithStoreSubcommandHelpFlags(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari add <name>") {
		t.Fatalf("expected add --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	if result := runCmd(store, "sync", "--help"); result.code != 0 || !strings.Contains(result.stdout, "madari sync claude-desktop") {
		t.Fatalf("expected sync --help to print command help, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
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

	// Make sure normal add still works after help coverage.
	if result := runCmd(store, "add", "stewreads", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("expected add after help checks to work, stderr=%s", result.stderr)
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

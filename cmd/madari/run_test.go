package main

import (
	"bytes"
	"path/filepath"
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

func TestRunWithStoreLifecycleCommands(t *testing.T) {
	store := newTestStore(t)

	result := runCmd(store, "add", "stewreads", "--command", "stewreads-mcp", "--client", "claude-desktop")
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

	result := runCmd(
		store,
		"add", "stewreads",
		"--command", "stewreads-mcp",
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

	if manifest.Command != "stewreads-mcp" {
		t.Fatalf("expected command to be saved, got: %q", manifest.Command)
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

func TestRunWithStoreAddValidatesRequiredFlags(t *testing.T) {
	store := newTestStore(t)

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
			args:     []string{"add", "stewreads", "--command", "stewreads-mcp"},
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

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name: "invalid env assignment",
			args: []string{
				"add", "stewreads", "--command", "stewreads-mcp", "--client", "claude-desktop",
				"--env", "BROKEN",
			},
			expected: "invalid env assignment",
		},
		{
			name: "duplicate env key",
			args: []string{
				"add", "stewreads", "--command", "stewreads-mcp", "--client", "claude-desktop",
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

	result := runCmd(
		store,
		"add", "stewreads",
		"--command", "stewreads-mcp",
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

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestRunWithStoreCodexRunExecutesWithRingServersAndPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex shell fixture is unix-specific")
	}
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	logPath := installFakeCodex(t, 0)
	t.Setenv("CLOUDSQL_MCP_TOKEN", "test-token")
	t.Setenv("LOCAL_TOKEN", "local-token")
	t.Setenv("LOCAL_SECRET", "local-secret")
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	if err := store.Save(registry.Manifest{
		Name:              "cloud-sql",
		Transport:         registry.TransportHTTP,
		URL:               "https://sqladmin.googleapis.com/mcp",
		OAuthResource:     "https://sqladmin.googleapis.com/",
		BearerTokenEnvVar: "CLOUDSQL_MCP_TOKEN",
		Headers: map[string]string{
			"x-goog-user-project": "stewreads",
		},
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save cloud-sql: %v", err)
	}
	if err := store.Save(registry.Manifest{
		Name:    "local-helper",
		Command: commandPath,
		Args:    []string{"--stdio"},
		Env: map[string]string{
			"LOCAL_MODE":   "test",
			"LOCAL_SECRET": "inline-secret",
		},
		RequiredEnv: registry.RequiredEnv{Keys: []string{"LOCAL_TOKEN"}},
		SecretEnv:   registry.SecretEnv{Keys: []string{"LOCAL_SECRET"}},
		Enabled:     true,
		Clients:     []string{"codex"},
	}); err != nil {
		t.Fatalf("save local-helper: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name:        "database",
		Members:     []string{"cloud-sql"},
		Description: "Database access",
		Contract: &registry.RingContract{
			Summary:         "Use bounded read-only SQL.",
			GoodFor:         []string{"aggregate reporting"},
			RequiredContext: []string{"project and database"},
			ExpectedOutputs: []string{"query summary"},
		},
	}); err != nil {
		t.Fatalf("save database ring: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name:    "helpers",
		Members: []string{"cloud-sql", "local-helper"},
	}); err != nil {
		t.Fatalf("save helpers ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "database", "--ring", "helpers", "--", "who are top 5 ebook creators?")
	if result.code != 0 {
		t.Fatalf("codex run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "codex stdout") || !strings.Contains(result.stderr, "codex stderr") {
		t.Fatalf("expected codex stdout/stderr to pass through, stdout=%s stderr=%s", result.stdout, result.stderr)
	}

	args := readNULArgs(t, logPath)
	wantPrefix := []string{"exec", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only", "--cd"}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unexpected codex args prefix:\n%#v", args)
	}
	if len(args) <= len(wantPrefix) {
		t.Fatalf("expected isolated codex run root after --cd, got %#v", args)
	}
	runRoot := args[len(wantPrefix)]
	pwdPayload, err := os.ReadFile(logPath + ".pwd")
	if err != nil {
		t.Fatalf("read fake codex cwd: %v", err)
	}
	if got := string(pwdPayload); got != runRoot {
		t.Fatalf("expected fake codex to run from isolated root %q, got %q", runRoot, got)
	}
	if _, err := os.Stat(runRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected isolated codex run root to be cleaned up, stat err=%v", err)
	}
	overrides := collectConfigOverrides(args)
	if len(overrides) != 3 {
		t.Fatalf("expected three config overrides, got %#v from args %#v", overrides, args)
	}
	if overrides[0] != "mcp_servers={}" {
		t.Fatalf("expected inherited MCP config clear first, got %#v", overrides)
	}
	wantCloud := `mcp_servers.cloud-sql={ url = "https://sqladmin.googleapis.com/mcp", required = true, oauth_resource = "https://sqladmin.googleapis.com/", bearer_token_env_var = "CLOUDSQL_MCP_TOKEN", http_headers = { x-goog-user-project = "stewreads" } }`
	if overrides[1] != wantCloud {
		t.Fatalf("unexpected cloud-sql override:\nwant %s\ngot  %s", wantCloud, overrides[1])
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	wantLocal := `mcp_servers.local-helper={ command = ` + tomlString(commandPath) + `, required = true, cwd = ` + tomlString(workingDir) + `, args = ["--stdio"], env_vars = ["LOCAL_SECRET", "LOCAL_TOKEN"], env = { LOCAL_MODE = "test" } }`
	if overrides[2] != wantLocal {
		t.Fatalf("unexpected local-helper override:\nwant %s\ngot  %s", wantLocal, overrides[2])
	}
	if strings.Contains(strings.Join(overrides, "\n"), "inline-secret") {
		t.Fatalf("static secret env value leaked into codex args: %#v", overrides)
	}

	if len(args) < 2 || args[len(args)-2] != "--" {
		t.Fatalf("expected prompt separator before final arg, got %#v", args)
	}
	prompt := args[len(args)-1]
	for _, want := range []string{
		"Selected rings:",
		"- database: Database access",
		"summary: Use bounded read-only SQL.",
		"good_for:",
		"- aggregate reporting",
		"- helpers",
		"Use only external MCP capabilities made available by the selected Madari rings.",
		"Original working directory:",
		"project-scoped Codex config cannot add capabilities outside these rings.",
		"User prompt:\nwho are top 5 ebook creators?",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	stateDir := filepath.Join(filepath.Dir(store.ServersDir()), "state")
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run should not create managed state, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run should not create Codex config, stat err=%v", err)
	}
}

func TestRunWithStoreCodexRunPropagatesCodexFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex shell fixture is unix-specific")
	}
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 17)

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save helper: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--", "prompt")
	if result.code == 0 {
		t.Fatalf("expected codex failure, stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "codex stdout") ||
		!strings.Contains(result.stderr, "codex stderr") ||
		!strings.Contains(result.stderr, "run codex exec") {
		t.Fatalf("expected forwarded output and wrapped error, stdout=%s stderr=%s", result.stdout, result.stderr)
	}
}

func TestRunWithStoreCodexRunRequiresCodexBinary(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	t.Setenv("PATH", t.TempDir())

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save helper: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--", "prompt")
	if result.code == 0 ||
		!strings.Contains(result.stderr, "launch plan is not ready") ||
		!strings.Contains(result.stdout, "codex executable not found in PATH") {
		t.Fatalf("expected missing codex plan error, code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
}

func installFakeCodex(t *testing.T, exitCode int) string {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "codex-args.bin")
	name := "codex"
	script := []byte("#!/bin/sh\n" +
		"printf '%s' \"$PWD\" > '" + logPath + ".pwd'\n" +
		"for arg in \"$@\"; do printf '%s\\0' \"$arg\" >> '" + logPath + "'; done\n" +
		"printf 'codex stdout\\n'\n" +
		"printf 'codex stderr\\n' >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n")
	if runtime.GOOS == "windows" {
		name = "codex.bat"
		script = []byte("@echo off\r\nexit /b " + strconv.Itoa(exitCode) + "\r\n")
	}
	codexPath := filepath.Join(binDir, name)
	if err := os.WriteFile(codexPath, script, 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readNULArgs(t *testing.T, path string) []string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake codex args: %v", err)
	}
	if len(payload) == 0 {
		return []string{}
	}
	parts := strings.Split(string(payload), "\x00")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func collectConfigOverrides(args []string) []string {
	var overrides []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			overrides = append(overrides, args[i+1])
			i++
		}
	}
	return overrides
}

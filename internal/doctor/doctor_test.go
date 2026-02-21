package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestRunHealthyServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix fixture mode bits are used in this test")
	}
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	commandPath := writeTestExecutable(t, tmp, "healthy-mcp")
	if err := store.Save(registry.Manifest{
		Name:    "healthy",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	configPath := filepath.Join(tmp, "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	report, err := Run(store, Options{ClaudeConfigPath: configPath})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if report.Summary.Ready != 1 || report.Summary.Error != 0 || report.Summary.Warning != 0 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if len(report.Servers) != 1 || report.Servers[0].Status != StatusReady {
		t.Fatalf("unexpected server report: %+v", report.Servers)
	}
	if report.ClaudeConfig.Status != StatusReady {
		t.Fatalf("expected ready claude config status, got: %+v", report.ClaudeConfig)
	}
}

func TestRunMissingRequiredEnvWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix fixture mode bits are used in this test")
	}
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	commandPath := writeTestExecutable(t, tmp, "warn-mcp")
	if err := store.Save(registry.Manifest{
		Name:    "warn",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"claude-desktop"},
		RequiredEnv: registry.RequiredEnv{
			Keys: []string{"MISSING_TEST_ENV_KEY"},
		},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	configPath := filepath.Join(tmp, "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	report, err := Run(store, Options{
		ClaudeConfigPath: configPath,
		EnvLookup: func(string) string {
			return ""
		},
	})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if report.Summary.Warning < 1 {
		t.Fatalf("expected warning in summary, got: %+v", report.Summary)
	}
	if len(report.Servers) != 1 || report.Servers[0].Status != StatusWarning {
		t.Fatalf("expected warning server status, got: %+v", report.Servers)
	}
}

func TestRunCapturesManifestAndConfigErrors(t *testing.T) {
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	if err := os.MkdirAll(store.ServersDir(), 0o755); err != nil {
		t.Fatalf("ensure servers dir: %v", err)
	}
	badManifestPath := filepath.Join(store.ServersDir(), "broken.toml")
	if err := os.WriteFile(badManifestPath, []byte("name = \"broken\"\nunknown = 1\n"), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}

	configPath := filepath.Join(tmp, "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	report, err := Run(store, Options{ClaudeConfigPath: configPath})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if len(report.ManifestErrors) != 1 {
		t.Fatalf("expected one manifest error, got: %+v", report.ManifestErrors)
	}
	if report.ClaudeConfig.Status != StatusError {
		t.Fatalf("expected config error status, got: %+v", report.ClaudeConfig)
	}
	if report.Summary.Error < 2 {
		t.Fatalf("expected at least two errors (manifest + config), got: %+v", report.Summary)
	}
}

func writeTestExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write test executable: %v", err)
	}
	return path
}

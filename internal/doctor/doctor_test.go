package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// testAdapter is a minimal ClientAdapter for use in doctor tests.
type testAdapter struct {
	target     string
	configPath string
}

func (a testAdapter) Target() string                    { return a.target }
func (a testAdapter) DefaultConfigPath() (string, error) { return a.configPath, nil }
func (a testAdapter) Sync(_ []registry.Manifest, _ clients.SyncOptions) (clients.SyncResult, error) {
	return clients.SyncResult{}, nil
}

func findClientConfig(report Report, target string) (ClientConfigReport, bool) {
	for _, cc := range report.ClientConfigs {
		if cc.Target == target {
			return cc, true
		}
	}
	return ClientConfigReport{}, false
}

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

	adapter := testAdapter{target: "claude-desktop", configPath: configPath}
	report, err := Run(store, Options{Adapters: []clients.ClientAdapter{adapter}})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if report.Summary.Ready != 1 || report.Summary.Error != 0 || report.Summary.Warning != 0 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if len(report.Servers) != 1 || report.Servers[0].Status != StatusReady {
		t.Fatalf("unexpected server report: %+v", report.Servers)
	}
	cc, ok := findClientConfig(report, "claude-desktop")
	if !ok {
		t.Fatalf("expected claude-desktop client config report, got: %+v", report.ClientConfigs)
	}
	if cc.Status != StatusReady {
		t.Fatalf("expected ready claude-desktop config status, got: %+v", cc)
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

	adapter := testAdapter{target: "claude-desktop", configPath: configPath}
	report, err := Run(store, Options{
		Adapters: []clients.ClientAdapter{adapter},
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
	// A valid manifest targeting the adapter is required so the config inspection runs.
	validManifestPath := filepath.Join(store.ServersDir(), "ok.toml")
	if err := os.WriteFile(validManifestPath, []byte("name = \"ok\"\ncommand = \"/nonexistent\"\nenabled = true\nclients = [\"claude-desktop\"]\n"), 0o644); err != nil {
		t.Fatalf("write valid manifest: %v", err)
	}

	configPath := filepath.Join(tmp, "claude_desktop_config.json")
	if err := os.WriteFile(configPath, []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	adapter := testAdapter{target: "claude-desktop", configPath: configPath}
	report, err := Run(store, Options{Adapters: []clients.ClientAdapter{adapter}})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if len(report.ManifestErrors) != 1 {
		t.Fatalf("expected one manifest error, got: %+v", report.ManifestErrors)
	}
	cc, ok := findClientConfig(report, "claude-desktop")
	if !ok {
		t.Fatalf("expected claude-desktop client config report, got: %+v", report.ClientConfigs)
	}
	if cc.Status != StatusError {
		t.Fatalf("expected config error status, got: %+v", cc)
	}
	if report.Summary.Error < 2 {
		t.Fatalf("expected at least two errors (manifest + config), got: %+v", report.Summary)
	}
}

func TestRunSkipsClientConfigWhenTargetUnused(t *testing.T) {
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	if err := store.Save(registry.Manifest{
		Name:    "code-only",
		Command: "not-used-for-skipped-check",
		Enabled: true,
		Clients: []string{"claude-code"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	adapter := testAdapter{
		target:     "claude-desktop",
		configPath: filepath.Join(tmp, "claude_desktop_config.json"),
	}
	report, err := Run(store, Options{Adapters: []clients.ClientAdapter{adapter}})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	if len(report.Servers) != 1 || report.Servers[0].Status != StatusSkipped {
		t.Fatalf("expected skipped server status, got: %+v", report.Servers)
	}
	cc, ok := findClientConfig(report, "claude-desktop")
	if !ok {
		t.Fatalf("expected claude-desktop client config report, got: %+v", report.ClientConfigs)
	}
	if cc.Status != StatusSkipped {
		t.Fatalf("expected skipped client config report, got: %+v", cc)
	}
	if report.Summary.Skipped != 1 {
		t.Fatalf("expected one skipped summary entry, got: %+v", report.Summary)
	}
}

func TestRunUsesConfigPathOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix fixture mode bits are used in this test")
	}
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	commandPath := writeTestExecutable(t, tmp, "override-mcp")
	if err := store.Save(registry.Manifest{
		Name:    "override",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	overridePath := filepath.Join(tmp, "override_config.json")
	if err := os.WriteFile(overridePath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("write override config: %v", err)
	}

	defaultPath := filepath.Join(tmp, "missing_default_config.json")
	adapter := testAdapter{
		target:     "claude-desktop",
		configPath: defaultPath,
	}
	report, err := Run(store, Options{
		Adapters:            []clients.ClientAdapter{adapter},
		ConfigPathOverrides: map[string]string{"claude-desktop": overridePath},
	})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	cc, ok := findClientConfig(report, "claude-desktop")
	if !ok {
		t.Fatalf("expected claude-desktop client config report, got: %+v", report.ClientConfigs)
	}
	if cc.Path != overridePath {
		t.Fatalf("expected override path %q, got %q", overridePath, cc.Path)
	}
	if cc.Status != StatusReady {
		t.Fatalf("expected ready status from override config, got: %+v", cc)
	}
}

func TestRunMissingClientConfigWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix fixture mode bits are used in this test")
	}
	tmp := t.TempDir()
	store := registry.NewStore(filepath.Join(tmp, "servers"))

	commandPath := writeTestExecutable(t, tmp, "missing-config-mcp")
	if err := store.Save(registry.Manifest{
		Name:    "missing-config",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	missingPath := filepath.Join(tmp, "does-not-exist.json")
	adapter := testAdapter{target: "claude-desktop", configPath: missingPath}
	report, err := Run(store, Options{Adapters: []clients.ClientAdapter{adapter}})
	if err != nil {
		t.Fatalf("doctor run failed: %v", err)
	}

	cc, ok := findClientConfig(report, "claude-desktop")
	if !ok {
		t.Fatalf("expected claude-desktop client config report, got: %+v", report.ClientConfigs)
	}
	if cc.Status != StatusWarning {
		t.Fatalf("expected warning status for missing config, got: %+v", cc)
	}
	if cc.Message != "config file not found" {
		t.Fatalf("expected missing config message, got: %q", cc.Message)
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

package claudecode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func TestSyncDryRunDoesNotMutateFiles(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	original := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv",
      "args": ["run", "weather.py"]
    }
  },
  "preferences": {
    "sidebarMode": "chat"
  }
}
`)
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	manifests := []registry.Manifest{newStewreadsManifest()}
	result, err := Sync(manifests, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("sync dry-run failed: %v", err)
	}

	if len(result.Added) != 1 || result.Added[0] != "stewreads" {
		t.Fatalf("expected stewreads to be planned as added, got: %+v", result)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after dry-run: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected dry-run to keep config unchanged")
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no state file write on dry-run, got err=%v", err)
	}
}

func TestSyncApplyAddUpdateRemoveLifecycle(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	baseConfig := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv",
      "args": ["run", "weather.py"]
    }
  },
  "preferences": {
    "sidebarMode": "chat"
  }
}
`)
	if err := os.WriteFile(configPath, baseConfig, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	manifest := newStewreadsManifest()
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "stewreads" {
		t.Fatalf("expected add result, got: %+v", result)
	}

	servers := readServers(t, configPath)
	if _, ok := servers["weather"]; !ok {
		t.Fatalf("expected existing weather server to be preserved")
	}
	if got := servers["stewreads"].Command; got != "stewreads-mcp" {
		t.Fatalf("expected stewreads command to be synced, got: %q", got)
	}

	managedNames := readManagedNames(t, statePath)
	if len(managedNames) != 1 || managedNames[0] != "stewreads" {
		t.Fatalf("expected managed state to track stewreads, got: %#v", managedNames)
	}

	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("unchanged sync failed: %v", err)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "stewreads" {
		t.Fatalf("expected unchanged result, got: %+v", result)
	}

	manifest.Args = []string{"--stdio"}
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("update sync failed: %v", err)
	}
	if len(result.Updated) != 1 || result.Updated[0] != "stewreads" {
		t.Fatalf("expected update result, got: %+v", result)
	}
	servers = readServers(t, configPath)
	if len(servers["stewreads"].Args) != 1 || servers["stewreads"].Args[0] != "--stdio" {
		t.Fatalf("expected synced args update, got: %#v", servers["stewreads"].Args)
	}

	manifest.Enabled = false
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("remove sync failed: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "stewreads" {
		t.Fatalf("expected remove result, got: %+v", result)
	}
	servers = readServers(t, configPath)
	if _, ok := servers["stewreads"]; ok {
		t.Fatalf("expected stewreads to be removed from Claude config")
	}
	if _, ok := servers["weather"]; !ok {
		t.Fatalf("expected weather to remain after removal")
	}

	managedNames = readManagedNames(t, statePath)
	if len(managedNames) != 0 {
		t.Fatalf("expected managed state to be empty after removal, got: %#v", managedNames)
	}
}

func TestSyncRejectsUnmanagedNameCollision(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	config := []byte(`{
  "mcpServers": {
    "stewreads": {
      "command": "manual-custom-command"
    }
  }
}
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     true,
	})
	if err == nil {
		t.Fatalf("expected sync conflict")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
	if !errors.Is(err, clients.ErrConflict) {
		t.Fatalf("expected clients.ErrConflict compatibility, got: %v", err)
	}
}

func TestSyncApplyPreservesUnknownTopLevelBlocks(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	config := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv"
    }
  },
  "preferences": {
    "sidebarMode": "chat",
    "theme": "solarized"
  },
  "project": {
    "name": "madari"
  }
}
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	if _, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	}); err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}

	root := readRoot(t, configPath)

	gotPrefs, ok := root["preferences"]
	if !ok {
		t.Fatalf("expected preferences block to be preserved")
	}
	assertJSONEqual(t, []byte(`{"sidebarMode":"chat","theme":"solarized"}`), gotPrefs)

	gotProject, ok := root["project"]
	if !ok {
		t.Fatalf("expected project block to be preserved")
	}
	assertJSONEqual(t, []byte(`{"name":"madari"}`), gotProject)
}

func TestSyncApplyFailsClosedOnInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	invalid := []byte("{broken")
	if err := os.WriteFile(configPath, invalid, 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err == nil {
		t.Fatalf("expected sync apply to fail on invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse Claude Code config JSON") {
		t.Fatalf("expected parse error, got: %v", err)
	}

	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config after failed sync: %v", readErr)
	}
	if string(after) != string(invalid) {
		t.Fatalf("expected fail-closed behavior with unchanged config")
	}
	if _, statErr := os.Stat(statePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no state file write on failure, got err=%v", statErr)
	}

	backups, globErr := filepath.Glob(configPath + ".bak.*")
	if globErr != nil {
		t.Fatalf("glob backup files: %v", globErr)
	}
	if len(backups) != 0 {
		t.Fatalf("expected no backup files on parse failure, got: %#v", backups)
	}
}

func TestSyncApplyCreatesBackup(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

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

	if _, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	}); err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}

	backups, err := filepath.Glob(configPath + ".bak.*")
	if err != nil {
		t.Fatalf("glob backup files: %v", err)
	}
	if len(backups) == 0 {
		t.Fatalf("expected backup file to be created")
	}

	backupPayload, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backupPayload) != string(original) {
		t.Fatalf("expected backup content to match original config")
	}
}

func TestSyncMigratesV1ManagedStateToV2(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".mcp.json")
	statePath := filepath.Join(tmp, "state", "claude-code-managed.json")

	baseConfig := []byte(`{
  "mcpServers": {
    "legacy": {
      "command": "legacy-mcp"
    }
  }
}
`)
	if err := os.WriteFile(configPath, baseConfig, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	v1State := []byte(`{"managed_servers":["legacy"]}` + "\n")
	if err := os.WriteFile(statePath, v1State, 0o644); err != nil {
		t.Fatalf("write v1 state fixture: %v", err)
	}

	result, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "legacy" {
		t.Fatalf("expected v1-managed legacy entry to be removed, got: %+v", result)
	}
	if len(result.Added) != 1 || result.Added[0] != "stewreads" {
		t.Fatalf("expected stewreads add, got: %+v", result)
	}

	servers := readServers(t, configPath)
	if _, ok := servers["legacy"]; ok {
		t.Fatalf("expected legacy entry to be removed from config")
	}

	payload, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read managed state: %v", err)
	}
	var file struct {
		Version        int                 `json:"version"`
		ManagedServers map[string][]string `json:"managed_servers"`
	}
	if err := json.Unmarshal(payload, &file); err != nil {
		t.Fatalf("parse managed state: %v", err)
	}
	if file.Version != 2 {
		t.Fatalf("expected managed state rewritten as version 2, got %d", file.Version)
	}
	expected := map[string][]string{"stewreads": {"standalone"}}
	if !reflect.DeepEqual(file.ManagedServers, expected) {
		t.Fatalf("expected managed state %#v, got %#v", expected, file.ManagedServers)
	}
}

func readRoot(t *testing.T, configPath string) map[string]json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("parse root config: %v", err)
	}
	return root
}

func readServers(t *testing.T, configPath string) map[string]serverConfig {
	t.Helper()
	root := readRoot(t, configPath)
	servers := map[string]serverConfig{}
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}
	return servers
}

func readManagedNames(t *testing.T, statePath string) []string {
	t.Helper()
	state, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}
	names := make([]string, 0, len(state))
	for name := range state {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var wantJSON any
	var gotJSON any
	if err := json.Unmarshal(want, &wantJSON); err != nil {
		t.Fatalf("parse expected JSON: %v", err)
	}
	if err := json.Unmarshal(got, &gotJSON); err != nil {
		t.Fatalf("parse actual JSON: %v", err)
	}
	if !reflect.DeepEqual(wantJSON, gotJSON) {
		t.Fatalf("JSON mismatch: want=%s got=%s", string(want), string(got))
	}
}

func newStewreadsManifest() registry.Manifest {
	return registry.Manifest{
		Name:    "stewreads",
		Command: "stewreads-mcp",
		Enabled: true,
		Clients: []string{Target},
		Env: map[string]string{
			"STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml",
		},
	}
}

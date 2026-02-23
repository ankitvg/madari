package claudecode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
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

func readServers(t *testing.T, configPath string) map[string]serverConfig {
	t.Helper()
	payload, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("parse root config: %v", err)
	}
	servers := map[string]serverConfig{}
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}
	return servers
}

func readManagedNames(t *testing.T, statePath string) []string {
	t.Helper()
	payload, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read managed state: %v", err)
	}
	state := managedState{}
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("parse managed state: %v", err)
	}
	sorted := append([]string(nil), state.ManagedServers...)
	slices.Sort(sorted)
	return sorted
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

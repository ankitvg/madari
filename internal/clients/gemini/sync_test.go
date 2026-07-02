package gemini

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func TestSyncDryRunDoesNotMutateFiles(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	original := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv",
      "args": ["run", "weather.py"]
    }
  },
  "theme": "Default"
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	result, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
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
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	baseConfig := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv",
      "args": ["run", "weather.py"]
    }
  },
  "telemetry": {
    "enabled": false
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
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
		t.Fatalf("expected stewreads to be removed from Gemini config")
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
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	config := []byte(`{
  "mcpServers": {
    "stewreads": {
      "command": "manual-custom-command"
    }
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     true,
	})
	if !errors.Is(err, clients.ErrConflict) {
		t.Fatalf("expected clients.ErrConflict, got: %v", err)
	}
}

func TestSyncApplyPreservesUnknownTopLevelBlocks(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	config := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv"
    }
  },
  "theme": "Default",
  "telemetry": {
    "enabled": false
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
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
	assertJSONEqual(t, []byte(`"Default"`), root["theme"])
	assertJSONEqual(t, []byte(`{"enabled":false}`), root["telemetry"])
}

func TestSyncApplyFailsClosedOnInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
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
	if !strings.Contains(err.Error(), "parse Gemini config JSON") {
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
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	original := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv"
    }
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
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

func TestSyncDoesNotAdoptEqualUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	config := []byte(`{
  "mcpServers": {
    "stewreads": {
      "command": "stewreads-mcp",
      "env": {
        "STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml"
      }
    }
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	manifest := newStewreadsManifest()
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "stewreads" {
		t.Fatalf("expected equal unmanaged entry to be unchanged, got: %+v", result)
	}
	if managedNames := readManagedNames(t, statePath); len(managedNames) != 0 {
		t.Fatalf("expected no adoption of unmanaged entry, got managed: %#v", managedNames)
	}

	manifest.Enabled = false
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync after disable failed: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("expected no removal of never-owned entry, got: %#v", result.Removed)
	}
	servers := readServers(t, configPath)
	if _, ok := servers["stewreads"]; !ok {
		t.Fatalf("expected hand-managed stewreads entry to survive disable")
	}
}

func TestSyncRefusesSecretEnvAtProjectScope(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	plain := newStewreadsManifest()
	plain.Name = "plain"
	result, err := Sync([]registry.Manifest{newSecretManifest(), plain}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("expected per-entry refusal without sync error, got: %v", err)
	}
	if len(result.Refused) != 1 || result.Refused[0] != "stewreads" {
		t.Fatalf("expected stewreads refused at project scope, got: %+v", result)
	}
	if len(result.Added) != 1 || result.Added[0] != "plain" {
		t.Fatalf("expected plain server to sync alongside refusal, got: %+v", result)
	}

	servers := readServers(t, configPath)
	if _, ok := servers["stewreads"]; ok {
		t.Fatalf("expected secret-bearing entry to stay out of repo-scoped config")
	}
	if _, ok := servers["plain"]; !ok {
		t.Fatalf("expected plain entry in repo-scoped config")
	}
}

func TestSyncAllowsSecretEnvAtUserScope(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-user-managed.json")

	result, err := Sync([]registry.Manifest{newSecretManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		Scope:      clients.ScopeUser,
	})
	if err != nil {
		t.Fatalf("expected user-scope sync to succeed, got: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "stewreads" {
		t.Fatalf("expected stewreads add, got: %+v", result)
	}
	servers := readServers(t, configPath)
	if servers["stewreads"].Env["STEWREADS_API_KEY"] != "shhh" {
		t.Fatalf("expected secret env value in user-scoped config, got: %#v", servers["stewreads"].Env)
	}
}

func TestSyncUserScopeDefaultsToUserStateFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MADARI_CONFIG_DIR", tmp)
	configPath := filepath.Join(tmp, ".gemini", "settings.json")

	_, err := Sync([]registry.Manifest{newSecretManifest()}, SyncOptions{
		ConfigPath: configPath,
		Scope:      clients.ScopeUser,
	})
	if err != nil {
		t.Fatalf("user-scope sync failed: %v", err)
	}

	userStatePath := filepath.Join(tmp, "state", Target+"-user-managed.json")
	if _, err := os.Stat(userStatePath); err != nil {
		t.Fatalf("expected user-scope state file at %s: %v", userStatePath, err)
	}
	projectStatePath := filepath.Join(tmp, "state", Target+"-managed.json")
	if _, err := os.Stat(projectStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no project state write for user-scope sync, err=%v", err)
	}
}

func TestSyncRejectsUnknownScope(t *testing.T) {
	tmp := t.TempDir()

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: filepath.Join(tmp, ".gemini", "settings.json"),
		StatePath:  filepath.Join(tmp, "state", "gemini-managed.json"),
		Scope:      "global",
	})
	if err == nil {
		t.Fatalf("expected error for unknown scope")
	}
	if !strings.Contains(err.Error(), "unknown sync scope") {
		t.Fatalf("expected unknown-scope error, got: %v", err)
	}
}

func TestAttachDetachOverlappingRingsAtConfigLevel(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")
	opts := SyncOptions{ConfigPath: configPath, StatePath: statePath}

	shared := newStewreadsManifest()
	only2 := newStewreadsManifest()
	only2.Name = "arxiv"
	manifests := []registry.Manifest{shared, only2}

	r1 := registry.Ring{Name: "r1", Members: []string{"stewreads"}}
	r2 := registry.Ring{Name: "r2", Members: []string{"stewreads", "arxiv"}}

	if _, err := AttachRing(r1, manifests, opts); err != nil {
		t.Fatalf("attach r1: %v", err)
	}
	result, err := AttachRing(r2, manifests, opts)
	if err != nil {
		t.Fatalf("attach r2: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "arxiv" {
		t.Fatalf("expected arxiv added on r2 attach, got: %+v", result)
	}

	state, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	expected := map[string][]string{
		"stewreads": {"ring:r1", "ring:r2"},
		"arxiv":     {"ring:r2"},
	}
	if !reflect.DeepEqual(state, expected) {
		t.Fatalf("expected overlapping refcounts, got: %#v", state)
	}

	result, err = DetachRing("r1", opts)
	if err != nil {
		t.Fatalf("detach r1: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("expected no removals while r2 owns shared member, got: %+v", result)
	}
	servers := readServers(t, configPath)
	if _, ok := servers["stewreads"]; !ok {
		t.Fatalf("expected shared member to survive r1 detach")
	}

	result, err = DetachRing("r2", opts)
	if err != nil {
		t.Fatalf("detach r2: %v", err)
	}
	if !reflect.DeepEqual(result.Removed, []string{"arxiv", "stewreads"}) {
		t.Fatalf("expected both members removed, got: %+v", result)
	}
	servers = readServers(t, configPath)
	if len(servers) != 0 {
		t.Fatalf("expected clean config, got: %#v", servers)
	}
}

func TestAttachRingConflictsOnEqualUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")

	config := []byte(`{
  "mcpServers": {
    "stewreads": {
      "command": "stewreads-mcp",
      "env": {
        "STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml"
      }
    }
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := AttachRing(
		registry.Ring{Name: "r1", Members: []string{"stewreads"}},
		[]registry.Manifest{newStewreadsManifest()},
		SyncOptions{ConfigPath: configPath, StatePath: statePath},
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict for equal-value unmanaged collision, got: %v", err)
	}
	if _, statErr := os.Stat(statePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no state write after conflict, got: %v", statErr)
	}
}

func TestSyncSkipsManagedRemoteManifest(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ".gemini", "settings.json")
	statePath := filepath.Join(tmp, "state", "gemini-managed.json")
	original := []byte(`{
  "mcpServers": {
    "weather": {
      "command": "uv",
      "args": ["run", "weather.py"]
    }
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	remote := registry.Manifest{
		Name:      "cloud-sql",
		Transport: registry.TransportHTTP,
		URL:       "https://example.com/mcp",
		Enabled:   true,
		Clients:   []string{Target},
	}
	result, err := Sync([]registry.Manifest{remote}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(result.Added) != 0 || len(result.Updated) != 0 || len(result.Removed) != 0 {
		t.Fatalf("expected remote manifest to be ineligible until the adapter supports it, got: %+v", result)
	}

	servers := readServers(t, configPath)
	if _, exists := servers["cloud-sql"]; exists {
		t.Fatalf("expected remote manifest not to be materialized, got: %#v", servers)
	}
	if _, ok := servers["weather"]; !ok {
		t.Fatalf("expected unmanaged weather entry to be preserved")
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

func newSecretManifest() registry.Manifest {
	manifest := newStewreadsManifest()
	manifest.Env["STEWREADS_API_KEY"] = "shhh"
	manifest.SecretEnv = registry.SecretEnv{Keys: []string{"STEWREADS_API_KEY"}}
	return manifest
}

func readRoot(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("parse config root: %v", err)
	}
	return root
}

func readServers(t *testing.T, path string) map[string]serverConfig {
	t.Helper()
	root := readRoot(t, path)
	raw, ok := root["mcpServers"]
	if !ok {
		return map[string]serverConfig{}
	}
	servers := map[string]serverConfig{}
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}
	return servers
}

func readManagedNames(t *testing.T, path string) []string {
	t.Helper()
	state, err := syncshared.LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}
	names := syncshared.MapKeys(state)
	return names
}

func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var wantValue any
	var gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("parse expected JSON: %v", err)
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("parse actual JSON: %v", err)
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatalf("JSON mismatch: want %#v got %#v", wantValue, gotValue)
	}
}

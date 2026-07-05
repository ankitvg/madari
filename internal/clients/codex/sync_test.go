package codex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
	"github.com/pelletier/go-toml/v2"
)

func TestSyncDryRunDoesNotMutateFiles(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	original := []byte(`model = "gpt-5"

[mcp_servers.weather]
command = "uv"
args = ["run", "weather.py"]
`)
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
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	baseConfig := []byte(`model = "gpt-5"

[mcp_servers.weather]
command = "uv"
args = ["run", "weather.py"]
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

	root := readRoot(t, configPath)
	if root["model"] != "gpt-5" {
		t.Fatalf("expected unrelated top-level model preserved, got: %#v", root["model"])
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
	if !reflect.DeepEqual(servers["stewreads"].Args, []string{"--stdio"}) {
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
		t.Fatalf("expected stewreads to be removed from Codex config")
	}
	if _, ok := servers["weather"]; !ok {
		t.Fatalf("expected weather to remain after removal")
	}
}

func TestSyncRejectsUnmanagedNameCollision(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	config := []byte(`[mcp_servers.stewreads]
command = "manual-custom-command"
`)
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

func TestSyncDoesNotAdoptEqualUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	config := []byte(`[mcp_servers.stewreads]
command = "stewreads-mcp"

[mcp_servers.stewreads.env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"
`)
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

func TestSyncConflictsOnDisabledUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	config := []byte(`[mcp_servers.stewreads]
command = "stewreads-mcp"
enabled = false

[mcp_servers.stewreads.env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     true,
	})
	if !errors.Is(err, clients.ErrConflict) {
		t.Fatalf("expected disabled unmanaged entry to conflict, got: %v", err)
	}
}

func TestSyncUpdatesOwnedDisabledEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	config := []byte(`[mcp_servers.stewreads]
command = "stewreads-mcp"
enabled = false

[mcp_servers.stewreads.env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{
		"stewreads": {syncshared.SourceStandalone},
	}); err != nil {
		t.Fatalf("write managed state: %v", err)
	}

	result, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("sync dry-run failed: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"stewreads"}) {
		t.Fatalf("expected owned disabled entry to be reported updated, got: %+v", result)
	}
}

func TestSyncApplyFailsClosedOnInvalidTOML(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	invalid := []byte("[broken")
	if err := os.WriteFile(configPath, invalid, 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err == nil {
		t.Fatalf("expected sync apply to fail on invalid TOML")
	}
	if !strings.Contains(err.Error(), "parse Codex config TOML") {
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

func TestSyncApplyFailsClosedOnMalformedArgs(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	invalid := []byte(`[mcp_servers.stewreads]
command = "stewreads-mcp"
args = [1]
`)
	if err := os.WriteFile(configPath, invalid, 0o644); err != nil {
		t.Fatalf("write invalid config fixture: %v", err)
	}

	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err == nil {
		t.Fatalf("expected sync apply to fail on malformed args")
	}
	if !strings.Contains(err.Error(), "mcp_servers.stewreads.args") {
		t.Fatalf("expected args parse error, got: %v", err)
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
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	original := []byte(`[mcp_servers.weather]
command = "uv"
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

func TestSyncPreservesPrivateConfigAndBackupModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix mode bits are used in this test")
	}
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	original := []byte(`[mcp_servers.weather]
command = "uv"
`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	if _, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	}); err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected rewritten config mode 0600, got %04o", got)
	}

	backups, err := filepath.Glob(configPath + ".bak.*")
	if err != nil {
		t.Fatalf("glob backup files: %v", err)
	}
	if len(backups) == 0 {
		t.Fatalf("expected backup file to be created")
	}
	backupInfo, err := os.Stat(backups[0])
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if got := backupInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected backup mode 0600, got %04o", got)
	}
}

func TestSyncCreatesNewConfigPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix mode bits are used in this test")
	}
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")

	if _, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	}); err != nil {
		t.Fatalf("sync apply failed: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected new config mode 0600, got %04o", got)
	}
}

func TestSyncMapsStaticEnvAndRuntimeEnvVars(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")

	manifest := newStewreadsManifest()
	manifest.Env["STEWREADS_API_KEY"] = "shhh"
	manifest.RequiredEnv = registry.RequiredEnv{Keys: []string{"STEWREADS_PROFILE"}}
	manifest.SecretEnv = registry.SecretEnv{Keys: []string{"STEWREADS_API_KEY"}}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "stewreads" {
		t.Fatalf("expected add result, got: %+v", result)
	}

	server := readServers(t, configPath)["stewreads"]
	if server.Env["STEWREADS_CONFIG_PATH"] == "" {
		t.Fatalf("expected non-secret static env to be written, got: %#v", server.Env)
	}
	if _, ok := server.Env["STEWREADS_API_KEY"]; ok {
		t.Fatalf("expected secret static value omitted from Codex config, got: %#v", server.Env)
	}
	if !reflect.DeepEqual(server.EnvVars, []string{"STEWREADS_API_KEY", "STEWREADS_PROFILE"}) {
		t.Fatalf("expected required and secret keys forwarded via env_vars, got: %#v", server.EnvVars)
	}
}

func TestSyncDefaultPathHonorsCodexHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CODEX_HOME", tmp)
	t.Setenv("MADARI_CONFIG_DIR", filepath.Join(tmp, "madari"))

	result, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	expectedPath := filepath.Join(tmp, "config.toml")
	if result.ConfigPath != expectedPath {
		t.Fatalf("expected default config path %q, got %q", expectedPath, result.ConfigPath)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected config at default CODEX_HOME path: %v", err)
	}
}

func TestAttachDetachOverlappingRingsAtConfigLevel(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
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

	result, err = DetachRing("r2", opts)
	if err != nil {
		t.Fatalf("detach r2: %v", err)
	}
	if !reflect.DeepEqual(result.Removed, []string{"arxiv", "stewreads"}) {
		t.Fatalf("expected both members removed, got: %+v", result)
	}
	servers := readServers(t, configPath)
	if len(servers) != 0 {
		t.Fatalf("expected clean config, got: %#v", servers)
	}
}

func TestAttachRingConflictsOnEqualUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	config := []byte(`[mcp_servers.stewreads]
command = "stewreads-mcp"

[mcp_servers.stewreads.env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"
`)
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

func TestSyncApplyRemoteHTTP(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	original := []byte(`model = "gpt-5"

[mcp_servers.weather]
command = "uv"
args = ["run", "weather.py"]
`)
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	manifest := newCloudSQLManifest()
	manifest.Headers = map[string]string{"x-goog-user-project": "example-project"}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync remote failed: %v", err)
	}
	if !reflect.DeepEqual(result.Added, []string{"cloud-sql"}) {
		t.Fatalf("expected remote add result, got: %+v", result)
	}

	servers := readServers(t, configPath)
	got := servers["cloud-sql"]
	if got.Command != "" {
		t.Fatalf("expected remote Codex entry to omit command, got: %q", got.Command)
	}
	if got.URL != "https://sqladmin.googleapis.com/mcp" {
		t.Fatalf("expected remote URL, got: %q", got.URL)
	}
	if got.OAuthResource != "https://sqladmin.googleapis.com/" {
		t.Fatalf("expected OAuth resource, got: %q", got.OAuthResource)
	}
	if got.BearerTokenEnvVar != "CLOUDSQL_MCP_TOKEN" {
		t.Fatalf("expected bearer token env var, got: %q", got.BearerTokenEnvVar)
	}
	if got.HTTPHeaders["x-goog-user-project"] != "example-project" {
		t.Fatalf("expected manifest headers as http_headers, got: %#v", got.HTTPHeaders)
	}
	if _, ok := servers["weather"]; !ok {
		t.Fatalf("expected unmanaged weather entry to be preserved")
	}

	manifest.Headers["x-goog-user-project"] = "other-project"
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync header change failed: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"cloud-sql"}) {
		t.Fatalf("expected header change to be an update, got: %+v", result)
	}

	manifest.URL = "https://sqladmin.googleapis.com/mcp/v2"
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync remote url change failed: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"cloud-sql"}) {
		t.Fatalf("expected url change to be an update, got: %+v", result)
	}
	if readServers(t, configPath)["cloud-sql"].URL != manifest.URL {
		t.Fatalf("expected updated url to be written")
	}
}

func TestAttachRingRemoteHTTPLifecycle(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")
	manifest := newCloudSQLManifest()
	opts := SyncOptions{ConfigPath: configPath, StatePath: statePath}

	result, err := AttachRing(registry.Ring{Name: "cloudsql-readonly", Members: []string{"cloud-sql"}}, []registry.Manifest{manifest}, opts)
	if err != nil {
		t.Fatalf("attach remote ring: %v", err)
	}
	if !reflect.DeepEqual(result.Added, []string{"cloud-sql"}) {
		t.Fatalf("expected remote ring add, got: %+v", result)
	}
	servers := readServers(t, configPath)
	if servers["cloud-sql"].URL != manifest.URL {
		t.Fatalf("expected remote entry to be materialized, got: %#v", servers["cloud-sql"])
	}

	result, err = DetachRing("cloudsql-readonly", opts)
	if err != nil {
		t.Fatalf("detach remote ring: %v", err)
	}
	if !reflect.DeepEqual(result.Removed, []string{"cloud-sql"}) {
		t.Fatalf("expected remote ring removal, got: %+v", result)
	}
	servers = readServers(t, configPath)
	if _, exists := servers["cloud-sql"]; exists {
		t.Fatalf("expected remote entry to be removed after detach, got: %#v", servers)
	}
}

func TestSyncKeepsSSEPending(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state", "codex-managed.json")

	manifest := newCloudSQLManifest()
	manifest.Transport = registry.TransportSSE
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(result.Added)+len(result.Updated)+len(result.Removed) != 0 {
		t.Fatalf("expected sse manifest to stay ineligible, got: %+v", result)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no config file for sse-only sync, got err=%v", err)
	}
}

func newCloudSQLManifest() registry.Manifest {
	return registry.Manifest{
		Name:              "cloud-sql",
		Transport:         registry.TransportHTTP,
		URL:               "https://sqladmin.googleapis.com/mcp",
		OAuthResource:     "https://sqladmin.googleapis.com/",
		BearerTokenEnvVar: "CLOUDSQL_MCP_TOKEN",
		Enabled:           true,
		Clients:           []string{Target},
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

func readRoot(t *testing.T, path string) map[string]any {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	root := map[string]any{}
	if err := toml.Unmarshal(payload, &root); err != nil {
		t.Fatalf("parse config root: %v", err)
	}
	return root
}

func readServers(t *testing.T, path string) map[string]serverConfig {
	t.Helper()
	_, _, servers, _, _, err := loadCodexConfig(path)
	if err != nil {
		t.Fatalf("load Codex config: %v", err)
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
	sort.Strings(names)
	return names
}

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotExportParseRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{
		Name:    "alpha",
		Command: "/usr/bin/env",
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save alpha manifest: %v", err)
	}
	if err := store.Save(Manifest{
		Name:    "beta",
		Command: "/usr/bin/env",
		Enabled: false,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save beta manifest: %v", err)
	}

	snapshot, err := ExportSnapshot(store)
	if err != nil {
		t.Fatalf("export snapshot failed: %v", err)
	}
	if snapshot.Version != SnapshotVersion {
		t.Fatalf("expected snapshot version %d, got %d", SnapshotVersion, snapshot.Version)
	}

	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot failed: %v", err)
	}

	parsed, err := ParseSnapshotJSON(payload)
	if err != nil {
		t.Fatalf("parse snapshot failed: %v", err)
	}
	if len(parsed.Servers) != 2 {
		t.Fatalf("expected 2 servers in parsed snapshot, got %d", len(parsed.Servers))
	}
	if parsed.Servers[0].Name != "alpha" && parsed.Servers[1].Name != "alpha" {
		t.Fatalf("expected alpha in parsed servers: %#v", parsed.Servers)
	}
}

func TestMarshalSnapshotUsesSnakeCaseKeys(t *testing.T) {
	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{
				Name:    "alpha",
				Command: "/usr/bin/env",
				Args:    []string{"--stdio"},
				Enabled: true,
				Clients: []string{"claude-desktop"},
				RequiredEnv: RequiredEnv{
					Keys: []string{"SMTP_PASSWORD"},
				},
			},
		},
	}

	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot failed: %v", err)
	}
	text := string(payload)
	for _, key := range []string{`"name"`, `"command"`, `"args"`, `"enabled"`, `"clients"`, `"required_env"`, `"keys"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("expected payload to contain key %s, payload=%s", key, text)
		}
	}
	for _, legacy := range []string{`"Name"`, `"Command"`, `"Args"`, `"Enabled"`, `"Clients"`, `"RequiredEnv"`, `"Keys"`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("expected payload to omit legacy key %s, payload=%s", legacy, text)
		}
	}
}

func TestImportSnapshotDryRunAndApply(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{
		Name:    "alpha",
		Command: "/usr/bin/env",
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save initial manifest: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{
				Name:    "alpha",
				Command: "/bin/echo",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
			{
				Name:    "beta",
				Command: "/usr/bin/env",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
		},
	}

	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if len(dryRunResult.Added) != 1 || dryRunResult.Added[0] != "beta" {
		t.Fatalf("expected beta added in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.Updated) != 1 || dryRunResult.Updated[0] != "alpha" {
		t.Fatalf("expected alpha updated in dry-run, got: %+v", dryRunResult)
	}

	alphaAfterDryRun, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after dry-run: %v", err)
	}
	if alphaAfterDryRun.Command != "/usr/bin/env" {
		t.Fatalf("expected dry-run not to change store")
	}
	if _, err := store.Get("beta"); err == nil {
		t.Fatalf("expected dry-run not to create beta")
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import failed: %v", err)
	}
	if len(applyResult.Added) != 1 || applyResult.Added[0] != "beta" {
		t.Fatalf("expected beta added in apply, got: %+v", applyResult)
	}
	if len(applyResult.Updated) != 1 || applyResult.Updated[0] != "alpha" {
		t.Fatalf("expected alpha updated in apply, got: %+v", applyResult)
	}

	alphaAfterApply, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after apply: %v", err)
	}
	if alphaAfterApply.Command != "/bin/echo" {
		t.Fatalf("expected alpha command to be updated, got: %q", alphaAfterApply.Command)
	}
	if _, err := store.Get("beta"); err != nil {
		t.Fatalf("expected beta to exist after apply: %v", err)
	}
}

func TestParseSnapshotRejectsInvalidPayloads(t *testing.T) {
	_, err := ParseSnapshotJSON([]byte(""))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty payload error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":1,"servers":[{"name":"a","command":"/x","enabled":true,"clients":["claude-desktop"]},{"name":"a","command":"/y","enabled":true,"clients":["claude-desktop"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate name error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":99,"servers":[]}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported snapshot version") {
		t.Fatalf("expected unsupported version error, got: %v", err)
	}
}

func TestMarshalSnapshotWritesNewline(t *testing.T) {
	snapshot := Snapshot{Version: SnapshotVersion, Servers: []Manifest{}}
	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		t.Fatalf("expected trailing newline")
	}
}

func TestImportSnapshotRejectsNilStore(t *testing.T) {
	_, err := ImportSnapshot(nil, Snapshot{Version: SnapshotVersion}, false)
	if err == nil {
		t.Fatalf("expected nil store error")
	}
}

func TestExportSnapshotRejectsNilStore(t *testing.T) {
	_, err := ExportSnapshot(nil)
	if err == nil {
		t.Fatalf("expected nil store error")
	}
}

func TestParseSnapshotFromFilePayload(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	snapshot, err := ExportSnapshot(store)
	if err != nil {
		t.Fatalf("export snapshot: %v", err)
	}
	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write snapshot file: %v", err)
	}

	readPayload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot file: %v", err)
	}
	parsed, err := ParseSnapshotJSON(readPayload)
	if err != nil {
		t.Fatalf("parse snapshot file payload: %v", err)
	}
	if len(parsed.Servers) != 1 || parsed.Servers[0].Name != "alpha" {
		t.Fatalf("unexpected parsed snapshot: %+v", parsed)
	}
}

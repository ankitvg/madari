package syncshared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadManagedStateMissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-managed.json")

	state, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}
	if len(state) != 0 {
		t.Fatalf("expected empty managed state for missing file, got: %#v", state)
	}
}

func TestLoadManagedStateReadsV1AsStandaloneSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")
	payload := []byte(`{"managed_servers":[" stewreads ","", "alpha","stewreads","beta","alpha"]}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	state, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}

	expected := map[string][]string{
		"alpha":     {SourceStandalone},
		"beta":      {SourceStandalone},
		"stewreads": {SourceStandalone},
	}
	if !reflect.DeepEqual(state, expected) {
		t.Fatalf("expected migrated v1 state %#v, got %#v", expected, state)
	}
}

func TestLoadManagedStateReadsV2Sources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")
	payload := []byte(`{
  "version": 2,
  "managed_servers": {
    " stewreads ": ["ring:research", " standalone ", "standalone", ""],
    "alpha": ["standalone"],
    "empty": ["", "  "],
    "": ["standalone"]
  }
}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	state, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}

	expected := map[string][]string{
		"alpha":     {SourceStandalone},
		"stewreads": {"ring:research", SourceStandalone},
	}
	if !reflect.DeepEqual(state, expected) {
		t.Fatalf("expected normalized v2 state %#v, got %#v", expected, state)
	}
}

func TestLoadManagedStateRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")
	payload := []byte(`{"version": 3, "managed_servers": {}}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := LoadManagedState(path)
	if err == nil {
		t.Fatalf("expected error for unsupported managed state version")
	}
	if !strings.Contains(err.Error(), "unsupported managed state version 3") {
		t.Fatalf("expected unsupported-version error, got: %v", err)
	}
}

func TestSaveManagedStateWritesDeterministicV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")

	state := map[string][]string{
		"zeta":  {SourceStandalone},
		"alpha": {"ring:research", SourceStandalone, "ring:research"},
		"beta":  {SourceStandalone},
	}
	if err := SaveManagedState(path, state); err != nil {
		t.Fatalf("save managed state: %v", err)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read managed state: %v", err)
	}

	expected := `{
  "version": 2,
  "managed_servers": {
    "alpha": [
      "ring:research",
      "standalone"
    ],
    "beta": [
      "standalone"
    ],
    "zeta": [
      "standalone"
    ]
  }
}
`
	if string(payload) != expected {
		t.Fatalf("expected deterministic v2 payload:\n%s\ngot:\n%s", expected, string(payload))
	}

	var file managedStateFile
	if err := json.Unmarshal(payload, &file); err != nil {
		t.Fatalf("parse managed state: %v", err)
	}
	if file.Version != managedStateVersion {
		t.Fatalf("expected version %d, got %d", managedStateVersion, file.Version)
	}
}

func TestSaveThenLoadManagedStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")

	state := map[string][]string{
		"beta":  {SourceStandalone, SourceStandalone},
		"alpha": {"ring:research", SourceStandalone},
		"gamma": {SourceStandalone},
	}
	if err := SaveManagedState(path, state); err != nil {
		t.Fatalf("save managed state: %v", err)
	}

	loaded, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}

	expected := map[string][]string{
		"alpha": {"ring:research", SourceStandalone},
		"beta":  {SourceStandalone},
		"gamma": {SourceStandalone},
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("expected round-trip state %#v, got %#v", expected, loaded)
	}
}

func TestNextManagedStatePreservesExistingSources(t *testing.T) {
	previous := map[string][]string{
		"alpha":    {"ring:research"},
		"beta":     {SourceStandalone},
		"gone":     {SourceStandalone},
		"ringonly": {"ring:research", SourceStandalone},
	}

	next := NextManagedState(previous, []string{"alpha", "beta", "new"})

	expected := map[string][]string{
		"alpha":    {"ring:research", SourceStandalone},
		"beta":     {SourceStandalone},
		"new":      {SourceStandalone},
		"ringonly": {"ring:research"},
	}
	if !reflect.DeepEqual(next, expected) {
		t.Fatalf("expected next state %#v, got %#v", expected, next)
	}
}

func TestBuildPlanRemovesOnlyWhenSourcesEmpty(t *testing.T) {
	equal := func(a, b string) bool { return a == b }
	cases := []struct {
		name        string
		existing    map[string]string
		managed     map[string][]string
		desired     map[string]string
		wantRemoved []string
	}{
		{
			name:        "standalone-only entry no longer desired is removed",
			existing:    map[string]string{"alpha": "cmd"},
			managed:     map[string][]string{"alpha": {SourceStandalone}},
			desired:     map[string]string{},
			wantRemoved: []string{"alpha"},
		},
		{
			name:     "entry with leftover ring source is kept",
			existing: map[string]string{"alpha": "cmd"},
			managed:  map[string][]string{"alpha": {"ring:research", SourceStandalone}},
			desired:  map[string]string{},
		},
		{
			name:    "undesired entry already absent from config is ignored",
			managed: map[string][]string{"alpha": {SourceStandalone}},
			desired: map[string]string{},
		},
		{
			name:     "still-desired entry is never removed",
			existing: map[string]string{"alpha": "cmd"},
			managed:  map[string][]string{"alpha": {SourceStandalone}},
			desired:  map[string]string{"alpha": "cmd"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := BuildPlan(tc.existing, tc.managed, tc.desired, equal, nil)
			if err != nil {
				t.Fatalf("build plan: %v", err)
			}
			if len(tc.wantRemoved) == 0 && len(result.Removed) != 0 {
				t.Fatalf("expected no removals, got %#v", result.Removed)
			}
			if len(tc.wantRemoved) > 0 && !reflect.DeepEqual(result.Removed, tc.wantRemoved) {
				t.Fatalf("expected removed %#v, got %#v", tc.wantRemoved, result.Removed)
			}
		})
	}
}

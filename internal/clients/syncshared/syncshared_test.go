package syncshared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadManagedStateMissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-managed.json")

	names, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected empty managed list for missing file, got: %#v", names)
	}
}

func TestLoadManagedStateNormalizesAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")
	payload := []byte(`{"managed_servers":[" stewreads ","", "alpha","stewreads","beta","alpha"]}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	names, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}

	expected := []string{"alpha", "beta", "stewreads"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("expected normalized names %#v, got %#v", expected, names)
	}
}

func TestSaveManagedStateWritesSortedNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")

	if err := SaveManagedState(path, []string{"zeta", "alpha", "beta"}); err != nil {
		t.Fatalf("save managed state: %v", err)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read managed state: %v", err)
	}

	var state managedState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("parse managed state: %v", err)
	}

	expected := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(state.ManagedServers, expected) {
		t.Fatalf("expected sorted names %#v, got %#v", expected, state.ManagedServers)
	}
}

func TestSaveThenLoadManagedStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")

	if err := SaveManagedState(path, []string{"beta", "alpha", "beta", "alpha", "gamma"}); err != nil {
		t.Fatalf("save managed state: %v", err)
	}

	names, err := LoadManagedState(path)
	if err != nil {
		t.Fatalf("load managed state: %v", err)
	}

	expected := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("expected round-trip normalized names %#v, got %#v", expected, names)
	}
}

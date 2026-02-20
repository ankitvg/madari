package registry

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(filepath.Join(tmp, "servers"))

	manifest := baseManifest()

	if err := store.Add(manifest); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	if err := store.Add(manifest); err == nil {
		t.Fatalf("expected add duplicate to fail")
	}

	loaded, err := store.Get(manifest.Name)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if loaded.Name != manifest.Name || loaded.Command != manifest.Command {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}

	if err := store.SetEnabled(manifest.Name, false); err != nil {
		t.Fatalf("set enabled failed: %v", err)
	}

	loaded, err = store.Get(manifest.Name)
	if err != nil {
		t.Fatalf("get after set enabled failed: %v", err)
	}
	if loaded.Enabled {
		t.Fatalf("expected enabled=false after toggle")
	}

	manifests, err := store.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(manifests) != 1 || manifests[0].Name != manifest.Name {
		t.Fatalf("unexpected list result: %#v", manifests)
	}

	if err := store.Remove(manifest.Name); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	_, err = store.Get(manifest.Name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after remove, got: %v", err)
	}
}

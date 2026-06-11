package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRingTestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	for _, name := range []string{"stewreads", "arxiv"} {
		if err := store.Save(Manifest{
			Name:    name,
			Command: "/bin/" + name,
			Enabled: true,
			Clients: []string{"claude-code"},
		}); err != nil {
			t.Fatalf("save manifest %s: %v", name, err)
		}
	}
	return store
}

func TestRingStoreLifecycle(t *testing.T) {
	store := newRingTestStore(t)

	ring := Ring{Name: "research", Members: []string{"stewreads", "arxiv"}, Description: "Research helpers"}
	if err := store.AddRing(ring); err != nil {
		t.Fatalf("add ring: %v", err)
	}

	got, err := store.GetRing("research")
	if err != nil {
		t.Fatalf("get ring: %v", err)
	}
	if got.Name != "research" || len(got.Members) != 2 {
		t.Fatalf("unexpected ring: %#v", got)
	}

	rings, err := store.ListRings()
	if err != nil {
		t.Fatalf("list rings: %v", err)
	}
	if len(rings) != 1 || rings[0].Name != "research" {
		t.Fatalf("unexpected rings: %#v", rings)
	}

	if err := store.RemoveRing("research"); err != nil {
		t.Fatalf("remove ring: %v", err)
	}
	if _, err := store.GetRing("research"); !errors.Is(err, ErrRingNotFound) {
		t.Fatalf("expected ErrRingNotFound after removal, got: %v", err)
	}
	if err := store.RemoveRing("research"); !errors.Is(err, ErrRingNotFound) {
		t.Fatalf("expected ErrRingNotFound for second removal, got: %v", err)
	}
}

func TestAddRingRejectsDuplicate(t *testing.T) {
	store := newRingTestStore(t)
	ring := Ring{Name: "research", Members: []string{"stewreads"}}

	if err := store.AddRing(ring); err != nil {
		t.Fatalf("add ring: %v", err)
	}
	err := store.AddRing(ring)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

func TestAddRingRejectsUnknownMembers(t *testing.T) {
	store := newRingTestStore(t)

	err := store.AddRing(Ring{Name: "research", Members: []string{"stewreads", "ghost", "phantom"}})
	if err == nil {
		t.Fatalf("expected unknown-member error")
	}
	if !strings.Contains(err.Error(), "unknown servers") ||
		!strings.Contains(err.Error(), "ghost, phantom") {
		t.Fatalf("expected sorted unknown member names, got: %v", err)
	}
	if _, getErr := store.GetRing("research"); !errors.Is(getErr, ErrRingNotFound) {
		t.Fatalf("expected no ring written after member validation failure, got: %v", getErr)
	}
}

func TestListRingsEmptyWithoutDirectory(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	rings, err := store.ListRings()
	if err != nil {
		t.Fatalf("list rings: %v", err)
	}
	if len(rings) != 0 {
		t.Fatalf("expected no rings, got: %#v", rings)
	}
}

func TestGetRingRejectsMismatchedName(t *testing.T) {
	store := newRingTestStore(t)
	if err := store.SaveRing(Ring{Name: "research", Members: []string{"stewreads"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	// Rename the file out from under the ring name.
	oldPath := filepath.Join(store.RingsDir(), "research.toml")
	newPath := filepath.Join(store.RingsDir(), "renamed.toml")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename ring file: %v", err)
	}

	_, err := store.GetRing("renamed")
	if err == nil || !strings.Contains(err.Error(), "mismatched name") {
		t.Fatalf("expected mismatched-name error, got: %v", err)
	}
}

func TestRingsDirIsSiblingOfServersDir(t *testing.T) {
	store := NewStore("/tmp/example/servers")
	if got := store.RingsDir(); got != filepath.Clean("/tmp/example/rings") {
		t.Fatalf("unexpected rings dir: %s", got)
	}
}

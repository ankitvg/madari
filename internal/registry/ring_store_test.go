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
	for _, name := range []string{"release", "review"} {
		if err := store.SaveSkill(Skill{Name: name, Description: name + " workflow"}, []byte("# "+name+"\n")); err != nil {
			t.Fatalf("save skill %s: %v", name, err)
		}
	}
	return store
}

func TestRingStoreLifecycle(t *testing.T) {
	store := newRingTestStore(t)

	ring := Ring{Name: "research", Members: []string{"stewreads", "arxiv"}, Skills: []string{"release"}, Description: "Research helpers"}
	if err := store.AddRing(ring); err != nil {
		t.Fatalf("add ring: %v", err)
	}

	got, err := store.GetRing("research")
	if err != nil {
		t.Fatalf("get ring: %v", err)
	}
	if got.Name != "research" || len(got.Members) != 2 || len(got.Skills) != 1 {
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

func TestAddRingRejectsUnknownSkills(t *testing.T) {
	store := newRingTestStore(t)

	err := store.AddRing(Ring{Name: "research", Skills: []string{"release", "ghost", "phantom"}})
	if err == nil {
		t.Fatalf("expected unknown-skill error")
	}
	if !strings.Contains(err.Error(), "unknown skills") ||
		!strings.Contains(err.Error(), "ghost, phantom") {
		t.Fatalf("expected sorted unknown skill names, got: %v", err)
	}
	if _, getErr := store.GetRing("research"); !errors.Is(getErr, ErrRingNotFound) {
		t.Fatalf("expected no ring written after skill validation failure, got: %v", getErr)
	}
}

func TestAddRequiredRingRejectsUnboundedMembers(t *testing.T) {
	store := newRingTestStore(t)
	ring := Ring{
		Name:    "bounded",
		Members: []string{"stewreads", "arxiv"},
		Policy:  &RingPolicy{Enforcement: PolicyEnforcementRequired},
	}
	err := store.AddRing(ring)
	if err == nil || !strings.Contains(err.Error(), "servers without an explicit non-empty allowed_tools allowlist: arxiv, stewreads") {
		t.Fatalf("expected sorted unbounded-member rejection, got: %v", err)
	}
	if _, getErr := store.GetRing("bounded"); !errors.Is(getErr, ErrRingNotFound) {
		t.Fatalf("required ring validation failure must write nothing, got: %v", getErr)
	}
}

func TestAddRequiredRingAcceptsBoundedMembers(t *testing.T) {
	store := newRingTestStore(t)
	for _, name := range []string{"stewreads", "arxiv"} {
		manifest, err := store.Get(name)
		if err != nil {
			t.Fatalf("get manifest %s: %v", name, err)
		}
		manifest.Access = &AccessProfile{AllowedTools: stringListPointer("read")}
		if err := store.Save(manifest); err != nil {
			t.Fatalf("save bounded manifest %s: %v", name, err)
		}
	}
	ring := Ring{
		Name:    "bounded",
		Members: []string{"stewreads", "arxiv"},
		Policy:  &RingPolicy{Enforcement: PolicyEnforcementRequired},
	}
	if err := store.AddRing(ring); err != nil {
		t.Fatalf("add bounded required ring: %v", err)
	}
	got, err := store.GetRing("bounded")
	if err != nil {
		t.Fatalf("get bounded required ring: %v", err)
	}
	if !got.RequiresPolicyEnforcement() {
		t.Fatalf("expected required policy to persist: %#v", got.Policy)
	}
}

func TestAddRequiredExecutionOnlyRingAcceptsMembersWithoutAccessProfiles(t *testing.T) {
	store := newRingTestStore(t)
	ring := Ring{
		Name:    "execution-only",
		Members: []string{"stewreads", "arxiv"},
		Policy: &RingPolicy{
			Enforcement: PolicyEnforcementRequired,
			Execution: &ExecutionPolicy{
				AmbientEnv: ExecutionAmbientEnvDeny, Sandbox: ExecutionSandboxReadOnly,
				MaxDuration: "10m", CredentialExposure: ExecutionCredentialExposureRunProcess,
			},
		},
	}
	if err := store.AddRing(ring); err != nil {
		t.Fatalf("required execution-only ring should not require access allowlists: %v", err)
	}
}

func TestAddRequiredExecutionRingKeepsAccessProfilesFailClosed(t *testing.T) {
	store := newRingTestStore(t)
	manifest, err := store.Get("stewreads")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	manifest.Access = &AccessProfile{AllowedTools: stringListPointer("read")}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save bounded manifest: %v", err)
	}
	ring := Ring{
		Name:    "mixed-policy",
		Members: []string{"stewreads", "arxiv"},
		Policy: &RingPolicy{
			Enforcement: PolicyEnforcementRequired,
			Execution: &ExecutionPolicy{
				AmbientEnv: ExecutionAmbientEnvDeny, Sandbox: ExecutionSandboxReadOnly,
				MaxDuration: "10m", CredentialExposure: ExecutionCredentialExposureRunProcess,
			},
		},
	}
	err = store.AddRing(ring)
	if err == nil || !strings.Contains(err.Error(), "servers without an explicit non-empty allowed_tools allowlist: arxiv") {
		t.Fatalf("selected access policy must keep every member bounded: %v", err)
	}
}

func TestAddRequiredRingRejectsExplicitEmptyAllowlist(t *testing.T) {
	store := newRingTestStore(t)
	manifest, err := store.Get("stewreads")
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	manifest.Access = &AccessProfile{AllowedTools: stringListPointer()}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save explicit allowlist clear: %v", err)
	}
	err = store.AddRing(Ring{
		Name:    "bounded",
		Members: []string{"stewreads"},
		Policy:  &RingPolicy{Enforcement: PolicyEnforcementRequired},
	})
	if err == nil || !strings.Contains(err.Error(), "without an explicit non-empty allowed_tools") {
		t.Fatalf("expected explicit empty allowlist rejection, got: %v", err)
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

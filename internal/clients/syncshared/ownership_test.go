package syncshared

import (
	"reflect"
	"testing"
)

// ownershipStep applies one operation to state and asserts the exact result.
type ownershipStep struct {
	desc   string
	apply  func(map[string][]string) map[string][]string
	expect map[string][]string
}

func attachStep(ring string, members ...string) func(map[string][]string) map[string][]string {
	return func(state map[string][]string) map[string][]string {
		return AttachRingState(state, ring, members)
	}
}

func detachStep(ring string) func(map[string][]string) map[string][]string {
	return func(state map[string][]string) map[string][]string {
		return DetachRingState(state, ring)
	}
}

// syncStep runs SyncOwnership with every named server eligible and nothing
// pre-existing unmanaged in config — the common case for ownership checks.
func syncStep(eligibleNames ...string) func(map[string][]string) map[string][]string {
	eligible := map[string]bool{}
	for _, name := range eligibleNames {
		eligible[name] = true
	}
	return func(state map[string][]string) map[string][]string {
		return SyncOwnership(state, eligible, map[string]bool{})
	}
}

// TestOwnershipOrderingMatrix is the refcount-correctness table the rings
// design hinges on: overlapping rings, both detach orders, standalone
// interleavings, idempotency, and plain syncs between every step that must
// never change ownership.
func TestOwnershipOrderingMatrix(t *testing.T) {
	scenarios := []struct {
		name    string
		initial map[string][]string
		steps   []ownershipStep
	}{
		{
			name:    "overlapping rings detach in attach order leaves clean state",
			initial: map[string][]string{},
			steps: []ownershipStep{
				{
					desc:  "attach r1 {shared, only1}",
					apply: attachStep("r1", "shared", "only1"),
					expect: map[string][]string{
						"shared": {"ring:r1"},
						"only1":  {"ring:r1"},
					},
				},
				{
					desc:  "plain sync changes no ownership",
					apply: syncStep("shared", "only1"),
					expect: map[string][]string{
						"shared": {"ring:r1"},
						"only1":  {"ring:r1"},
					},
				},
				{
					desc:  "attach r2 {shared, only2}",
					apply: attachStep("r2", "shared", "only2"),
					expect: map[string][]string{
						"shared": {"ring:r1", "ring:r2"},
						"only1":  {"ring:r1"},
						"only2":  {"ring:r2"},
					},
				},
				{
					desc:  "detach r1 keeps shared via r2",
					apply: detachStep("r1"),
					expect: map[string][]string{
						"shared": {"ring:r2"},
						"only2":  {"ring:r2"},
					},
				},
				{
					desc:  "plain sync still changes no ownership",
					apply: syncStep("shared", "only2"),
					expect: map[string][]string{
						"shared": {"ring:r2"},
						"only2":  {"ring:r2"},
					},
				},
				{
					desc:   "detach r2 leaves clean state",
					apply:  detachStep("r2"),
					expect: map[string][]string{},
				},
			},
		},
		{
			name:    "overlapping rings detach in reverse order leaves clean state",
			initial: map[string][]string{},
			steps: []ownershipStep{
				{
					desc:  "attach r1 {shared, only1}",
					apply: attachStep("r1", "shared", "only1"),
					expect: map[string][]string{
						"shared": {"ring:r1"},
						"only1":  {"ring:r1"},
					},
				},
				{
					desc:  "attach r2 {shared, only2}",
					apply: attachStep("r2", "shared", "only2"),
					expect: map[string][]string{
						"shared": {"ring:r1", "ring:r2"},
						"only1":  {"ring:r1"},
						"only2":  {"ring:r2"},
					},
				},
				{
					desc:  "detach r2 keeps shared via r1",
					apply: detachStep("r2"),
					expect: map[string][]string{
						"shared": {"ring:r1"},
						"only1":  {"ring:r1"},
					},
				},
				{
					desc:   "detach r1 leaves clean state",
					apply:  detachStep("r1"),
					expect: map[string][]string{},
				},
			},
		},
		{
			name: "standalone before ring survives ring detach",
			initial: map[string][]string{
				"server": {"standalone"},
			},
			steps: []ownershipStep{
				{
					desc:  "attach r1 {server}",
					apply: attachStep("r1", "server"),
					expect: map[string][]string{
						"server": {"ring:r1", "standalone"},
					},
				},
				{
					desc:  "plain sync changes no ownership",
					apply: syncStep("server"),
					expect: map[string][]string{
						"server": {"ring:r1", "standalone"},
					},
				},
				{
					desc:  "detach r1 keeps standalone",
					apply: detachStep("r1"),
					expect: map[string][]string{
						"server": {"standalone"},
					},
				},
			},
		},
		{
			name:    "ring before standalone claim never promotes",
			initial: map[string][]string{},
			steps: []ownershipStep{
				{
					desc:  "attach r1 {server}",
					apply: attachStep("r1", "server"),
					expect: map[string][]string{
						"server": {"ring:r1"},
					},
				},
				{
					desc:  "plain sync does NOT add standalone to ring-only entry",
					apply: syncStep("server"),
					expect: map[string][]string{
						"server": {"ring:r1"},
					},
				},
				{
					desc:   "detach r1 leaves clean state (no stranded standalone)",
					apply:  detachStep("r1"),
					expect: map[string][]string{},
				},
			},
		},
		{
			name: "attach is idempotent",
			initial: map[string][]string{
				"server": {"ring:r1"},
			},
			steps: []ownershipStep{
				{
					desc:  "re-attach r1 {server}",
					apply: attachStep("r1", "server"),
					expect: map[string][]string{
						"server": {"ring:r1"},
					},
				},
			},
		},
		{
			name: "detach of unattached ring is a no-op",
			initial: map[string][]string{
				"server": {"standalone"},
			},
			steps: []ownershipStep{
				{
					desc:  "detach unknown ring",
					apply: detachStep("ghost"),
					expect: map[string][]string{
						"server": {"standalone"},
					},
				},
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			state := copyState(sc.initial)
			for _, step := range sc.steps {
				state = step.apply(state)
				if !reflect.DeepEqual(state, step.expect) {
					t.Fatalf("step %q: expected state %#v, got %#v", step.desc, step.expect, state)
				}
			}
		})
	}
}

func TestSyncOwnershipClaimAndRelease(t *testing.T) {
	tests := []struct {
		name           string
		prev           map[string][]string
		eligible       map[string]bool
		existsInConfig map[string]bool
		expect         map[string][]string
	}{
		{
			name:     "new eligible entry not in config is claimed",
			prev:     map[string][]string{},
			eligible: map[string]bool{"fresh": true},
			expect:   map[string][]string{"fresh": {"standalone"}},
		},
		{
			name:           "pre-existing unmanaged config entry is never adopted",
			prev:           map[string][]string{},
			eligible:       map[string]bool{"hand": true},
			existsInConfig: map[string]bool{"hand": true},
			expect:         map[string][]string{},
		},
		{
			name:     "standalone released when no longer eligible",
			prev:     map[string][]string{"gone": {"standalone"}},
			eligible: map[string]bool{},
			expect:   map[string][]string{},
		},
		{
			name:     "ring sources retained through ineligibility",
			prev:     map[string][]string{"member": {"ring:r1", "standalone"}},
			eligible: map[string]bool{},
			expect:   map[string][]string{"member": {"ring:r1"}},
		},
		{
			name:     "eligible entry keeps existing sources verbatim",
			prev:     map[string][]string{"member": {"ring:r1"}},
			eligible: map[string]bool{"member": true},
			expect:   map[string][]string{"member": {"ring:r1"}},
		},
		{
			name: "hand-deleted ring-only entry is not promoted on resync",
			prev: map[string][]string{"member": {"ring:r1"}},
			// eligible and absent from config (user deleted the entry):
			// rematerialization must not add standalone.
			eligible: map[string]bool{"member": true},
			expect:   map[string][]string{"member": {"ring:r1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := tt.existsInConfig
			if exists == nil {
				exists = map[string]bool{}
			}
			got := SyncOwnership(tt.prev, tt.eligible, exists)
			if !reflect.DeepEqual(got, tt.expect) {
				t.Fatalf("expected %#v, got %#v", tt.expect, got)
			}
		})
	}
}

func TestRingSourceHelpers(t *testing.T) {
	if RingSource("research") != "ring:research" {
		t.Fatalf("unexpected ring source: %s", RingSource("research"))
	}
	if name, ok := RingNameFromSource("ring:research"); !ok || name != "research" {
		t.Fatalf("expected ring name research, got %q ok=%t", name, ok)
	}
	if _, ok := RingNameFromSource("standalone"); ok {
		t.Fatalf("standalone is not a ring source")
	}
	if _, ok := RingNameFromSource("ring:"); ok {
		t.Fatalf("empty ring name is not a ring source")
	}

	state := map[string][]string{
		"a": {"ring:r2", "standalone"},
		"b": {"ring:r1", "ring:r2"},
	}
	if got := AttachedRings(state); !reflect.DeepEqual(got, []string{"r1", "r2"}) {
		t.Fatalf("unexpected attached rings: %#v", got)
	}
}

package syncshared

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients"
)

var errTestConflict = errors.New("test conflict")

func planSyncStrings(
	t *testing.T,
	existing map[string]string,
	prev map[string][]string,
	entries map[string]Entry[string],
) (clients.SyncResult, map[string][]string, map[string]string) {
	t.Helper()
	result, next, writeSet, err := PlanSync(existing, prev, entries, func(a, b string) bool { return a == b }, errTestConflict)
	if err != nil {
		t.Fatalf("plan sync: %v", err)
	}
	return result, next, writeSet
}

func TestPlanSyncDisabledMemberScrubbedButRingOwned(t *testing.T) {
	result, next, writeSet := planSyncStrings(t,
		map[string]string{"member": "v1"},
		map[string][]string{"member": {"ring:r1", "standalone"}},
		map[string]Entry[string]{"member": {Eligible: false}},
	)

	if !reflect.DeepEqual(result.Removed, []string{"member"}) {
		t.Fatalf("expected disabled member removed from config, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"member": {"ring:r1"}}) {
		t.Fatalf("expected ring ownership retained and standalone released, got: %#v", next)
	}
	if len(writeSet) != 0 {
		t.Fatalf("expected empty write set, got: %#v", writeSet)
	}
}

func TestPlanSyncReenableRematerializesWithoutOwnershipChange(t *testing.T) {
	result, next, writeSet := planSyncStrings(t,
		map[string]string{},
		map[string][]string{"member": {"ring:r1"}},
		map[string]Entry[string]{"member": {Value: "v1", Eligible: true}},
	)

	if !reflect.DeepEqual(result.Added, []string{"member"}) {
		t.Fatalf("expected rematerialization, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"member": {"ring:r1"}}) {
		t.Fatalf("expected unchanged ring-only ownership, got: %#v", next)
	}
	if writeSet["member"] != "v1" {
		t.Fatalf("expected member in write set, got: %#v", writeSet)
	}
}

func TestPlanSyncSecretRefusedScrubbedButRingOwned(t *testing.T) {
	result, next, _ := planSyncStrings(t,
		map[string]string{"vault": "v1"},
		map[string][]string{"vault": {"ring:r1", "standalone"}},
		map[string]Entry[string]{"vault": {Eligible: false, Refused: true}},
	)

	if !reflect.DeepEqual(result.Refused, []string{"vault"}) {
		t.Fatalf("expected refusal report, got: %+v", result)
	}
	if !reflect.DeepEqual(result.Removed, []string{"vault"}) {
		t.Fatalf("expected secret scrubbed from config, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"vault": {"ring:r1"}}) {
		t.Fatalf("expected ring ownership retained, got: %#v", next)
	}
}

func TestPlanSyncNeverAdoptsUnmanagedExactMatch(t *testing.T) {
	result, next, writeSet := planSyncStrings(t,
		map[string]string{"hand": "v1"},
		map[string][]string{},
		map[string]Entry[string]{"hand": {Value: "v1", Eligible: true}},
	)

	if !reflect.DeepEqual(result.Unchanged, []string{"hand"}) {
		t.Fatalf("expected unmanaged exact match unchanged, got: %+v", result)
	}
	if len(next) != 0 {
		t.Fatalf("expected no standalone source recorded, got: %#v", next)
	}
	if _, written := writeSet["hand"]; written {
		t.Fatalf("expected unmanaged entry left unwritten, got: %#v", writeSet)
	}
}

func TestPlanSyncUnmanagedMismatchConflicts(t *testing.T) {
	_, _, _, err := PlanSync(
		map[string]string{"hand": "theirs"},
		map[string][]string{},
		map[string]Entry[string]{"hand": {Value: "ours", Eligible: true}},
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if !errors.Is(err, errTestConflict) {
		t.Fatalf("expected conflict error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hand") {
		t.Fatalf("expected conflicting name in error, got: %v", err)
	}
}

func TestPlanSyncClaimsFreshEntries(t *testing.T) {
	result, next, writeSet := planSyncStrings(t,
		map[string]string{},
		map[string][]string{},
		map[string]Entry[string]{"fresh": {Value: "v1", Eligible: true}},
	)

	if !reflect.DeepEqual(result.Added, []string{"fresh"}) {
		t.Fatalf("expected fresh add, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"fresh": {"standalone"}}) {
		t.Fatalf("expected standalone claim, got: %#v", next)
	}
	if writeSet["fresh"] != "v1" {
		t.Fatalf("expected fresh in write set, got: %#v", writeSet)
	}
}

func TestPlanSyncManifestDeletedAfterAttach(t *testing.T) {
	result, next, _ := planSyncStrings(t,
		map[string]string{"member": "v1"},
		map[string][]string{"member": {"ring:r1", "standalone"}},
		map[string]Entry[string]{},
	)

	if !reflect.DeepEqual(result.Removed, []string{"member"}) {
		t.Fatalf("expected deleted-manifest member scrubbed, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"member": {"ring:r1"}}) {
		t.Fatalf("expected ring ownership retained for dangling member, got: %#v", next)
	}
}

func TestPlanSyncStandaloneOnlyReleaseRemovesEntry(t *testing.T) {
	result, next, _ := planSyncStrings(t,
		map[string]string{"gone": "v1"},
		map[string][]string{"gone": {"standalone"}},
		map[string]Entry[string]{},
	)

	if !reflect.DeepEqual(result.Removed, []string{"gone"}) {
		t.Fatalf("expected standalone-only entry removed, got: %+v", result)
	}
	if len(next) != 0 {
		t.Fatalf("expected empty state, got: %#v", next)
	}
}

func TestPlanSyncManagedUpdateAndUnchanged(t *testing.T) {
	result, next, writeSet := planSyncStrings(t,
		map[string]string{"same": "v1", "changed": "old"},
		map[string][]string{"same": {"standalone"}, "changed": {"standalone"}},
		map[string]Entry[string]{
			"same":    {Value: "v1", Eligible: true},
			"changed": {Value: "new", Eligible: true},
		},
	)

	if !reflect.DeepEqual(result.Unchanged, []string{"same"}) || !reflect.DeepEqual(result.Updated, []string{"changed"}) {
		t.Fatalf("unexpected plan: %+v", result)
	}
	if writeSet["changed"] != "new" || writeSet["same"] != "v1" {
		t.Fatalf("expected managed entries in write set, got: %#v", writeSet)
	}
	if !reflect.DeepEqual(next, map[string][]string{"same": {"standalone"}, "changed": {"standalone"}}) {
		t.Fatalf("unexpected ownership: %#v", next)
	}
}

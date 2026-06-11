package syncshared

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestPlanAttachMaterializesMembers(t *testing.T) {
	result, next, writeSet, err := PlanAttach(
		map[string]string{"already": "v1"},
		map[string][]string{"already": {"standalone"}},
		"r1",
		[]string{"already", "fresh"},
		map[string]Entry[string]{
			"already": {Value: "v1", Eligible: true},
			"fresh":   {Value: "v2", Eligible: true},
		},
		nil,
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if err != nil {
		t.Fatalf("plan attach: %v", err)
	}

	if !reflect.DeepEqual(result.Added, []string{"fresh"}) || !reflect.DeepEqual(result.Unchanged, []string{"already"}) {
		t.Fatalf("unexpected plan: %+v", result)
	}
	expectedState := map[string][]string{
		"already": {"ring:r1", "standalone"},
		"fresh":   {"ring:r1"},
	}
	if !reflect.DeepEqual(next, expectedState) {
		t.Fatalf("unexpected ownership: %#v", next)
	}
	if writeSet["fresh"] != "v2" || writeSet["already"] != "v1" {
		t.Fatalf("unexpected write set: %#v", writeSet)
	}
}

func TestPlanAttachConflictsOnAnyUnmanagedCollisionEvenEqual(t *testing.T) {
	// The hand-managed entry has values IDENTICAL to the manifest. Plain
	// sync tolerates that as unchanged; attach must refuse — rings never
	// adopt hand-managed entries.
	_, _, _, err := PlanAttach(
		map[string]string{"hand": "v1"},
		map[string][]string{},
		"r1",
		[]string{"hand"},
		map[string]Entry[string]{"hand": {Value: "v1", Eligible: true}},
		nil,
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if !errors.Is(err, errTestConflict) {
		t.Fatalf("expected conflict for equal-value unmanaged collision, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hand") {
		t.Fatalf("expected member name in error, got: %v", err)
	}
}

func TestPlanAttachRecordsOwnershipForIneligibleMembers(t *testing.T) {
	result, next, writeSet, err := PlanAttach(
		map[string]string{},
		map[string][]string{},
		"r1",
		[]string{"disabled", "secret", "missing"},
		map[string]Entry[string]{
			"disabled": {Eligible: false},
			"secret":   {Refused: true},
		},
		nil,
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if err != nil {
		t.Fatalf("plan attach: %v", err)
	}

	if !reflect.DeepEqual(result.Refused, []string{"secret"}) {
		t.Fatalf("expected secret member refused, got: %+v", result)
	}
	if len(result.Added)+len(result.Updated)+len(result.Unchanged) != 0 {
		t.Fatalf("expected no materialization, got: %+v", result)
	}
	expectedState := map[string][]string{
		"disabled": {"ring:r1"},
		"secret":   {"ring:r1"},
		"missing":  {"ring:r1"},
	}
	if !reflect.DeepEqual(next, expectedState) {
		t.Fatalf("expected ownership recorded for all members, got: %#v", next)
	}
	if len(writeSet) != 0 {
		t.Fatalf("expected empty write set, got: %#v", writeSet)
	}
}

func TestPlanDetachRemovesOnlyLastSourceEntries(t *testing.T) {
	result, next := PlanDetach(
		map[string]string{"only": "v1", "shared": "v2", "standalone-too": "v3"},
		map[string][]string{
			"only":           {"ring:r1"},
			"shared":         {"ring:r1", "ring:r2"},
			"standalone-too": {"ring:r1", "standalone"},
		},
		"r1",
		nil,
	)

	if !reflect.DeepEqual(result.Removed, []string{"only"}) {
		t.Fatalf("expected only last-source entry removed, got: %+v", result)
	}
	expectedState := map[string][]string{
		"shared":         {"ring:r2"},
		"standalone-too": {"standalone"},
	}
	if !reflect.DeepEqual(next, expectedState) {
		t.Fatalf("unexpected ownership: %#v", next)
	}
}

func TestPlanDetachUnattachedRingIsNoOp(t *testing.T) {
	prev := map[string][]string{"server": {"standalone"}}
	result, next := PlanDetach(map[string]string{"server": "v1"}, prev, "ghost", nil)

	if result.HasChanges() {
		t.Fatalf("expected empty plan, got: %+v", result)
	}
	if !reflect.DeepEqual(next, prev) {
		t.Fatalf("expected unchanged ownership, got: %#v", next)
	}
}

func TestPlanDetachHandlesEntryAbsentFromConfig(t *testing.T) {
	// The ring-owned entry was hand-deleted from config; detach releases
	// ownership without reporting a removal.
	result, next := PlanDetach(map[string]string{}, map[string][]string{"only": {"ring:r1"}}, "r1", nil)

	if len(result.Removed) != 0 {
		t.Fatalf("expected no config removal, got: %+v", result)
	}
	if len(next) != 0 {
		t.Fatalf("expected ownership released, got: %#v", next)
	}
}

func TestPlanSyncNeverGrantsRingSourceToUnmanagedCollision(t *testing.T) {
	// Ring r1 is attached; its membership was edited to include "weather",
	// which already exists unmanaged in the client config. Reconciliation
	// must not adopt it: a value mismatch is a conflict, never an overwrite.
	_, _, _, err := PlanSync(
		map[string]string{"weather": "theirs", "member": "v1"},
		map[string][]string{"member": {"ring:r1"}},
		map[string]Entry[string]{
			"member":  {Value: "v1", Eligible: true},
			"weather": {Value: "ours", Eligible: true},
		},
		[]registry.Ring{{Name: "r1", Members: []string{"member", "weather"}}},
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if !errors.Is(err, errTestConflict) {
		t.Fatalf("expected conflict for unmanaged collision via membership edit, got: %v", err)
	}

	// Equal values: tolerated unchanged, but never granted the ring source.
	result, next, writeSet, err := PlanSync(
		map[string]string{"weather": "same", "member": "v1"},
		map[string][]string{"member": {"ring:r1"}},
		map[string]Entry[string]{
			"member":  {Value: "v1", Eligible: true},
			"weather": {Value: "same", Eligible: true},
		},
		[]registry.Ring{{Name: "r1", Members: []string{"member", "weather"}}},
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if err != nil {
		t.Fatalf("plan sync: %v", err)
	}
	if !reflect.DeepEqual(result.Unchanged, []string{"member", "weather"}) {
		t.Fatalf("expected equal collision tolerated, got: %+v", result)
	}
	if !reflect.DeepEqual(next, map[string][]string{"member": {"ring:r1"}}) {
		t.Fatalf("expected weather to stay unowned, got: %#v", next)
	}
	if _, written := writeSet["weather"]; written {
		t.Fatalf("expected unmanaged entry left unwritten, got: %#v", writeSet)
	}
}

func TestPlanAttachScrubsIneligibleMaterializedMembers(t *testing.T) {
	// Both members were materialized earlier; one is now secret-refused and
	// one disabled. Re-attaching must scrub them from the config while
	// keeping ownership — a stale secret must not linger until a later sync.
	result, next, writeSet, err := PlanAttach(
		map[string]string{"vault": "v-secret", "sleepy": "v1"},
		map[string][]string{"vault": {"ring:r1"}, "sleepy": {"ring:r1"}},
		"r1",
		[]string{"vault", "sleepy"},
		map[string]Entry[string]{
			"vault":  {Refused: true},
			"sleepy": {Eligible: false},
		},
		nil,
		func(a, b string) bool { return a == b },
		errTestConflict,
	)
	if err != nil {
		t.Fatalf("plan attach: %v", err)
	}

	if !reflect.DeepEqual(result.Removed, []string{"sleepy", "vault"}) {
		t.Fatalf("expected ineligible members scrubbed, got: %+v", result)
	}
	if !reflect.DeepEqual(result.Refused, []string{"vault"}) {
		t.Fatalf("expected vault refused, got: %+v", result)
	}
	expectedState := map[string][]string{
		"vault":  {"ring:r1"},
		"sleepy": {"ring:r1"},
	}
	if !reflect.DeepEqual(next, expectedState) {
		t.Fatalf("expected ownership retained, got: %#v", next)
	}
	if len(writeSet) != 0 {
		t.Fatalf("expected empty write set, got: %#v", writeSet)
	}
}

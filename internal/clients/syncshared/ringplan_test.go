package syncshared

import (
	"errors"
	"reflect"
	"strings"
	"testing"
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
	result, next := PlanDetach(map[string]string{"server": "v1"}, prev, "ghost")

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
	result, next := PlanDetach(map[string]string{}, map[string][]string{"only": {"ring:r1"}}, "r1")

	if len(result.Removed) != 0 {
		t.Fatalf("expected no config removal, got: %+v", result)
	}
	if len(next) != 0 {
		t.Fatalf("expected ownership released, got: %#v", next)
	}
}

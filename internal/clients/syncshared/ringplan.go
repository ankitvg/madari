package syncshared

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// PlanAttach computes the config mutations and ownership state for attaching
// a ring. Attach is scoped: it materializes (or refreshes) the ring's
// eligible members and touches nothing else. Attaching onto ANY pre-existing
// unmanaged name collision — equal values included — is a conflict: rings
// never adopt hand-managed entries. Ineligible members (disabled, refused,
// missing manifests) still gain the ring source — ownership persists through
// ineligibility — but are not materialized.
func PlanAttach[T any](
	existing map[string]T,
	prev map[string][]string,
	ring string,
	members []string,
	entries map[string]Entry[T],
	rings []registry.Ring,
	equal func(a, b T) bool,
	conflictErr error,
) (clients.SyncResult, map[string][]string, map[string]T, error) {
	if equal == nil {
		return clients.SyncResult{}, nil, nil, fmt.Errorf("equal comparer is required")
	}

	reconciled, released := ReconcileRingSources(prev, rings, unmanagedExisting(existing, prev))
	releasedSet := make(map[string]bool, len(released))
	for _, name := range released {
		releasedSet[name] = true
	}

	var conflicts []string
	for _, member := range members {
		member = strings.TrimSpace(member)
		_, exists := existing[member]
		_, owned := reconciled[member]
		if exists && !owned && !releasedSet[member] {
			conflicts = append(conflicts, member)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return clients.SyncResult{}, nil, nil, fmt.Errorf(
			"%w: unmanaged entries already exist: %s",
			conflictErr,
			strings.Join(conflicts, ", "),
		)
	}

	next := AttachRingState(reconciled, ring, members)

	result := clients.SyncResult{}
	writeSet := map[string]T{}
	// Entries fully released by reconciliation leave the config unless this
	// attach re-materializes them below.
	for name := range prev {
		if _, still := next[name]; still {
			continue
		}
		if _, exists := existing[name]; exists {
			result.Removed = append(result.Removed, name)
		}
	}
	for _, member := range members {
		member = strings.TrimSpace(member)
		entry, known := entries[member]
		if !known || !entry.Eligible {
			// Ownership is recorded but the member must be absent from the
			// config; a previously materialized entry is scrubbed now (the
			// conflict check above guarantees it is ours). For refused
			// members this removes a static secret from a repo-scoped
			// config immediately, not on some later sync.
			if entry.Refused {
				result.Refused = append(result.Refused, member)
			}
			if _, exists := existing[member]; exists {
				result.Removed = append(result.Removed, member)
			}
			continue
		}
		existingValue, exists := existing[member]
		switch {
		case !exists:
			result.Added = append(result.Added, member)
		case equal(existingValue, entry.Value):
			result.Unchanged = append(result.Unchanged, member)
		default:
			result.Updated = append(result.Updated, member)
		}
		writeSet[member] = entry.Value
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	sort.Strings(result.Unchanged)
	sort.Strings(result.Refused)
	return result, next, writeSet, nil
}

// unmanagedExisting lists config names madari does not own — reconciliation
// must never grant these a ring source.
func unmanagedExisting[T any](existing map[string]T, prev map[string][]string) map[string]bool {
	blocked := map[string]bool{}
	for name := range existing {
		if _, owned := prev[name]; !owned {
			blocked[name] = true
		}
	}
	return blocked
}

// PlanDetach computes the config mutations and ownership state for detaching
// a ring by name (the ring file is not required — detach must always be able
// to release sources). Only entries that lose their last source leave the
// config; entries still owned by other sources are untouched. Detaching an
// unattached ring yields an empty plan.
func PlanDetach[T any](
	existing map[string]T,
	prev map[string][]string,
	ring string,
	rings []registry.Ring,
) (clients.SyncResult, map[string][]string) {
	reconciled, _ := ReconcileRingSources(prev, rings, unmanagedExisting(existing, prev))
	next := DetachRingState(reconciled, ring)

	result := clients.SyncResult{}
	for name := range prev {
		if _, still := next[name]; still {
			continue
		}
		if _, exists := existing[name]; exists {
			result.Removed = append(result.Removed, name)
		}
	}
	sort.Strings(result.Removed)
	return result, next
}

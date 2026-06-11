package syncshared

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
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
	equal func(a, b T) bool,
	conflictErr error,
) (clients.SyncResult, map[string][]string, map[string]T, error) {
	if equal == nil {
		return clients.SyncResult{}, nil, nil, fmt.Errorf("equal comparer is required")
	}

	var conflicts []string
	for _, member := range members {
		member = strings.TrimSpace(member)
		_, exists := existing[member]
		_, owned := prev[member]
		if exists && !owned {
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

	next := AttachRingState(prev, ring, members)

	result := clients.SyncResult{}
	writeSet := map[string]T{}
	for _, member := range members {
		member = strings.TrimSpace(member)
		entry, known := entries[member]
		if !known {
			continue // no manifest: ownership recorded, nothing materialized
		}
		if entry.Refused {
			result.Refused = append(result.Refused, member)
			continue
		}
		if !entry.Eligible {
			continue // e.g. disabled: ownership recorded, not materialized
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
	sort.Strings(result.Unchanged)
	sort.Strings(result.Refused)
	return result, next, writeSet, nil
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
) (clients.SyncResult, map[string][]string) {
	next := DetachRingState(prev, ring)

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

package syncshared

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// Entry describes one manifest's standing for a sync operation.
type Entry[T any] struct {
	// Value is the materialized client config entry.
	Value T
	// Eligible reports whether the entry belongs in the client config right
	// now: enabled, targets this client, and not refused by the secrets
	// placement policy for the scope. Command validity is filtered at the
	// operation boundary (CLI sync, doctor drift) before entries are built.
	Eligible bool
	// Refused marks a would-be-eligible entry excluded by the secrets
	// placement policy; reported via SyncResult.Refused.
	Refused bool
}

// PlanSync computes a sync plan and the post-sync ownership state under the
// ownership/materialization model: an entry is present in the client config
// iff it is owned (sources non-empty) AND eligible. Ownership persists
// through ineligibility (ring sources always; standalone per SyncOwnership's
// claim/release rules). The returned write set contains exactly the entries
// the caller should serialize; pre-existing unmanaged entries are never
// written, adopted, or removed — an unmanaged value mismatch is a conflict.
func PlanSync[T any](
	existing map[string]T,
	prev map[string][]string,
	entries map[string]Entry[T],
	rings []registry.Ring,
	equal func(a, b T) bool,
	conflictErr error,
) (clients.SyncResult, map[string][]string, map[string]T, error) {
	if equal == nil {
		return clients.SyncResult{}, nil, nil, fmt.Errorf("equal comparer is required")
	}

	// Ring membership edits converge here: reconciled sources drive
	// ownership, while removal candidates are judged against the original
	// state so fully released entries still leave the config.
	reconciled, _ := ReconcileRingSources(prev, rings)

	eligible := make(map[string]bool, len(entries))
	for name, entry := range entries {
		if entry.Eligible {
			eligible[name] = true
		}
	}
	existsInConfig := make(map[string]bool, len(existing))
	for name := range existing {
		existsInConfig[name] = true
	}

	next := SyncOwnership(reconciled, eligible, existsInConfig)

	result := clients.SyncResult{}
	writeSet := map[string]T{}
	var conflicts []string

	for name, entry := range entries {
		if !entry.Eligible {
			if entry.Refused {
				result.Refused = append(result.Refused, name)
			}
			continue
		}
		if len(prev[name]) > 0 && len(next[name]) == 0 {
			// Ownership fully released this pass (e.g. reconciliation
			// dropped the last ring source): this is a removal, not a
			// materialization candidate. A later sync may re-claim it.
			continue
		}
		existingValue, exists := existing[name]
		owned := len(next[name]) > 0
		switch {
		case !exists:
			// SyncOwnership claims unowned names absent from config, so an
			// eligible entry missing from config is always owned here.
			result.Added = append(result.Added, name)
			writeSet[name] = entry.Value
		case owned:
			// In config and owned implies previously owned (claims only
			// happen for names absent from config).
			if equal(existingValue, entry.Value) {
				result.Unchanged = append(result.Unchanged, name)
			} else {
				result.Updated = append(result.Updated, name)
			}
			writeSet[name] = entry.Value
		default:
			// Unmanaged collision: tolerate an exact match untouched (no
			// adoption, no rewrite), refuse otherwise.
			if equal(existingValue, entry.Value) {
				result.Unchanged = append(result.Unchanged, name)
			} else {
				conflicts = append(conflicts, name)
			}
		}
	}

	for name := range prev {
		entry, known := entries[name]
		if known && entry.Eligible && len(next[name]) > 0 {
			continue
		}
		if _, exists := existing[name]; exists {
			result.Removed = append(result.Removed, name)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	sort.Strings(result.Unchanged)
	sort.Strings(result.Refused)

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		if conflictErr != nil {
			return clients.SyncResult{}, nil, nil, fmt.Errorf(
				"%w: unmanaged entries already exist with different values: %s",
				conflictErr,
				strings.Join(conflicts, ", "),
			)
		}
		return clients.SyncResult{}, nil, nil, fmt.Errorf(
			"unmanaged entries already exist with different values: %s",
			strings.Join(conflicts, ", "),
		)
	}

	return result, next, writeSet, nil
}

package syncshared

import (
	"slices"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

const ringSourcePrefix = "ring:"

// RingSource returns the ownership source recorded for ring name.
func RingSource(ring string) string {
	return ringSourcePrefix + ring
}

// RingNameFromSource extracts the ring name from a ring ownership source.
func RingNameFromSource(source string) (string, bool) {
	if !strings.HasPrefix(source, ringSourcePrefix) {
		return "", false
	}
	name := strings.TrimPrefix(source, ringSourcePrefix)
	if name == "" {
		return "", false
	}
	return name, true
}

// AttachRingState returns state with ring's source added to every member.
// Members with no prior entry become ring-owned; existing sources are
// preserved. Idempotent: attaching an already-attached ring is a no-op.
// The input map is never mutated.
func AttachRingState(state map[string][]string, ring string, members []string) map[string][]string {
	next := copyState(state)
	source := RingSource(ring)
	for _, member := range members {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if !slices.Contains(next[member], source) {
			next[member] = append(append([]string(nil), next[member]...), source)
		}
	}
	return normalizeManagedState(next)
}

// DetachRingState returns state with ring's source removed from every entry
// carrying it — not just current ring members, so membership edits and
// missing ring files never strand sources. Entries whose sources empty are
// dropped; ownership by other sources is untouched. Idempotent: detaching an
// unattached ring is a no-op. The input map is never mutated.
func DetachRingState(state map[string][]string, ring string) map[string][]string {
	next := make(map[string][]string, len(state))
	source := RingSource(ring)
	for name, sources := range state {
		remaining := withoutSource(sources, source)
		if len(remaining) == 0 {
			continue
		}
		next[name] = remaining
	}
	return normalizeManagedState(next)
}

// AttachedRings lists ring names appearing among state sources, sorted.
func AttachedRings(state map[string][]string) []string {
	seen := map[string]struct{}{}
	for _, sources := range state {
		for _, source := range sources {
			if ring, ok := RingNameFromSource(source); ok {
				seen[ring] = struct{}{}
			}
		}
	}
	rings := make([]string, 0, len(seen))
	for ring := range seen {
		rings = append(rings, ring)
	}
	slices.Sort(rings)
	return rings
}

// ReconcileRingSources recomputes ring sources against current ring
// membership for every ring that is attached per the state: new members gain
// the ring's source, ex-members lose it. Names in blocked are never granted
// a source — callers pass the unmanaged config collisions so a membership
// edit can never adopt or overwrite a hand-managed entry (the collision
// surfaces through the normal conflict path instead). Sources of rings
// absent from the given definitions (e.g. the ring file was deleted outside
// madari) are left intact — doctor flags them and detach-by-name can always
// release them. Returns the reconciled state plus the names whose sources
// emptied (released entries leave the client config). The input map is
// never mutated.
func ReconcileRingSources(state map[string][]string, rings []registry.Ring, blocked map[string]bool) (map[string][]string, []string) {
	attached := map[string]bool{}
	for _, ring := range AttachedRings(state) {
		attached[ring] = true
	}

	next := copyState(state)
	for _, ring := range rings {
		if !attached[ring.Name] {
			continue
		}
		source := RingSource(ring.Name)
		members := map[string]bool{}
		for _, member := range ring.Members {
			member = strings.TrimSpace(member)
			if member != "" {
				members[member] = true
			}
		}
		for name, sources := range next {
			if members[name] || !slices.Contains(sources, source) {
				continue
			}
			next[name] = withoutSource(sources, source)
		}
		for member := range members {
			if blocked[member] {
				continue
			}
			if !slices.Contains(next[member], source) {
				next[member] = append(append([]string(nil), next[member]...), source)
			}
		}
	}

	var released []string
	for name, sources := range state {
		if len(sources) > 0 && len(next[name]) == 0 {
			released = append(released, name)
		}
	}
	slices.Sort(released)
	return normalizeManagedState(next), released
}

// SyncOwnership computes post-plain-sync ownership. The standalone claim is
// derived from ownership intent, never from a sync plan:
//
//   - an entry whose sources already contain standalone keeps it while it
//     stays eligible, and loses only standalone when it does not;
//   - a name with no previous entry is claimed iff it is eligible AND the
//     client config does not already contain it (no adoption of pre-existing
//     unmanaged entries, even exact matches);
//   - ring-only entries are never promoted to standalone;
//   - ring sources are always retained — only detach removes them.
//
// The input map is never mutated.
func SyncOwnership(prev map[string][]string, eligible map[string]bool, existsInConfig map[string]bool) map[string][]string {
	next := make(map[string][]string, len(prev)+len(eligible))
	for name, sources := range prev {
		kept := sources
		if !eligible[name] {
			kept = withoutSource(sources, SourceStandalone)
		}
		if len(kept) == 0 {
			continue
		}
		next[name] = append([]string(nil), kept...)
	}
	for name, ok := range eligible {
		if !ok {
			continue
		}
		if _, owned := prev[name]; owned {
			continue
		}
		if existsInConfig[name] {
			continue
		}
		next[name] = []string{SourceStandalone}
	}
	return normalizeManagedState(next)
}

func copyState(state map[string][]string) map[string][]string {
	next := make(map[string][]string, len(state))
	for name, sources := range state {
		next[name] = append([]string(nil), sources...)
	}
	return next
}

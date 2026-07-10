package main

import (
	"github.com/ankitvg/madari/internal/policy"
	"github.com/ankitvg/madari/internal/registry"
)

func preflightRequiredRingPolicy(ring registry.Ring, manifests []registry.Manifest, target string, surface policy.Surface) error {
	return policy.ValidateRequiredRing(ring, manifests, target, surface).Err()
}

// preflightAttachedRequiredRingPolicies checks only rings whose ownership
// source is currently recorded for the selected target. A missing definition
// blocks sync because Madari cannot know whether that attached ring required
// enforcement; detach-by-name remains available as the recovery path.
func preflightAttachedRequiredRingPolicies(rings []registry.Ring, attached []string, manifests []registry.Manifest, target string) error {
	return policy.ValidateAttachedRequiredRings(rings, attached, manifests, target, policy.SurfacePersistent)
}

func hasAttachedRequiredRingPolicy(rings []registry.Ring, attached []string) bool {
	attachedSet := make(map[string]bool, len(attached))
	for _, name := range attached {
		attachedSet[strings.TrimSpace(name)] = true
	}
	for _, ring := range rings {
		if attachedSet[ring.Name] && ring.RequiresPolicyEnforcement() {
			return true
		}
	}
	return false
}

func preflightRequiredRingServerOwnership(rings []registry.Ring, policyAttached, serverAttached []string, target string) error {
	policySet := make(map[string]bool, len(policyAttached))
	for _, name := range policyAttached {
		policySet[strings.TrimSpace(name)] = true
	}
	serverSet := make(map[string]bool, len(serverAttached))
	for _, name := range serverAttached {
		serverSet[strings.TrimSpace(name)] = true
	}
	for _, ring := range rings {
		if !policySet[ring.Name] || serverSet[ring.Name] || !ring.RequiresPolicyEnforcement() || len(ring.Members) == 0 {
			continue
		}
		return fmt.Errorf("ring %q requires policy enforcement and is attached only through skills; run `madari ring attach %s %s` to establish server ownership before sync", ring.Name, ring.Name, target)
	}
	return nil
}

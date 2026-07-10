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

package main

import (
	"fmt"
	"strings"

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
	byName := make(map[string]registry.Ring, len(rings))
	for _, ring := range rings {
		byName[ring.Name] = ring
	}
	for _, name := range sortedUniqueStrings(attached) {
		ring, exists := byName[strings.TrimSpace(name)]
		if !exists {
			return fmt.Errorf("attached ring %q is missing; restore its definition or detach it before sync so policy requirements cannot be bypassed", name)
		}
		if err := preflightRequiredRingPolicy(ring, manifests, target, policy.SurfacePersistent); err != nil {
			return err
		}
	}
	return nil
}

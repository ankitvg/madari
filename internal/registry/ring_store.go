package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrRingNotFound = errors.New("ring not found")

// RingsDir returns the on-disk rings directory, a sibling of the servers
// directory under the same config root.
func (s *Store) RingsDir() string {
	return filepath.Join(filepath.Dir(s.serversDir), "rings")
}

// AddRing inserts a new ring; it fails if the ring already exists or any
// referenced server or skill is missing from the registry.
func (s *Store) AddRing(ring Ring) error {
	if err := ring.Validate(); err != nil {
		return err
	}
	if _, err := s.GetRing(ring.Name); err == nil {
		return fmt.Errorf("ring %q already exists", ring.Name)
	} else if !errors.Is(err, ErrRingNotFound) {
		return err
	}
	if err := s.ValidateRingMembers(ring); err != nil {
		return err
	}
	if err := s.ValidateRingPolicy(ring); err != nil {
		return err
	}
	return s.SaveRing(ring)
}

// SaveRing writes or updates a ring manifest. Shape is validated; member
// existence is the caller's responsibility (AddRing checks the registry,
// snapshot import checks snapshot plus registry).
func (s *Store) SaveRing(ring Ring) error {
	if err := ring.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.RingsDir(), 0o755); err != nil {
		return fmt.Errorf("ensure rings directory: %w", err)
	}

	path, err := s.pathForRing(ring.Name)
	if err != nil {
		return err
	}
	payload, err := MarshalRing(ring)
	if err != nil {
		return err
	}
	if err := writeFileAtomically(path, payload, 0o644); err != nil {
		return fmt.Errorf("save ring %q: %w", ring.Name, err)
	}
	return nil
}

// GetRing loads one ring by name.
func (s *Store) GetRing(name string) (Ring, error) {
	path, err := s.pathForRing(name)
	if err != nil {
		return Ring{}, err
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Ring{}, ErrRingNotFound
		}
		return Ring{}, fmt.Errorf("read ring %q: %w", name, err)
	}

	ring, err := ParseRing(payload)
	if err != nil {
		return Ring{}, fmt.Errorf("parse ring %q: %w", name, err)
	}
	if ring.Name != name {
		return Ring{}, fmt.Errorf("ring %q has mismatched name %q", name, ring.Name)
	}
	return ring, nil
}

// ListRings returns all rings sorted by name.
func (s *Store) ListRings() ([]Ring, error) {
	entries, err := os.ReadDir(s.RingsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Ring{}, nil
		}
		return nil, fmt.Errorf("read rings directory: %w", err)
	}

	rings := make([]Ring, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		ring, err := s.GetRing(name)
		if err != nil {
			return nil, err
		}
		rings = append(rings, ring)
	}

	sort.Slice(rings, func(i, j int) bool {
		return rings[i].Name < rings[j].Name
	})
	return rings, nil
}

// RemoveRing deletes one ring by name.
func (s *Store) RemoveRing(name string) error {
	path, err := s.pathForRing(name)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrRingNotFound
		}
		return fmt.Errorf("remove ring %q: %w", name, err)
	}
	return nil
}

// ValidateRingMembers checks that every server and skill referenced by the
// ring exists in the registry.
func (s *Store) ValidateRingMembers(ring Ring) error {
	var missing []string
	for _, member := range ring.Members {
		if _, err := s.Get(strings.TrimSpace(member)); errors.Is(err, ErrNotFound) {
			missing = append(missing, member)
		} else if err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("ring %q references unknown servers: %s", ring.Name, strings.Join(missing, ", "))
	}

	var missingSkills []string
	for _, skill := range ring.Skills {
		if _, err := s.GetSkill(strings.TrimSpace(skill)); errors.Is(err, ErrSkillNotFound) {
			missingSkills = append(missingSkills, skill)
		} else if err != nil {
			return err
		}
	}
	if len(missingSkills) > 0 {
		sort.Strings(missingSkills)
		return fmt.Errorf("ring %q references unknown skills: %s", ring.Name, strings.Join(missingSkills, ", "))
	}
	return nil
}

// ValidateRingPolicy checks the registry-level prerequisites for a
// policy-required ring. Target-specific eligibility and compiler support are
// operation-level checks. The store requires every referenced server to carry
// an explicit non-empty exact allowlist when the ring selects access-policy
// enforcement; execution-only required rings do not invent that requirement.
func (s *Store) ValidateRingPolicy(ring Ring) error {
	if !ring.RequiresPolicyEnforcement() {
		return nil
	}
	manifests := make([]Manifest, 0, len(ring.Members))
	for _, member := range ring.Members {
		manifest, err := s.Get(strings.TrimSpace(member))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("ring %q policy requires unknown server %q", ring.Name, strings.TrimSpace(member))
			}
			return err
		}
		manifests = append(manifests, manifest)
	}
	return ValidateRequiredRingAccess(ring, manifests)
}

// ValidateRequiredRingAccess validates the registry-level bounded-access
// invariant against a manifest set. It is shared by store and snapshot paths
// so both reject an invalid required ring before writing anything.
func ValidateRequiredRingAccess(ring Ring, manifests []Manifest) error {
	byName := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		byName[manifest.Name] = manifest
	}
	return validateRequiredRingAccessAgainst(ring, byName)
}

func validateRequiredRingAccessAgainst(ring Ring, manifests map[string]Manifest) error {
	if !ring.RequiresPolicyEnforcement() {
		return nil
	}
	var missing []string
	selected := make([]Manifest, 0, len(ring.Members))
	for _, member := range ring.Members {
		name := strings.TrimSpace(member)
		manifest, exists := manifests[name]
		if !exists {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, manifest)
	}
	sort.Strings(missing)
	var errs []string
	if len(missing) > 0 {
		errs = append(errs, fmt.Sprintf("unknown servers: %s", strings.Join(missing, ", ")))
	}
	if ring.RequiresAccessPolicyEnforcement(selected) {
		var unbounded []string
		for _, manifest := range selected {
			if !manifest.HasExplicitToolAllowlist() {
				unbounded = append(unbounded, manifest.Name)
			}
		}
		sort.Strings(unbounded)
		if len(unbounded) > 0 {
			errs = append(errs, fmt.Sprintf("servers without an explicit non-empty allowed_tools allowlist: %s", strings.Join(unbounded, ", ")))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("ring %q requires policy enforcement but has %s", ring.Name, strings.Join(errs, "; "))
}

func (s *Store) pathForRing(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.RingsDir(), name+".toml"), nil
}

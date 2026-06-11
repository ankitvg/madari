package registry

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Ring is a named capability set of MCP servers. Members reference registry
// entries by name only; rings never embed command, args, or env — the server
// manifest stays the single source of truth.
type Ring struct {
	Name        string   `toml:"name" json:"name"`
	Members     []string `toml:"members" json:"members"`
	Description string   `toml:"description,omitempty" json:"description,omitempty"`
}

// Validate enforces ring-level invariants. Member existence in the registry
// is a store-level check; the parser cannot see the registry.
func (r Ring) Validate() error {
	var errs []string

	if err := validateServerName(r.Name); err != nil {
		errs = append(errs, err.Error())
	}

	if len(r.Members) == 0 {
		errs = append(errs, "at least one member is required")
	}
	seen := map[string]struct{}{}
	for _, member := range r.Members {
		if err := validateServerName(strings.TrimSpace(member)); err != nil {
			errs = append(errs, fmt.Sprintf("invalid member %q", member))
			continue
		}
		member = strings.TrimSpace(member)
		if _, exists := seen[member]; exists {
			errs = append(errs, fmt.Sprintf("duplicate member %q", member))
			continue
		}
		seen[member] = struct{}{}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid ring: %s", strings.Join(errs, "; "))
	}
	return nil
}

// HasMember reports whether name is a member of the ring.
func (r Ring) HasMember(name string) bool {
	name = strings.TrimSpace(name)
	for _, member := range r.Members {
		if strings.TrimSpace(member) == name {
			return true
		}
	}
	return false
}

// ParseRing parses a constrained TOML ring manifest and rejects unknown
// fields, mirroring ParseManifest's strictness.
func ParseRing(data []byte) (Ring, error) {
	ring := Ring{Members: []string{}}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = stripInlineComment(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			return Ring{}, fmt.Errorf("line %d: ring manifests have no sections", lineNo)
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return Ring{}, fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch key {
		case "name":
			sv, err := parseString(value)
			if err != nil {
				return Ring{}, fmt.Errorf("line %d: invalid name: %w", lineNo, err)
			}
			ring.Name = sv
		case "members":
			av, err := parseStringArray(value)
			if err != nil {
				return Ring{}, fmt.Errorf("line %d: invalid members: %w", lineNo, err)
			}
			ring.Members = av
		case "description":
			sv, err := parseString(value)
			if err != nil {
				return Ring{}, fmt.Errorf("line %d: invalid description: %w", lineNo, err)
			}
			ring.Description = sv
		default:
			return Ring{}, fmt.Errorf("line %d: unknown key %q", lineNo, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return Ring{}, fmt.Errorf("scan ring manifest: %w", err)
	}

	if err := ring.Validate(); err != nil {
		return Ring{}, err
	}
	return ring, nil
}

// MarshalRing renders a deterministic TOML ring manifest with sorted members.
func MarshalRing(ring Ring) ([]byte, error) {
	if err := ring.Validate(); err != nil {
		return nil, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(ring.Name))
	members := append([]string(nil), ring.Members...)
	sort.Strings(members)
	fmt.Fprintf(&b, "members = %s\n", formatStringArray(members))
	if strings.TrimSpace(ring.Description) != "" {
		fmt.Fprintf(&b, "description = %s\n", strconv.Quote(ring.Description))
	}
	return []byte(b.String()), nil
}

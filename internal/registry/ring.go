package registry

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Ring is a named capability set. Members reference server registry entries
// by name; Skills reference skill registry entries by name. Rings never embed
// command, args, env, or skill content — those primitives stay the source of
// truth.
type Ring struct {
	Name        string        `toml:"name" json:"name"`
	Members     []string      `toml:"members" json:"members"`
	Skills      []string      `toml:"skills,omitempty" json:"skills,omitempty"`
	Description string        `toml:"description,omitempty" json:"description,omitempty"`
	Contract    *RingContract `toml:"contract,omitempty" json:"contract,omitempty"`
}

// RingContract is advisory metadata that helps an orchestrating agent decide
// when to use a ring, what context to provide, and what response shape to
// expect. It does not affect sync, attach, render, or ownership behavior.
type RingContract struct {
	Summary         string   `toml:"summary,omitempty" json:"summary,omitempty"`
	GoodFor         []string `toml:"good_for,omitempty" json:"good_for,omitempty"`
	NotFor          []string `toml:"not_for,omitempty" json:"not_for,omitempty"`
	RequiredContext []string `toml:"required_context,omitempty" json:"required_context,omitempty"`
	OptionalContext []string `toml:"optional_context,omitempty" json:"optional_context,omitempty"`
	ExpectedOutputs []string `toml:"expected_outputs,omitempty" json:"expected_outputs,omitempty"`
}

func (c *RingContract) Empty() bool {
	return c == nil ||
		strings.TrimSpace(c.Summary) == "" &&
			len(c.GoodFor) == 0 &&
			len(c.NotFor) == 0 &&
			len(c.RequiredContext) == 0 &&
			len(c.OptionalContext) == 0 &&
			len(c.ExpectedOutputs) == 0
}

// Validate enforces ring-level invariants. Member existence in the registry
// is a store-level check; the parser cannot see the registry.
func (r Ring) Validate() error {
	var errs []string

	if err := validateServerName(r.Name); err != nil {
		errs = append(errs, err.Error())
	}

	if len(r.Members)+len(r.Skills) == 0 {
		errs = append(errs, "at least one member or skill is required")
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
	seenSkills := map[string]struct{}{}
	for _, skill := range r.Skills {
		if err := validateServerName(strings.TrimSpace(skill)); err != nil {
			errs = append(errs, fmt.Sprintf("invalid skill %q", skill))
			continue
		}
		skill = strings.TrimSpace(skill)
		if _, exists := seenSkills[skill]; exists {
			errs = append(errs, fmt.Sprintf("duplicate skill %q", skill))
			continue
		}
		seenSkills[skill] = struct{}{}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid ring: %s", strings.Join(errs, "; "))
	}
	return nil
}

// HasMember reports whether name is a server member of the ring.
func (r Ring) HasMember(name string) bool {
	name = strings.TrimSpace(name)
	for _, member := range r.Members {
		if strings.TrimSpace(member) == name {
			return true
		}
	}
	return false
}

// HasSkill reports whether name is a skill member of the ring.
func (r Ring) HasSkill(name string) bool {
	name = strings.TrimSpace(name)
	for _, skill := range r.Skills {
		if strings.TrimSpace(skill) == name {
			return true
		}
	}
	return false
}

// ParseRing parses a constrained TOML ring manifest and rejects unknown
// fields, mirroring ParseManifest's strictness.
func ParseRing(data []byte) (Ring, error) {
	ring := Ring{Members: []string{}, Skills: []string{}}

	section := sectionTop
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
			if !strings.HasSuffix(line, "]") {
				return Ring{}, fmt.Errorf("line %d: invalid section header", lineNo)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			switch name {
			case "contract":
				section = name
				if ring.Contract == nil {
					ring.Contract = &RingContract{}
				}
			default:
				return Ring{}, fmt.Errorf("line %d: unknown section %q", lineNo, name)
			}
			continue
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return Ring{}, fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch section {
		case sectionTop:
			if err := parseRingTopLevel(&ring, key, value); err != nil {
				return Ring{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "contract":
			if ring.Contract == nil {
				ring.Contract = &RingContract{}
			}
			if err := parseRingContract(ring.Contract, key, value); err != nil {
				return Ring{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
		default:
			return Ring{}, fmt.Errorf("line %d: unknown parse section", lineNo)
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

func parseRingTopLevel(ring *Ring, key, value string) error {
	switch key {
	case "name":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid name: %w", err)
		}
		ring.Name = sv
	case "members":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid members: %w", err)
		}
		ring.Members = av
	case "skills":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid skills: %w", err)
		}
		ring.Skills = av
	case "description":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid description: %w", err)
		}
		ring.Description = sv
	default:
		return fmt.Errorf("unknown top-level key %q", key)
	}
	return nil
}

func parseRingContract(contract *RingContract, key, value string) error {
	switch key {
	case "summary":
		sv, err := parseString(value)
		if err != nil {
			return fmt.Errorf("invalid contract summary: %w", err)
		}
		contract.Summary = sv
	case "good_for":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid contract good_for: %w", err)
		}
		contract.GoodFor = av
	case "not_for":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid contract not_for: %w", err)
		}
		contract.NotFor = av
	case "required_context":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid contract required_context: %w", err)
		}
		contract.RequiredContext = av
	case "optional_context":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid contract optional_context: %w", err)
		}
		contract.OptionalContext = av
	case "expected_outputs":
		av, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid contract expected_outputs: %w", err)
		}
		contract.ExpectedOutputs = av
	default:
		return fmt.Errorf("unknown key %q in [contract]", key)
	}
	return nil
}

// MarshalRing renders a deterministic TOML ring manifest with sorted members.
func MarshalRing(ring Ring) ([]byte, error) {
	if err := ring.Validate(); err != nil {
		return nil, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(ring.Name))
	if len(ring.Members) > 0 {
		members := append([]string(nil), ring.Members...)
		sort.Strings(members)
		fmt.Fprintf(&b, "members = %s\n", formatStringArray(members))
	}
	if len(ring.Skills) > 0 {
		skills := append([]string(nil), ring.Skills...)
		sort.Strings(skills)
		fmt.Fprintf(&b, "skills = %s\n", formatStringArray(skills))
	}
	if strings.TrimSpace(ring.Description) != "" {
		fmt.Fprintf(&b, "description = %s\n", strconv.Quote(ring.Description))
	}
	if !ring.Contract.Empty() {
		b.WriteString("\n[contract]\n")
		if strings.TrimSpace(ring.Contract.Summary) != "" {
			fmt.Fprintf(&b, "summary = %s\n", strconv.Quote(ring.Contract.Summary))
		}
		if len(ring.Contract.GoodFor) > 0 {
			fmt.Fprintf(&b, "good_for = %s\n", formatStringArray(ring.Contract.GoodFor))
		}
		if len(ring.Contract.NotFor) > 0 {
			fmt.Fprintf(&b, "not_for = %s\n", formatStringArray(ring.Contract.NotFor))
		}
		if len(ring.Contract.RequiredContext) > 0 {
			fmt.Fprintf(&b, "required_context = %s\n", formatStringArray(ring.Contract.RequiredContext))
		}
		if len(ring.Contract.OptionalContext) > 0 {
			fmt.Fprintf(&b, "optional_context = %s\n", formatStringArray(ring.Contract.OptionalContext))
		}
		if len(ring.Contract.ExpectedOutputs) > 0 {
			fmt.Fprintf(&b, "expected_outputs = %s\n", formatStringArray(ring.Contract.ExpectedOutputs))
		}
	}
	return []byte(b.String()), nil
}

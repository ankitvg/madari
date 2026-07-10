package registry

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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
	Policy      *RingPolicy   `toml:"policy,omitempty" json:"policy,omitempty"`
}

const (
	PolicyEnforcementRequired = "required"

	ExecutionAmbientEnvDeny               = "deny"
	ExecutionSandboxReadOnly              = "read-only"
	ExecutionCredentialExposureRunProcess = "run-process"
)

// RingPolicy declares the access-profile and execution guarantees requested by
// a ring. Enforcement is optional for advisory policies; when present, the
// only supported value requires every declared guarantee to be enforced.
type RingPolicy struct {
	Enforcement string           `toml:"enforcement,omitempty" json:"enforcement,omitempty"`
	Execution   *ExecutionPolicy `toml:"execution,omitempty" json:"execution,omitempty"`
}

// ExecutionPolicy declares the complete bounded-execution contract supported
// by this registry version. Partial execution policies are rejected so callers
// never have to infer omitted authority or lifetime semantics.
type ExecutionPolicy struct {
	AmbientEnv         string `toml:"ambient_env" json:"ambient_env"`
	Sandbox            string `toml:"sandbox" json:"sandbox"`
	MaxDuration        string `toml:"max_duration" json:"max_duration"`
	CredentialExposure string `toml:"credential_exposure" json:"credential_exposure"`
}

func (p *RingPolicy) Required() bool {
	return p != nil && p.Enforcement == PolicyEnforcementRequired
}

func (p *RingPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.Enforcement == "" && p.Execution == nil {
		return fmt.Errorf("policy enforcement must be %q", PolicyEnforcementRequired)
	}
	if p.Enforcement != "" && p.Enforcement != PolicyEnforcementRequired {
		return fmt.Errorf("policy enforcement must be %q when declared", PolicyEnforcementRequired)
	}
	if err := p.Execution.Validate(); err != nil {
		return err
	}
	return nil
}

func (p *ExecutionPolicy) Validate() error {
	if p == nil {
		return nil
	}

	var errs []string
	if p.AmbientEnv != ExecutionAmbientEnvDeny {
		errs = append(errs, fmt.Sprintf("ambient_env must be %q", ExecutionAmbientEnvDeny))
	}
	if p.Sandbox != ExecutionSandboxReadOnly {
		errs = append(errs, fmt.Sprintf("sandbox must be %q", ExecutionSandboxReadOnly))
	}
	if p.MaxDuration != strings.TrimSpace(p.MaxDuration) {
		errs = append(errs, "max_duration must not have leading or trailing whitespace")
	} else if duration, err := time.ParseDuration(p.MaxDuration); err != nil {
		errs = append(errs, "max_duration must be a Go duration")
	} else if duration <= 0 {
		errs = append(errs, "max_duration must be positive")
	}
	if p.CredentialExposure != ExecutionCredentialExposureRunProcess {
		errs = append(errs, fmt.Sprintf("credential_exposure must be %q", ExecutionCredentialExposureRunProcess))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid execution policy: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r Ring) RequiresPolicyEnforcement() bool {
	return r.Policy.Required()
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
	if err := r.Policy.Validate(); err != nil {
		errs = append(errs, err.Error())
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

// ParseRingContract parses a standalone contract TOML file. The file contains
// only contract fields, not a [contract] section.
func ParseRingContract(data []byte) (RingContract, error) {
	contract := RingContract{}

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
				return RingContract{}, fmt.Errorf("line %d: invalid section header", lineNo)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			return RingContract{}, fmt.Errorf("line %d: unknown section %q", lineNo, name)
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return RingContract{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if err := parseRingContractKey(&contract, key, value, "contract file"); err != nil {
			return RingContract{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return RingContract{}, fmt.Errorf("scan ring contract: %w", err)
	}
	if contract.Empty() {
		return RingContract{}, fmt.Errorf("contract file has no fields")
	}
	return contract, nil
}

// MarshalRingContract renders a standalone contract TOML file with fields in
// the same deterministic order used by ring manifests.
func MarshalRingContract(contract RingContract) ([]byte, error) {
	if contract.Empty() {
		return nil, fmt.Errorf("contract is empty")
	}

	var b strings.Builder
	writeRingContractFields(&b, &contract)
	return []byte(b.String()), nil
}

// ParseRing parses a constrained TOML ring manifest and rejects unknown
// fields, mirroring ParseManifest's strictness.
func ParseRing(data []byte) (Ring, error) {
	ring := Ring{Members: []string{}, Skills: []string{}}

	section := sectionTop
	policySectionSeen := false
	policyExecutionSectionSeen := false
	policyEnforcementSeen := false
	policyExecutionKeysSeen := map[string]bool{}
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
			case "policy":
				if policySectionSeen {
					return Ring{}, fmt.Errorf("line %d: duplicate section %q", lineNo, name)
				}
				policySectionSeen = true
				section = name
				if ring.Policy == nil {
					ring.Policy = &RingPolicy{}
				}
			case "policy.execution":
				if policyExecutionSectionSeen {
					return Ring{}, fmt.Errorf("line %d: duplicate section %q", lineNo, name)
				}
				policyExecutionSectionSeen = true
				section = name
				if ring.Policy == nil {
					ring.Policy = &RingPolicy{}
				}
				ring.Policy.Execution = &ExecutionPolicy{}
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
		case "policy":
			if key != "enforcement" {
				return Ring{}, fmt.Errorf("line %d: unknown key %q in [policy]", lineNo, key)
			}
			if policyEnforcementSeen {
				return Ring{}, fmt.Errorf("line %d: duplicate key %q in [policy]", lineNo, key)
			}
			parsed, err := parseString(value)
			if err != nil {
				return Ring{}, fmt.Errorf("line %d: invalid policy enforcement: %w", lineNo, err)
			}
			policyEnforcementSeen = true
			ring.Policy.Enforcement = parsed
		case "policy.execution":
			switch key {
			case "ambient_env", "sandbox", "max_duration", "credential_exposure":
			default:
				return Ring{}, fmt.Errorf("line %d: unknown key %q in [policy.execution]", lineNo, key)
			}
			if policyExecutionKeysSeen[key] {
				return Ring{}, fmt.Errorf("line %d: duplicate key %q in [policy.execution]", lineNo, key)
			}
			parsed, err := parseString(value)
			if err != nil {
				return Ring{}, fmt.Errorf("line %d: invalid execution policy %s: %w", lineNo, key, err)
			}
			switch key {
			case "ambient_env":
				ring.Policy.Execution.AmbientEnv = parsed
			case "sandbox":
				ring.Policy.Execution.Sandbox = parsed
			case "max_duration":
				ring.Policy.Execution.MaxDuration = parsed
			case "credential_exposure":
				ring.Policy.Execution.CredentialExposure = parsed
			}
			policyExecutionKeysSeen[key] = true
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
	return parseRingContractKey(contract, key, value, "[contract]")
}

func parseRingContractKey(contract *RingContract, key, value, context string) error {
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
		return fmt.Errorf("unknown key %q in %s", key, context)
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
		writeRingContractFields(&b, ring.Contract)
	}
	if ring.Policy != nil {
		if ring.Policy.Enforcement != "" {
			b.WriteString("\n[policy]\n")
			fmt.Fprintf(&b, "enforcement = %s\n", strconv.Quote(ring.Policy.Enforcement))
		}
		if ring.Policy.Execution != nil {
			b.WriteString("\n[policy.execution]\n")
			fmt.Fprintf(&b, "ambient_env = %s\n", strconv.Quote(ring.Policy.Execution.AmbientEnv))
			fmt.Fprintf(&b, "sandbox = %s\n", strconv.Quote(ring.Policy.Execution.Sandbox))
			fmt.Fprintf(&b, "max_duration = %s\n", strconv.Quote(ring.Policy.Execution.MaxDuration))
			fmt.Fprintf(&b, "credential_exposure = %s\n", strconv.Quote(ring.Policy.Execution.CredentialExposure))
		}
	}
	return []byte(b.String()), nil
}

func writeRingContractFields(b *strings.Builder, contract *RingContract) {
	if strings.TrimSpace(contract.Summary) != "" {
		fmt.Fprintf(b, "summary = %s\n", strconv.Quote(contract.Summary))
	}
	if len(contract.GoodFor) > 0 {
		fmt.Fprintf(b, "good_for = %s\n", formatStringArray(contract.GoodFor))
	}
	if len(contract.NotFor) > 0 {
		fmt.Fprintf(b, "not_for = %s\n", formatStringArray(contract.NotFor))
	}
	if len(contract.RequiredContext) > 0 {
		fmt.Fprintf(b, "required_context = %s\n", formatStringArray(contract.RequiredContext))
	}
	if len(contract.OptionalContext) > 0 {
		fmt.Fprintf(b, "optional_context = %s\n", formatStringArray(contract.OptionalContext))
	}
	if len(contract.ExpectedOutputs) > 0 {
		fmt.Fprintf(b, "expected_outputs = %s\n", formatStringArray(contract.ExpectedOutputs))
	}
}

package registry

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var agentSkillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Skill is a standalone Agent Skill package. Package-backed skills store their
// canonical metadata in SKILL.md frontmatter; the TOML parser below exists only
// for legacy flat skill reads.
type Skill struct {
	Name          string            `toml:"name" json:"name"`
	Description   string            `toml:"description,omitempty" json:"description,omitempty"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	AllowedTools  string            `json:"allowed_tools,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Validate enforces skill-level invariants.
func (s Skill) Validate() error {
	if err := validateServerName(s.Name); err != nil {
		return fmt.Errorf("invalid skill: %w", err)
	}
	return nil
}

// ValidateAgentSkillName enforces the official Agent Skills naming contract for
// new package-backed skills. Legacy flat TOML reads still use Validate.
func ValidateAgentSkillName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("name must be at most 64 characters")
	}
	if !agentSkillNamePattern.MatchString(name) {
		return fmt.Errorf("name must contain lowercase letters, numbers, and single hyphen separators")
	}
	return nil
}

// ParseSkill parses a constrained TOML skill manifest and rejects unknown
// fields, mirroring ring manifest strictness.
func ParseSkill(data []byte) (Skill, error) {
	var skill Skill

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
			return Skill{}, fmt.Errorf("line %d: skill manifests have no sections", lineNo)
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return Skill{}, fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch key {
		case "name":
			sv, err := parseString(value)
			if err != nil {
				return Skill{}, fmt.Errorf("line %d: invalid name: %w", lineNo, err)
			}
			skill.Name = sv
		case "description":
			sv, err := parseString(value)
			if err != nil {
				return Skill{}, fmt.Errorf("line %d: invalid description: %w", lineNo, err)
			}
			skill.Description = sv
		default:
			return Skill{}, fmt.Errorf("line %d: unknown key %q", lineNo, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return Skill{}, fmt.Errorf("scan skill manifest: %w", err)
	}

	if err := skill.Validate(); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

// MarshalSkill renders a deterministic TOML skill manifest.
func MarshalSkill(skill Skill) ([]byte, error) {
	if err := skill.Validate(); err != nil {
		return nil, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(skill.Name))
	if strings.TrimSpace(skill.Description) != "" {
		fmt.Fprintf(&b, "description = %s\n", strconv.Quote(skill.Description))
	}
	return []byte(b.String()), nil
}

func validateSkillContent(content []byte) error {
	if strings.TrimSpace(string(content)) == "" {
		return fmt.Errorf("skill content is required")
	}
	return nil
}

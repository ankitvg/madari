package registry

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Skill is a standalone instruction primitive. The TOML manifest stores only
// metadata; the managed Markdown body is stored alongside it by the Store.
type Skill struct {
	Name        string `toml:"name" json:"name"`
	Description string `toml:"description,omitempty" json:"description,omitempty"`
}

// Validate enforces skill-level invariants.
func (s Skill) Validate() error {
	if err := validateServerName(s.Name); err != nil {
		return fmt.Errorf("invalid skill: %w", err)
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

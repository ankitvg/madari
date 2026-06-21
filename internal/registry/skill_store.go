package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrSkillNotFound = errors.New("skill not found")

// SkillsDir returns the on-disk skills directory, a sibling of the servers
// directory under the same config root.
func (s *Store) SkillsDir() string {
	return filepath.Join(filepath.Dir(s.serversDir), "skills")
}

// AddSkill inserts a new skill; it fails if the skill already exists.
func (s *Store) AddSkill(skill Skill, content []byte) error {
	if err := skill.Validate(); err != nil {
		return err
	}
	if err := validateSkillContent(content); err != nil {
		return err
	}
	if _, err := s.GetSkill(skill.Name); err == nil {
		return fmt.Errorf("skill %q already exists", skill.Name)
	} else if !errors.Is(err, ErrSkillNotFound) {
		return err
	}
	return s.SaveSkill(skill, content)
}

// SaveSkill writes or updates a skill manifest and its managed Markdown body.
func (s *Store) SaveSkill(skill Skill, content []byte) error {
	if err := skill.Validate(); err != nil {
		return err
	}
	if err := validateSkillContent(content); err != nil {
		return err
	}
	if err := os.MkdirAll(s.SkillsDir(), 0o755); err != nil {
		return fmt.Errorf("ensure skills directory: %w", err)
	}

	metaPath, err := s.pathForSkill(skill.Name)
	if err != nil {
		return err
	}
	contentPath, err := s.SkillContentPath(skill.Name)
	if err != nil {
		return err
	}
	metaPayload, err := MarshalSkill(skill)
	if err != nil {
		return err
	}

	if err := writeFileAtomically(metaPath, metaPayload, 0o644); err != nil {
		return fmt.Errorf("save skill %q metadata: %w", skill.Name, err)
	}
	if err := writeFileAtomically(contentPath, content, 0o644); err != nil {
		return fmt.Errorf("save skill %q content: %w", skill.Name, err)
	}
	return nil
}

// GetSkill loads one skill manifest by name.
func (s *Store) GetSkill(name string) (Skill, error) {
	path, err := s.pathForSkill(name)
	if err != nil {
		return Skill{}, err
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Skill{}, ErrSkillNotFound
		}
		return Skill{}, fmt.Errorf("read skill %q: %w", name, err)
	}

	skill, err := ParseSkill(payload)
	if err != nil {
		return Skill{}, fmt.Errorf("parse skill %q: %w", name, err)
	}
	if skill.Name != name {
		return Skill{}, fmt.Errorf("skill %q has mismatched name %q", name, skill.Name)
	}
	return skill, nil
}

// GetSkillContent loads one skill's managed Markdown body by name.
func (s *Store) GetSkillContent(name string) ([]byte, error) {
	path, err := s.SkillContentPath(name)
	if err != nil {
		return nil, err
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSkillNotFound
		}
		return nil, fmt.Errorf("read skill %q content: %w", name, err)
	}
	if err := validateSkillContent(payload); err != nil {
		return nil, fmt.Errorf("read skill %q content: %w", name, err)
	}
	return payload, nil
}

// ListSkills returns all skills sorted by name.
func (s *Store) ListSkills() ([]Skill, error) {
	entries, err := os.ReadDir(s.SkillsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Skill{}, nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		skill, err := s.GetSkill(name)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// RemoveSkill deletes one skill manifest and its managed Markdown body.
func (s *Store) RemoveSkill(name string) error {
	metaPath, err := s.pathForSkill(name)
	if err != nil {
		return err
	}

	if err := os.Remove(metaPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSkillNotFound
		}
		return fmt.Errorf("remove skill %q metadata: %w", name, err)
	}

	contentPath, err := s.SkillContentPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(contentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove skill %q content: %w", name, err)
	}
	return nil
}

// SkillContentPath returns the managed Markdown file path for a skill.
func (s *Store) SkillContentPath(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.SkillsDir(), name+".md"), nil
}

func (s *Store) pathForSkill(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.SkillsDir(), name+".toml"), nil
}

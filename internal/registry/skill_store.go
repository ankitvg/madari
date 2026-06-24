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
	pkg, err := NewSkillPackageFromContent(skill, content)
	if err != nil {
		return err
	}
	return s.AddSkillPackage(pkg)
}

// SaveSkill writes or updates a skill manifest and its managed Markdown body.
func (s *Store) SaveSkill(skill Skill, content []byte) error {
	pkg, err := NewSkillPackageFromContent(skill, content)
	if err != nil {
		return err
	}
	return s.SaveSkillPackage(pkg)
}

func (s *Store) AddSkillPackage(pkg SkillPackage) error {
	if err := pkg.Skill.Validate(); err != nil {
		return err
	}
	if _, err := s.GetSkill(pkg.Skill.Name); err == nil {
		return fmt.Errorf("skill %q already exists", pkg.Skill.Name)
	} else if !errors.Is(err, ErrSkillNotFound) {
		return err
	}
	return s.SaveSkillPackage(pkg)
}

func (s *Store) SaveSkillPackage(pkg SkillPackage) error {
	normalized, err := NewSkillPackage(pkg.Files, pkg.Skill.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.SkillsDir(), 0o755); err != nil {
		return fmt.Errorf("ensure skills directory: %w", err)
	}
	if err := s.writeSkillPackage(normalized); err != nil {
		return err
	}
	_ = os.Remove(s.legacySkillManifestPath(normalized.Skill.Name))
	_ = os.Remove(s.legacySkillContentPath(normalized.Skill.Name))
	return nil
}

func (s *Store) writeSkillPackage(pkg SkillPackage) error {
	dest, err := s.SkillPackageDir(pkg.Skill.Name)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.SkillsDir(), "."+pkg.Skill.Name+"-")
	if err != nil {
		return fmt.Errorf("create temporary skill package: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()

	for _, file := range pkg.Files {
		path := filepath.Join(tmp, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("ensure skill package directory: %w", err)
		}
		if err := os.WriteFile(path, file.Content, file.Mode); err != nil {
			return fmt.Errorf("write skill package file %q: %w", file.Path, err)
		}
	}
	if err := replaceSkillPackageDir(dest, tmp); err != nil {
		return fmt.Errorf("replace skill package %q: %w", pkg.Skill.Name, err)
	}
	cleanup = false
	return nil
}

func replaceSkillPackageDir(dest, staged string) error {
	dest = filepath.Clean(dest)
	parent := filepath.Dir(dest)
	base := filepath.Base(dest)

	backup := ""
	if _, err := os.Lstat(dest); err == nil {
		var backupErr error
		backup, backupErr = unusedSkillPackageTempPath(parent, "."+base+".bak-*")
		if backupErr != nil {
			return backupErr
		}
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.Rename(staged, dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func unusedSkillPackageTempPath(dir, pattern string) (string, error) {
	path, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

// GetSkill loads one skill manifest by name.
func (s *Store) GetSkill(name string) (Skill, error) {
	dir, err := s.SkillPackageDir(name)
	if err != nil {
		return Skill{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, SkillFileName)); err == nil {
		pkg, err := NewSkillPackageFromDir(dir)
		if err != nil {
			return Skill{}, fmt.Errorf("parse skill package %q: %w", name, err)
		}
		return pkg.Skill, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Skill{}, fmt.Errorf("inspect skill package %q: %w", name, err)
	}
	return s.getLegacySkill(name)
}

// GetSkillContent loads one skill's managed Markdown body by name.
func (s *Store) GetSkillContent(name string) ([]byte, error) {
	dir, err := s.SkillPackageDir(name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, SkillFileName)); err == nil {
		pkg, err := NewSkillPackageFromDir(dir)
		if err != nil {
			return nil, fmt.Errorf("parse skill package %q: %w", name, err)
		}
		return pkg.SkillFileContent()
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect skill package %q: %w", name, err)
	}

	if _, err := s.getLegacySkill(name); err != nil {
		return nil, err
	}
	contentPath, err := s.legacySkillContentPathChecked(name)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(contentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSkillNotFound
		}
		return nil, fmt.Errorf("read skill %q content: %w", name, err)
	}
	return content, nil
}

func (s *Store) GetSkillPackage(name string) (SkillPackage, error) {
	dir, err := s.SkillPackageDir(name)
	if err != nil {
		return SkillPackage{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, SkillFileName)); err == nil {
		pkg, err := NewSkillPackageFromDir(dir)
		if err != nil {
			return SkillPackage{}, fmt.Errorf("parse skill package %q: %w", name, err)
		}
		return pkg, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return SkillPackage{}, fmt.Errorf("inspect skill package %q: %w", name, err)
	}
	return s.getLegacySkillPackage(name)
}

func (s *Store) getLegacySkillPackage(name string) (SkillPackage, error) {
	skill, err := s.getLegacySkill(name)
	if err != nil {
		return SkillPackage{}, err
	}
	contentPath, err := s.legacySkillContentPathChecked(name)
	if err != nil {
		return SkillPackage{}, err
	}
	content, err := os.ReadFile(contentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SkillPackage{}, ErrSkillNotFound
		}
		return SkillPackage{}, fmt.Errorf("read skill %q content: %w", name, err)
	}
	pkg, err := NewSkillPackageFromContent(skill, content)
	if err != nil {
		return SkillPackage{}, fmt.Errorf("convert legacy skill %q: %w", name, err)
	}
	return pkg, nil
}

func (s *Store) getLegacySkill(name string) (Skill, error) {
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

// ListSkills returns all skills sorted by name.
func (s *Store) ListSkills() ([]Skill, error) {
	entries, err := os.ReadDir(s.SkillsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Skill{}, nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	seen := map[string]struct{}{}
	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if err := ValidateAgentSkillName(name); err != nil {
				continue
			}
			skill, err := s.GetSkill(name)
			if err != nil {
				if !errors.Is(err, ErrSkillNotFound) {
					return nil, err
				}
				skill = Skill{Name: name}
			}
			seen[skill.Name] = struct{}{}
			skills = append(skills, skill)
			continue
		}
		if filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		if _, exists := seen[name]; exists {
			continue
		}
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

// RemoveSkill deletes one skill package plus any legacy flat files.
func (s *Store) RemoveSkill(name string) error {
	if _, err := s.GetSkill(name); err != nil {
		return err
	}
	dir, err := s.SkillPackageDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove skill %q package: %w", name, err)
	}
	if err := os.Remove(s.legacySkillManifestPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove skill %q metadata: %w", name, err)
	}
	if err := os.Remove(s.legacySkillContentPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove skill %q content: %w", name, err)
	}
	return nil
}

// SkillContentPath returns the managed SKILL.md file path for a skill.
func (s *Store) SkillContentPath(name string) (string, error) {
	dir, err := s.SkillPackageDir(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, SkillFileName)); err == nil {
		return filepath.Join(dir, SkillFileName), nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	legacyPath, err := s.legacySkillContentPathChecked(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Join(dir, SkillFileName), nil
}

func (s *Store) SkillPackageDir(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.SkillsDir(), name), nil
}

func (s *Store) pathForSkill(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return s.legacySkillManifestPath(name), nil
}

func (s *Store) legacySkillManifestPath(name string) string {
	return filepath.Join(s.SkillsDir(), name+".toml")
}

func (s *Store) legacySkillContentPathChecked(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	return s.legacySkillContentPath(name), nil
}

func (s *Store) legacySkillContentPath(name string) string {
	return filepath.Join(s.SkillsDir(), name+".md")
}

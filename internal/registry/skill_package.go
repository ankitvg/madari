package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const SkillFileName = "SKILL.md"

type SkillPackage struct {
	Skill Skill
	Files []SkillPackageFile
}

type SkillPackageFile struct {
	Path    string
	Content []byte
	Mode    os.FileMode
}

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
}

type skillPackageFileView struct {
	Path    string
	Content []byte
	Mode    os.FileMode
}

func NewSkillPackageFromDir(path string) (SkillPackage, error) {
	root := filepath.Clean(strings.TrimSpace(path))
	if root == "." || root == "" {
		return SkillPackage{}, fmt.Errorf("skill directory is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return SkillPackage{}, fmt.Errorf("inspect skill directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return SkillPackage{}, fmt.Errorf("skill source %q is not a directory", root)
	}

	files := []SkillPackageFile{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel, err = normalizeSkillPackagePath(rel)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill package file %q is a symlink", rel)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill package file %q is not a regular file", rel)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, SkillPackageFile{
			Path:    rel,
			Content: content,
			Mode:    normalizeSkillPackageMode(info.Mode()),
		})
		return nil
	}); err != nil {
		return SkillPackage{}, fmt.Errorf("read skill package %q: %w", root, err)
	}
	return NewSkillPackage(files, filepath.Base(root))
}

func NewSkillPackage(files []SkillPackageFile, parentName string) (SkillPackage, error) {
	normalized, err := normalizeSkillPackageFiles(files)
	if err != nil {
		return SkillPackage{}, err
	}
	skillFile, ok := skillPackageFileByPath(normalized, SkillFileName)
	if !ok {
		return SkillPackage{}, fmt.Errorf("skill package requires %s", SkillFileName)
	}
	skill, body, err := ParseSkillFile(skillFile.Content)
	if err != nil {
		return SkillPackage{}, err
	}
	if err := ValidateAgentSkillName(skill.Name); err != nil {
		return SkillPackage{}, fmt.Errorf("invalid skill: %w", err)
	}
	if expected := strings.TrimSpace(parentName); expected != "" && skill.Name != expected {
		return SkillPackage{}, fmt.Errorf("skill %q has mismatched directory name %q", skill.Name, expected)
	}
	if strings.TrimSpace(skill.Description) == "" {
		return SkillPackage{}, fmt.Errorf("skill %q requires a description", skill.Name)
	}
	if len(skill.Description) > 1024 {
		return SkillPackage{}, fmt.Errorf("skill %q description must be at most 1024 characters", skill.Name)
	}
	if strings.TrimSpace(skill.Compatibility) != "" && len(skill.Compatibility) > 500 {
		return SkillPackage{}, fmt.Errorf("skill %q compatibility must be at most 500 characters", skill.Name)
	}
	if strings.TrimSpace(string(body)) == "" {
		return SkillPackage{}, fmt.Errorf("skill %q body is empty", skill.Name)
	}
	return SkillPackage{Skill: skill, Files: normalized}, nil
}

func ParseSkillFile(content []byte) (Skill, []byte, error) {
	fmPayload, body, ok := splitCanonicalSkillFrontmatter(content)
	if !ok {
		return Skill{}, nil, fmt.Errorf("%s requires YAML frontmatter", SkillFileName)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(fmPayload, &raw); err != nil {
		return Skill{}, nil, fmt.Errorf("parse %s frontmatter: %w", SkillFileName, err)
	}
	for key := range raw {
		switch key {
		case "name", "description", "license", "compatibility", "metadata", "allowed-tools":
		default:
			return Skill{}, nil, fmt.Errorf("unknown %s frontmatter key %q", SkillFileName, key)
		}
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal(fmPayload, &fm); err != nil {
		return Skill{}, nil, fmt.Errorf("parse %s frontmatter: %w", SkillFileName, err)
	}
	if fm.Metadata == nil {
		fm.Metadata = map[string]string{}
	}
	return Skill{
		Name:          strings.TrimSpace(fm.Name),
		Description:   strings.TrimSpace(fm.Description),
		License:       strings.TrimSpace(fm.License),
		Compatibility: strings.TrimSpace(fm.Compatibility),
		AllowedTools:  strings.TrimSpace(fm.AllowedTools),
		Metadata:      normalizeSkillMetadata(fm.Metadata),
	}, body, nil
}

func NewSkillPackageFromContent(skill Skill, content []byte) (SkillPackage, error) {
	rendered, err := RenderSkillFile(skill, content)
	if err != nil {
		return SkillPackage{}, err
	}
	return NewSkillPackage([]SkillPackageFile{{
		Path:    SkillFileName,
		Content: rendered,
		Mode:    0o644,
	}}, strings.TrimSpace(skill.Name))
}

func RenderSkillFile(skill Skill, content []byte) ([]byte, error) {
	if err := skill.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(skill.Description) == "" {
		skill.Description = skill.Name
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, fmt.Errorf("skill content is required")
	}
	if sourceSkill, body, err := ParseSkillFile(content); err == nil {
		if skill.Name == "" {
			skill.Name = sourceSkill.Name
		}
		if strings.TrimSpace(skill.Description) == "" {
			skill.Description = sourceSkill.Description
		}
		if strings.TrimSpace(skill.License) == "" {
			skill.License = sourceSkill.License
		}
		if strings.TrimSpace(skill.Compatibility) == "" {
			skill.Compatibility = sourceSkill.Compatibility
		}
		if strings.TrimSpace(skill.AllowedTools) == "" {
			skill.AllowedTools = sourceSkill.AllowedTools
		}
		if len(skill.Metadata) == 0 {
			skill.Metadata = sourceSkill.Metadata
		}
		if skill.Name == sourceSkill.Name {
			return content, nil
		}
		content = body
	}
	return marshalSkillFile(skill, content)
}

func marshalSkillFile(skill Skill, body []byte) ([]byte, error) {
	if err := ValidateAgentSkillName(skill.Name); err != nil {
		return nil, fmt.Errorf("invalid skill: %w", err)
	}
	if strings.TrimSpace(skill.Description) == "" {
		return nil, fmt.Errorf("skill %q requires a description", skill.Name)
	}
	fm := skillFrontmatter{
		Name:          skill.Name,
		Description:   skill.Description,
		License:       strings.TrimSpace(skill.License),
		Compatibility: strings.TrimSpace(skill.Compatibility),
		Metadata:      normalizeSkillMetadata(skill.Metadata),
		AllowedTools:  strings.TrimSpace(skill.AllowedTools),
	}
	fmPayload, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal %s frontmatter: %w", SkillFileName, err)
	}
	fmPayload = bytes.ReplaceAll(fmPayload, []byte("allowedtools:"), []byte("allowed-tools:"))
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fmPayload)
	out.WriteString("---\n\n")
	out.Write(bytes.TrimLeft(body, "\r\n"))
	return out.Bytes(), nil
}

func splitCanonicalSkillFrontmatter(content []byte) ([]byte, []byte, bool) {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil, nil, false
	}
	lines := splitLinesWithEndings(text)
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, nil, false
	}
	var fm strings.Builder
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			body := []byte(strings.Join(lines[i+1:], ""))
			return []byte(fm.String()), body, true
		}
		fm.WriteString(lines[i])
	}
	return nil, nil, false
}

func splitLinesWithEndings(text string) []string {
	if text == "" {
		return nil
	}
	lines := []string{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		lines = append(lines, text[start:i+1])
		start = i + 1
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

func normalizeSkillPackageFiles(files []SkillPackageFile) ([]SkillPackageFile, error) {
	seen := map[string]struct{}{}
	normalized := make([]SkillPackageFile, 0, len(files))
	for _, file := range files {
		path, err := normalizeSkillPackagePath(file.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("duplicate skill package file %q", path)
		}
		seen[path] = struct{}{}
		normalized = append(normalized, SkillPackageFile{
			Path:    path,
			Content: append([]byte(nil), file.Content...),
			Mode:    normalizeSkillPackageMode(file.Mode),
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Path < normalized[j].Path
	})
	return normalized, nil
}

func normalizeSkillPackagePath(path string) (string, error) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" || path == "." {
		return "", fmt.Errorf("skill package file path is required")
	}
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("skill package file path %q must be relative", path)
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("skill package file path %q escapes package root", path)
	}
	return clean, nil
}

func normalizeSkillPackageMode(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func skillPackageFileByPath(files []SkillPackageFile, path string) (SkillPackageFile, bool) {
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return SkillPackageFile{}, false
}

func (p SkillPackage) SkillFileContent() ([]byte, error) {
	file, ok := skillPackageFileByPath(p.Files, SkillFileName)
	if !ok {
		return nil, ErrSkillNotFound
	}
	return append([]byte(nil), file.Content...), nil
}

func (p SkillPackage) Hash() string {
	h := sha256.New()
	for _, file := range p.Files {
		h.Write([]byte(file.Path))
		h.Write([]byte{0})
		h.Write([]byte(file.Mode.String()))
		h.Write([]byte{0})
		h.Write(file.Content)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeSkillMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

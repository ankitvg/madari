package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

type skillRootResolver func() (string, error)

type skillTargetRoots struct {
	project skillRootResolver
	user    skillRootResolver
}

func (r skillTargetRoots) supported() bool {
	return r.project != nil && r.user != nil
}

func (r skillTargetRoots) resolve(scope, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return syncshared.ResolvePath(override, nil)
	}
	switch strings.TrimSpace(scope) {
	case "", clients.ScopeProject:
		if r.project == nil {
			return "", errors.New("project skill root is not configured")
		}
		return r.project()
	case clients.ScopeUser:
		if r.user == nil {
			return "", errors.New("user skill root is not configured")
		}
		return r.user()
	default:
		return "", fmt.Errorf("unknown scope %q (supported: %s, %s)", scope, clients.ScopeProject, clients.ScopeUser)
	}
}

func defaultProjectSkillRoot(parts ...string) skillRootResolver {
	return func() (string, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		return filepath.Join(append([]string{cwd}, parts...)...), nil
	}
}

func defaultHomeSkillRoot(parts ...string) skillRootResolver {
	return func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return filepath.Join(append([]string{home}, parts...)...), nil
	}
}

type renderedSkill struct {
	Content    []byte
	SourceDesc string
}

func renderClientSkill(skill registry.Skill, content []byte) (renderedSkill, error) {
	if err := skill.Validate(); err != nil {
		return renderedSkill{}, err
	}

	fm, body := splitSkillFrontmatter(content)
	sourceDescription := frontmatterDescription(fm)
	description := strings.TrimSpace(skill.Description)
	if description == "" {
		description = sourceDescription
	}
	if strings.TrimSpace(description) == "" {
		return renderedSkill{}, fmt.Errorf("skill %q requires a description for client-native render", skill.Name)
	}

	body = bytes.TrimLeft(body, "\r\n")
	if strings.TrimSpace(string(body)) == "" {
		return renderedSkill{}, fmt.Errorf("skill %q content is empty after frontmatter normalization", skill.Name)
	}

	extras := frontmatterExtraLines(fm)
	var out bytes.Buffer
	out.WriteString("---\n")
	fmt.Fprintf(&out, "name: %s\n", yamlDoubleQuoted(skill.Name))
	fmt.Fprintf(&out, "description: %s\n", yamlDoubleQuoted(description))
	for _, line := range extras {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	out.WriteString("---\n\n")
	out.Write(body)
	return renderedSkill{Content: out.Bytes(), SourceDesc: sourceDescription}, nil
}

func splitSkillFrontmatter(content []byte) ([]string, []byte) {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil, content
	}

	lines := splitLinesWithEndings(text)
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") != "---" {
			continue
		}
		frontmatter := make([]string, 0, i-1)
		for _, line := range lines[1:i] {
			frontmatter = append(frontmatter, strings.TrimRight(line, "\r\n"))
		}
		body := []byte(strings.Join(lines[i+1:], ""))
		return frontmatter, body
	}
	return nil, content
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

var yamlTopLevelKeyPattern = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*:(.*)$`)

func frontmatterDescription(lines []string) string {
	for i := 0; i < len(lines); i++ {
		key, value, ok := yamlTopLevelKey(lines[i])
		if !ok || key != "description" {
			continue
		}
		value = strings.TrimSpace(value)
		switch value {
		case "", "|", ">":
			collected := collectIndentedYAMLValue(lines[i+1:])
			if value == "|" {
				return strings.TrimSpace(strings.Join(collected, "\n"))
			}
			return strings.TrimSpace(strings.Join(collected, " "))
		default:
			return strings.TrimSpace(unquoteYAMLScalar(value))
		}
	}
	return ""
}

func frontmatterExtraLines(lines []string) []string {
	extras := []string{}
	for i := 0; i < len(lines); {
		key, _, ok := yamlTopLevelKey(lines[i])
		if ok && (key == "name" || key == "description") {
			i++
			for i < len(lines) {
				if _, _, next := yamlTopLevelKey(lines[i]); next {
					break
				}
				if strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t") || strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
					i++
					continue
				}
				break
			}
			continue
		}
		extras = append(extras, lines[i])
		i++
	}
	for len(extras) > 0 && strings.TrimSpace(extras[0]) == "" {
		extras = extras[1:]
	}
	for len(extras) > 0 && strings.TrimSpace(extras[len(extras)-1]) == "" {
		extras = extras[:len(extras)-1]
	}
	return extras
}

func yamlTopLevelKey(line string) (key, value string, ok bool) {
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return "", "", false
	}
	match := yamlTopLevelKeyPattern.FindStringSubmatch(line)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

func collectIndentedYAMLValue(lines []string) []string {
	values := []string{}
	for _, line := range lines {
		if _, _, ok := yamlTopLevelKey(line); ok {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		values = append(values, strings.TrimSpace(line))
	}
	return values
}

func unquoteYAMLScalar(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			if out, err := strconvUnquote(value); err == nil {
				return out
			}
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
	}
	return value
}

func yamlDoubleQuoted(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func strconvUnquote(value string) (string, error) {
	var out strings.Builder
	escaped := false
	for i := 1; i < len(value)-1; i++ {
		ch := value[i]
		if escaped {
			switch ch {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			default:
				out.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		out.WriteByte(ch)
	}
	if escaped {
		out.WriteByte('\\')
	}
	return out.String(), nil
}

const skillAttachmentStateVersion = 1

type skillAttachmentEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type skillAttachmentStateFile struct {
	Version int                             `json:"version"`
	Skills  map[string]skillAttachmentEntry `json:"skills"`
}

func loadSkillAttachmentState(path string) (map[string]skillAttachmentEntry, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]skillAttachmentEntry{}, nil
		}
		return nil, fmt.Errorf("read skill attachment state %q: %w", path, err)
	}
	state := skillAttachmentStateFile{}
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("parse skill attachment state JSON: %w", err)
	}
	if state.Version != skillAttachmentStateVersion {
		return nil, fmt.Errorf("unsupported skill attachment state version %d in %q", state.Version, path)
	}
	return normalizeSkillAttachmentState(state.Skills), nil
}

func saveSkillAttachmentState(path string, skills map[string]skillAttachmentEntry) error {
	state := skillAttachmentStateFile{
		Version: skillAttachmentStateVersion,
		Skills:  normalizeSkillAttachmentState(skills),
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skill attachment state JSON: %w", err)
	}
	payload = append(payload, '\n')
	return syncshared.WriteFileAtomically(path, payload, 0o644)
}

func normalizeSkillAttachmentState(skills map[string]skillAttachmentEntry) map[string]skillAttachmentEntry {
	normalized := make(map[string]skillAttachmentEntry, len(skills))
	for name, entry := range skills {
		name = strings.TrimSpace(name)
		entry.Path = filepath.Clean(strings.TrimSpace(entry.Path))
		entry.Hash = strings.TrimSpace(entry.Hash)
		if name == "" || entry.Path == "." || entry.Hash == "" {
			continue
		}
		normalized[name] = entry
	}
	return normalized
}

func skillContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func sortedAttachedSkillNames(state map[string]skillAttachmentEntry) []string {
	names := make([]string, 0, len(state))
	for name := range state {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

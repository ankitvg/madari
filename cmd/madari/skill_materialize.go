package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func defaultVibeUserSkillRoot() skillRootResolver {
	return func() (string, error) {
		if vibeHome := strings.TrimSpace(os.Getenv("VIBE_HOME")); vibeHome != "" {
			resolved, err := syncshared.ExpandHome(vibeHome)
			if err != nil {
				return "", err
			}
			return filepath.Join(filepath.Clean(resolved), "skills"), nil
		}
		return defaultHomeSkillRoot(".vibe", "skills")()
	}
}

type renderedSkill struct {
	Content []byte
}

type skillAttachResult struct {
	SkillPath string
	SkillsDir string
	DryRun    bool
	Added     []string
	Updated   []string
	Removed   []string
	Unchanged []string
}

type skillAttachmentRef struct {
	target string
	scope  string
	path   string
	source string
}

func skillTargetByName(target string) (clientTarget, error) {
	ct, ok := clientTargetByName(target)
	if !ok || !ct.skillRoots.supported() {
		return clientTarget{}, commandInputError("skill", fmt.Sprintf("unsupported skill target %q (supported: %s)", target, strings.Join(supportedSkillTargets(), ", ")))
	}
	return ct, nil
}

func (a cliApp) skillAttachmentStatePath(target, scope string) string {
	suffix := "-skills-managed.json"
	if scope == clients.ScopeUser {
		suffix = "-skills-user-managed.json"
	}
	return filepath.Join(filepath.Dir(a.store.ServersDir()), "state", target+suffix)
}

func (a cliApp) skillAttachmentRefs() []skillAttachmentRef {
	refs := []skillAttachmentRef{}
	for _, target := range supportedSkillTargets() {
		refs = append(refs, skillAttachmentRef{
			target: target,
			path:   a.skillAttachmentStatePath(target, ""),
		})
		refs = append(refs, skillAttachmentRef{
			target: target,
			scope:  clients.ScopeUser,
			path:   a.skillAttachmentStatePath(target, clients.ScopeUser),
		})
	}
	return refs
}

func (a cliApp) ensureSkillNotAttached(name string) error {
	holders := []skillAttachmentRef{}
	for _, ref := range a.skillAttachmentRefs() {
		state, err := loadSkillAttachmentState(ref.path)
		if err != nil {
			return err
		}
		for _, entry := range skillAttachmentsByName(state, name) {
			for _, source := range entry.Sources {
				holders = append(holders, skillAttachmentRef{
					target: ref.target,
					scope:  ref.scope,
					path:   entry.Path,
					source: source,
				})
			}
		}
	}
	if len(holders) == 0 {
		return nil
	}

	guidance := make([]string, 0, len(holders))
	for _, holder := range holders {
		command := fmt.Sprintf("madari skill detach %s %s", name, holder.target)
		if ring, ok := syncshared.RingNameFromSource(holder.source); ok {
			command = fmt.Sprintf("madari ring detach %s %s", ring, holder.target)
		}
		if holder.scope == clients.ScopeUser {
			command += " --scope user"
		}
		if holder.path != "" && holder.source == syncshared.SourceStandalone {
			command += fmt.Sprintf(" --skills-dir %s", filepath.Dir(filepath.Dir(holder.path)))
		}
		guidance = append(guidance, command)
	}
	return fmt.Errorf("skill %q is still attached; detach first: %s", name, strings.Join(guidance, "; "))
}

func (a cliApp) attachSkill(name, target, scope, skillsDir string, dryRun bool) (skillAttachResult, error) {
	return a.attachSkillSource(name, target, scope, skillsDir, syncshared.SourceStandalone, dryRun)
}

func (a cliApp) attachSkillSource(name, target, scope, skillsDir, source string, dryRun bool) (skillAttachResult, error) {
	ct, err := skillTargetByName(target)
	if err != nil {
		return skillAttachResult{}, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return skillAttachResult{}, fmt.Errorf("skill attachment source is required")
	}
	skill, err := a.store.GetSkill(name)
	if err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return skillAttachResult{}, fmt.Errorf("skill %q not found", name)
		}
		return skillAttachResult{}, err
	}
	content, err := a.store.GetSkillContent(name)
	if err != nil {
		return skillAttachResult{}, err
	}
	rendered, err := renderClientSkill(skill, content)
	if err != nil {
		return skillAttachResult{}, err
	}
	root, err := ct.skillRoots.resolve(scope, skillsDir)
	if err != nil {
		return skillAttachResult{}, err
	}
	root = filepath.Clean(root)
	skillPath := filepath.Join(root, name, "SKILL.md")
	statePath := a.skillAttachmentStatePath(target, scope)
	state, err := loadSkillAttachmentState(statePath)
	if err != nil {
		return skillAttachResult{}, err
	}

	desiredHash := skillContentHash(rendered.Content)
	result := skillAttachResult{
		SkillPath: skillPath,
		SkillsDir: root,
		DryRun:    dryRun,
	}

	stateKey, entry, owned := findSkillAttachment(state, name, skillPath)
	var previousContent []byte
	hadPreviousContent := false
	needsFileWrite := false
	if owned {
		current, readErr := os.ReadFile(skillPath)
		switch {
		case readErr == nil:
			previousContent = append([]byte(nil), current...)
			hadPreviousContent = true
			if skillContentHash(current) != entry.Hash {
				return skillAttachResult{}, fmt.Errorf("refusing to update modified skill file %s; detach or restore it manually", skillPath)
			}
			hasSource := skillAttachmentHasSource(entry, source)
			switch {
			case desiredHash == entry.Hash && hasSource:
				result.Unchanged = []string{name}
			case desiredHash == entry.Hash:
				result.Updated = []string{name}
			default:
				result.Updated = []string{name}
				needsFileWrite = true
			}
		case errors.Is(readErr, os.ErrNotExist):
			result.Added = []string{name}
			needsFileWrite = true
		default:
			return skillAttachResult{}, fmt.Errorf("read skill file %s: %w", skillPath, readErr)
		}
	} else {
		if _, statErr := os.Stat(skillPath); statErr == nil {
			return skillAttachResult{}, fmt.Errorf("refusing to overwrite unmanaged skill file %s", skillPath)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return skillAttachResult{}, fmt.Errorf("inspect skill file %s: %w", skillPath, statErr)
		}
		result.Added = []string{name}
		needsFileWrite = true
	}

	if dryRun || len(result.Unchanged) > 0 {
		return result, nil
	}
	if needsFileWrite {
		if err := syncshared.WriteFileAtomically(skillPath, rendered.Content, 0o644); err != nil {
			return skillAttachResult{}, fmt.Errorf("write skill file %s: %w", skillPath, err)
		}
	}
	entry.Name = name
	entry.Path = skillPath
	entry.Hash = desiredHash
	entry.Sources = skillAttachmentSourcesWith(entry.Sources, source)
	state[stateKey] = entry
	if err := saveSkillAttachmentStateFunc(statePath, state); err != nil {
		if needsFileWrite {
			if rollbackErr := rollbackSkillFileWrite(skillPath, previousContent, hadPreviousContent); rollbackErr != nil {
				return skillAttachResult{}, fmt.Errorf("write skill attachment state: %w; rollback skill file %s: %v", err, skillPath, rollbackErr)
			}
		}
		return skillAttachResult{}, fmt.Errorf("write skill attachment state: %w", err)
	}
	return result, nil
}

func (a cliApp) detachSkill(name, target, scope, skillsDir string, dryRun bool) (skillAttachResult, error) {
	return a.detachSkillSource(name, target, scope, skillsDir, syncshared.SourceStandalone, dryRun)
}

func (a cliApp) detachSkillSource(name, target, scope, skillsDir, source string, dryRun bool) (skillAttachResult, error) {
	ct, err := skillTargetByName(target)
	if err != nil {
		return skillAttachResult{}, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return skillAttachResult{}, fmt.Errorf("skill attachment source is required")
	}
	statePath := a.skillAttachmentStatePath(target, scope)
	state, err := loadSkillAttachmentState(statePath)
	if err != nil {
		return skillAttachResult{}, err
	}

	root, rootErr := ct.skillRoots.resolve(scope, skillsDir)
	if rootErr != nil {
		return skillAttachResult{}, rootErr
	}
	root = filepath.Clean(root)
	expectedSkillPath := filepath.Join(root, name, "SKILL.md")

	stateKey, entry, owned := findSkillAttachment(state, name, expectedSkillPath)
	if !owned {
		if matches := skillAttachmentsByName(state, name); len(matches) > 0 {
			locations := make([]string, 0, len(matches))
			for _, match := range matches {
				locations = append(locations, match.Path)
			}
			return skillAttachResult{}, fmt.Errorf("skill %q is attached to %s, but not at %s; attached paths: %s", name, target, expectedSkillPath, strings.Join(locations, ", "))
		}
		return skillAttachResult{
			SkillPath: expectedSkillPath,
			SkillsDir: root,
			DryRun:    dryRun,
			Unchanged: []string{name},
		}, nil
	}
	if !skillAttachmentHasSource(entry, source) {
		return skillAttachResult{
			SkillPath: expectedSkillPath,
			SkillsDir: root,
			DryRun:    dryRun,
			Unchanged: []string{name},
		}, nil
	}

	skillPath := filepath.Clean(entry.Path)
	root = filepath.Dir(filepath.Dir(skillPath))
	remainingSources := skillAttachmentSourcesWithout(entry.Sources, source)
	result := skillAttachResult{
		SkillPath: skillPath,
		SkillsDir: root,
		DryRun:    dryRun,
		Removed:   []string{name},
	}

	current, readErr := os.ReadFile(skillPath)
	switch {
	case readErr == nil:
		if skillContentHash(current) != entry.Hash {
			return skillAttachResult{}, fmt.Errorf("refusing to remove modified skill file %s; remove it manually if desired", skillPath)
		}
	case errors.Is(readErr, os.ErrNotExist):
		// Clear stale ownership state below.
	default:
		return skillAttachResult{}, fmt.Errorf("read skill file %s: %w", skillPath, readErr)
	}

	if dryRun {
		return result, nil
	}
	if len(remainingSources) == 0 && readErr == nil {
		if err := os.Remove(skillPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return skillAttachResult{}, fmt.Errorf("remove skill file %s: %w", skillPath, err)
		}
		if err := os.Remove(filepath.Dir(skillPath)); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
			return skillAttachResult{}, fmt.Errorf("remove empty skill directory %s: %w", filepath.Dir(skillPath), err)
		}
	}
	if len(remainingSources) == 0 {
		delete(state, stateKey)
	} else {
		entry.Sources = remainingSources
		state[stateKey] = entry
	}
	if err := saveSkillAttachmentStateFunc(statePath, state); err != nil {
		return skillAttachResult{}, err
	}
	return result, nil
}

func (a cliApp) detachSkillSourceAll(target, scope, source string, dryRun bool) (skillAttachResult, error) {
	if _, err := skillTargetByName(target); err != nil {
		return skillAttachResult{}, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return skillAttachResult{}, fmt.Errorf("skill attachment source is required")
	}

	statePath := a.skillAttachmentStatePath(target, scope)
	state, err := loadSkillAttachmentState(statePath)
	if err != nil {
		return skillAttachResult{}, err
	}

	result := skillAttachResult{DryRun: dryRun}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	changed := false
	for _, key := range keys {
		entry := state[key]
		if !skillAttachmentHasSource(entry, source) {
			continue
		}
		skillPath := filepath.Clean(entry.Path)
		current, readErr := os.ReadFile(skillPath)
		switch {
		case readErr == nil:
			if skillContentHash(current) != entry.Hash {
				return skillAttachResult{}, fmt.Errorf("refusing to remove modified skill file %s; remove it manually if desired", skillPath)
			}
		case errors.Is(readErr, os.ErrNotExist):
			// Clear stale ownership state below.
		default:
			return skillAttachResult{}, fmt.Errorf("read skill file %s: %w", skillPath, readErr)
		}

		result.SkillsDir = filepath.Dir(filepath.Dir(skillPath))
		result.Removed = appendUniqueName(result.Removed, entry.Name)
		changed = true
		if dryRun {
			continue
		}

		remainingSources := skillAttachmentSourcesWithout(entry.Sources, source)
		if len(remainingSources) == 0 && readErr == nil {
			if err := os.Remove(skillPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return skillAttachResult{}, fmt.Errorf("remove skill file %s: %w", skillPath, err)
			}
			if err := os.Remove(filepath.Dir(skillPath)); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
				return skillAttachResult{}, fmt.Errorf("remove empty skill directory %s: %w", filepath.Dir(skillPath), err)
			}
		}
		if len(remainingSources) == 0 {
			delete(state, key)
		} else {
			entry.Sources = remainingSources
			state[key] = entry
		}
	}

	if dryRun || !changed {
		return result, nil
	}
	if err := saveSkillAttachmentStateFunc(statePath, state); err != nil {
		return skillAttachResult{}, err
	}
	return result, nil
}

func isDirectoryNotEmpty(err error) bool {
	return strings.Contains(err.Error(), "directory not empty") || strings.Contains(err.Error(), "not empty")
}

func printSkillAttachSummary(out io.Writer, target, scope string, result skillAttachResult) {
	if scope == "" {
		scope = clients.ScopeProject
	}
	fmt.Fprintf(out, "target: %s\n", target)
	fmt.Fprintf(out, "scope: %s\n", scope)
	fmt.Fprintf(out, "skills_dir: %s\n", result.SkillsDir)
	fmt.Fprintf(out, "skill_file: %s\n", result.SkillPath)
	if result.DryRun {
		fmt.Fprintln(out, "dry-run: true")
	}
	fmt.Fprintf(out, "added: %s\n", formatNameList(result.Added))
	fmt.Fprintf(out, "updated: %s\n", formatNameList(result.Updated))
	fmt.Fprintf(out, "removed: %s\n", formatNameList(result.Removed))
	fmt.Fprintf(out, "unchanged: %s\n", formatNameList(result.Unchanged))
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
	return renderedSkill{Content: out.Bytes()}, nil
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
		if style, ok := yamlBlockScalarStyle(value); ok || value == "" {
			collected := collectIndentedYAMLValue(lines[i+1:])
			if style == "|" {
				return strings.TrimSpace(strings.Join(collected, "\n"))
			}
			return strings.TrimSpace(strings.Join(collected, " "))
		}
		return strings.TrimSpace(unquoteYAMLScalar(value))
	}
	return ""
}

func yamlBlockScalarStyle(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	style := value[0]
	if style != '|' && style != '>' {
		return "", false
	}
	for _, r := range value[1:] {
		if r == '-' || r == '+' || (r >= '0' && r <= '9') {
			continue
		}
		return "", false
	}
	return string(style), true
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

const skillAttachmentStateVersion = 3

var saveSkillAttachmentStateFunc = saveSkillAttachmentState

type skillAttachmentEntry struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Hash    string   `json:"hash"`
	Sources []string `json:"sources"`
}

type skillAttachmentStateFile struct {
	Version int                    `json:"version"`
	Skills  []skillAttachmentEntry `json:"skills"`
}

func loadSkillAttachmentState(path string) (map[string]skillAttachmentEntry, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]skillAttachmentEntry{}, nil
		}
		return nil, fmt.Errorf("read skill attachment state %q: %w", path, err)
	}

	probe := struct {
		Version int             `json:"version"`
		Skills  json.RawMessage `json:"skills"`
	}{}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, fmt.Errorf("parse skill attachment state JSON: %w", err)
	}

	switch probe.Version {
	case 1:
		legacy := map[string]skillAttachmentEntry{}
		if len(probe.Skills) > 0 {
			if err := json.Unmarshal(probe.Skills, &legacy); err != nil {
				return nil, fmt.Errorf("parse skill attachment state v1 entries: %w", err)
			}
		}
		return normalizeSkillAttachmentState(legacy, []string{syncshared.SourceStandalone}), nil
	case 2:
		entries := []skillAttachmentEntry{}
		if len(probe.Skills) > 0 {
			if err := json.Unmarshal(probe.Skills, &entries); err != nil {
				return nil, fmt.Errorf("parse skill attachment state v2 entries: %w", err)
			}
		}
		return normalizeSkillAttachmentEntries(entries, []string{syncshared.SourceStandalone}), nil
	case skillAttachmentStateVersion:
		entries := []skillAttachmentEntry{}
		if len(probe.Skills) > 0 {
			if err := json.Unmarshal(probe.Skills, &entries); err != nil {
				return nil, fmt.Errorf("parse skill attachment state v3 entries: %w", err)
			}
		}
		return normalizeSkillAttachmentEntries(entries, nil), nil
	default:
		return nil, fmt.Errorf("unsupported skill attachment state version %d in %q", probe.Version, path)
	}
}

func saveSkillAttachmentState(path string, skills map[string]skillAttachmentEntry) error {
	state := skillAttachmentStateFile{
		Version: skillAttachmentStateVersion,
		Skills:  sortedSkillAttachmentEntries(normalizeSkillAttachmentState(skills, nil)),
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skill attachment state JSON: %w", err)
	}
	payload = append(payload, '\n')
	return syncshared.WriteFileAtomically(path, payload, 0o644)
}

func normalizeSkillAttachmentState(skills map[string]skillAttachmentEntry, defaultSources []string) map[string]skillAttachmentEntry {
	normalized := make(map[string]skillAttachmentEntry, len(skills))
	for key, entry := range skills {
		if strings.TrimSpace(entry.Name) == "" {
			entry.Name = strings.TrimSpace(key)
		}
		normalizedEntry, ok := normalizeSkillAttachmentEntry(entry, defaultSources)
		if !ok {
			continue
		}
		normalized[skillAttachmentKey(normalizedEntry.Name, normalizedEntry.Path)] = normalizedEntry
	}
	return normalized
}

func normalizeSkillAttachmentEntries(entries []skillAttachmentEntry, defaultSources []string) map[string]skillAttachmentEntry {
	normalized := make(map[string]skillAttachmentEntry, len(entries))
	for _, entry := range entries {
		normalizedEntry, ok := normalizeSkillAttachmentEntry(entry, defaultSources)
		if !ok {
			continue
		}
		normalized[skillAttachmentKey(normalizedEntry.Name, normalizedEntry.Path)] = normalizedEntry
	}
	return normalized
}

func normalizeSkillAttachmentEntry(entry skillAttachmentEntry, defaultSources []string) (skillAttachmentEntry, bool) {
	entry.Name = strings.TrimSpace(entry.Name)
	entry.Path = filepath.Clean(strings.TrimSpace(entry.Path))
	entry.Hash = strings.TrimSpace(entry.Hash)
	entry.Sources = normalizeSkillAttachmentSources(entry.Sources)
	if len(entry.Sources) == 0 {
		entry.Sources = normalizeSkillAttachmentSources(defaultSources)
	}
	if entry.Name == "" || entry.Path == "." || entry.Hash == "" || len(entry.Sources) == 0 {
		return skillAttachmentEntry{}, false
	}
	return entry, true
}

func sortedSkillAttachmentEntries(state map[string]skillAttachmentEntry) []skillAttachmentEntry {
	entries := make([]skillAttachmentEntry, 0, len(state))
	for _, entry := range state {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Path < entries[j].Path
	})
	return entries
}

func skillAttachmentKey(name, path string) string {
	return strings.TrimSpace(name) + "\x00" + filepath.Clean(strings.TrimSpace(path))
}

func normalizeSkillAttachmentSources(sources []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}
		normalized = append(normalized, source)
	}
	sort.Strings(normalized)
	return normalized
}

func skillAttachmentHasSource(entry skillAttachmentEntry, source string) bool {
	source = strings.TrimSpace(source)
	for _, candidate := range entry.Sources {
		if strings.TrimSpace(candidate) == source {
			return true
		}
	}
	return false
}

func skillAttachmentSourcesWith(sources []string, source string) []string {
	source = strings.TrimSpace(source)
	if source == "" {
		return normalizeSkillAttachmentSources(sources)
	}
	return normalizeSkillAttachmentSources(append(append([]string(nil), sources...), source))
}

func skillAttachmentSourcesWithout(sources []string, source string) []string {
	source = strings.TrimSpace(source)
	remaining := make([]string, 0, len(sources))
	for _, candidate := range sources {
		if strings.TrimSpace(candidate) == source {
			continue
		}
		remaining = append(remaining, candidate)
	}
	return normalizeSkillAttachmentSources(remaining)
}

func findSkillAttachment(state map[string]skillAttachmentEntry, name, path string) (string, skillAttachmentEntry, bool) {
	key := skillAttachmentKey(name, path)
	entry, ok := state[key]
	return key, entry, ok
}

func skillAttachmentsByName(state map[string]skillAttachmentEntry, name string) []skillAttachmentEntry {
	name = strings.TrimSpace(name)
	matches := []skillAttachmentEntry{}
	for _, entry := range state {
		if entry.Name == name {
			matches = append(matches, entry)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func rollbackSkillFileWrite(path string, previous []byte, hadPrevious bool) error {
	if hadPrevious {
		return syncshared.WriteFileAtomically(path, previous, 0o644)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Dir(path)); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
		return err
	}
	return nil
}

func skillContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func sortedAttachedSkillNames(state map[string]skillAttachmentEntry) []string {
	seen := map[string]struct{}{}
	for _, entry := range state {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendUniqueName(names []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return names
	}
	for _, existing := range names {
		if existing == name {
			return names
		}
	}
	names = append(names, name)
	sort.Strings(names)
	return names
}

func mergeSkillAttachResult(dst *skillAttachResult, src skillAttachResult) {
	if dst.SkillsDir == "" {
		dst.SkillsDir = src.SkillsDir
	}
	if dst.SkillPath == "" {
		dst.SkillPath = src.SkillPath
	}
	dst.DryRun = dst.DryRun || src.DryRun
	for _, name := range src.Added {
		dst.Added = appendUniqueName(dst.Added, name)
	}
	for _, name := range src.Updated {
		dst.Updated = appendUniqueName(dst.Updated, name)
	}
	for _, name := range src.Removed {
		dst.Removed = appendUniqueName(dst.Removed, name)
	}
	for _, name := range src.Unchanged {
		dst.Unchanged = appendUniqueName(dst.Unchanged, name)
	}
}

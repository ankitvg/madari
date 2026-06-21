package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func TestRenderClientSkillSynthesizesFrontmatter(t *testing.T) {
	rendered, err := renderClientSkill(
		registry.Skill{Name: "release", Description: "Release workflow"},
		[]byte("# Release\n\nCut a patch release.\n"),
	)
	if err != nil {
		t.Fatalf("render client skill: %v", err)
	}

	want := `---
name: "release"
description: "Release workflow"
---

# Release

Cut a patch release.
`
	if string(rendered.Content) != want {
		t.Fatalf("client skill render drift:\nwant:\n%s\ngot:\n%s", want, rendered.Content)
	}
}

func TestRenderClientSkillNormalizesExistingFrontmatter(t *testing.T) {
	content := `---
name: old-name
description: Source description
allowed-tools:
  - Read
disable-model-invocation: true
---

# Release
Use the release checklist.
`
	rendered, err := renderClientSkill(registry.Skill{Name: "release"}, []byte(content))
	if err != nil {
		t.Fatalf("render client skill: %v", err)
	}

	got := string(rendered.Content)
	for _, want := range []string{
		`name: "release"`,
		`description: "Source description"`,
		"allowed-tools:\n  - Read",
		"disable-model-invocation: true",
		"# Release\nUse the release checklist.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered skill:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old-name") {
		t.Fatalf("expected old name to be replaced, got:\n%s", got)
	}
	if strings.Count(got, "description:") != 1 {
		t.Fatalf("expected one description field, got:\n%s", got)
	}
}

func TestRenderClientSkillNormalizesBlockScalarDescription(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        string
	}{
		{
			name: "folded chomp",
			description: `description: >-
  Source release
  workflow`,
			want: `description: "Source release workflow"`,
		},
		{
			name: "literal chomp",
			description: `description: |-
  Source release
  workflow`,
			want: `description: "Source release\nworkflow"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := "---\nname: old-name\n" + tt.description + "\nallowed-tools:\n  - Read\n---\n\n# Release\nUse the release checklist.\n"
			rendered, err := renderClientSkill(registry.Skill{Name: "release"}, []byte(content))
			if err != nil {
				t.Fatalf("render client skill: %v", err)
			}

			got := string(rendered.Content)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("expected %q in rendered skill:\n%s", tt.want, got)
			}
			if strings.Contains(got, `description: ">`) || strings.Contains(got, `description: "|`) {
				t.Fatalf("expected block scalar description to be parsed, got:\n%s", got)
			}
			if !strings.Contains(got, "allowed-tools:\n  - Read") {
				t.Fatalf("expected unrelated frontmatter to be preserved, got:\n%s", got)
			}
		})
	}
}

func TestRenderClientSkillRequiresDescriptionAndBody(t *testing.T) {
	if _, err := renderClientSkill(registry.Skill{Name: "release"}, []byte("# Release\n")); err == nil || !strings.Contains(err.Error(), "requires a description") {
		t.Fatalf("expected missing description error, got: %v", err)
	}

	frontmatterOnly := []byte("---\ndescription: Release workflow\n---\n\n")
	if _, err := renderClientSkill(registry.Skill{Name: "release"}, frontmatterOnly); err == nil || !strings.Contains(err.Error(), "content is empty") {
		t.Fatalf("expected empty body error, got: %v", err)
	}
}

func TestSkillAttachmentStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "codex-skills-managed.json")
	skillPath := filepath.Join(t.TempDir(), ".agents", "skills", "release", "SKILL.md")
	state := map[string]skillAttachmentEntry{
		skillAttachmentKey("release", skillPath): {
			Name:    "release",
			Path:    skillPath,
			Hash:    "abc123",
			Sources: []string{syncshared.RingSource("zeta"), syncshared.SourceStandalone, syncshared.RingSource("alpha"), syncshared.RingSource("zeta")},
		},
	}

	if err := saveSkillAttachmentState(path, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := loadSkillAttachmentState(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	want := skillAttachmentEntry{
		Name:    "release",
		Path:    filepath.Clean(skillPath),
		Hash:    "abc123",
		Sources: []string{syncshared.RingSource("alpha"), syncshared.RingSource("zeta"), syncshared.SourceStandalone},
	}
	if !reflect.DeepEqual(got[skillAttachmentKey("release", skillPath)], want) {
		t.Fatalf("state roundtrip mismatch: got=%+v want=%+v", got[skillAttachmentKey("release", skillPath)], want)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state payload: %v", err)
	}
	for _, wantText := range []string{
		`"version": 3`,
		`"sources": [`,
		`"ring:alpha"`,
		`"ring:zeta"`,
		`"standalone"`,
	} {
		if !strings.Contains(string(payload), wantText) {
			t.Fatalf("expected %q in state payload:\n%s", wantText, payload)
		}
	}
}

func TestSkillAttachmentStateLoadsV1NameKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "codex-skills-managed.json")
	skillPath := filepath.Join(t.TempDir(), ".agents", "skills", "release", "SKILL.md")
	payload := `{
  "version": 1,
  "skills": {
    "release": {
      "path": "` + filepath.ToSlash(skillPath) + `",
      "hash": "abc123"
    }
  }
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	got, err := loadSkillAttachmentState(path)
	if err != nil {
		t.Fatalf("load legacy state: %v", err)
	}
	entry, ok := got[skillAttachmentKey("release", skillPath)]
	if !ok {
		t.Fatalf("expected normalized legacy entry, got=%+v", got)
	}
	if entry.Name != "release" || entry.Path != filepath.Clean(skillPath) || entry.Hash != "abc123" || !reflect.DeepEqual(entry.Sources, []string{syncshared.SourceStandalone}) {
		t.Fatalf("unexpected legacy entry: %+v", entry)
	}
}

func TestSkillAttachmentStateLoadsV2ListAsStandalone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "codex-skills-managed.json")
	skillPath := filepath.Join(t.TempDir(), ".agents", "skills", "release", "SKILL.md")
	payload := `{
  "version": 2,
  "skills": [
    {
      "name": "release",
      "path": "` + filepath.ToSlash(skillPath) + `",
      "hash": "abc123"
    }
  ]
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write v2 state: %v", err)
	}

	got, err := loadSkillAttachmentState(path)
	if err != nil {
		t.Fatalf("load v2 state: %v", err)
	}
	entry, ok := got[skillAttachmentKey("release", skillPath)]
	if !ok {
		t.Fatalf("expected normalized v2 entry, got=%+v", got)
	}
	if entry.Name != "release" || entry.Path != filepath.Clean(skillPath) || entry.Hash != "abc123" || !reflect.DeepEqual(entry.Sources, []string{syncshared.SourceStandalone}) {
		t.Fatalf("unexpected v2 entry: %+v", entry)
	}
}

func TestSkillAttachmentStateRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "codex-skills-managed.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"skills":[]}`), 0o644); err != nil {
		t.Fatalf("write unknown state: %v", err)
	}
	if _, err := loadSkillAttachmentState(path); err == nil || !strings.Contains(err.Error(), "unsupported skill attachment state version 99") {
		t.Fatalf("expected unknown version error, got: %v", err)
	}
}

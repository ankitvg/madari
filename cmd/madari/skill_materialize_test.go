package main

import (
	"path/filepath"
	"strings"
	"testing"

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
	state := map[string]skillAttachmentEntry{
		"release": {
			Path: filepath.Join(t.TempDir(), ".agents", "skills", "release", "SKILL.md"),
			Hash: "abc123",
		},
	}

	if err := saveSkillAttachmentState(path, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := loadSkillAttachmentState(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got["release"] != state["release"] {
		t.Fatalf("state roundtrip mismatch: got=%+v want=%+v", got, state)
	}
}

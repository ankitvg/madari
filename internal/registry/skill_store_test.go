package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillStoreLifecycle(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	skill := Skill{Name: "release", Description: "Release workflow"}
	content := []byte("# Release\n\nFollow the checklist.\n")

	if err := store.AddSkill(skill, content); err != nil {
		t.Fatalf("add skill: %v", err)
	}
	if err := store.AddSkill(skill, content); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate add error, got: %v", err)
	}

	got, err := store.GetSkill("release")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if got != skill {
		t.Fatalf("unexpected skill: %#v", got)
	}
	gotContent, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("get skill content: %v", err)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("unexpected skill content: %q", gotContent)
	}

	if err := store.SaveSkill(Skill{Name: "release", Description: "Updated"}, []byte("new instructions")); err != nil {
		t.Fatalf("save skill: %v", err)
	}
	updated, err := store.GetSkill("release")
	if err != nil {
		t.Fatalf("get updated skill: %v", err)
	}
	if updated.Description != "Updated" {
		t.Fatalf("expected updated description, got: %#v", updated)
	}

	skills, err := store.ListSkills()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "release" {
		t.Fatalf("unexpected skills: %#v", skills)
	}

	if err := store.RemoveSkill("release"); err != nil {
		t.Fatalf("remove skill: %v", err)
	}
	if _, err := store.GetSkill("release"); !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("expected ErrSkillNotFound after removal, got: %v", err)
	}
	if _, err := store.GetSkillContent("release"); !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("expected content removal, got: %v", err)
	}
	if err := store.RemoveSkill("release"); !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("expected ErrSkillNotFound for second removal, got: %v", err)
	}
}

func TestSkillStoreRejectsEmptyContent(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))

	err := store.AddSkill(Skill{Name: "release"}, []byte("\n"))
	if err == nil || !strings.Contains(err.Error(), "skill content is required") {
		t.Fatalf("expected content validation error, got: %v", err)
	}
	if _, getErr := store.GetSkill("release"); !errors.Is(getErr, ErrSkillNotFound) {
		t.Fatalf("expected no skill written after validation failure, got: %v", getErr)
	}
}

func TestListSkillsEmptyWithoutDirectory(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	skills, err := store.ListSkills()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected no skills, got: %#v", skills)
	}
}

func TestGetSkillRejectsMismatchedName(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "release"}, []byte("# Release\n")); err != nil {
		t.Fatalf("save skill: %v", err)
	}

	oldPath := filepath.Join(store.SkillsDir(), "release.toml")
	newPath := filepath.Join(store.SkillsDir(), "renamed.toml")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename skill file: %v", err)
	}

	_, err := store.GetSkill("renamed")
	if err == nil || !strings.Contains(err.Error(), "mismatched name") {
		t.Fatalf("expected mismatched-name error, got: %v", err)
	}
}

func TestSkillsDirIsSiblingOfServersDir(t *testing.T) {
	store := NewStore("/tmp/example/servers")
	if got := store.SkillsDir(); got != filepath.Clean("/tmp/example/skills") {
		t.Fatalf("unexpected skills dir: %s", got)
	}
	path, err := store.SkillContentPath("release")
	if err != nil {
		t.Fatalf("skill content path: %v", err)
	}
	if path != filepath.Clean("/tmp/example/skills/release.md") {
		t.Fatalf("unexpected content path: %s", path)
	}
}

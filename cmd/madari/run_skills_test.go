package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMaterializeRunSkillsUsesTargetRunSkillRoot(t *testing.T) {
	store := newTestStore(t)
	saveTestSkillPackage(t, store, "release", "Release workflow")
	runRoot := t.TempDir()

	result, err := materializeRunSkills(store, "codex", []runPlanSkill{{Name: "release"}}, runRoot)
	if err != nil {
		t.Fatalf("materialize run skills: %v", err)
	}
	if result.Target != "codex" || result.SkillsDir != filepath.Join(runRoot, ".agents", "skills") || !slices.Equal(result.Skills, []string{"release"}) {
		t.Fatalf("unexpected materialization result: %#v", result)
	}
	for _, path := range []string{
		filepath.Join(runRoot, ".agents", "skills", "release", "SKILL.md"),
		filepath.Join(runRoot, ".agents", "skills", "release", "references", "CHECKLIST.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected materialized skill file %s: %v", path, err)
		}
	}
	stateDir := filepath.Join(filepath.Dir(store.ServersDir()), "state")
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("run skill materialization should not create managed state, stat err=%v", err)
	}
}

func TestMaterializeRunSkillsUsesNonCodexTargetRoot(t *testing.T) {
	store := newTestStore(t)
	saveTestSkillPackage(t, store, "release", "Release workflow")
	runRoot := t.TempDir()

	result, err := materializeRunSkills(store, "gemini", []runPlanSkill{{Name: "release"}}, runRoot)
	if err != nil {
		t.Fatalf("materialize run skills: %v", err)
	}
	if result.SkillsDir != filepath.Join(runRoot, ".gemini", "skills") {
		t.Fatalf("expected Gemini run skill root, got: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(runRoot, ".gemini", "skills", "release", "SKILL.md")); err != nil {
		t.Fatalf("expected materialized Gemini run skill: %v", err)
	}
}

func TestMaterializeRunSkillsRejectsUnsupportedTarget(t *testing.T) {
	store := newTestStore(t)
	saveTestSkillPackage(t, store, "release", "Release workflow")

	_, err := materializeRunSkills(store, "claude-desktop", []runPlanSkill{{Name: "release"}}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "claude-desktop run does not support run skill materialization") {
		t.Fatalf("expected unsupported run skill target error, got: %v", err)
	}
}

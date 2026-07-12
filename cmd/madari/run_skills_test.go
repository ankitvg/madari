package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/launch"
	"github.com/ankitvg/madari/internal/registry"
)

func TestMaterializeRunSkillsUsesTargetRunSkillRoot(t *testing.T) {
	store := newTestStore(t)
	saveTestSkillPackage(t, store, "release", "Release workflow")
	runRoot := t.TempDir()

	artifact := testRunSkillArtifact(t, store, "codex", "release")
	result, err := materializeRunSkills(artifact, runRoot)
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

	artifact := testRunSkillArtifact(t, store, "gemini", "release")
	result, err := materializeRunSkills(artifact, runRoot)
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

	artifact := testRunSkillArtifact(t, store, "claude-desktop", "release")
	_, err := materializeRunSkills(artifact, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "claude-desktop run does not support run skill materialization") {
		t.Fatalf("expected unsupported run skill target error, got: %v", err)
	}
}

func testRunSkillArtifact(t *testing.T, store *registry.Store, target string, names ...string) *launch.Artifact {
	t.Helper()
	packages := make([]registry.SkillPackage, 0, len(names))
	for _, name := range names {
		pkg, err := store.GetSkillPackage(name)
		if err != nil {
			t.Fatalf("load skill package %s: %v", name, err)
		}
		packages = append(packages, pkg)
	}
	artifact, err := launch.Compile(launch.Input{
		Target:           target,
		WorkingDirectory: t.TempDir(),
		Prompt:           "test prompt",
		Skills:           packages,
	})
	if err != nil {
		t.Fatalf("compile launch artifact: %v", err)
	}
	return artifact
}

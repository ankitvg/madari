package registry

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(got, skill) {
		t.Fatalf("unexpected skill: %#v", got)
	}
	gotContent, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("get skill content: %v", err)
	}
	if !strings.Contains(string(gotContent), "name: release") || !strings.Contains(string(gotContent), "Follow the checklist.") {
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

func TestSkillPackageFromDirWithBundledFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release")
	if err := os.MkdirAll(filepath.Join(root, "references"), 0o755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	skillMD := "---\nname: release\ndescription: Release workflow\nlicense: MIT\ncompatibility: Requires git\nallowed-tools: Bash(git:*) Read\nmetadata:\n  owner: platform\n---\n\n# Release\n"
	if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "references", "CHECKLIST.md"), []byte("checklist\n"), 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}
	scriptPath := filepath.Join(root, "scripts", "release.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	pkg, err := NewSkillPackageFromDir(root)
	if err != nil {
		t.Fatalf("read skill package: %v", err)
	}
	if pkg.Skill.Name != "release" || pkg.Skill.Description != "Release workflow" || pkg.Skill.License != "MIT" || pkg.Skill.Metadata["owner"] != "platform" {
		t.Fatalf("unexpected package metadata: %+v", pkg.Skill)
	}
	if len(pkg.Files) != 3 || pkg.Files[2].Path != "scripts/release.sh" || pkg.Files[2].Mode != 0o755 {
		t.Fatalf("unexpected package files: %+v", pkg.Files)
	}
}

func TestSkillPackageFromCurrentDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: release\ndescription: Release\n---\n\n# Release\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir package: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	pkg, err := NewSkillPackageFromDir(".")
	if err != nil {
		t.Fatalf("read current directory package: %v", err)
	}
	if pkg.Skill.Name != "release" {
		t.Fatalf("unexpected package: %+v", pkg.Skill)
	}
}

func TestSkillPackageFromContentRewritesChangedFrontmatter(t *testing.T) {
	content := []byte("---\nname: release\ndescription: Source description\nallowed-tools: Read\n---\n\n# Release\n")

	pkg, err := NewSkillPackageFromContent(Skill{Name: "release", Description: "Updated description"}, content)
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	if pkg.Skill.Description != "Updated description" {
		t.Fatalf("expected override description, got: %+v", pkg.Skill)
	}
	rendered, err := pkg.SkillFileContent()
	if err != nil {
		t.Fatalf("skill content: %v", err)
	}
	if !strings.Contains(string(rendered), "description: Updated description") || strings.Contains(string(rendered), "Source description") {
		t.Fatalf("expected frontmatter rewritten, got:\n%s", rendered)
	}
}

func TestSkillPackageRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		setup func(string)
		want  string
	}{
		{
			name: "missing skill file",
			setup: func(root string) {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			want: "requires SKILL.md",
		},
		{
			name: "invalid name",
			setup: func(root string) {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: BadName\ndescription: Bad\n---\n\n# Bad\n"), 0o644); err != nil {
					t.Fatalf("write skill: %v", err)
				}
			},
			want: "invalid skill",
		},
		{
			name: "unknown frontmatter key",
			setup: func(root string) {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: release\ndescription: Release\nextra: nope\n---\n\n# Release\n"), 0o644); err != nil {
					t.Fatalf("write skill: %v", err)
				}
			},
			want: "unknown SKILL.md frontmatter key",
		},
		{
			name: "missing description",
			setup: func(root string) {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: release\n---\n\n# Release\n"), 0o644); err != nil {
					t.Fatalf("write skill: %v", err)
				}
			},
			want: "requires a description",
		},
		{
			name: "empty body",
			setup: func(root string) {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: release\ndescription: Release\n---\n\n"), 0o644); err != nil {
					t.Fatalf("write skill: %v", err)
				}
			},
			want: "body is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "release")
			tt.setup(root)
			_, err := NewSkillPackageFromDir(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got: %v", tt.want, err)
			}
		})
	}
}

func TestSkillPackageRejectsUnsafeFilePaths(t *testing.T) {
	validSkill := []byte("---\nname: release\ndescription: Release\n---\n\n# Release\n")
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "path escape", path: "../SKILL.md", want: "escapes package root"},
		{name: "absolute path", path: "/tmp/SKILL.md", want: "must be relative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSkillPackage([]SkillPackageFile{{
				Path:    tt.path,
				Content: validSkill,
				Mode:    0o644,
			}}, "release")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got: %v", tt.want, err)
			}
		})
	}
}

func TestSkillPackageRejectsSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, SkillFileName), []byte("---\nname: release\ndescription: Release\n---\n\n# Release\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.Symlink(SkillFileName, filepath.Join(root, "linked.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := NewSkillPackageFromDir(root)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("expected symlink rejection, got: %v", err)
	}
}

func TestListSkillsSkipsHiddenAndInvalidPackageDirectories(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "release", Description: "Release"}, []byte("# Release\n")); err != nil {
		t.Fatalf("save skill: %v", err)
	}
	for _, dir := range []string{".release.tmp", ".release.bak-123", "BadName"} {
		if err := os.MkdirAll(filepath.Join(store.SkillsDir(), dir), 0o755); err != nil {
			t.Fatalf("mkdir stray dir %s: %v", dir, err)
		}
	}

	skills, err := store.ListSkills()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "release" {
		t.Fatalf("unexpected skills: %+v", skills)
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

func TestLegacyFlatSkillWithDottedNameStillReadsAndRemoves(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := os.MkdirAll(store.SkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "release.patch.toml"), []byte("name = \"release.patch\"\ndescription = \"Patch release\"\n"), 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}
	legacyContentPath := filepath.Join(store.SkillsDir(), "release.patch.md")
	if err := os.WriteFile(legacyContentPath, []byte("# Patch\n"), 0o644); err != nil {
		t.Fatalf("write legacy content: %v", err)
	}

	skill, err := store.GetSkill("release.patch")
	if err != nil {
		t.Fatalf("get dotted legacy skill: %v", err)
	}
	if skill.Name != "release.patch" || skill.Description != "Patch release" {
		t.Fatalf("unexpected skill: %+v", skill)
	}
	content, err := store.GetSkillContent("release.patch")
	if err != nil {
		t.Fatalf("get dotted legacy content: %v", err)
	}
	if string(content) != "# Patch\n" {
		t.Fatalf("unexpected content: %q", content)
	}
	contentPath, err := store.SkillContentPath("release.patch")
	if err != nil {
		t.Fatalf("content path: %v", err)
	}
	if contentPath != legacyContentPath {
		t.Fatalf("expected legacy content path %s, got %s", legacyContentPath, contentPath)
	}
	skills, err := store.ListSkills()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "release.patch" {
		t.Fatalf("unexpected listed skills: %+v", skills)
	}
	if err := store.RemoveSkill("release.patch"); err != nil {
		t.Fatalf("remove dotted legacy skill: %v", err)
	}
	if _, err := os.Stat(legacyContentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy content removed, got: %v", err)
	}
}

func TestSkillContentPathReportsLegacyContentBeforeMigration(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := os.MkdirAll(store.SkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "release.toml"), []byte("name = \"release\"\ndescription = \"Release\"\n"), 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}
	legacyContentPath := filepath.Join(store.SkillsDir(), "release.md")
	if err := os.WriteFile(legacyContentPath, []byte("# Release\n"), 0o644); err != nil {
		t.Fatalf("write legacy content: %v", err)
	}
	got, err := store.SkillContentPath("release")
	if err != nil {
		t.Fatalf("content path: %v", err)
	}
	if got != legacyContentPath {
		t.Fatalf("expected legacy content path %s, got %s", legacyContentPath, got)
	}
}

func TestGetSkillRejectsMismatchedName(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := os.MkdirAll(store.SkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "renamed.toml"), []byte("name = \"release\"\n"), 0o644); err != nil {
		t.Fatalf("write legacy skill metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "renamed.md"), []byte("# Release\n"), 0o644); err != nil {
		t.Fatalf("write legacy skill content: %v", err)
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
	if path != filepath.Clean("/tmp/example/skills/release/SKILL.md") {
		t.Fatalf("unexpected content path: %s", path)
	}
}

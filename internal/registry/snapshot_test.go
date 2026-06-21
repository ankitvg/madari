package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotExportParseRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{
		Name:    "alpha",
		Command: "/usr/bin/env",
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save alpha manifest: %v", err)
	}
	if err := store.Save(Manifest{
		Name:    "beta",
		Command: "/usr/bin/env",
		Enabled: false,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save beta manifest: %v", err)
	}
	if err := store.SaveRing(Ring{
		Name:        "research",
		Members:     []string{"beta", "alpha"},
		Skills:      []string{"release"},
		Description: "Research helpers",
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	if err := store.SaveSkill(Skill{
		Name:        "release",
		Description: "Release workflow",
	}, []byte("# Release\n\nCut a patch release.\n")); err != nil {
		t.Fatalf("save skill: %v", err)
	}

	snapshot, err := ExportSnapshot(store)
	if err != nil {
		t.Fatalf("export snapshot failed: %v", err)
	}
	if snapshot.Version != SnapshotVersion {
		t.Fatalf("expected snapshot version %d, got %d", SnapshotVersion, snapshot.Version)
	}

	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot failed: %v", err)
	}

	parsed, err := ParseSnapshotJSON(payload)
	if err != nil {
		t.Fatalf("parse snapshot failed: %v", err)
	}
	if len(parsed.Servers) != 2 {
		t.Fatalf("expected 2 servers in parsed snapshot, got %d", len(parsed.Servers))
	}
	if parsed.Servers[0].Name != "alpha" && parsed.Servers[1].Name != "alpha" {
		t.Fatalf("expected alpha in parsed servers: %#v", parsed.Servers)
	}
	if len(parsed.Rings) != 1 || parsed.Rings[0].Name != "research" {
		t.Fatalf("expected research ring in parsed snapshot, got: %#v", parsed.Rings)
	}
	if got := strings.Join(parsed.Rings[0].Members, ","); got != "alpha,beta" {
		t.Fatalf("expected deterministic ring members, got: %s", got)
	}
	if got := strings.Join(parsed.Rings[0].Skills, ","); got != "release" {
		t.Fatalf("expected deterministic ring skills, got: %s", got)
	}
	if len(parsed.Skills) != 1 || parsed.Skills[0].Name != "release" {
		t.Fatalf("expected release skill in parsed snapshot, got: %#v", parsed.Skills)
	}
	if parsed.Skills[0].Content != "# Release\n\nCut a patch release.\n" {
		t.Fatalf("unexpected skill content: %q", parsed.Skills[0].Content)
	}
}

func TestMarshalSnapshotUsesSnakeCaseKeys(t *testing.T) {
	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{
				Name:    "alpha",
				Command: "/usr/bin/env",
				Args:    []string{"--stdio"},
				Enabled: true,
				Clients: []string{"claude-desktop"},
				RequiredEnv: RequiredEnv{
					Keys: []string{"SMTP_PASSWORD"},
				},
			},
		},
		Rings: []Ring{
			{
				Name:        "research",
				Members:     []string{"alpha"},
				Skills:      []string{"release"},
				Description: "Research helpers",
			},
		},
		Skills: []SnapshotSkill{
			{
				Name:        "release",
				Description: "Release workflow",
				Content:     "# Release\n",
			},
		},
	}

	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot failed: %v", err)
	}
	text := string(payload)
	for _, key := range []string{`"name"`, `"command"`, `"args"`, `"enabled"`, `"clients"`, `"required_env"`, `"keys"`, `"rings"`, `"members"`, `"skills"`, `"content"`, `"description"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("expected payload to contain key %s, payload=%s", key, text)
		}
	}
	for _, legacy := range []string{`"Name"`, `"Command"`, `"Args"`, `"Enabled"`, `"Clients"`, `"RequiredEnv"`, `"Keys"`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("expected payload to omit legacy key %s, payload=%s", legacy, text)
		}
	}
}

func TestParseSnapshotV1TreatsMissingRingsAsEmpty(t *testing.T) {
	payload := []byte(`{"version":1,"servers":[{"name":"alpha","command":"/usr/bin/env","enabled":true,"clients":["claude-desktop"]}]}`)
	snapshot, err := ParseSnapshotJSON(payload)
	if err != nil {
		t.Fatalf("parse v1 snapshot: %v", err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected v1 snapshot to keep version 1, got %d", snapshot.Version)
	}
	if len(snapshot.Rings) != 0 {
		t.Fatalf("expected missing v1 rings to parse as empty, got: %#v", snapshot.Rings)
	}
	if len(snapshot.Skills) != 0 {
		t.Fatalf("expected missing v1 skills to parse as empty, got: %#v", snapshot.Skills)
	}
}

func TestParseSnapshotV2TreatsMissingSkillsAsEmpty(t *testing.T) {
	payload := []byte(`{"version":2,"servers":[{"name":"alpha","command":"/usr/bin/env","enabled":true,"clients":["claude-desktop"]}],"rings":[{"name":"research","members":["alpha"]}]}`)
	snapshot, err := ParseSnapshotJSON(payload)
	if err != nil {
		t.Fatalf("parse v2 snapshot: %v", err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("expected v2 snapshot to keep version 2, got %d", snapshot.Version)
	}
	if len(snapshot.Rings) != 1 {
		t.Fatalf("expected v2 rings to parse, got: %#v", snapshot.Rings)
	}
	if len(snapshot.Skills) != 0 {
		t.Fatalf("expected missing v2 skills to parse as empty, got: %#v", snapshot.Skills)
	}
}

func TestParseSnapshotV3RejectsRingSkills(t *testing.T) {
	payload := []byte(`{"version":3,"servers":[],"rings":[{"name":"research","skills":["release"]}],"skills":[{"name":"release","content":"# Release\n"}]}`)
	_, err := ParseSnapshotJSON(payload)
	if err == nil || !strings.Contains(err.Error(), "does not support ring skills") {
		t.Fatalf("expected v3 ring-skills error, got: %v", err)
	}
}

func TestImportSnapshotDryRunAndApply(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{
		Name:    "alpha",
		Command: "/usr/bin/env",
		Enabled: true,
		Clients: []string{"claude-desktop"},
	}); err != nil {
		t.Fatalf("save initial manifest: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{
				Name:    "alpha",
				Command: "/bin/echo",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
			{
				Name:    "beta",
				Command: "/usr/bin/env",
				Enabled: true,
				Clients: []string{"claude-desktop"},
			},
		},
	}

	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if len(dryRunResult.Added) != 1 || dryRunResult.Added[0] != "beta" {
		t.Fatalf("expected beta added in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.Updated) != 1 || dryRunResult.Updated[0] != "alpha" {
		t.Fatalf("expected alpha updated in dry-run, got: %+v", dryRunResult)
	}

	alphaAfterDryRun, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after dry-run: %v", err)
	}
	if alphaAfterDryRun.Command != "/usr/bin/env" {
		t.Fatalf("expected dry-run not to change store")
	}
	if _, err := store.Get("beta"); err == nil {
		t.Fatalf("expected dry-run not to create beta")
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import failed: %v", err)
	}
	if len(applyResult.Added) != 1 || applyResult.Added[0] != "beta" {
		t.Fatalf("expected beta added in apply, got: %+v", applyResult)
	}
	if len(applyResult.Updated) != 1 || applyResult.Updated[0] != "alpha" {
		t.Fatalf("expected alpha updated in apply, got: %+v", applyResult)
	}

	alphaAfterApply, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("load alpha after apply: %v", err)
	}
	if alphaAfterApply.Command != "/bin/echo" {
		t.Fatalf("expected alpha command to be updated, got: %q", alphaAfterApply.Command)
	}
	if _, err := store.Get("beta"); err != nil {
		t.Fatalf("expected beta to exist after apply: %v", err)
	}
}

func TestImportSnapshotRingsDryRunAndApply(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	for _, manifest := range []Manifest{
		{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
		{Name: "beta", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
	} {
		if err := store.Save(manifest); err != nil {
			t.Fatalf("save manifest %s: %v", manifest.Name, err)
		}
	}
	for _, ring := range []Ring{
		{Name: "research", Members: []string{"alpha"}, Description: "old"},
		{Name: "stable", Members: []string{"alpha"}, Description: "same"},
		{Name: "local", Members: []string{"alpha"}, Description: "preserved"},
	} {
		if err := store.SaveRing(ring); err != nil {
			t.Fatalf("save ring %s: %v", ring.Name, err)
		}
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{Name: "gamma", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
		},
		Rings: []Ring{
			{Name: "portable", Members: []string{"gamma", "alpha"}, Description: "new"},
			{Name: "research", Members: []string{"beta", "alpha"}, Description: "new"},
			{Name: "stable", Members: []string{"alpha"}, Description: "same"},
		},
	}

	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if len(dryRunResult.RingsAdded) != 1 || dryRunResult.RingsAdded[0] != "portable" {
		t.Fatalf("expected portable ring added in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.RingsUpdated) != 1 || dryRunResult.RingsUpdated[0] != "research" {
		t.Fatalf("expected research ring updated in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.RingsUnchanged) != 1 || dryRunResult.RingsUnchanged[0] != "stable" {
		t.Fatalf("expected stable ring unchanged in dry-run, got: %+v", dryRunResult)
	}
	researchAfterDryRun, err := store.GetRing("research")
	if err != nil {
		t.Fatalf("load research after dry-run: %v", err)
	}
	if researchAfterDryRun.Description != "old" {
		t.Fatalf("expected dry-run not to change ring, got: %#v", researchAfterDryRun)
	}
	if _, err := store.GetRing("portable"); !errors.Is(err, ErrRingNotFound) {
		t.Fatalf("expected dry-run not to create portable, got: %v", err)
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import failed: %v", err)
	}
	if len(applyResult.RingsAdded) != 1 || applyResult.RingsAdded[0] != "portable" {
		t.Fatalf("expected portable ring added in apply, got: %+v", applyResult)
	}
	if len(applyResult.RingsUpdated) != 1 || applyResult.RingsUpdated[0] != "research" {
		t.Fatalf("expected research ring updated in apply, got: %+v", applyResult)
	}
	researchAfterApply, err := store.GetRing("research")
	if err != nil {
		t.Fatalf("load research after apply: %v", err)
	}
	if researchAfterApply.Description != "new" || strings.Join(researchAfterApply.Members, ",") != "alpha,beta" {
		t.Fatalf("expected research ring updated, got: %#v", researchAfterApply)
	}
	if _, err := store.GetRing("portable"); err != nil {
		t.Fatalf("expected portable ring to exist after apply: %v", err)
	}
	if _, err := store.GetRing("local"); err != nil {
		t.Fatalf("existing ring absent from snapshot should be preserved: %v", err)
	}
}

func TestImportSnapshotRingsWithSkillsDryRunAndApply(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "release", Description: "old"}, []byte("# Release\n")); err != nil {
		t.Fatalf("save release skill: %v", err)
	}
	if err := store.SaveSkill(Skill{Name: "review", Description: "review"}, []byte("# Review\n")); err != nil {
		t.Fatalf("save review skill: %v", err)
	}
	if err := store.SaveRing(Ring{Name: "workflow", Skills: []string{"release"}, Description: "old"}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Rings: []Ring{
			{Name: "workflow", Skills: []string{"review", "release"}, Description: "new"},
		},
	}

	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if len(dryRunResult.RingsUpdated) != 1 || dryRunResult.RingsUpdated[0] != "workflow" {
		t.Fatalf("expected workflow ring updated in dry-run, got: %+v", dryRunResult)
	}
	afterDryRun, err := store.GetRing("workflow")
	if err != nil {
		t.Fatalf("load workflow after dry-run: %v", err)
	}
	if strings.Join(afterDryRun.Skills, ",") != "release" {
		t.Fatalf("expected dry-run not to change ring skills, got: %#v", afterDryRun)
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import failed: %v", err)
	}
	if len(applyResult.RingsUpdated) != 1 || applyResult.RingsUpdated[0] != "workflow" {
		t.Fatalf("expected workflow ring updated in apply, got: %+v", applyResult)
	}
	afterApply, err := store.GetRing("workflow")
	if err != nil {
		t.Fatalf("load workflow after apply: %v", err)
	}
	if strings.Join(afterApply.Skills, ",") != "release,review" || afterApply.Description != "new" {
		t.Fatalf("expected workflow ring skills updated, got: %#v", afterApply)
	}
}

func TestImportSnapshotSkillsDryRunAndApply(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	for _, skill := range []SnapshotSkill{
		{Name: "release", Description: "old", Content: "# Old\n"},
		{Name: "stable", Description: "same", Content: "# Stable\n"},
		{Name: "local", Description: "preserved", Content: "# Local\n"},
	} {
		if err := store.SaveSkill(Skill{Name: skill.Name, Description: skill.Description}, []byte(skill.Content)); err != nil {
			t.Fatalf("save skill %s: %v", skill.Name, err)
		}
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Skills: []SnapshotSkill{
			{Name: "new-skill", Description: "new", Content: "# New\n"},
			{Name: "release", Description: "new", Content: "# Release\n"},
			{Name: "stable", Description: "same", Content: "# Stable\n"},
		},
	}

	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if len(dryRunResult.SkillsAdded) != 1 || dryRunResult.SkillsAdded[0] != "new-skill" {
		t.Fatalf("expected new-skill added in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.SkillsUpdated) != 1 || dryRunResult.SkillsUpdated[0] != "release" {
		t.Fatalf("expected release updated in dry-run, got: %+v", dryRunResult)
	}
	if len(dryRunResult.SkillsUnchanged) != 1 || dryRunResult.SkillsUnchanged[0] != "stable" {
		t.Fatalf("expected stable unchanged in dry-run, got: %+v", dryRunResult)
	}
	releaseAfterDryRun, err := store.GetSkill("release")
	if err != nil {
		t.Fatalf("load release after dry-run: %v", err)
	}
	if releaseAfterDryRun.Description != "old" {
		t.Fatalf("expected dry-run not to change skill, got: %#v", releaseAfterDryRun)
	}
	if _, err := store.GetSkill("new-skill"); !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("expected dry-run not to create new skill, got: %v", err)
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import failed: %v", err)
	}
	if len(applyResult.SkillsAdded) != 1 || applyResult.SkillsAdded[0] != "new-skill" {
		t.Fatalf("expected new-skill added in apply, got: %+v", applyResult)
	}
	if len(applyResult.SkillsUpdated) != 1 || applyResult.SkillsUpdated[0] != "release" {
		t.Fatalf("expected release updated in apply, got: %+v", applyResult)
	}
	releaseAfterApply, err := store.GetSkill("release")
	if err != nil {
		t.Fatalf("load release after apply: %v", err)
	}
	if releaseAfterApply.Description != "new" {
		t.Fatalf("expected release skill metadata updated, got: %#v", releaseAfterApply)
	}
	releaseContent, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("load release content after apply: %v", err)
	}
	if string(releaseContent) != "# Release\n" {
		t.Fatalf("expected release content updated, got: %q", releaseContent)
	}
	if _, err := store.GetSkill("new-skill"); err != nil {
		t.Fatalf("expected new skill to exist after apply: %v", err)
	}
	if _, err := store.GetSkill("local"); err != nil {
		t.Fatalf("existing skill absent from snapshot should be preserved: %v", err)
	}
}

func TestImportSnapshotDoesNotReadUnrelatedBrokenLocalSkillContent(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "broken", Description: "local"}, []byte("# Broken\n")); err != nil {
		t.Fatalf("save broken skill: %v", err)
	}
	contentPath, err := store.SkillContentPath("broken")
	if err != nil {
		t.Fatalf("skill content path: %v", err)
	}
	if err := os.Remove(contentPath); err != nil {
		t.Fatalf("remove skill content: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
		},
	}
	result, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("server-only import should ignore unrelated broken skill content: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "alpha" {
		t.Fatalf("expected alpha added, got: %+v", result)
	}
}

func TestImportSnapshotCanRepairBrokenLocalSkillContent(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "release", Description: "old"}, []byte("# Old\n")); err != nil {
		t.Fatalf("save release skill: %v", err)
	}
	contentPath, err := store.SkillContentPath("release")
	if err != nil {
		t.Fatalf("skill content path: %v", err)
	}
	if err := os.Remove(contentPath); err != nil {
		t.Fatalf("remove skill content: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Skills: []SnapshotSkill{
			{Name: "release", Description: "new", Content: "# Release\n"},
		},
	}
	dryRunResult, err := ImportSnapshot(store, snapshot, false)
	if err != nil {
		t.Fatalf("dry-run import should classify broken local skill as updated: %v", err)
	}
	if len(dryRunResult.SkillsUpdated) != 1 || dryRunResult.SkillsUpdated[0] != "release" {
		t.Fatalf("expected release updated in dry-run, got: %+v", dryRunResult)
	}

	applyResult, err := ImportSnapshot(store, snapshot, true)
	if err != nil {
		t.Fatalf("apply import should repair broken local skill content: %v", err)
	}
	if len(applyResult.SkillsUpdated) != 1 || applyResult.SkillsUpdated[0] != "release" {
		t.Fatalf("expected release updated in apply, got: %+v", applyResult)
	}
	content, err := store.GetSkillContent("release")
	if err != nil {
		t.Fatalf("load repaired skill content: %v", err)
	}
	if string(content) != "# Release\n" {
		t.Fatalf("expected repaired content, got: %q", content)
	}
}

func TestImportSnapshotRejectsUnknownRingMembers(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}}); err != nil {
		t.Fatalf("save alpha: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{Name: "beta", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
		},
		Rings: []Ring{
			{Name: "good", Members: []string{"alpha", "beta"}},
			{Name: "bad", Members: []string{"ghost"}},
		},
	}

	_, err := ImportSnapshot(store, snapshot, false)
	if err == nil || !strings.Contains(err.Error(), `ring "bad" references unknown servers: ghost`) {
		t.Fatalf("expected unknown ring member error, got: %v", err)
	}
}

func TestImportSnapshotRejectsUnknownRingSkills(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.SaveSkill(Skill{Name: "release", Description: "release"}, []byte("# Release\n")); err != nil {
		t.Fatalf("save release skill: %v", err)
	}

	snapshot := Snapshot{
		Version: SnapshotVersion,
		Rings: []Ring{
			{Name: "good", Skills: []string{"release"}},
			{Name: "bad", Skills: []string{"ghost"}},
		},
	}

	_, err := ImportSnapshot(store, snapshot, false)
	if err == nil || !strings.Contains(err.Error(), `ring "bad" references unknown skills: ghost`) {
		t.Fatalf("expected unknown ring skill error, got: %v", err)
	}
}

func TestParseSnapshotRejectsInvalidPayloads(t *testing.T) {
	_, err := ParseSnapshotJSON([]byte(""))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty payload error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":1,"servers":[{"name":"a","command":"/x","enabled":true,"clients":["claude-desktop"]},{"name":"a","command":"/y","enabled":true,"clients":["claude-desktop"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate name error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":99,"servers":[]}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported snapshot version") {
		t.Fatalf("expected unsupported version error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":1,"servers":[],"rings":[{"name":"research","members":["alpha"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "does not support rings") {
		t.Fatalf("expected v1 rings error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":2,"servers":[],"rings":[{"name":"research","members":["alpha"]},{"name":"research","members":["beta"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate ring name") {
		t.Fatalf("expected duplicate ring error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":2,"servers":[],"rings":[],"skills":[{"name":"release","content":"# Release\n"}]}`))
	if err == nil || !strings.Contains(err.Error(), "does not support skills") {
		t.Fatalf("expected v2 skills error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":3,"servers":[],"rings":[],"skills":[{"name":"release","content":"# One\n"},{"name":"release","content":"# Two\n"}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate skill name") {
		t.Fatalf("expected duplicate skill error, got: %v", err)
	}

	_, err = ParseSnapshotJSON([]byte(`{"version":3,"servers":[],"rings":[],"skills":[{"name":"release","content":"   "}]}`))
	if err == nil || !strings.Contains(err.Error(), "skill content is required") {
		t.Fatalf("expected empty skill content error, got: %v", err)
	}
}

func TestMarshalSnapshotWritesNewline(t *testing.T) {
	snapshot := Snapshot{Version: SnapshotVersion, Servers: []Manifest{}}
	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		t.Fatalf("expected trailing newline")
	}
}

func TestImportSnapshotRejectsNilStore(t *testing.T) {
	_, err := ImportSnapshot(nil, Snapshot{Version: SnapshotVersion}, false)
	if err == nil {
		t.Fatalf("expected nil store error")
	}
}

func TestExportSnapshotRejectsNilStore(t *testing.T) {
	_, err := ExportSnapshot(nil)
	if err == nil {
		t.Fatalf("expected nil store error")
	}
}

func TestParseSnapshotFromFilePayload(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	snapshot, err := ExportSnapshot(store)
	if err != nil {
		t.Fatalf("export snapshot: %v", err)
	}
	payload, err := MarshalSnapshotJSON(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write snapshot file: %v", err)
	}

	readPayload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot file: %v", err)
	}
	parsed, err := ParseSnapshotJSON(readPayload)
	if err != nil {
		t.Fatalf("parse snapshot file payload: %v", err)
	}
	if len(parsed.Servers) != 1 || parsed.Servers[0].Name != "alpha" {
		t.Fatalf("unexpected parsed snapshot: %+v", parsed)
	}
}

func TestImportSnapshotApplyRejectsInvalidRingsWithoutPartialWrites(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))

	// "zz-bad" sorts after the valid ring; before the fix, servers (and
	// earlier rings) were already saved when its validation failed.
	snapshot := Snapshot{
		Version: SnapshotVersion,
		Servers: []Manifest{
			{Name: "beta", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}},
		},
		Rings: []Ring{
			{Name: "good", Members: []string{"beta"}},
			{Name: "zz-bad", Members: []string{"ghost"}},
		},
	}

	_, err := ImportSnapshot(store, snapshot, true)
	if err == nil || !strings.Contains(err.Error(), `ring "zz-bad" references unknown servers: ghost`) {
		t.Fatalf("expected unknown ring member error, got: %v", err)
	}
	if _, err := store.Get("beta"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected snapshot must not partially import servers, got: %v", err)
	}
	if _, err := store.GetRing("good"); !errors.Is(err, ErrRingNotFound) {
		t.Fatalf("rejected snapshot must not partially import rings, got: %v", err)
	}
}

func TestExportSnapshotRejectsStaleRings(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "servers"))
	if err := store.Save(Manifest{Name: "alpha", Command: "/usr/bin/env", Enabled: true, Clients: []string{"claude-desktop"}}); err != nil {
		t.Fatalf("save alpha: %v", err)
	}
	if err := store.SaveRing(Ring{Name: "research", Members: []string{"alpha"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	// The member disappears after ring creation: export must refuse to
	// write a snapshot the importer would reject.
	if err := store.Remove("alpha"); err != nil {
		t.Fatalf("remove alpha: %v", err)
	}

	_, err := ExportSnapshot(store)
	if err == nil ||
		!strings.Contains(err.Error(), `ring "research" references unknown servers: alpha`) ||
		!strings.Contains(err.Error(), "update or delete the ring before exporting") {
		t.Fatalf("expected stale-ring export refusal, got: %v", err)
	}
}

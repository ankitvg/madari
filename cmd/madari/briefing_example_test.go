package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

// Exercise the checked-in tutorial packages through the public CLI dispatcher.
// Keep the registry, attachments, and Codex discovery independent of the host.
func TestBriefingExampleLifecycle(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples", "briefing"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv(registry.ConfigDirEnvVar, filepath.Join(root, "registry"))
	chdirForTest(t, root)
	codexLog := installFakeCodex(t, 0)
	withCodexAdminSkillRoots(t, []string{t.TempDir()})

	cli := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v failed (%d): %s %s", args, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	assertAbsent := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s absent, got %v", path, err)
		}
	}

	cli("skill", "add", "--dir", filepath.Join(examples, "brief-writer"))
	cli("ring", "create", "briefing", "--skill", "brief-writer", "--description", "Write a brief from supplied source notes")
	cli("skill", "update", "brief-writer", "--dir", filepath.Join(examples, "brief-writer"))
	cli("ring", "contract", "set", "briefing", "--file", filepath.Join(examples, "contract.toml"))
	var shown struct {
		Ring registry.Ring `json:"ring"`
	}
	if err := json.Unmarshal([]byte(cli("ring", "show", "briefing", "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Ring.Contract == nil || !reflect.DeepEqual(shown.Ring.Contract.ExpectedOutputs, []string{"main finding", "supporting evidence", "open questions"}) {
		t.Fatalf("unexpected example contract: %+v", shown.Ring.Contract)
	}

	prompt := "Use brief-writer. Audience: product team. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief about what this tells us and what is still unknown."
	plan := decodeRunPlan(t, cli("run", "codex", "--ring", "briefing", "--dry-run", "--json", "--", prompt))
	if !plan.Ready || !plan.RunnerAvailable || len(plan.Servers) != 0 || len(plan.Skills) != 1 || plan.Skills[0].Name != "brief-writer" {
		t.Fatalf("unexpected briefing plan: %+v", plan)
	}
	cli("skill", "add", "--dir", filepath.Join(examples, "evidence-check"))
	cli("ring", "create", "evidence-review", "--skill", "evidence-check")
	plan = decodeRunPlan(t, cli("run", "codex", "--ring", "briefing", "--ring", "evidence-review", "--dry-run", "--json", "--", prompt))
	if !plan.Ready || len(plan.Servers) != 0 || len(plan.Skills) != 2 {
		t.Fatalf("unexpected composed plan: %+v", plan)
	}
	findRunPlanSkill(t, plan, "brief-writer")
	findRunPlanSkill(t, plan, "evidence-check")
	assertAbsent(codexLog) // Dry-run must not execute the model or runner.
	assertAbsent(filepath.Join(root, ".agents"))
	assertAbsent(filepath.Join(root, "registry", "state"))

	attached := filepath.Join(root, ".claude", "skills", "brief-writer", "SKILL.md")
	cli("ring", "attach", "briefing", "claude-code", "--dry-run")
	assertAbsent(attached)
	cli("ring", "attach", "briefing", "claude-code")
	got, err := os.ReadFile(attached)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(examples, "brief-writer", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("attached skill differs from the checked-in example")
	}
	cli("ring", "status")
	cli("ring", "detach", "briefing", "claude-code")
	assertAbsent(filepath.Dir(attached))
	for _, name := range []string{"briefing", "evidence-review"} {
		cli("ring", "delete", name)
		assertAbsent(filepath.Join(root, "registry", "rings", name+".toml"))
	}
	for _, name := range []string{"brief-writer", "evidence-check"} {
		cli("skill", "remove", name)
		assertAbsent(filepath.Join(root, "registry", "skills", name))
	}
}

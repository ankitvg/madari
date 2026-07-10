package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestPolicyRequiredRingAttachFailsBeforeConfigStateOrSkillMutation(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	savePolicyTestManifest(t, store, "docs", "codex")

	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, []string{"release"}, true)

	configPath := filepath.Join(t.TempDir(), "config.toml")
	result := runCmd(store, "ring", "attach", "restricted", "codex", "--config-path", configPath)
	if result.code == 0 {
		t.Fatalf("expected required policy attach to fail closed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	for _, want := range []string{
		`ring "restricted" requires policy enforcement`,
		"codex persistent policy support is unsupported",
		"[access].allowed_tools",
	} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("attach error missing %q: %s", want, result.stderr)
		}
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config must not be mutated after policy refusal: %v", err)
	}
	statePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed state must not be mutated after policy refusal: %v", err)
	}
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill must not be materialized after policy refusal: %v", err)
	}
}

func TestPolicyRequiredRingRenderFailsWithoutPartialOutput(t *testing.T) {
	store := newTestStore(t)
	savePolicyTestManifest(t, store, "docs", "codex")
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, nil, true)

	result := runCmd(store, "ring", "render", "restricted", "--client", "codex")
	if result.code == 0 {
		t.Fatalf("expected required policy render to fail closed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("required policy render must emit no partial config, got: %s", result.stdout)
	}
	if !strings.Contains(result.stderr, "codex render policy support is unsupported") {
		t.Fatalf("unexpected render error: %s", result.stderr)
	}
}

func TestPolicyRequiredAttachedRingSyncFailsBeforeConfigStateOrSkillMutation(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	savePolicyTestManifest(t, store, "docs", "codex")
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, nil, false)

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if result := runCmd(store, "ring", "attach", "restricted", "codex", "--config-path", configPath); result.code != 0 {
		t.Fatalf("advisory ring attach failed: %s", result.stderr)
	}
	statePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read attached config: %v", err)
	}
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read attached state: %v", err)
	}

	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, []string{"release"}, true)

	manifest, err := store.Get("docs")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	manifest.Args = []string{"--changed"}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("update manifest: %v", err)
	}

	result := runCmd(store, "sync", "codex", "--config-path", configPath)
	if result.code == 0 || !strings.Contains(result.stderr, "codex persistent policy support is unsupported") {
		t.Fatalf("expected attached required ring sync refusal: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	doctorResult := runCmd(store, "doctor", "--client-config", "codex="+configPath)
	if doctorResult.code == 0 || !strings.Contains(doctorResult.stdout, "codex drift: [error] policy preflight:") || !strings.Contains(doctorResult.stdout, "codex persistent policy support is unsupported") {
		t.Fatalf("doctor hid attached policy preflight failure: stdout=%s stderr=%s", doctorResult.stdout, doctorResult.stderr)
	}
	statusResult := runCmd(store, "status", "--client-config", "codex="+configPath)
	if statusResult.code == 0 || !strings.Contains(statusResult.stdout, "codex-drift: error policy preflight:") || !strings.Contains(statusResult.stdout, "codex persistent policy support is unsupported") {
		t.Fatalf("status hid attached policy preflight failure: stdout=%s stderr=%s", statusResult.stdout, statusResult.stderr)
	}
	assertPolicyTestFileUnchanged(t, configPath, configBefore)
	assertPolicyTestFileUnchanged(t, statePath, stateBefore)
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sync must not materialize skills after policy refusal: %v", err)
	}
}

func TestPolicyRequiredRingMakesRunPlanNotReady(t *testing.T) {
	store := newTestStore(t)
	savePolicyTestManifest(t, store, "docs", "codex")
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, nil, true)

	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"restricted"}, "inspect the docs")
	if err != nil {
		t.Fatalf("build run plan: %v", err)
	}
	if plan.Ready {
		t.Fatalf("required policy must block execution while run compiler is unsupported: %#v", plan)
	}
	if !containsPolicyTestSubstring(plan.Errors, "codex run policy support is unsupported") {
		t.Fatalf("run plan missing policy support error: %#v", plan.Errors)
	}
}

func TestPolicyRequiredSkillOnlyRingFailsBeforeSkillMaterialization(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	savePolicyTestRing(t, store, "workflow", nil, []string{"release"}, true)

	result := runCmd(store, "ring", "attach", "workflow", "codex")
	if result.code == 0 || !strings.Contains(result.stderr, "codex persistent has no lossless policy compiler yet") {
		t.Fatalf("expected skill-only required ring refusal: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill-only required ring must fail before materialization: %v", err)
	}

	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"workflow"}, "prepare release")
	if err != nil {
		t.Fatalf("build run plan: %v", err)
	}
	if plan.Ready || !containsPolicyTestSubstring(plan.Errors, "codex run has no lossless policy compiler yet") {
		t.Fatalf("skill-only required run must be blocked: %#v", plan)
	}
}

func TestSyncFailsClosedWhenAttachedRingDefinitionIsMissing(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	if err := store.Save(registry.Manifest{
		Name:    "docs",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "research", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if result := runCmd(store, "ring", "attach", "research", "codex", "--config-path", configPath); result.code != 0 {
		t.Fatalf("attach ring: %s", result.stderr)
	}
	statePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if err := store.RemoveRing("research"); err != nil {
		t.Fatalf("remove ring definition: %v", err)
	}
	manifest, err := store.Get("docs")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	manifest.Args = []string{"--changed"}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("update manifest: %v", err)
	}

	result := runCmd(store, "sync", "codex", "--config-path", configPath)
	if result.code == 0 || !strings.Contains(result.stderr, `attached ring "research" is missing`) {
		t.Fatalf("expected missing attached ring refusal: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	assertPolicyTestFileUnchanged(t, configPath, configBefore)
	assertPolicyTestFileUnchanged(t, statePath, stateBefore)

	detach := runCmd(store, "ring", "detach", "research", "codex", "--config-path", configPath)
	if detach.code != 0 {
		t.Fatalf("detach must remain available for missing-ring recovery: %s", detach.stderr)
	}
}

func savePolicyTestManifest(t *testing.T, store *registry.Store, name, target string) {
	t.Helper()
	allowed := []string{"read"}
	manifest := registry.Manifest{
		Name:    name,
		Command: mustCurrentExecutable(t),
		Enabled: true,
		Clients: []string{target},
		Access: &registry.AccessProfile{
			AllowedTools: &allowed,
		},
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save policy manifest: %v", err)
	}
}

func savePolicyTestRing(t *testing.T, store *registry.Store, name string, members, skills []string, required bool) {
	t.Helper()
	ring := registry.Ring{Name: name, Members: members, Skills: skills}
	if required {
		ring.Policy = &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}
	}
	if err := store.SaveRing(ring); err != nil {
		t.Fatalf("save policy ring: %v", err)
	}
}

func assertPolicyTestFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s after policy refusal: %v", path, err)
	}
	if string(after) != string(before) {
		t.Fatalf("file %s changed after policy refusal", path)
	}
}

func containsPolicyTestSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

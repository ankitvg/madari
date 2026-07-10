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
	manifest, err := store.Get("docs")
	if err != nil {
		t.Fatalf("load policy manifest: %v", err)
	}
	manifest.Command = filepath.Join(t.TempDir(), "missing-command")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save invalid command manifest: %v", err)
	}

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
		`member "docs" cannot execute on Codex`,
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

func TestPolicyRequiredRingAttachNativeFidelityFailsBeforeSkillMutation(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	savePolicyTestManifest(t, store, "docs", "codex")
	if err := store.SaveRing(registry.Ring{Name: "base", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save base ring: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if result := runCmd(store, "ring", "attach", "base", "codex", "--config-path", configPath); result.code != 0 {
		t.Fatalf("attach base ring: %s", result.stderr)
	}
	serverStatePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	unknownConfig := "[mcp_servers.docs]\ncommand = " + tomlString(mustCurrentExecutable(t)) + "\nfuture_policy = \"strict\"\n"
	if err := os.WriteFile(configPath, []byte(unknownConfig), 0o600); err != nil {
		t.Fatalf("write unknown native policy: %v", err)
	}
	serverStateBefore, err := os.ReadFile(serverStatePath)
	if err != nil {
		t.Fatalf("read server state: %v", err)
	}

	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, []string{"release"}, true)

	result := runCmd(store, "ring", "attach", "restricted", "codex", "--config-path", configPath)
	if result.code == 0 || !strings.Contains(result.stderr, "future_policy") {
		t.Fatalf("expected native fidelity refusal: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	assertPolicyTestFileUnchanged(t, configPath, []byte(unknownConfig))
	assertPolicyTestFileUnchanged(t, serverStatePath, serverStateBefore)
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native fidelity preflight materialized skill: %v", err)
	}
	skillStatePath := (cliApp{store: store}).skillAttachmentStatePath("codex", "")
	if _, err := os.Stat(skillStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native fidelity preflight wrote skill state: %v", err)
	}
}

func TestPolicyRequiredCodexAttachPreservesServerAndSkillRefcounts(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	allowed := []string{"read", "write"}
	denied := []string{"delete"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{"read": registry.ApprovalBehaviorAlwaysAllow}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools: &allowed, DeniedTools: &denied,
			DefaultApproval: &defaultApproval, ToolApprovals: &toolApprovals,
		},
	}); err != nil {
		t.Fatalf("save policy manifest: %v", err)
	}
	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	for _, name := range []string{"r1", "r2"} {
		savePolicyTestRing(t, store, name, []string{"docs"}, []string{"release"}, true)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	for _, name := range []string{"r1", "r2"} {
		result := runCmd(store, "ring", "attach", name, "codex", "--config-path", configPath)
		if result.code != 0 {
			t.Fatalf("attach %s: %s", name, result.stderr)
		}
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Codex config: %v", err)
	}
	for _, want := range []string{
		`enabled_tools = ['read', 'write']`,
		`disabled_tools = ['delete']`,
		`default_tools_approval_mode = 'prompt'`,
		`approval_mode = 'approve'`,
	} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("Codex policy config missing %q:\n%s", want, config)
		}
	}
	serverStatePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	serverState, err := os.ReadFile(serverStatePath)
	if err != nil {
		t.Fatalf("read server state: %v", err)
	}
	skillStatePath := (cliApp{store: store}).skillAttachmentStatePath("codex", "")
	skillState, err := os.ReadFile(skillStatePath)
	if err != nil {
		t.Fatalf("read skill state: %v", err)
	}
	for _, source := range []string{`"ring:r1"`, `"ring:r2"`} {
		if !strings.Contains(string(serverState), source) || !strings.Contains(string(skillState), source) {
			t.Fatalf("missing overlapping source %s: server=%s skill=%s", source, serverState, skillState)
		}
	}
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("required attach did not materialize skill: %v", err)
	}

	if result := runCmd(store, "ring", "detach", "r1", "codex", "--config-path", configPath); result.code != 0 {
		t.Fatalf("detach r1: %s", result.stderr)
	}
	config, err = os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(config), "mcp_servers.docs") {
		t.Fatalf("shared server removed while r2 owns it: err=%v config=%s", err, config)
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("shared skill removed while r2 owns it: %v", err)
	}

	if result := runCmd(store, "ring", "detach", "r2", "codex", "--config-path", configPath); result.code != 0 {
		t.Fatalf("detach r2: %s", result.stderr)
	}
	config, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after detach: %v", err)
	}
	if strings.Contains(string(config), "mcp_servers.docs") {
		t.Fatalf("last detach retained server: %s", config)
	}
	if _, err := os.Stat(skillPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("last detach retained skill: %v", err)
	}
}

func TestPolicyRequiredRingRenderFailsForUnsupportedTargetWithoutPartialOutput(t *testing.T) {
	store := newTestStore(t)
	savePolicyTestManifest(t, store, "docs", "gemini")
	savePolicyTestRing(t, store, "restricted", []string{"docs"}, nil, true)

	result := runCmd(store, "ring", "render", "restricted", "--client", "gemini")
	if result.code == 0 {
		t.Fatalf("expected required policy render to fail closed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("required policy render must emit no partial config, got: %s", result.stdout)
	}
	if !strings.Contains(result.stderr, "gemini render policy support is unsupported") {
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
	configWithUnknownPolicy := "[mcp_servers.docs]\ncommand = " + tomlString(mustCurrentExecutable(t)) + "\nfuture_policy = \"strict\"\n"
	if err := os.WriteFile(configPath, []byte(configWithUnknownPolicy), 0o600); err != nil {
		t.Fatalf("write unknown native policy fixture: %v", err)
	}
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
	if result.code == 0 || !strings.Contains(result.stderr, "behavior-affecting native fields") || !strings.Contains(result.stderr, "future_policy") {
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

func TestPolicyRequiredSkillOnlyRingAttachesButRunRemainsBlocked(t *testing.T) {
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill setup failed: %s", result.stderr)
	}
	savePolicyTestRing(t, store, "workflow", nil, []string{"release"}, true)

	result := runCmd(store, "ring", "attach", "workflow", "codex")
	if result.code != 0 {
		t.Fatalf("skill-only required ring attach failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	skillPath := filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill-only required ring should materialize with the persistent compiler: %v", err)
	}
	savePolicyTestManifest(t, store, "docs", "codex")
	savePolicyTestRing(t, store, "workflow", []string{"docs"}, []string{"release"}, true)
	syncResult := runCmd(store, "sync", "codex")
	if syncResult.code == 0 || !strings.Contains(syncResult.stderr, "attached only through skills") {
		t.Fatalf("expected missing server ownership refusal: stdout=%s stderr=%s", syncResult.stdout, syncResult.stderr)
	}
	serverStatePath := (cliApp{store: store}).ringOpStatePath("codex", "")
	if _, err := os.Stat(serverStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed policy sync created server state: %v", err)
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

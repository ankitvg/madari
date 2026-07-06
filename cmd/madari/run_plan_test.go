package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestRunWithStoreRunPlanMultipleRings(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 0)
	t.Setenv("CLOUDSQL_MCP_TOKEN", "test-token")
	t.Setenv("LOCAL_TOKEN", "test-token")

	if result := runCmd(store, "add", "cloud-sql",
		"--transport", "http",
		"--url", "https://sqladmin.googleapis.com/mcp",
		"--client", "codex",
		"--bearer-token-env-var", "CLOUDSQL_MCP_TOKEN"); result.code != 0 {
		t.Fatalf("setup cloud-sql failed: %s", result.stderr)
	}
	if result := runCmd(store, "add", "local-helper",
		"--command", commandPath,
		"--client", "codex",
		"--required-env", "LOCAL_TOKEN"); result.code != 0 {
		t.Fatalf("setup local-helper failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "database", "--member", "cloud-sql"); result.code != 0 {
		t.Fatalf("database ring create failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "helpers", "--member", "cloud-sql", "--member", "local-helper"); result.code != 0 {
		t.Fatalf("helpers ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex",
		"--ring", "database",
		"--ring", "helpers",
		"--dry-run",
		"--json",
		"--",
		"inspect the database")
	if result.code != 0 {
		t.Fatalf("run plan failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.Ready {
		t.Fatalf("expected ready plan, got errors: %#v", plan.Errors)
	}
	if !plan.RunnerAvailable {
		t.Fatalf("expected codex runner to be available: %#v", plan)
	}
	if !slices.Equal(plan.Rings, []string{"database", "helpers"}) {
		t.Fatalf("unexpected rings: %#v", plan.Rings)
	}
	if len(plan.Servers) != 2 {
		t.Fatalf("expected deduped servers, got: %#v", plan.Servers)
	}
	cloud := findRunPlanServer(t, plan, "cloud-sql")
	if cloud.Auth != "bearer_token_env_var" || !slices.Equal(cloud.Rings, []string{"database", "helpers"}) {
		t.Fatalf("unexpected cloud-sql plan: %#v", cloud)
	}
	local := findRunPlanServer(t, plan, "local-helper")
	if local.Transport != registry.TransportStdio || !slices.Equal(local.RuntimeEnv, []string{"LOCAL_TOKEN"}) {
		t.Fatalf("unexpected local-helper plan: %#v", local)
	}
	if len(plan.Skills) != 0 {
		t.Fatalf("expected server-only plan, got skills: %#v", plan.Skills)
	}
	if env := findRunPlanEnv(t, plan, "CLOUDSQL_MCP_TOKEN"); !env.Present {
		t.Fatalf("expected CLOUDSQL_MCP_TOKEN present, got: %#v", env)
	}
	if env := findRunPlanEnv(t, plan, "LOCAL_TOKEN"); !env.Present {
		t.Fatalf("expected LOCAL_TOKEN present, got: %#v", env)
	}
	stateDir := filepath.Join(filepath.Dir(store.ServersDir()), "state")
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run plan should not create managed state, stat err=%v", err)
	}
}

func TestRunWithStoreRunPlanRequiresDryRunRingAndPrompt(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "helper", "--command", commandPath, "--client", "claude-code"); result.code != 0 {
		t.Fatalf("setup helper failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "database", "--member", "helper"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "claude-code", "--ring", "database", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "execution is only implemented for codex") {
		t.Fatalf("expected dry-run-only error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	result = runCmd(store, "run", "claude-code", "--ring", "database", "--dry-run", "--json", "--", "prompt")
	if result.code != 0 {
		t.Fatalf("claude-code dry-run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.RunnerAvailable {
		t.Fatalf("non-codex dry-run should not report a runner: %#v", plan)
	}
	result = runCmd(store, "run", "codex", "--ring", "database", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "--json is only supported with --dry-run") {
		t.Fatalf("expected json dry-run error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	result = runCmd(store, "run", "codex", "--dry-run", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "--ring is required") {
		t.Fatalf("expected required ring error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	result = runCmd(store, "run", "codex", "--ring", " ", "--dry-run", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "--ring is required") {
		t.Fatalf("expected blank ring error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	result = runCmd(store, "run", "codex", "--ring", "database", "--dry-run")
	if result.code == 0 || !strings.Contains(result.stderr, "prompt is required") {
		t.Fatalf("expected required prompt error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
}

func TestRunWithStoreRunPlanBlocksMissingCodexBinary(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	t.Setenv("PATH", t.TempDir())

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("setup helper failed: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}}); err != nil {
		t.Fatalf("setup ring failed: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.RunnerAvailable {
		t.Fatalf("expected runner unavailable without codex binary, got: %#v", plan)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "codex executable not found in PATH") {
		t.Fatalf("expected missing codex error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksCodexAdminSkillRoot(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 0)
	adminRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(adminRoot, "admin-skill"), 0o755); err != nil {
		t.Fatalf("mkdir admin skill: %v", err)
	}
	withCodexAdminSkillRoots(t, []string{adminRoot})

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("setup helper failed: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}}); err != nil {
		t.Fatalf("setup ring failed: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if !plan.RunnerAvailable {
		t.Fatalf("expected codex runner to remain available when preflight fails, got: %#v", plan)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "cannot guarantee ring-only skill isolation") {
		t.Fatalf("expected admin skill root error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanIncludesCodexRingSkills(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 0)

	if result := runCmd(store, "add", "helper", "--command", commandPath, "--client", "codex"); result.code != 0 {
		t.Fatalf("setup helper failed: %s", result.stderr)
	}
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if result := runCmd(store, "ring", "create", "mixed", "--member", "helper", "--skill", "release"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex", "--ring", "mixed", "--dry-run", "--json", "--", "prompt")
	if result.code != 0 {
		t.Fatalf("run plan failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.Ready {
		t.Fatalf("expected ready plan, got errors: %#v", plan.Errors)
	}
	if !plan.RunnerAvailable {
		t.Fatalf("expected codex runner available, got: %#v", plan)
	}
	if skill := findRunPlanSkill(t, plan, "release"); skill.Status != "ready" || !slices.Equal(skill.Rings, []string{"mixed"}) {
		t.Fatalf("expected ready skill, got: %#v", skill)
	}
	textResult := runCmd(store, "run", "codex", "--ring", "mixed", "--dry-run", "--", "prompt")
	if textResult.code != 0 || !strings.Contains(textResult.stdout, "release ready rings=mixed") {
		t.Fatalf("expected text dry-run to report ready skill, code=%d stdout=%s stderr=%s", textResult.code, textResult.stdout, textResult.stderr)
	}
}

func TestRunWithStoreRunPlanBlocksMissingRingSkill(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)

	if err := store.SaveRing(registry.Ring{Name: "workflow", Skills: []string{"release"}}); err != nil {
		t.Fatalf("setup stale skill ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "workflow", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if skill := findRunPlanSkill(t, plan, "release"); skill.Status != "blocked" {
		t.Fatalf("expected blocked skill, got: %#v", skill)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "skill release: skill is missing from the registry") {
		t.Fatalf("expected missing skill error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksNonMaterializableRingSkill(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	if err := os.MkdirAll(store.SkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "release.patch.toml"), []byte("name = \"release.patch\"\ndescription = \"Patch release\"\n"), 0o644); err != nil {
		t.Fatalf("write legacy skill manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillsDir(), "release.patch.md"), []byte("# Patch release\n"), 0o644); err != nil {
		t.Fatalf("write legacy skill content: %v", err)
	}
	if _, err := store.GetSkill("release.patch"); err != nil {
		t.Fatalf("legacy skill metadata should still parse: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "workflow", Skills: []string{"release.patch"}}); err != nil {
		t.Fatalf("setup legacy skill ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "workflow", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if skill := findRunPlanSkill(t, plan, "release.patch"); skill.Status != "blocked" {
		t.Fatalf("expected blocked skill, got: %#v", skill)
	}
	errors := strings.Join(plan.Errors, "\n")
	if !strings.Contains(errors, "skill release.patch: skill package cannot be materialized") ||
		!strings.Contains(errors, "name must contain lowercase letters") {
		t.Fatalf("expected non-materializable skill error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksMissingEnv(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	t.Setenv("CLOUDSQL_MCP_TOKEN", "")

	if result := runCmd(store, "add", "cloud-sql",
		"--transport", "http",
		"--url", "https://sqladmin.googleapis.com/mcp",
		"--client", "codex",
		"--bearer-token-env-var", "CLOUDSQL_MCP_TOKEN"); result.code != 0 {
		t.Fatalf("setup cloud-sql failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "cloudsql-readonly", "--member", "cloud-sql"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex", "--ring", "cloudsql-readonly", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if env := findRunPlanEnv(t, plan, "CLOUDSQL_MCP_TOKEN"); env.Present {
		t.Fatalf("expected missing env, got: %#v", env)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "runtime env CLOUDSQL_MCP_TOKEN is missing") {
		t.Fatalf("expected missing env error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksUnsupportedBearerAuth(t *testing.T) {
	store := newTestStore(t)
	t.Setenv("CLOUDSQL_MCP_TOKEN", "test-token")

	if result := runCmd(store, "add", "cloud-sql",
		"--transport", "http",
		"--url", "https://sqladmin.googleapis.com/mcp",
		"--client", "claude-code",
		"--bearer-token-env-var", "CLOUDSQL_MCP_TOKEN"); result.code != 0 {
		t.Fatalf("setup cloud-sql failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "cloudsql-readonly", "--member", "cloud-sql"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "claude-code", "--ring", "cloudsql-readonly", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "requires bearer_token_env_var auth") {
		t.Fatalf("expected unsupported bearer auth error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksUnsupportedOAuthResource(t *testing.T) {
	store := newTestStore(t)

	if result := runCmd(store, "add", "cloud-sql",
		"--transport", "http",
		"--url", "https://sqladmin.googleapis.com/mcp",
		"--client", "claude-code",
		"--oauth-resource", "https://sqladmin.googleapis.com/"); result.code != 0 {
		t.Fatalf("setup cloud-sql failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "cloudsql-readonly", "--member", "cloud-sql"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "claude-code", "--ring", "cloudsql-readonly", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	cloud := findRunPlanServer(t, plan, "cloud-sql")
	if cloud.Auth != "oauth_resource" || cloud.Status != "blocked" {
		t.Fatalf("expected blocked oauth_resource server, got: %#v", cloud)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "requires oauth_resource auth") {
		t.Fatalf("expected unsupported oauth_resource error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanBlocksCodexSecretRemoteHeaders(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)

	if err := store.Save(registry.Manifest{
		Name:      "cloud-sql",
		Transport: registry.TransportHTTP,
		URL:       "https://sqladmin.googleapis.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
		},
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("setup cloud-sql failed: %v", err)
	}
	if result := runCmd(store, "ring", "create", "cloudsql-readonly", "--member", "cloud-sql"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex", "--ring", "cloudsql-readonly", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	cloud := findRunPlanServer(t, plan, "cloud-sql")
	if cloud.Status != "blocked" {
		t.Fatalf("expected blocked cloud-sql, got: %#v", cloud)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "static secret header values cannot be passed to codex run") {
		t.Fatalf("expected static secret header error, got: %#v", plan.Errors)
	}
}

func TestRunWithStoreRunPlanMissingServerRuntimeEnvIsEmptyArray(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 0)

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("setup helper failed: %v", err)
	}
	if result := runCmd(store, "ring", "create", "helpers", "--member", "helper"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}
	if result := runCmd(store, "remove", "helper"); result.code != 0 {
		t.Fatalf("remove helper failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	var raw struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &raw); err != nil {
		t.Fatalf("decode raw run plan: %v\n%s", err, result.stdout)
	}
	if len(raw.Servers) != 1 {
		t.Fatalf("expected one server, got: %#v", raw.Servers)
	}
	runtimeEnv, ok := raw.Servers[0]["runtime_env"].([]any)
	if !ok {
		t.Fatalf("expected runtime_env to be an array, got %#v in %s", raw.Servers[0]["runtime_env"], result.stdout)
	}
	if len(runtimeEnv) != 0 {
		t.Fatalf("expected empty runtime_env array, got %#v", runtimeEnv)
	}
}

func TestRunWithStoreRunPlanBlocksUnsupportedSkillTarget(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)

	if result := runCmd(store, "add", "desktop", "--command", commandPath, "--client", "claude-desktop"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "mixed", "--member", "desktop", "--skill", "release"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "claude-desktop", "--ring", "mixed", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("expected blocked plan, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if plan.Ready {
		t.Fatalf("expected blocked plan, got: %#v", plan)
	}
	if skill := findRunPlanSkill(t, plan, "release"); skill.Status != "blocked" {
		t.Fatalf("expected blocked skill, got: %#v", skill)
	}
}

func TestRunWithStoreRunPlanBlocksDisabledServerAndDuplicateRing(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	installFakeCodex(t, 0)

	if result := runCmd(store, "add", "helper", "--command", commandPath, "--client", "codex"); result.code != 0 {
		t.Fatalf("setup add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "helpers", "--member", "helper"); result.code != 0 {
		t.Fatalf("ring create failed: %s", result.stderr)
	}
	if result := runCmd(store, "disable", "helper"); result.code != 0 {
		t.Fatalf("disable failed: %s", result.stderr)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--ring", "helpers", "--dry-run", "--json", "--", "prompt")
	if result.code == 0 {
		t.Fatalf("expected blocked plan, got stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	errors := strings.Join(plan.Errors, "\n")
	if !strings.Contains(errors, `duplicate ring "helpers"`) || !strings.Contains(errors, "server helper: server is disabled") {
		t.Fatalf("expected duplicate and disabled errors, got: %#v", plan.Errors)
	}
}

func decodeRunPlan(t *testing.T, payload string) runPlanJSON {
	t.Helper()
	var plan runPlanJSON
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		t.Fatalf("decode run plan: %v\n%s", err, payload)
	}
	return plan
}

func findRunPlanServer(t *testing.T, plan runPlanJSON, name string) runPlanServer {
	t.Helper()
	for _, server := range plan.Servers {
		if server.Name == name {
			return server
		}
	}
	t.Fatalf("server %q not found in %#v", name, plan.Servers)
	return runPlanServer{}
}

func findRunPlanSkill(t *testing.T, plan runPlanJSON, name string) runPlanSkill {
	t.Helper()
	for _, skill := range plan.Skills {
		if skill.Name == name {
			return skill
		}
	}
	t.Fatalf("skill %q not found in %#v", name, plan.Skills)
	return runPlanSkill{}
}

func findRunPlanEnv(t *testing.T, plan runPlanJSON, key string) runPlanEnv {
	t.Helper()
	for _, env := range plan.Env {
		if env.Key == key {
			return env
		}
	}
	t.Fatalf("env %q not found in %#v", key, plan.Env)
	return runPlanEnv{}
}

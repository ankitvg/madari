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
	skillSource := writeSkillFile(t, t.TempDir(), "release.md", "# Release\n")
	if result := runCmd(store, "skill", "add", "release", "--file", skillSource, "--description", "Release workflow"); result.code != 0 {
		t.Fatalf("skill add failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "database", "--member", "cloud-sql", "--skill", "release"); result.code != 0 {
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
	if plan.RunnerAvailable {
		t.Fatalf("PR 1 planner should not report a runner yet: %#v", plan)
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
	if skill := findRunPlanSkill(t, plan, "release"); skill.Status != "ready" {
		t.Fatalf("unexpected skill plan: %#v", skill)
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

	result := runCmd(store, "run", "codex", "--ring", "database", "--", "prompt")
	if result.code == 0 || !strings.Contains(result.stderr, "execution is not implemented yet") {
		t.Fatalf("expected dry-run-only error, got code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
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

func TestRunWithStoreRunPlanBlocksMissingEnv(t *testing.T) {
	store := newTestStore(t)
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

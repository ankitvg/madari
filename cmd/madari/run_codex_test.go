package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/launch"
	"github.com/ankitvg/madari/internal/registry"
	"github.com/pelletier/go-toml/v2"
)

func TestRunWithStoreCodexRunExecutesWithRingServersAndPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex shell fixture is unix-specific")
	}
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	logPath := installFakeCodex(t, 0)
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	if _, err := stdinWrite.WriteString("implicit stdin should not be forwarded\n"); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	if err := stdinWrite.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	originalStdin := os.Stdin
	os.Stdin = stdinRead
	defer func() {
		os.Stdin = originalStdin
		stdinRead.Close()
	}()
	t.Setenv("CLOUDSQL_MCP_TOKEN", "test-token")
	t.Setenv("LOCAL_TOKEN", "local-token")
	t.Setenv("LOCAL_SECRET", "local-secret")
	ambientSentinels := map[string]string{
		"AWS_SECRET_ACCESS_KEY":          "ambient-aws-secret",
		"GOOGLE_APPLICATION_CREDENTIALS": "ambient-gcp-secret",
		"GITHUB_TOKEN":                   "ambient-github-secret",
		"SSH_AUTH_SOCK":                  "ambient-ssh-secret",
		"MADARI_UNDECLARED_SENTINEL":     "ambient-arbitrary-secret",
	}
	for key, value := range ambientSentinels {
		t.Setenv(key, value)
	}
	originalHome := t.TempDir()
	globalSkillDir := filepath.Join(originalHome, ".agents", "skills", "global")
	if err := os.MkdirAll(globalSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir global skill: %v", err)
	}
	writeTextFile(t, globalSkillDir, "SKILL.md", "---\nname: global\ndescription: Global skill\n---\n\n# Global\n")
	t.Setenv("HOME", originalHome)
	t.Setenv("USERPROFILE", "")
	codexHome := t.TempDir()
	writeTextFile(t, codexHome, "auth.json", "test-auth\n")
	codexHomeSkillDir := filepath.Join(codexHome, "skills", "codex-global")
	if err := os.MkdirAll(codexHomeSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir codex-home skill: %v", err)
	}
	writeTextFile(t, codexHomeSkillDir, "SKILL.md", "---\nname: codex-global\ndescription: Codex home skill\n---\n\n# Codex home global\n")
	t.Setenv("CODEX_HOME", codexHome)

	if err := store.Save(registry.Manifest{
		Name:              "cloud-sql",
		Transport:         registry.TransportHTTP,
		URL:               "https://sqladmin.googleapis.com/mcp",
		OAuthResource:     "https://sqladmin.googleapis.com/",
		BearerTokenEnvVar: "CLOUDSQL_MCP_TOKEN",
		Headers: map[string]string{
			"x-goog-user-project": "stewreads",
		},
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save cloud-sql: %v", err)
	}
	if err := store.Save(registry.Manifest{
		Name:    "github.com",
		Command: commandPath,
		Args:    []string{"--stdio"},
		Env: map[string]string{
			"LOCAL_MODE":   "test",
			"LOCAL_SECRET": "inline-secret",
		},
		RequiredEnv: registry.RequiredEnv{Keys: []string{"CODEX_HOME", "HOME", "LOCAL_TOKEN"}},
		SecretEnv:   registry.SecretEnv{Keys: []string{"LOCAL_SECRET"}},
		Enabled:     true,
		Clients:     []string{"codex"},
	}); err != nil {
		t.Fatalf("save local-helper: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if err := store.SaveRing(registry.Ring{
		Name:        "database",
		Members:     []string{"cloud-sql"},
		Skills:      []string{"release"},
		Description: "Database access",
		Contract: &registry.RingContract{
			Summary:         "Use bounded read-only SQL.",
			GoodFor:         []string{"aggregate reporting"},
			RequiredContext: []string{"project and database"},
			ExpectedOutputs: []string{"query summary"},
		},
	}); err != nil {
		t.Fatalf("save database ring: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name:    "helpers",
		Members: []string{"cloud-sql", "github.com"},
	}); err != nil {
		t.Fatalf("save helpers ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "database", "--ring", "helpers", "--", "who are top 5 ebook creators?")
	if result.code != 0 {
		t.Fatalf("codex run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "codex stdout") || !strings.Contains(result.stderr, "codex stderr") {
		t.Fatalf("expected codex stdout/stderr to pass through, stdout=%s stderr=%s", result.stdout, result.stderr)
	}

	args := readNULArgs(t, logPath)
	wantPrefix := []string{"exec", "--strict-config", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only", "--cd"}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unexpected codex args prefix:\n%#v", args)
	}
	if len(args) <= len(wantPrefix) {
		t.Fatalf("expected isolated codex run root after --cd, got %#v", args)
	}
	runRoot := args[len(wantPrefix)]
	pwdPayload, err := os.ReadFile(logPath + ".pwd")
	if err != nil {
		t.Fatalf("read fake codex cwd: %v", err)
	}
	if got := strings.TrimSpace(string(pwdPayload)); !samePath(t, got, runRoot) {
		t.Fatalf("expected fake codex to run from isolated root %q, got %q", runRoot, got)
	}
	stdinPayload, err := os.ReadFile(logPath + ".stdin")
	if err != nil {
		t.Fatalf("read fake codex stdin: %v", err)
	}
	if len(stdinPayload) != 0 {
		t.Fatalf("expected fake codex stdin to be empty, got %q", string(stdinPayload))
	}
	if _, err := os.Stat(runRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected isolated codex run root to be cleaned up, stat err=%v", err)
	}
	skillFiles, err := os.ReadFile(logPath + ".skillfiles")
	if err != nil {
		t.Fatalf("read fake codex skill files: %v", err)
	}
	for _, want := range []string{
		".agents/skills/release/SKILL.md",
		".agents/skills/release/references/CHECKLIST.md",
	} {
		if !strings.Contains(string(skillFiles), want) {
			t.Fatalf("expected materialized skill file %q, got:\n%s", want, skillFiles)
		}
	}
	skillBody, err := os.ReadFile(logPath + ".release")
	if err != nil {
		t.Fatalf("read fake codex skill body: %v", err)
	}
	if !strings.Contains(string(skillBody), "# release") {
		t.Fatalf("expected materialized skill body, got:\n%s", skillBody)
	}
	checklist, err := os.ReadFile(logPath + ".checklist")
	if err != nil {
		t.Fatalf("read fake codex skill reference: %v", err)
	}
	if string(checklist) != "release checklist\n" {
		t.Fatalf("expected materialized skill reference, got %q", string(checklist))
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run should not materialize skills in original project root, stat err=%v", err)
	}
	homePayload, err := os.ReadFile(logPath + ".home")
	if err != nil {
		t.Fatalf("read fake codex home: %v", err)
	}
	if got, want := strings.TrimSpace(string(homePayload)), filepath.Join(runRoot, "home"); !samePath(t, got, want) {
		t.Fatalf("expected fake codex HOME %q, got %q", want, got)
	}
	codexHomePayload, err := os.ReadFile(logPath + ".codexhome")
	if err != nil {
		t.Fatalf("read fake codex CODEX_HOME: %v", err)
	}
	if got, want := strings.TrimSpace(string(codexHomePayload)), filepath.Join(runRoot, "codex-home"); !samePath(t, got, want) {
		t.Fatalf("expected fake codex CODEX_HOME %q, got %q", want, got)
	}
	codexAuthPayload, err := os.ReadFile(logPath + ".codexauth")
	if err != nil {
		t.Fatalf("read fake codex auth: %v", err)
	}
	if string(codexAuthPayload) != "test-auth\n" {
		t.Fatalf("expected copied codex auth, got %q", string(codexAuthPayload))
	}
	codexHomeSkillFiles, err := os.ReadFile(logPath + ".codexhomeskillfiles")
	if err != nil {
		t.Fatalf("read fake codex home skill files: %v", err)
	}
	if len(codexHomeSkillFiles) != 0 {
		t.Fatalf("expected isolated CODEX_HOME to hide caller codex-home skills, got:\n%s", codexHomeSkillFiles)
	}
	homeSkillFiles, err := os.ReadFile(logPath + ".homeskillfiles")
	if err != nil {
		t.Fatalf("read fake codex home skill files: %v", err)
	}
	if len(homeSkillFiles) != 0 {
		t.Fatalf("expected isolated HOME to hide caller user skills, got:\n%s", homeSkillFiles)
	}
	overrides := collectConfigOverrides(args)
	if len(overrides) != 1 {
		t.Fatalf("expected one config override, got %#v from args %#v", overrides, args)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	wantOverride := `mcp_servers={ cloud-sql = { url = "https://sqladmin.googleapis.com/mcp", required = true, oauth_resource = "https://sqladmin.googleapis.com/", bearer_token_env_var = "CLOUDSQL_MCP_TOKEN", http_headers = { x-goog-user-project = "stewreads" } }, "github.com" = { command = ` + tomlString(commandPath) + `, required = true, cwd = ` + tomlString(workingDir) + `, args = ["--stdio"], env_vars = ["CODEX_HOME", "HOME", "LOCAL_SECRET", "LOCAL_TOKEN"], env = { LOCAL_MODE = "test" } } }`
	if overrides[0] != wantOverride {
		t.Fatalf("unexpected mcp_servers override:\nwant %s\ngot  %s", wantOverride, overrides[0])
	}
	if strings.Contains(overrides[0], originalHome) || strings.Contains(overrides[0], codexHome) {
		t.Fatalf("caller home directory leaked into stdio configuration: %s", overrides[0])
	}
	if strings.Contains(strings.Join(overrides, "\n"), "inline-secret") {
		t.Fatalf("static secret env value leaked into codex args: %#v", overrides)
	}
	execEnv, err := os.ReadFile(logPath + ".env")
	if err != nil {
		t.Fatalf("read fake Codex environment: %v", err)
	}
	versionEnv, err := os.ReadFile(logPath + ".versionenv")
	if err != nil {
		t.Fatalf("read fake Codex version environment: %v", err)
	}
	for key, value := range ambientSentinels {
		for label, payload := range map[string][]byte{"version": versionEnv, "execution": execEnv} {
			if strings.Contains(string(payload), key+"=") || strings.Contains(string(payload), value) {
				t.Fatalf("undeclared %s credential reached Codex %s environment", key, label)
			}
		}
	}
	for key, value := range map[string]string{
		"CLOUDSQL_MCP_TOKEN": "test-token",
		"LOCAL_TOKEN":        "local-token",
		"LOCAL_SECRET":       "local-secret",
	} {
		if !strings.Contains(string(execEnv), key+"="+value) {
			t.Fatalf("declared credential %s did not reach the Codex run process", key)
		}
		if strings.Contains(string(versionEnv), key+"=") || strings.Contains(string(versionEnv), value) {
			t.Fatalf("declared credential %s leaked into the Codex version probe", key)
		}
	}
	allOverrides := collectAllConfigOverrides(args)
	shellPolicy := ""
	for _, override := range allOverrides {
		if strings.HasPrefix(override, "shell_environment_policy=") {
			shellPolicy = override
		}
	}
	if shellPolicy == "" || !strings.Contains(shellPolicy, `inherit = "none"`) || !strings.Contains(shellPolicy, "include_only") {
		t.Fatalf("missing deny-by-default Codex shell environment policy: %#v", allOverrides)
	}
	for _, key := range []string{"CLOUDSQL_MCP_TOKEN", "LOCAL_TOKEN", "LOCAL_SECRET"} {
		if strings.Contains(shellPolicy, key) {
			t.Fatalf("MCP credential key %s leaked into shell environment policy: %s", key, shellPolicy)
		}
	}

	if len(args) < 2 || args[len(args)-2] != "--" {
		t.Fatalf("expected prompt separator before final arg, got %#v", args)
	}
	prompt := args[len(args)-1]
	for _, want := range []string{
		"Selected rings:",
		"- database: Database access",
		"summary: Use bounded read-only SQL.",
		"good_for:",
		"- aggregate reporting",
		"- helpers",
		"Selected skills:",
		"- release: Release workflow",
		"Selected ring skills are materialized as project skills for this session.",
		"Use only external MCP capabilities made available by the selected Madari rings.",
		"Original working directory:",
		"project-scoped Codex config cannot add capabilities outside these rings.",
		"User prompt:\nwho are top 5 ebook creators?",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	stateDir := filepath.Join(filepath.Dir(store.ServersDir()), "state")
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run should not create managed state, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run should not create Codex config, stat err=%v", err)
	}
}

func TestRunWithStoreCodexRunPropagatesCodexFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex shell fixture is unix-specific")
	}
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	logPath := installFakeCodex(t, 17)

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save helper: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}, Skills: []string{"release"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--", "prompt")
	if result.code == 0 {
		t.Fatalf("expected codex failure, stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "codex stdout") ||
		!strings.Contains(result.stderr, "codex stderr") ||
		!strings.Contains(result.stderr, "run codex exec") {
		t.Fatalf("expected forwarded output and wrapped error, stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	args := readNULArgs(t, logPath)
	wantPrefix := []string{"exec", "--strict-config", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only", "--cd"}
	if len(args) <= len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unexpected codex args prefix:\n%#v", args)
	}
	skillFiles, err := os.ReadFile(logPath + ".skillfiles")
	if err != nil {
		t.Fatalf("read fake codex skill files: %v", err)
	}
	if !strings.Contains(string(skillFiles), ".agents/skills/release/SKILL.md") {
		t.Fatalf("expected failed codex run to see materialized skill, got:\n%s", skillFiles)
	}
	runRoot := args[len(wantPrefix)]
	if _, err := os.Stat(runRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected failed codex run root to be cleaned up, stat err=%v", err)
	}
}

func TestRunCodexArtifactIgnoresRegistryMutationAfterPlanning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex shell fixture is unix-specific")
	}
	store := newTestStore(t)
	logPath := installFakeCodex(t, 0)
	t.Setenv("DOCS_TOKEN", "frozen-before")
	allowed := []string{"read"}
	manifest := registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Args: []string{"--before"}, Enabled: true, Clients: []string{"codex"},
		RequiredEnv: registry.RequiredEnv{Keys: []string{"DOCS_TOKEN"}}, Access: &registry.AccessProfile{AllowedTools: &allowed},
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Original workflow")
	if err := store.SaveRing(registry.Ring{
		Name: "research", Members: []string{"docs"}, Skills: []string{"release"},
		Contract: &registry.RingContract{Summary: "original contract"},
		Policy:   &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"research"}, "original prompt")
	if err != nil || !plan.Ready || plan.Artifact == nil {
		t.Fatalf("build launch: ready=%t artifact=%v err=%v errors=%v", plan.Ready, plan.Artifact != nil, err, plan.Errors)
	}

	changed := []string{"write"}
	manifest.Args = []string{"--after"}
	manifest.Access.AllowedTools = &changed
	t.Setenv("DOCS_TOKEN", "mutated-after")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("mutate manifest: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Mutated workflow")
	if err := store.SaveRing(registry.Ring{
		Name: "research", Members: []string{"docs"}, Skills: []string{"release"},
		Contract: &registry.RingContract{Summary: "mutated contract"},
		Policy:   &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("mutate ring: %v", err)
	}

	if _, err := runCodex(context.Background(), cliApp{store: store}, plan); err != nil {
		t.Fatalf("execute compiled artifact: %v", err)
	}
	args := readNULArgs(t, logPath)
	overrides := collectConfigOverrides(args)
	if len(overrides) != 1 || !strings.Contains(overrides[0], `args = ["--before"]`) ||
		!strings.Contains(overrides[0], `enabled_tools = ["read"]`) || strings.Contains(overrides[0], "--after") || strings.Contains(overrides[0], `enabled_tools = ["write"]`) {
		t.Fatalf("execution did not use the compiled manifest snapshot: %#v", overrides)
	}
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "original contract") || !strings.Contains(prompt, "Original workflow") ||
		strings.Contains(prompt, "mutated contract") || strings.Contains(prompt, "Mutated workflow") {
		t.Fatalf("execution did not use the compiled ring/skill prompt snapshot:\n%s", prompt)
	}
	skill, err := os.ReadFile(logPath + ".release")
	if err != nil {
		t.Fatalf("read materialized skill: %v", err)
	}
	if !strings.Contains(string(skill), "Original workflow") || strings.Contains(string(skill), "Mutated workflow") {
		t.Fatalf("execution did not materialize the compiled skill snapshot:\n%s", skill)
	}
	execEnv, err := os.ReadFile(logPath + ".env")
	if err != nil {
		t.Fatalf("read fake Codex environment: %v", err)
	}
	if !strings.Contains(string(execEnv), "DOCS_TOKEN=frozen-before") || strings.Contains(string(execEnv), "mutated-after") {
		t.Fatalf("execution did not use the compiled environment snapshot:\n%s", execEnv)
	}
}

func TestRunCodexBlocksAdminSkillAddedAfterPlanning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Codex shell fixture is Unix-specific")
	}
	adminRoot := t.TempDir()
	withCodexAdminSkillRoots(t, []string{adminRoot})
	store := newTestStore(t)
	logPath := installFakeCodex(t, 0)
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "research", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"research"}, "inspect")
	if err != nil || !plan.Ready || plan.Artifact == nil {
		t.Fatalf("build launch: ready=%t artifact=%v err=%v errors=%v", plan.Ready, plan.Artifact != nil, err, plan.Errors)
	}
	if err := os.Mkdir(filepath.Join(adminRoot, "late-skill"), 0o755); err != nil {
		t.Fatalf("add admin skill after planning: %v", err)
	}

	if _, err := runCodex(context.Background(), cliApp{store: store}, plan); err == nil || !strings.Contains(err.Error(), "cannot guarantee ring-only skill isolation") {
		t.Fatalf("late admin skill did not block execution: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex started despite late admin skill: %v", err)
	}
}

func TestRunWithStoreCodexRunRequiresCodexBinary(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	t.Setenv("PATH", t.TempDir())

	if err := store.Save(registry.Manifest{
		Name:    "helper",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save helper: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "helpers", Members: []string{"helper"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "helpers", "--", "prompt")
	if result.code == 0 ||
		!strings.Contains(result.stderr, "launch plan is not ready") ||
		!strings.Contains(result.stdout, "codex executable not found in PATH") {
		t.Fatalf("expected missing codex plan error, code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
}

func TestCodexRunEnvBlocksAdminSkillRoot(t *testing.T) {
	adminRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(adminRoot, "admin-skill"), 0o755); err != nil {
		t.Fatalf("mkdir admin skill: %v", err)
	}
	withCodexAdminSkillRoots(t, []string{adminRoot})

	err := validateCodexRunPlan()
	if err == nil || !strings.Contains(err.Error(), "cannot guarantee ring-only skill isolation") {
		t.Fatalf("expected admin skill isolation error, got: %v", err)
	}
}

func TestCodexRunDefaultAdminSkillRootsIncludeKnownSystemRoots(t *testing.T) {
	roots := map[string]bool{}
	for _, root := range codexAdminSkillRoots {
		roots[root] = true
	}
	for _, want := range []string{"/etc/codex/skills", "/opt/codex/skills"} {
		if !roots[want] {
			t.Fatalf("expected default codex admin/system skill roots to include %s, got %#v", want, codexAdminSkillRoots)
		}
	}
}

func TestCodexRunEnvUsesExplicitCodexHomeWithoutHome(t *testing.T) {
	withCodexAdminSkillRoots(t, nil)
	runRoot := t.TempDir()
	codexHome := t.TempDir()
	writeTextFile(t, codexHome, "auth.json", "explicit-auth\n")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	auth, err := readCodexRunAuthSnapshot()
	if err != nil {
		t.Fatalf("snapshot auth: %v", err)
	}
	artifact, err := launch.Compile(launch.Input{Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect", Client: launch.ClientInput{Auth: auth}})
	if err != nil {
		t.Fatalf("compile launch: %v", err)
	}
	prepared, err := artifact.Prepare(runRoot)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	env := prepared.Env()
	isolatedCodexHome := filepath.Join(runRoot, "codex-home")
	if got := testEnvValue(env, "CODEX_HOME"); !samePath(t, got, isolatedCodexHome) {
		t.Fatalf("expected isolated CODEX_HOME %q, got %q", isolatedCodexHome, got)
	}
	authPayload, err := os.ReadFile(filepath.Join(isolatedCodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read copied auth: %v", err)
	}
	if string(authPayload) != "explicit-auth\n" {
		t.Fatalf("expected copied auth payload, got %q", string(authPayload))
	}
	authInfo, err := os.Stat(filepath.Join(isolatedCodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("stat isolated auth: %v", err)
	}
	if authInfo.Mode().Perm() != 0o600 {
		t.Fatalf("expected owner-only isolated auth, mode=%v", authInfo.Mode().Perm())
	}
	if got, want := testEnvValue(env, "HOME"), filepath.Join(runRoot, "home"); !samePath(t, got, want) {
		t.Fatalf("expected isolated HOME %q, got %q", want, got)
	}
}

func TestCodexRunEnvExpandsCodexHomeBeforeCopyingAuth(t *testing.T) {
	withCodexAdminSkillRoots(t, nil)
	runRoot := t.TempDir()
	home := t.TempDir()
	sourceCodexHome := filepath.Join(home, "codex-auth")
	if err := os.MkdirAll(sourceCodexHome, 0o755); err != nil {
		t.Fatalf("mkdir source CODEX_HOME: %v", err)
	}
	writeTextFile(t, sourceCodexHome, "auth.json", "tilde-auth\n")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "~/codex-auth")

	auth, err := readCodexRunAuthSnapshot()
	if err != nil {
		t.Fatalf("snapshot auth: %v", err)
	}
	artifact, err := launch.Compile(launch.Input{Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect", Client: launch.ClientInput{Auth: auth}})
	if err != nil {
		t.Fatalf("compile launch: %v", err)
	}
	prepared, err := artifact.Prepare(runRoot)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	env := prepared.Env()
	isolatedCodexHome := testEnvValue(env, "CODEX_HOME")
	authPayload, err := os.ReadFile(filepath.Join(isolatedCodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read copied auth: %v", err)
	}
	if string(authPayload) != "tilde-auth\n" {
		t.Fatalf("expected copied auth payload, got %q", string(authPayload))
	}
}

func TestCodexRunServerConfigValueBlocksSecretIsolatedEnvKeys(t *testing.T) {
	commandPath := mustCurrentExecutable(t)
	_, err := launch.Compile(launch.Input{
		Target:           "codex",
		WorkingDirectory: t.TempDir(),
		Prompt:           "inspect",
		Servers: []registry.Manifest{{
			Name:      "secret-home",
			Command:   commandPath,
			SecretEnv: registry.SecretEnv{Keys: []string{"HOME"}},
			Enabled:   true,
			Clients:   []string{"codex"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "secret env HOME cannot be forwarded by codex run") {
		t.Fatalf("expected blocked secret HOME, got: %v", err)
	}
	_, err = launch.Compile(launch.Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Servers: []registry.Manifest{{
			Name: "static-home", Command: commandPath, Env: map[string]string{"HOME": "/caller/home"},
			Enabled: true, Clients: []string{"codex"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "static env HOME cannot override") {
		t.Fatalf("expected blocked static HOME override, got: %v", err)
	}
}

func TestCodexRunConfigOverridesCompilePolicyDeterministically(t *testing.T) {
	store := newTestStore(t)
	allowed := []string{"tools.write", "tools.inherit", "tools.read\x7f"}
	denied := []string{"tools.remove", "tools.delete"}
	scopes := []string{"repo.write", "repo.read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{
		"tools.write":    registry.ApprovalBehaviorAlwaysAllow,
		"tools.inherit":  registry.ApprovalBehaviorInherit,
		"tools.read\x7f": registry.ApprovalBehaviorAutomatic,
	}
	if err := store.Save(registry.Manifest{
		Name: "docs.server", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools: &allowed, DeniedTools: &denied, OAuthScopes: &scopes,
			DefaultApproval: &defaultApproval, ToolApprovals: &toolApprovals,
		},
	}); err != nil {
		t.Fatalf("save policy server: %v", err)
	}
	empty := []string{}
	inherit := registry.ApprovalBehaviorInherit
	clearApprovals := map[string]registry.ApprovalBehavior{"tools.clear": registry.ApprovalBehaviorInherit}
	if err := store.Save(registry.Manifest{
		Name: "z-last", Transport: registry.TransportHTTP, URL: "https://example.com/last",
		Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools: &empty, DeniedTools: &empty, OAuthScopes: &empty,
			DefaultApproval: &inherit, ToolApprovals: &clearApprovals,
		},
	}); err != nil {
		t.Fatalf("save explicit-clear server: %v", err)
	}

	manifests, err := store.List()
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	artifact, err := launch.Compile(launch.Input{
		Target:           "codex",
		WorkingDirectory: t.TempDir(),
		Prompt:           "inspect",
		Servers:          manifests,
	})
	if err != nil {
		t.Fatalf("compile Codex launch artifact: %v", err)
	}
	overrides := artifact.CodexOverrides()
	want := `mcp_servers={ "docs.server" = { url = "https://example.com/mcp", required = true, enabled_tools = ["tools.inherit", "tools.read\u007F", "tools.write"], disabled_tools = ["tools.delete", "tools.remove"], scopes = ["repo.read", "repo.write"], default_tools_approval_mode = "prompt", tools = { "tools.read\u007F" = { approval_mode = "auto" }, "tools.write" = { approval_mode = "approve" } } }, z-last = { url = "https://example.com/last", required = true } }`
	if len(overrides) != 1 || overrides[0] != want {
		t.Fatalf("unexpected deterministic policy override:\nwant %#v\ngot  %#v", []string{want}, overrides)
	}
	if strings.ContainsRune(overrides[0], '\x7f') || strings.Contains(overrides[0], `approval_mode = ""`) {
		t.Fatalf("override did not omit inherit or escape controls: %q", overrides[0])
	}
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(overrides[0]), &parsed); err != nil {
		t.Fatalf("compiled policy override is not valid TOML: %v\n%s", err, overrides[0])
	}
}

func withCodexAdminSkillRoots(t *testing.T, roots []string) {
	t.Helper()
	previous := codexAdminSkillRoots
	codexAdminSkillRoots = roots
	t.Cleanup(func() {
		codexAdminSkillRoots = previous
	})
}

func testEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func installFakeCodex(t *testing.T, exitCode int) string {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "codex-args.bin")
	name := "codex"
	script := []byte("#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then /usr/bin/env | sort > '" + logPath + ".versionenv'; printf 'codex-cli 0.139.0\\n'; exit 0; fi\n" +
		"/usr/bin/env | sort > '" + logPath + ".env'\n" +
		"printf '%s' \"$PWD\" > '" + logPath + ".pwd'\n" +
		"printf '%s' \"$HOME\" > '" + logPath + ".home'\n" +
		"printf '%s' \"$CODEX_HOME\" > '" + logPath + ".codexhome'\n" +
		"for arg in \"$@\"; do printf '%s\\0' \"$arg\" >> '" + logPath + "'; done\n" +
		"if [ -d .agents/skills ]; then find .agents/skills -type f | sort > '" + logPath + ".skillfiles'; else : > '" + logPath + ".skillfiles'; fi\n" +
		"if [ -d \"$HOME/.agents/skills\" ]; then find \"$HOME/.agents/skills\" -type f | sort > '" + logPath + ".homeskillfiles'; else : > '" + logPath + ".homeskillfiles'; fi\n" +
		"if [ -d \"$CODEX_HOME/skills\" ]; then find \"$CODEX_HOME/skills\" -type f | sort > '" + logPath + ".codexhomeskillfiles'; else : > '" + logPath + ".codexhomeskillfiles'; fi\n" +
		"if [ -f \"$CODEX_HOME/auth.json\" ]; then cat \"$CODEX_HOME/auth.json\" > '" + logPath + ".codexauth'; else : > '" + logPath + ".codexauth'; fi\n" +
		"if [ -f .agents/skills/release/SKILL.md ]; then cat .agents/skills/release/SKILL.md > '" + logPath + ".release'; else : > '" + logPath + ".release'; fi\n" +
		"if [ -f .agents/skills/release/references/CHECKLIST.md ]; then cat .agents/skills/release/references/CHECKLIST.md > '" + logPath + ".checklist'; else : > '" + logPath + ".checklist'; fi\n" +
		"if IFS= read line; then printf '%s' \"$line\" > '" + logPath + ".stdin'; else : > '" + logPath + ".stdin'; fi\n" +
		"printf 'codex stdout\\n'\n" +
		"printf 'codex stderr\\n' >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n")
	if runtime.GOOS == "windows" {
		name = "codex.bat"
		script = []byte("@echo off\r\nif \"%1\"==\"--version\" (\r\n  echo codex-cli 0.139.0\r\n  exit /b 0\r\n)\r\nexit /b " + strconv.Itoa(exitCode) + "\r\n")
	}
	codexPath := filepath.Join(binDir, name)
	if err := os.WriteFile(codexPath, script, 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func installHangingFakeCodex(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	name := "codex"
	script := []byte("#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then printf 'codex-cli 0.139.0\\n'; exit 0; fi\n" +
		"( trap '' TERM INT; while :; do :; done ) &\n" +
		"printf '%s' \"$!\" > '" + childPIDPath + "'\n" +
		"trap '' TERM INT\n" +
		"while :; do :; done\n")
	if runtime.GOOS == "windows" {
		name = "codex.bat"
		script = []byte("@echo off\r\nif \"%1\"==\"--version\" (\r\n  echo codex-cli 0.139.0\r\n  exit /b 0\r\n)\r\n:loop\r\ngoto loop\r\n")
	}
	if err := os.WriteFile(filepath.Join(binDir, name), script, 0o755); err != nil {
		t.Fatalf("write hanging fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return childPIDPath
}

func saveTestSkillPackage(t *testing.T, store *registry.Store, name, description string) {
	t.Helper()
	sourceDir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(sourceDir, "references"), 0o755); err != nil {
		t.Fatalf("mkdir skill references: %v", err)
	}
	writeTextFile(t, sourceDir, "SKILL.md", "---\nname: "+name+"\ndescription: "+description+"\n---\n\n# "+name+"\n")
	writeTextFile(t, filepath.Join(sourceDir, "references"), "CHECKLIST.md", name+" checklist\n")
	pkg, err := registry.NewSkillPackageFromDir(sourceDir)
	if err != nil {
		t.Fatalf("read skill package: %v", err)
	}
	if err := store.SaveSkillPackage(pkg); err != nil {
		t.Fatalf("save skill package: %v", err)
	}
}

func readNULArgs(t *testing.T, path string) []string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake codex args: %v", err)
	}
	if len(payload) == 0 {
		return []string{}
	}
	parts := strings.Split(string(payload), "\x00")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func collectConfigOverrides(args []string) []string {
	var overrides []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" && strings.HasPrefix(args[i+1], "mcp_servers=") {
			overrides = append(overrides, args[i+1])
			i++
		}
	}
	return overrides
}

func collectAllConfigOverrides(args []string) []string {
	var overrides []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			overrides = append(overrides, args[i+1])
			i++
		}
	}
	return overrides
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	evalA, err := filepath.EvalSymlinks(a)
	if err == nil {
		a = evalA
	}
	evalB, err := filepath.EvalSymlinks(b)
	if err == nil {
		b = evalB
	}
	return normalizePathAlias(a) == normalizePathAlias(b)
}

func normalizePathAlias(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, "/private/var/") {
		return strings.TrimPrefix(path, "/private")
	}
	return path
}

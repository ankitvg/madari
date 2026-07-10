package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	codexclient "github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

type runExecutor func(cliApp, runLaunchPlan, string) error

func runCodex(a cliApp, plan runLaunchPlan, prompt string) error {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex not found in PATH; install Codex CLI or use --dry-run to inspect the launch plan")
	}
	if runPlanUsesPolicyContract(plan) {
		if err := validateCodexPolicyRunCompatibility(); err != nil {
			return err
		}
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current working directory: %w", err)
	}
	overrides, err := codexRunConfigOverrides(a.store, plan, workingDir)
	if err != nil {
		return err
	}
	runRoot, err := os.MkdirTemp("", "madari-codex-run-*")
	if err != nil {
		return fmt.Errorf("create isolated codex run directory: %w", err)
	}
	defer os.RemoveAll(runRoot)

	if _, err := materializeRunSkills(a.store, plan.Target, plan.Skills, runRoot); err != nil {
		return err
	}
	env, err := codexRunEnv(runRoot)
	if err != nil {
		return err
	}

	augmentedPrompt, err := codexRunPrompt(a.store, plan.Rings, prompt, workingDir)
	if err != nil {
		return err
	}

	args := []string{"exec"}
	if runPlanHasDeclaredAccess(plan) {
		args = append(args, "--strict-config")
	}
	args = append(args, "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only", "--cd", runRoot)
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	args = append(args, "--", augmentedPrompt)

	cmd := exec.Command(codexPath, args...)
	cmd.Dir = runRoot
	cmd.Env = env
	cmd.Stdout = a.stdout
	cmd.Stderr = a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run codex exec: %w", err)
	}
	return nil
}

func runPlanUsesPolicyContract(plan runLaunchPlan) bool {
	if plan.PolicyRequired {
		return true
	}
	return runPlanHasDeclaredAccess(plan)
}

func runPlanHasDeclaredAccess(plan runLaunchPlan) bool {
	for _, server := range plan.Servers {
		if server.Policy.Declared != nil || server.Manifest.Access != nil {
			return true
		}
	}
	return false
}

var codexAdminSkillRoots = []string{
	"/etc/codex/skills",
	"/opt/codex/skills",
}

func codexRunEnv(runRoot string) ([]string, error) {
	if err := validateCodexRunPlan(); err != nil {
		return nil, err
	}
	sourceCodexHome, err := codexRunSourceHome()
	if err != nil {
		return nil, err
	}
	isolatedHome := filepath.Join(runRoot, "home")
	if err := os.MkdirAll(isolatedHome, 0o700); err != nil {
		return nil, fmt.Errorf("create isolated codex home: %w", err)
	}
	isolatedCodexHome := filepath.Join(runRoot, "codex-home")
	if err := os.MkdirAll(isolatedCodexHome, 0o700); err != nil {
		return nil, fmt.Errorf("create isolated codex state home: %w", err)
	}
	if err := copyCodexRunAuthState(sourceCodexHome, isolatedCodexHome); err != nil {
		return nil, err
	}
	env := os.Environ()
	env = withEnvValue(env, "HOME", isolatedHome)
	env = withEnvValue(env, "USERPROFILE", isolatedHome)
	env = withEnvValue(env, "CODEX_HOME", isolatedCodexHome)
	return env, nil
}

func validateCodexRunPlan() error {
	return ensureCodexRunNoAdminSkillRoots(codexAdminSkillRoots)
}

func codexRunSourceHome() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome != "" {
		expanded, err := syncshared.ExpandHome(codexHome)
		if err != nil {
			return "", fmt.Errorf("expand CODEX_HOME: %w", err)
		}
		return filepath.Clean(expanded), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve current home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func copyCodexRunAuthState(sourceCodexHome, isolatedCodexHome string) error {
	if strings.TrimSpace(sourceCodexHome) == "" {
		return nil
	}
	if err := copyExistingCodexRunFile(filepath.Join(sourceCodexHome, "auth.json"), filepath.Join(isolatedCodexHome, "auth.json")); err != nil {
		return fmt.Errorf("copy codex auth state: %w", err)
	}
	return nil
}

func copyExistingCodexRunFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", src)
	}
	payload, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	return os.WriteFile(dst, payload, mode)
}

func ensureCodexRunNoAdminSkillRoots(roots []string) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect codex admin skill root %s: %w", root, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("codex admin skill root %s contains skills; cannot guarantee ring-only skill isolation", root)
		}
	}
	return nil
}

func withEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func codexRunConfigOverrides(store *registry.Store, plan runLaunchPlan, workingDir string) ([]string, error) {
	callerEnv, err := codexCallerIsolatedEnv()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]registry.Manifest, len(plan.Servers))
	needsStoreFallback := false
	for _, server := range plan.Servers {
		if server.Manifest.Name == server.Name {
			byName[server.Name] = server.Manifest
		} else {
			needsStoreFallback = true
		}
	}
	if needsStoreFallback {
		manifests, err := store.List()
		if err != nil {
			return nil, err
		}
		for _, manifest := range manifests {
			if _, planned := byName[manifest.Name]; !planned {
				byName[manifest.Name] = manifest
			}
		}
	}

	names := make([]string, 0, len(plan.Servers))
	for _, server := range plan.Servers {
		names = append(names, server.Name)
	}
	sort.Strings(names)

	servers := make([]string, 0, len(names))
	for _, name := range names {
		manifest, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("server %s no longer exists in the registry", name)
		}
		value, err := codexRunServerConfigValue(manifest, workingDir, callerEnv)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", name, err)
		}
		servers = append(servers, fmt.Sprintf("%s = %s", tomlKey(name), value))
	}
	return []string{"mcp_servers={ " + strings.Join(servers, ", ") + " }"}, nil
}

func codexCallerIsolatedEnv() (map[string]string, error) {
	env := map[string]string{}
	for _, key := range codexIsolatedEnvKeys() {
		value, ok := os.LookupEnv(key)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if key == "CODEX_HOME" {
			expanded, err := syncshared.ExpandHome(value)
			if err != nil {
				return nil, fmt.Errorf("expand CODEX_HOME: %w", err)
			}
			value = filepath.Clean(expanded)
		}
		env[key] = value
	}
	if len(env) == 0 {
		return nil, nil
	}
	return env, nil
}

func codexRunServerConfigValue(manifest registry.Manifest, workingDir string, callerEnv map[string]string) (string, error) {
	if manifest.IsRemote() {
		if secretNames := manifest.SecretHeaderNames(); len(secretNames) > 0 {
			return "", fmt.Errorf("static secret header values cannot be passed to codex run: %s", strings.Join(secretNames, ", "))
		}
		fields := []string{fmt.Sprintf("url = %s", tomlString(manifest.URL)), "required = true"}
		if strings.TrimSpace(manifest.OAuthResource) != "" {
			fields = append(fields, fmt.Sprintf("oauth_resource = %s", tomlString(manifest.OAuthResource)))
		}
		if manifest.RequiresBearerTokenEnv() {
			fields = append(fields, fmt.Sprintf("bearer_token_env_var = %s", tomlString(manifest.BearerTokenEnvVar)))
		}
		if len(manifest.Headers) > 0 {
			fields = append(fields, fmt.Sprintf("http_headers = %s", tomlInlineStringMap(manifest.Headers)))
		}
		fields = append(fields, codexRunAccessConfigFields(manifest.Access)...)
		return "{ " + strings.Join(fields, ", ") + " }", nil
	}
	if issues := codexRunServerPlanIssues(manifest); len(issues) > 0 {
		return "", fmt.Errorf("%s", strings.Join(issues, "; "))
	}

	fields := []string{fmt.Sprintf("command = %s", tomlString(manifest.Command)), "required = true", fmt.Sprintf("cwd = %s", tomlString(workingDir))}
	if len(manifest.Args) > 0 {
		fields = append(fields, fmt.Sprintf("args = %s", tomlStringArray(manifest.Args)))
	}
	envVars := codexRunRuntimeEnvVars(manifest, callerEnv)
	if len(envVars) > 0 {
		fields = append(fields, fmt.Sprintf("env_vars = %s", tomlStringArray(envVars)))
	}
	if env := codexRunStaticEnv(manifest, callerEnv); len(env) > 0 {
		fields = append(fields, fmt.Sprintf("env = %s", tomlInlineStringMap(env)))
	}
	fields = append(fields, codexRunAccessConfigFields(manifest.Access)...)
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func codexRunAccessConfigFields(access *registry.AccessProfile) []string {
	compiled := codexclient.CompileAccess(access)
	fields := make([]string, 0, 5)
	if compiled.EnabledTools != nil && len(*compiled.EnabledTools) > 0 {
		fields = append(fields, fmt.Sprintf("enabled_tools = %s", tomlStringArray(*compiled.EnabledTools)))
	}
	if compiled.DisabledTools != nil && len(*compiled.DisabledTools) > 0 {
		fields = append(fields, fmt.Sprintf("disabled_tools = %s", tomlStringArray(*compiled.DisabledTools)))
	}
	if compiled.Scopes != nil && len(*compiled.Scopes) > 0 {
		fields = append(fields, fmt.Sprintf("scopes = %s", tomlStringArray(*compiled.Scopes)))
	}
	if compiled.DefaultApproval != nil && *compiled.DefaultApproval != "" {
		fields = append(fields, fmt.Sprintf("default_tools_approval_mode = %s", tomlString(*compiled.DefaultApproval)))
	}
	if compiled.ToolApprovals != nil {
		tools := make([]string, 0, len(*compiled.ToolApprovals))
		for _, tool := range sortedMapKeys(*compiled.ToolApprovals) {
			approval := (*compiled.ToolApprovals)[tool]
			if approval == "" {
				continue
			}
			tools = append(tools, fmt.Sprintf("%s = { approval_mode = %s }", tomlKey(tool), tomlString(approval)))
		}
		if len(tools) > 0 {
			fields = append(fields, "tools = { "+strings.Join(tools, ", ")+" }")
		}
	}
	return fields
}

func codexRunServerPlanIssues(manifest registry.Manifest) []string {
	if manifest.IsRemote() {
		return nil
	}
	keys := sortedIntersectingEnvKeys(manifest.SecretEnv.Keys, codexIsolatedEnvKeySet())
	if len(keys) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("secret env %s cannot be forwarded by codex run because Codex isolates %s; move it to required_env or remove it from this server", strings.Join(keys, ", "), strings.Join(keys, ", "))}
}

func codexRunRuntimeEnvVars(manifest registry.Manifest, callerEnv map[string]string) []string {
	keys := runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
	if len(keys) == 0 || len(callerEnv) == 0 {
		return keys
	}
	secret := envKeySet(manifest.SecretEnv.Keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := callerEnv[key]; ok && !secret[key] {
			continue
		}
		out = append(out, key)
	}
	return out
}

func codexRunStaticEnv(manifest registry.Manifest, callerEnv map[string]string) map[string]string {
	secret := envKeySet(manifest.SecretEnv.Keys)
	env := map[string]string{}
	for key, value := range manifest.Env {
		if secret[key] {
			continue
		}
		env[key] = value
	}
	for key, value := range callerEnv {
		if _, exists := env[key]; !exists && !secret[key] {
			env[key] = value
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func codexIsolatedEnvKeys() []string {
	return []string{"CODEX_HOME", "HOME", "USERPROFILE"}
}

func codexIsolatedEnvKeySet() map[string]bool {
	keys := codexIsolatedEnvKeys()
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

func sortedIntersectingEnvKeys(keys []string, allowed map[string]bool) []string {
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" && allowed[key] {
			seen[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func envKeySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[strings.TrimSpace(key)] = true
	}
	return set
}

func codexRunPrompt(store *registry.Store, ringNames []string, prompt string, workingDir string) (string, error) {
	var b strings.Builder
	fmt.Fprintln(&b, "You are running through Madari with MCP capability rings selected for this session.")
	fmt.Fprintln(&b, "Use only external MCP capabilities made available by the selected Madari rings.")
	fmt.Fprintf(&b, "Original working directory: %s\n", workingDir)
	fmt.Fprintln(&b, "Codex is launched from an isolated temporary working directory so project-scoped Codex config cannot add capabilities outside these rings.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Selected rings:")
	for _, name := range ringNames {
		ring, err := store.GetRing(name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "- %s", ring.Name)
		if strings.TrimSpace(ring.Description) != "" {
			fmt.Fprintf(&b, ": %s", strings.TrimSpace(ring.Description))
		}
		fmt.Fprintln(&b)
		appendRingContractPrompt(&b, ring.Contract)
	}
	if err := appendRunSkillPrompt(&b, store, ringNames); err != nil {
		return "", err
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "User prompt:")
	fmt.Fprintln(&b, strings.TrimSpace(prompt))
	return b.String(), nil
}

func appendRingContractPrompt(b *strings.Builder, contract *registry.RingContract) {
	if contract.Empty() {
		return
	}
	if strings.TrimSpace(contract.Summary) != "" {
		fmt.Fprintf(b, "  summary: %s\n", strings.TrimSpace(contract.Summary))
	}
	appendPromptList(b, "good_for", contract.GoodFor)
	appendPromptList(b, "not_for", contract.NotFor)
	appendPromptList(b, "required_context", contract.RequiredContext)
	appendPromptList(b, "optional_context", contract.OptionalContext)
	appendPromptList(b, "expected_outputs", contract.ExpectedOutputs)
}

func appendPromptList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s:\n", label)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			fmt.Fprintf(b, "  - %s\n", value)
		}
	}
}

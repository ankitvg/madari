package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

type runExecutor func(cliApp, runLaunchPlan) error

func runCodex(a cliApp, plan runLaunchPlan) error {
	if plan.Artifact == nil {
		return fmt.Errorf("immutable Codex launch artifact is missing")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex not found in PATH; install Codex CLI or use --dry-run to inspect the launch plan")
	}
	if runPlanUsesPolicyContract(plan) {
		if err := validateCodexPolicyRunCompatibility(); err != nil {
			return err
		}
	}

	overrides := plan.Artifact.CodexOverrides()
	runRoot, err := os.MkdirTemp("", "madari-codex-run-*")
	if err != nil {
		return fmt.Errorf("create isolated codex run directory: %w", err)
	}
	defer os.RemoveAll(runRoot)

	if _, err := materializeRunSkills(plan.Artifact, runRoot); err != nil {
		return err
	}
	env, err := codexRunEnv(runRoot)
	if err != nil {
		return err
	}

	args := []string{"exec"}
	if plan.Artifact.StrictConfig() {
		args = append(args, "--strict-config")
	}
	args = append(args, "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only", "--cd", runRoot)
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	args = append(args, "--", plan.Artifact.Prompt())

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

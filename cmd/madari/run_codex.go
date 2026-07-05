package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

type runExecutor func(cliApp, runLaunchPlan, string) error

func runCodex(a cliApp, plan runLaunchPlan, prompt string) error {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex not found in PATH; install Codex CLI or use --dry-run to inspect the launch plan")
	}

	overrides, err := codexRunConfigOverrides(a.store, plan)
	if err != nil {
		return err
	}
	augmentedPrompt, err := codexRunPrompt(a.store, plan.Rings, prompt)
	if err != nil {
		return err
	}

	args := []string{"exec", "--ephemeral", "--ignore-user-config", "--sandbox", "read-only"}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	args = append(args, "--", augmentedPrompt)

	cmd := exec.Command(codexPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = a.stdout
	cmd.Stderr = a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run codex exec: %w", err)
	}
	return nil
}

func codexRunConfigOverrides(store *registry.Store, plan runLaunchPlan) ([]string, error) {
	manifests, err := store.List()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		byName[manifest.Name] = manifest
	}

	names := make([]string, 0, len(plan.Servers))
	for _, server := range plan.Servers {
		names = append(names, server.Name)
	}
	sort.Strings(names)

	overrides := make([]string, 0, len(names))
	for _, name := range names {
		manifest, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("server %s no longer exists in the registry", name)
		}
		value, err := codexRunServerConfigValue(manifest)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", name, err)
		}
		overrides = append(overrides, fmt.Sprintf("mcp_servers.%s=%s", tomlKey(name), value))
	}
	return overrides, nil
}

func codexRunServerConfigValue(manifest registry.Manifest) (string, error) {
	if manifest.IsRemote() {
		if secretNames := manifest.SecretHeaderNames(); len(secretNames) > 0 {
			return "", fmt.Errorf("static secret header values cannot be passed to codex run: %s", strings.Join(secretNames, ", "))
		}
		fields := []string{fmt.Sprintf("url = %s", tomlString(manifest.URL))}
		if strings.TrimSpace(manifest.OAuthResource) != "" {
			fields = append(fields, fmt.Sprintf("oauth_resource = %s", tomlString(manifest.OAuthResource)))
		}
		if manifest.RequiresBearerTokenEnv() {
			fields = append(fields, fmt.Sprintf("bearer_token_env_var = %s", tomlString(manifest.BearerTokenEnvVar)))
		}
		if len(manifest.Headers) > 0 {
			fields = append(fields, fmt.Sprintf("http_headers = %s", tomlInlineStringMap(manifest.Headers)))
		}
		return "{ " + strings.Join(fields, ", ") + " }", nil
	}

	fields := []string{fmt.Sprintf("command = %s", tomlString(manifest.Command))}
	if len(manifest.Args) > 0 {
		fields = append(fields, fmt.Sprintf("args = %s", tomlStringArray(manifest.Args)))
	}
	envVars := runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
	if len(envVars) > 0 {
		fields = append(fields, fmt.Sprintf("env_vars = %s", tomlStringArray(envVars)))
	}
	if env := codexRunStaticEnv(manifest); len(env) > 0 {
		fields = append(fields, fmt.Sprintf("env = %s", tomlInlineStringMap(env)))
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func codexRunStaticEnv(manifest registry.Manifest) map[string]string {
	if len(manifest.Env) == 0 {
		return nil
	}
	secret := make(map[string]bool, len(manifest.SecretEnv.Keys))
	for _, key := range manifest.SecretEnv.Keys {
		secret[strings.TrimSpace(key)] = true
	}
	env := map[string]string{}
	for key, value := range manifest.Env {
		if secret[key] {
			continue
		}
		env[key] = value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func codexRunPrompt(store *registry.Store, ringNames []string, prompt string) (string, error) {
	var b strings.Builder
	fmt.Fprintln(&b, "You are running through Madari with MCP capability rings selected for this session.")
	fmt.Fprintln(&b, "Use only external MCP capabilities made available by the selected Madari rings.")
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

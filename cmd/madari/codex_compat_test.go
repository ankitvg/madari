package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	codexclient "github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/registry"
)

// TestCodexCompatMatchesEmittedFlagsAndConfig runs the Codex run, render, and
// attach paths against servers that exercise every field Madari writes, then
// checks the output against internal/clients/codex/compat.toml in both
// directions: nothing emitted is missing from the file, and nothing listed is
// no longer emitted.
func TestCodexCompatMatchesEmittedFlagsAndConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	compat := codexclient.Compatibility()
	store := newTestStore(t)
	saveCodexCompatFixtures(t, store)
	chdirForTest(t, t.TempDir())
	withCodexAdminSkillRoots(t, nil)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("LOCAL_TOKEN", "local-value")
	t.Setenv("REMOTE_TOKEN", "remote-value")
	logPath := installLoggingCodex(t, compat.MinimumVersion)

	if result := runCmd(store, "run", "codex", "--ring", "compat", "--", "task"); result.code != 0 {
		t.Fatalf("run failed: code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	execArgs := checkCodexInvocations(t, compat, readCodexInvocations(t, logPath))
	overrides := checkCodexExecArgs(t, compat.Exec, execArgs)

	fields, toolFields, approvals := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, override := range overrides {
		collectCodexServerConfig(t, "run -c override", []byte(override), fields, toolFields, approvals)
	}

	render := runCmd(store, "ring", "render", "compat", "--client", "codex")
	if render.code != 0 {
		t.Fatalf("render failed: code=%d stderr=%s", render.code, render.stderr)
	}
	collectCodexServerConfig(t, "ring render", []byte(render.stdout), fields, toolFields, approvals)

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if attach := runCmd(store, "ring", "attach", "compat", "codex", "--config-path", configPath); attach.code != 0 {
		t.Fatalf("attach failed: code=%d stderr=%s", attach.code, attach.stderr)
	}
	attached, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read attached config: %v", err)
	}
	collectCodexServerConfig(t, "ring attach", attached, fields, toolFields, approvals)

	assertCompatSet(t, "mcp_server.fields", fields, compat.MCPServer.Fields)
	assertCompatSet(t, "mcp_server.tool_fields", toolFields, compat.MCPServer.ToolFields)
	assertCompatSet(t, "mcp_server.approval_modes", approvals, compat.MCPServer.ApprovalModes)
}

func TestCodexCompatVersionsAreOrdered(t *testing.T) {
	compat := codexclient.Compatibility()
	minimum, err := parseCodexSemanticVersion(compat.MinimumVersion)
	if err != nil || minimum.prerelease != "" {
		t.Fatalf("minimum_version %q must be a stable MAJOR.MINOR.PATCH: %v", compat.MinimumVersion, err)
	}
	verified, err := parseCodexSemanticVersion(compat.LastVerifiedVersion)
	if err != nil || verified.prerelease != "" {
		t.Fatalf("last_verified_version %q must be a stable MAJOR.MINOR.PATCH: %v", compat.LastVerifiedVersion, err)
	}
	if verified.lessThan(minimum) {
		t.Fatalf("last_verified_version %s is older than minimum_version %s", verified, minimum)
	}
}

func saveCodexCompatFixtures(t *testing.T, store *registry.Store) {
	t.Helper()
	localAllowed := []string{"read", "write"}
	localDenied := []string{"delete"}
	automatic := registry.ApprovalBehaviorAutomatic
	localApprovals := map[string]registry.ApprovalBehavior{"write": registry.ApprovalBehaviorAlwaysPrompt}
	remoteAllowed := []string{"search"}
	remoteScopes := []string{"read"}
	remoteApprovals := map[string]registry.ApprovalBehavior{"search": registry.ApprovalBehaviorAlwaysAllow}
	manifests := []registry.Manifest{
		{
			Name: "local", Command: mustCurrentExecutable(t), Args: []string{"serve"},
			Env:         map[string]string{"LOG_LEVEL": "info"},
			RequiredEnv: registry.RequiredEnv{Keys: []string{"LOCAL_TOKEN"}},
			Enabled:     true, Clients: []string{"codex"},
			Access: &registry.AccessProfile{
				AllowedTools: &localAllowed, DeniedTools: &localDenied,
				DefaultApproval: &automatic, ToolApprovals: &localApprovals,
			},
		},
		{
			Name: "remote", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
			OAuthResource: "https://example.com", BearerTokenEnvVar: "REMOTE_TOKEN",
			Headers: map[string]string{"X-Team": "core"},
			Enabled: true, Clients: []string{"codex"},
			Access: &registry.AccessProfile{
				AllowedTools: &remoteAllowed, OAuthScopes: &remoteScopes, ToolApprovals: &remoteApprovals,
			},
		},
	}
	for _, manifest := range manifests {
		if err := store.Save(manifest); err != nil {
			t.Fatalf("save %s: %v", manifest.Name, err)
		}
	}
	if err := store.SaveRing(registry.Ring{Name: "compat", Members: []string{"local", "remote"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
}

// installLoggingCodex puts a fake codex on PATH that records every
// invocation's arguments and answers the version query.
func installLoggingCodex(t *testing.T, version string) string {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "codex-invocations.bin")
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do printf '%s\\0' \"$arg\" >> '" + logPath + "'; done\n" +
		"printf '\\036' >> '" + logPath + "'\n" +
		"if [ \"$1\" = \"--version\" ]; then printf 'codex-cli " + version + "\\n'; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readCodexInvocations(t *testing.T, logPath string) [][]string {
	t.Helper()
	payload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake codex log: %v", err)
	}
	var invocations [][]string
	for _, record := range strings.Split(string(payload), "\x1e") {
		if record == "" {
			continue
		}
		invocations = append(invocations, strings.Split(strings.TrimSuffix(record, "\x00"), "\x00"))
	}
	return invocations
}

// checkCodexInvocations requires every codex call to be either the version
// query or the exec subcommand, and returns the exec call's arguments.
func checkCodexInvocations(t *testing.T, compat codexclient.Compat, invocations [][]string) []string {
	t.Helper()
	var execArgs []string
	versionQueried := false
	for _, args := range invocations {
		switch {
		case len(args) == 1 && args[0] == compat.VersionFlag:
			versionQueried = true
		case len(args) > 0 && args[0] == compat.Exec.Subcommand:
			if execArgs != nil {
				t.Fatalf("codex %s ran more than once: %q", compat.Exec.Subcommand, invocations)
			}
			execArgs = args
		default:
			t.Fatalf("codex was called with %q; add the invocation to compat.toml", args)
		}
	}
	if !versionQueried {
		t.Fatalf("access-bearing run did not query the Codex version with %q: %q", compat.VersionFlag, invocations)
	}
	if execArgs == nil {
		t.Fatalf("codex %s was not called: %q", compat.Exec.Subcommand, invocations)
	}
	return execArgs
}

// checkCodexExecArgs compares the flags Madari passed to `codex exec` with
// compat.toml and returns the -c override values.
func checkCodexExecArgs(t *testing.T, compat codexclient.CompatExec, args []string) []string {
	t.Helper()
	valueFlags := map[string]bool{}
	for _, flag := range compat.ValueFlags {
		valueFlags[flag] = true
	}
	seenFlags, seenValueFlags, overrideKeys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var overrides []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			t.Fatalf("unexpected argument %q after %q before --; if it is the value of a new flag, list %q in compat.toml exec.value_flags", arg, args[i-1], args[i-1])
		}
		if !valueFlags[arg] {
			seenFlags[arg] = true
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("flag %s has no value: %q", arg, args)
		}
		seenValueFlags[arg] = true
		value := args[i+1]
		i++
		switch arg {
		case "--sandbox":
			if value != compat.Sandbox {
				t.Fatalf("--sandbox %q does not match exec.sandbox %q", value, compat.Sandbox)
			}
		case "-c":
			key, _, _ := strings.Cut(value, "=")
			overrideKeys[strings.TrimSpace(key)] = true
			overrides = append(overrides, value)
		}
	}
	assertCompatSet(t, "exec.flags", seenFlags, compat.Flags)
	assertCompatSet(t, "exec.value_flags", seenValueFlags, compat.ValueFlags)
	assertCompatSet(t, "exec.config_overrides", overrideKeys, compat.ConfigOverrides)
	return overrides
}

func collectCodexServerConfig(t *testing.T, surface string, payload []byte, fields, toolFields, approvals map[string]bool) {
	t.Helper()
	var root map[string]any
	if err := toml.Unmarshal(payload, &root); err != nil {
		t.Fatalf("%s output is not TOML: %v\n%s", surface, err, payload)
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("%s should write both fixture servers, got %v", surface, servers)
	}
	for name, rawServer := range servers {
		server, ok := rawServer.(map[string]any)
		if !ok {
			t.Fatalf("%s: mcp_servers.%s is not a table", surface, name)
		}
		for key, value := range server {
			fields[key] = true
			switch key {
			case "default_tools_approval_mode":
				approvals[value.(string)] = true
			case "tools":
				for tool, rawTool := range value.(map[string]any) {
					toolTable, ok := rawTool.(map[string]any)
					if !ok {
						t.Fatalf("%s: mcp_servers.%s.tools.%s is not a table", surface, name, tool)
					}
					for toolKey, toolValue := range toolTable {
						toolFields[toolKey] = true
						if toolKey == "approval_mode" {
							approvals[toolValue.(string)] = true
						}
					}
				}
			}
		}
	}
}

func assertCompatSet(t *testing.T, label string, got map[string]bool, want []string) {
	t.Helper()
	listed := map[string]bool{}
	for _, item := range want {
		listed[item] = true
	}
	var unlisted, unused []string
	for item := range got {
		if !listed[item] {
			unlisted = append(unlisted, item)
		}
	}
	for item := range listed {
		if !got[item] {
			unused = append(unused, item)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(unused)
	if len(unlisted) > 0 {
		t.Errorf("Madari emits %s not listed in internal/clients/codex/compat.toml %s: %q", strings.Join(unlisted, ", "), label, unlisted)
	}
	if len(unused) > 0 {
		t.Errorf("compat.toml %s lists %q, which Madari no longer emits; remove it or extend the fixtures", label, unused)
	}
}

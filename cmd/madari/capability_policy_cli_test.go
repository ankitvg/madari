package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestCapabilityPolicyAddFlagsAndListJSON(t *testing.T) {
	store := newTestStore(t)
	result := runCmd(
		store,
		"add", "docs.api",
		"--transport", "http",
		"--url", "https://example.com/mcp",
		"--client", "codex",
		"--allow-tool", "issues.get",
		"--allow-tool", "issues.list",
		"--deny-tool", "issues.delete",
		"--oauth-scope", "issues:read",
		"--default-tool-approval", "always-prompt",
		"--tool-approval", "issues.get=always-allow",
	)
	if result.code != 0 {
		t.Fatalf("add with access profile failed: %s", result.stderr)
	}

	manifest, err := store.Get("docs.api")
	if err != nil {
		t.Fatalf("load access manifest: %v", err)
	}
	if manifest.Access == nil || manifest.Access.AllowedTools == nil || manifest.Access.DeniedTools == nil ||
		manifest.Access.OAuthScopes == nil || manifest.Access.DefaultApproval == nil || manifest.Access.ToolApprovals == nil {
		t.Fatalf("access profile lost CLI field presence: %#v", manifest.Access)
	}
	if got, want := *manifest.Access.AllowedTools, []string{"issues.get", "issues.list"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed tools mismatch: got %#v want %#v", got, want)
	}
	if *manifest.Access.DefaultApproval != registry.ApprovalBehaviorAlwaysPrompt ||
		(*manifest.Access.ToolApprovals)["issues.get"] != registry.ApprovalBehaviorAlwaysAllow {
		t.Fatalf("approval mapping mismatch: %#v", manifest.Access)
	}

	list := runCmd(store, "list", "--json")
	if list.code != 0 {
		t.Fatalf("list JSON failed: %s", list.stderr)
	}
	payload := decodeJSONObject(t, list.stdout)
	server := findCapabilityPolicyServer(t, payload, "docs.api")
	access, ok := server["access"].(map[string]any)
	if !ok {
		t.Fatalf("list JSON missing access object: %#v", server)
	}
	assertJSONKeys(t, access, "allowed_tools", "denied_tools", "oauth_scopes", "default_approval", "tool_approvals")
	if access["default_approval"] != "always-prompt" {
		t.Fatalf("unexpected access JSON: %#v", access)
	}
}

func TestCapabilityPolicyInstallFlags(t *testing.T) {
	store := newTestStore(t)
	result := runCmd(
		store,
		"install", "docs-mcp",
		"--skip-install",
		"--no-sync",
		"--command", mustCurrentExecutable(t),
		"--allow-tool", "read",
		"--default-tool-approval", "automatic",
		"--tool-approval", "read=always-allow",
	)
	if result.code != 0 {
		t.Fatalf("install with access profile failed: %s", result.stderr)
	}
	manifest, err := store.Get("docs")
	if err != nil {
		t.Fatalf("load installed manifest: %v", err)
	}
	if manifest.Access == nil || manifest.Access.AllowedTools == nil || manifest.Access.DefaultApproval == nil {
		t.Fatalf("install lost access profile: %#v", manifest.Access)
	}
}

func TestCapabilityPolicyCLIRejectsInvalidAccessFlags(t *testing.T) {
	commandPath := mustCurrentExecutable(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "raw client approval",
			args: []string{"add", "raw-mode", "--command", commandPath, "--client", "codex", "--allow-tool", "read", "--default-tool-approval", "prompt"},
			want: "invalid default_approval",
		},
		{
			name: "blank approval",
			args: []string{"add", "blank-mode", "--command", commandPath, "--client", "codex", "--default-tool-approval="},
			want: "behavior must be non-empty",
		},
		{
			name: "stdio oauth scope",
			args: []string{"add", "stdio-scope", "--command", commandPath, "--client", "codex", "--allow-tool", "read", "--oauth-scope", "docs:read"},
			want: "oauth_scopes requires a remote transport",
		},
		{
			name: "duplicate tool approval",
			args: []string{"add", "duplicate-approval", "--command", commandPath, "--client", "codex", "--allow-tool", "read", "--tool-approval", "read=automatic", "--tool-approval", "read=always-prompt"},
			want: `duplicate tool approval for "read"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runCmd(newTestStore(t), tt.args...)
			if result.code == 0 || !strings.Contains(result.stderr, tt.want) {
				t.Fatalf("expected %q refusal, code=%d stdout=%s stderr=%s", tt.want, result.code, result.stdout, result.stderr)
			}
		})
	}
}

func TestCapabilityPolicyJSONPreservesAbsenceAndExplicitClear(t *testing.T) {
	store := newTestStore(t)
	commandPath := mustCurrentExecutable(t)
	if err := store.Save(registry.Manifest{Name: "legacy", Command: commandPath, Enabled: true, Clients: []string{"codex"}}); err != nil {
		t.Fatalf("save legacy manifest: %v", err)
	}
	emptyTools := []string{}
	emptyApprovals := map[string]registry.ApprovalBehavior{}
	if err := store.Save(registry.Manifest{
		Name:    "cleared",
		Command: commandPath,
		Enabled: true,
		Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools:  &emptyTools,
			ToolApprovals: &emptyApprovals,
		},
	}); err != nil {
		t.Fatalf("save explicit-clear manifest: %v", err)
	}

	result := runCmd(store, "list", "--json")
	if result.code != 0 {
		t.Fatalf("list JSON failed: %s", result.stderr)
	}
	payload := decodeJSONObject(t, result.stdout)
	legacy := findCapabilityPolicyServer(t, payload, "legacy")
	if _, exists := legacy["access"]; exists {
		t.Fatalf("legacy access absence collapsed: %#v", legacy)
	}
	cleared := findCapabilityPolicyServer(t, payload, "cleared")
	access := cleared["access"].(map[string]any)
	if values, ok := access["allowed_tools"].([]any); !ok || len(values) != 0 {
		t.Fatalf("explicit empty allowlist not preserved: %#v", access)
	}
	if values, ok := access["tool_approvals"].(map[string]any); !ok || len(values) != 0 {
		t.Fatalf("explicit empty tool approval table not preserved: %#v", access)
	}
}

func TestCapabilityPolicyRingCreateShowAndJSON(t *testing.T) {
	store := newTestStore(t)
	if result := runCmd(store, "add", "docs", "--command", mustCurrentExecutable(t), "--client", "codex", "--allow-tool", "read"); result.code != 0 {
		t.Fatalf("server setup failed: %s", result.stderr)
	}
	if result := runCmd(store, "ring", "create", "restricted", "--member", "docs", "--enforcement", "required"); result.code != 0 {
		t.Fatalf("required ring create failed: %s", result.stderr)
	}
	ring, err := store.GetRing("restricted")
	if err != nil || !ring.RequiresPolicyEnforcement() {
		t.Fatalf("required ring policy not stored: ring=%#v err=%v", ring, err)
	}

	show := runCmd(store, "ring", "show", "restricted")
	if show.code != 0 || !strings.Contains(show.stdout, "policy:\n  enforcement: required") {
		t.Fatalf("ring show missing policy: stdout=%s stderr=%s", show.stdout, show.stderr)
	}
	showJSON := runCmd(store, "ring", "show", "restricted", "--json")
	if showJSON.code != 0 {
		t.Fatalf("ring show JSON failed: %s", showJSON.stderr)
	}
	payload := decodeJSONObject(t, showJSON.stdout)
	policy := payload["ring"].(map[string]any)["policy"].(map[string]any)
	if policy["enforcement"] != "required" {
		t.Fatalf("unexpected policy JSON: %#v", policy)
	}

	for _, args := range [][]string{
		{"ring", "create", "blank", "--member", "docs", "--enforcement="},
		{"ring", "create", "optional", "--member", "docs", "--enforcement", "optional"},
	} {
		result := runCmd(store, args...)
		if result.code == 0 || !strings.Contains(result.stderr, "enforcement") {
			t.Fatalf("invalid enforcement should fail: args=%#v stdout=%s stderr=%s", args, result.stdout, result.stderr)
		}
	}
}

func TestCapabilityPolicyHelp(t *testing.T) {
	store := newTestStore(t)
	add := runCmd(store, "add", "--help")
	for _, want := range []string{"--allow-tool", "--oauth-scope", "--default-tool-approval", "always-prompt"} {
		if add.code != 0 || !strings.Contains(add.stdout, want) {
			t.Fatalf("add help missing %q: code=%d stdout=%s", want, add.code, add.stdout)
		}
	}
	install := runCmd(store, "install", "--help")
	if install.code != 0 || !strings.Contains(install.stdout, "--allow-tool") || strings.Contains(install.stdout, "--oauth-scope") {
		t.Fatalf("install access help mismatch: code=%d stdout=%s", install.code, install.stdout)
	}
	ring := runCmd(store, "ring", "create", "--help")
	if ring.code != 0 || !strings.Contains(ring.stdout, "--enforcement required") {
		t.Fatalf("ring create help missing enforcement: code=%d stdout=%s", ring.code, ring.stdout)
	}
}

func findCapabilityPolicyServer(t *testing.T, payload map[string]any, name string) map[string]any {
	t.Helper()
	servers, ok := payload["servers"].([]any)
	if !ok {
		t.Fatalf("JSON payload has no servers array: %#v", payload)
	}
	for _, raw := range servers {
		server := raw.(map[string]any)
		if server["name"] == name {
			return server
		}
	}
	t.Fatalf("server %q not found in JSON: %#v", name, payload)
	return nil
}

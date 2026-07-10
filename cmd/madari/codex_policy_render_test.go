package main

import (
	"strings"
	"testing"

	codexclient "github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/registry"
	"github.com/pelletier/go-toml/v2"
)

func TestRenderCodexTOMLCompilesPolicyWithDottedNamesDeterministically(t *testing.T) {
	access := codexclient.CompileAccess(codexPolicyRenderAccess())
	servers := map[string]renderedServer{
		"z-last": {
			Command: "/bin/z-last",
		},
		"docs.server": {
			Transport:   registry.TransportHTTP,
			URL:         "https://example.com/mcp",
			CodexAccess: &access,
		},
	}

	var out strings.Builder
	if err := renderCodexTOML(&out, servers); err != nil {
		t.Fatalf("render Codex policy: %v", err)
	}
	want := `[mcp_servers."docs.server"]
url = "https://example.com/mcp"
enabled_tools = ["tools.inherit", "tools.read", "tools.write"]
disabled_tools = ["tools.delete", "tools.remove"]
scopes = ["repo.read", "repo.write"]
default_tools_approval_mode = "prompt"

[mcp_servers."docs.server".tools."tools.read"]
approval_mode = "auto"

[mcp_servers."docs.server".tools."tools.write"]
approval_mode = "approve"

[mcp_servers.z-last]
command = "/bin/z-last"
`
	if out.String() != want {
		t.Fatalf("Codex policy render drift:\nwant:\n%s\ngot:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), `approval_mode = "inherit"`) || strings.Contains(out.String(), `approval_mode = ""`) {
		t.Fatalf("portable inherit must omit the native per-tool override:\n%s", out.String())
	}
}

func TestRenderCodexTOMLEscapesDeleteControlInToolNames(t *testing.T) {
	tool := "read\x7f"
	allowed := []string{tool}
	approvals := map[string]string{tool: "prompt"}
	access := codexclient.CompiledAccess{EnabledTools: &allowed, ToolApprovals: &approvals}
	servers := map[string]renderedServer{
		"docs": {Command: "/bin/docs", CodexAccess: &access},
	}
	var out strings.Builder
	if err := renderCodexTOML(&out, servers); err != nil {
		t.Fatalf("render Codex policy: %v", err)
	}
	if strings.ContainsRune(out.String(), '\x7f') || !strings.Contains(out.String(), `read\u007F`) {
		t.Fatalf("DEL control was not deterministically escaped:\n%s", out.String())
	}
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("escaped policy output is invalid TOML: %v\n%s", err, out.String())
	}
}

func TestRequiredCodexRingRenderCompilesEveryAccessField(t *testing.T) {
	store := newTestStore(t)
	manifest := registry.Manifest{
		Name:      "docs.server",
		Transport: registry.TransportHTTP,
		URL:       "https://example.com/mcp",
		Enabled:   true,
		Clients:   []string{"codex"},
		Access:    codexPolicyRenderAccess(),
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save policy manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name:    "restricted",
		Members: []string{manifest.Name},
		Policy: &registry.RingPolicy{
			Enforcement: registry.PolicyEnforcementRequired,
		},
	}); err != nil {
		t.Fatalf("save policy ring: %v", err)
	}

	result := runCmd(store, "ring", "render", "restricted", "--client", "codex")
	if result.code != 0 {
		t.Fatalf("required Codex policy render failed: %s", result.stderr)
	}
	for _, want := range []string{
		`enabled_tools = ["tools.inherit", "tools.read", "tools.write"]`,
		`disabled_tools = ["tools.delete", "tools.remove"]`,
		`scopes = ["repo.read", "repo.write"]`,
		`default_tools_approval_mode = "prompt"`,
		`[mcp_servers."docs.server".tools."tools.read"]`,
		`approval_mode = "auto"`,
		`[mcp_servers."docs.server".tools."tools.write"]`,
		`approval_mode = "approve"`,
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("render missing %q:\n%s", want, result.stdout)
		}
	}
	if result.stderr != "" {
		t.Fatalf("unexpected required render warning: %s", result.stderr)
	}
}

func TestRequiredCodexRingRenderRejectsUnsupportedMemberBeforeOutput(t *testing.T) {
	store := newTestStore(t)
	allowed := []string{"events.read"}
	manifest := registry.Manifest{
		Name:      "events",
		Transport: registry.TransportSSE,
		URL:       "https://example.com/sse",
		Enabled:   true,
		Clients:   []string{"codex"},
		Access:    &registry.AccessProfile{AllowedTools: &allowed},
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save SSE manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name:    "restricted",
		Members: []string{manifest.Name},
		Policy:  &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save required ring: %v", err)
	}

	result := runCmd(store, "ring", "render", "restricted", "--client", "codex")
	if result.code == 0 || !strings.Contains(result.stderr, "uses sse transport") {
		t.Fatalf("expected fail-closed SSE refusal: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("required render emitted partial output: %s", result.stdout)
	}
}

func codexPolicyRenderAccess() *registry.AccessProfile {
	allowed := []string{"tools.write", "tools.inherit", "tools.read"}
	denied := []string{"tools.remove", "tools.delete"}
	scopes := []string{"repo.write", "repo.read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{
		"tools.write":   registry.ApprovalBehaviorAlwaysAllow,
		"tools.inherit": registry.ApprovalBehaviorInherit,
		"tools.read":    registry.ApprovalBehaviorAutomatic,
	}
	return &registry.AccessProfile{
		AllowedTools:    &allowed,
		DeniedTools:     &denied,
		OAuthScopes:     &scopes,
		DefaultApproval: &defaultApproval,
		ToolApprovals:   &toolApprovals,
	}
}

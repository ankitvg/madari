package codex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func TestSyncCompilesAllCodexAccessFields(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	manifest := newCloudSQLManifest()
	manifest.Access = fullPolicyAccess()

	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath})
	if err != nil {
		t.Fatalf("sync policy: %v", err)
	}
	if !reflect.DeepEqual(result.Added, []string{"cloud-sql"}) {
		t.Fatalf("unexpected plan: %#v", result)
	}
	table := rawServerTable(t, configPath, "cloud-sql")
	assertRawStringSlice(t, table, "enabled_tools", []string{"issues.get", "issues.inherit", "issues.list"})
	assertRawStringSlice(t, table, "disabled_tools", []string{"issues.delete"})
	assertRawStringSlice(t, table, "scopes", []string{"issues:read", "profile:read"})
	if table["default_tools_approval_mode"] != "prompt" {
		t.Fatalf("default approval not compiled: %#v", table)
	}
	tools := table["tools"].(map[string]any)
	if got := tools["issues.get"].(map[string]any)["approval_mode"]; got != "approve" {
		t.Fatalf("per-tool approval not compiled: %#v", tools)
	}
	if got := tools["issues.list"].(map[string]any)["approval_mode"]; got != "auto" {
		t.Fatalf("automatic approval not compiled: %#v", tools)
	}
	if _, exists := tools["issues.inherit"]; exists {
		t.Fatalf("inherit must omit the native approval override: %#v", tools)
	}
}

func TestSyncPreservesLegacyNativeRestrictionsDuringCoreUpdate(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	original := `[mcp_servers.stewreads]
command = "stewreads-mcp"
args = ["--old"]
enabled_tools = ["read"]
disabled_tools = ["delete"]
scopes = ["repo:read"]
default_tools_approval_mode = "writes"
startup_timeout_ms = 1500
auth = "chatgpt"
experimental_environment = "remote"
future_restriction = "strict"

[mcp_servers.stewreads.tools.read]
approval_mode = "prompt"
classification = "sensitive"
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"stewreads": {syncshared.SourceStandalone}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	manifest := newStewreadsManifest()
	manifest.Args = []string{"--new"}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath})
	if err != nil {
		t.Fatalf("sync legacy entry: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"stewreads"}) || len(result.PolicyUpdated) != 0 {
		t.Fatalf("unexpected plan: %#v", result)
	}
	table := rawServerTable(t, configPath, "stewreads")
	assertRawStringSlice(t, table, "enabled_tools", []string{"read"})
	assertRawStringSlice(t, table, "disabled_tools", []string{"delete"})
	assertRawStringSlice(t, table, "scopes", []string{"repo:read"})
	if table["default_tools_approval_mode"] != "writes" || table["future_restriction"] != "strict" {
		t.Fatalf("legacy native fields were stripped: %#v", table)
	}
	if table["startup_timeout_ms"] != int64(1500) {
		t.Fatalf("documented millisecond timeout alias was stripped: %#v", table)
	}
	if table["auth"] != "chatgpt" || table["experimental_environment"] != "remote" {
		t.Fatalf("documented native fields were stripped: %#v", table)
	}
	tool := table["tools"].(map[string]any)["read"].(map[string]any)
	if tool["approval_mode"] != "prompt" || tool["classification"] != "sensitive" {
		t.Fatalf("legacy nested fields were stripped: %#v", tool)
	}
}

func TestSyncExplicitAccessClearsRemoveNativeOverrides(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	original := `[mcp_servers.cloud-sql]
url = "https://sqladmin.googleapis.com/mcp"
oauth_resource = "https://sqladmin.googleapis.com/"
bearer_token_env_var = "CLOUDSQL_MCP_TOKEN"
enabled_tools = ["read"]
disabled_tools = ["delete"]
scopes = ["repo:read"]
default_tools_approval_mode = "prompt"

[mcp_servers.cloud-sql.tools.read]
approval_mode = "approve"
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"cloud-sql": {syncshared.SourceStandalone}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	empty := []string{}
	inherit := registry.ApprovalBehaviorInherit
	emptyTools := map[string]registry.ApprovalBehavior{}
	manifest := newCloudSQLManifest()
	manifest.Access = &registry.AccessProfile{
		AllowedTools: &empty, DeniedTools: &empty, OAuthScopes: &empty,
		DefaultApproval: &inherit, ToolApprovals: &emptyTools,
	}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath})
	if err != nil {
		t.Fatalf("sync explicit clears: %v", err)
	}
	if !reflect.DeepEqual(result.PolicyUpdated, []string{"cloud-sql"}) {
		t.Fatalf("expected policy drift classification: %#v", result)
	}
	table := rawServerTable(t, configPath, "cloud-sql")
	for _, key := range []string{"enabled_tools", "disabled_tools", "scopes", "default_tools_approval_mode", "tools"} {
		if _, exists := table[key]; exists {
			t.Fatalf("explicit clear left %s behind: %#v", key, table)
		}
	}
}

func TestSyncDeclaredToolApprovalsPreserveUnknownNestedDataAdvisory(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	original := `[mcp_servers.stewreads]
command = "stewreads-mcp"

[mcp_servers.stewreads.tools.read]
approval_mode = "prompt"
classification = "sensitive"

[mcp_servers.stewreads.tools.stale]
approval_mode = "approve"
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"stewreads": {syncshared.SourceStandalone}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	approval := map[string]registry.ApprovalBehavior{
		"read": registry.ApprovalBehaviorAlwaysAllow,
		"new":  registry.ApprovalBehaviorAlwaysPrompt,
	}
	manifest := newStewreadsManifest()
	manifest.Access = &registry.AccessProfile{ToolApprovals: &approval}
	if _, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath}); err != nil {
		t.Fatalf("sync approvals: %v", err)
	}
	tools := rawServerTable(t, configPath, "stewreads")["tools"].(map[string]any)
	read := tools["read"].(map[string]any)
	if read["approval_mode"] != "approve" || read["classification"] != "sensitive" {
		t.Fatalf("declared approval or unknown nested data lost: %#v", read)
	}
	if _, exists := tools["stale"]; exists {
		t.Fatalf("stale approval was not cleared: %#v", tools)
	}
	if tools["new"].(map[string]any)["approval_mode"] != "prompt" {
		t.Fatalf("new approval missing: %#v", tools)
	}
}

func TestRequiredSyncUnknownNativeFieldFailsBeforeMutation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	command := currentTestExecutable(t)
	originalConfig := "[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + "\nfuture_policy = \"strict\"\n"
	if err := os.WriteFile(configPath, []byte(originalConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"docs": {syncshared.RingSource("restricted")}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	allowed := []string{"read"}
	manifest := registry.Manifest{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &allowed}}
	ring := registry.Ring{Name: "restricted", Members: []string{"docs"}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
	_, err = Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}})
	if err == nil || !strings.Contains(err.Error(), "future_policy") || !strings.Contains(err.Error(), "behavior-affecting native fields") {
		t.Fatalf("expected native fidelity refusal, got: %v", err)
	}
	assertFileEquals(t, configPath, []byte(originalConfig))
	assertFileEquals(t, statePath, originalState)
	backups, globErr := filepath.Glob(configPath + ".bak.*")
	if globErr != nil || len(backups) != 0 {
		t.Fatalf("preflight created backups: %#v err=%v", backups, globErr)
	}
}

func TestRequiredSyncRejectsNewMemberCollidingWithUnmanagedEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	command := currentTestExecutable(t)
	config := "[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + "\nenabled_tools = [\"read\"]\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"existing": {syncshared.RingSource("restricted")}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	allowed := []string{"read"}
	manifests := []registry.Manifest{
		{Name: "existing", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &allowed}},
		{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &allowed}},
	}
	ring := registry.Ring{Name: "restricted", Members: []string{"existing", "docs"}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
	_, err := Sync(manifests, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "unmanaged") || !strings.Contains(err.Error(), "docs") {
		t.Fatalf("expected unmanaged required-member refusal, got: %v", err)
	}
}

func TestRequiredSyncBlocksUnknownNestedAndVersionSkewApproval(t *testing.T) {
	command := currentTestExecutable(t)
	cases := []struct {
		name       string
		nativeTOML string
		want       string
	}{
		{
			name: "unknown nested tool field",
			nativeTOML: "\n[mcp_servers.docs.tools.read]\n" +
				"approval_mode = \"prompt\"\nclassification = \"sensitive\"\n",
			want: "classification",
		},
		{
			name:       "version skew approval",
			nativeTOML: "\ndefault_tools_approval_mode = \"future\"\n",
			want:       "future",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			configPath := filepath.Join(tmp, "config.toml")
			statePath := filepath.Join(tmp, "state.json")
			payload := "[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + tc.nativeTOML
			if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if err := syncshared.SaveManagedState(statePath, map[string][]string{"docs": {syncshared.RingSource("restricted")}}); err != nil {
				t.Fatalf("write state: %v", err)
			}
			allowed := []string{"read"}
			manifest := registry.Manifest{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &allowed}}
			ring := registry.Ring{Name: "restricted", Members: []string{"docs"}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
			_, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}, DryRun: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected fidelity refusal containing %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestRequiredSyncAcceptsDocumentedNativeCompatibilityFields(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	command := currentTestExecutable(t)
	payload := "[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + "\nenabled = false\nenabled_tools = [\"read\"]\ndefault_tools_approval_mode = \"writes\"\nstartup_timeout_ms = 1500\nauth = \"chatgpt\"\nexperimental_environment = \"remote\"\n\n[mcp_servers.docs.tools.read]\napproval_mode = \"writes\"\n"
	if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"docs": {syncshared.RingSource("restricted")}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	allowed := []string{"read"}
	manifest := registry.Manifest{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &allowed}}
	ring := registry.Ring{Name: "restricted", Members: []string{"docs"}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}})
	if err != nil {
		t.Fatalf("sync documented native compatibility fields: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"docs"}) {
		t.Fatalf("native disabled state was not reconciled: %#v", result)
	}
	table := rawServerTable(t, configPath, "docs")
	if _, exists := table["enabled"]; exists {
		t.Fatalf("native disabled state survived reconciliation: %#v", table)
	}
	if table["default_tools_approval_mode"] != "writes" || table["startup_timeout_ms"] != int64(1500) {
		t.Fatalf("documented native fields were not preserved: %#v", table)
	}
	if table["auth"] != "chatgpt" || table["experimental_environment"] != "remote" {
		t.Fatalf("documented native compatibility fields were not preserved: %#v", table)
	}
	if table["tools"].(map[string]any)["read"].(map[string]any)["approval_mode"] != "writes" {
		t.Fatalf("documented per-tool writes approval was not preserved: %#v", table)
	}
}

func TestSyncRejectsUnsupportedDocumentedNativeValuesWithoutMutation(t *testing.T) {
	command := currentTestExecutable(t)
	for _, tc := range []struct {
		name  string
		field string
		value string
	}{
		{name: "auth", field: "auth", value: "future"},
		{name: "experimental environment", field: "experimental_environment", value: "cloud"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			configPath := filepath.Join(tmp, "config.toml")
			statePath := filepath.Join(tmp, "state.json")
			original := []byte("[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + "\n" + tc.field + " = " + tomlQuoted(tc.value) + "\n")
			if err := os.WriteFile(configPath, original, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			_, err := Sync([]registry.Manifest{{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}}}, SyncOptions{ConfigPath: configPath, StatePath: statePath})
			if err == nil || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), tc.value) {
				t.Fatalf("expected unsupported native value error, got: %v", err)
			}
			assertFileEquals(t, configPath, original)
			if _, statErr := os.Stat(statePath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("parse failure wrote state: %v", statErr)
			}
		})
	}
}

func TestSyncRejectsMalformedNativePolicyTypesWithoutMutation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	original := []byte("[mcp_servers.stewreads]\ncommand = \"stewreads-mcp\"\nenabled_tools = [1]\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Sync([]registry.Manifest{newStewreadsManifest()}, SyncOptions{ConfigPath: configPath, StatePath: statePath})
	if err == nil || !strings.Contains(err.Error(), "enabled_tools") {
		t.Fatalf("expected native policy parse failure, got: %v", err)
	}
	assertFileEquals(t, configPath, original)
	if _, statErr := os.Stat(statePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("parse failure wrote state: %v", statErr)
	}
}

func TestRequiredAttachRejectsInvalidInMemoryPortableApproval(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	allowed := []string{"read"}
	invalid := registry.ApprovalBehavior("prompt")
	manifest := registry.Manifest{
		Name: "docs", Command: currentTestExecutable(t), Enabled: true, Clients: []string{Target},
		Access: &registry.AccessProfile{AllowedTools: &allowed, DefaultApproval: &invalid},
	}
	ring := registry.Ring{Name: "restricted", Members: []string{"docs"}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
	_, err := AttachRing(ring, []registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}})
	if err == nil || !strings.Contains(err.Error(), "invalid default_approval") {
		t.Fatalf("expected invalid portable approval refusal, got: %v", err)
	}
	for _, path := range []string{configPath, statePath} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid approval mutated %s: %v", path, statErr)
		}
	}
}

func TestRequiredAttachRejectsUnsupportedSSEBeforeMutation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	manifest := newCloudSQLManifest()
	manifest.Transport = registry.TransportSSE
	allowed := []string{"read"}
	manifest.Access = &registry.AccessProfile{AllowedTools: &allowed}
	ring := registry.Ring{Name: "restricted", Members: []string{manifest.Name}, Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired}}
	_, err := AttachRing(ring, []registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, Rings: []registry.Ring{ring}})
	if err == nil || !strings.Contains(err.Error(), "uses sse transport") {
		t.Fatalf("expected SSE refusal, got: %v", err)
	}
	for _, path := range []string{configPath, statePath} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("required preflight mutated %s: %v", path, statErr)
		}
	}
}

func TestSyncPolicyOnlyDriftIsClassifiedAndSetOrderIsSemantic(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	statePath := filepath.Join(tmp, "state.json")
	command := currentTestExecutable(t)
	original := "[mcp_servers.docs]\ncommand = " + tomlQuoted(command) + "\nenabled_tools = [\"write\", \"read\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := syncshared.SaveManagedState(statePath, map[string][]string{"docs": {syncshared.SourceStandalone}}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	matching := []string{"read", "write"}
	manifest := registry.Manifest{Name: "docs", Command: command, Enabled: true, Clients: []string{Target}, Access: &registry.AccessProfile{AllowedTools: &matching}}
	result, err := Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run matching set: %v", err)
	}
	if !reflect.DeepEqual(result.Unchanged, []string{"docs"}) || len(result.PolicyUpdated) != 0 {
		t.Fatalf("array ordering must not create drift: %#v", result)
	}

	changed := []string{"read"}
	manifest.Access.AllowedTools = &changed
	result, err = Sync([]registry.Manifest{manifest}, SyncOptions{ConfigPath: configPath, StatePath: statePath, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run policy drift: %v", err)
	}
	if !reflect.DeepEqual(result.Updated, []string{"docs"}) || !reflect.DeepEqual(result.PolicyUpdated, []string{"docs"}) {
		t.Fatalf("policy-only drift not classified: %#v", result)
	}
}

func fullPolicyAccess() *registry.AccessProfile {
	allowed := []string{"issues.list", "issues.inherit", "issues.get"}
	denied := []string{"issues.delete"}
	scopes := []string{"profile:read", "issues:read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{
		"issues.get":     registry.ApprovalBehaviorAlwaysAllow,
		"issues.list":    registry.ApprovalBehaviorAutomatic,
		"issues.inherit": registry.ApprovalBehaviorInherit,
	}
	return &registry.AccessProfile{
		AllowedTools: &allowed, DeniedTools: &denied, OAuthScopes: &scopes,
		DefaultApproval: &defaultApproval, ToolApprovals: &toolApprovals,
	}
}

func rawServerTable(t *testing.T, path, name string) map[string]any {
	t.Helper()
	root := readRoot(t, path)
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("missing mcp_servers table: %#v", root)
	}
	table, ok := servers[name].(map[string]any)
	if !ok {
		t.Fatalf("missing server %s: %#v", name, servers)
	}
	return table
}

func assertRawStringSlice(t *testing.T, table map[string]any, key string, want []string) {
	t.Helper()
	raw, ok := table[key].([]any)
	if !ok {
		t.Fatalf("%s is not an array: %#v", key, table[key])
	}
	got := make([]string, len(raw))
	for i, value := range raw {
		got[i] = value.(string)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s mismatch: got %#v want %#v", key, got, want)
	}
}

func currentTestExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve executable: %v", err)
	}
	return path
}

func tomlQuoted(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func assertFileEquals(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file %s changed\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

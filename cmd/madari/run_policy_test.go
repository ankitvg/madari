package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestRunDryRunReportsDeclaredEffectiveAndStrongestRequiredPolicy(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	manifest := registry.Manifest{
		Name: "docs.server", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"}, Access: codexPolicyRenderAccess(),
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save policy manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "advisory", Members: []string{manifest.Name}}); err != nil {
		t.Fatalf("save advisory ring: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{manifest.Name},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save required ring: %v", err)
	}

	result := runCmd(store, "run", "codex", "--ring", "advisory", "--ring", "required", "--dry-run", "--json", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("required policy dry-run failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.Ready || len(plan.Servers) != 1 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	server := plan.Servers[0]
	if server.Policy.RingEnforcement != registry.PolicyEnforcementRequired ||
		server.Policy.SupportState != "supported" ||
		server.Policy.EnforcementClassification != "exact" ||
		!reflect.DeepEqual(server.Policy.RequiredBy, []string{"required"}) {
		t.Fatalf("required ring did not win for shared server: %#v", server.Policy)
	}
	if server.Policy.Declared == nil || server.Policy.Declared.DefaultApproval == nil || *server.Policy.Declared.DefaultApproval != string(registry.ApprovalBehaviorAlwaysPrompt) {
		t.Fatalf("portable declared policy missing: %#v", server.Policy.Declared)
	}
	effective := server.Policy.Effective
	if effective == nil ||
		!reflect.DeepEqual(effective.EnabledTools, []string{"tools.inherit", "tools.read", "tools.write"}) ||
		!reflect.DeepEqual(effective.DisabledTools, []string{"tools.delete", "tools.remove"}) ||
		!reflect.DeepEqual(effective.RequestedOAuthScopes, []string{"repo.read", "repo.write"}) ||
		effective.DefaultToolsApprovalMode != "prompt" ||
		effective.ToolApprovalModes["tools.read"] != "auto" ||
		effective.ToolApprovalModes["tools.write"] != "approve" {
		t.Fatalf("Codex effective policy mismatch: %#v", effective)
	}
	if plan.PolicyControls.ToolFiltering != "client-enforced" ||
		plan.PolicyControls.OAuthScopes != "requested/client-configured/provider-unverified" ||
		plan.PolicyControls.Approvals != "client-control/not-authorization" ||
		plan.PolicyControls.Instructions != "contracts-and-skills-advisory" {
		t.Fatalf("policy controls are ambiguous: %#v", plan.PolicyControls)
	}
	if len(plan.LaunchDigest) != 64 || len(plan.PolicyDigest) != 64 || len(plan.ContentHashes.Rings) != 2 || len(plan.ContentHashes.Servers) != 1 {
		t.Fatalf("immutable launch evidence missing: %#v", plan)
	}
	authorityByControl := map[string]string{}
	for _, control := range plan.Authority.Effective {
		authorityByControl[control.Control] = string(control.EnforcedBy) + "/" + string(control.Verification)
	}
	for control, want := range map[string]string{
		"mcp-tool-filtering": "client/configured",
		"oauth-scopes":       "provider/unverified",
		"tool-approvals":     "client/configured",
	} {
		if authorityByControl[control] != want {
			t.Fatalf("authority %s mismatch: got %q want %q (%#v)", control, authorityByControl[control], want, plan.Authority)
		}
	}

	textResult := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--", "inspect")
	if textResult.code != 0 {
		t.Fatalf("text dry-run failed: %s", textResult.stderr)
	}
	for _, want := range []string{
		"policy controls: tool-filtering=client-enforced",
		"oauth-scopes=requested/client-configured/provider-unverified",
		"approvals=client-control/not-authorization",
		"instructions=contracts-and-skills-advisory",
		"launch digest:",
		"policy digest:",
		"requested authority:",
		"mcp-tool-filtering enforced_by=client verification=configured",
		"oauth-scopes enforced_by=provider verification=unverified",
		"policy: support=supported enforcement=exact",
		"declared policy: allowed_tools=[tools.inherit,tools.read,tools.write]",
		"effective policy: enabled_tools=tools.inherit,tools.read,tools.write",
	} {
		if !strings.Contains(textResult.stdout, want) {
			t.Fatalf("text policy report missing %q:\n%s", want, textResult.stdout)
		}
	}
}

func TestRunDryRunReportsAdvisoryPolicyWithoutMandatoryClaim(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "advisory", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "advisory", "--dry-run", "--json", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("advisory dry-run failed: %s", result.stderr)
	}
	policy := decodeRunPlan(t, result.stdout).Servers[0].Policy
	if policy.SupportState != "supported" || policy.EnforcementClassification != "advisory" || policy.RingEnforcement != "none" || len(policy.RequiredBy) != 0 {
		t.Fatalf("advisory policy made a mandatory claim: %#v", policy)
	}
}

func TestRunAdvisoryPolicyReportsUnsupportedOldCodexTruthfully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	store := newTestStore(t)
	marker := filepath.Join(t.TempDir(), "executed")
	installPolicyVersionCodex(t, "0.138.9", marker)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "advisory", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "advisory", "--dry-run", "--json", "--", "inspect")
	if result.code == 0 {
		t.Fatalf("old Codex should not report a runnable advisory policy: %s", result.stdout)
	}
	plan := decodeRunPlan(t, result.stdout)
	policy := plan.Servers[0].Policy
	if plan.PolicyRequired || policy.RingEnforcement != "none" || policy.SupportState != "unsupported" || policy.EnforcementClassification != "blocked" {
		t.Fatalf("advisory support state was not truthful: %#v", policy)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old Codex executed advisory policy: %v", err)
	}
}

func TestRunLegacyNoAccessRemainsCompatibleWithOlderCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	store := newTestStore(t)
	marker := filepath.Join(t.TempDir(), "executed")
	installPolicyVersionCodex(t, "0.138.9", marker)
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
	}); err != nil {
		t.Fatalf("save legacy manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{Name: "legacy", Members: []string{"docs"}}); err != nil {
		t.Fatalf("save legacy ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "legacy", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("legacy run was broken by policy compatibility checks: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("legacy Codex did not execute: %v", err)
	}
}

func TestRunDryRunPreservesDeclaredClearAndReportsTargetDefault(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	empty := []string{}
	inherit := registry.ApprovalBehaviorInherit
	toolApprovals := map[string]registry.ApprovalBehavior{"read": registry.ApprovalBehaviorInherit}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools: &allowed, DeniedTools: &empty, OAuthScopes: &empty,
			DefaultApproval: &inherit, ToolApprovals: &toolApprovals,
		},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--json", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("clear policy dry-run failed: %s", result.stderr)
	}
	policy := decodeRunPlan(t, result.stdout).Servers[0].Policy
	if policy.Declared == nil || policy.Declared.DeniedTools == nil || len(*policy.Declared.DeniedTools) != 0 ||
		policy.Declared.OAuthScopes == nil || len(*policy.Declared.OAuthScopes) != 0 ||
		policy.Declared.DefaultApproval == nil || *policy.Declared.DefaultApproval != string(registry.ApprovalBehaviorInherit) ||
		policy.Declared.ToolApprovals == nil || (*policy.Declared.ToolApprovals)["read"] != string(registry.ApprovalBehaviorInherit) {
		t.Fatalf("declared clear presence was lost: %#v", policy.Declared)
	}
	if policy.Effective == nil || !reflect.DeepEqual(policy.Effective.EnabledTools, []string{"read"}) ||
		len(policy.Effective.DisabledTools) != 0 || len(policy.Effective.RequestedOAuthScopes) != 0 ||
		policy.Effective.DefaultToolsApprovalMode != "" || len(policy.Effective.ToolApprovalModes) != 0 {
		t.Fatalf("effective clear did not resolve to target defaults: %#v", policy.Effective)
	}
	textResult := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--", "inspect")
	if textResult.code != 0 || !strings.Contains(textResult.stdout, "disabled_tools=target-default requested_oauth_scopes=target-default default_tools_approval_mode=target-default tool_approval_modes={}") {
		t.Fatalf("text clear reporting is ambiguous: code=%d stdout=%s stderr=%s", textResult.code, textResult.stdout, textResult.stderr)
	}
}

func TestRunRequiredPolicyBlocksOldCodexBeforeExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	marker := filepath.Join(t.TempDir(), "executed")
	installPolicyVersionCodex(t, "0.138.9", marker)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs"}, Skills: []string{"release"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--json", "--", "inspect")
	if result.code == 0 || !strings.Contains(result.stderr, "launch plan is not ready") {
		t.Fatalf("old Codex should block readiness: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	plan := decodeRunPlan(t, result.stdout)
	server := plan.Servers[0]
	if plan.Ready || server.Status != "blocked" || server.Policy.SupportState != "unsupported" || server.Policy.EnforcementClassification != "blocked" {
		t.Fatalf("old Codex downgrade was not classified: %#v", plan)
	}
	if !strings.Contains(strings.Join(plan.Errors, "\n"), "stable Codex CLI 0.139.x") {
		t.Fatalf("old Codex error is not actionable: %#v", plan.Errors)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old Codex was executed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required run materialized a permanent skill before readiness: %v", err)
	}
	nonDry := runCmd(store, "run", "codex", "--ring", "required", "--", "inspect")
	if nonDry.code == 0 || !strings.Contains(nonDry.stderr, "launch plan is not ready") {
		t.Fatalf("non-dry old Codex run crossed readiness boundary: stdout=%s stderr=%s", nonDry.stdout, nonDry.stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-dry old Codex executed: %v", err)
	}
}

func TestRunSkillOnlyRequiredPolicyStillGatesCodexCompatibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	store := newTestStore(t)
	projectDir := t.TempDir()
	chdirForTest(t, projectDir)
	marker := filepath.Join(t.TempDir(), "executed")
	installPolicyVersionCodex(t, "0.138.9", marker)
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if err := store.SaveRing(registry.Ring{
		Name: "workflow", Skills: []string{"release"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save skill-only ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "workflow", "--dry-run", "--json", "--", "release")
	if result.code == 0 {
		t.Fatalf("skill-only required policy bypassed compatibility gate: %s", result.stdout)
	}
	plan := decodeRunPlan(t, result.stdout)
	if !plan.PolicyRequired || plan.Ready || !strings.Contains(strings.Join(plan.Errors, "\n"), "stable Codex CLI 0.139.x") {
		t.Fatalf("skill-only policy gate was not reported: %#v", plan)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old Codex was executed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".agents", "skills", "release", registry.SkillFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill-only required run materialized before compatibility: %v", err)
	}
}

func TestRunRequiredPolicyReportsUnrepresentableTimeoutBlocked(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp", TimeoutMS: 1000,
		Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "required", "--dry-run", "--json", "--", "inspect")
	if result.code == 0 {
		t.Fatalf("unrepresentable timeout should block: %s", result.stdout)
	}
	server := decodeRunPlan(t, result.stdout).Servers[0]
	if server.Policy.SupportState != "unsupported" || server.Policy.EnforcementClassification != "blocked" || server.Status != "blocked" {
		t.Fatalf("timeout downgrade not classified: %#v", server)
	}
}

func TestCodexRunOverridesUsePlannedManifestSnapshot(t *testing.T) {
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	manifest := registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"required"}, "inspect")
	if err != nil || !plan.Ready {
		t.Fatalf("build plan: ready=%t err=%v errors=%v", plan.Ready, err, plan.Errors)
	}
	changed := []string{"write"}
	manifest.Access.AllowedTools = &changed
	if err := store.Save(manifest); err != nil {
		t.Fatalf("mutate store after plan: %v", err)
	}
	overrides := plan.Artifact.CodexOverrides()
	if len(overrides) != 1 || !strings.Contains(overrides[0], `enabled_tools = ["read"]`) || strings.Contains(overrides[0], `enabled_tools = ["write"]`) {
		t.Fatalf("override re-read a changed manifest instead of using the plan snapshot: %#v", overrides)
	}
}

func TestRunRequiredPolicyExecutesWithStrictCompiledOverride(t *testing.T) {
	store := newTestStore(t)
	logPath := installFakeCodex(t, 0)
	allowed := []string{"read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	if err := store.Save(registry.Manifest{
		Name: "docs.server", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed, DefaultApproval: &defaultApproval},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs.server"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	result := runCmd(store, "run", "codex", "--ring", "required", "--", "inspect")
	if result.code != 0 {
		t.Fatalf("required policy execution failed: stdout=%s stderr=%s", result.stdout, result.stderr)
	}
	args := readNULArgs(t, logPath)
	if len(args) < 2 || args[0] != "exec" || args[1] != "--strict-config" {
		t.Fatalf("required run did not enable strict config: %#v", args)
	}
	overrides := collectConfigOverrides(args)
	if len(overrides) != 1 || !strings.Contains(overrides[0], `"docs.server"`) ||
		!strings.Contains(overrides[0], `enabled_tools = ["read"]`) ||
		!strings.Contains(overrides[0], `default_tools_approval_mode = "prompt"`) {
		t.Fatalf("required run omitted compiled policy: %#v", overrides)
	}
}

func TestRunCodexRechecksPolicyCompatibilityBeforeSkillMaterialization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	store := newTestStore(t)
	installFakeCodex(t, 0)
	allowed := []string{"read"}
	if err := store.Save(registry.Manifest{
		Name: "docs", Command: mustCurrentExecutable(t), Enabled: true, Clients: []string{"codex"},
		Access: &registry.AccessProfile{AllowedTools: &allowed},
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	saveTestSkillPackage(t, store, "release", "Release workflow")
	if err := store.SaveRing(registry.Ring{
		Name: "required", Members: []string{"docs"}, Skills: []string{"release"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}); err != nil {
		t.Fatalf("save ring: %v", err)
	}
	plan, err := (cliApp{store: store}).buildRunPlan("codex", []string{"required"}, "inspect")
	if err != nil || !plan.Ready {
		t.Fatalf("build exact plan: ready=%t err=%v errors=%v", plan.Ready, err, plan.Errors)
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatalf("locate fake Codex: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	replaced := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then printf 'codex-cli 0.138.9\\n'; exit 0; fi\n" +
		"printf executed > '" + marker + "'\n"
	if err := os.WriteFile(codexPath, []byte(replaced), 0o755); err != nil {
		t.Fatalf("replace Codex after plan: %v", err)
	}
	if err := store.RemoveSkill("release"); err != nil {
		t.Fatalf("remove planned skill to detect materialization: %v", err)
	}
	err = runCodex(cliApp{store: store}, plan)
	if err == nil || !strings.Contains(err.Error(), "stable Codex CLI 0.139.x") {
		t.Fatalf("executor did not recheck compatibility first: %v", err)
	}
	if strings.Contains(err.Error(), "skill") {
		t.Fatalf("skill materialization happened before compatibility check: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaced Codex executed: %v", err)
	}
}

func installPolicyVersionCodex(t *testing.T, version, executionMarker string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then printf 'codex-cli " + version + "\\n'; exit 0; fi\n" +
		"printf executed > '" + executionMarker + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write versioned Codex fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

package launch

import (
	"os"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestCompileFreezesEveryRegistryInput(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	allowed := []string{"read"}
	manifest := registry.Manifest{
		Name: "docs", Command: command, Args: []string{"--mode", "before"},
		Enabled: true, Clients: []string{"codex"}, Env: map[string]string{"MODE": "before"},
		RequiredEnv: registry.RequiredEnv{Keys: []string{"DOCS_TOKEN"}},
		Access:      &registry.AccessProfile{AllowedTools: &allowed},
	}
	contract := &registry.RingContract{Summary: "before contract"}
	ring := registry.Ring{
		Name: "research", Members: []string{"docs"}, Skills: []string{"release"}, Contract: contract,
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}
	skill := testSkillPackage(t, "before skill")

	artifact, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "before prompt",
		Rings: []registry.Ring{ring}, Servers: []registry.Manifest{manifest}, Skills: []registry.SkillPackage{skill},
		Environment: EnvironmentInput{Declared: map[string]string{"DOCS_TOKEN": "before-token"}},
	})
	if err != nil {
		t.Fatalf("compile launch: %v", err)
	}
	wantDigest := artifact.Digest()
	wantPolicyDigest := artifact.PolicyDigest()
	wantOverride := artifact.CodexOverrides()[0]
	wantPrompt := artifact.Prompt()
	wantSkill := string(artifact.Skills()[0].Files[0].Content)

	allowed[0] = "write"
	manifest.Args[1] = "after"
	manifest.Env["MODE"] = "after"
	contract.Summary = "after contract"
	ring.Members[0] = "other"
	skill.Files[0].Content[0] = 'X'

	if artifact.Digest() != wantDigest || artifact.PolicyDigest() != wantPolicyDigest {
		t.Fatal("mutating compiler inputs changed immutable artifact digests")
	}
	if got := artifact.CodexOverrides()[0]; got != wantOverride || strings.Contains(got, "after") || strings.Contains(got, "write") {
		t.Fatalf("mutating compiler inputs changed override:\n%s", got)
	}
	if got := artifact.Prompt(); got != wantPrompt || strings.Contains(got, "after contract") {
		t.Fatalf("mutating compiler inputs changed prompt:\n%s", got)
	}
	if got := string(artifact.Skills()[0].Files[0].Content); got != wantSkill {
		t.Fatalf("mutating compiler inputs changed skill: %q", got)
	}

	servers := artifact.Servers()
	servers[0].Args[1] = "accessor mutation"
	servers[0].Access.AllowedTools = &[]string{"delete"}
	rings := artifact.Rings()
	rings[0].Contract.Summary = "accessor mutation"
	skills := artifact.Skills()
	skills[0].Files[0].Content[0] = 'Y'
	overrides := artifact.CodexOverrides()
	overrides[0] = "accessor mutation"
	if artifact.CodexOverrides()[0] != wantOverride || artifact.Prompt() != wantPrompt || string(artifact.Skills()[0].Files[0].Content) != wantSkill {
		t.Fatal("mutating accessor results changed immutable artifact")
	}
}

func TestLaunchDigestExcludesPromptRuntimeValuesContractsAndArguments(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	input := Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "secret prompt one",
		Rings: []registry.Ring{{Name: "research", Members: []string{"docs"}, Contract: &registry.RingContract{Summary: "secret contract one"}}},
		Servers: []registry.Manifest{{
			Name: "docs", Command: command, Args: []string{"--token=secret-one"}, Enabled: true, Clients: []string{"codex"},
			RequiredEnv: registry.RequiredEnv{Keys: []string{"DOCS_TOKEN"}},
		}},
		Environment: EnvironmentInput{Declared: map[string]string{"DOCS_TOKEN": "secret-token-one"}},
	}
	first, err := Compile(input)
	if err != nil {
		t.Fatalf("compile first launch: %v", err)
	}
	input.Prompt = "secret prompt two"
	input.Rings[0].Contract.Summary = "secret contract two"
	input.Servers[0].Args[0] = "--token=secret-two"
	input.Environment.Declared["DOCS_TOKEN"] = "secret-token-two"
	second, err := Compile(input)
	if err != nil {
		t.Fatalf("compile second launch: %v", err)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("receipt-safe launch digest changed with excluded content:\n%s\n%s", first.Digest(), second.Digest())
	}
	if first.Prompt() == second.Prompt() || first.CodexOverrides()[0] == second.CodexOverrides()[0] {
		t.Fatal("excluded values should still remain frozen in their distinct in-memory artifacts")
	}
}

func TestCompileReportsRequestedAndEffectiveAuthority(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	allowed := []string{"read"}
	scopes := []string{"docs.read"}
	approval := registry.ApprovalBehaviorAlwaysPrompt
	artifact, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings: []registry.Ring{{Name: "research", Members: []string{"docs"}, Contract: &registry.RingContract{Summary: "instructions"}}},
		Servers: []registry.Manifest{{
			Name: "docs", Command: command, Enabled: true, Clients: []string{"codex"},
			Access: &registry.AccessProfile{AllowedTools: &allowed, OAuthScopes: &scopes, DefaultApproval: &approval},
		}},
	})
	if err == nil {
		// OAuth scopes are invalid on stdio manifests, so use a remote transport.
		t.Fatal("expected stdio OAuth validation to fail")
	}
	artifact, err = Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings: []registry.Ring{{Name: "research", Members: []string{"docs"}, Contract: &registry.RingContract{Summary: "instructions"}}},
		Servers: []registry.Manifest{{
			Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp", Enabled: true, Clients: []string{"codex"},
			Access: &registry.AccessProfile{AllowedTools: &allowed, OAuthScopes: &scopes, DefaultApproval: &approval},
		}},
	})
	if err != nil {
		t.Fatalf("compile launch: %v", err)
	}
	want := map[string]AuthorityControl{
		"instructions":       {Control: "instructions", EnforcedBy: EnforcedByAdvisory, Verification: VerificationConfigured, Classification: ClassificationAdvisory},
		"mcp-tool-filtering": {Control: "mcp-tool-filtering", EnforcedBy: EnforcedByClient, Verification: VerificationConfigured, Classification: ClassificationAdvisory},
		"oauth-scopes":       {Control: "oauth-scopes", EnforcedBy: EnforcedByProvider, Verification: VerificationUnverified, Classification: ClassificationAdvisory},
		"tool-approvals":     {Control: "tool-approvals", EnforcedBy: EnforcedByClient, Verification: VerificationConfigured, Classification: ClassificationAdvisory},
	}
	authority := artifact.Authority()
	if len(authority.Requested) != len(want) || len(authority.Effective) != len(want)+4 {
		t.Fatalf("unexpected authority: %#v", authority)
	}
	for _, control := range authority.Effective {
		expected, ok := want[control.Control]
		if ok && control != expected {
			t.Fatalf("unexpected authority control: %#v", control)
		}
	}
}

func TestCompileAuthorityDoesNotUpgradeUnrelatedAdvisoryServer(t *testing.T) {
	allowedRequired := []string{"required.read"}
	allowedAdvisory := []string{"advisory.read"}
	servers := []registry.Manifest{
		{Name: "required", Transport: registry.TransportHTTP, URL: "https://required.example/mcp", Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowedRequired}},
		{Name: "advisory", Transport: registry.TransportHTTP, URL: "https://advisory.example/mcp", Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowedAdvisory}},
	}
	requiredRing := registry.Ring{
		Name: "required-ring", Members: []string{"required"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}

	requiredOnly, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings: []registry.Ring{requiredRing}, Servers: servers[:1],
	})
	if err != nil {
		t.Fatalf("compile required launch: %v", err)
	}
	if got := authorityClassification(requiredOnly.Authority(), "mcp-tool-filtering"); got != ClassificationExact {
		t.Fatalf("required-only authority should be exact, got %s", got)
	}

	mixed, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings: []registry.Ring{requiredRing, {Name: "advisory-ring", Members: []string{"advisory"}}}, Servers: servers,
	})
	if err != nil {
		t.Fatalf("compile mixed launch: %v", err)
	}
	if got := authorityClassification(mixed.Authority(), "mcp-tool-filtering"); got != ClassificationAdvisory {
		t.Fatalf("unrelated advisory server was globally upgraded: got %s", got)
	}
}

func authorityClassification(authority Authority, control string) Classification {
	for _, candidate := range authority.Effective {
		if candidate.Control == control {
			return candidate.Classification
		}
	}
	return ""
}

func TestCompileReportsUnfilteredAccessInMixedSelection(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	allowed := []string{"read"}
	artifact, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings: []registry.Ring{{Name: "mixed", Members: []string{"filtered", "unfiltered"}}},
		Servers: []registry.Manifest{
			{Name: "filtered", Command: command, Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowed}},
			{Name: "unfiltered", Command: command, Enabled: true, Clients: []string{"codex"}},
		},
	})
	if err != nil {
		t.Fatalf("compile mixed launch: %v", err)
	}
	controls := map[string]AuthorityControl{}
	for _, control := range artifact.Authority().Effective {
		controls[control.Control] = control
	}
	if got := controls["mcp-tool-filtering"]; got.EnforcedBy != EnforcedByClient || got.Verification != VerificationConfigured || got.Classification != ClassificationAdvisory {
		t.Fatalf("filtered member authority missing: %#v", artifact.Authority())
	}
	if got := controls["mcp-access"]; got.EnforcedBy != EnforcedByNone || got.Verification != VerificationUnverified || got.Classification != ClassificationNone {
		t.Fatalf("unfiltered member authority missing: %#v", artifact.Authority())
	}
}

func TestReceiptSafeSkillHashExcludesPrivatePackageContents(t *testing.T) {
	build := func(body, reference string, withScript bool) registry.SkillPackage {
		files := []registry.SkillPackageFile{
			{Path: registry.SkillFileName, Content: []byte("---\nname: release\ndescription: Release workflow\n---\n\n" + body + "\n"), Mode: 0o644},
			{Path: "references/private.md", Content: []byte(reference), Mode: 0o644},
		}
		if withScript {
			files = append(files, registry.SkillPackageFile{Path: "scripts/check.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: 0o755})
		}
		pkg, err := registry.NewSkillPackage(files, "release")
		if err != nil {
			t.Fatalf("build skill package: %v", err)
		}
		return pkg
	}
	compile := func(pkg registry.SkillPackage) *Artifact {
		artifact, err := Compile(Input{
			Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
			Rings: []registry.Ring{{Name: "workflow", Skills: []string{"release"}}}, Skills: []registry.SkillPackage{pkg},
		})
		if err != nil {
			t.Fatalf("compile skill launch: %v", err)
		}
		return artifact
	}

	first := compile(build("private body one", "private reference one", false))
	second := compile(build("private body two", "private reference two", false))
	if first.ContentHashes().Skills[0].SHA256 == second.ContentHashes().Skills[0].SHA256 {
		t.Fatal("full immutable content hash must still distinguish private skill contents")
	}
	if first.ReceiptContentHashes().Skills[0].SHA256 != second.ReceiptContentHashes().Skills[0].SHA256 {
		t.Fatal("receipt-safe skill hash fingerprinted private package contents")
	}
	third := compile(build("private body two", "private reference two", true))
	if second.ReceiptContentHashes().Skills[0].SHA256 == third.ReceiptContentHashes().Skills[0].SHA256 {
		t.Fatal("receipt-safe skill hash must retain non-content structural evidence")
	}
}

func TestCompileCodexSkillOnlyLaunchStillClearsMCPServers(t *testing.T) {
	artifact, err := Compile(Input{
		Target: "codex", WorkingDirectory: t.TempDir(), Prompt: "inspect",
		Rings:  []registry.Ring{{Name: "workflow", Skills: []string{"release"}}},
		Skills: []registry.SkillPackage{testSkillPackage(t, "instructions")},
	})
	if err != nil {
		t.Fatalf("compile launch: %v", err)
	}
	if got := artifact.CodexOverrides(); len(got) != 1 || got[0] != "mcp_servers={  }" {
		t.Fatalf("skill-only launch must explicitly clear MCP servers: %#v", got)
	}
	if !artifact.StrictConfig() {
		t.Fatal("every Codex artifact must require strict parsing for the shell environment policy")
	}
	authority := artifact.Authority()
	foundNone := false
	for _, control := range authority.Effective {
		if control.Control == "mcp-access" && control.EnforcedBy == EnforcedByNone && control.Verification == VerificationUnverified {
			foundNone = true
		}
	}
	if !foundNone {
		t.Fatalf("skill-only launch did not report absent MCP enforcement: %#v", authority)
	}
}

func testSkillPackage(t *testing.T, body string) registry.SkillPackage {
	t.Helper()
	pkg, err := registry.NewSkillPackage([]registry.SkillPackageFile{{
		Path:    registry.SkillFileName,
		Content: []byte("---\nname: release\ndescription: Release workflow\n---\n\n" + body + "\n"),
		Mode:    0o644,
	}}, "release")
	if err != nil {
		t.Fatalf("build skill package: %v", err)
	}
	return pkg
}

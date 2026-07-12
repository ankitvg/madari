package policy

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ankitvg/madari/internal/registry"
)

func TestCapabilitiesDeclareEveryTargetSurfaceFailClosed(t *testing.T) {
	wantTargets := []string{"claude-code", "claude-desktop", "codex", "gemini", "vibe"}
	if got := Targets(); !reflect.DeepEqual(got, wantTargets) {
		t.Fatalf("unexpected policy targets: got %#v want %#v", got, wantTargets)
	}

	features := []Feature{
		FeatureToolAllowlist,
		FeatureToolDenylist,
		FeatureOAuthScopes,
		FeatureDefaultApproval,
		FeatureToolApprovals,
	}
	for _, target := range wantTargets {
		for _, surface := range Surfaces() {
			capabilities, ok := CapabilitiesFor(target, surface)
			if !ok {
				t.Fatalf("missing declaration for %s %s", target, surface)
			}
			expectCodexCompiler := target == "codex"
			if capabilities.Compiler != expectCodexCompiler {
				t.Fatalf("unexpected compiler declaration for %s %s: %#v", target, surface, capabilities)
			}
			for _, feature := range features {
				if capabilities.Supports(feature) != expectCodexCompiler {
					t.Fatalf("unexpected %s support for %s %s: %#v", feature, target, surface, capabilities)
				}
			}
		}
	}
}

func TestValidateRequiredRingLeavesAdvisoryRingBackwardCompatible(t *testing.T) {
	result := ValidateRequiredRing(
		registry.Ring{Name: "legacy", Members: []string{"missing"}},
		nil,
		"codex",
		SurfacePersistent,
	)
	if result.Required || result.Classification != SupportNotRequired || !result.Ready() || result.Err() != nil {
		t.Fatalf("advisory ring should bypass enforcement preflight: %#v", result)
	}
}

func TestValidateRequiredRingClassifiesUnsupportedDeclaredFeatures(t *testing.T) {
	allowed := []string{"read"}
	denied := []string{"delete"}
	scopes := []string{"documents.read"}
	defaultApproval := registry.ApprovalBehaviorAlwaysPrompt
	toolApprovals := map[string]registry.ApprovalBehavior{"read": registry.ApprovalBehaviorAlwaysAllow}
	manifest := registry.Manifest{
		Name:    "docs",
		Enabled: true,
		Clients: []string{"gemini"},
		Access: &registry.AccessProfile{
			AllowedTools:    &allowed,
			DeniedTools:     &denied,
			OAuthScopes:     &scopes,
			DefaultApproval: &defaultApproval,
			ToolApprovals:   &toolApprovals,
		},
	}

	result := ValidateRequiredRing(requiredRing("research", "docs"), []registry.Manifest{manifest}, "gemini", SurfacePersistent)
	if result.Classification != SupportUnsupported || result.Ready() {
		t.Fatalf("expected unsupported result, got: %#v", result)
	}
	wantFeatures := []Feature{
		FeatureToolAllowlist,
		FeatureToolDenylist,
		FeatureOAuthScopes,
		FeatureDefaultApproval,
		FeatureToolApprovals,
	}
	gotFeatures := make([]Feature, 0, len(result.Issues))
	if len(result.Issues) == 0 || result.Issues[0].Code != IssueUnsupportedCompiler {
		t.Fatalf("expected compiler support issue first, got: %#v", result.Issues)
	}
	for _, issue := range result.Issues[1:] {
		if issue.Code != IssueUnsupportedFeature {
			t.Fatalf("expected remaining unsupported-feature issues, got: %#v", result.Issues)
		}
		gotFeatures = append(gotFeatures, issue.Feature)
	}
	if !reflect.DeepEqual(gotFeatures, wantFeatures) {
		t.Fatalf("feature issue order drift: got %#v want %#v", gotFeatures, wantFeatures)
	}

	var validationErr *ValidationError
	if !errors.As(result.Err(), &validationErr) {
		t.Fatalf("expected structured validation error, got: %v", result.Err())
	}
	if validationErr.Result.Classification != SupportUnsupported || !strings.Contains(validationErr.Error(), "gemini persistent policy support is unsupported") {
		t.Fatalf("unexpected actionable error: %v", validationErr)
	}
}

func TestValidateRequiredRingReportsMemberProblemsDeterministically(t *testing.T) {
	allowed := []string{"read"}
	ring := requiredRing("research", "wrong", "missing", "disabled", "unbounded")
	manifests := []registry.Manifest{
		{Name: "wrong", Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowed}},
		{Name: "disabled", Enabled: false, Clients: []string{"gemini"}},
		{Name: "unbounded", Enabled: true, Clients: []string{"gemini"}},
	}

	result := ValidateRequiredRing(ring, manifests, "gemini", SurfaceRender)
	if result.Classification != SupportInvalid || result.Ready() {
		t.Fatalf("expected invalid member result, got: %#v", result)
	}
	want := []struct {
		code   IssueCode
		member string
	}{
		{IssueUnsupportedCompiler, ""},
		{IssueDisabledMember, "disabled"},
		{IssueUnboundedMember, "disabled"},
		{IssueMissingMember, "missing"},
		{IssueUnboundedMember, "unbounded"},
		{IssueWrongTarget, "wrong"},
		{IssueUnsupportedFeature, "wrong"},
	}
	if len(result.Issues) != len(want) {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
	for i, expected := range want {
		if result.Issues[i].Code != expected.code || result.Issues[i].Member != expected.member {
			t.Fatalf("issue %d drift: got %#v want code=%s member=%s", i, result.Issues[i], expected.code, expected.member)
		}
	}

	message := result.Err().Error()
	for _, fragment := range []string{
		`member "disabled" is disabled`,
		`member "missing" is missing from the registry`,
		`member "unbounded" is unbounded`,
		`member "wrong" does not target "gemini"`,
	} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("error missing actionable detail %q: %s", fragment, message)
		}
	}
}

func TestValidateRequiredRingTreatsExplicitEmptyAllowlistAsUnbounded(t *testing.T) {
	empty := []string{}
	manifest := registry.Manifest{
		Name:    "docs",
		Command: currentExecutable(t),
		Enabled: true,
		Clients: []string{"codex"},
		Access:  &registry.AccessProfile{AllowedTools: &empty},
	}
	result := ValidateRequiredRing(requiredRing("research", "docs"), []registry.Manifest{manifest}, "codex", SurfaceRun)
	if result.Classification != SupportInvalid || len(result.Issues) != 1 {
		t.Fatalf("expected explicit-clear allowlist to remain unbounded, got: %#v", result)
	}
	if result.Issues[0].Code != IssueUnboundedMember {
		t.Fatalf("unexpected explicit-clear issue order: %#v", result.Issues)
	}
}

func TestValidateRequiredExecutionOnlyRingDoesNotRequireAccessProfile(t *testing.T) {
	ring := requiredRing("execution-only", "docs")
	ring.Policy.Execution = &registry.ExecutionPolicy{
		AmbientEnv: registry.ExecutionAmbientEnvDeny, Sandbox: registry.ExecutionSandboxReadOnly,
		MaxDuration: "10m", CredentialExposure: registry.ExecutionCredentialExposureRunProcess,
	}
	manifest := registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"},
	}
	result := ValidateRequiredRing(ring, []registry.Manifest{manifest}, "codex", SurfaceRun)
	if result.Required || result.Classification != SupportNotRequired || !result.Ready() {
		t.Fatalf("execution-only policy should not select access enforcement: %#v", result)
	}
}

func currentExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	return path
}

func TestValidateRequiredRingAllowsSkillOnlyWithCompiler(t *testing.T) {
	ring := registry.Ring{
		Name:   "workflow",
		Skills: []string{"release"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}
	result := ValidateRequiredRing(ring, nil, "codex", SurfaceRun)
	if result.Classification != SupportSupported || !result.Ready() || len(result.Issues) != 0 {
		t.Fatalf("skill-only required ring should use the Codex run compiler: %#v", result)
	}
}

func TestValidateRequiredCodexRingBlocksUnrepresentableServerFields(t *testing.T) {
	allowed := []string{"read"}
	base := registry.Manifest{
		Name: "docs", Transport: registry.TransportHTTP, URL: "https://example.com/mcp",
		Enabled: true, Clients: []string{"codex"}, Access: &registry.AccessProfile{AllowedTools: &allowed},
	}
	cases := []struct {
		name     string
		surface  Surface
		mutate   func(*registry.Manifest)
		fragment string
	}{
		{
			name: "timeout persistent", surface: SurfacePersistent, fragment: "timeout_ms",
			mutate: func(manifest *registry.Manifest) { manifest.TimeoutMS = 5000 },
		},
		{
			name: "secret header render", surface: SurfaceRender, fragment: "secret_headers",
			mutate: func(manifest *registry.Manifest) {
				manifest.Headers = map[string]string{"x-private-token": "secret"}
				manifest.SecretHeaders = registry.SecretHeaders{Keys: []string{"x-private-token"}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := base
			tc.mutate(&manifest)
			result := ValidateRequiredRing(requiredRing("restricted", "docs"), []registry.Manifest{manifest}, "codex", tc.surface)
			if result.Ready() || result.Classification != SupportUnsupported || len(result.Issues) != 1 || result.Issues[0].Code != IssueUnsupportedServerField {
				t.Fatalf("expected unsupported server field: %#v", result)
			}
			if !strings.Contains(result.Issues[0].Message, tc.fragment) {
				t.Fatalf("issue missing %q: %#v", tc.fragment, result.Issues[0])
			}
		})
	}
}

func TestValidateRequiredRingRejectsUnknownTargetAndSurface(t *testing.T) {
	ring := requiredRing("research")

	unknownTarget := ValidateRequiredRing(ring, nil, "future-client", SurfacePersistent)
	if unknownTarget.Classification != SupportInvalid || len(unknownTarget.Issues) != 1 || unknownTarget.Issues[0].Code != IssueUnknownTarget {
		t.Fatalf("unexpected unknown target result: %#v", unknownTarget)
	}

	unknownSurface := ValidateRequiredRing(ring, nil, "codex", Surface("future"))
	if unknownSurface.Classification != SupportInvalid || len(unknownSurface.Issues) != 1 || unknownSurface.Issues[0].Code != IssueUnknownSurface {
		t.Fatalf("unexpected unknown surface result: %#v", unknownSurface)
	}
}

func TestValidateAttachedRequiredRingsChecksOnlyRecordedSources(t *testing.T) {
	allowed := []string{"read"}
	manifest := registry.Manifest{
		Name:    "docs",
		Command: currentExecutable(t),
		Enabled: true,
		Clients: []string{"codex"},
		Access:  &registry.AccessProfile{AllowedTools: &allowed},
	}
	rings := []registry.Ring{
		{Name: "advisory", Members: []string{"docs"}},
		requiredRing("required", "docs"),
	}

	if err := ValidateAttachedRequiredRings(rings, []string{"advisory"}, []registry.Manifest{manifest}, "codex", SurfacePersistent); err != nil {
		t.Fatalf("advisory attached ring should remain compatible: %v", err)
	}
	if err := ValidateAttachedRequiredRings(rings, []string{"required", "required"}, []registry.Manifest{manifest}, "codex", SurfacePersistent); err != nil {
		t.Fatalf("supported required attached ring should compile: %v", err)
	}
	if err := ValidateAttachedRequiredRings(rings, []string{"missing"}, []registry.Manifest{manifest}, "codex", SurfacePersistent); err == nil || !strings.Contains(err.Error(), `attached ring "missing" is missing`) {
		t.Fatalf("missing attached ring should fail closed: %v", err)
	}
}

func requiredRing(name string, members ...string) registry.Ring {
	return registry.Ring{
		Name:    name,
		Members: members,
		Policy: &registry.RingPolicy{
			Enforcement: registry.PolicyEnforcementRequired,
		},
	}
}

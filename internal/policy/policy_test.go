package policy

import (
	"errors"
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
			if capabilities.Compiler {
				t.Fatalf("PR 1A must not enable the %s %s policy compiler", target, surface)
			}
			for _, feature := range features {
				if capabilities.Supports(feature) {
					t.Fatalf("PR 1A must fail closed, but %s %s supports %s", target, surface, feature)
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
		Clients: []string{"codex"},
		Access: &registry.AccessProfile{
			AllowedTools:    &allowed,
			DeniedTools:     &denied,
			OAuthScopes:     &scopes,
			DefaultApproval: &defaultApproval,
			ToolApprovals:   &toolApprovals,
		},
	}

	result := ValidateRequiredRing(requiredRing("research", "docs"), []registry.Manifest{manifest}, "codex", SurfacePersistent)
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
	if validationErr.Result.Classification != SupportUnsupported || !strings.Contains(validationErr.Error(), "codex persistent policy support is unsupported") {
		t.Fatalf("unexpected actionable error: %v", validationErr)
	}
}

func TestValidateRequiredRingReportsMemberProblemsDeterministically(t *testing.T) {
	allowed := []string{"read"}
	ring := requiredRing("research", "wrong", "missing", "disabled", "unbounded")
	manifests := []registry.Manifest{
		{Name: "wrong", Enabled: true, Clients: []string{"gemini"}, Access: &registry.AccessProfile{AllowedTools: &allowed}},
		{Name: "disabled", Enabled: false, Clients: []string{"codex"}},
		{Name: "unbounded", Enabled: true, Clients: []string{"codex"}},
	}

	result := ValidateRequiredRing(ring, manifests, "codex", SurfaceRender)
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
		`member "wrong" does not target "codex"`,
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
		Enabled: true,
		Clients: []string{"codex"},
		Access:  &registry.AccessProfile{AllowedTools: &empty},
	}
	result := ValidateRequiredRing(requiredRing("research", "docs"), []registry.Manifest{manifest}, "codex", SurfaceRun)
	if result.Classification != SupportInvalid || len(result.Issues) != 3 {
		t.Fatalf("expected unbounded plus unsupported explicit-clear issues, got: %#v", result)
	}
	if result.Issues[0].Code != IssueUnsupportedCompiler || result.Issues[1].Code != IssueUnboundedMember || result.Issues[2].Code != IssueUnsupportedFeature {
		t.Fatalf("unexpected explicit-clear issue order: %#v", result.Issues)
	}
}

func TestValidateRequiredRingBlocksSkillOnlyWithoutCompiler(t *testing.T) {
	ring := registry.Ring{
		Name:   "workflow",
		Skills: []string{"release"},
		Policy: &registry.RingPolicy{Enforcement: registry.PolicyEnforcementRequired},
	}
	result := ValidateRequiredRing(ring, nil, "codex", SurfaceRun)
	if result.Classification != SupportUnsupported || result.Ready() || len(result.Issues) != 1 {
		t.Fatalf("skill-only required ring must still require a compiler: %#v", result)
	}
	if result.Issues[0].Code != IssueUnsupportedCompiler {
		t.Fatalf("unexpected skill-only issue: %#v", result.Issues)
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
	if err := ValidateAttachedRequiredRings(rings, []string{"required", "required"}, []registry.Manifest{manifest}, "codex", SurfacePersistent); err == nil || !strings.Contains(err.Error(), "codex persistent policy support is unsupported") {
		t.Fatalf("required attached ring should fail closed: %v", err)
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

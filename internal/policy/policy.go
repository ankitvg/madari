// Package policy centralizes target capability declarations and validates
// policy-required rings before an operation can mutate or execute anything.
package policy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// Surface identifies the operation shape for which a target must compile a
// server access profile. Support can differ between persistent config, render,
// and ephemeral run paths even for the same target.
type Surface string

const (
	SurfacePersistent Surface = "persistent"
	SurfaceRender     Surface = "render"
	SurfaceRun        Surface = "run"
)

// Feature is one independently representable part of an access profile.
type Feature string

const (
	FeatureToolAllowlist   Feature = "tool-allowlist"
	FeatureToolDenylist    Feature = "tool-denylist"
	FeatureOAuthScopes     Feature = "oauth-scopes"
	FeatureDefaultApproval Feature = "default-approval"
	FeatureToolApprovals   Feature = "tool-approvals"
)

// Capabilities declares which access-profile features one target surface can
// represent without semantic loss.
type Capabilities struct {
	Compiler        bool `json:"compiler"`
	ToolAllowlist   bool `json:"tool_allowlist"`
	ToolDenylist    bool `json:"tool_denylist"`
	OAuthScopes     bool `json:"oauth_scopes"`
	DefaultApproval bool `json:"default_approval"`
	ToolApprovals   bool `json:"tool_approvals"`
}

// Supports reports whether the feature can be compiled on this surface.
func (c Capabilities) Supports(feature Feature) bool {
	switch feature {
	case FeatureToolAllowlist:
		return c.ToolAllowlist
	case FeatureToolDenylist:
		return c.ToolDenylist
	case FeatureOAuthScopes:
		return c.OAuthScopes
	case FeatureDefaultApproval:
		return c.DefaultApproval
	case FeatureToolApprovals:
		return c.ToolApprovals
	default:
		return false
	}
}

// TargetCapabilities is the central declaration for one supported Madari
// target. A compiler enables only the surface and features it implements
// losslessly; every unspecified surface remains unsupported.
type TargetCapabilities struct {
	Target     string       `json:"target"`
	Persistent Capabilities `json:"persistent"`
	Render     Capabilities `json:"render"`
	Run        Capabilities `json:"run"`
}

// ForSurface returns the capability declaration for surface.
func (c TargetCapabilities) ForSurface(surface Surface) (Capabilities, bool) {
	switch surface {
	case SurfacePersistent:
		return c.Persistent, true
	case SurfaceRender:
		return c.Render, true
	case SurfaceRun:
		return c.Run, true
	default:
		return Capabilities{}, false
	}
}

var targetCapabilities = map[string]TargetCapabilities{
	"claude-desktop": {Target: "claude-desktop"},
	"claude-code":    {Target: "claude-code"},
	"codex": {
		Target: "codex",
		Persistent: Capabilities{
			Compiler: true, ToolAllowlist: true, ToolDenylist: true,
			OAuthScopes: true, DefaultApproval: true, ToolApprovals: true,
		},
		Render: Capabilities{
			Compiler: true, ToolAllowlist: true, ToolDenylist: true,
			OAuthScopes: true, DefaultApproval: true, ToolApprovals: true,
		},
	},
	"gemini": {Target: "gemini"},
	"vibe":   {Target: "vibe"},
}

// Targets returns all centrally declared targets in deterministic order.
func Targets() []string {
	targets := make([]string, 0, len(targetCapabilities))
	for target := range targetCapabilities {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// Surfaces returns every policy-compilation surface in deterministic order.
func Surfaces() []Surface {
	return []Surface{SurfacePersistent, SurfaceRender, SurfaceRun}
}

// CapabilitiesFor returns the declaration for target and surface.
func CapabilitiesFor(target string, surface Surface) (Capabilities, bool) {
	declaration, ok := targetCapabilities[strings.TrimSpace(target)]
	if !ok {
		return Capabilities{}, false
	}
	return declaration.ForSurface(surface)
}

// SupportClassification summarizes whether a ring can make an enforcement
// claim for a selected target surface.
type SupportClassification string

const (
	SupportNotRequired SupportClassification = "not-required"
	SupportSupported   SupportClassification = "supported"
	SupportUnsupported SupportClassification = "unsupported"
	SupportInvalid     SupportClassification = "invalid"
)

// IssueCode identifies one deterministic fail-closed reason.
type IssueCode string

const (
	IssueUnknownTarget          IssueCode = "unknown-target"
	IssueUnknownSurface         IssueCode = "unknown-surface"
	IssueMissingMember          IssueCode = "missing-member"
	IssueDisabledMember         IssueCode = "disabled-member"
	IssueWrongTarget            IssueCode = "wrong-target"
	IssueUnboundedMember        IssueCode = "unbounded-member"
	IssueUnsupportedCompiler    IssueCode = "unsupported-compiler"
	IssueUnsupportedFeature     IssueCode = "unsupported-feature"
	IssueUnsupportedTransport   IssueCode = "unsupported-transport"
	IssueUnsupportedServerField IssueCode = "unsupported-server-field"
	IssueInvalidCommand         IssueCode = "invalid-command"
)

// Issue is one actionable policy-preflight failure.
type Issue struct {
	Code    IssueCode `json:"code"`
	Member  string    `json:"member,omitempty"`
	Feature Feature   `json:"feature,omitempty"`
	Message string    `json:"message"`
}

// ValidationResult is the reusable support and error classification for one
// ring/target/surface selection.
type ValidationResult struct {
	Ring           string                `json:"ring"`
	Target         string                `json:"target"`
	Surface        Surface               `json:"surface"`
	Required       bool                  `json:"required"`
	Classification SupportClassification `json:"classification"`
	Issues         []Issue               `json:"issues"`
}

// Ready reports whether the operation may claim policy fidelity.
func (r ValidationResult) Ready() bool {
	return len(r.Issues) == 0
}

// Err returns an inspectable validation error when policy compilation is
// blocked.
func (r ValidationResult) Err() error {
	if r.Ready() {
		return nil
	}
	return &ValidationError{Result: r}
}

// ValidationError preserves the structured result while providing an
// actionable CLI error.
type ValidationError struct {
	Result ValidationResult
}

func (e *ValidationError) Error() string {
	details := make([]string, 0, len(e.Result.Issues))
	for _, issue := range e.Result.Issues {
		details = append(details, issue.Message)
	}
	message := fmt.Sprintf(
		"ring %q requires policy enforcement, but %s %s policy support is %s: %s",
		e.Result.Ring,
		e.Result.Target,
		e.Result.Surface,
		e.Result.Classification,
		strings.Join(details, "; "),
	)
	if e.Result.Classification == SupportUnsupported {
		message += `; choose a target surface with exact policy support or remove enforcement = "required" until a compiler is available`
	}
	return message
}

// ValidateRequiredRing determines whether every declared access restriction
// for a policy-required ring can be compiled exactly for target and surface.
// Rings without required enforcement remain backward compatible.
func ValidateRequiredRing(ring registry.Ring, manifests []registry.Manifest, target string, surface Surface) ValidationResult {
	target = strings.TrimSpace(target)
	result := ValidationResult{
		Ring:           ring.Name,
		Target:         target,
		Surface:        surface,
		Required:       ring.RequiresPolicyEnforcement(),
		Classification: SupportNotRequired,
		Issues:         []Issue{},
	}
	if !result.Required {
		return result
	}

	capabilities, targetKnown := CapabilitiesFor(target, surface)
	if !targetKnown {
		if _, knownTarget := targetCapabilities[target]; !knownTarget {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueUnknownTarget,
				Message: fmt.Sprintf("target %q has no policy capability declaration; choose a supported target", target),
			})
		} else {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueUnknownSurface,
				Message: fmt.Sprintf("surface %q has no policy capability declaration for target %q", surface, target),
			})
		}
		result.Classification = SupportInvalid
		return result
	}
	if !capabilities.Compiler {
		result.Issues = append(result.Issues, Issue{
			Code:    IssueUnsupportedCompiler,
			Message: fmt.Sprintf("%s %s has no lossless policy compiler yet", target, surface),
		})
	}

	manifestByName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		manifestByName[manifest.Name] = manifest
	}

	members := append([]string(nil), ring.Members...)
	sort.Strings(members)
	for _, rawMember := range members {
		member := strings.TrimSpace(rawMember)
		manifest, exists := manifestByName[member]
		if !exists {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueMissingMember,
				Member:  member,
				Message: fmt.Sprintf("member %q is missing from the registry; restore its manifest or remove it from the ring", member),
			})
			continue
		}
		if !manifest.Enabled {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueDisabledMember,
				Member:  member,
				Message: fmt.Sprintf("member %q is disabled; enable it before using this policy-required ring", member),
			})
		}
		if !manifest.HasClient(target) {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueWrongTarget,
				Member:  member,
				Message: fmt.Sprintf("member %q does not target %q; add that client target or remove the member from the ring", member, target),
			})
		}
		result.Issues = append(result.Issues, targetRepresentationIssues(manifest, target, surface)...)
		if !manifest.HasExplicitToolAllowlist() {
			result.Issues = append(result.Issues, Issue{
				Code:    IssueUnboundedMember,
				Member:  member,
				Feature: FeatureToolAllowlist,
				Message: fmt.Sprintf("member %q is unbounded; declare a non-empty [access].allowed_tools allowlist", member),
			})
		}

		for _, feature := range declaredFeatures(manifest.Access) {
			if capabilities.Supports(feature) {
				continue
			}
			result.Issues = append(result.Issues, Issue{
				Code:    IssueUnsupportedFeature,
				Member:  member,
				Feature: feature,
				Message: fmt.Sprintf("member %q declares %s, which %s %s cannot compile yet", member, featureField(feature), target, surface),
			})
		}
	}

	result.Classification = classify(result.Issues)
	return result
}

func targetRepresentationIssues(manifest registry.Manifest, target string, surface Surface) []Issue {
	if target != "codex" {
		return nil
	}
	issues := []Issue{}
	if manifest.IsRemote() {
		if manifest.TransportType() != registry.TransportHTTP {
			issues = append(issues, Issue{
				Code: IssueUnsupportedTransport, Member: manifest.Name,
				Message: fmt.Sprintf("member %q uses %s transport, which Codex %s cannot represent", manifest.Name, manifest.TransportType(), surface),
			})
		}
	} else if commandErr := clients.ValidateCommandPath(manifest.Command); commandErr != nil {
		issues = append(issues, Issue{
			Code: IssueInvalidCommand, Member: manifest.Name,
			Message: fmt.Sprintf("member %q cannot execute on Codex: %s", manifest.Name, commandErr.Message),
		})
	}
	if manifest.TimeoutMS > 0 {
		issues = append(issues, Issue{
			Code: IssueUnsupportedServerField, Member: manifest.Name,
			Message: fmt.Sprintf("member %q declares timeout_ms, which Codex %s cannot represent exactly", manifest.Name, surface),
		})
	}
	if surface == SurfaceRender && len(manifest.SecretHeaderNames()) > 0 {
		issues = append(issues, Issue{
			Code: IssueUnsupportedServerField, Member: manifest.Name,
			Message: fmt.Sprintf("member %q declares secret_headers whose values Codex render intentionally omits", manifest.Name),
		})
	}
	return issues
}

// ValidateAttachedRequiredRings applies required-ring policy validation only
// to ring ownership sources recorded for one target. Diagnostics and sync use
// this shared gate so a dry-run drift plan cannot claim readiness when the
// corresponding sync operation must fail closed.
func ValidateAttachedRequiredRings(rings []registry.Ring, attached []string, manifests []registry.Manifest, target string, surface Surface) error {
	byName := make(map[string]registry.Ring, len(rings))
	for _, ring := range rings {
		byName[ring.Name] = ring
	}

	seen := make(map[string]struct{}, len(attached))
	names := make([]string, 0, len(attached))
	for _, rawName := range attached {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ring, exists := byName[name]
		if !exists {
			return fmt.Errorf("attached ring %q is missing; restore its definition or detach it before sync so policy requirements cannot be bypassed", name)
		}
		if err := ValidateRequiredRing(ring, manifests, target, surface).Err(); err != nil {
			return err
		}
	}
	return nil
}

func declaredFeatures(access *registry.AccessProfile) []Feature {
	if access == nil {
		return nil
	}
	features := make([]Feature, 0, 5)
	if access.AllowedTools != nil {
		features = append(features, FeatureToolAllowlist)
	}
	if access.DeniedTools != nil {
		features = append(features, FeatureToolDenylist)
	}
	if access.OAuthScopes != nil {
		features = append(features, FeatureOAuthScopes)
	}
	if access.DefaultApproval != nil {
		features = append(features, FeatureDefaultApproval)
	}
	if access.ToolApprovals != nil {
		features = append(features, FeatureToolApprovals)
	}
	return features
}

func featureField(feature Feature) string {
	switch feature {
	case FeatureToolAllowlist:
		return "[access].allowed_tools"
	case FeatureToolDenylist:
		return "[access].denied_tools"
	case FeatureOAuthScopes:
		return "[access].oauth_scopes"
	case FeatureDefaultApproval:
		return "[access].default_approval"
	case FeatureToolApprovals:
		return "[access].tool_approvals"
	default:
		return string(feature)
	}
}

func classify(issues []Issue) SupportClassification {
	if len(issues) == 0 {
		return SupportSupported
	}
	for _, issue := range issues {
		if issue.Code != IssueUnsupportedCompiler && issue.Code != IssueUnsupportedFeature &&
			issue.Code != IssueUnsupportedTransport && issue.Code != IssueUnsupportedServerField {
			return SupportInvalid
		}
	}
	return SupportUnsupported
}

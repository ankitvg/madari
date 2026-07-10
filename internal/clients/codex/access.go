package codex

import (
	"sort"

	"github.com/ankitvg/madari/internal/registry"
)

// CompiledAccess is the Codex-native representation of one portable Madari
// access profile. Presence is part of the contract: nil fields were not
// declared and must preserve an existing native value during persistent sync,
// while non-nil empty slices and maps explicitly clear that native override.
// An empty approval string is the native omission produced by portable
// "inherit"; non-empty values are Codex's auto, prompt, or approve enums.
type CompiledAccess struct {
	EnabledTools    *[]string
	DisabledTools   *[]string
	Scopes          *[]string
	DefaultApproval *string
	ToolApprovals   *map[string]string
}

// CompileAccess maps Madari's portable access vocabulary into Codex-native
// values without mutating the registry profile. Registry validation guarantees
// the input approval values belong to the portable V1 vocabulary.
func CompileAccess(access *registry.AccessProfile) CompiledAccess {
	if access == nil {
		return CompiledAccess{}
	}

	compiled := CompiledAccess{
		EnabledTools:  sortedStringSetCopy(access.AllowedTools),
		DisabledTools: sortedStringSetCopy(access.DeniedTools),
		Scopes:        sortedStringSetCopy(access.OAuthScopes),
	}
	if access.DefaultApproval != nil {
		value := compileApproval(*access.DefaultApproval)
		compiled.DefaultApproval = &value
	}
	if access.ToolApprovals != nil {
		values := make(map[string]string, len(*access.ToolApprovals))
		for tool, approval := range *access.ToolApprovals {
			values[tool] = compileApproval(approval)
		}
		compiled.ToolApprovals = &values
	}
	return compiled
}

func compileApproval(approval registry.ApprovalBehavior) string {
	switch approval {
	case registry.ApprovalBehaviorAutomatic:
		return "auto"
	case registry.ApprovalBehaviorAlwaysPrompt:
		return "prompt"
	case registry.ApprovalBehaviorAlwaysAllow:
		return "approve"
	case registry.ApprovalBehaviorInherit:
		return ""
	default:
		// Invalid portable values are rejected by registry validation before a
		// manifest reaches a client compiler. Keep unknown values fail-closed by
		// omitting rather than forwarding a raw client-native enum.
		return ""
	}
}

func sortedStringSetCopy(values *[]string) *[]string {
	if values == nil {
		return nil
	}
	copyOfValues := append([]string(nil), (*values)...)
	sort.Strings(copyOfValues)
	if copyOfValues == nil {
		copyOfValues = []string{}
	}
	return &copyOfValues
}

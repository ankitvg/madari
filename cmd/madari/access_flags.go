package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

// accessFlags is shared by add and install so both server-registration paths
// expose the same portable access-profile vocabulary.
type accessFlags struct {
	allowedTools    stringList
	deniedTools     stringList
	oauthScopes     stringList
	defaultApproval optionalStringFlag
	toolApprovals   stringList
}

func (f *accessFlags) register(fs *flag.FlagSet, includeOAuthScopes bool) {
	fs.Var(&f.allowedTools, "allow-tool", "Allowed MCP tool name (repeatable)")
	fs.Var(&f.deniedTools, "deny-tool", "Denied MCP tool name (repeatable)")
	if includeOAuthScopes {
		fs.Var(&f.oauthScopes, "oauth-scope", "Requested OAuth scope (repeatable)")
	}
	fs.Var(&f.defaultApproval, "default-tool-approval", "Default portable tool approval behavior")
	fs.Var(&f.toolApprovals, "tool-approval", "Per-tool approval TOOL=BEHAVIOR (repeatable)")
}

func (f accessFlags) profile() (*registry.AccessProfile, error) {
	if len(f.allowedTools) == 0 && len(f.deniedTools) == 0 && len(f.oauthScopes) == 0 &&
		!f.defaultApproval.set && len(f.toolApprovals) == 0 {
		return nil, nil
	}

	profile := &registry.AccessProfile{}
	if len(f.allowedTools) > 0 {
		values := append([]string(nil), f.allowedTools...)
		profile.AllowedTools = &values
	}
	if len(f.deniedTools) > 0 {
		values := append([]string(nil), f.deniedTools...)
		profile.DeniedTools = &values
	}
	if len(f.oauthScopes) > 0 {
		values := append([]string(nil), f.oauthScopes...)
		profile.OAuthScopes = &values
	}
	if f.defaultApproval.set {
		value := strings.TrimSpace(f.defaultApproval.value)
		if value == "" {
			return nil, fmt.Errorf("invalid default tool approval %q: behavior must be non-empty", f.defaultApproval.value)
		}
		approval := registry.ApprovalBehavior(value)
		profile.DefaultApproval = &approval
	}
	if len(f.toolApprovals) > 0 {
		approvals := make(map[string]registry.ApprovalBehavior, len(f.toolApprovals))
		for _, assignment := range f.toolApprovals {
			tool, value, ok := strings.Cut(assignment, "=")
			tool = strings.TrimSpace(tool)
			value = strings.TrimSpace(value)
			if !ok || tool == "" || value == "" {
				return nil, fmt.Errorf("invalid tool approval %q, expected TOOL=BEHAVIOR", assignment)
			}
			if _, exists := approvals[tool]; exists {
				return nil, fmt.Errorf("duplicate tool approval for %q", tool)
			}
			approvals[tool] = registry.ApprovalBehavior(value)
		}
		profile.ToolApprovals = &approvals
	}
	return profile, nil
}

type optionalStringFlag struct {
	value string
	set   bool
}

func (f *optionalStringFlag) String() string {
	return f.value
}

func (f *optionalStringFlag) Set(value string) error {
	f.value = value
	f.set = true
	return nil
}

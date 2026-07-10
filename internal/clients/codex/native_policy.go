package codex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/policy"
	"github.com/ankitvg/madari/internal/registry"
)

type fidelityIssueKind string

const (
	fidelityUnknown         fidelityIssueKind = "unknown"
	fidelityDefaultApproval fidelityIssueKind = "default-approval"
	fidelityToolApproval    fidelityIssueKind = "tool-approval"
)

type nativeFidelityIssue struct {
	Kind fidelityIssueKind
	Path string
}

var knownServerFields = map[string]bool{
	"command": true, "args": true, "env": true, "env_vars": true, "cwd": true,
	"url": true, "bearer_token_env_var": true, "http_headers": true,
	"env_http_headers": true, "oauth_resource": true, "enabled": true,
	"required": true, "startup_timeout_sec": true, "tool_timeout_sec": true,
	"enabled_tools": true, "disabled_tools": true, "scopes": true,
	"default_tools_approval_mode": true, "tools": true,
}

func parseNativeAccess(name string, table map[string]any) (CompiledAccess, []nativeFidelityIssue, error) {
	var access CompiledAccess
	issues := make([]nativeFidelityIssue, 0)

	for _, field := range []struct {
		key string
		set func(*[]string)
	}{
		{key: "enabled_tools", set: func(value *[]string) { access.EnabledTools = value }},
		{key: "disabled_tools", set: func(value *[]string) { access.DisabledTools = value }},
		{key: "scopes", set: func(value *[]string) { access.Scopes = value }},
	} {
		values, present, err := optionalStringSlice(table, field.key)
		if err != nil {
			return CompiledAccess{}, nil, fmt.Errorf("parse mcp_servers.%s.%s: %w", name, field.key, err)
		}
		if present {
			copyOfValues := append([]string(nil), values...)
			field.set(&copyOfValues)
		}
	}

	if raw, present := table["default_tools_approval_mode"]; present {
		value, ok := raw.(string)
		if !ok {
			return CompiledAccess{}, nil, fmt.Errorf("parse mcp_servers.%s.default_tools_approval_mode: expected string", name)
		}
		access.DefaultApproval = &value
		if !supportedNativeApproval(value) {
			issues = append(issues, nativeFidelityIssue{
				Kind: fidelityDefaultApproval,
				Path: fmt.Sprintf("mcp_servers.%s.default_tools_approval_mode=%q", name, value),
			})
		}
	}

	if raw, present := table["tools"]; present {
		tools, ok := raw.(map[string]any)
		if !ok {
			return CompiledAccess{}, nil, fmt.Errorf("parse mcp_servers.%s.tools: expected table", name)
		}
		approvals := make(map[string]string)
		toolNames := sortedAnyMapKeys(tools)
		for _, tool := range toolNames {
			rawTool := tools[tool]
			toolTable, ok := rawTool.(map[string]any)
			if !ok {
				return CompiledAccess{}, nil, fmt.Errorf("parse mcp_servers.%s.tools.%s: expected table", name, tool)
			}
			for _, key := range sortedAnyMapKeys(toolTable) {
				if key != "approval_mode" {
					issues = append(issues, nativeFidelityIssue{
						Kind: fidelityUnknown,
						Path: fmt.Sprintf("mcp_servers.%s.tools.%s.%s", name, tool, key),
					})
				}
			}
			if rawApproval, exists := toolTable["approval_mode"]; exists {
				approval, ok := rawApproval.(string)
				if !ok {
					return CompiledAccess{}, nil, fmt.Errorf("parse mcp_servers.%s.tools.%s.approval_mode: expected string", name, tool)
				}
				approvals[tool] = approval
				if !supportedNativeApproval(approval) {
					issues = append(issues, nativeFidelityIssue{
						Kind: fidelityToolApproval,
						Path: fmt.Sprintf("mcp_servers.%s.tools.%s.approval_mode=%q", name, tool, approval),
					})
				}
			}
		}
		access.ToolApprovals = &approvals
	}

	for _, key := range sortedAnyMapKeys(table) {
		if !knownServerFields[key] {
			issues = append(issues, nativeFidelityIssue{
				Kind: fidelityUnknown,
				Path: fmt.Sprintf("mcp_servers.%s.%s", name, key),
			})
		}
	}
	if rawEnabled, present := table["enabled"]; present && rawEnabled == false {
		issues = append(issues, nativeFidelityIssue{
			Kind: fidelityUnknown,
			Path: fmt.Sprintf("mcp_servers.%s.enabled=false", name),
		})
	}
	if err := validateKnownNativeExtras(name, table); err != nil {
		return CompiledAccess{}, nil, err
	}
	return access, issues, nil
}

func validateKnownNativeExtras(name string, table map[string]any) error {
	if raw, ok := table["cwd"]; ok {
		if _, valid := raw.(string); !valid {
			return fmt.Errorf("parse mcp_servers.%s.cwd: expected string", name)
		}
	}
	if _, _, err := optionalStringMap(table, "env_http_headers"); err != nil {
		return fmt.Errorf("parse mcp_servers.%s.env_http_headers: %w", name, err)
	}
	if _, _, err := optionalBool(table, "required"); err != nil {
		return fmt.Errorf("parse mcp_servers.%s.required: %w", name, err)
	}
	for _, key := range []string{"startup_timeout_sec", "tool_timeout_sec"} {
		if raw, ok := table[key]; ok {
			switch raw.(type) {
			case int64, float64:
			default:
				return fmt.Errorf("parse mcp_servers.%s.%s: expected number", name, key)
			}
		}
	}
	return nil
}

func supportedNativeApproval(value string) bool {
	switch value {
	case "auto", "prompt", "approve":
		return true
	default:
		return false
	}
}

func equalDeclaredAccess(existing, desired CompiledAccess) bool {
	if !equalDeclaredStringSet(existing.EnabledTools, desired.EnabledTools) ||
		!equalDeclaredStringSet(existing.DisabledTools, desired.DisabledTools) ||
		!equalDeclaredStringSet(existing.Scopes, desired.Scopes) {
		return false
	}
	if desired.DefaultApproval != nil {
		if *desired.DefaultApproval == "" {
			if existing.DefaultApproval != nil {
				return false
			}
		} else if existing.DefaultApproval == nil || *existing.DefaultApproval != *desired.DefaultApproval {
			return false
		}
	}
	if desired.ToolApprovals != nil {
		wanted := effectiveToolApprovals(*desired.ToolApprovals)
		got := map[string]string{}
		if existing.ToolApprovals != nil {
			got = effectiveToolApprovals(*existing.ToolApprovals)
		}
		if !equalStringMap(got, wanted) {
			return false
		}
	}
	return true
}

func equalDeclaredStringSet(existing, desired *[]string) bool {
	if desired == nil {
		return true
	}
	if len(*desired) == 0 {
		return existing == nil
	}
	if existing == nil || len(*existing) != len(*desired) {
		return false
	}
	got := append([]string(nil), (*existing)...)
	want := append([]string(nil), (*desired)...)
	sort.Strings(got)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func effectiveToolApprovals(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for tool, approval := range values {
		if approval != "" {
			out[tool] = approval
		}
	}
	return out
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func policyUpdatedNames(updated []string, existing, desired map[string]serverConfig) []string {
	result := make([]string, 0, len(updated))
	for _, name := range updated {
		current, currentOK := existing[name]
		want, desiredOK := desired[name]
		if currentOK && desiredOK && !equalDeclaredAccess(current.Access, want.Access) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func mergeServerTable(raw any, desired serverConfig) (map[string]any, error) {
	table := map[string]any{}
	if raw != nil {
		existing, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected existing table")
		}
		table = cloneAnyMap(existing)
	}

	if desired.URL != "" {
		deleteKeys(table, "command", "args", "env", "env_vars", "cwd")
		table["url"] = desired.URL
		setOrDeleteString(table, "oauth_resource", desired.OAuthResource)
		setOrDeleteString(table, "bearer_token_env_var", desired.BearerTokenEnvVar)
		setOrDeleteStringMap(table, "http_headers", desired.HTTPHeaders)
	} else {
		deleteKeys(table, "url", "oauth_resource", "bearer_token_env_var", "http_headers", "env_http_headers")
		table["command"] = desired.Command
		setOrDeleteStringSlice(table, "args", desired.Args)
		setOrDeleteStringSlice(table, "env_vars", desired.EnvVars)
		setOrDeleteStringMap(table, "env", desired.Env)
	}
	applyDeclaredAccess(table, desired.Access)
	return table, nil
}

func applyDeclaredAccess(table map[string]any, access CompiledAccess) {
	applyStringSliceField(table, "enabled_tools", access.EnabledTools)
	applyStringSliceField(table, "disabled_tools", access.DisabledTools)
	applyStringSliceField(table, "scopes", access.Scopes)
	if access.DefaultApproval != nil {
		if *access.DefaultApproval == "" {
			delete(table, "default_tools_approval_mode")
		} else {
			table["default_tools_approval_mode"] = *access.DefaultApproval
		}
	}
	if access.ToolApprovals == nil {
		return
	}
	tools := map[string]any{}
	if rawTools, ok := table["tools"].(map[string]any); ok {
		tools = cloneAnyMap(rawTools)
	}
	for tool, rawTool := range tools {
		toolTable, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		copyOfTool := cloneAnyMap(toolTable)
		delete(copyOfTool, "approval_mode")
		if len(copyOfTool) == 0 {
			delete(tools, tool)
		} else {
			tools[tool] = copyOfTool
		}
	}
	for tool, approval := range *access.ToolApprovals {
		if approval == "" {
			continue
		}
		toolTable := map[string]any{}
		if rawTool, ok := tools[tool].(map[string]any); ok {
			toolTable = cloneAnyMap(rawTool)
		}
		toolTable["approval_mode"] = approval
		tools[tool] = toolTable
	}
	if len(tools) == 0 {
		delete(table, "tools")
	} else {
		table["tools"] = tools
	}
}

func applyStringSliceField(table map[string]any, key string, value *[]string) {
	if value == nil {
		return
	}
	if len(*value) == 0 {
		delete(table, key)
		return
	}
	table[key] = append([]string(nil), (*value)...)
}

func preflightAttachedPolicyRings(rings []registry.Ring, manifests []registry.Manifest, state map[string][]string, existing map[string]serverConfig) error {
	byName := make(map[string]registry.Ring, len(rings))
	for _, ring := range rings {
		byName[ring.Name] = ring
	}
	for _, name := range syncshared.AttachedRings(state) {
		ring, ok := byName[name]
		if !ok {
			return fmt.Errorf("attached ring %q is missing; restore its definition or detach it before sync so policy requirements cannot be bypassed", name)
		}
		if err := preflightPolicyRing(ring, manifests, state, existing); err != nil {
			return err
		}
	}
	return nil
}

func preflightPolicyRing(ring registry.Ring, manifests []registry.Manifest, state map[string][]string, existing map[string]serverConfig) error {
	if err := policy.ValidateRequiredRing(ring, manifests, Target, policy.SurfacePersistent).Err(); err != nil {
		return err
	}
	if !ring.RequiresPolicyEnforcement() {
		return nil
	}
	manifestByName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		manifestByName[manifest.Name] = manifest
	}
	for _, member := range ring.Members {
		member = strings.TrimSpace(member)
		server, exists := existing[member]
		if !exists {
			continue
		}
		if len(state[member]) == 0 {
			return fmt.Errorf("ring %q requires policy enforcement, but Codex entry %q is unmanaged and cannot satisfy ring ownership; remove or rename the unmanaged native entry, then retry sync or attach", ring.Name, member)
		}
		desired := CompileAccess(manifestByName[member].Access)
		paths := make([]string, 0, len(server.FidelityIssues))
		for _, issue := range server.FidelityIssues {
			if issue.Kind == fidelityDefaultApproval && desired.DefaultApproval != nil {
				continue
			}
			if issue.Kind == fidelityToolApproval && desired.ToolApprovals != nil {
				continue
			}
			paths = append(paths, issue.Path)
		}
		if len(paths) > 0 {
			sort.Strings(paths)
			return fmt.Errorf("ring %q requires policy enforcement, but existing managed Codex entry %q has behavior-affecting native fields Madari cannot prove equivalent: %s; remove or migrate those fields before retrying", ring.Name, member, strings.Join(paths, ", "))
		}
	}
	return nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneAnyMap(typed)
		case []any:
			out[key] = append([]any(nil), typed...)
		case []string:
			out[key] = append([]string(nil), typed...)
		default:
			out[key] = value
		}
	}
	return out
}

func sortedAnyMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deleteKeys(table map[string]any, keys ...string) {
	for _, key := range keys {
		delete(table, key)
	}
}

func setOrDeleteString(table map[string]any, key, value string) {
	if value == "" {
		delete(table, key)
	} else {
		table[key] = value
	}
}

func setOrDeleteStringSlice(table map[string]any, key string, values []string) {
	if len(values) == 0 {
		delete(table, key)
	} else {
		table[key] = append([]string(nil), values...)
	}
}

func setOrDeleteStringMap(table map[string]any, key string, values map[string]string) {
	if len(values) == 0 {
		delete(table, key)
		return
	}
	copyOfValues := make(map[string]string, len(values))
	for mapKey, value := range values {
		copyOfValues[mapKey] = value
	}
	table[key] = copyOfValues
}

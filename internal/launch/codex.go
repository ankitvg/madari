package launch

import (
	"fmt"
	"sort"
	"strings"

	codexclient "github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/registry"
)

func compileCodexOverrides(manifests []registry.Manifest, workingDirectory string, callerEnv map[string]string) ([]string, error) {
	servers := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		value, err := compileCodexServer(manifest, workingDirectory, callerEnv)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", manifest.Name, err)
		}
		servers = append(servers, fmt.Sprintf("%s = %s", tomlKey(manifest.Name), value))
	}
	return []string{"mcp_servers={ " + strings.Join(servers, ", ") + " }"}, nil
}

func compileCodexServer(manifest registry.Manifest, workingDirectory string, callerEnv map[string]string) (string, error) {
	if manifest.IsRemote() {
		if secretNames := manifest.SecretHeaderNames(); len(secretNames) > 0 {
			return "", fmt.Errorf("static secret header values cannot be passed to codex run: %s", strings.Join(secretNames, ", "))
		}
		fields := []string{fmt.Sprintf("url = %s", tomlString(manifest.URL)), "required = true"}
		if strings.TrimSpace(manifest.OAuthResource) != "" {
			fields = append(fields, fmt.Sprintf("oauth_resource = %s", tomlString(manifest.OAuthResource)))
		}
		if manifest.RequiresBearerTokenEnv() {
			fields = append(fields, fmt.Sprintf("bearer_token_env_var = %s", tomlString(manifest.BearerTokenEnvVar)))
		}
		if len(manifest.Headers) > 0 {
			fields = append(fields, fmt.Sprintf("http_headers = %s", tomlInlineStringMap(manifest.Headers)))
		}
		fields = append(fields, codexAccessFields(manifest.Access)...)
		return "{ " + strings.Join(fields, ", ") + " }", nil
	}

	if keys := intersectingKeys(manifest.SecretEnv.Keys, map[string]bool{"CODEX_HOME": true, "HOME": true, "USERPROFILE": true}); len(keys) > 0 {
		return "", fmt.Errorf("secret env %s cannot be forwarded by codex run because Codex isolates %s; move it to required_env or remove it from this server", strings.Join(keys, ", "), strings.Join(keys, ", "))
	}
	fields := []string{
		fmt.Sprintf("command = %s", tomlString(manifest.Command)),
		"required = true",
		fmt.Sprintf("cwd = %s", tomlString(workingDirectory)),
	}
	if len(manifest.Args) > 0 {
		fields = append(fields, fmt.Sprintf("args = %s", tomlStringArray(manifest.Args)))
	}
	if keys := codexRuntimeEnvVars(manifest, callerEnv); len(keys) > 0 {
		fields = append(fields, fmt.Sprintf("env_vars = %s", tomlStringArray(keys)))
	}
	if env := codexStaticEnv(manifest, callerEnv); len(env) > 0 {
		fields = append(fields, fmt.Sprintf("env = %s", tomlInlineStringMap(env)))
	}
	fields = append(fields, codexAccessFields(manifest.Access)...)
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func codexAccessFields(access *registry.AccessProfile) []string {
	compiled := codexclient.CompileAccess(access)
	fields := make([]string, 0, 5)
	if compiled.EnabledTools != nil && len(*compiled.EnabledTools) > 0 {
		fields = append(fields, fmt.Sprintf("enabled_tools = %s", tomlStringArray(*compiled.EnabledTools)))
	}
	if compiled.DisabledTools != nil && len(*compiled.DisabledTools) > 0 {
		fields = append(fields, fmt.Sprintf("disabled_tools = %s", tomlStringArray(*compiled.DisabledTools)))
	}
	if compiled.Scopes != nil && len(*compiled.Scopes) > 0 {
		fields = append(fields, fmt.Sprintf("scopes = %s", tomlStringArray(*compiled.Scopes)))
	}
	if compiled.DefaultApproval != nil && *compiled.DefaultApproval != "" {
		fields = append(fields, fmt.Sprintf("default_tools_approval_mode = %s", tomlString(*compiled.DefaultApproval)))
	}
	if compiled.ToolApprovals != nil {
		tools := make([]string, 0, len(*compiled.ToolApprovals))
		for _, tool := range sortedMapKeys(*compiled.ToolApprovals) {
			approval := (*compiled.ToolApprovals)[tool]
			if approval != "" {
				tools = append(tools, fmt.Sprintf("%s = { approval_mode = %s }", tomlKey(tool), tomlString(approval)))
			}
		}
		if len(tools) > 0 {
			fields = append(fields, "tools = { "+strings.Join(tools, ", ")+" }")
		}
	}
	return fields
}

func codexRuntimeEnvVars(manifest registry.Manifest, callerEnv map[string]string) []string {
	keys := runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
	secret := envKeySet(manifest.SecretEnv.Keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, suppliedStatically := callerEnv[key]; suppliedStatically && !secret[key] {
			continue
		}
		out = append(out, key)
	}
	return out
}

func codexStaticEnv(manifest registry.Manifest, callerEnv map[string]string) map[string]string {
	secret := envKeySet(manifest.SecretEnv.Keys)
	env := map[string]string{}
	for key, value := range manifest.Env {
		if !secret[key] {
			env[key] = value
		}
	}
	for key, value := range callerEnv {
		if _, exists := env[key]; !exists && !secret[key] {
			env[key] = value
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func runtimeEnvKeys(groups ...[]string) []string {
	seen := map[string]struct{}{}
	for _, values := range groups {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				seen[value] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func envKeySet(keys []string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[strings.TrimSpace(key)] = true
	}
	return out
}

func intersectingKeys(keys []string, allowed map[string]bool) []string {
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" && allowed[key] {
			seen[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func tomlKey(key string) string {
	if key == "" {
		return tomlString(key)
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return tomlString(key)
	}
	return key
}

func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, tomlString(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlInlineStringMap(values map[string]string) string {
	keys := sortedMapKeys(values)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s = %s", tomlKey(key), tomlString(values[key])))
	}
	return "{ " + strings.Join(pairs, ", ") + " }"
}

func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

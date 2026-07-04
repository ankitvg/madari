package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

// renderedServer is the self-contained client config entry ring render emits.
type renderedServer struct {
	Transport      string            `json:"type,omitempty"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutMS      int               `json:"timeout,omitempty"`
	OAuthResource  string            `json:"-"`
	Env            map[string]string `json:"env,omitempty"`
	RuntimeEnvKeys []string          `json:"-"`
}

type ringRenderTarget struct {
	target string
	// supportsRemote mirrors the sync adapter's per-transport remote
	// capability so render and sync never disagree about materialization.
	supportsRemote func(transport string) bool
	// emitsRemoteTimeout is true when the renderer carries timeout_ms into
	// the client's per-server timeout field; targets without an equivalent
	// (codex) warn instead.
	emitsRemoteTimeout bool
	render             func(io.Writer, map[string]renderedServer) error
}

var ringRenderTargets = ringRenderTargetsFromClientTargets()

func supportedRingRenderTargets() []string {
	targets := make([]string, 0, len(ringRenderTargets))
	for target := range ringRenderTargets {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

func renderMCPServersJSON(out io.Writer, servers map[string]renderedServer) error {
	return writeJSON(out, map[string]map[string]renderedServer{"mcpServers": servers})
}

// geminiRenderedServer mirrors gemini-cli's settings.json server shape:
// remote transports are distinguished by field (httpUrl = Streamable HTTP,
// url = SSE) rather than a type key.
type geminiRenderedServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	HTTPURL string            `json:"httpUrl,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func renderGeminiJSON(out io.Writer, servers map[string]renderedServer) error {
	shaped := make(map[string]geminiRenderedServer, len(servers))
	for name, entry := range servers {
		server := geminiRenderedServer{
			Command: entry.Command,
			Args:    entry.Args,
			Headers: entry.Headers,
			Timeout: entry.TimeoutMS,
			Env:     entry.Env,
		}
		switch entry.Transport {
		case registry.TransportHTTP:
			server.HTTPURL = entry.URL
		case registry.TransportSSE:
			server.URL = entry.URL
		}
		shaped[name] = server
	}
	return writeJSON(out, map[string]map[string]geminiRenderedServer{"mcpServers": shaped})
}

func renderCodexTOML(out io.Writer, servers map[string]renderedServer) error {
	names := sortedServerNames(servers)
	for i, name := range names {
		if i > 0 {
			fmt.Fprintln(out)
		}
		entry := servers[name]
		fmt.Fprintf(out, "[mcp_servers.%s]\n", tomlKey(name))
		if entry.URL != "" {
			fmt.Fprintf(out, "url = %s\n", tomlString(entry.URL))
			if entry.OAuthResource != "" {
				fmt.Fprintf(out, "oauth_resource = %s\n", tomlString(entry.OAuthResource))
			}
			if len(entry.Headers) > 0 {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "[mcp_servers.%s.http_headers]\n", tomlKey(name))
				for _, key := range sortedMapKeys(entry.Headers) {
					fmt.Fprintf(out, "%s = %s\n", tomlKey(key), tomlString(entry.Headers[key]))
				}
			}
		} else {
			fmt.Fprintf(out, "command = %s\n", tomlString(entry.Command))
			if len(entry.Args) > 0 {
				fmt.Fprintf(out, "args = %s\n", tomlStringArray(entry.Args))
			}
			if len(entry.RuntimeEnvKeys) > 0 {
				fmt.Fprintf(out, "env_vars = %s\n", tomlStringArray(entry.RuntimeEnvKeys))
			}
			if len(entry.Env) > 0 {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "[mcp_servers.%s.env]\n", tomlKey(name))
				for _, key := range sortedMapKeys(entry.Env) {
					fmt.Fprintf(out, "%s = %s\n", tomlKey(key), tomlString(entry.Env[key]))
				}
			}
		}
	}
	return nil
}

func renderVibeTOML(out io.Writer, servers map[string]renderedServer) error {
	names := sortedServerNames(servers)
	for i, name := range names {
		if i > 0 {
			fmt.Fprintln(out)
		}
		entry := servers[name]
		fmt.Fprintln(out, "[[mcp_servers]]")
		fmt.Fprintf(out, "name = %s\n", tomlString(name))
		fmt.Fprintln(out, `transport = "stdio"`)
		fmt.Fprintf(out, "command = %s\n", tomlString(entry.Command))
		if len(entry.Args) > 0 {
			fmt.Fprintf(out, "args = %s\n", tomlStringArray(entry.Args))
		}
		if len(entry.Env) > 0 {
			fmt.Fprintf(out, "env = %s\n", tomlInlineStringMap(entry.Env))
		}
	}
	return nil
}

func runtimeEnvKeys(keyGroups ...[]string) []string {
	seen := map[string]bool{}
	for _, keys := range keyGroups {
		for _, key := range keys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedServerNames(servers map[string]renderedServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
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
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

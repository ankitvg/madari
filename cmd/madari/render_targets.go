package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	claudedesktop "github.com/ankitvg/madari/internal/clients/claude-desktop"
	"github.com/ankitvg/madari/internal/clients/claudecode"
)

// renderedServer is the self-contained client config entry ring render emits.
type renderedServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type ringRenderTarget struct {
	target string
	render func(io.Writer, map[string]renderedServer) error
}

var ringRenderTargets = map[string]ringRenderTarget{
	claudedesktop.Target: {target: claudedesktop.Target, render: renderMCPServersJSON},
	claudecode.Target:    {target: claudecode.Target, render: renderMCPServersJSON},
	"codex":              {target: "codex", render: renderCodexTOML},
	"gemini":             {target: "gemini", render: renderMCPServersJSON},
	"vibe":               {target: "vibe", render: renderVibeTOML},
}

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

func renderCodexTOML(out io.Writer, servers map[string]renderedServer) error {
	names := sortedServerNames(servers)
	for i, name := range names {
		if i > 0 {
			fmt.Fprintln(out)
		}
		entry := servers[name]
		fmt.Fprintf(out, "[mcp_servers.%s]\n", tomlKey(name))
		fmt.Fprintf(out, "command = %s\n", tomlString(entry.Command))
		if len(entry.Args) > 0 {
			fmt.Fprintf(out, "args = %s\n", tomlStringArray(entry.Args))
		}
		if len(entry.Env) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintf(out, "[mcp_servers.%s.env]\n", tomlKey(name))
			for _, key := range sortedMapKeys(entry.Env) {
				fmt.Fprintf(out, "%s = %s\n", tomlKey(key), tomlString(entry.Env[key]))
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

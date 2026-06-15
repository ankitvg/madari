package main

import (
	"io"
	"sort"

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

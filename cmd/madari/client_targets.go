package main

import (
	"io"
	"sort"

	"github.com/ankitvg/madari/internal/clients"
	claudedesktop "github.com/ankitvg/madari/internal/clients/claude-desktop"
	"github.com/ankitvg/madari/internal/clients/claudecode"
	"github.com/ankitvg/madari/internal/clients/codex"
	"github.com/ankitvg/madari/internal/clients/gemini"
	"github.com/ankitvg/madari/internal/clients/vibe"
)

// clientTarget records the command-layer capabilities Madari knows for one
// AI client. Sync, render, and future skill support should extend this table
// instead of scattering target-specific switches.
type clientTarget struct {
	target             string
	syncAdapter        clients.ClientAdapter
	ringConfigRenderer func(io.Writer, map[string]renderedServer) error
	runExecutor        runExecutor
	runExecutable      string
	// ringRenderTimeout marks renderers that emit timeout_ms as the
	// client's per-server timeout field for remote entries.
	ringRenderTimeout bool
	// remoteOAuthResource marks clients that can carry OAuth resource metadata
	// for remote entries instead of dropping it from the materialized config.
	remoteOAuthResource bool
	// remoteBearerTokenEnv marks clients that can read remote bearer tokens
	// from an env var reference without Madari storing the token value.
	remoteBearerTokenEnv bool
	userScope            bool
	skillRoots           skillTargetRoots
}

var clientTargets = []clientTarget{
	{
		target:             claudedesktop.Target,
		syncAdapter:        claudedesktop.Adapter{},
		ringConfigRenderer: renderMCPServersJSON,
	},
	{
		target:             claudecode.Target,
		syncAdapter:        claudecode.Adapter{},
		ringConfigRenderer: renderMCPServersJSON,
		ringRenderTimeout:  true,
		userScope:          true,
		skillRoots: skillTargetRoots{
			project: defaultProjectSkillRoot(".claude", "skills"),
			user:    defaultHomeSkillRoot(".claude", "skills"),
		},
	},
	{
		target:               codex.Target,
		syncAdapter:          codex.Adapter{},
		ringConfigRenderer:   renderCodexTOML,
		runExecutor:          runCodex,
		runExecutable:        "codex",
		remoteOAuthResource:  true,
		remoteBearerTokenEnv: true,
		skillRoots: skillTargetRoots{
			project: defaultProjectSkillRoot(".agents", "skills"),
			user:    defaultHomeSkillRoot(".agents", "skills"),
		},
	},
	{
		target:             gemini.Target,
		syncAdapter:        gemini.Adapter{},
		ringConfigRenderer: renderGeminiJSON,
		ringRenderTimeout:  true,
		userScope:          true,
		skillRoots: skillTargetRoots{
			project: defaultProjectSkillRoot(".gemini", "skills"),
			user:    defaultHomeSkillRoot(".gemini", "skills"),
		},
	},
	{
		target:             vibe.Target,
		syncAdapter:        vibe.Adapter{},
		ringConfigRenderer: renderVibeTOML,
		skillRoots: skillTargetRoots{
			project: defaultProjectSkillRoot(".vibe", "skills"),
			user:    defaultVibeUserSkillRoot(),
		},
	},
}

func defaultInstallClientTarget() string {
	return claudedesktop.Target
}

func clientTargetByName(target string) (clientTarget, bool) {
	for _, ct := range clientTargets {
		if ct.target == target {
			return ct, true
		}
	}
	return clientTarget{}, false
}

func syncAdaptersFromClientTargets() map[string]clients.ClientAdapter {
	adapters := map[string]clients.ClientAdapter{}
	for _, ct := range clientTargets {
		if ct.syncAdapter != nil {
			adapters[ct.target] = ct.syncAdapter
		}
	}
	return adapters
}

func ringRenderTargetsFromClientTargets() map[string]ringRenderTarget {
	targets := map[string]ringRenderTarget{}
	for _, ct := range clientTargets {
		if ct.ringConfigRenderer != nil {
			targets[ct.target] = ringRenderTarget{
				target:             ct.target,
				emitsRemoteTimeout: ct.ringRenderTimeout,
				render:             ct.ringConfigRenderer,
			}
		}
	}
	return targets
}

func sortedClientTargetNames() []string {
	names := make([]string, 0, len(clientTargets))
	for _, ct := range clientTargets {
		names = append(names, ct.target)
	}
	sort.Strings(names)
	return names
}

func supportedSkillTargets() []string {
	targets := []string{}
	for _, ct := range clientTargets {
		if ct.skillRoots.supported() {
			targets = append(targets, ct.target)
		}
	}
	sort.Strings(targets)
	return targets
}

func supportsSkillMaterialization(target string) bool {
	ct, ok := clientTargetByName(target)
	return ok && ct.skillRoots.supported()
}

func supportsRemoteBearerTokenEnv(target string) bool {
	ct, ok := clientTargetByName(target)
	return ok && ct.remoteBearerTokenEnv
}

func supportsRemoteOAuthResource(target string) bool {
	ct, ok := clientTargetByName(target)
	return ok && ct.remoteOAuthResource
}

func runExecutorForTarget(target string) (runExecutor, bool) {
	ct, ok := clientTargetByName(target)
	return ct.runExecutor, ok && ct.runExecutor != nil
}

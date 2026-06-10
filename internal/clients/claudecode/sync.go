package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

const (
	Target = "claude-code"
)

var ErrConflict = clients.ErrConflict

// SyncOptions configures sync behavior.
type SyncOptions = clients.SyncOptions

// SyncResult captures the computed or applied mutation plan.
type SyncResult = clients.SyncResult

// Sync synchronizes enabled Claude Code-targeted manifests into the Claude Code config file.
//
// The default scope is project (repo-scoped .mcp.json). Manifests carrying a
// static env value for a secret_env key are refused at project scope — they
// are excluded from the desired set (and removed if previously managed,
// scrubbing the secret) and reported via SyncResult.Refused. User scope
// materializes them normally; scope is declared via opts.Scope, never
// inferred.
func Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	userScope, err := resolveScope(opts.Scope)
	if err != nil {
		return SyncResult{}, err
	}

	configPath, err := resolveConfigPath(opts.ConfigPath, userScope)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, existingServers, configExists, err := loadClaudeCodeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	desiredServers, refused := desiredServersForTarget(manifests, userScope)
	result, err := buildPlan(existingServers, managedState, desiredServers)
	if err != nil {
		return SyncResult{}, err
	}
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun
	result.Refused = refused

	if opts.DryRun {
		return result, nil
	}

	mutated := copyServers(existingServers)
	for _, name := range result.Removed {
		delete(mutated, name)
	}
	for name, server := range desiredServers {
		mutated[name] = server
	}

	updatedRoot := make(map[string]json.RawMessage, len(root)+1)
	for key, value := range root {
		updatedRoot[key] = value
	}
	serversPayload, err := json.Marshal(mutated)
	if err != nil {
		return SyncResult{}, fmt.Errorf("marshal mcpServers: %w", err)
	}
	updatedRoot["mcpServers"] = serversPayload

	payload, err := json.MarshalIndent(updatedRoot, "", "  ")
	if err != nil {
		return SyncResult{}, fmt.Errorf("marshal Claude Code config: %w", err)
	}
	payload = append(payload, '\n')

	if configExists {
		if _, err := syncshared.BackupFile(configPath); err != nil {
			return SyncResult{}, fmt.Errorf("backup Claude Code config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, 0o644); err != nil {
		return SyncResult{}, fmt.Errorf("write Claude Code config: %w", err)
	}

	nextState := syncshared.NextManagedState(managedState, syncshared.MapKeys(desiredServers), result.Added)
	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return SyncResult{}, fmt.Errorf("write managed sync state: %w", err)
	}

	return result, nil
}

func DefaultProjectConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}
	return filepath.Join(cwd, ".mcp.json"), nil
}

// DefaultUserConfigPath locates the user-scoped Claude Code config, where
// secret env values are allowed to materialize.
func DefaultUserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

func DefaultStatePath() (string, error) {
	root, err := registry.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", Target+"-managed.json"), nil
}

func resolveConfigPath(configPath string, userScope bool) (string, error) {
	if userScope {
		return syncshared.ResolvePath(configPath, DefaultUserConfigPath)
	}
	return syncshared.ResolvePath(configPath, DefaultProjectConfigPath)
}

// resolveScope reports whether opts.Scope selects the user-scoped config.
// Empty defaults to project (fail closed); unknown values are rejected.
func resolveScope(scope string) (bool, error) {
	switch strings.TrimSpace(scope) {
	case "", clients.ScopeProject:
		return false, nil
	case clients.ScopeUser:
		return true, nil
	default:
		return false, fmt.Errorf("unknown sync scope %q (supported: %s, %s)", scope, clients.ScopeProject, clients.ScopeUser)
	}
}

func resolveStatePath(statePath string) (string, error) {
	return syncshared.ResolvePath(statePath, DefaultStatePath)
}

func desiredServersForTarget(manifests []registry.Manifest, userScope bool) (map[string]serverConfig, []string) {
	servers := map[string]serverConfig{}
	var refused []string
	for _, manifest := range manifests {
		if !manifest.Enabled {
			continue
		}
		if !manifest.HasClient(Target) {
			continue
		}
		if !userScope && manifest.HasSecretValue() {
			refused = append(refused, manifest.Name)
			continue
		}

		entry := serverConfig{Command: manifest.Command}
		if len(manifest.Args) > 0 {
			entry.Args = append([]string(nil), manifest.Args...)
		}
		if len(manifest.Env) > 0 {
			entry.Env = map[string]string{}
			for key, value := range manifest.Env {
				entry.Env[key] = value
			}
		}
		servers[manifest.Name] = entry
	}
	sort.Strings(refused)
	return servers, refused
}

func buildPlan(existing map[string]serverConfig, managed map[string][]string, desired map[string]serverConfig) (SyncResult, error) {
	return syncshared.BuildPlan(existing, managed, desired, equalServer, ErrConflict)
}

func equalServer(a, b serverConfig) bool {
	if a.Command != b.Command {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for key, value := range a.Env {
		if b.Env[key] != value {
			return false
		}
	}
	return true
}

func loadClaudeCodeConfig(path string) (map[string]json.RawMessage, map[string]serverConfig, bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, map[string]serverConfig{}, false, nil
		}
		return nil, nil, false, fmt.Errorf("read Claude Code config %q: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, true, fmt.Errorf("parse Claude Code config JSON: %w", err)
	}

	servers := map[string]serverConfig{}
	if raw, exists := root["mcpServers"]; exists {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, true, fmt.Errorf("parse mcpServers: %w", err)
		}
	}
	if servers == nil {
		servers = map[string]serverConfig{}
	}

	return root, servers, true, nil
}

func copyServers(in map[string]serverConfig) map[string]serverConfig {
	out := make(map[string]serverConfig, len(in))
	for name, server := range in {
		clone := serverConfig{Command: server.Command}
		if len(server.Args) > 0 {
			clone.Args = append([]string(nil), server.Args...)
		}
		if len(server.Env) > 0 {
			clone.Env = map[string]string{}
			for key, value := range server.Env {
				clone.Env[key] = value
			}
		}
		out[name] = clone
	}
	return out
}

type serverConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

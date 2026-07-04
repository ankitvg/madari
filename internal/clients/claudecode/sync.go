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
	statePath, err := resolveStatePath(opts.StatePath, userScope)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeCodeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	entries := entriesForTarget(manifests, userScope)
	if err := rejectRawMismatchedUnmanagedEntries(rawServers, existingServers, managedState, entries); err != nil {
		return SyncResult{}, err
	}
	result, nextState, writeSet, err := syncshared.PlanSync(existingServers, managedState, entries, opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun || (!configExists && syncshared.PlanIsNoOp(result, managedState, nextState)) {
		return result, nil
	}

	if err := applyPlan(configPath, statePath, userScope, root, rawServers, configExists, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// AttachRing adds the ring's ownership source to every member and
// materializes the eligible ones into the Claude Code config. Attaching onto
// any pre-existing unmanaged entry — equal values included — refuses with
// ErrConflict.
func AttachRing(ring registry.Ring, manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	userScope, err := resolveScope(opts.Scope)
	if err != nil {
		return SyncResult{}, err
	}
	configPath, err := resolveConfigPath(opts.ConfigPath, userScope)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath, userScope)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeCodeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	result, nextState, writeSet, err := syncshared.PlanAttach(existingServers, managedState, ring.Name, ring.Members, entriesForTarget(manifests, userScope), opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun {
		return result, nil
	}
	if err := applyPlan(configPath, statePath, userScope, root, rawServers, configExists, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// DetachRing removes the ring's ownership source everywhere; entries that
// lose their last source leave the config. The ring file is not required, so
// stale sources can always be released.
func DetachRing(ringName string, opts SyncOptions) (SyncResult, error) {
	userScope, err := resolveScope(opts.Scope)
	if err != nil {
		return SyncResult{}, err
	}
	configPath, err := resolveConfigPath(opts.ConfigPath, userScope)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath, userScope)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeCodeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	result, nextState := syncshared.PlanDetach(existingServers, managedState, ringName, opts.Rings)
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun {
		return result, nil
	}
	if err := applyPlan(configPath, statePath, userScope, root, rawServers, configExists, result.Removed, nil, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// applyPlan writes the mutated config (backup + atomic write) and ownership
// state. The raw entries pass through untouched except for removals and the
// write set; pre-existing unmanaged entries are never serialized.
func applyPlan(
	configPath, statePath string,
	userScope bool,
	root, rawServers map[string]json.RawMessage,
	configExists bool,
	removed []string,
	writeSet map[string]serverConfig,
	nextState map[string][]string,
) error {
	mutated := make(map[string]json.RawMessage, len(rawServers)+len(writeSet))
	for name, raw := range rawServers {
		mutated[name] = raw
	}
	for _, name := range removed {
		delete(mutated, name)
	}
	for name, server := range writeSet {
		entryPayload, err := json.Marshal(server)
		if err != nil {
			return fmt.Errorf("marshal server %q: %w", name, err)
		}
		mutated[name] = entryPayload
	}

	updatedRoot := make(map[string]json.RawMessage, len(root)+1)
	for key, value := range root {
		updatedRoot[key] = value
	}
	serversPayload, err := json.Marshal(mutated)
	if err != nil {
		return fmt.Errorf("marshal mcpServers: %w", err)
	}
	updatedRoot["mcpServers"] = serversPayload

	payload, err := json.MarshalIndent(updatedRoot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Claude Code config: %w", err)
	}
	payload = append(payload, '\n')

	configMode := os.FileMode(0o644)
	if userScope {
		configMode = 0o600
	}
	if configExists {
		var err error
		if userScope {
			_, err = syncshared.BackupFileWithMode(configPath, configMode)
		} else {
			_, err = syncshared.BackupFile(configPath)
		}
		if err != nil {
			return fmt.Errorf("backup Claude Code config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, configMode); err != nil {
		return fmt.Errorf("write Claude Code config: %w", err)
	}

	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return fmt.Errorf("write managed sync state: %w", err)
	}
	return nil
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

// DefaultUserStatePath locates managed state for the user-scoped config.
// Project and user scopes must never share a state file: ownership recorded
// for one config would drive removals against the other.
func DefaultUserStatePath() (string, error) {
	root, err := registry.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", Target+"-user-managed.json"), nil
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

func resolveStatePath(statePath string, userScope bool) (string, error) {
	if userScope {
		return syncshared.ResolvePath(statePath, DefaultUserStatePath)
	}
	return syncshared.ResolvePath(statePath, DefaultStatePath)
}

// entriesForTarget classifies every Claude Code-targeted manifest for the
// plan engine. Disabled manifests are ineligible (ownership release);
// secret-bearing manifests at project scope are refused (scrubbed but ring
// ownership persists); manifests for other clients are omitted entirely.
func entriesForTarget(manifests []registry.Manifest, userScope bool) map[string]syncshared.Entry[serverConfig] {
	entries := map[string]syncshared.Entry[serverConfig]{}
	for _, manifest := range manifests {
		if !manifest.HasClient(Target) {
			continue
		}
		entry := syncshared.Entry[serverConfig]{}
		switch {
		case !manifest.Enabled:
			// ineligible
		case manifest.IsRemote() && !supportsRemoteTransport(manifest.TransportType()):
			// unsupported remote transports stay pending
		case manifest.IsRemote() && manifest.RequiresBearerTokenEnv():
			// bearer_token_env_var is not part of Claude Code .mcp.json.
			// Keep the remote pending instead of emitting an unauthenticated URL.
		case !userScope && manifest.HasSecretValue():
			// covers static secret env values and secret header values;
			// refusal at project scope keeps credentials out of .mcp.json
			entry.Refused = true
		default:
			entry.Eligible = true
			entry.Value = materializeServer(manifest)
		}
		entries[manifest.Name] = entry
	}
	return entries
}

// supportsRemoteTransport gates which remote transports Claude Code
// materializes: both Streamable HTTP and SSE are documented .mcp.json
// server types (SSE is deprecated upstream but still supported).
func supportsRemoteTransport(transport string) bool {
	switch transport {
	case registry.TransportHTTP, registry.TransportSSE:
		return true
	default:
		return false
	}
}

func materializeServer(manifest registry.Manifest) serverConfig {
	if manifest.IsRemote() {
		// Remote entries carry type/url/headers plus the per-server tool
		// timeout in milliseconds. oauth_resource has no .mcp.json
		// equivalent and is not emitted.
		entry := serverConfig{
			Type:    manifest.TransportType(),
			URL:     manifest.URL,
			Timeout: manifest.TimeoutMS,
		}
		if len(manifest.Headers) > 0 {
			entry.Headers = make(map[string]string, len(manifest.Headers))
			for key, value := range manifest.Headers {
				entry.Headers[key] = value
			}
		}
		return entry
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
	return entry
}

// normalizeType folds Claude Code's documented streamable-http alias into
// http so hand-written entries copied from server docs compare as equal to
// madari's materialization instead of raising unmanaged conflicts.
func normalizeType(transport string) string {
	if transport == "streamable-http" {
		return "http"
	}
	return transport
}

func equalServer(a, b serverConfig) bool {
	if a.Command != b.Command {
		return false
	}
	if normalizeType(a.Type) != normalizeType(b.Type) || a.URL != b.URL {
		return false
	}
	if a.Timeout != b.Timeout {
		return false
	}
	if len(a.Headers) != len(b.Headers) {
		return false
	}
	for key, value := range a.Headers {
		if b.Headers[key] != value {
			return false
		}
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

func rejectRawMismatchedUnmanagedEntries(
	rawServers map[string]json.RawMessage,
	existingServers map[string]serverConfig,
	managedState map[string][]string,
	entries map[string]syncshared.Entry[serverConfig],
) error {
	var conflicts []string
	for name, entry := range entries {
		if !entry.Eligible || len(managedState[name]) > 0 {
			continue
		}
		raw, exists := rawServers[name]
		if !exists {
			continue
		}
		existing, exists := existingServers[name]
		if !exists || !equalServer(existing, entry.Value) {
			continue
		}
		if rawMatchesServer(raw, entry.Value) {
			continue
		}
		conflicts = append(conflicts, name)
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("%w: unmanaged entries already exist with different raw JSON: %s", ErrConflict, strings.Join(conflicts, ", "))
	}
	return nil
}

func rawMatchesServer(raw json.RawMessage, server serverConfig) bool {
	desired, err := json.Marshal(server)
	if err != nil {
		return false
	}
	rawPayload, ok := canonicalServerJSON(raw)
	if !ok {
		return false
	}
	desiredPayload, ok := canonicalServerJSON(desired)
	if !ok {
		return false
	}
	return string(rawPayload) == string(desiredPayload)
}

func canonicalServerJSON(payload []byte) ([]byte, bool) {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, false
	}
	if !stripEmptyModeledServerFields(object) {
		return nil, false
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, false
	}
	return canonical, true
}

func stripEmptyModeledServerFields(object map[string]any) bool {
	if value, exists := object["args"]; exists {
		args, ok := value.([]any)
		if !ok {
			return false
		}
		if len(args) == 0 {
			delete(object, "args")
		}
	}
	if value, exists := object["env"]; exists {
		env, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if len(env) == 0 {
			delete(object, "env")
		}
	}
	if value, exists := object["headers"]; exists {
		headers, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if len(headers) == 0 {
			delete(object, "headers")
		}
	}
	if value, exists := object["type"]; exists {
		transport, ok := value.(string)
		if !ok {
			return false
		}
		object["type"] = normalizeType(transport)
	}
	return true
}

// loadClaudeCodeConfig returns the config root plus two views of mcpServers:
// the raw JSON per entry (preserved verbatim for entries madari does not
// write) and the typed view used for planning.
func loadClaudeCodeConfig(path string) (map[string]json.RawMessage, map[string]json.RawMessage, map[string]serverConfig, bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, map[string]json.RawMessage{}, map[string]serverConfig{}, false, nil
		}
		return nil, nil, nil, false, fmt.Errorf("read Claude Code config %q: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, nil, true, fmt.Errorf("parse Claude Code config JSON: %w", err)
	}

	rawServers := map[string]json.RawMessage{}
	if raw, exists := root["mcpServers"]; exists {
		if err := json.Unmarshal(raw, &rawServers); err != nil {
			return nil, nil, nil, true, fmt.Errorf("parse mcpServers: %w", err)
		}
	}
	if rawServers == nil {
		rawServers = map[string]json.RawMessage{}
	}

	servers := make(map[string]serverConfig, len(rawServers))
	for name, raw := range rawServers {
		entry := serverConfig{}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, nil, nil, true, fmt.Errorf("parse mcpServers entry %q: %w", name, err)
		}
		servers[name] = entry
	}

	return root, rawServers, servers, true, nil
}

type serverConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

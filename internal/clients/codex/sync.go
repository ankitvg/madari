package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
	"github.com/pelletier/go-toml/v2"
)

const (
	Target            = "codex"
	defaultConfigMode = 0o600
)

var ErrConflict = clients.ErrConflict

// SyncOptions configures sync behavior.
type SyncOptions = clients.SyncOptions

// SyncResult captures the computed or applied mutation plan.
type SyncResult = clients.SyncResult

// Sync synchronizes enabled Codex-targeted manifests into the Codex user
// config file. Codex's native `codex mcp add` command writes global MCP
// servers to $CODEX_HOME/config.toml, defaulting to ~/.codex/config.toml.
func Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	if err := validateInputManifests(manifests); err != nil {
		return SyncResult{}, err
	}
	if err := validateScope(opts.Scope); err != nil {
		return SyncResult{}, err
	}
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, configMode, err := loadCodexConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}
	if err := preflightAttachedPolicyRings(opts.Rings, manifests, managedState, existingServers); err != nil {
		return SyncResult{}, err
	}

	result, nextState, writeSet, err := syncshared.PlanSync(existingServers, managedState, entriesForTarget(manifests), opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
	result.PolicyUpdated = policyUpdatedNames(result.Updated, existingServers, writeSet)
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun || (!configExists && syncshared.PlanIsNoOp(result, managedState, nextState)) {
		return result, nil
	}

	if err := applyPlan(configPath, statePath, root, rawServers, configExists, configMode, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// AttachRing adds the ring's ownership source to every member and materializes
// eligible ones into Codex config. Attaching onto any pre-existing unmanaged
// entry, equal values included, refuses with ErrConflict.
func AttachRing(ring registry.Ring, manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	if err := validateInputManifests(manifests); err != nil {
		return SyncResult{}, err
	}
	if err := validateScope(opts.Scope); err != nil {
		return SyncResult{}, err
	}
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, configMode, err := loadCodexConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}
	if err := preflightPolicyRing(ring, manifests, managedState, existingServers); err != nil {
		return SyncResult{}, err
	}

	result, nextState, writeSet, err := syncshared.PlanAttach(existingServers, managedState, ring.Name, ring.Members, entriesForTarget(manifests), opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
	result.PolicyUpdated = policyUpdatedNames(result.Updated, existingServers, writeSet)
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun {
		return result, nil
	}
	if err := applyPlan(configPath, statePath, root, rawServers, configExists, configMode, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// DetachRing removes the ring's ownership source everywhere; entries that lose
// their last source leave the config. The ring file is not required, so stale
// sources can always be released.
func DetachRing(ringName string, opts SyncOptions) (SyncResult, error) {
	if err := validateScope(opts.Scope); err != nil {
		return SyncResult{}, err
	}
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, configMode, err := loadCodexConfig(configPath)
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
	if err := applyPlan(configPath, statePath, root, rawServers, configExists, configMode, result.Removed, nil, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

func applyPlan(
	configPath, statePath string,
	root, rawServers map[string]any,
	configExists bool,
	configMode os.FileMode,
	removed []string,
	writeSet map[string]serverConfig,
	nextState map[string][]string,
) error {
	mutated := make(map[string]any, len(rawServers)+len(writeSet))
	for name, raw := range rawServers {
		mutated[name] = raw
	}
	for _, name := range removed {
		delete(mutated, name)
	}
	for name, server := range writeSet {
		merged, err := mergeServerTable(rawServers[name], server)
		if err != nil {
			return fmt.Errorf("merge Codex server %q: %w", name, err)
		}
		mutated[name] = merged
	}

	updatedRoot := make(map[string]any, len(root)+1)
	for key, value := range root {
		updatedRoot[key] = value
	}
	updatedRoot["mcp_servers"] = mutated

	payload, err := toml.Marshal(updatedRoot)
	if err != nil {
		return fmt.Errorf("marshal Codex config: %w", err)
	}

	if configExists {
		if _, err := syncshared.BackupFile(configPath); err != nil {
			return fmt.Errorf("backup Codex config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, configMode); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}

	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return fmt.Errorf("write managed sync state: %w", err)
	}
	return nil
}

func DefaultConfigPath() (string, error) {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		resolved, err := syncshared.ExpandHome(codexHome)
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Clean(resolved), "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func DefaultStatePath() (string, error) {
	root, err := registry.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", Target+"-managed.json"), nil
}

func resolveConfigPath(configPath string) (string, error) {
	return syncshared.ResolvePath(configPath, DefaultConfigPath)
}

func resolveStatePath(statePath string) (string, error) {
	return syncshared.ResolvePath(statePath, DefaultStatePath)
}

func validateScope(scope string) error {
	switch strings.TrimSpace(scope) {
	case "", clients.ScopeUser:
		return nil
	default:
		return fmt.Errorf("%s sync supports the user-scoped config only", Target)
	}
}

func validateInputManifests(manifests []registry.Manifest) error {
	for _, manifest := range manifests {
		if !manifest.HasClient(Target) {
			continue
		}
		if err := manifest.Validate(); err != nil {
			return fmt.Errorf("validate manifest %q before Codex policy compilation: %w", manifest.Name, err)
		}
	}
	return nil
}

func entriesForTarget(manifests []registry.Manifest) map[string]syncshared.Entry[serverConfig] {
	entries := map[string]syncshared.Entry[serverConfig]{}
	for _, manifest := range manifests {
		if !manifest.HasClient(Target) {
			continue
		}
		entry := syncshared.Entry[serverConfig]{}
		if manifest.Enabled && (!manifest.IsRemote() || supportsRemoteTransport(manifest.TransportType())) {
			entry.Eligible = true
			entry.Value = materializeServer(manifest)
		}
		entries[manifest.Name] = entry
	}
	return entries
}

// supportsRemoteTransport gates which remote transports Codex materializes:
// Streamable HTTP is Codex's documented remote config support; SSE stays
// pending.
func supportsRemoteTransport(transport string) bool {
	return transport == registry.TransportHTTP
}

func materializeServer(manifest registry.Manifest) serverConfig {
	if manifest.IsRemote() {
		// Codex remote entries carry url, optional OAuth metadata,
		// bearer-token env references, and static headers as http_headers.
		// timeout_ms has no Codex
		// equivalent and is deliberately not emitted (documented in the
		// manifest spec).
		entry := serverConfig{
			URL:               manifest.URL,
			OAuthResource:     manifest.OAuthResource,
			BearerTokenEnvVar: manifest.BearerTokenEnvVar,
			Access:            CompileAccess(manifest.Access),
		}
		if len(manifest.Headers) > 0 {
			entry.HTTPHeaders = make(map[string]string, len(manifest.Headers))
			for key, value := range manifest.Headers {
				entry.HTTPHeaders[key] = value
			}
		}
		return entry
	}

	entry := serverConfig{
		Command: manifest.Command,
		Access:  CompileAccess(manifest.Access),
		EnvVars: runtimeEnvKeys(
			manifest.RequiredEnv.Keys,
			manifest.SecretEnv.Keys,
		),
	}
	if len(manifest.Args) > 0 {
		entry.Args = append([]string(nil), manifest.Args...)
	}

	secret := map[string]bool{}
	for _, key := range manifest.SecretEnv.Keys {
		secret[strings.TrimSpace(key)] = true
	}
	for key, value := range manifest.Env {
		if secret[key] {
			continue
		}
		if entry.Env == nil {
			entry.Env = map[string]string{}
		}
		entry.Env[key] = value
	}
	return entry
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

func equalServer(a, b serverConfig) bool {
	if a.Command != b.Command {
		return false
	}
	if a.URL != b.URL {
		return false
	}
	if a.OAuthResource != b.OAuthResource {
		return false
	}
	if a.BearerTokenEnvVar != b.BearerTokenEnvVar {
		return false
	}
	if len(a.HTTPHeaders) != len(b.HTTPHeaders) {
		return false
	}
	for key, value := range a.HTTPHeaders {
		if b.HTTPHeaders[key] != value {
			return false
		}
	}
	if !equalStringSlices(a.Args, b.Args) {
		return false
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for key, value := range a.Env {
		if b.Env[key] != value {
			return false
		}
	}
	if !equalStringSlices(a.EnvVars, b.EnvVars) {
		return false
	}
	return equalDeclaredAccess(a.Access, b.Access)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func loadCodexConfig(path string) (map[string]any, map[string]any, map[string]serverConfig, bool, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, map[string]any{}, map[string]serverConfig{}, false, defaultConfigMode, nil
		}
		return nil, nil, nil, false, 0, fmt.Errorf("stat Codex config %q: %w", path, err)
	}
	configMode := info.Mode().Perm()
	if configMode == 0 {
		configMode = defaultConfigMode
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, false, 0, fmt.Errorf("read Codex config %q: %w", path, err)
	}

	root := map[string]any{}
	if err := toml.Unmarshal(payload, &root); err != nil {
		return nil, nil, nil, true, configMode, fmt.Errorf("parse Codex config TOML: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}

	rawServers := map[string]any{}
	if raw, exists := root["mcp_servers"]; exists {
		servers, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, true, configMode, fmt.Errorf("parse mcp_servers: expected table")
		}
		for name, server := range servers {
			rawServers[name] = server
		}
	}

	servers := make(map[string]serverConfig, len(rawServers))
	for name, raw := range rawServers {
		server, err := parseServer(name, raw)
		if err != nil {
			return nil, nil, nil, true, configMode, err
		}
		servers[name] = server
	}

	return root, rawServers, servers, true, configMode, nil
}

func parseServer(name string, raw any) (serverConfig, error) {
	table, ok := raw.(map[string]any)
	if !ok {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s: expected table", name)
	}

	entry := serverConfig{}
	if rawCommand, exists := table["command"]; exists {
		command, ok := rawCommand.(string)
		if !ok {
			return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.command: expected string", name)
		}
		entry.Command = command
	}
	if rawURL, exists := table["url"]; exists {
		url, ok := rawURL.(string)
		if !ok {
			return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.url: expected string", name)
		}
		entry.URL = url
	}
	if rawOAuthResource, exists := table["oauth_resource"]; exists {
		oauthResource, ok := rawOAuthResource.(string)
		if !ok {
			return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.oauth_resource: expected string", name)
		}
		entry.OAuthResource = oauthResource
	}
	if rawBearerTokenEnvVar, exists := table["bearer_token_env_var"]; exists {
		bearerTokenEnvVar, ok := rawBearerTokenEnvVar.(string)
		if !ok {
			return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.bearer_token_env_var: expected string", name)
		}
		entry.BearerTokenEnvVar = bearerTokenEnvVar
	}
	if headers, ok, err := optionalStringMap(table, "http_headers"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.http_headers: %w", name, err)
	} else if ok {
		entry.HTTPHeaders = headers
	}
	if enabled, ok, err := optionalBool(table, "enabled"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.enabled: %w", name, err)
	} else if ok {
		entry.Enabled = &enabled
	}
	if args, ok, err := optionalStringSlice(table, "args"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.args: %w", name, err)
	} else if ok {
		entry.Args = args
	}
	if env, ok, err := optionalStringMap(table, "env"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.env: %w", name, err)
	} else if ok {
		entry.Env = env
	}
	if envVars, ok, err := optionalStringSlice(table, "env_vars"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers.%s.env_vars: %w", name, err)
	} else if ok {
		entry.EnvVars = envVars
	}
	access, fidelityIssues, err := parseNativeAccess(name, table)
	if err != nil {
		return serverConfig{}, err
	}
	entry.Access = access
	entry.FidelityIssues = fidelityIssues
	return entry, nil
}

func optionalBool(table map[string]any, key string) (bool, bool, error) {
	raw, exists := table[key]
	if !exists {
		return false, false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, true, fmt.Errorf("expected bool")
	}
	return value, true, nil
}

func optionalStringSlice(table map[string]any, key string) ([]string, bool, error) {
	raw, exists := table[key]
	if !exists {
		return nil, false, nil
	}
	values, ok := raw.([]any)
	if !ok {
		if strings, ok := raw.([]string); ok {
			return append([]string(nil), strings...), true, nil
		}
		return nil, true, fmt.Errorf("expected array of strings")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		str, ok := value.(string)
		if !ok {
			return nil, true, fmt.Errorf("expected array of strings")
		}
		out = append(out, str)
	}
	return out, true, nil
}

func optionalStringMap(table map[string]any, key string) (map[string]string, bool, error) {
	raw, exists := table[key]
	if !exists {
		return nil, false, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		if strings, ok := raw.(map[string]string); ok {
			out := make(map[string]string, len(strings))
			for key, value := range strings {
				out[key] = value
			}
			return out, true, nil
		}
		return nil, true, fmt.Errorf("expected table of strings")
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		str, ok := value.(string)
		if !ok {
			return nil, true, fmt.Errorf("expected table of strings")
		}
		out[key] = str
	}
	return out, true, nil
}

type serverConfig struct {
	Command           string                `toml:"command,omitempty"`
	URL               string                `toml:"url,omitempty"`
	OAuthResource     string                `toml:"oauth_resource,omitempty"`
	BearerTokenEnvVar string                `toml:"bearer_token_env_var,omitempty"`
	Enabled           *bool                 `toml:"enabled,omitempty"`
	Args              []string              `toml:"args,omitempty"`
	EnvVars           []string              `toml:"env_vars,omitempty"`
	Env               map[string]string     `toml:"env,omitempty"`
	HTTPHeaders       map[string]string     `toml:"http_headers,omitempty"`
	Access            CompiledAccess        `toml:"-"`
	FidelityIssues    []nativeFidelityIssue `toml:"-"`
}

package vibe

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
	Target            = "vibe"
	defaultConfigMode = 0o600
)

var ErrConflict = clients.ErrConflict

// SyncOptions configures sync behavior.
type SyncOptions = clients.SyncOptions

// SyncResult captures the computed or applied mutation plan.
type SyncResult = clients.SyncResult

// Sync synchronizes enabled Vibe-targeted manifests into the Vibe user config.
// Vibe stores persistent MCP servers in $VIBE_HOME/config.toml, defaulting to
// ~/.vibe/config.toml, under [[mcp_servers]] array entries.
func Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
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

	root, rawServers, existingServers, configExists, configMode, err := loadVibeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	result, nextState, writeSet, err := syncshared.PlanSync(existingServers, managedState, entriesForTarget(manifests), opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
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

// AttachRing adds the ring's ownership source to every member and materializes
// eligible ones into Vibe config. Attaching onto any pre-existing unmanaged
// entry, equal values included, refuses with ErrConflict.
func AttachRing(ring registry.Ring, manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
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

	root, rawServers, existingServers, configExists, configMode, err := loadVibeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedState, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	result, nextState, writeSet, err := syncshared.PlanAttach(existingServers, managedState, ring.Name, ring.Members, entriesForTarget(manifests), opts.Rings, equalServer, ErrConflict)
	if err != nil {
		return SyncResult{}, err
	}
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

	root, rawServers, existingServers, configExists, configMode, err := loadVibeConfig(configPath)
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
	root map[string]any,
	rawServers []rawServerEntry,
	configExists bool,
	configMode os.FileMode,
	removed []string,
	writeSet map[string]serverConfig,
	nextState map[string][]string,
) error {
	removedSet := map[string]bool{}
	for _, name := range removed {
		removedSet[name] = true
	}

	mutated := make([]any, 0, len(rawServers)+len(writeSet))
	written := map[string]bool{}
	for _, entry := range rawServers {
		if removedSet[entry.Name] {
			continue
		}
		if server, exists := writeSet[entry.Name]; exists {
			mutated = append(mutated, server)
			written[entry.Name] = true
			continue
		}
		mutated = append(mutated, entry.Raw)
	}

	pending := make([]string, 0, len(writeSet))
	for name := range writeSet {
		if !written[name] && !removedSet[name] {
			pending = append(pending, name)
		}
	}
	sort.Strings(pending)
	for _, name := range pending {
		mutated = append(mutated, writeSet[name])
	}

	updatedRoot := make(map[string]any, len(root)+1)
	for key, value := range root {
		updatedRoot[key] = value
	}
	updatedRoot["mcp_servers"] = mutated

	payload, err := toml.Marshal(updatedRoot)
	if err != nil {
		return fmt.Errorf("marshal Vibe config: %w", err)
	}

	if configExists {
		if _, err := syncshared.BackupFile(configPath); err != nil {
			return fmt.Errorf("backup Vibe config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, configMode); err != nil {
		return fmt.Errorf("write Vibe config: %w", err)
	}

	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return fmt.Errorf("write managed sync state: %w", err)
	}
	return nil
}

func DefaultConfigPath() (string, error) {
	if vibeHome := strings.TrimSpace(os.Getenv("VIBE_HOME")); vibeHome != "" {
		resolved, err := syncshared.ExpandHome(vibeHome)
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Clean(resolved), "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".vibe", "config.toml"), nil
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

func entriesForTarget(manifests []registry.Manifest) map[string]syncshared.Entry[serverConfig] {
	entries := map[string]syncshared.Entry[serverConfig]{}
	for _, manifest := range manifests {
		if !manifest.HasClient(Target) {
			continue
		}
		entry := syncshared.Entry[serverConfig]{}
		if manifest.Enabled && !manifest.IsRemote() {
			entry.Eligible = true
			entry.Value = materializeServer(manifest)
		}
		entries[manifest.Name] = entry
	}
	return entries
}

func materializeServer(manifest registry.Manifest) serverConfig {
	entry := serverConfig{
		Name:      manifest.Name,
		Transport: "stdio",
		Command:   manifest.Command,
	}
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

func equalServer(a, b serverConfig) bool {
	if a.Name != b.Name || a.Transport != b.Transport || a.Command != b.Command {
		return false
	}
	if !equalStringSlices(a.CommandList, b.CommandList) {
		return false
	}
	if a.HasExtra || b.HasExtra {
		return false
	}
	if effectiveDisabled(a) != effectiveDisabled(b) {
		return false
	}
	if !equalStringSlices(a.DisabledTools, b.DisabledTools) {
		return false
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
	return true
}

func effectiveDisabled(server serverConfig) bool {
	return server.Disabled != nil && *server.Disabled
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

func loadVibeConfig(path string) (map[string]any, []rawServerEntry, map[string]serverConfig, bool, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, []rawServerEntry{}, map[string]serverConfig{}, false, defaultConfigMode, nil
		}
		return nil, nil, nil, false, 0, fmt.Errorf("stat Vibe config %q: %w", path, err)
	}
	configMode := info.Mode().Perm()
	if configMode == 0 {
		configMode = defaultConfigMode
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, false, 0, fmt.Errorf("read Vibe config %q: %w", path, err)
	}

	root := map[string]any{}
	if err := toml.Unmarshal(payload, &root); err != nil {
		return nil, nil, nil, true, configMode, fmt.Errorf("parse Vibe config TOML: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}

	rawServers := []rawServerEntry{}
	if raw, exists := root["mcp_servers"]; exists {
		servers, ok := raw.([]any)
		if !ok {
			return nil, nil, nil, true, configMode, fmt.Errorf("parse mcp_servers: expected array")
		}
		for i, server := range servers {
			parsed, err := parseServer(i, server)
			if err != nil {
				return nil, nil, nil, true, configMode, err
			}
			rawServers = append(rawServers, rawServerEntry{
				Name: parsed.Name,
				Raw:  server,
			})
		}
	}

	servers := make(map[string]serverConfig, len(rawServers))
	for i, entry := range rawServers {
		if _, exists := servers[entry.Name]; exists {
			return nil, nil, nil, true, configMode, fmt.Errorf("parse mcp_servers[%d].name: duplicate server name %q", i, entry.Name)
		}
		server, err := parseServer(i, entry.Raw)
		if err != nil {
			return nil, nil, nil, true, configMode, err
		}
		servers[entry.Name] = server
	}

	return root, rawServers, servers, true, configMode, nil
}

func parseServer(index int, raw any) (serverConfig, error) {
	table, ok := raw.(map[string]any)
	if !ok {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d]: expected table", index)
	}

	entry := serverConfig{}
	name, ok, err := requiredString(table, "name")
	if err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].name: %w", index, err)
	}
	if !ok || strings.TrimSpace(name) == "" {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].name: expected non-empty string", index)
	}
	entry.Name = name

	transport, ok, err := requiredString(table, "transport")
	if err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].transport: %w", index, err)
	}
	if !ok || strings.TrimSpace(transport) == "" {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].transport: expected non-empty string", index)
	}
	entry.Transport = transport

	if disabled, ok, err := optionalBool(table, "disabled"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].disabled: %w", index, err)
	} else if ok {
		entry.Disabled = &disabled
	}
	if disabledTools, ok, err := optionalStringSlice(table, "disabled_tools"); err != nil {
		return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].disabled_tools: %w", index, err)
	} else if ok {
		entry.DisabledTools = disabledTools
	}

	if entry.Transport == "stdio" {
		if command, commandList, ok, err := requiredCommand(table); err != nil {
			return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].command: %w", index, err)
		} else if !ok {
			return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].command: expected string or array of strings", index)
		} else {
			entry.Command = command
			entry.CommandList = commandList
		}
		if args, ok, err := optionalStringSlice(table, "args"); err != nil {
			return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].args: %w", index, err)
		} else if ok {
			entry.Args = args
		}
		if env, ok, err := optionalStringMap(table, "env"); err != nil {
			return serverConfig{}, fmt.Errorf("parse mcp_servers[%d].env: %w", index, err)
		} else if ok {
			entry.Env = env
		}
	}

	entry.HasExtra = hasBehavioralExtras(table)
	return entry, nil
}

func requiredString(table map[string]any, key string) (string, bool, error) {
	raw, exists := table[key]
	if !exists {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("expected string")
	}
	return value, true, nil
}

func requiredCommand(table map[string]any) (string, []string, bool, error) {
	raw, exists := table["command"]
	if !exists {
		return "", nil, false, nil
	}
	if command, ok := raw.(string); ok {
		if strings.TrimSpace(command) == "" {
			return "", nil, true, fmt.Errorf("expected non-empty string")
		}
		return command, nil, true, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return "", nil, true, fmt.Errorf("expected string or array of strings")
	}
	commandList := make([]string, 0, len(values))
	for _, value := range values {
		str, ok := value.(string)
		if !ok {
			return "", nil, true, fmt.Errorf("expected string or array of strings")
		}
		commandList = append(commandList, str)
	}
	if len(commandList) == 0 {
		return "", nil, true, fmt.Errorf("expected non-empty array")
	}
	return "", commandList, true, nil
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

func hasBehavioralExtras(table map[string]any) bool {
	for key, raw := range table {
		switch key {
		case "name", "transport", "command", "args", "env":
			continue
		case "disabled":
			disabled, _ := raw.(bool)
			if disabled {
				return true
			}
		case "disabled_tools":
			if values, ok := raw.([]any); ok && len(values) > 0 {
				return true
			}
			if values, ok := raw.([]string); ok && len(values) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

type rawServerEntry struct {
	Name string
	Raw  any
}

type serverConfig struct {
	Name          string            `toml:"name"`
	Transport     string            `toml:"transport"`
	Command       string            `toml:"command,omitempty"`
	CommandList   []string          `toml:"-"`
	Args          []string          `toml:"args,omitempty"`
	Env           map[string]string `toml:"env,omitempty"`
	Disabled      *bool             `toml:"disabled,omitempty"`
	DisabledTools []string          `toml:"disabled_tools,omitempty"`
	HasExtra      bool              `toml:"-"`
}

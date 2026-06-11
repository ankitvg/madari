package claudedesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

const (
	Target = "claude-desktop"
)

var ErrConflict = clients.ErrConflict

// SyncOptions configures sync behavior.
type SyncOptions = clients.SyncOptions

// SyncResult captures the computed or applied mutation plan.
type SyncResult = clients.SyncResult

// Sync synchronizes enabled Claude-targeted manifests into the Claude Desktop config file.
func Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeConfig(configPath)
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

	if err := applyPlan(configPath, statePath, root, rawServers, configExists, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// AttachRing adds the ring's ownership source to every member and
// materializes the eligible ones into the Claude Desktop config. Attaching
// onto any pre-existing unmanaged entry — equal values included — refuses
// with ErrConflict.
func AttachRing(ring registry.Ring, manifests []registry.Manifest, opts SyncOptions) (SyncResult, error) {
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeConfig(configPath)
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
	if err := applyPlan(configPath, statePath, root, rawServers, configExists, result.Removed, writeSet, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// DetachRing removes the ring's ownership source everywhere; entries that
// lose their last source leave the config. The ring file is not required, so
// stale sources can always be released.
func DetachRing(ringName string, opts SyncOptions) (SyncResult, error) {
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return SyncResult{}, err
	}
	statePath, err := resolveStatePath(opts.StatePath)
	if err != nil {
		return SyncResult{}, err
	}

	root, rawServers, existingServers, configExists, err := loadClaudeConfig(configPath)
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
	if err := applyPlan(configPath, statePath, root, rawServers, configExists, result.Removed, nil, nextState); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

// applyPlan writes the mutated config (backup + atomic write) and ownership
// state. The raw entries pass through untouched except for removals and the
// write set; pre-existing unmanaged entries are never serialized.
func applyPlan(
	configPath, statePath string,
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
		return fmt.Errorf("marshal Claude config: %w", err)
	}
	payload = append(payload, '\n')

	if configExists {
		if _, err := syncshared.BackupFile(configPath); err != nil {
			return fmt.Errorf("backup Claude config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, 0o644); err != nil {
		return fmt.Errorf("write Claude config: %w", err)
	}

	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return fmt.Errorf("write managed sync state: %w", err)
	}
	return nil
}

func DefaultDesktopConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	case "windows":
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
	}
}

func DefaultStatePath() (string, error) {
	root, err := registry.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", Target+"-managed.json"), nil
}

func resolveConfigPath(configPath string) (string, error) {
	return syncshared.ResolvePath(configPath, DefaultDesktopConfigPath)
}

func resolveStatePath(statePath string) (string, error) {
	return syncshared.ResolvePath(statePath, DefaultStatePath)
}

// entriesForTarget classifies every Claude Desktop-targeted manifest for the
// plan engine. Disabled manifests are ineligible (ownership release);
// manifests for other clients are omitted entirely. The Claude Desktop
// config is inherently user-scoped, so secrets are never refused here.
func entriesForTarget(manifests []registry.Manifest) map[string]syncshared.Entry[serverConfig] {
	entries := map[string]syncshared.Entry[serverConfig]{}
	for _, manifest := range manifests {
		if !manifest.HasClient(Target) {
			continue
		}
		entry := syncshared.Entry[serverConfig]{}
		if manifest.Enabled {
			entry.Eligible = true
			entry.Value = materializeServer(manifest)
		}
		entries[manifest.Name] = entry
	}
	return entries
}

func materializeServer(manifest registry.Manifest) serverConfig {
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

// loadClaudeConfig returns the config root plus two views of mcpServers:
// the raw JSON per entry (preserved verbatim for entries madari does not
// write) and the typed view used for planning.
func loadClaudeConfig(path string) (map[string]json.RawMessage, map[string]json.RawMessage, map[string]serverConfig, bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, map[string]json.RawMessage{}, map[string]serverConfig{}, false, nil
		}
		return nil, nil, nil, false, fmt.Errorf("read Claude config %q: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, nil, true, fmt.Errorf("parse Claude config JSON: %w", err)
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
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

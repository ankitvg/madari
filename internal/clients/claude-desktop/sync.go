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

	desiredServers := desiredServersForTarget(manifests)
	result, err := buildPlan(existingServers, managedState, desiredServers)
	if err != nil {
		return SyncResult{}, err
	}
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

	if opts.DryRun {
		return result, nil
	}

	// Rebuild from the raw entries so anything madari does not write keeps
	// its JSON value, including fields and server shapes madari does not
	// model. Only entries madari owns (managed) or introduces are serialized.
	mutated := make(map[string]json.RawMessage, len(rawServers)+len(desiredServers))
	for name, raw := range rawServers {
		mutated[name] = raw
	}
	for _, name := range result.Removed {
		delete(mutated, name)
	}
	for name, server := range desiredServers {
		if _, exists := rawServers[name]; exists && len(managedState[name]) == 0 {
			// Unmanaged entry whose values equal the manifest (buildPlan
			// guarantees, else it errored as a conflict): no adoption, no
			// rewrite — its JSON value stays untouched.
			continue
		}
		entryPayload, err := json.Marshal(server)
		if err != nil {
			return SyncResult{}, fmt.Errorf("marshal server %q: %w", name, err)
		}
		mutated[name] = entryPayload
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
		return SyncResult{}, fmt.Errorf("marshal Claude config: %w", err)
	}
	payload = append(payload, '\n')

	if configExists {
		if _, err := syncshared.BackupFile(configPath); err != nil {
			return SyncResult{}, fmt.Errorf("backup Claude config: %w", err)
		}
	}
	if err := syncshared.WriteFileAtomically(configPath, payload, 0o644); err != nil {
		return SyncResult{}, fmt.Errorf("write Claude config: %w", err)
	}

	nextState := syncshared.NextManagedState(managedState, syncshared.MapKeys(desiredServers), result.Added)
	if err := syncshared.SaveManagedState(statePath, nextState); err != nil {
		return SyncResult{}, fmt.Errorf("write managed sync state: %w", err)
	}

	return result, nil
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

func desiredServersForTarget(manifests []registry.Manifest) map[string]serverConfig {
	servers := map[string]serverConfig{}
	for _, manifest := range manifests {
		if !manifest.Enabled {
			continue
		}
		if !manifest.HasClient(Target) {
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
	return servers
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

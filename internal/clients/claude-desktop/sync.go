package claudedesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ankitvg/madari/internal/clients"
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

	root, existingServers, configExists, err := loadClaudeConfig(configPath)
	if err != nil {
		return SyncResult{}, err
	}
	managedNames, err := loadManagedState(statePath)
	if err != nil {
		return SyncResult{}, err
	}

	desiredServers := desiredServersForTarget(manifests)
	result, err := buildPlan(existingServers, managedNames, desiredServers)
	if err != nil {
		return SyncResult{}, err
	}
	result.ConfigPath = configPath
	result.DryRun = opts.DryRun

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
		return SyncResult{}, fmt.Errorf("marshal Claude config: %w", err)
	}
	payload = append(payload, '\n')

	if configExists {
		if _, err := backupFile(configPath); err != nil {
			return SyncResult{}, fmt.Errorf("backup Claude config: %w", err)
		}
	}
	if err := writeFileAtomically(configPath, payload, 0o644); err != nil {
		return SyncResult{}, fmt.Errorf("write Claude config: %w", err)
	}

	if err := saveManagedState(statePath, mapKeys(desiredServers)); err != nil {
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
	if strings.TrimSpace(configPath) != "" {
		resolved, err := expandHome(configPath)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return DefaultDesktopConfigPath()
}

func resolveStatePath(statePath string) (string, error) {
	if strings.TrimSpace(statePath) != "" {
		resolved, err := expandHome(statePath)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return DefaultStatePath()
}

func desiredServersForTarget(manifests []registry.Manifest) map[string]serverConfig {
	servers := map[string]serverConfig{}
	for _, manifest := range manifests {
		if !manifest.Enabled {
			continue
		}
		if !hasTargetClient(manifest.Clients) {
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

func hasTargetClient(clients []string) bool {
	for _, client := range clients {
		if strings.EqualFold(strings.TrimSpace(client), Target) {
			return true
		}
	}
	return false
}

func buildPlan(existing map[string]serverConfig, managed []string, desired map[string]serverConfig) (SyncResult, error) {
	managedSet := map[string]struct{}{}
	for _, name := range managed {
		managedSet[name] = struct{}{}
	}

	result := SyncResult{}
	for name := range managedSet {
		if _, stillDesired := desired[name]; stillDesired {
			continue
		}
		if _, exists := existing[name]; exists {
			result.Removed = append(result.Removed, name)
		}
	}

	var conflicts []string
	for name, desiredServer := range desired {
		existingServer, exists := existing[name]
		_, managedByMadari := managedSet[name]

		if !exists {
			result.Added = append(result.Added, name)
			continue
		}
		if !managedByMadari {
			if equalServer(existingServer, desiredServer) {
				result.Unchanged = append(result.Unchanged, name)
				continue
			}
			conflicts = append(conflicts, name)
			continue
		}

		if equalServer(existingServer, desiredServer) {
			result.Unchanged = append(result.Unchanged, name)
		} else {
			result.Updated = append(result.Updated, name)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	sort.Strings(result.Unchanged)

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return SyncResult{}, fmt.Errorf("%w: unmanaged entries already exist with different values: %s", ErrConflict, strings.Join(conflicts, ", "))
	}
	return result, nil
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

func loadClaudeConfig(path string) (map[string]json.RawMessage, map[string]serverConfig, bool, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, map[string]serverConfig{}, false, nil
		}
		return nil, nil, false, fmt.Errorf("read Claude config %q: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, true, fmt.Errorf("parse Claude config JSON: %w", err)
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

func loadManagedState(path string) ([]string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read managed state %q: %w", path, err)
	}

	state := managedState{}
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("parse managed state JSON: %w", err)
	}

	seen := map[string]struct{}{}
	unique := make([]string, 0, len(state.ManagedServers))
	for _, name := range state.ManagedServers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	sort.Strings(unique)
	return unique, nil
}

func saveManagedState(path string, names []string) error {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	state := managedState{ManagedServers: sorted}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed state JSON: %w", err)
	}
	payload = append(payload, '\n')

	return writeFileAtomically(path, payload, 0o644)
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func backupFile(path string) (string, error) {
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()

	backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return "", fmt.Errorf("ensure backup directory: %w", err)
	}

	target, err := os.Create(backupPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return "", err
	}
	if err := target.Close(); err != nil {
		return "", err
	}
	return backupPath, nil
}

func writeFileAtomically(path string, payload []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".madari-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

type managedState struct {
	ManagedServers []string `json:"managed_servers"`
}

type serverConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

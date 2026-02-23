package syncshared

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ankitvg/madari/internal/clients"
)

// ResolvePath resolves a path override (including "~" expansion) or falls back
// to the default resolver when override is empty.
func ResolvePath(override string, defaultResolver func() (string, error)) (string, error) {
	if strings.TrimSpace(override) != "" {
		resolved, err := ExpandHome(override)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return defaultResolver()
}

// BuildPlan computes sync mutations against existing + managed state.
func BuildPlan[T any](
	existing map[string]T,
	managed []string,
	desired map[string]T,
	equal func(a, b T) bool,
	conflictErr error,
) (clients.SyncResult, error) {
	if equal == nil {
		return clients.SyncResult{}, fmt.Errorf("equal comparer is required")
	}

	managedSet := map[string]struct{}{}
	for _, name := range managed {
		managedSet[name] = struct{}{}
	}

	result := clients.SyncResult{}
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
			if equal(existingServer, desiredServer) {
				result.Unchanged = append(result.Unchanged, name)
				continue
			}
			conflicts = append(conflicts, name)
			continue
		}

		if equal(existingServer, desiredServer) {
			result.Unchanged = append(result.Unchanged, name)
		} else {
			result.Updated = append(result.Updated, name)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	sort.Strings(result.Unchanged)

	if len(conflicts) == 0 {
		return result, nil
	}

	sort.Strings(conflicts)
	if conflictErr != nil {
		return clients.SyncResult{}, fmt.Errorf(
			"%w: unmanaged entries already exist with different values: %s",
			conflictErr,
			strings.Join(conflicts, ", "),
		)
	}
	return clients.SyncResult{}, fmt.Errorf(
		"unmanaged entries already exist with different values: %s",
		strings.Join(conflicts, ", "),
	)
}

// LoadManagedState reads and normalizes managed server names.
func LoadManagedState(path string) ([]string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
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

// SaveManagedState writes managed server names in sorted order.
func SaveManagedState(path string, names []string) error {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	state := managedState{ManagedServers: sorted}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed state JSON: %w", err)
	}
	payload = append(payload, '\n')

	return WriteFileAtomically(path, payload, 0o644)
}

func MapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func BackupFile(path string) (string, error) {
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

func WriteFileAtomically(path string, payload []byte, mode os.FileMode) error {
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

func ExpandHome(path string) (string, error) {
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

type managedState struct {
	ManagedServers []string `json:"managed_servers"`
}

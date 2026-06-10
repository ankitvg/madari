package syncshared

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

// SourceStandalone marks an entry as owned by a direct (non-ring) sync.
const SourceStandalone = "standalone"

// managedStateVersion is the only version the writer emits.
const managedStateVersion = 2

// LoadManagedState reads managed server state, mapping each server name to
// the sources that own it. Version 1 files (a bare name list) are read
// transparently with every name owned by SourceStandalone; unknown versions
// fail closed.
func LoadManagedState(path string) (map[string][]string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("read managed state %q: %w", path, err)
	}

	probe := struct {
		Version        int             `json:"version"`
		ManagedServers json.RawMessage `json:"managed_servers"`
	}{}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, fmt.Errorf("parse managed state JSON: %w", err)
	}

	switch probe.Version {
	case 0:
		var names []string
		if len(probe.ManagedServers) > 0 {
			if err := json.Unmarshal(probe.ManagedServers, &names); err != nil {
				return nil, fmt.Errorf("parse managed state v1 names: %w", err)
			}
		}
		servers := make(map[string][]string, len(names))
		for _, name := range names {
			servers[name] = []string{SourceStandalone}
		}
		return normalizeManagedState(servers), nil
	case managedStateVersion:
		servers := map[string][]string{}
		if len(probe.ManagedServers) > 0 {
			if err := json.Unmarshal(probe.ManagedServers, &servers); err != nil {
				return nil, fmt.Errorf("parse managed state v2 entries: %w", err)
			}
		}
		return normalizeManagedState(servers), nil
	default:
		return nil, fmt.Errorf("unsupported managed state version %d in %q", probe.Version, path)
	}
}

// SaveManagedState writes managed server state in the current (v2) format
// with deterministic ordering.
func SaveManagedState(path string, servers map[string][]string) error {
	state := managedStateFile{
		Version:        managedStateVersion,
		ManagedServers: normalizeManagedState(servers),
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed state JSON: %w", err)
	}
	payload = append(payload, '\n')

	return WriteFileAtomically(path, payload, 0o644)
}

// NextManagedState computes post-sync managed state: every desired name is
// owned at least by SourceStandalone, preserving any other sources already
// recorded for it.
func NextManagedState(previous map[string][]string, desiredNames []string) map[string][]string {
	next := make(map[string][]string, len(desiredNames))
	for _, name := range desiredNames {
		sources := append([]string(nil), previous[name]...)
		if !slices.Contains(sources, SourceStandalone) {
			sources = append(sources, SourceStandalone)
		}
		next[name] = sources
	}
	return next
}

// normalizeManagedState trims and deduplicates names and sources, dropping
// entries that end up without a name or any sources.
func normalizeManagedState(servers map[string][]string) map[string][]string {
	normalized := make(map[string][]string, len(servers))
	for name, sources := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		seen := map[string]struct{}{}
		unique := make([]string, 0, len(sources))
		for _, source := range sources {
			source = strings.TrimSpace(source)
			if source == "" {
				continue
			}
			if _, exists := seen[source]; exists {
				continue
			}
			seen[source] = struct{}{}
			unique = append(unique, source)
		}
		if len(unique) == 0 {
			continue
		}
		sort.Strings(unique)
		normalized[name] = unique
	}
	return normalized
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

type managedStateFile struct {
	Version        int                 `json:"version"`
	ManagedServers map[string][]string `json:"managed_servers"`
}

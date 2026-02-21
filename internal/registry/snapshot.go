package registry

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const SnapshotVersion = 1

type Snapshot struct {
	Version int        `json:"version"`
	Servers []Manifest `json:"servers"`
}

type ImportResult struct {
	Added     []string
	Updated   []string
	Unchanged []string
}

func (r ImportResult) HasChanges() bool {
	return len(r.Added)+len(r.Updated) > 0
}

func ExportSnapshot(store *Store) (Snapshot, error) {
	if store == nil {
		return Snapshot{}, fmt.Errorf("store is required")
	}
	servers, err := store.List()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Version: SnapshotVersion,
		Servers: servers,
	}, nil
}

func MarshalSnapshotJSON(snapshot Snapshot) ([]byte, error) {
	if snapshot.Version == 0 {
		snapshot.Version = SnapshotVersion
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot json: %w", err)
	}
	payload = append(payload, '\n')
	return payload, nil
}

func ParseSnapshotJSON(payload []byte) (Snapshot, error) {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return Snapshot{}, fmt.Errorf("snapshot payload is empty")
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot json: %w", err)
	}
	if snapshot.Version == 0 {
		snapshot.Version = SnapshotVersion
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func ImportSnapshot(store *Store, snapshot Snapshot, apply bool) (ImportResult, error) {
	if store == nil {
		return ImportResult{}, fmt.Errorf("store is required")
	}
	if err := snapshot.Validate(); err != nil {
		return ImportResult{}, err
	}

	existing, err := store.List()
	if err != nil {
		return ImportResult{}, err
	}
	existingByName := make(map[string]Manifest, len(existing))
	for _, manifest := range existing {
		existingByName[manifest.Name] = manifest
	}

	servers := append([]Manifest(nil), snapshot.Servers...)
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].Name < servers[j].Name
	})

	result := ImportResult{}
	for _, incoming := range servers {
		existingManifest, exists := existingByName[incoming.Name]
		if !exists {
			result.Added = append(result.Added, incoming.Name)
			if apply {
				if err := store.Save(incoming); err != nil {
					return ImportResult{}, fmt.Errorf("save imported server %q: %w", incoming.Name, err)
				}
			}
			continue
		}

		if manifestsEqual(existingManifest, incoming) {
			result.Unchanged = append(result.Unchanged, incoming.Name)
			continue
		}

		result.Updated = append(result.Updated, incoming.Name)
		if apply {
			if err := store.Save(incoming); err != nil {
				return ImportResult{}, fmt.Errorf("update imported server %q: %w", incoming.Name, err)
			}
		}
	}

	return result, nil
}

func (s Snapshot) Validate() error {
	if s.Version != SnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (supported: %d)", s.Version, SnapshotVersion)
	}

	seen := map[string]struct{}{}
	for _, server := range s.Servers {
		if err := server.Validate(); err != nil {
			return fmt.Errorf("invalid server %q: %w", server.Name, err)
		}
		if _, exists := seen[server.Name]; exists {
			return fmt.Errorf("duplicate server name %q in snapshot", server.Name)
		}
		seen[server.Name] = struct{}{}
	}

	return nil
}

func manifestsEqual(a, b Manifest) bool {
	if a.Name != b.Name || a.Command != b.Command || a.Enabled != b.Enabled || a.Description != b.Description {
		return false
	}

	if !slices.Equal(a.Args, b.Args) {
		return false
	}

	aClients := append([]string(nil), a.Clients...)
	bClients := append([]string(nil), b.Clients...)
	sort.Strings(aClients)
	sort.Strings(bClients)
	if !slices.Equal(aClients, bClients) {
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

	aReq := append([]string(nil), a.RequiredEnv.Keys...)
	bReq := append([]string(nil), b.RequiredEnv.Keys...)
	sort.Strings(aReq)
	sort.Strings(bReq)
	return slices.Equal(aReq, bReq)
}

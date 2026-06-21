package registry

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	snapshotVersionV1 = 1
	snapshotVersionV2 = 2
	SnapshotVersion   = 3
)

type Snapshot struct {
	Version int             `json:"version"`
	Servers []Manifest      `json:"servers"`
	Rings   []Ring          `json:"rings"`
	Skills  []SnapshotSkill `json:"skills"`
}

type SnapshotSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

type ImportResult struct {
	Added           []string
	Updated         []string
	Unchanged       []string
	RingsAdded      []string
	RingsUpdated    []string
	RingsUnchanged  []string
	SkillsAdded     []string
	SkillsUpdated   []string
	SkillsUnchanged []string
}

func (r ImportResult) HasChanges() bool {
	return len(r.Added)+len(r.Updated)+len(r.RingsAdded)+len(r.RingsUpdated)+len(r.SkillsAdded)+len(r.SkillsUpdated) > 0
}

func ExportSnapshot(store *Store) (Snapshot, error) {
	if store == nil {
		return Snapshot{}, fmt.Errorf("store is required")
	}
	servers, err := store.List()
	if err != nil {
		return Snapshot{}, err
	}
	rings, err := store.ListRings()
	if err != nil {
		return Snapshot{}, err
	}
	skills, err := snapshotSkillsFromStore(store)
	if err != nil {
		return Snapshot{}, err
	}

	// A snapshot must round-trip: every exported ring member has to exist in
	// the exported server set, or the fresh import of this backup would be
	// refused. Fail loudly instead of writing a broken artifact.
	exportable := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		exportable[server.Name] = struct{}{}
	}
	for _, ring := range rings {
		if err := validateRingMembersAgainst(ring, exportable); err != nil {
			return Snapshot{}, fmt.Errorf("%w; update or delete the ring before exporting", err)
		}
	}

	return Snapshot{
		Version: SnapshotVersion,
		Servers: servers,
		Rings:   rings,
		Skills:  skills,
	}, nil
}

func MarshalSnapshotJSON(snapshot Snapshot) ([]byte, error) {
	if snapshot.Version == 0 || snapshot.Version == snapshotVersionV1 || snapshot.Version == snapshotVersionV2 {
		snapshot.Version = SnapshotVersion
	}
	if snapshot.Servers == nil {
		snapshot.Servers = []Manifest{}
	}
	if snapshot.Rings == nil {
		snapshot.Rings = []Ring{}
	}
	if snapshot.Skills == nil {
		snapshot.Skills = []SnapshotSkill{}
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
	if snapshot.Servers == nil {
		snapshot.Servers = []Manifest{}
	}
	if snapshot.Rings == nil {
		snapshot.Rings = []Ring{}
	}
	if snapshot.Skills == nil {
		snapshot.Skills = []SnapshotSkill{}
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
	existingRings, err := store.ListRings()
	if err != nil {
		return ImportResult{}, err
	}
	existingRingsByName := make(map[string]Ring, len(existingRings))
	for _, ring := range existingRings {
		existingRingsByName[ring.Name] = ring
	}
	existingSkillsByName := map[string]Skill{}
	if len(snapshot.Skills) > 0 {
		existingSkills, err := store.ListSkills()
		if err != nil {
			return ImportResult{}, err
		}
		existingSkillsByName = make(map[string]Skill, len(existingSkills))
		for _, skill := range existingSkills {
			existingSkillsByName[skill.Name] = skill
		}
	}

	// Validate every ring before any write: a rejected snapshot must not
	// partially mutate the registry.
	allowedMembers := make(map[string]struct{}, len(existingByName)+len(snapshot.Servers))
	for name := range existingByName {
		allowedMembers[name] = struct{}{}
	}
	for _, server := range snapshot.Servers {
		allowedMembers[server.Name] = struct{}{}
	}
	for _, ring := range snapshot.Rings {
		if err := validateRingMembersAgainst(ring, allowedMembers); err != nil {
			return ImportResult{}, err
		}
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

	rings := append([]Ring(nil), snapshot.Rings...)
	sort.Slice(rings, func(i, j int) bool {
		return rings[i].Name < rings[j].Name
	})
	for _, incoming := range rings {
		existingRing, exists := existingRingsByName[incoming.Name]
		if !exists {
			result.RingsAdded = append(result.RingsAdded, incoming.Name)
			if apply {
				if err := store.SaveRing(incoming); err != nil {
					return ImportResult{}, fmt.Errorf("save imported ring %q: %w", incoming.Name, err)
				}
			}
			continue
		}

		if ringsEqual(existingRing, incoming) {
			result.RingsUnchanged = append(result.RingsUnchanged, incoming.Name)
			continue
		}

		result.RingsUpdated = append(result.RingsUpdated, incoming.Name)
		if apply {
			if err := store.SaveRing(incoming); err != nil {
				return ImportResult{}, fmt.Errorf("update imported ring %q: %w", incoming.Name, err)
			}
		}
	}

	skills := append([]SnapshotSkill(nil), snapshot.Skills...)
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	for _, incoming := range skills {
		existingSkill, exists := existingSkillsByName[incoming.Name]
		if !exists {
			result.SkillsAdded = append(result.SkillsAdded, incoming.Name)
			if apply {
				if err := store.SaveSkill(incoming.toSkill(), []byte(incoming.Content)); err != nil {
					return ImportResult{}, fmt.Errorf("save imported skill %q: %w", incoming.Name, err)
				}
			}
			continue
		}

		existingSnapshot := SnapshotSkill{
			Name:        existingSkill.Name,
			Description: existingSkill.Description,
		}
		if content, err := store.GetSkillContent(incoming.Name); err == nil {
			existingSnapshot.Content = string(content)
		}
		if skillsEqual(existingSnapshot, incoming) {
			result.SkillsUnchanged = append(result.SkillsUnchanged, incoming.Name)
			continue
		}

		result.SkillsUpdated = append(result.SkillsUpdated, incoming.Name)
		if apply {
			if err := store.SaveSkill(incoming.toSkill(), []byte(incoming.Content)); err != nil {
				return ImportResult{}, fmt.Errorf("update imported skill %q: %w", incoming.Name, err)
			}
		}
	}

	return result, nil
}

func (s Snapshot) Validate() error {
	if s.Version != snapshotVersionV1 && s.Version != snapshotVersionV2 && s.Version != SnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (supported: %d)", s.Version, SnapshotVersion)
	}
	if s.Version == snapshotVersionV1 && len(s.Rings) > 0 {
		return fmt.Errorf("snapshot version %d does not support rings", snapshotVersionV1)
	}
	if s.Version < SnapshotVersion && len(s.Skills) > 0 {
		return fmt.Errorf("snapshot version %d does not support skills", s.Version)
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

	seenRings := map[string]struct{}{}
	for _, ring := range s.Rings {
		if err := ring.Validate(); err != nil {
			return fmt.Errorf("invalid ring %q: %w", ring.Name, err)
		}
		if _, exists := seenRings[ring.Name]; exists {
			return fmt.Errorf("duplicate ring name %q in snapshot", ring.Name)
		}
		seenRings[ring.Name] = struct{}{}
	}

	seenSkills := map[string]struct{}{}
	for _, skill := range s.Skills {
		if err := skill.Validate(); err != nil {
			return fmt.Errorf("invalid skill %q: %w", skill.Name, err)
		}
		if _, exists := seenSkills[skill.Name]; exists {
			return fmt.Errorf("duplicate skill name %q in snapshot", skill.Name)
		}
		seenSkills[skill.Name] = struct{}{}
	}

	return nil
}

func validateRingMembersAgainst(ring Ring, allowed map[string]struct{}) error {
	var missing []string
	for _, member := range ring.Members {
		member = strings.TrimSpace(member)
		if _, ok := allowed[member]; ok {
			continue
		}
		missing = append(missing, member)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("ring %q references unknown servers: %s", ring.Name, strings.Join(missing, ", "))
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
	if !slices.Equal(aReq, bReq) {
		return false
	}

	aSecret := append([]string(nil), a.SecretEnv.Keys...)
	bSecret := append([]string(nil), b.SecretEnv.Keys...)
	sort.Strings(aSecret)
	sort.Strings(bSecret)
	return slices.Equal(aSecret, bSecret)
}

func ringsEqual(a, b Ring) bool {
	if a.Name != b.Name || a.Description != b.Description {
		return false
	}
	aMembers := normalizedRingMembers(a)
	bMembers := normalizedRingMembers(b)
	return slices.Equal(aMembers, bMembers)
}

func skillsEqual(a, b SnapshotSkill) bool {
	return a.Name == b.Name && a.Description == b.Description && a.Content == b.Content
}

func snapshotSkillsFromStore(store *Store) ([]SnapshotSkill, error) {
	skills, err := store.ListSkills()
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotSkill, 0, len(skills))
	for _, skill := range skills {
		content, err := store.GetSkillContent(skill.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, SnapshotSkill{
			Name:        skill.Name,
			Description: skill.Description,
			Content:     string(content),
		})
	}
	return out, nil
}

func (s SnapshotSkill) Validate() error {
	if err := s.toSkill().Validate(); err != nil {
		return err
	}
	return validateSkillContent([]byte(s.Content))
}

func (s SnapshotSkill) toSkill() Skill {
	return Skill{Name: s.Name, Description: s.Description}
}

func normalizedRingMembers(r Ring) []string {
	members := make([]string, 0, len(r.Members))
	for _, member := range r.Members {
		members = append(members, strings.TrimSpace(member))
	}
	sort.Strings(members)
	return members
}

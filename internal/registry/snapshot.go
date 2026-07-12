package registry

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
)

const (
	snapshotVersionV1 = 1
	snapshotVersionV2 = 2
	snapshotVersionV3 = 3
	snapshotVersionV4 = 4
	snapshotVersionV5 = 5
	snapshotVersionV6 = 6
	// snapshotVersionV7 added remote transport fields on server manifests;
	// pre-remote importers reject v7 snapshots cleanly by version instead of
	// misreading a remote server as a stdio manifest without a command.
	snapshotVersionV7 = 7
	// SnapshotVersion 8 adds [secret_headers]; older importers reject by
	// version instead of silently dropping the secrecy annotation.
	snapshotVersionV8 = 8
	// SnapshotVersion 9 adds bearer_token_env_var; older importers reject by
	// version instead of silently dropping the runtime auth env reference.
	snapshotVersionV9 = 9
	// SnapshotVersion 10 adds server access profiles and required ring policy;
	// older importers reject v10 rather than silently widening access by
	// dropping policy declarations.
	snapshotVersionV10 = 10
	// SnapshotVersion 11 adds bounded ring execution policy; older importers
	// reject v11 rather than silently dropping environment, sandbox, lifetime,
	// or credential-exposure declarations.
	SnapshotVersion = 11
)

type Snapshot struct {
	Version int             `json:"version"`
	Servers []Manifest      `json:"servers"`
	Rings   []Ring          `json:"rings"`
	Skills  []SnapshotSkill `json:"skills"`
}

type SnapshotSkill struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Content     string              `json:"content,omitempty"`
	Files       []SnapshotSkillFile `json:"files,omitempty"`
}

type SnapshotSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
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

	// A snapshot must round-trip: every exported ring reference has to exist
	// in the exported primitive sets, or the fresh import of this backup would
	// be refused. Fail loudly instead of writing a broken artifact.
	exportableServers := make(map[string]struct{}, len(servers))
	exportableManifests := make(map[string]Manifest, len(servers))
	for _, server := range servers {
		exportableServers[server.Name] = struct{}{}
		exportableManifests[server.Name] = server
	}
	exportableSkills := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		exportableSkills[skill.Name] = struct{}{}
	}
	for _, ring := range rings {
		if err := validateRingReferencesAgainst(ring, exportableServers, exportableSkills); err != nil {
			return Snapshot{}, fmt.Errorf("%w; update or delete the ring before exporting", err)
		}
		if err := validateRequiredRingAccessAgainst(ring, exportableManifests); err != nil {
			return Snapshot{}, fmt.Errorf("%w; update the ring or its server access profiles before exporting", err)
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
	if snapshot.Version == 0 || snapshot.Version < SnapshotVersion {
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
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot json: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Snapshot{}, fmt.Errorf("parse snapshot json: trailing data after snapshot document")
		}
		return Snapshot{}, fmt.Errorf("parse snapshot json: trailing data: %w", err)
	}
	if err := validateSnapshotPolicyPresence(payload); err != nil {
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

// validateSnapshotPolicyPresence rejects explicit JSON null values on V10/V11
// fields whose presence has policy meaning. The ordinary encoding/json pointer
// decoder maps both an omitted field and an explicit null to nil; accepting
// null would therefore turn an explicit declaration into legacy absence.
func validateSnapshotPolicyPresence(payload []byte) error {
	var raw struct {
		Servers []json.RawMessage `json:"servers"`
		Rings   []json.RawMessage `json:"rings"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	for i, serverRaw := range raw.Servers {
		server, err := rawJSONObject(serverRaw)
		if err != nil {
			return fmt.Errorf("servers[%d]: %w", i, err)
		}
		accessRaw, exists := server["access"]
		if !exists {
			continue
		}
		if rawJSONNull(accessRaw) {
			return fmt.Errorf("servers[%d].access must not be null; omit it for legacy absence", i)
		}
		access, err := rawJSONObject(accessRaw)
		if err != nil {
			return fmt.Errorf("servers[%d].access: %w", i, err)
		}
		for _, field := range []string{"allowed_tools", "denied_tools", "oauth_scopes", "default_approval", "tool_approvals"} {
			value, exists := access[field]
			if exists && rawJSONNull(value) {
				return fmt.Errorf("servers[%d].access.%s must not be null; omit it for absence or use an explicit clear value", i, field)
			}
		}
	}
	for i, ringRaw := range raw.Rings {
		ring, err := rawJSONObject(ringRaw)
		if err != nil {
			return fmt.Errorf("rings[%d]: %w", i, err)
		}
		policyRaw, exists := ring["policy"]
		if !exists {
			continue
		}
		if rawJSONNull(policyRaw) {
			return fmt.Errorf("rings[%d].policy must not be null; omit it for advisory behavior", i)
		}
		policy, err := rawJSONObject(policyRaw)
		if err != nil {
			return fmt.Errorf("rings[%d].policy: %w", i, err)
		}
		if enforcementRaw, exists := policy["enforcement"]; exists && rawJSONNull(enforcementRaw) {
			return fmt.Errorf("rings[%d].policy.enforcement must not be null; omit it for advisory behavior", i)
		}
		if executionRaw, exists := policy["execution"]; exists && rawJSONNull(executionRaw) {
			return fmt.Errorf("rings[%d].policy.execution must not be null; omit it for absence", i)
		}
	}
	return nil
}

func rawJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	value := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("expected object: %w", err)
	}
	if value == nil {
		return nil, fmt.Errorf("expected object")
	}
	return value, nil
}

func rawJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
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
	if len(snapshot.Skills) > 0 || snapshotHasRingSkills(snapshot) {
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
	allowedSkills := make(map[string]struct{}, len(existingSkillsByName)+len(snapshot.Skills))
	for name := range existingSkillsByName {
		allowedSkills[name] = struct{}{}
	}
	for _, skill := range snapshot.Skills {
		allowedSkills[skill.Name] = struct{}{}
	}
	for _, ring := range snapshot.Rings {
		if err := validateRingReferencesAgainst(ring, allowedMembers, allowedSkills); err != nil {
			return ImportResult{}, err
		}
	}

	// Validate policy against the final registry state before any write. An
	// incoming manifest may replace the access profile used by an existing
	// required ring, and an incoming ring may reference a manifest that remains
	// local rather than appearing in the snapshot.
	finalManifests := make(map[string]Manifest, len(existingByName)+len(snapshot.Servers))
	for name, manifest := range existingByName {
		finalManifests[name] = manifest
	}
	for _, manifest := range snapshot.Servers {
		finalManifests[manifest.Name] = manifest
	}
	finalRings := make(map[string]Ring, len(existingRingsByName)+len(snapshot.Rings))
	for name, ring := range existingRingsByName {
		finalRings[name] = ring
	}
	for _, ring := range snapshot.Rings {
		finalRings[ring.Name] = ring
	}
	finalRingNames := make([]string, 0, len(finalRings))
	for name := range finalRings {
		finalRingNames = append(finalRingNames, name)
	}
	sort.Strings(finalRingNames)
	for _, name := range finalRingNames {
		if err := validateRequiredRingAccessAgainst(finalRings[name], finalManifests); err != nil {
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
				pkg, err := incoming.toPackage()
				if err != nil {
					return ImportResult{}, fmt.Errorf("parse imported skill %q: %w", incoming.Name, err)
				}
				if err := store.SaveSkillPackage(pkg); err != nil {
					return ImportResult{}, fmt.Errorf("save imported skill %q: %w", incoming.Name, err)
				}
			}
			continue
		}

		existingSnapshot := SnapshotSkill{Name: existingSkill.Name, Description: existingSkill.Description}
		if pkg, err := store.GetSkillPackage(incoming.Name); err == nil {
			existingSnapshot = snapshotSkillFromPackage(pkg)
		}
		if skillsEqual(existingSnapshot, incoming) {
			result.SkillsUnchanged = append(result.SkillsUnchanged, incoming.Name)
			continue
		}

		result.SkillsUpdated = append(result.SkillsUpdated, incoming.Name)
		if apply {
			pkg, err := incoming.toPackage()
			if err != nil {
				return ImportResult{}, fmt.Errorf("parse imported skill %q: %w", incoming.Name, err)
			}
			if err := store.SaveSkillPackage(pkg); err != nil {
				return ImportResult{}, fmt.Errorf("update imported skill %q: %w", incoming.Name, err)
			}
		}
	}

	return result, nil
}

func (s Snapshot) Validate() error {
	if s.Version != snapshotVersionV1 && s.Version != snapshotVersionV2 && s.Version != snapshotVersionV3 && s.Version != snapshotVersionV4 && s.Version != snapshotVersionV5 && s.Version != snapshotVersionV6 && s.Version != snapshotVersionV7 && s.Version != snapshotVersionV8 && s.Version != snapshotVersionV9 && s.Version != snapshotVersionV10 && s.Version != SnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (supported: %d)", s.Version, SnapshotVersion)
	}
	if s.Version == snapshotVersionV1 && len(s.Rings) > 0 {
		return fmt.Errorf("snapshot version %d does not support rings", snapshotVersionV1)
	}
	if s.Version < snapshotVersionV3 && len(s.Skills) > 0 {
		return fmt.Errorf("snapshot version %d does not support skills", s.Version)
	}
	if s.Version < snapshotVersionV4 && snapshotHasRingSkills(s) {
		return fmt.Errorf("snapshot version %d does not support ring skills", s.Version)
	}
	if s.Version < snapshotVersionV5 && snapshotHasRingContracts(s) {
		return fmt.Errorf("snapshot version %d does not support ring contracts", s.Version)
	}
	if s.Version < snapshotVersionV7 && snapshotHasRemoteServers(s) {
		return fmt.Errorf("snapshot version %d does not support remote transports", s.Version)
	}
	if s.Version < snapshotVersionV8 && snapshotHasSecretHeaders(s) {
		return fmt.Errorf("snapshot version %d does not support secret_headers", s.Version)
	}
	if s.Version < snapshotVersionV9 && snapshotHasBearerTokenEnv(s) {
		return fmt.Errorf("snapshot version %d does not support bearer_token_env_var", s.Version)
	}
	if s.Version < snapshotVersionV10 && snapshotHasAccessProfiles(s) {
		return fmt.Errorf("snapshot version %d does not support server access profiles", s.Version)
	}
	if s.Version < snapshotVersionV10 && snapshotHasRingPolicies(s) {
		return fmt.Errorf("snapshot version %d does not support ring policies", s.Version)
	}
	if s.Version < SnapshotVersion && snapshotHasExecutionPolicies(s) {
		return fmt.Errorf("snapshot version %d does not support ring execution policies", s.Version)
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

func validateRingReferencesAgainst(ring Ring, allowedServers, allowedSkills map[string]struct{}) error {
	var missing []string
	for _, member := range ring.Members {
		member = strings.TrimSpace(member)
		if _, ok := allowedServers[member]; ok {
			continue
		}
		missing = append(missing, member)
	}
	var errs []string
	if len(missing) > 0 {
		sort.Strings(missing)
		errs = append(errs, fmt.Sprintf("unknown servers: %s", strings.Join(missing, ", ")))
	}
	var missingSkills []string
	for _, skill := range ring.Skills {
		skill = strings.TrimSpace(skill)
		if _, ok := allowedSkills[skill]; ok {
			continue
		}
		missingSkills = append(missingSkills, skill)
	}
	if len(missingSkills) > 0 {
		sort.Strings(missingSkills)
		errs = append(errs, fmt.Sprintf("unknown skills: %s", strings.Join(missingSkills, ", ")))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("ring %q references %s", ring.Name, strings.Join(errs, "; "))
}

func manifestsEqual(a, b Manifest) bool {
	if a.Name != b.Name || a.Command != b.Command || a.Enabled != b.Enabled || a.Description != b.Description {
		return false
	}

	if a.TransportType() != b.TransportType() || a.URL != b.URL ||
		a.TimeoutMS != b.TimeoutMS || a.OAuthResource != b.OAuthResource ||
		a.BearerTokenEnvVar != b.BearerTokenEnvVar {
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
	if !slices.Equal(aSecret, bSecret) {
		return false
	}

	aSecretHeaders := append([]string(nil), a.SecretHeaders.Keys...)
	bSecretHeaders := append([]string(nil), b.SecretHeaders.Keys...)
	sort.Strings(aSecretHeaders)
	sort.Strings(bSecretHeaders)
	return slices.Equal(aSecretHeaders, bSecretHeaders) && accessProfilesEqual(a.Access, b.Access)
}

func ringsEqual(a, b Ring) bool {
	if a.Name != b.Name || a.Description != b.Description {
		return false
	}
	aMembers := normalizedRingMembers(a)
	bMembers := normalizedRingMembers(b)
	if !slices.Equal(aMembers, bMembers) {
		return false
	}
	aSkills := normalizedRingSkills(a)
	bSkills := normalizedRingSkills(b)
	return slices.Equal(aSkills, bSkills) && ringContractsEqual(a.Contract, b.Contract) && ringPoliciesEqual(a.Policy, b.Policy)
}

func accessProfilesEqual(a, b *AccessProfile) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if !optionalStringSetsEqual(a.AllowedTools, b.AllowedTools) ||
		!optionalStringSetsEqual(a.DeniedTools, b.DeniedTools) ||
		!optionalStringSetsEqual(a.OAuthScopes, b.OAuthScopes) {
		return false
	}
	if a.DefaultApproval == nil || b.DefaultApproval == nil {
		if a.DefaultApproval != nil || b.DefaultApproval != nil {
			return false
		}
	} else if *a.DefaultApproval != *b.DefaultApproval {
		return false
	}
	if a.ToolApprovals == nil || b.ToolApprovals == nil {
		return a.ToolApprovals == nil && b.ToolApprovals == nil
	}
	if len(*a.ToolApprovals) != len(*b.ToolApprovals) {
		return false
	}
	for tool, approval := range *a.ToolApprovals {
		if other, exists := (*b.ToolApprovals)[tool]; !exists || other != approval {
			return false
		}
	}
	return true
}

func optionalStringSetsEqual(a, b *[]string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aValues := append([]string(nil), (*a)...)
	bValues := append([]string(nil), (*b)...)
	sort.Strings(aValues)
	sort.Strings(bValues)
	return slices.Equal(aValues, bValues)
}

func ringPoliciesEqual(a, b *RingPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Enforcement != b.Enforcement {
		return false
	}
	if a.Execution == nil || b.Execution == nil {
		return a.Execution == nil && b.Execution == nil
	}
	return *a.Execution == *b.Execution
}

func ringContractsEqual(a, b *RingContract) bool {
	if a.Empty() && b.Empty() {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Summary == b.Summary &&
		slices.Equal(a.GoodFor, b.GoodFor) &&
		slices.Equal(a.NotFor, b.NotFor) &&
		slices.Equal(a.RequiredContext, b.RequiredContext) &&
		slices.Equal(a.OptionalContext, b.OptionalContext) &&
		slices.Equal(a.ExpectedOutputs, b.ExpectedOutputs)
}

func skillsEqual(a, b SnapshotSkill) bool {
	aPkg, aErr := a.toPackage()
	bPkg, bErr := b.toPackage()
	if aErr != nil || bErr != nil {
		return a.Name == b.Name && a.Description == b.Description && a.Content == b.Content && slices.Equal(a.Files, b.Files)
	}
	return aPkg.Skill.Name == bPkg.Skill.Name &&
		aPkg.Skill.Description == bPkg.Skill.Description &&
		aPkg.Hash() == bPkg.Hash()
}

func snapshotSkillsFromStore(store *Store) ([]SnapshotSkill, error) {
	skills, err := store.ListSkills()
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotSkill, 0, len(skills))
	for _, skill := range skills {
		pkg, err := store.GetSkillPackage(skill.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshotSkillFromPackage(pkg))
	}
	return out, nil
}

func (s SnapshotSkill) Validate() error {
	_, err := s.toPackage()
	return err
}

func (s SnapshotSkill) toSkill() Skill {
	return Skill{Name: s.Name, Description: s.Description}
}

func (s SnapshotSkill) toPackage() (SkillPackage, error) {
	if len(s.Files) == 0 {
		return NewSkillPackageFromContent(s.toSkill(), []byte(s.Content))
	}
	files := make([]SkillPackageFile, 0, len(s.Files))
	for _, file := range s.Files {
		path, err := normalizeSkillPackagePath(file.Path)
		if err != nil {
			return SkillPackage{}, err
		}
		content, err := base64.StdEncoding.DecodeString(file.Content)
		if err != nil {
			return SkillPackage{}, fmt.Errorf("decode skill file %q: %w", file.Path, err)
		}
		mode, err := parseSnapshotSkillMode(file.Mode)
		if err != nil {
			return SkillPackage{}, fmt.Errorf("invalid mode for skill file %q: %w", file.Path, err)
		}
		files = append(files, SkillPackageFile{Path: path, Content: content, Mode: mode})
	}
	pkg, err := NewSkillPackage(files, s.Name)
	if err != nil {
		return SkillPackage{}, err
	}
	if strings.TrimSpace(s.Description) != "" && s.Description != pkg.Skill.Description {
		return SkillPackage{}, fmt.Errorf("skill %q description does not match %s frontmatter", s.Name, SkillFileName)
	}
	return pkg, nil
}

func snapshotSkillFromPackage(pkg SkillPackage) SnapshotSkill {
	files := make([]SnapshotSkillFile, 0, len(pkg.Files))
	for _, file := range pkg.Files {
		files = append(files, SnapshotSkillFile{
			Path:    file.Path,
			Content: base64.StdEncoding.EncodeToString(file.Content),
			Mode:    fmt.Sprintf("%04o", file.Mode.Perm()),
		})
	}
	return SnapshotSkill{
		Name:        pkg.Skill.Name,
		Description: pkg.Skill.Description,
		Files:       files,
	}
}

func parseSnapshotSkillMode(value string) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "", "0644":
		return 0o644, nil
	case "0755":
		return 0o755, nil
	default:
		return 0, fmt.Errorf("expected 0644 or 0755")
	}
}

func normalizedRingMembers(r Ring) []string {
	members := make([]string, 0, len(r.Members))
	for _, member := range r.Members {
		members = append(members, strings.TrimSpace(member))
	}
	sort.Strings(members)
	return members
}

func normalizedRingSkills(r Ring) []string {
	skills := make([]string, 0, len(r.Skills))
	for _, skill := range r.Skills {
		skills = append(skills, strings.TrimSpace(skill))
	}
	sort.Strings(skills)
	return skills
}

func snapshotHasRingSkills(snapshot Snapshot) bool {
	for _, ring := range snapshot.Rings {
		if len(ring.Skills) > 0 {
			return true
		}
	}
	return false
}

func snapshotHasRemoteServers(snapshot Snapshot) bool {
	for _, server := range snapshot.Servers {
		if server.IsRemote() {
			return true
		}
	}
	return false
}

func snapshotHasSecretHeaders(snapshot Snapshot) bool {
	for _, server := range snapshot.Servers {
		if len(server.SecretHeaders.Keys) > 0 {
			return true
		}
	}
	return false
}

func snapshotHasBearerTokenEnv(snapshot Snapshot) bool {
	for _, server := range snapshot.Servers {
		if strings.TrimSpace(server.BearerTokenEnvVar) != "" {
			return true
		}
	}
	return false
}

func snapshotHasAccessProfiles(snapshot Snapshot) bool {
	for _, server := range snapshot.Servers {
		if server.Access != nil {
			return true
		}
	}
	return false
}

func snapshotHasRingPolicies(snapshot Snapshot) bool {
	for _, ring := range snapshot.Rings {
		if ring.Policy != nil {
			return true
		}
	}
	return false
}

func snapshotHasExecutionPolicies(snapshot Snapshot) bool {
	for _, ring := range snapshot.Rings {
		if ring.Policy != nil && ring.Policy.Execution != nil {
			return true
		}
	}
	return false
}

func snapshotHasRingContracts(snapshot Snapshot) bool {
	for _, ring := range snapshot.Rings {
		if ring.Contract != nil {
			return true
		}
	}
	return false
}

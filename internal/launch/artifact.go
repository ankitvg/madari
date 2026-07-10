package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

const artifactSchemaVersion = 1

type EnforcedBy string

const (
	EnforcedByProvider EnforcedBy = "provider"
	EnforcedByClient   EnforcedBy = "client"
	EnforcedByProcess  EnforcedBy = "process"
	EnforcedByAdvisory EnforcedBy = "advisory"
	EnforcedByNone     EnforcedBy = "none"
)

type Verification string

const (
	VerificationObserved   Verification = "observed"
	VerificationConfigured Verification = "configured"
	VerificationUnverified Verification = "unverified"
)

// AuthorityControl explains where one requested or effective control is
// enforced and what Madari can honestly verify about it.
type AuthorityControl struct {
	Control      string       `json:"control"`
	EnforcedBy   EnforcedBy   `json:"enforced_by"`
	Verification Verification `json:"verification"`
}

type Authority struct {
	Requested []AuthorityControl `json:"requested"`
	Effective []AuthorityControl `json:"effective"`
}

type NamedHash struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type ContentHashes struct {
	Rings   []NamedHash `json:"rings"`
	Servers []NamedHash `json:"servers"`
	Skills  []NamedHash `json:"skills"`
}

// Input is the complete mutable planning snapshot. Compile normalizes and
// clones it; callers may mutate every input value after Compile returns without
// changing the resulting Artifact.
type Input struct {
	Target            string
	WorkingDirectory  string
	Prompt            string
	Rings             []registry.Ring
	Servers           []registry.Manifest
	Skills            []registry.SkillPackage
	CallerIsolatedEnv map[string]string
}

// Artifact is the immutable boundary between planning and execution. All
// fields are private and every accessor returns a defensive copy.
type Artifact struct {
	target           string
	workingDirectory string
	prompt           string
	rings            []registry.Ring
	servers          []registry.Manifest
	skills           []registry.SkillPackage
	codexOverrides   []string
	strictConfig     bool
	authority        Authority
	hashes           ContentHashes
	receiptHashes    ContentHashes
	policyDigest     string
	launchDigest     string
}

func Compile(input Input) (*Artifact, error) {
	target := strings.TrimSpace(input.Target)
	if target == "" {
		return nil, fmt.Errorf("launch target is required")
	}
	workingDirectory := input.WorkingDirectory
	if strings.TrimSpace(workingDirectory) == "" {
		return nil, fmt.Errorf("launch working directory is required")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("launch prompt is required")
	}

	rings, ringHashes, err := normalizeRings(input.Rings)
	if err != nil {
		return nil, err
	}
	servers, serverHashes, err := normalizeServers(input.Servers)
	if err != nil {
		return nil, err
	}
	skills, skillHashes, err := normalizeSkills(input.Skills)
	if err != nil {
		return nil, err
	}

	compiledPrompt := compilePrompt(rings, skills, prompt, workingDirectory)
	artifact := &Artifact{
		target:           target,
		workingDirectory: workingDirectory,
		prompt:           compiledPrompt,
		rings:            rings,
		servers:          servers,
		skills:           skills,
		hashes: ContentHashes{
			Rings:   ringHashes,
			Servers: serverHashes,
			Skills:  skillHashes,
		},
	}
	artifact.authority = compileAuthority(rings, servers, skills)
	artifact.receiptHashes, err = compileReceiptSafeHashes(rings, servers, skills)
	if err != nil {
		return nil, err
	}
	artifact.strictConfig = hasDeclaredAccess(servers)
	if target == "codex" {
		overrides, err := compileCodexOverrides(servers, workingDirectory, cloneStringMap(input.CallerIsolatedEnv))
		if err != nil {
			return nil, err
		}
		artifact.codexOverrides = overrides
	}
	artifact.policyDigest, err = compilePolicyDigest(rings, servers)
	if err != nil {
		return nil, err
	}
	artifact.launchDigest, err = compileLaunchDigest(artifact)
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

func (a *Artifact) Target() string {
	if a == nil {
		return ""
	}
	return a.target
}

func (a *Artifact) WorkingDirectory() string {
	if a == nil {
		return ""
	}
	return a.workingDirectory
}

func (a *Artifact) Prompt() string {
	if a == nil {
		return ""
	}
	return a.prompt
}

func (a *Artifact) Rings() []registry.Ring {
	if a == nil {
		return []registry.Ring{}
	}
	return cloneRings(a.rings)
}

func (a *Artifact) Servers() []registry.Manifest {
	if a == nil {
		return []registry.Manifest{}
	}
	return cloneServers(a.servers)
}

func (a *Artifact) Skills() []registry.SkillPackage {
	if a == nil {
		return []registry.SkillPackage{}
	}
	return cloneSkills(a.skills)
}

func (a *Artifact) CodexOverrides() []string {
	if a == nil || a.codexOverrides == nil {
		return []string{}
	}
	return append([]string(nil), a.codexOverrides...)
}

func (a *Artifact) StrictConfig() bool {
	return a != nil && a.strictConfig
}

func (a *Artifact) Authority() Authority {
	if a == nil {
		return Authority{Requested: []AuthorityControl{}, Effective: []AuthorityControl{}}
	}
	return cloneAuthority(a.authority)
}

func (a *Artifact) ContentHashes() ContentHashes {
	if a == nil {
		return ContentHashes{Rings: []NamedHash{}, Servers: []NamedHash{}, Skills: []NamedHash{}}
	}
	return cloneContentHashes(a.hashes)
}

// ReceiptContentHashes returns component fingerprints that omit prompt-like
// contract text, command arguments, URLs, header values, and environment
// values. Receipts use these hashes instead of full registry-content hashes.
func (a *Artifact) ReceiptContentHashes() ContentHashes {
	if a == nil {
		return ContentHashes{Rings: []NamedHash{}, Servers: []NamedHash{}, Skills: []NamedHash{}}
	}
	return cloneContentHashes(a.receiptHashes)
}

func (a *Artifact) PolicyDigest() string {
	if a == nil {
		return ""
	}
	return a.policyDigest
}

// Digest is a deterministic digest of the configured launch authority and
// component snapshots. It intentionally excludes the prompt body and runtime
// environment values so receipts can publish it without becoming a prompt or
// credential fingerprint oracle.
func (a *Artifact) Digest() string {
	if a == nil {
		return ""
	}
	return a.launchDigest
}

func normalizeRings(values []registry.Ring) ([]registry.Ring, []NamedHash, error) {
	seen := map[string]struct{}{}
	rings := make([]registry.Ring, 0, len(values))
	hashes := make([]NamedHash, 0, len(values))
	for _, value := range values {
		payload, err := registry.MarshalRing(value)
		if err != nil {
			return nil, nil, fmt.Errorf("normalize ring %q: %w", value.Name, err)
		}
		ring, err := registry.ParseRing(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("parse normalized ring %q: %w", value.Name, err)
		}
		if _, ok := seen[ring.Name]; ok {
			return nil, nil, fmt.Errorf("duplicate launch ring %q", ring.Name)
		}
		seen[ring.Name] = struct{}{}
		rings = append(rings, ring)
		hashes = append(hashes, NamedHash{Name: ring.Name, SHA256: hashBytes(payload)})
	}
	sortNamedHashes(hashes)
	return rings, hashes, nil
}

func normalizeServers(values []registry.Manifest) ([]registry.Manifest, []NamedHash, error) {
	seen := map[string]struct{}{}
	servers := make([]registry.Manifest, 0, len(values))
	hashes := make([]NamedHash, 0, len(values))
	for _, value := range values {
		payload, err := registry.MarshalManifest(value)
		if err != nil {
			return nil, nil, fmt.Errorf("normalize server %q: %w", value.Name, err)
		}
		manifest, err := registry.ParseManifest(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("parse normalized server %q: %w", value.Name, err)
		}
		if _, ok := seen[manifest.Name]; ok {
			return nil, nil, fmt.Errorf("duplicate launch server %q", manifest.Name)
		}
		seen[manifest.Name] = struct{}{}
		servers = append(servers, manifest)
		hashes = append(hashes, NamedHash{Name: manifest.Name, SHA256: hashBytes(payload)})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	sortNamedHashes(hashes)
	return servers, hashes, nil
}

func normalizeSkills(values []registry.SkillPackage) ([]registry.SkillPackage, []NamedHash, error) {
	seen := map[string]struct{}{}
	skills := make([]registry.SkillPackage, 0, len(values))
	hashes := make([]NamedHash, 0, len(values))
	for _, value := range values {
		pkg, err := registry.NewSkillPackage(value.Files, value.Skill.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("normalize skill %q: %w", value.Skill.Name, err)
		}
		if _, ok := seen[pkg.Skill.Name]; ok {
			return nil, nil, fmt.Errorf("duplicate launch skill %q", pkg.Skill.Name)
		}
		seen[pkg.Skill.Name] = struct{}{}
		skills = append(skills, pkg)
		hashes = append(hashes, NamedHash{Name: pkg.Skill.Name, SHA256: pkg.Hash()})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Skill.Name < skills[j].Skill.Name })
	sortNamedHashes(hashes)
	return skills, hashes, nil
}

func cloneRings(values []registry.Ring) []registry.Ring {
	out, _, err := normalizeRings(values)
	if err != nil {
		panic(fmt.Sprintf("clone immutable launch rings: %v", err))
	}
	if out == nil {
		return []registry.Ring{}
	}
	return out
}

func cloneServers(values []registry.Manifest) []registry.Manifest {
	out, _, err := normalizeServers(values)
	if err != nil {
		panic(fmt.Sprintf("clone immutable launch servers: %v", err))
	}
	if out == nil {
		return []registry.Manifest{}
	}
	return out
}

func cloneSkills(values []registry.SkillPackage) []registry.SkillPackage {
	out, _, err := normalizeSkills(values)
	if err != nil {
		panic(fmt.Sprintf("clone immutable launch skills: %v", err))
	}
	if out == nil {
		return []registry.SkillPackage{}
	}
	return out
}

func compileAuthority(rings []registry.Ring, servers []registry.Manifest, skills []registry.SkillPackage) Authority {
	controls := map[string]AuthorityControl{}
	hasMCPControl := false
	for _, server := range servers {
		if server.Access == nil {
			continue
		}
		if server.Access.AllowedTools != nil || server.Access.DeniedTools != nil {
			hasMCPControl = true
			controls["mcp-tool-filtering"] = AuthorityControl{Control: "mcp-tool-filtering", EnforcedBy: EnforcedByClient, Verification: VerificationConfigured}
		}
		if server.Access.OAuthScopes != nil {
			hasMCPControl = true
			controls["oauth-scopes"] = AuthorityControl{Control: "oauth-scopes", EnforcedBy: EnforcedByProvider, Verification: VerificationUnverified}
		}
		if server.Access.DefaultApproval != nil || server.Access.ToolApprovals != nil {
			hasMCPControl = true
			controls["tool-approvals"] = AuthorityControl{Control: "tool-approvals", EnforcedBy: EnforcedByClient, Verification: VerificationConfigured}
		}
	}
	hasInstructions := len(skills) > 0
	for _, ring := range rings {
		if !ring.Contract.Empty() {
			hasInstructions = true
			break
		}
	}
	if hasInstructions {
		controls["instructions"] = AuthorityControl{Control: "instructions", EnforcedBy: EnforcedByAdvisory, Verification: VerificationConfigured}
	}
	if !hasMCPControl {
		controls["mcp-access"] = AuthorityControl{Control: "mcp-access", EnforcedBy: EnforcedByNone, Verification: VerificationUnverified}
	}
	ordered := make([]AuthorityControl, 0, len(controls))
	for _, control := range controls {
		ordered = append(ordered, control)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Control < ordered[j].Control })
	return Authority{Requested: append([]AuthorityControl(nil), ordered...), Effective: append([]AuthorityControl(nil), ordered...)}
}

func compilePolicyDigest(rings []registry.Ring, servers []registry.Manifest) (string, error) {
	type ringPolicy struct {
		Name   string               `json:"name"`
		Policy *registry.RingPolicy `json:"policy,omitempty"`
	}
	type serverPolicy struct {
		Name   string                  `json:"name"`
		Access *registry.AccessProfile `json:"access,omitempty"`
	}
	record := struct {
		SchemaVersion int            `json:"schema_version"`
		Rings         []ringPolicy   `json:"rings"`
		Servers       []serverPolicy `json:"servers"`
	}{SchemaVersion: artifactSchemaVersion, Rings: []ringPolicy{}, Servers: []serverPolicy{}}
	for _, ring := range rings {
		record.Rings = append(record.Rings, ringPolicy{Name: ring.Name, Policy: ring.Policy})
	}
	for _, server := range servers {
		record.Servers = append(record.Servers, serverPolicy{Name: server.Name, Access: server.Access})
	}
	sort.Slice(record.Rings, func(i, j int) bool { return record.Rings[i].Name < record.Rings[j].Name })
	sort.Slice(record.Servers, func(i, j int) bool { return record.Servers[i].Name < record.Servers[j].Name })
	payload, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("marshal launch policy digest: %w", err)
	}
	return hashBytes(payload), nil
}

func compileLaunchDigest(a *Artifact) (string, error) {
	record := struct {
		SchemaVersion int           `json:"schema_version"`
		Target        string        `json:"target"`
		StrictConfig  bool          `json:"strict_config"`
		Authority     Authority     `json:"authority"`
		Hashes        ContentHashes `json:"content_hashes"`
		PolicyDigest  string        `json:"policy_digest"`
	}{
		SchemaVersion: artifactSchemaVersion,
		Target:        a.target,
		StrictConfig:  a.strictConfig,
		Authority:     a.authority,
		Hashes:        a.receiptHashes,
		PolicyDigest:  a.policyDigest,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("marshal launch digest: %w", err)
	}
	return hashBytes(payload), nil
}

func compileReceiptSafeHashes(rings []registry.Ring, servers []registry.Manifest, skills []registry.SkillPackage) (ContentHashes, error) {
	result := ContentHashes{Rings: []NamedHash{}, Servers: []NamedHash{}, Skills: []NamedHash{}}
	for _, ring := range rings {
		record := struct {
			Name        string               `json:"name"`
			Members     []string             `json:"members"`
			Skills      []string             `json:"skills"`
			Policy      *registry.RingPolicy `json:"policy"`
			HasContract bool                 `json:"has_contract"`
		}{
			Name:        ring.Name,
			Members:     append([]string(nil), ring.Members...),
			Skills:      append([]string(nil), ring.Skills...),
			Policy:      ring.Policy,
			HasContract: !ring.Contract.Empty(),
		}
		sort.Strings(record.Members)
		sort.Strings(record.Skills)
		payload, err := json.Marshal(record)
		if err != nil {
			return ContentHashes{}, fmt.Errorf("marshal receipt-safe ring hash: %w", err)
		}
		result.Rings = append(result.Rings, NamedHash{Name: ring.Name, SHA256: hashBytes(payload)})
	}
	for _, server := range servers {
		record := struct {
			Name              string                  `json:"name"`
			Transport         string                  `json:"transport"`
			Enabled           bool                    `json:"enabled"`
			Clients           []string                `json:"clients"`
			CommandConfigured bool                    `json:"command_configured"`
			ArgumentCount     int                     `json:"argument_count"`
			URLConfigured     bool                    `json:"url_configured"`
			HeaderNames       []string                `json:"header_names"`
			EnvironmentKeys   []string                `json:"environment_keys"`
			RequiredEnv       []string                `json:"required_env"`
			SecretEnv         []string                `json:"secret_env"`
			SecretHeaders     []string                `json:"secret_headers"`
			TimeoutMS         int                     `json:"timeout_ms"`
			OAuthConfigured   bool                    `json:"oauth_configured"`
			BearerTokenKey    string                  `json:"bearer_token_key"`
			Access            *registry.AccessProfile `json:"access"`
		}{
			Name:              server.Name,
			Transport:         server.TransportType(),
			Enabled:           server.Enabled,
			Clients:           append([]string(nil), server.Clients...),
			CommandConfigured: strings.TrimSpace(server.Command) != "",
			ArgumentCount:     len(server.Args),
			URLConfigured:     strings.TrimSpace(server.URL) != "",
			HeaderNames:       sortedMapStringKeys(server.Headers),
			EnvironmentKeys:   sortedMapStringKeys(server.Env),
			RequiredEnv:       sortedStringsCopy(server.RequiredEnv.Keys),
			SecretEnv:         sortedStringsCopy(server.SecretEnv.Keys),
			SecretHeaders:     sortedStringsCopy(server.SecretHeaders.Keys),
			TimeoutMS:         server.TimeoutMS,
			OAuthConfigured:   strings.TrimSpace(server.OAuthResource) != "",
			BearerTokenKey:    strings.TrimSpace(server.BearerTokenEnvVar),
			Access:            server.Access,
		}
		sort.Strings(record.Clients)
		payload, err := json.Marshal(record)
		if err != nil {
			return ContentHashes{}, fmt.Errorf("marshal receipt-safe server hash: %w", err)
		}
		result.Servers = append(result.Servers, NamedHash{Name: server.Name, SHA256: hashBytes(payload)})
	}
	for _, skill := range skills {
		result.Skills = append(result.Skills, NamedHash{Name: skill.Skill.Name, SHA256: skill.Hash()})
	}
	sortNamedHashes(result.Rings)
	sortNamedHashes(result.Servers)
	sortNamedHashes(result.Skills)
	return result, nil
}

func sortedMapStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringsCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

func hasDeclaredAccess(servers []registry.Manifest) bool {
	for _, server := range servers {
		if server.Access != nil {
			return true
		}
	}
	return false
}

func cloneAuthority(value Authority) Authority {
	return Authority{
		Requested: append([]AuthorityControl(nil), value.Requested...),
		Effective: append([]AuthorityControl(nil), value.Effective...),
	}
}

func cloneContentHashes(value ContentHashes) ContentHashes {
	return ContentHashes{
		Rings:   append([]NamedHash(nil), value.Rings...),
		Servers: append([]NamedHash(nil), value.Servers...),
		Skills:  append([]NamedHash(nil), value.Skills...),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sortNamedHashes(values []NamedHash) {
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

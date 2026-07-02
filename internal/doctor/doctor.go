package doctor

import (
	"encoding/json"
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

type Status string

const (
	StatusReady   Status = "ready"
	StatusWarning Status = "warn"
	StatusError   Status = "error"
	StatusSkipped Status = "skipped"
)

type IssueSeverity string

const (
	SeverityWarning IssueSeverity = "warn"
	SeverityError   IssueSeverity = "error"
)

type Issue struct {
	Severity IssueSeverity
	Code     string
	Message  string
}

type ServerReport struct {
	Name    string
	Enabled bool
	Clients []string
	Command string
	Status  Status
	Issues  []Issue
}

type ManifestError struct {
	File    string
	Message string
}

type ClientConfigReport struct {
	Target  string
	Path    string
	Exists  bool
	Status  Status
	Message string
}

type Summary struct {
	Total   int
	Ready   int
	Warning int
	Error   int
	Skipped int
}

type Report struct {
	ServersDir     string
	Servers        []ServerReport
	ManifestErrors []ManifestError
	ClientConfigs  []ClientConfigReport
	Drift          []DriftReport
	RingIssues     []RingIssue
	Summary        Summary
}

// RingIssue reports a ring consistency problem: an attached ring whose file
// no longer exists (error; ownership sources are never auto-removed, detach
// releases them), or a ring referencing a deleted manifest (warning).
// Registry-level issues carry no target/scope.
type RingIssue struct {
	Target   string
	Scope    string
	Ring     string
	Severity IssueSeverity
	Message  string
}

// DriftTarget names one managed-state/config pair to diff against manifests.
type DriftTarget struct {
	Adapter clients.ClientAdapter
	// Scope is passed through to the adapter ("" = adapter default).
	Scope string
	// StatePath locates managed state for this target+scope; drift is only
	// checked when it tracks at least one entry.
	StatePath string
	// ConfigPath overrides the adapter's default config path when non-empty.
	ConfigPath string
}

// DriftReport diffs materialized client entries against current manifests
// for one target+scope.
type DriftReport struct {
	Target     string
	Scope      string
	ConfigPath string
	// Stale are managed entries whose materialized values differ from the
	// manifest.
	Stale []string
	// Missing are managed entries absent from the client config.
	Missing []string
	// Orphaned are managed entries no longer desired; the next sync removes
	// them.
	Orphaned []string
	// Issue carries a sync-plan error (e.g. unmanaged-entry conflict).
	Issue  string
	Status Status
}

type Options struct {
	Adapters            []clients.ClientAdapter
	ConfigPathOverrides map[string]string // keyed by Target(), e.g. {"claude-desktop": "/custom/path"}
	EnvLookup           func(string) string
	// ServerTargets are client IDs whose manifests should receive server-level
	// readiness checks. If empty, adapter targets are used for compatibility.
	ServerTargets []string
	// DriftTargets enables drift detection for the listed state/config
	// pairs.
	DriftTargets []DriftTarget
	// Rings carries current ring definitions so drift plans reconcile ring
	// sources the same way sync does.
	Rings []registry.Ring
}

func Run(store *registry.Store, opts Options) (Report, error) {
	if store == nil {
		return Report{}, fmt.Errorf("store is required")
	}

	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.Getenv
	}

	report := Report{ServersDir: store.ServersDir()}

	manifests, manifestErrors, err := loadManifests(store.ServersDir())
	if err != nil {
		return Report{}, err
	}
	report.ManifestErrors = manifestErrors

	serverTargets := opts.ServerTargets
	if len(serverTargets) == 0 {
		serverTargets = adapterTargets(opts.Adapters)
	}

	report.Servers = make([]ServerReport, 0, len(manifests))
	for _, manifest := range manifests {
		report.Servers = append(report.Servers, inspectServer(manifest, envLookup, serverTargets))
	}
	sort.Slice(report.Servers, func(i, j int) bool {
		return report.Servers[i].Name < report.Servers[j].Name
	})

	report.ClientConfigs = make([]ClientConfigReport, 0, len(opts.Adapters))
	for _, adapter := range opts.Adapters {
		if !hasTargetInManifests(manifests, adapter.Target()) {
			report.ClientConfigs = append(report.ClientConfigs, ClientConfigReport{
				Target: adapter.Target(),
				Status: StatusSkipped,
			})
			continue
		}
		configPath, err := resolveAdapterConfigPath(adapter, opts.ConfigPathOverrides)
		if err != nil {
			return Report{}, err
		}
		cr := inspectClientConfig(adapter.Target(), configPath)
		cr.Target = adapter.Target()
		report.ClientConfigs = append(report.ClientConfigs, cr)
	}

	// Drift is only meaningful against a fully parsed registry: a manifest
	// that failed to load would otherwise read as "orphaned" with a sync fix
	// hint, while the real sync command refuses to run until the manifest is
	// repaired.
	report.Drift = []DriftReport{}
	if len(manifestErrors) == 0 {
		report.Drift = checkDrift(manifests, opts.DriftTargets, opts.Rings)
	}

	report.RingIssues = checkRingIssues(manifests, opts.DriftTargets, opts.Rings)

	report.Summary = summarize(report)
	return report, nil
}

// checkRingIssues flags attached rings whose file is gone (per target+scope)
// and ring members whose manifest no longer exists (registry-level). It runs
// regardless of manifest errors: a stale ring source must never hide.
func checkRingIssues(manifests []registry.Manifest, targets []DriftTarget, rings []registry.Ring) []RingIssue {
	issues := []RingIssue{}

	known := make(map[string]registry.Ring, len(rings))
	for _, ring := range rings {
		known[ring.Name] = ring
	}
	for _, target := range targets {
		state, err := syncshared.LoadManagedState(target.StatePath)
		if err != nil {
			continue // drift already reports unreadable state
		}
		for _, ring := range syncshared.AttachedRings(state) {
			if _, exists := known[ring]; exists {
				continue
			}
			fix := fmt.Sprintf("madari ring detach %s %s", ring, target.Adapter.Target())
			if target.Scope == clients.ScopeUser {
				fix += " --scope user"
			}
			issues = append(issues, RingIssue{
				Target:   target.Adapter.Target(),
				Scope:    target.Scope,
				Ring:     ring,
				Severity: SeverityError,
				Message:  fmt.Sprintf("ring file missing; release the stale sources with `%s` (pass --config-path if it was attached to a custom config)", fix),
			})
		}
	}

	manifestNames := make(map[string]bool, len(manifests))
	for _, manifest := range manifests {
		manifestNames[manifest.Name] = true
	}
	for _, ring := range rings {
		for _, member := range ring.Members {
			if !manifestNames[strings.TrimSpace(member)] {
				issues = append(issues, RingIssue{
					Ring:     ring.Name,
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("member %s no longer exists in the registry", member),
				})
			}
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Ring != issues[j].Ring {
			return issues[i].Ring < issues[j].Ring
		}
		return issues[i].Target < issues[j].Target
	})
	return issues
}

// checkDrift diffs materialized client entries against manifests by reading
// each target's dry-run sync plan. Targets without managed entries are
// skipped: no ownership, nothing to drift.
func checkDrift(manifests []registry.Manifest, targets []DriftTarget, rings []registry.Ring) []DriftReport {
	reports := []DriftReport{}
	for _, target := range targets {
		dr := DriftReport{
			Target: target.Adapter.Target(),
			Scope:  target.Scope,
			Status: StatusReady,
		}

		state, err := syncshared.LoadManagedState(target.StatePath)
		if err != nil {
			dr.Status = StatusError
			dr.Issue = fmt.Sprintf("read managed state: %v", err)
			reports = append(reports, dr)
			continue
		}
		if len(state) == 0 {
			continue
		}

		plan, err := target.Adapter.Sync(syncableForTarget(manifests, target.Adapter.Target()), clients.SyncOptions{
			ConfigPath: target.ConfigPath,
			StatePath:  target.StatePath,
			Rings:      rings,
			Scope:      target.Scope,
			DryRun:     true,
		})
		if err != nil {
			dr.Status = StatusError
			dr.Issue = fmt.Sprintf("compute sync plan: %v", err)
			reports = append(reports, dr)
			continue
		}

		dr.ConfigPath = plan.ConfigPath
		dr.Stale = append([]string(nil), plan.Updated...)
		for _, name := range plan.Added {
			if _, managed := state[name]; managed {
				dr.Missing = append(dr.Missing, name)
			}
		}
		dr.Orphaned = append([]string(nil), plan.Removed...)
		if len(dr.Stale)+len(dr.Missing)+len(dr.Orphaned) > 0 {
			dr.Status = StatusWarning
		}
		reports = append(reports, dr)
	}
	return reports
}

// syncableForTarget mirrors the sync command's manifest filtering: entries
// enabled for this target whose command fails validation never reach the
// adapter, so the drift plan matches what `madari sync` would actually do.
func syncableForTarget(manifests []registry.Manifest, target string) []registry.Manifest {
	out := make([]registry.Manifest, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.Enabled && manifest.HasClient(target) && !manifest.IsRemote() {
			if issue := validateAbsoluteExecutablePath(manifest.Command); issue != nil {
				continue
			}
		}
		out = append(out, manifest)
	}
	return out
}

func summarize(report Report) Summary {
	summary := Summary{Total: len(report.Servers)}
	for _, server := range report.Servers {
		switch server.Status {
		case StatusReady:
			summary.Ready++
		case StatusWarning:
			summary.Warning++
		case StatusError:
			summary.Error++
		case StatusSkipped:
			summary.Skipped++
		}
	}

	summary.Error += len(report.ManifestErrors)
	for _, cc := range report.ClientConfigs {
		if cc.Status == StatusError {
			summary.Error++
		} else if cc.Status == StatusWarning {
			summary.Warning++
		}
	}
	for _, dr := range report.Drift {
		if dr.Status == StatusError {
			summary.Error++
		} else if dr.Status == StatusWarning {
			summary.Warning++
		}
	}
	for _, issue := range report.RingIssues {
		if issue.Severity == SeverityError {
			summary.Error++
		} else {
			summary.Warning++
		}
	}
	return summary
}

func inspectServer(manifest registry.Manifest, envLookup func(string) string, targets []string) ServerReport {
	report := ServerReport{
		Name:    manifest.Name,
		Enabled: manifest.Enabled,
		Clients: append([]string(nil), manifest.Clients...),
		Command: manifest.Command,
		Status:  StatusSkipped,
		Issues:  []Issue{},
	}

	if !manifest.Enabled || !hasTarget(manifest, targets) {
		return report
	}

	report.Status = StatusReady
	if !manifest.IsRemote() {
		if issue := validateAbsoluteExecutablePath(manifest.Command); issue != nil {
			report.Issues = append(report.Issues, *issue)
			report.Status = StatusError
		}
	}

	if manifest.IsRemote() && strings.TrimSpace(manifest.URL) == "" {
		report.Issues = append(report.Issues, Issue{
			Severity: SeverityError,
			Code:     "missing_remote_url",
			Message:  "remote transport requires url",
		})
		report.Status = StatusError
	}

	for _, key := range manifest.RequiredEnv.Keys {
		if strings.TrimSpace(envLookup(key)) == "" {
			report.Issues = append(report.Issues, Issue{
				Severity: SeverityWarning,
				Code:     "missing_required_env",
				Message:  fmt.Sprintf("missing required env key %s", key),
			})
			if report.Status == StatusReady {
				report.Status = StatusWarning
			}
		}
	}

	return report
}

func resolveAdapterConfigPath(adapter clients.ClientAdapter, overrides map[string]string) (string, error) {
	if override := overrides[adapter.Target()]; strings.TrimSpace(override) != "" {
		resolved, err := expandHome(override)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return adapter.DefaultConfigPath()
}

func InspectConfigPath(path string) ClientConfigReport {
	return inspectClientConfig("", path)
}

func InspectClientConfigPath(target, path string) ClientConfigReport {
	return inspectClientConfig(target, path)
}

func inspectClientConfig(target, path string) ClientConfigReport {
	report := ClientConfigReport{Path: path}
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Status = StatusWarning
			report.Message = "config file not found"
			return report
		}
		report.Status = StatusError
		report.Message = fmt.Sprintf("unable to read config: %v", err)
		return report
	}

	report.Exists = true
	switch target {
	case "codex":
		return inspectCodexTOMLClientConfig(payload, report)
	case "vibe":
		return inspectVibeTOMLClientConfig(payload, report)
	case "":
		if filepath.Ext(path) == ".toml" {
			return inspectCodexTOMLClientConfig(payload, report)
		}
	}
	return inspectJSONClientConfig(payload, report)
}

func inspectJSONClientConfig(payload []byte, report ClientConfigReport) ClientConfigReport {
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &root); err != nil {
		report.Status = StatusError
		report.Message = fmt.Sprintf("invalid JSON: %v", err)
		return report
	}

	if raw, exists := root["mcpServers"]; exists {
		servers := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &servers); err != nil {
			report.Status = StatusError
			report.Message = fmt.Sprintf("invalid mcpServers object: %v", err)
			return report
		}
	}

	report.Status = StatusReady
	report.Message = "ok"
	return report
}

func inspectCodexTOMLClientConfig(payload []byte, report ClientConfigReport) ClientConfigReport {
	root := map[string]any{}
	if err := toml.Unmarshal(payload, &root); err != nil {
		report.Status = StatusError
		report.Message = fmt.Sprintf("invalid TOML: %v", err)
		return report
	}

	if raw, exists := root["mcp_servers"]; exists {
		if _, ok := raw.(map[string]any); !ok {
			report.Status = StatusError
			report.Message = "invalid mcp_servers value: expected table"
			return report
		}
	}

	report.Status = StatusReady
	report.Message = "ok"
	return report
}

func inspectVibeTOMLClientConfig(payload []byte, report ClientConfigReport) ClientConfigReport {
	root := map[string]any{}
	if err := toml.Unmarshal(payload, &root); err != nil {
		report.Status = StatusError
		report.Message = fmt.Sprintf("invalid TOML: %v", err)
		return report
	}

	if raw, exists := root["mcp_servers"]; exists {
		servers, ok := raw.([]any)
		if !ok {
			report.Status = StatusError
			report.Message = "invalid mcp_servers value: expected array"
			return report
		}
		for i, rawServer := range servers {
			server, ok := rawServer.(map[string]any)
			if !ok {
				report.Status = StatusError
				report.Message = fmt.Sprintf("invalid mcp_servers[%d] value: expected table", i)
				return report
			}
			if name, ok := server["name"].(string); !ok || strings.TrimSpace(name) == "" {
				report.Status = StatusError
				report.Message = fmt.Sprintf("invalid mcp_servers[%d].name value: expected non-empty string", i)
				return report
			}
			transport, ok := server["transport"].(string)
			if !ok || strings.TrimSpace(transport) == "" {
				report.Status = StatusError
				report.Message = fmt.Sprintf("invalid mcp_servers[%d].transport value: expected non-empty string", i)
				return report
			}
			if strings.TrimSpace(transport) == "stdio" {
				if err := validateVibeStdioCommand(server["command"]); err != nil {
					report.Status = StatusError
					report.Message = fmt.Sprintf("invalid mcp_servers[%d].command value: %v", i, err)
					return report
				}
			}
		}
	}

	report.Status = StatusReady
	report.Message = "ok"
	return report
}

func validateVibeStdioCommand(raw any) error {
	command, ok := raw.(string)
	if ok {
		if strings.TrimSpace(command) == "" {
			return fmt.Errorf("expected non-empty string")
		}
		return nil
	}

	values, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("expected string or array of strings")
	}
	if len(values) == 0 {
		return fmt.Errorf("expected non-empty array")
	}
	for _, value := range values {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string or array of strings")
		}
	}
	return nil
}

func loadManifests(serversDir string) ([]registry.Manifest, []ManifestError, error) {
	entries, err := os.ReadDir(serversDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []registry.Manifest{}, []ManifestError{}, nil
		}
		return nil, nil, fmt.Errorf("read servers directory: %w", err)
	}

	manifests := []registry.Manifest{}
	manifestErrors := []ManifestError{}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		path := filepath.Join(serversDir, entry.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			manifestErrors = append(manifestErrors, ManifestError{
				File:    path,
				Message: fmt.Sprintf("read failed: %v", err),
			})
			continue
		}

		manifest, err := registry.ParseManifest(payload)
		if err != nil {
			manifestErrors = append(manifestErrors, ManifestError{
				File:    path,
				Message: err.Error(),
			})
			continue
		}

		expectedName := strings.TrimSuffix(entry.Name(), ".toml")
		if manifest.Name != expectedName {
			manifestErrors = append(manifestErrors, ManifestError{
				File:    path,
				Message: fmt.Sprintf("manifest name %q does not match filename %q", manifest.Name, expectedName),
			})
			continue
		}

		manifests = append(manifests, manifest)
	}

	return manifests, manifestErrors, nil
}

func adapterTargets(adapters []clients.ClientAdapter) []string {
	targets := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		targets = append(targets, adapter.Target())
	}
	return targets
}

func hasTarget(manifest registry.Manifest, targets []string) bool {
	for _, target := range targets {
		if manifest.HasClient(target) {
			return true
		}
	}
	return false
}

func hasTargetInManifests(manifests []registry.Manifest, target string) bool {
	for _, manifest := range manifests {
		// No adapter materializes remote transports yet, so remote manifests
		// alone don't warrant a client config on disk.
		if manifest.IsRemote() {
			continue
		}
		if manifest.HasClient(target) {
			return true
		}
	}
	return false
}

// validateAbsoluteExecutablePath wraps the shared command-validity check so
// doctor, drift, and sync filtering never disagree about what is runnable.
func validateAbsoluteExecutablePath(path string) *Issue {
	if err := clients.ValidateCommandPath(path); err != nil {
		return &Issue{Severity: SeverityError, Code: err.Code, Message: err.Message}
	}
	return nil
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

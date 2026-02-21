package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients/claude"
	"github.com/ankitvg/madari/internal/registry"
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

type ClaudeConfigReport struct {
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
	ClaudeConfig   ClaudeConfigReport
	Summary        Summary
}

type Options struct {
	ClaudeConfigPath string
	EnvLookup        func(string) string
}

func Run(store *registry.Store, opts Options) (Report, error) {
	if store == nil {
		return Report{}, fmt.Errorf("store is required")
	}

	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.Getenv
	}

	claudePath, err := resolveClaudePath(opts.ClaudeConfigPath)
	if err != nil {
		return Report{}, err
	}

	report := Report{ServersDir: store.ServersDir()}
	report.ClaudeConfig = inspectClaudeConfig(claudePath)

	manifests, manifestErrors, err := loadManifests(store.ServersDir())
	if err != nil {
		return Report{}, err
	}
	report.ManifestErrors = manifestErrors

	report.Servers = make([]ServerReport, 0, len(manifests))
	for _, manifest := range manifests {
		report.Servers = append(report.Servers, inspectServer(manifest, envLookup))
	}
	sort.Slice(report.Servers, func(i, j int) bool {
		return report.Servers[i].Name < report.Servers[j].Name
	})

	report.Summary = summarize(report)
	return report, nil
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
	if report.ClaudeConfig.Status == StatusError {
		summary.Error++
	} else if report.ClaudeConfig.Status == StatusWarning {
		summary.Warning++
	}
	return summary
}

func inspectServer(manifest registry.Manifest, envLookup func(string) string) ServerReport {
	report := ServerReport{
		Name:    manifest.Name,
		Enabled: manifest.Enabled,
		Clients: append([]string(nil), manifest.Clients...),
		Command: manifest.Command,
		Status:  StatusSkipped,
		Issues:  []Issue{},
	}

	if !manifest.Enabled || !hasClaudeTarget(manifest.Clients) {
		return report
	}

	report.Status = StatusReady
	if issue := validateAbsoluteExecutablePath(manifest.Command); issue != nil {
		report.Issues = append(report.Issues, *issue)
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

func resolveClaudePath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		resolved, err := expandHome(path)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	return claude.DefaultDesktopConfigPath()
}

func inspectClaudeConfig(path string) ClaudeConfigReport {
	report := ClaudeConfigReport{Path: path}
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

func hasClaudeTarget(clients []string) bool {
	for _, client := range clients {
		if strings.EqualFold(strings.TrimSpace(client), claude.Target) {
			return true
		}
	}
	return false
}

func validateAbsoluteExecutablePath(path string) *Issue {
	if !filepath.IsAbs(path) {
		return &Issue{Severity: SeverityError, Code: "command_not_absolute", Message: "command path must be absolute"}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Issue{Severity: SeverityError, Code: "command_missing", Message: "command path does not exist"}
		}
		return &Issue{Severity: SeverityError, Code: "command_stat_error", Message: fmt.Sprintf("unable to inspect command path: %v", err)}
	}
	if info.IsDir() {
		return &Issue{Severity: SeverityError, Code: "command_is_directory", Message: "command path is a directory"}
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return &Issue{Severity: SeverityError, Code: "command_not_executable", Message: "command path is not executable"}
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

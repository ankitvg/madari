package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ankitvg/madari/internal/doctor"
)

// jsonSchemaVersion identifies the envelope version shared by all --json
// output. Field names and shapes below are a public contract: additions are
// allowed, renames and removals require a version bump.
const jsonSchemaVersion = 1

type listJSON struct {
	SchemaVersion int          `json:"schema_version"`
	Command       string       `json:"command"`
	Servers       []serverJSON `json:"servers"`
}

type serverJSON struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Command string   `json:"command"`
	Clients []string `json:"clients"`
	Sources []string `json:"sources"`
}

type statusJSON struct {
	SchemaVersion  int                `json:"schema_version"`
	Command        string             `json:"command"`
	Summary        summaryJSON        `json:"summary"`
	ClientConfigs  []statusConfigJSON `json:"client_configs"`
	Managed        []managedJSON      `json:"managed"`
	ManifestErrors int                `json:"manifest_errors"`
	Drift          []driftJSON        `json:"drift"`
}

type summaryJSON struct {
	Total   int `json:"total"`
	Ready   int `json:"ready"`
	Warning int `json:"warning"`
	Error   int `json:"error"`
	Skipped int `json:"skipped"`
}

type statusConfigJSON struct {
	Target string `json:"target"`
	Status string `json:"status"`
}

type managedJSON struct {
	Target  string   `json:"target"`
	Entries int      `json:"entries"`
	Sources []string `json:"sources"`
}

type doctorJSON struct {
	SchemaVersion  int                 `json:"schema_version"`
	Command        string              `json:"command"`
	ServersDir     string              `json:"servers_dir"`
	Servers        []doctorServerJSON  `json:"servers"`
	ManifestErrors []manifestErrorJSON `json:"manifest_errors"`
	ClientConfigs  []doctorConfigJSON  `json:"client_configs"`
	Drift          []driftJSON         `json:"drift"`
	Summary        summaryJSON         `json:"summary"`
}

type driftJSON struct {
	Target     string   `json:"target"`
	Scope      string   `json:"scope"`
	ConfigPath string   `json:"config_path"`
	Status     string   `json:"status"`
	Stale      []string `json:"stale"`
	Missing    []string `json:"missing"`
	Orphaned   []string `json:"orphaned"`
	Issue      string   `json:"issue"`
}

type doctorServerJSON struct {
	Name    string      `json:"name"`
	Enabled bool        `json:"enabled"`
	Clients []string    `json:"clients"`
	Command string      `json:"command"`
	Status  string      `json:"status"`
	Issues  []issueJSON `json:"issues"`
}

type issueJSON struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type manifestErrorJSON struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

type doctorConfigJSON struct {
	Target  string `json:"target"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type syncJSON struct {
	SchemaVersion int      `json:"schema_version"`
	Command       string   `json:"command"`
	Target        string   `json:"target"`
	ConfigPath    string   `json:"config_path"`
	DryRun        bool     `json:"dry_run"`
	Added         []string `json:"added"`
	Updated       []string `json:"updated"`
	Removed       []string `json:"removed"`
	Unchanged     []string `json:"unchanged"`
	Skipped       []string `json:"skipped"`
	Refused       []string `json:"refused"`
}

// writeJSON emits one indented JSON document followed by a newline; --json
// modes must write nothing else to stdout.
func writeJSON(out io.Writer, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON output: %w", err)
	}
	data = append(data, '\n')
	_, err = out.Write(data)
	return err
}

// nonNilStrings keeps list-valued JSON fields as [] instead of null.
func nonNilStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func summaryToJSON(s doctor.Summary) summaryJSON {
	return summaryJSON{
		Total:   s.Total,
		Ready:   s.Ready,
		Warning: s.Warning,
		Error:   s.Error,
		Skipped: s.Skipped,
	}
}

func driftToJSON(reports []doctor.DriftReport) []driftJSON {
	out := make([]driftJSON, 0, len(reports))
	for _, dr := range reports {
		scope := dr.Scope
		if scope == "" {
			scope = "default"
		}
		out = append(out, driftJSON{
			Target:     dr.Target,
			Scope:      scope,
			ConfigPath: dr.ConfigPath,
			Status:     string(dr.Status),
			Stale:      nonNilStrings(dr.Stale),
			Missing:    nonNilStrings(dr.Missing),
			Orphaned:   nonNilStrings(dr.Orphaned),
			Issue:      dr.Issue,
		})
	}
	return out
}

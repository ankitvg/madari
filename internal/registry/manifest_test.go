package registry

import (
	"strings"
	"testing"
)

func baseManifest() Manifest {
	return Manifest{
		Name:    "stewreads",
		Command: "stewreads-mcp",
		Args:    []string{"--stdio"},
		Enabled: true,
		Clients: []string{"claude-desktop"},
		Env: map[string]string{
			"STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml",
		},
		RequiredEnv: RequiredEnv{Keys: []string{"STEWREADS_GMAIL_APP_PASSWORD"}},
	}
}

func TestManifestValidateOK(t *testing.T) {
	m := baseManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("expected manifest to validate, got error: %v", err)
	}
}

func TestManifestValidateAllowsDotsInName(t *testing.T) {
	m := baseManifest()
	m.Name = "awslabs.core-mcp-server"
	if err := m.Validate(); err != nil {
		t.Fatalf("expected dotted name to validate, got error: %v", err)
	}
}

func TestManifestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		expects string
	}{
		{
			name: "invalid name",
			mutate: func(m *Manifest) {
				m.Name = "StewReads"
			},
			expects: "name must match",
		},
		{
			name: "missing command",
			mutate: func(m *Manifest) {
				m.Command = " "
			},
			expects: "command is required",
		},
		{
			name: "duplicate clients",
			mutate: func(m *Manifest) {
				m.Clients = []string{"claude-desktop", "claude-desktop"}
			},
			expects: "duplicate client",
		},
		{
			name: "invalid env key",
			mutate: func(m *Manifest) {
				m.Env = map[string]string{"stewreads": "x"}
			},
			expects: "invalid env key",
		},
		{
			name: "duplicate required_env keys",
			mutate: func(m *Manifest) {
				m.RequiredEnv.Keys = []string{"FOO", "FOO"}
			},
			expects: "duplicate required_env key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := baseManifest()
			tt.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
	}
}

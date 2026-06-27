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
			name: "invalid transport",
			mutate: func(m *Manifest) {
				m.Transport = "websocket"
			},
			expects: "transport must be one of",
		},
		{
			name: "stdio rejects url",
			mutate: func(m *Manifest) {
				m.URL = "https://example.com/mcp"
			},
			expects: "url is only supported",
		},
		{
			name: "remote missing url",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "url is required",
		},
		{
			name: "remote rejects command",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp",
					Command:   "mcp-server-cloud-sql",
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "command is not supported",
		},
		{
			name: "remote rejects args",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp",
					Args:      []string{"--stdio"},
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "args are not supported",
		},
		{
			name: "remote requires http url",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "file:///tmp/mcp",
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "url must use http or https",
		},
		{
			name: "remote rejects invalid header name",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp",
					Headers:   map[string]string{"Bad Header": "value"},
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "invalid header name",
		},
		{
			name: "remote rejects negative timeout",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp",
					TimeoutMS: -1,
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "timeout_ms must be positive",
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
		{
			name: "duplicate secret_env keys",
			mutate: func(m *Manifest) {
				m.SecretEnv.Keys = []string{"FOO", "FOO"}
			},
			expects: "duplicate secret_env key",
		},
		{
			name: "invalid secret_env key",
			mutate: func(m *Manifest) {
				m.SecretEnv.Keys = []string{"not-valid"}
			},
			expects: "invalid secret_env key",
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

func TestManifestValidateRemoteOK(t *testing.T) {
	m := Manifest{
		Name:          "cloud-sql",
		Transport:     TransportHTTP,
		URL:           "https://sqladmin.googleapis.com/mcp",
		OAuthResource: "https://sqladmin.googleapis.com/",
		TimeoutMS:     30000,
		Headers:       map[string]string{"x-goog-user-project": "project-id"},
		Enabled:       true,
		Clients:       []string{"codex"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected remote manifest to validate, got: %v", err)
	}
	if !m.IsRemote() {
		t.Fatalf("expected remote manifest to report IsRemote")
	}
}

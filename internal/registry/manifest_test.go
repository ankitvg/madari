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
			name: "remote rejects header name that cannot round-trip",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp",
					Headers:   map[string]string{"X#Trace": "1"},
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "invalid header name",
		},
		{
			name: "remote rejects url with surrounding whitespace",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:      "cloud-sql",
					Transport: TransportHTTP,
					URL:       "https://sqladmin.googleapis.com/mcp ",
					Enabled:   true,
					Clients:   []string{"codex"},
				}
			},
			expects: "url must not have leading or trailing whitespace",
		},
		{
			name: "stdio rejects secret_headers",
			mutate: func(m *Manifest) {
				m.SecretHeaders.Keys = []string{"x-routing-key"}
			},
			expects: "secret_headers is only supported for remote transports",
		},
		{
			name: "stdio rejects bearer_token_env_var",
			mutate: func(m *Manifest) {
				m.BearerTokenEnvVar = "CLOUDSQL_MCP_TOKEN"
			},
			expects: "bearer_token_env_var is only supported for remote transports",
		},
		{
			name: "remote rejects invalid secret_headers name",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:          "cloud-sql",
					Transport:     TransportHTTP,
					URL:           "https://sqladmin.googleapis.com/mcp",
					SecretHeaders: SecretHeaders{Keys: []string{"bad header"}},
					Enabled:       true,
					Clients:       []string{"codex"},
				}
			},
			expects: "invalid secret_headers name",
		},
		{
			name: "remote rejects duplicate secret_headers names",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:          "cloud-sql",
					Transport:     TransportHTTP,
					URL:           "https://sqladmin.googleapis.com/mcp",
					SecretHeaders: SecretHeaders{Keys: []string{"x-routing-key", "X-Routing-Key"}},
					Enabled:       true,
					Clients:       []string{"codex"},
				}
			},
			expects: "duplicate secret_headers name",
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
			name: "remote rejects invalid bearer_token_env_var",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:              "cloud-sql",
					Transport:         TransportHTTP,
					URL:               "https://sqladmin.googleapis.com/mcp",
					BearerTokenEnvVar: "cloudsql-token",
					Enabled:           true,
					Clients:           []string{"codex"},
				}
			},
			expects: "invalid bearer_token_env_var key",
		},
		{
			name: "remote rejects padded bearer_token_env_var",
			mutate: func(m *Manifest) {
				*m = Manifest{
					Name:              "cloud-sql",
					Transport:         TransportHTTP,
					URL:               "https://sqladmin.googleapis.com/mcp",
					BearerTokenEnvVar: " CLOUDSQL_MCP_TOKEN ",
					Enabled:           true,
					Clients:           []string{"codex"},
				}
			},
			expects: "bearer_token_env_var must not have leading or trailing whitespace",
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
		Name:              "cloud-sql",
		Transport:         TransportHTTP,
		URL:               "https://sqladmin.googleapis.com/mcp",
		OAuthResource:     "https://sqladmin.googleapis.com/",
		BearerTokenEnvVar: "CLOUDSQL_MCP_TOKEN",
		TimeoutMS:         30000,
		Headers:           map[string]string{"x-goog-user-project": "project-id"},
		Enabled:           true,
		Clients:           []string{"codex"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected remote manifest to validate, got: %v", err)
	}
	if !m.IsRemote() {
		t.Fatalf("expected remote manifest to report IsRemote")
	}
}

func TestIsSecretHeaderName(t *testing.T) {
	secret := []string{
		"Authorization", "authorization", "Proxy-Authorization", "Cookie",
		"X-Api-Key", "api-key", "ApiKey", "X-Auth-Token", "X-Goog-Api-Key",
		"Session-Secret", "X-Access-Token",
		"X_API_KEY", "X-API_KEY", "Api_Key", "ACCESS_TOKEN",
	}
	for _, name := range secret {
		if !IsSecretHeaderName(name) {
			t.Fatalf("expected %q to be a secret header name", name)
		}
	}
	public := []string{"x-goog-user-project", "X-Figma-Region", "Accept", "Content-Type", "X-Request-Id"}
	for _, name := range public {
		if IsSecretHeaderName(name) {
			t.Fatalf("expected %q to be a non-secret header name", name)
		}
	}
}

func TestManifestSecretHeaderDetection(t *testing.T) {
	m := Manifest{
		Name:      "cloud-sql",
		Transport: TransportHTTP,
		URL:       "https://example.com/mcp",
		Headers:   map[string]string{"x-goog-user-project": "project-id"},
		Enabled:   true,
		Clients:   []string{"claude-code"},
	}
	if m.HasSecretValue() {
		t.Fatalf("expected routing-only headers to be non-secret")
	}

	m.Headers["Authorization"] = "Bearer token"
	if !m.HasSecretValue() {
		t.Fatalf("expected built-in credential header to be secret without annotation")
	}
	if names := m.SecretHeaderNames(); len(names) != 1 || names[0] != "Authorization" {
		t.Fatalf("expected Authorization as the secret header, got: %#v", names)
	}

	delete(m.Headers, "Authorization")
	m.Headers["x-routing-key"] = "internal-value"
	m.SecretHeaders.Keys = []string{"X-Routing-Key"}
	if !m.HasSecretValue() {
		t.Fatalf("expected explicitly marked header to be secret (case-insensitive)")
	}
}

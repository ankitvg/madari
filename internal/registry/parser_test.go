package registry

import (
	"strings"
	"testing"
)

func TestParseManifestRejectsUnknownTopLevelKey(t *testing.T) {
	manifest := `
name = "stewreads"
command = "stewreads-mcp"
args = []
enabled = true
clients = ["claude-desktop"]
unknown = "value"
`

	_, err := ParseManifest([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown top-level key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAndMarshalManifestRoundTrip(t *testing.T) {
	in := Manifest{
		Name:        "stewreads",
		Command:     "stewreads-mcp",
		Args:        []string{"--stdio"},
		Enabled:     true,
		Clients:     []string{"claude-desktop"},
		Description: "Turn chats into ebooks",
		Env: map[string]string{
			"STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml",
		},
		RequiredEnv: RequiredEnv{Keys: []string{"STEWREADS_GMAIL_APP_PASSWORD"}},
	}

	encoded, err := MarshalManifest(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	out, err := ParseManifest(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if out.Name != in.Name || out.Command != in.Command || out.Enabled != in.Enabled {
		t.Fatalf("roundtrip mismatch: %#v vs %#v", in, out)
	}
	if len(out.Clients) != 1 || out.Clients[0] != "claude-desktop" {
		t.Fatalf("unexpected clients: %#v", out.Clients)
	}
	if out.Env["STEWREADS_CONFIG_PATH"] == "" {
		t.Fatalf("expected env value to survive roundtrip")
	}
}

func TestParseManifestRejectsUnknownSection(t *testing.T) {
	manifest := `
name = "stewreads"
command = "stewreads-mcp"
args = []
enabled = true
clients = ["claude-desktop"]

[weird]
foo = "bar"
`

	_, err := ParseManifest([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "unknown section") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseManifestInlineCommentsPreserveQuotedHashes(t *testing.T) {
	manifest := `
name = "stewreads" # service id
command = "/usr/local/bin/stewreads-mcp" # executable path
args = ["--stdio"] # transport arg
enabled = true # enabled by default
clients = ["claude-desktop"] # one client for now
description = "works with #hashtags" # inline comment after quoted hash
`

	got, err := ParseManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("expected parse success, got: %v", err)
	}
	if got.Description != "works with #hashtags" {
		t.Fatalf("expected quoted hash to be preserved, got: %q", got.Description)
	}
}

func TestParseManifestRejectsUnknownRequiredEnvKey(t *testing.T) {
	manifest := `
name = "stewreads"
command = "/usr/local/bin/stewreads-mcp"
args = []
enabled = true
clients = ["claude-desktop"]

[required_env]
unexpected = ["MISSING_KEY"]
`

	_, err := ParseManifest([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown required_env key")
	}
	if !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "[required_env]") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseManifestRejectsMalformedStringArrays(t *testing.T) {
	tests := []struct {
		name           string
		clientsLine    string
		expectedErrSub string
	}{
		{
			name:           "missing comma",
			clientsLine:    `clients = ["claude-desktop" "claude-code"]`,
			expectedErrSub: "expected comma between array values",
		},
		{
			name:           "unquoted value",
			clientsLine:    `clients = [claude-desktop]`,
			expectedErrSub: "array values must be quoted strings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := `
name = "stewreads"
command = "/usr/local/bin/stewreads-mcp"
args = []
enabled = true
` + tt.clientsLine + `
`

			_, err := ParseManifest([]byte(manifest))
			if err == nil {
				t.Fatalf("expected parse error")
			}
			if !strings.Contains(err.Error(), tt.expectedErrSub) {
				t.Fatalf("expected error containing %q, got: %v", tt.expectedErrSub, err)
			}
		})
	}
}

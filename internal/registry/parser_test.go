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

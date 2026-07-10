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
	if out.TransportType() != TransportStdio {
		t.Fatalf("expected legacy manifest to default to stdio, got: %q", out.TransportType())
	}
	if strings.Contains(string(encoded), "transport") {
		t.Fatalf("expected stdio manifest to omit default transport, got:\n%s", encoded)
	}
}

func TestParseAndMarshalRemoteManifestRoundTrip(t *testing.T) {
	in := Manifest{
		Name:              "cloud-sql",
		Transport:         TransportHTTP,
		URL:               "https://sqladmin.googleapis.com/mcp",
		TimeoutMS:         30000,
		OAuthResource:     "https://sqladmin.googleapis.com/",
		BearerTokenEnvVar: "CLOUDSQL_MCP_TOKEN",
		Enabled:           true,
		Clients:           []string{"codex"},
		Description:       "Cloud SQL remote MCP",
		Headers: map[string]string{
			"x-goog-user-project": "example-project",
			"x-routing-key":       "internal-value",
		},
		SecretHeaders: SecretHeaders{Keys: []string{"x-routing-key"}},
	}

	encoded, err := MarshalManifest(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, want := range []string{
		`transport = "http"`,
		`url = "https://sqladmin.googleapis.com/mcp"`,
		`timeout_ms = 30000`,
		`oauth_resource = "https://sqladmin.googleapis.com/"`,
		`bearer_token_env_var = "CLOUDSQL_MCP_TOKEN"`,
		"[headers]",
		`x-goog-user-project = "example-project"`,
		"[secret_headers]",
		`keys = ["x-routing-key"]`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("expected encoded manifest to contain %q, got:\n%s", want, encoded)
		}
	}
	if strings.Contains(string(encoded), "command") {
		t.Fatalf("expected remote manifest to omit command, got:\n%s", encoded)
	}

	out, err := ParseManifest(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if out.TransportType() != TransportHTTP || out.URL != in.URL ||
		out.OAuthResource != in.OAuthResource ||
		out.BearerTokenEnvVar != in.BearerTokenEnvVar {
		t.Fatalf("remote roundtrip mismatch: got %#v", out)
	}
	if out.Headers["x-goog-user-project"] != "example-project" {
		t.Fatalf("expected headers to survive roundtrip, got: %#v", out.Headers)
	}
	if len(out.SecretHeaders.Keys) != 1 || out.SecretHeaders.Keys[0] != "x-routing-key" {
		t.Fatalf("expected secret_headers to survive roundtrip, got: %#v", out.SecretHeaders)
	}
}

func TestParseAndMarshalAccessProfileRoundTrip(t *testing.T) {
	payload := `name = "remote-tools"
transport = "http"
url = "https://example.com/mcp"
enabled = true
clients = ["codex"]

[access]
allowed_tools = ["repos.search", "issues.read"]
denied_tools = ["issues.delete"]
oauth_scopes = ["repo.read", "openid"]
default_approval = "always-prompt"

[access.tool_approvals]
"repos.search" = "automatic"
"issues.read" = "always-allow"
`

	manifest, err := ParseManifest([]byte(payload))
	if err != nil {
		t.Fatalf("parse access profile: %v", err)
	}
	if manifest.Access == nil || manifest.Access.AllowedTools == nil || manifest.Access.ToolApprovals == nil {
		t.Fatalf("expected access profile presence to survive parse: %#v", manifest.Access)
	}
	if got := (*manifest.Access.ToolApprovals)["issues.read"]; got != ApprovalBehaviorAlwaysAllow {
		t.Fatalf("unexpected dotted-tool approval: %q", got)
	}

	encoded, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal access profile: %v", err)
	}
	wantAccess := `[access]
allowed_tools = ["issues.read", "repos.search"]
denied_tools = ["issues.delete"]
oauth_scopes = ["openid", "repo.read"]
default_approval = "always-prompt"

[access.tool_approvals]
"issues.read" = "always-allow"
"repos.search" = "automatic"
`
	if !strings.Contains(string(encoded), wantAccess) {
		t.Fatalf("expected deterministic access profile:\n%s\ngot:\n%s", wantAccess, encoded)
	}

	roundTripped, err := ParseManifest(encoded)
	if err != nil {
		t.Fatalf("parse marshaled access profile: %v", err)
	}
	if !accessProfilesEqual(manifest.Access, roundTripped.Access) {
		t.Fatalf("access profile roundtrip mismatch: %#v vs %#v", manifest.Access, roundTripped.Access)
	}
}

func TestParseAndMarshalAccessExplicitClears(t *testing.T) {
	payload := `name = "tools"
command = "/usr/bin/env"
args = []
enabled = true
clients = ["codex"]

[access]
allowed_tools = []
denied_tools = []
oauth_scopes = []

[access.tool_approvals]
`
	manifest, err := ParseManifest([]byte(payload))
	if err != nil {
		t.Fatalf("parse explicit access clears: %v", err)
	}
	if manifest.Access == nil || manifest.Access.AllowedTools == nil || manifest.Access.DeniedTools == nil || manifest.Access.OAuthScopes == nil || manifest.Access.ToolApprovals == nil {
		t.Fatalf("expected every explicit clear to remain present: %#v", manifest.Access)
	}
	if manifest.HasExplicitToolAllowlist() {
		t.Fatalf("empty allowed_tools is a clear, not a bounded allowlist")
	}

	encoded, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal explicit access clears: %v", err)
	}
	for _, want := range []string{"allowed_tools = []", "denied_tools = []", "oauth_scopes = []", "[access.tool_approvals]"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("expected %q in marshaled clears:\n%s", want, encoded)
		}
	}
	roundTripped, err := ParseManifest(encoded)
	if err != nil {
		t.Fatalf("parse marshaled clears: %v", err)
	}
	if !accessProfilesEqual(manifest.Access, roundTripped.Access) {
		t.Fatalf("explicit clear presence changed after roundtrip")
	}
}

func TestParseManifestRejectsInvalidAccessSyntax(t *testing.T) {
	base := `name = "tools"
command = "/usr/bin/env"
args = []
enabled = true
clients = ["codex"]
`
	tests := []struct {
		name    string
		extra   string
		expects string
	}{
		{name: "empty access", extra: "\n[access]\n", expects: "access must declare at least one field"},
		{name: "unknown access key", extra: "\n[access]\nunknown = []\n", expects: `unknown key "unknown" in [access]`},
		{name: "unknown nested section", extra: "\n[access.unknown]\nvalue = \"x\"\n", expects: "unknown section"},
		{name: "duplicate access key", extra: "\n[access]\nallowed_tools = [\"read\"]\nallowed_tools = [\"search\"]\n", expects: `duplicate key "allowed_tools"`},
		{name: "duplicate access section", extra: "\n[access]\nallowed_tools = [\"read\"]\n[access]\ndenied_tools = []\n", expects: "duplicate section"},
		{name: "duplicate approvals section", extra: "\n[access.tool_approvals]\n\"read\" = \"automatic\"\n[access.tool_approvals]\n", expects: "duplicate section"},
		{name: "duplicate approval tool", extra: "\n[access.tool_approvals]\n\"read\" = \"automatic\"\n\"read\" = \"always-prompt\"\n", expects: `duplicate tool_approvals tool "read"`},
		{name: "malformed quoted tool", extra: "\n[access.tool_approvals]\n\"read = \"automatic\"\n", expects: "invalid tool_approvals tool name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(base + tt.extra))
			if err == nil || !strings.Contains(err.Error(), tt.expects) {
				t.Fatalf("expected error containing %q, got: %v", tt.expects, err)
			}
		})
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

func TestParseManifestSecretEnvRoundTrip(t *testing.T) {
	in := Manifest{
		Name:    "stewreads",
		Command: "stewreads-mcp",
		Args:    []string{},
		Enabled: true,
		Clients: []string{"claude-desktop"},
		Env: map[string]string{
			"STEWREADS_API_KEY": "shhh",
		},
		SecretEnv: SecretEnv{Keys: []string{"STEWREADS_API_KEY"}},
	}

	encoded, err := MarshalManifest(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(encoded), "[secret_env]") {
		t.Fatalf("expected [secret_env] section in output, got:\n%s", encoded)
	}

	out, err := ParseManifest(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(out.SecretEnv.Keys) != 1 || out.SecretEnv.Keys[0] != "STEWREADS_API_KEY" {
		t.Fatalf("expected secret_env keys to survive roundtrip, got: %#v", out.SecretEnv.Keys)
	}
	if !out.HasSecretValue() {
		t.Fatalf("expected HasSecretValue for secret key with static env value")
	}
}

func TestParseManifestRejectsUnknownSecretEnvKey(t *testing.T) {
	manifest := `
name = "stewreads"
command = "/usr/local/bin/stewreads-mcp"
args = []
enabled = true
clients = ["claude-desktop"]

[secret_env]
unexpected = ["STEWREADS_API_KEY"]
`

	_, err := ParseManifest([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for unknown secret_env key")
	}
	if !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "[secret_env]") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseManifestRejectsUnknownHeadersValue(t *testing.T) {
	manifest := `
name = "cloud-sql"
transport = "http"
url = "https://sqladmin.googleapis.com/mcp"
enabled = true
clients = ["codex"]

[headers]
x-goog-user-project = 123
`

	_, err := ParseManifest([]byte(manifest))
	if err == nil {
		t.Fatalf("expected parse error for non-string header value")
	}
	if !strings.Contains(err.Error(), "invalid header value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHasSecretValueTrimsPaddedKeys(t *testing.T) {
	manifest := Manifest{
		Name:    "stewreads",
		Command: "stewreads-mcp",
		Enabled: true,
		Clients: []string{"claude-code"},
		Env: map[string]string{
			"STEWREADS_API_KEY": "shhh",
		},
		SecretEnv: SecretEnv{Keys: []string{" STEWREADS_API_KEY "}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("expected padded secret_env key to validate, got: %v", err)
	}
	if !manifest.HasSecretValue() {
		t.Fatalf("expected padded secret_env key to match its env value")
	}
}

func TestHasSecretValueFalseWithoutStaticValue(t *testing.T) {
	manifest := Manifest{
		Name:      "stewreads",
		Command:   "stewreads-mcp",
		Enabled:   true,
		Clients:   []string{"claude-desktop"},
		SecretEnv: SecretEnv{Keys: []string{"STEWREADS_API_KEY"}},
	}
	if manifest.HasSecretValue() {
		t.Fatalf("expected no secret value when [env] carries none")
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

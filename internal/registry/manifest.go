package registry

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	manifestNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	envKeyPattern       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// Manifest is the canonical configuration for one local MCP server.
type Manifest struct {
	Name          string            `toml:"name" json:"name"`
	Transport     string            `toml:"transport,omitempty" json:"transport,omitempty"`
	Command       string            `toml:"command" json:"command"`
	Args          []string          `toml:"args" json:"args"`
	URL           string            `toml:"url,omitempty" json:"url,omitempty"`
	Headers       map[string]string `toml:"headers,omitempty" json:"headers,omitempty"`
	TimeoutMS     int               `toml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	OAuthResource string            `toml:"oauth_resource,omitempty" json:"oauth_resource,omitempty"`
	Enabled       bool              `toml:"enabled" json:"enabled"`
	Clients       []string          `toml:"clients" json:"clients"`
	Description   string            `toml:"description,omitempty" json:"description,omitempty"`
	Env           map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	RequiredEnv   RequiredEnv       `toml:"required_env,omitempty" json:"required_env,omitempty"`
	SecretEnv     SecretEnv         `toml:"secret_env,omitempty" json:"secret_env,omitempty"`
}

// RequiredEnv describes environment variables that must be present at runtime.
type RequiredEnv struct {
	Keys []string `toml:"keys,omitempty" json:"keys,omitempty"`
}

// SecretEnv marks environment variable keys whose values are secrets and must
// never be materialized into repo-scoped client configs.
type SecretEnv struct {
	Keys []string `toml:"keys,omitempty" json:"keys,omitempty"`
}

// HasSecretValue reports whether the manifest carries a static env value for
// any key marked secret — the case placement policy must guard against.
// Keys are trimmed to match Validate, which accepts padded secret_env
// entries; an untrimmed lookup here would fail open and leak the value.
func (m Manifest) HasSecretValue() bool {
	for _, key := range m.SecretEnv.Keys {
		if _, exists := m.Env[strings.TrimSpace(key)]; exists {
			return true
		}
	}
	return false
}

// HasClient reports whether target appears in the manifest's client list.
// Comparison is case-insensitive and trims surrounding whitespace.
func (m Manifest) HasClient(target string) bool {
	target = strings.TrimSpace(target)
	for _, c := range m.Clients {
		if strings.EqualFold(strings.TrimSpace(c), target) {
			return true
		}
	}
	return false
}

// TransportType returns the effective transport. An empty transport preserves
// legacy manifests and means stdio.
func (m Manifest) TransportType() string {
	transport := strings.TrimSpace(strings.ToLower(m.Transport))
	if transport == "" {
		return TransportStdio
	}
	return transport
}

func (m Manifest) IsRemote() bool {
	switch m.TransportType() {
	case TransportHTTP, TransportSSE:
		return true
	default:
		return false
	}
}

// Validate enforces manifest-level invariants.
func (m Manifest) Validate() error {
	var errs []string

	if err := validateServerName(m.Name); err != nil {
		errs = append(errs, err.Error())
	}

	transport := m.TransportType()
	switch transport {
	case TransportStdio:
		if strings.TrimSpace(m.Command) == "" {
			errs = append(errs, "command is required")
		}
		if strings.TrimSpace(m.URL) != "" {
			errs = append(errs, "url is only supported for remote transports")
		}
		if len(m.Headers) > 0 {
			errs = append(errs, "headers are only supported for remote transports")
		}
		if m.TimeoutMS != 0 {
			errs = append(errs, "timeout_ms is only supported for remote transports")
		}
		if strings.TrimSpace(m.OAuthResource) != "" {
			errs = append(errs, "oauth_resource is only supported for remote transports")
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(m.URL) == "" {
			errs = append(errs, "url is required for remote transports")
		} else if m.URL != strings.TrimSpace(m.URL) {
			errs = append(errs, "url must not have leading or trailing whitespace")
		} else if err := validateRemoteURL(m.URL); err != nil {
			errs = append(errs, err.Error())
		}
		if strings.TrimSpace(m.Command) != "" {
			errs = append(errs, "command is not supported for remote transports")
		}
		if len(m.Args) > 0 {
			errs = append(errs, "args are not supported for remote transports")
		}
		if len(m.Env) > 0 {
			errs = append(errs, "env is not supported for remote transports")
		}
		if len(m.RequiredEnv.Keys) > 0 {
			errs = append(errs, "required_env is not supported for remote transports")
		}
		if len(m.SecretEnv.Keys) > 0 {
			errs = append(errs, "secret_env is not supported for remote transports")
		}
	default:
		errs = append(errs, fmt.Sprintf("transport must be one of %q, %q, or %q", TransportStdio, TransportHTTP, TransportSSE))
	}

	if m.TimeoutMS < 0 {
		errs = append(errs, "timeout_ms must be positive")
	}

	for key := range m.Headers {
		if !validHeaderName(key) {
			errs = append(errs, fmt.Sprintf("invalid header name %q (allowed: letters, digits, '-', '_')", key))
		}
	}

	if len(m.Clients) == 0 {
		errs = append(errs, "at least one client is required")
	}

	seenClients := map[string]struct{}{}
	for _, client := range m.Clients {
		client = strings.TrimSpace(client)
		if client == "" {
			errs = append(errs, "client values must be non-empty")
			continue
		}
		if _, exists := seenClients[client]; exists {
			errs = append(errs, fmt.Sprintf("duplicate client %q", client))
			continue
		}
		seenClients[client] = struct{}{}
	}

	for _, arg := range m.Args {
		if arg == "" {
			errs = append(errs, "args cannot contain empty values")
			break
		}
	}

	for key := range m.Env {
		if !envKeyPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("invalid env key %q", key))
		}
	}

	seenRequired := map[string]struct{}{}
	for _, key := range m.RequiredEnv.Keys {
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("invalid required_env key %q", key))
			continue
		}
		if _, exists := seenRequired[key]; exists {
			errs = append(errs, fmt.Sprintf("duplicate required_env key %q", key))
			continue
		}
		seenRequired[key] = struct{}{}
	}

	seenSecret := map[string]struct{}{}
	for _, key := range m.SecretEnv.Keys {
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("invalid secret_env key %q", key))
			continue
		}
		if _, exists := seenSecret[key]; exists {
			errs = append(errs, fmt.Sprintf("duplicate secret_env key %q", key))
			continue
		}
		seenSecret[key] = struct{}{}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid manifest: %s", strings.Join(errs, "; "))
	}
	return nil
}

func validateServerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !manifestNamePattern.MatchString(name) {
		return fmt.Errorf("name must match %q", manifestNamePattern.String())
	}
	return nil
}

func validateRemoteURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

// validHeaderName restricts header names to characters that round-trip as raw
// manifest keys; the plain-text parser treats '#', '=', quotes, and brackets
// specially, so anything outside this set could not be reloaded.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

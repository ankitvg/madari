package registry

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
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

// ApprovalBehavior is Madari's portable vocabulary for client-side MCP tool
// approval behavior. Client adapters map these values to their native enums;
// the native values are deliberately not part of the registry contract.
type ApprovalBehavior string

const (
	ApprovalBehaviorInherit      ApprovalBehavior = "inherit"
	ApprovalBehaviorAutomatic    ApprovalBehavior = "automatic"
	ApprovalBehaviorAlwaysPrompt ApprovalBehavior = "always-prompt"
	ApprovalBehaviorAlwaysAllow  ApprovalBehavior = "always-allow"
)

// AccessProfile describes the access restrictions Madari should compile for
// one MCP server. Pointer fields preserve the distinction between an absent
// declaration (leave the target-native value untouched) and an explicit empty
// declaration (clear the target-native override).
type AccessProfile struct {
	AllowedTools    *[]string                    `toml:"allowed_tools,omitempty" json:"allowed_tools,omitempty"`
	DeniedTools     *[]string                    `toml:"denied_tools,omitempty" json:"denied_tools,omitempty"`
	OAuthScopes     *[]string                    `toml:"oauth_scopes,omitempty" json:"oauth_scopes,omitempty"`
	DefaultApproval *ApprovalBehavior            `toml:"default_approval,omitempty" json:"default_approval,omitempty"`
	ToolApprovals   *map[string]ApprovalBehavior `toml:"tool_approvals,omitempty" json:"tool_approvals,omitempty"`
}

// Manifest is the canonical configuration for one local MCP server.
type Manifest struct {
	Name              string            `toml:"name" json:"name"`
	Transport         string            `toml:"transport,omitempty" json:"transport,omitempty"`
	Command           string            `toml:"command" json:"command"`
	Args              []string          `toml:"args" json:"args"`
	URL               string            `toml:"url,omitempty" json:"url,omitempty"`
	Headers           map[string]string `toml:"headers,omitempty" json:"headers,omitempty"`
	TimeoutMS         int               `toml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	OAuthResource     string            `toml:"oauth_resource,omitempty" json:"oauth_resource,omitempty"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var,omitempty" json:"bearer_token_env_var,omitempty"`
	Enabled           bool              `toml:"enabled" json:"enabled"`
	Clients           []string          `toml:"clients" json:"clients"`
	Description       string            `toml:"description,omitempty" json:"description,omitempty"`
	Env               map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	RequiredEnv       RequiredEnv       `toml:"required_env,omitempty" json:"required_env,omitempty"`
	SecretEnv         SecretEnv         `toml:"secret_env,omitempty" json:"secret_env,omitempty"`
	SecretHeaders     SecretHeaders     `toml:"secret_headers,omitempty" json:"secret_headers,omitempty"`
	Access            *AccessProfile    `toml:"access,omitempty" json:"access,omitempty"`
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

// SecretHeaders marks remote header names whose values are secrets and must
// never be materialized into repo-scoped client configs. Well-known
// credential headers (see IsSecretHeaderName) are treated as secret even
// when not listed here.
type SecretHeaders struct {
	Keys []string `toml:"keys,omitempty" json:"keys,omitempty"`
}

// secretHeaderNameExact lists headers that carry credentials by definition,
// compared case-insensitively. Values for these names are refused in
// repo-scoped configs even without a [secret_headers] entry, so a forgotten
// annotation cannot commit a token.
var secretHeaderNameExact = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"api-key":             true,
	"apikey":              true,
	"x-api-key":           true,
}

// secretHeaderNameSubstrings extends the exact list to conventional
// credential naming (x-auth-token, x-goog-api-key, session-secret, ...).
var secretHeaderNameSubstrings = []string{"token", "secret", "api-key", "apikey"}

// IsSecretHeaderName reports whether a header name is treated as a
// credential by default. Underscores are folded to hyphens before matching
// so variants like X_API_KEY cannot slip past the detector.
func IsSecretHeaderName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	if secretHeaderNameExact[name] {
		return true
	}
	for _, fragment := range secretHeaderNameSubstrings {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}

// HasSecretValue reports whether the manifest carries a static value the
// placement policy must keep out of repo-scoped configs: an env value for a
// key marked secret, or a remote header value whose name is marked secret or
// is a well-known credential header. Keys are trimmed to match Validate,
// which accepts padded entries; an untrimmed lookup here would fail open and
// leak the value.
func (m Manifest) HasSecretValue() bool {
	for _, key := range m.SecretEnv.Keys {
		if _, exists := m.Env[strings.TrimSpace(key)]; exists {
			return true
		}
	}
	for name := range m.Headers {
		if m.isSecretHeader(name) {
			return true
		}
	}
	return false
}

// SecretHeaderNames returns the manifest's header names whose values must
// not land in repo-scoped configs, sorted for stable output.
func (m Manifest) SecretHeaderNames() []string {
	var names []string
	for name := range m.Headers {
		if m.isSecretHeader(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m Manifest) isSecretHeader(name string) bool {
	if IsSecretHeaderName(name) {
		return true
	}
	for _, key := range m.SecretHeaders.Keys {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(name)) {
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

func (m Manifest) RequiresBearerTokenEnv() bool {
	return strings.TrimSpace(m.BearerTokenEnvVar) != ""
}

// HasExplicitToolAllowlist reports whether the manifest declares a non-empty
// exact tool allowlist. Policy-required rings use this as their bounded-access
// prerequisite; an explicitly empty allowlist clears a native override and is
// therefore unbounded.
func (m Manifest) HasExplicitToolAllowlist() bool {
	return m.Access != nil && m.Access.AllowedTools != nil && len(*m.Access.AllowedTools) > 0
}

func (a *AccessProfile) Validate() error {
	if a == nil {
		return nil
	}
	if a.AllowedTools == nil && a.DeniedTools == nil && a.OAuthScopes == nil && a.DefaultApproval == nil && a.ToolApprovals == nil {
		return fmt.Errorf("access must declare at least one field")
	}

	var errs []string
	allowed, allowedErrs := validateAccessStringSet("allowed_tools", a.AllowedTools)
	errs = append(errs, allowedErrs...)
	denied, deniedErrs := validateAccessStringSet("denied_tools", a.DeniedTools)
	errs = append(errs, deniedErrs...)
	_, scopeErrs := validateAccessStringSet("oauth_scopes", a.OAuthScopes)
	errs = append(errs, scopeErrs...)

	if a.DefaultApproval != nil && !validApprovalBehavior(*a.DefaultApproval) {
		errs = append(errs, fmt.Sprintf("invalid default_approval %q (supported: %s)", *a.DefaultApproval, supportedApprovalBehaviors()))
	}

	for tool := range allowed {
		if _, exists := denied[tool]; exists {
			errs = append(errs, fmt.Sprintf("tool %q cannot appear in both allowed_tools and denied_tools", tool))
		}
	}

	if a.ToolApprovals != nil {
		tools := make([]string, 0, len(*a.ToolApprovals))
		for tool := range *a.ToolApprovals {
			tools = append(tools, tool)
		}
		sort.Strings(tools)
		for _, tool := range tools {
			approval := (*a.ToolApprovals)[tool]
			trimmed := strings.TrimSpace(tool)
			switch {
			case trimmed == "":
				errs = append(errs, "tool_approvals names must be non-empty")
			case tool != trimmed:
				errs = append(errs, fmt.Sprintf("tool_approvals name %q must not have leading or trailing whitespace", tool))
			}
			if !validApprovalBehavior(approval) {
				errs = append(errs, fmt.Sprintf("invalid approval %q for tool %q (supported: %s)", approval, tool, supportedApprovalBehaviors()))
			}
			if _, exists := denied[tool]; exists {
				errs = append(errs, fmt.Sprintf("tool_approvals cannot configure denied tool %q", tool))
			}
			if a.AllowedTools != nil && len(*a.AllowedTools) > 0 {
				if _, exists := allowed[tool]; !exists {
					errs = append(errs, fmt.Sprintf("tool_approvals tool %q is not in allowed_tools", tool))
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid access profile: %s", strings.Join(errs, "; "))
	}
	return nil
}

func validateAccessStringSet(field string, values *[]string) (map[string]struct{}, []string) {
	seen := map[string]struct{}{}
	if values == nil {
		return seen, nil
	}
	var errs []string
	for _, value := range *values {
		trimmed := strings.TrimSpace(value)
		switch {
		case trimmed == "":
			errs = append(errs, fmt.Sprintf("%s values must be non-empty", field))
			continue
		case value != trimmed:
			errs = append(errs, fmt.Sprintf("%s value %q must not have leading or trailing whitespace", field, value))
			continue
		}
		if _, exists := seen[value]; exists {
			errs = append(errs, fmt.Sprintf("duplicate %s value %q", field, value))
			continue
		}
		seen[value] = struct{}{}
	}
	return seen, errs
}

func validApprovalBehavior(value ApprovalBehavior) bool {
	switch value {
	case ApprovalBehaviorInherit, ApprovalBehaviorAutomatic, ApprovalBehaviorAlwaysPrompt, ApprovalBehaviorAlwaysAllow:
		return true
	default:
		return false
	}
}

func supportedApprovalBehaviors() string {
	return fmt.Sprintf("%q, %q, %q, or %q", ApprovalBehaviorInherit, ApprovalBehaviorAutomatic, ApprovalBehaviorAlwaysPrompt, ApprovalBehaviorAlwaysAllow)
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
		if strings.TrimSpace(m.BearerTokenEnvVar) != "" {
			errs = append(errs, "bearer_token_env_var is only supported for remote transports")
		}
		if len(m.SecretHeaders.Keys) > 0 {
			errs = append(errs, "secret_headers is only supported for remote transports")
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

	if key := strings.TrimSpace(m.BearerTokenEnvVar); key != "" {
		if m.BearerTokenEnvVar != key {
			errs = append(errs, "bearer_token_env_var must not have leading or trailing whitespace")
		} else if !envKeyPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("invalid bearer_token_env_var key %q", key))
		}
	}

	if err := m.Access.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	if !m.IsRemote() && m.Access != nil && m.Access.OAuthScopes != nil && len(*m.Access.OAuthScopes) > 0 {
		errs = append(errs, "oauth_scopes requires a remote transport with an OAuth client flow")
	}

	for key := range m.Headers {
		if !validHeaderName(key) {
			errs = append(errs, fmt.Sprintf("invalid header name %q (allowed: letters, digits, '-', '_')", key))
		}
	}

	seenSecretHeaders := map[string]struct{}{}
	for _, key := range m.SecretHeaders.Keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if !validHeaderName(key) {
			errs = append(errs, fmt.Sprintf("invalid secret_headers name %q (allowed: letters, digits, '-', '_')", key))
			continue
		}
		if _, exists := seenSecretHeaders[key]; exists {
			errs = append(errs, fmt.Sprintf("duplicate secret_headers name %q", key))
			continue
		}
		seenSecretHeaders[key] = struct{}{}
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

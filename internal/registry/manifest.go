package registry

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	manifestNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	envKeyPattern       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Manifest is the canonical configuration for one local MCP server.
type Manifest struct {
	Name        string            `toml:"name" json:"name"`
	Command     string            `toml:"command" json:"command"`
	Args        []string          `toml:"args" json:"args"`
	Enabled     bool              `toml:"enabled" json:"enabled"`
	Clients     []string          `toml:"clients" json:"clients"`
	Description string            `toml:"description,omitempty" json:"description,omitempty"`
	Env         map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	RequiredEnv RequiredEnv       `toml:"required_env,omitempty" json:"required_env,omitempty"`
}

// RequiredEnv describes environment variables that must be present at runtime.
type RequiredEnv struct {
	Keys []string `toml:"keys,omitempty" json:"keys,omitempty"`
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

// Validate enforces manifest-level invariants.
func (m Manifest) Validate() error {
	var errs []string

	if err := validateServerName(m.Name); err != nil {
		errs = append(errs, err.Error())
	}

	if strings.TrimSpace(m.Command) == "" {
		errs = append(errs, "command is required")
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

package codex

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

//go:embed compat.toml
var compatTOML []byte

// Compat lists the Codex CLI behavior Madari depends on: the policy-run
// version floor, the arguments `madari run codex` passes, and the config keys
// and values Madari writes. See compat.toml.
type Compat struct {
	MinimumVersion      string          `toml:"minimum_version"`
	LastVerifiedVersion string          `toml:"last_verified_version"`
	VersionFlag         string          `toml:"version_flag"`
	Exec                CompatExec      `toml:"exec"`
	MCPServer           CompatMCPServer `toml:"mcp_server"`
}

// CompatExec describes the `codex exec` invocation used by `madari run`.
type CompatExec struct {
	Subcommand      string   `toml:"subcommand"`
	Flags           []string `toml:"flags"`
	ValueFlags      []string `toml:"value_flags"`
	Sandbox         string   `toml:"sandbox"`
	ConfigOverrides []string `toml:"config_overrides"`
}

// CompatMCPServer describes the keys and values Madari writes in Codex
// mcp_servers entries.
type CompatMCPServer struct {
	Fields        []string `toml:"fields"`
	ToolFields    []string `toml:"tool_fields"`
	ApprovalModes []string `toml:"approval_modes"`
}

var compat = mustParseCompat(compatTOML)

// Compatibility returns the parsed compat.toml.
func Compatibility() Compat {
	return compat
}

func mustParseCompat(payload []byte) Compat {
	parsed, err := parseCompat(payload)
	if err != nil {
		panic(fmt.Sprintf("codex compat.toml: %v", err))
	}
	return parsed
}

func parseCompat(payload []byte) (Compat, error) {
	var parsed Compat
	decoder := toml.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return Compat{}, err
	}
	if parsed.MinimumVersion == "" || parsed.VersionFlag == "" || parsed.Exec.Subcommand == "" {
		return Compat{}, fmt.Errorf("minimum_version, version_flag, and exec.subcommand are required")
	}
	return parsed, nil
}

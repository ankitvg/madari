# madari (muh-DAA-ree)

Madari is a CLI to deploy MCP servers into your AI client setup with reliable install, registration, and sync.

## Installation

Homebrew (recommended):

```bash
brew tap ankitvg/tap
brew install madari
```

Go:

```bash
go install github.com/ankitvg/madari/cmd/madari@latest
```

## Commands

- `madari install <package> [options]`
- `madari add <name> --command <cmd> --client <client>`
- `madari list [--json]`
- `madari remove <name>`
- `madari enable <name>`
- `madari disable <name>`
- `madari sync <client> [--dry-run] [--config-path <path>] [--json] [--scope project|user]`
- `madari ring create <name> [--member <server> ...] [--skill <skill> ...] [--description <text>]`
- `madari ring list [--json]`
- `madari ring show <name> [--json]`
- `madari ring attach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]`
- `madari ring detach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]`
- `madari ring delete <name>`
- `madari ring render <name> --client <target>`
- `madari ring status [--json]`
- `madari skill add <name> --file <path> [--description <text>]`
- `madari skill update <name> --file <path> [--description <text>]`
- `madari skill remove <name>`
- `madari skill list [--json]`
- `madari skill show <name> [--json]`
- `madari skill render <name> [--client <target>]`
- `madari skill attach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]`
- `madari skill detach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]`
- `madari clients`
- `madari doctor [--client-config target=path ...] [--json]`
- `madari status [--client-config target=path ...] [--json]`
- `madari export [--file <path>]`
- `madari import --file <path> [--apply]`
- `madari help [command]`
- `madari version`

Notes:

- `install` runs package-manager install (`uv` by default, or `npm` via `--manager npm`), auto-registers the server, and syncs to configured clients in one command.
- `install` requires the selected package manager in PATH unless you use `--skip-install` and pass `--command`.
- `install --manager npm` requires `--command` because npm package names can differ from executable names.
- `add` resolves `--command` to an absolute executable path and stores that path in the manifest.
- `sync` skips servers with missing/non-executable command paths and continues syncing others.
- Manifests can mark secret env keys (`[secret_env]`, or `--secret-env` on `add`/`install`); sync refuses to write their static values into repo-scoped configs such as Claude Code `.mcp.json` and Gemini `.gemini/settings.json` — refused entries are reported with guidance (and scrubbed if previously materialized) while other servers sync normally. Use `madari sync <client> --scope user` for clients that support user scope.
- `list` shows the managed sources owning each synced entry (`standalone` today; `-` when not synced), and `status` summarizes managed entries per client.
- `list`, `status`, `doctor`, `sync --dry-run`, `ring list`, `ring show`, `ring status`, `skill list`, and `skill show` accept `--json` for machine-readable output with a versioned schema; schemas and exit codes are documented in `docs/cli-reference.md`.
- Rings are named capability sets of servers and skills (`madari ring ...`). Server members reference registry entries by name, and skill members reference managed skill entries by name.
- `ring attach` records reference-counted ownership (`ring:<name>` sources) and materializes eligible server members plus native skill files for supported skill targets; `ring detach` releases it, and an entry or skill file only leaves the client when nothing owns it anymore. Overlapping rings and standalone+ring combinations resolve by refcount, in any order. Attaching onto an entry or skill file madari does not manage is refused — even when values match. Rings containing skills cannot attach to `claude-desktop` because it is not a skill materialization target.
- `ring delete` removes only unattached ring definitions. It refuses while any client scope still records `ring:<name>` ownership and prints scoped detach guidance; deletion never edits client configs or managed state.
- `ring render` prints a self-contained MCP config to stdout for ephemeral use — `claude --mcp-config <(madari ring render research --client claude-code)` — mutating nothing; secret env values are never emitted, and ring skill members are not embedded in MCP render output. Render targets are independent from persistent sync support: Claude and Gemini emit JSON, while Codex and Vibe emit TOML. `ring status` shows attached rings plus per-server and per-skill ownership for every client and scope.
- Skills are standalone Markdown instruction primitives (`madari skill ...`). Madari stores skill metadata and a managed Markdown copy; plain `skill render` prints that Markdown exactly to stdout, while `skill render --client`, direct `skill attach`, and skill-bearing `ring attach` synthesize native `SKILL.md` frontmatter for supported skill clients. Skills can be ring members, but they are not written into MCP client configs and are not consumed by `run`.
- Codex sync writes static non-secret `[env]` values under `[mcp_servers.<name>.env]` and forwards `[required_env]` plus `[secret_env]` keys through `env_vars`; static secret values are not written into Codex config.
- Vibe sync writes static `[env]` values into user-scoped `[[mcp_servers]]` entries and preserves unmanaged HTTP/streamable/manual entries.
- Supported sync clients: `claude-desktop`, `claude-code`, `gemini`, `codex`, and `vibe`.
- Supported render targets: `claude-code`, `claude-desktop`, `gemini`, `codex`, and `vibe`.
- Supported skill targets: `claude-code`, `codex`, `gemini`, and `vibe`.
- Default sync config paths:
  - `claude-desktop`: platform-specific Claude Desktop config path.
  - `claude-code`: `<current working directory>/.mcp.json`.
  - `gemini`: `<current working directory>/.gemini/settings.json`.
  - `codex`: `$CODEX_HOME/config.toml` or `~/.codex/config.toml`.
  - `vibe`: `$VIBE_HOME/config.toml` or `~/.vibe/config.toml`.
- Default skill roots:
  - `claude-code`: project `.claude/skills`, user `~/.claude/skills`.
  - `codex`: project `.agents/skills`, user `~/.agents/skills`.
  - `gemini`: project `.gemini/skills`, user `~/.gemini/skills`.
  - `vibe`: project `.vibe/skills`, user `~/.vibe/skills`.
- `install --config-path` can only be used when exactly one sync target is selected.
- `export` writes a versioned JSON snapshot of server, ring, and skill manifests for backup/sharing (stdout by default).
- `import` is dry-run by default and only adds/updates listed servers, rings, and skills (`--apply` persists). Existing entries absent from the snapshot are left unchanged; imported rings are not attached or synced.

Claude and Gemini config shape:

```json
{
  "mcpServers": {
    "stewreads": {
      "command": "/Users/me/.local/bin/stewreads-mcp",
      "args": ["--stdio"],
      "env": {
        "STEWREADS_CONFIG_PATH": "~/.config/stewreads/config.toml"
      }
    }
  }
}
```

Codex sync/render output uses TOML tables:

```toml
[mcp_servers.stewreads]
command = "/Users/me/.local/bin/stewreads-mcp"
args = ["--stdio"]
env_vars = ["STEWREADS_API_KEY"]

[mcp_servers.stewreads.env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"
```

Vibe sync/render output uses TOML array entries:

```toml
[[mcp_servers]]
name = "stewreads"
transport = "stdio"
command = "/Users/me/.local/bin/stewreads-mcp"
args = ["--stdio"]
env = { STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml" }
```

Example:

```bash
madari install stewreads-mcp
madari install @modelcontextprotocol/server-sequential-thinking --manager npm --command mcp-server-sequential-thinking
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client claude-desktop
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client claude-code
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client gemini
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client codex
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client vibe
madari list
madari status
madari sync claude-desktop --dry-run
madari sync claude-code --dry-run
madari sync gemini --dry-run
madari sync codex --dry-run
madari sync vibe --dry-run
madari skill add release --file ./SKILL.md --description "Release workflow"
madari skill render release
madari skill render release --client codex
madari skill attach release codex --dry-run
madari export --file madari-snapshot.json
madari import --file madari-snapshot.json
madari import --file madari-snapshot.json --apply
madari doctor
madari help install
madari version
```

Rings — bundle servers and skills into a capability set, attach it to a client, and
spin it up ephemerally:

```bash
madari ring create research --member stewreads --member arxiv --skill release
madari ring attach research claude-code
madari ring attach research gemini
madari ring attach research codex
madari ring status
claude --mcp-config <(madari ring render research --client claude-code)
madari ring render research --client gemini
madari ring render research --client codex
madari ring render research --client vibe
madari ring detach research claude-code
madari ring detach research gemini
madari ring detach research codex
madari ring delete research
```

## Development

Build:

```bash
make build
```

Test:

```bash
go test ./...
```

## Architecture

- Reads registry state, writes client configs; no daemon or proxy
- Only touches entries Madari registered; everything else keeps its JSON value, including fields and server shapes Madari does not model
- Backup + atomic write on every sync; skips invalid entries rather than aborting
- Reference-counted ring ownership: entries leave a client config only when nothing owns them
- `doctor` and `status` for diagnostics, drift detection, and ring consistency
- Supports `uv` and `npm` package manager installs, plus manual `add` for any runtime/framework
- macOS, Linux, and Windows; supports Claude Desktop, Claude Code, Gemini, Codex, and Vibe sync targets

## Principles

- Local-first and transparent
- Human-readable config
- Safe writes (backup + atomic replacement)
- Explicit ownership of managed entries

## Documentation

- `docs/architecture.md`
- `docs/manifest-spec.md`
- `docs/cli-reference.md`
- `docs/troubleshooting.md`

## License

Apache License 2.0. See `LICENSE` and `NOTICE`.

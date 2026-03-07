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
- `madari list`
- `madari remove <name>`
- `madari enable <name>`
- `madari disable <name>`
- `madari sync <client> [--dry-run] [--config-path <path>]`
- `madari clients`
- `madari doctor [--client-config target=path ...]`
- `madari status [--client-config target=path ...]`
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
- Supported sync clients: `claude-desktop` and `claude-code`.
- Default sync config paths:
  - `claude-desktop`: platform-specific Claude Desktop config path.
  - `claude-code`: `<current working directory>/.mcp.json`.
- `install --config-path` can only be used when exactly one sync target is selected.
- `export` writes a versioned JSON snapshot for backup/sharing (stdout by default).
- `import` is dry-run by default and only adds/updates listed servers (`--apply` persists).

Claude Code project config shape (`.mcp.json`):

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

Example:

```bash
madari install stewreads-mcp
madari install @modelcontextprotocol/server-sequential-thinking --manager npm --command mcp-server-sequential-thinking
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client claude-desktop
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client claude-code
madari list
madari status
madari sync claude-desktop --dry-run
madari sync claude-code --dry-run
madari export --file madari-snapshot.json
madari import --file madari-snapshot.json
madari import --file madari-snapshot.json --apply
madari doctor
madari help install
madari version
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
- Only touches entries Madari registered; leaves everything else alone
- Backup + atomic write on every sync; skips invalid entries rather than aborting
- `doctor` and `status` for diagnostics
- Supports `uv` and `npm` package manager installs, plus manual `add` for any runtime/framework
- macOS, Linux, and Windows; supports Claude Desktop and Claude Code sync targets

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

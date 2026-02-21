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
- `madari sync claude-desktop [--dry-run] [--config-path <path>]`
- `madari doctor [--config-path <path>]`
- `madari status [--config-path <path>]`
- `madari export [--file <path>]`
- `madari import --file <path> [--apply]`

Notes:

- `install` runs `uv tool install`, auto-registers the server, and syncs to Claude in one command.
- `install` requires `uv` in PATH unless you use `--skip-install` and pass `--command`.
- `add` resolves `--command` to an absolute executable path and stores that path in the manifest.
- `sync` skips servers with missing/non-executable command paths and continues syncing others.
- `export` writes a versioned JSON snapshot for backup/sharing (stdout by default).
- `import` is dry-run by default and only adds/updates listed servers (`--apply` persists).

Example:

```bash
madari install stewreads-mcp
madari add stewreads --command /Users/me/.local/bin/stewreads-mcp --client claude-desktop
madari list
madari status
madari sync claude-desktop --dry-run
madari export --file madari-snapshot.json
madari import --file madari-snapshot.json
madari import --file madari-snapshot.json --apply
madari doctor
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
- Works with any package manager (`uv`, `pip`, `npm`, etc.), runtime (Python, Node), or MCP framework
- macOS, Linux, and Windows; Claude Desktop is the current sync target

## Principles

- Local-first and transparent
- Human-readable config
- Safe writes (backup + atomic replacement)
- Explicit ownership of managed entries

## Documentation

- `docs/architecture.md`
- `docs/manifest-spec.md`

## License

Apache License 2.0. See `LICENSE` and `NOTICE`.

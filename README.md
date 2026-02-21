# madari

Madari is a local MCP manager focused on reliable server registration, lifecycle management, and client config sync.
Pronunciation: `muh-DAA-ree` (`mə-ˈdɑː-ri`).

## Commands

- `madari add <name> --command <cmd> --client <client>`
- `madari list`
- `madari remove <name>`
- `madari enable <name>`
- `madari disable <name>`
- `madari sync claude-desktop [--dry-run] [--config-path <path>]`
- `madari doctor [--config-path <path>]`
- `madari status [--config-path <path>]`

Notes:

- `add` resolves `--command` to an absolute executable path and stores that path in the manifest.
- `sync` skips servers with missing/non-executable command paths and continues syncing others.

Example:

```bash
madari add stewreads --command stewreads-mcp --client claude-desktop
madari list
madari status
madari sync claude-desktop --dry-run
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

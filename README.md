# madari

Madari is a local MCP manager focused on reliable server registration, lifecycle management, and client config sync.

## Commands

- `madari add <name> --command <cmd> --client <client>`
- `madari list`
- `madari remove <name>`
- `madari enable <name>`
- `madari disable <name>`

Example:

```bash
madari add stewreads --command stewreads-mcp --client claude-desktop
madari list
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

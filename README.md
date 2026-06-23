# madari (muh-DAA-ree)

Madari is a local-first CLI for managing MCP capability setup across AI clients.
It registers MCP servers, stores reusable skills, groups them into rings, and
syncs only the entries it owns into client config files.

Madari is intentionally static: no daemon, proxy, or background mux. It helps
the AI clients and agents you already use get the right capabilities with
predictable config, ownership, and cleanup.

## Installation

Homebrew:

```bash
brew tap ankitvg/tap
brew install madari
```

Go:

```bash
go install github.com/ankitvg/madari/cmd/madari@latest
```

## Quickstart

Install an MCP server, register it for a couple of clients, and inspect what
Madari knows about it:

```bash
madari install @modelcontextprotocol/server-sequential-thinking \
  --name sequential-thinking \
  --manager npm \
  --command mcp-server-sequential-thinking \
  --client codex \
  --client claude-code \
  --no-sync

madari list
madari doctor
```

Dry-run the config change before writing to a client:

```bash
madari sync codex --dry-run
madari sync codex
```

Create a ring when a few capabilities belong together:

```bash
madari skill add release --file ./SKILL.md --description "Release workflow"
madari ring create research \
  --member sequential-thinking \
  --skill release \
  --description "Research and release helpers"

madari ring attach research codex
madari ring status
```

For one-off or ephemeral usage, render a ring without mutating any client
config:

```bash
madari ring render research --client codex
claude --mcp-config <(madari ring render research --client claude-code)
```

Use `madari help <command>` or `docs/cli-reference.md` for complete command
syntax.

## Core Concepts

**Servers** are executable MCP servers. Madari stores their command, arguments,
environment metadata, supported clients, and ownership state.

**Skills** are managed Markdown instructions. They can be attached directly to
supported clients or included in rings, where Madari materializes native
`SKILL.md` files for the target.

**Rings** are named capability sets. A ring can contain server members and skill
members, then attach to a client as one unit. Ring ownership is reference
counted, so overlapping rings and standalone entries detach cleanly in any
order.

**Sync** writes managed server entries into client config files. Madari backs up
before writing, skips ineligible entries instead of aborting the whole sync, and
refuses to adopt or overwrite unmanaged config blocks.

**Render** prints client-native MCP config to stdout without changing state. It
is useful for temporary sessions and experiments.

## Safety Model

- Local-first registry and human-readable config files
- Backup plus atomic write on sync
- Explicit ownership of every managed entry
- No hidden mutation of unmanaged client config
- Secret env keys are not written into repo-scoped configs
- Diagnostics through `madari doctor`, `madari status`, and `madari ring status`

## Supported Clients

Madari can sync MCP servers for:

- `claude-desktop`
- `claude-code`
- `gemini`
- `codex`
- `vibe`

Madari can materialize skills for:

- `claude-code`
- `codex`
- `gemini`
- `vibe`

## Documentation

- `docs/cli-reference.md`
- `docs/architecture.md`
- `docs/manifest-spec.md`
- `docs/troubleshooting.md`

## Development

```bash
make build
go test ./...
```

## License

Apache License 2.0. See `LICENSE` and `NOTICE`.

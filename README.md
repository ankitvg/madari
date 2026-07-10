# madari (muh-DAA-ree)

Madari is a local-first CLI for managing MCP capability setup across AI clients.
It registers MCP servers and their optional access profiles, stores reusable
skills, groups them into rings, plans ring-based agent launches, and syncs only
the entries it owns into client config files.

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

Remote MCP servers are added with a transport and URL instead of a command.
Credential headers (like `Authorization`) and names marked `--secret-header`
never land in repo-scoped configs — they refuse at project scope and sync
with `--scope user`:

```bash
madari add linear \
  --transport http \
  --url https://mcp.linear.app/mcp \
  --header "Authorization=Bearer $LINEAR_TOKEN" \
  --client claude-code

madari sync claude-code --scope user
```

Remote servers that expect a bearer token from the runtime environment can
store only the env var name. Codex materializes this as
`bearer_token_env_var`, keeping the token value out of Madari and repo files:

```bash
madari add cloud-sql \
  --transport http \
  --url https://sqladmin.googleapis.com/mcp \
  --bearer-token-env-var CLOUDSQL_MCP_TOKEN \
  --client codex

madari sync codex
```

Create a ring when a few capabilities belong together:

```bash
madari skill add --dir ./release
madari ring create thinking \
  --member sequential-thinking \
  --description "Sequential thinking helper"
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

Or run Codex with one or more rings without mutating Codex config:

```bash
madari run codex --ring thinking -- \
  "Use this ring to inspect the target context."
```

Use `madari help <command>` or `docs/cli-reference.md` for complete command
syntax.

## Core Concepts

**Servers** are MCP server capabilities. Madari stores stdio commands or remote
HTTP/SSE URLs, environment or header metadata, supported clients, and ownership
state. A server may also declare one portable `[access]` profile with an exact
tool allowlist, an optional deny list, requested OAuth scopes, and default or
per-tool approval behavior.

**Skills** are official Agent Skill packages: directories with `SKILL.md`
frontmatter and optional bundled files such as `references/`, `scripts/`, and
`assets/`. They can be attached directly to supported clients or included in
rings, where Madari materializes the full package for the target.

**Rings** are named capability sets. A ring can contain server members and skill
members, then attach to a client as one unit. A ring `[policy]` can require exact
compilation of every server member's access profile for the selected target.
Rings can also carry an advisory contract for delegation: when to use the ring,
what context to provide, and what outputs to expect. Contracts never authorize
tool access. They can be managed from standalone TOML files with `madari ring
contract`. Ring ownership is reference counted, so overlapping rings and
standalone entries detach cleanly in any order.

**Sync** writes managed server entries into client config files. Madari backs up
before writing, skips ineligible entries instead of aborting the whole sync, and
refuses to adopt or overwrite unmanaged config blocks.

**Render** prints client-native MCP config to stdout without changing state. It
is useful for temporary sessions and experiments.

**Run** starts or plans an ephemeral client launch from one or more rings.
Codex execution injects selected server members into `codex exec`, temporarily
materializes selected skill members as Codex project skills, and writes no
client config or managed state. Codex run clears inherited MCP config, marks
the selected servers required, and runs from an isolated working root so
project-scoped Codex config cannot add unselected capabilities. It also starts
Codex with a temporary `HOME` so personal Codex skills do not leak into the
ring run, and with a temporary `CODEX_HOME` that copies only `auth.json` from
the caller's Codex home so Codex-home skills do not leak into the run. Stdio
MCP servers still receive the caller's non-secret `HOME`, `USERPROFILE`, and
`CODEX_HOME` values when present so home-based server credentials keep working;
secret declarations for those isolated env keys are blocked. Other clients
remain dry-run only for now.
Every access-bearing Codex run requires a stable Codex CLI 0.139.x release and
passes the complete profile through one strict ephemeral config override. A
required ring upgrades that supported profile from advisory to exact
enforcement and blocks before temporary skill materialization on any downgrade.

Capability Policy Contract V1 preserves existing behavior for manifests without
`[access]` and rings without `[policy]`. Codex compiles all five V1 access fields
for persistent sync/attach, render, and ephemeral run. Required operations fail
during preflight if a member, native field, or installed Codex version cannot
represent the contract exactly. Other target policy surfaces remain unsupported
until their own compilers land.

## Safety Model

- Local-first registry and human-readable config files
- Backup plus atomic write on sync
- Explicit ownership of every managed entry
- No hidden mutation of unmanaged client config
- Secret env values are not written into repo-scoped configs
- Remote header values for credential headers (`Authorization`, api-key and
  token variants) and `--secret-header` names follow the same policy, and
  `ring render`/`madari run codex` never emit them
- Bearer token env references store only the env var name; the token value
  stays in the runtime environment
- `madari run` checks runtime env keys by name without printing their values;
  Codex execution passes bearer-token env var names, not token values
- Policy-required operations fail closed instead of approximating or dropping
  an access restriction
- OAuth scopes are requested and client-configured; Madari cannot prove that an
  OAuth provider granted them
- Tool approval behavior controls client prompts and is not an authorization
  boundary
- Diagnostics through `madari doctor`, `madari status`, and `madari ring status`

## Supported Clients

Madari can sync MCP servers for:

- `claude-desktop`
- `claude-code`
- `gemini`
- `codex`
- `vibe`

Remote (`http`/`sse`) servers currently materialize for `claude-code` and
`gemini` (both transports) and `codex` (`http` only); other targets store
remote manifests and report them as pending. Remote entries that require
`oauth_resource` or `bearer_token_env_var` currently materialize only for
`codex`.

Transport, auth, and skill support are separate from capability-policy support.
Codex supports exact policy compilation on persistent sync/attach, render, and
run. Every policy surface for other clients remains unsupported. Legacy
operations remain available; rings with `[policy] enforcement = "required"`
block whenever the selected target surface lacks an exact compiler.

Madari can materialize skills for:

- `claude-code`
- `codex`
- `gemini`
- `vibe`

## Documentation

- `docs/cli-reference.md`
- `docs/architecture.md`
- `docs/manifest-spec.md`
- `docs/adr/003-capability-policy-contract-v1.md`
- `docs/troubleshooting.md`
- `examples/README.md` (self-contained example catalog)
- `examples/cloudsql-readonly/README.md` (worked example: a read-only Cloud SQL ring)

## Development

```bash
make build
go test ./...
```

## License

Apache License 2.0. See `LICENSE` and `NOTICE`.

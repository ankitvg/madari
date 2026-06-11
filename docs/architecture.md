# Architecture

## Goals

- Manage local MCP server registrations through a stable registry.
- Materialize valid client config files without clobbering user-managed entries.
- Provide deterministic lifecycle operations and diagnostics.

## Components

1. Registry
- Path: `<os.UserConfigDir()>/madari/servers/*.toml` (or `$MADARI_CONFIG_DIR/servers/*.toml`)
- One file per server entry; rings live alongside as `rings/<name>.toml`.
- Human-readable and versionable.
- Snapshots (`export`/`import`) carry servers and rings as versioned JSON;
  export refuses rings that would not round-trip, import validates everything
  before writing anything and never attaches or syncs.

2. Client Adapters
- Translate registry entries into client-specific config.
- Current adapters: Claude Desktop and Claude Code.
- Adapters own read/merge/write behavior for their client format.

3. Sync Engine
- Reads registry + client config.
- Generates a deterministic mutation plan.
- Supports `--dry-run` to preview changes.
- Performs backup + atomic write when applying changes.

4. Managed Sync State (ownership)
- Path: `<config-root>/state/<target>-managed.json`, one file per sync
  target and scope (Claude Code has separate project- and user-scope files).
- Versioned JSON (current: version 2) mapping each managed server name to
  the sources that own it: `standalone` and/or `ring:<name>`.
- Version 1 files (bare name lists) are read transparently as
  standalone-owned; the writer emits version 2 only; unknown versions fail
  closed.
- Ownership and materialization are distinct: an entry is present in the
  client config iff it is owned (sources non-empty) AND eligible (enabled,
  targets the client, command-valid, not secret-refused for the scope).
  Ownership persists through ineligibility — a disabled or refused member
  stays owned but absent, and rematerializes when it becomes eligible again.
- Plain sync claims `standalone` only when the previous state already holds
  it, or when there is no state entry and the client config does not contain
  the name (madari is creating the entry). Ring-only entries are never
  promoted to standalone. Standalone is released when the manifest is
  missing, disabled, or no longer targets the client; ring sources are
  released only by detach or membership reconciliation.

5. Rings
- Named capability sets of servers (`rings/<name>.toml`); members reference
  registry entries by name only — the server manifest stays the single
  source of truth for command, args, and env.
- Attachment is derived state: ring `R` is attached to a target+scope iff
  `ring:R` appears among ownership sources there. Attach records the source
  on every member and materializes the eligible ones; detach releases it by
  name (no ring file required), and an entry leaves the config only when its
  last source goes. Overlapping rings resolve by refcount in any order.
- Every sync/attach/detach runs a reconciliation pass: recorded ring sources
  are recomputed against current ring membership, so edits (including
  snapshot imports) converge on the next operation. Unmanaged config
  collisions are never granted a ring source.
- `ring render` materializes a self-contained config to stdout (secret
  values omitted) without touching state or refcounts; `ring status` reports
  attachments, per-server sources, and pending/stale reconciliation work.
- `ring delete` refuses while any target/scope still records the ring as an
  ownership source, and never edits client configs or managed state.

6. Doctor Engine
- Verifies command/binary resolution.
- Validates required env values are present.
- Validates client config parseability.
- Detects drift between manifests and materialized client entries (stale,
  missing, orphaned) for every target+scope with managed entries.
- Flags ring issues: an attached ring whose file is missing (error, with
  detach guidance) and rings referencing deleted manifests (warning).

## Safety Model

- Never overwrite unknown config blocks.
- Entries Madari does not manage keep their JSON value, including fields and
  server shapes Madari does not model (e.g. `type`/`url` remote entries);
  only managed or newly added entries are serialized from manifests. The
  enclosing document is reformatted on write.
- Keep managed entries isolated via per-target managed state tracking files
  (per scope for clients with both repo- and user-scoped configs).
- Never adopt pre-existing entries Madari did not create, even when their
  values match a manifest; ownership is only taken for entries sync introduces.
  Ring attach is stricter still: any unmanaged name collision — equal values
  included — is refused, and membership reconciliation never grants a ring
  source to an unmanaged config entry.
- Remove managed entries from client config only when no recorded source owns them.
- Never materialize static values for secret-marked env keys into repo-scoped
  configs; refused entries are reported (and scrubbed if previously written),
  and sync scope is declared explicitly, never inferred from paths.
- Always backup before write.
- Fail closed on parse errors.

Materialized sync is the only mode: client configs always contain real
commands and survive madari's removal. A launcher shim was considered and
rejected (docs/adr/002-launcher-shim-rejected.md).

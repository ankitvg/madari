# Architecture

## Goals

- Manage local MCP server registrations through a stable registry.
- Materialize valid client config files without clobbering user-managed entries.
- Provide deterministic lifecycle operations and diagnostics.

## Primitive Boundary

Madari keeps four concepts separate:

- Server: an executable MCP capability with command, args, env, and target
  client metadata.
- Ring: a named capability grouping. Today persisted rings contain server
  members only; the grouping surface is intentionally the place where future
  capability types such as skills can compose with MCP servers.
- Skill: procedural or domain instructions for how an agent should use
  capabilities. Skills are standalone Markdown primitives with their own
  metadata, managed content, and render surface.
- Run: ephemeral execution that combines a task, selected capabilities, client
  target, optional skills/context, and result capture. Run is not implemented
  yet and should not be modeled as persistent client sync.

Current behavior follows this boundary: `sync` and `ring attach` persist MCP
server config, `ring render` emits MCP config only, and `skill render` emits
Markdown instructions only. Skills do not write client config and are not ring
members or run inputs yet.

## Components

1. Registry
- Path: `<os.UserConfigDir()>/madari/servers/*.toml` (or `$MADARI_CONFIG_DIR/servers/*.toml`)
- One file per server entry; rings live alongside as `rings/<name>.toml`;
  skills live alongside as `skills/<name>.toml` plus managed Markdown content
  at `skills/<name>.md`.
- Human-readable and versionable.
- Snapshots (`export`/`import`) carry servers, rings, and skills as versioned
  JSON; export refuses rings that would not round-trip, import validates
  everything before writing anything and never attaches or syncs.

2. Client Targets and Adapters
- The command layer keeps a single client target registry for target-level
  capabilities: sync adapter, ring config renderer, and scope support.
  Future client-native skill render support should extend that registry
  instead of adding another target-specific switch.
- Translate registry entries into client-specific config.
- Current adapters: Claude Desktop, Claude Code, Gemini, Codex, and Vibe.
- Adapters own read/merge/write behavior for their client format.

3. Sync Engine
- Reads registry + client config.
- Generates a deterministic mutation plan.
- Supports `--dry-run` to preview changes.
- Performs backup + atomic write when applying changes.

4. Managed Sync State (ownership)
- Path: `<config-root>/state/<target>-managed.json`, one file per sync
  target and scope (project/user-scoped clients have separate state files).
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
- Named capability sets (`rings/<name>.toml`). In the current schema, members
  are server references by name only — the server manifest stays the single
  source of truth for command, args, and env. Skills are standalone in V1;
  future skill membership should grow the ring schema deliberately rather than
  overloading server manifests.
- Attachment is derived state: ring `R` is attached to a target+scope iff
  `ring:R` appears among ownership sources there. Attach records the source
  on every member and materializes the eligible ones; detach releases it by
  name (no ring file required), and an entry leaves the config only when its
  last source goes. Overlapping rings resolve by refcount in any order.
- Every sync/attach/detach runs a reconciliation pass: recorded ring sources
  are recomputed against current ring membership, so edits (including
  snapshot imports) converge on the next operation. Unmanaged config
  collisions are never granted a ring source.
- `ring render` materializes a self-contained client-native config to stdout
  (secret values omitted) without touching state or refcounts. Render targets
  are registered independently from sync adapters, so a client can support
  render-only output before persistent sync/attach support exists. `ring
  status` reports attachments, per-server sources, and pending/stale
  reconciliation work.
- `ring delete` refuses while any target/scope still records the ring as an
  ownership source, and never edits client configs or managed state.

6. Skills
- Standalone instruction primitives stored as metadata plus managed Markdown:
  `skills/<name>.toml` and `skills/<name>.md`.
- `skill add` copies a non-empty Markdown file into Madari; `skill update`
  replaces that managed copy and preserves description unless a new one is
  provided.
- `skill render` prints the managed Markdown exactly to stdout. It mutates no
  state, writes no client files, and does not imply model restriction to that
  workflow.
- V1 skills are exported/imported in snapshots but are not consumed by rings,
  sync adapters, or run execution.

7. Doctor Engine
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

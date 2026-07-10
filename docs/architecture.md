# Architecture

## Goals

- Manage local MCP server registrations through a stable registry.
- Materialize valid client config files without clobbering user-managed entries.
- Provide deterministic lifecycle operations and diagnostics.

## Primitive Boundary

Madari keeps four concepts separate:

- Server: an executable MCP capability with command, args, env, target client
  metadata, and one optional portable access profile.
- Ring: a named capability grouping with optional required-enforcement policy
  and optional advisory contract metadata. Persisted rings contain server and
  skill members by name; referenced server manifests and skill packages remain
  the source of truth.
- Skill: procedural or domain instructions for how an agent should use
  capabilities. Skills are official Agent Skill package directories with
  `SKILL.md` metadata, bundled files, and a render surface.
- Run: ephemeral execution that combines a task, selected capabilities, client
  target, optional skills/context, and result capture. Codex run is
  implemented as ephemeral execution and should not be modeled as persistent
  client sync.

Current behavior follows this boundary: `sync` and `ring attach` persist MCP
server config, `ring attach` also materializes ring skill members as native
skill package directories for supported targets, `ring render` emits MCP config
only, `madari run codex` clears inherited MCP config and injects selected
required server members into `codex exec`, while temporarily materializing
selected ring skill members under an isolated working root without writing
config/state. Plain `skill render` emits managed `SKILL.md` only, and `skill
attach` materializes client-native skill packages without changing MCP client
configs.

Access policy follows the same boundary: the server owns `allowed_tools`,
`denied_tools`, `oauth_scopes`, `default_approval`, and per-tool approvals. A
ring only selects whether exact enforcement is required; it does not duplicate
member profiles. `[contract]` and skill content remain advisory, while
`[policy.execution]` is reserved and rejected in V1.

## Components

1. Registry
- Path: `<os.UserConfigDir()>/madari/servers/*.toml` (or `$MADARI_CONFIG_DIR/servers/*.toml`)
- One file per server entry; rings live alongside as `rings/<name>.toml`;
  skills live alongside as official Agent Skill packages under
  `skills/<name>/`.
- Human-readable and versionable.
- Snapshots (`export`/`import`) carry servers, rings, and skills as versioned
  JSON; export refuses rings that would not round-trip, import validates
  everything before writing anything and never attaches or syncs. Snapshot
  version 4 added ring skill membership, version 5 added ring contract metadata,
  version 6 stores full skill package files, and V10 adds server access profiles
  plus ring policy. V1 through V9 remain importable; older versions carrying V10
  fields are rejected so policy data cannot be discarded silently.

2. Client Targets and Adapters
- The command layer keeps a single client target registry for target-level
  capabilities: sync adapter, ring config renderer, skill roots, scope support,
  and separate policy support declarations for persistent sync/attach, render,
  and run.
- Translate registry entries into client-specific config.
- Current adapters: Claude Desktop, Claude Code, Gemini, Codex, and Vibe.
- Adapters own read/merge/write behavior for their client format.
- An unspecified policy compiler is unsupported. Codex persistent sync/attach
  and render opt into all five V1 access features; Codex run and every other
  target policy surface remain fail-closed.

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
- Named capability sets (`rings/<name>.toml`). Server members are references by
  name only — the server manifest stays the single source of truth for command,
  args, env, and access profile. Skill members are references by name only —
  managed skill metadata and Markdown stay the source of truth. Optional
  `[contract]` metadata describes when the ring is useful, what context a
  delegating agent should provide, and what outputs to expect. Contracts are
  advisory only: attach, detach, sync, render, status, ownership, and skill
  materialization do not change when a contract changes. Optional `[policy]`
  metadata can set `enforcement = "required"` without embedding member policy.
- Attachment is derived state: ring `R` is attached to a target+scope iff
  `ring:R` appears among server ownership sources or skill attachment sources
  there. Attach records the source on every server and skill member,
  materializes eligible servers into MCP config, and materializes skills into
  native skill directories for supported targets. Detach releases the source by
  name (no ring file required), and an entry or skill package leaves the client
  only when its last source goes. Overlapping rings resolve by refcount in any
  order.
- Every sync/attach/detach runs a reconciliation pass: recorded ring sources
  are recomputed against current ring membership, so edits (including
  snapshot imports) converge on the next operation. Unmanaged config
  collisions are never granted a ring source.
- `ring render` materializes a self-contained client-native MCP config to
  stdout (secret values omitted) without touching state or refcounts. Ring
  skill members are not embedded in render output. Render targets are
  registered independently from sync adapters, so a client can support
  render-only output before persistent sync/attach support exists. `ring
  status` reports attachments, per-server and per-skill sources, and
  pending/stale reconciliation work.
- Contracts are distinct from run/session/context primitives. Codex run renders
  contract metadata into the prompt preamble, but the ring definition does not
  embed task context or result transport.
- `ring delete` refuses while any target/scope still records the ring as an
  ownership source, and never edits client configs or managed state.

6. Capability Policy
- `[access]` is optional. No section means a legacy server makes no Madari
  access declaration.
- The portable approval vocabulary is `inherit`, `automatic`, `always-prompt`,
  and `always-allow`. Raw client-native values are not registry values.
- Field presence is semantic. Absent fields preserve native values during
  persistent sync, while explicit empty arrays/tables and `inherit` clear the
  corresponding override. Required rings reject absent or empty allowlists as
  unbounded.
- A required operation succeeds only if every member exists, is enabled, targets
  the selected client, has a non-empty exact allowlist, and compiles every field
  without approximation. Unknown behavior-affecting native fields also prevent
  an exact fidelity claim.
- Required sync/attach fails before config, managed state, or skills change;
  render fails before partial output; run fails before skill materialization or
  execution. Detach remains available.
- Codex sync patches cloned native server tables instead of serializing a narrow
  replacement. Undeclared native policy fields survive legacy updates; declared
  fields compile exactly, explicit clears remove only their native overrides,
  and required operations reject unknown behavior-affecting fields.
- OAuth scopes are requested and client-configured, not proof of a provider
  grant. Approval behavior is a client prompt control, not authorization.
- Environment sanitization, TTLs, receipts, credential brokers, audit, and
  `[policy.execution]` are outside V1.

7. Skills
- Standalone Agent Skill packages stored at `skills/<name>/` with `SKILL.md`
  frontmatter plus optional bundled files such as `references/`, `scripts/`,
  and `assets/`.
- `skill add --dir` copies a complete package into Madari. The legacy
  `--file` form converts one Markdown file into package-backed `SKILL.md`;
  legacy flat store files are still read and migrate on the next save/import.
- Plain `skill render` prints the managed `SKILL.md` exactly to stdout. With
  `--client`, render validates target support and prints the same file.
- `skill attach` writes Madari-owned package directories into supported native
  skill roots and records separate source-aware skill attachment state
  (current: version 4). Direct attach owns the `standalone` source; ring attach
  owns `ring:<name>`. It refuses to overwrite unmanaged package directories or
  remove packages modified after Madari wrote them.
- Skills are exported/imported in snapshots and can be consumed by rings. They
  are not written into MCP config files. `madari run codex` consumes selected
  ring skills by temporarily materializing them as project skills under the
  isolated run root; other run targets are dry-run only today.

8. Doctor Engine
- Verifies command/binary resolution.
- Validates required env values are present.
- Validates client config parseability.
- Detects drift between manifests and materialized client entries (stale,
  missing, orphaned) for every target+scope with managed entries.
- Flags ring issues: an attached ring whose file is missing (error, with
  detach guidance) and rings referencing deleted server manifests (warning).

## Safety Model

- Never overwrite unknown config blocks.
- Entries Madari does not manage keep their JSON value, including fields and
  server shapes Madari does not model (e.g. `type`/`url` remote entries);
  only managed or newly added entries are serialized from manifests. Managed
  stdio entries are serialized from command manifests; managed remote entries
  are serialized client-natively (Codex `url`/`http_headers`, Claude Code
  `type`/`url`/`headers`, Gemini `httpUrl` or `url` plus `headers`). Adapters
  report remote support per transport via `SupportsRemote(transport)`;
  unsupported combinations keep remote manifests ineligible. Remote header
  values for credential headers (built-in list plus `[secret_headers]`)
  follow the secret placement policy: refused and scrubbed at repo scope,
  user scope only. The enclosing document is reformatted on write. For adapters with raw-match
  validation (currently Claude Code and Claude Desktop), if an unmanaged entry
  has the same name as a desired manifest, Madari only treats it as an exact
  match when its canonical raw JSON, after normalizing empty modeled optional
  fields, equals the manifest materialization; extra or unmodeled fields are
  reported as a conflict instead of being silently preserved under a trusted
  manifest name.
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
- Never claim required policy fidelity when any declared restriction is
  unsupported, omitted, or approximated.
- Keep client-enforced tool filtering, requested OAuth scopes, client approval
  prompts, and advisory contract/skill instructions distinct in reporting.

Materialized sync is the only mode: client configs always contain real
commands and survive madari's removal. A launcher shim was considered and
rejected (docs/adr/002-launcher-shim-rejected.md).

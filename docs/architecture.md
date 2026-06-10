# Architecture

## Goals

- Manage local MCP server registrations through a stable registry.
- Materialize valid client config files without clobbering user-managed entries.
- Provide deterministic lifecycle operations and diagnostics.

## Components

1. Registry
- Path: `<os.UserConfigDir()>/madari/servers/*.toml` (or `$MADARI_CONFIG_DIR/servers/*.toml`)
- One file per server entry.
- Human-readable and versionable.

2. Client Adapters
- Translate registry entries into client-specific config.
- Current adapters: Claude Desktop and Claude Code.
- Adapters own read/merge/write behavior for their client format.

3. Sync Engine
- Reads registry + client config.
- Generates a deterministic mutation plan.
- Supports `--dry-run` to preview changes.
- Performs backup + atomic write when applying changes.

4. Managed Sync State
- Path: `<config-root>/state/<target>-managed.json`, one file per sync target.
- Versioned JSON (current: version 2) mapping each managed server name to the
  sources that own it (`standalone` today; ring sources later).
- Version 1 files (bare name lists) are read transparently as
  standalone-owned; the writer emits version 2 only; unknown versions fail
  closed.
- A managed entry that is no longer desired loses its `standalone` source and
  is removed from client config only when no sources remain to own it.

5. Doctor Engine
- Verifies command/binary resolution.
- Validates required env values are present.
- Validates client config parseability and managed entry consistency.

## Safety Model

- Never overwrite unknown config blocks.
- Keep managed entries isolated via per-target managed state tracking files.
- Remove managed entries from client config only when no recorded source owns them.
- Always backup before write.
- Fail closed on parse errors.

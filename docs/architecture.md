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

4. Doctor Engine
- Verifies command/binary resolution.
- Validates required env values are present.
- Validates client config parseability and managed entry consistency.

## Safety Model

- Never overwrite unknown config blocks.
- Keep managed entries isolated via per-target managed state tracking files.
- Always backup before write.
- Fail closed on parse errors.

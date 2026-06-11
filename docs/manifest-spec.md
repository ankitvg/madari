# Manifest Spec

Each managed server is stored as a TOML document.

## File Location

`<os.UserConfigDir()>/madari/servers/<name>.toml` (or `$MADARI_CONFIG_DIR/servers/<name>.toml`)

## Fields

- `name` (string, required): stable logical ID.
- `command` (string, required): absolute executable path for reliable sync behavior.
- `args` (array of strings, optional): command arguments.
- `enabled` (bool, required): whether this server should be synced into clients.
- `clients` (array of strings, required): client IDs.
- `description` (string, optional): friendly description.

Supported client IDs:

- `claude-desktop`
- `claude-code`

### `[env]`

Key/value static environment variables.

### `[required_env]`

- `keys` (array of strings): env vars that must exist in runtime context.

### `[secret_env]`

- `keys` (array of strings): env vars whose values are secrets. Sync refuses
  to materialize a static `[env]` value for a secret key into repo-scoped
  client configs (for example the Claude Code project `.mcp.json`); secret
  values may only land in user-scoped configs.

## Example

```toml
name = "stewreads"
command = "/Users/me/.local/bin/stewreads-mcp"
args = []
enabled = true
clients = ["claude-desktop"]
description = "Turn AI conversations into ebooks"

[env]
STEWREADS_CONFIG_PATH = "~/.config/stewreads/config.toml"

[required_env]
keys = ["STEWREADS_GMAIL_APP_PASSWORD"]
```

## Validation Rules

- `name` must be lowercase alphanumeric with `-` and `.` allowed as separators.
- `clients` must contain unique values.
- Unknown top-level keys are rejected.
- Empty `command` is invalid.

## Ring Files

Rings are named capability sets of servers, stored one TOML document per
ring at `<config-root>/rings/<name>.toml` (sibling of `servers/`).

- `name` (string, required): stable ring ID; same pattern as server names.
- `members` (array of strings, required, non-empty, unique): server names.
  Members reference registry entries by name only — rings never embed
  command, args, or env; the server manifest stays the single source of
  truth. Every member must exist in the registry when a ring is created or
  imported.
- `description` (string, optional): friendly description.

Ring manifests have no sections; unknown keys are rejected. Files are
written deterministically with sorted members.

### Example

```toml
name = "research"
members = ["arxiv", "stewreads"]
description = "Research helpers"
```

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

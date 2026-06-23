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

Known client IDs:

- `claude-desktop`
- `claude-code`
- `gemini`
- `codex`
- `vibe`

`claude-desktop`, `claude-code`, `gemini`, `codex`, and `vibe` are
sync-capable today. All are also render targets for `madari ring render`.

### `[env]`

Key/value static environment variables.

### `[required_env]`

- `keys` (array of strings): env vars that must exist in runtime context.

### `[secret_env]`

- `keys` (array of strings): env vars whose values are secrets. Sync refuses
  to materialize a static `[env]` value for a secret key into repo-scoped
  client configs (for example Claude Code `.mcp.json` or Gemini
  `.gemini/settings.json`); secret values may only land in user-scoped
  configs. Codex sync forwards secret keys through `env_vars` and does not
  write static secret values into Codex config. Vibe sync targets the user
  config, so static secret values are allowed there like other user-scoped
  configs.

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

Rings are named capability sets of servers and skills. Madari stores one TOML
document per ring at
`<config-root>/rings/<name>.toml` (sibling of `servers/`).

- `name` (string, required): stable ring ID; same pattern as server names.
- `members` (array of strings, optional, unique): server names.
  Members reference registry entries by name only — rings never embed
  command, args, or env; the server manifest stays the single source of
  truth.
- `skills` (array of strings, optional, unique): skill names. Skill members
  reference managed skill entries by name only — rings never embed Markdown
  content.
- `description` (string, optional): friendly description.

At least one `members` or `skills` entry is required. Every referenced server
and skill must exist in the registry when a ring is created or imported.
Ring manifests support only the top-level fields above plus the optional
`[contract]` section. Unknown top-level keys, unknown sections, and unknown
`[contract]` keys are rejected. Files are written deterministically with sorted
server and skill members; contract arrays preserve authored order.

### `[contract]`

Ring contracts are advisory metadata for main-thread delegation and future
subagent initialization. They do not affect attach, detach, sync, status, or
render behavior.

- `summary` (string, optional): what this ring is for.
- `good_for` (array of strings, optional): task types this ring is suited to.
- `not_for` (array of strings, optional): task types this ring should avoid.
- `required_context` (array of strings, optional): conceptual inputs the main
  thread should provide before delegating.
- `optional_context` (array of strings, optional): useful but non-required
  inputs.
- `expected_outputs` (array of strings, optional): advisory response shape;
  these are not filesystem paths.

`madari ring contract set <name> --file <path>` uses the same fields in a
standalone contract file without the `[contract]` section. `madari ring
contract show <name>` prints that standalone shape, and `madari ring contract
clear <name>` removes the contract from the ring.

### Example

```toml
name = "research"
members = ["arxiv", "stewreads"]
skills = ["release"]
description = "Research helpers"

[contract]
summary = "Collect source context and prepare a research brief."
good_for = ["source collection", "evidence review"]
not_for = ["deployments", "database mutation"]
required_context = ["research question"]
optional_context = ["time window", "known source URLs"]
expected_outputs = ["findings summary", "sources inspected", "recommended next check"]
```

## Skill Files

Skills are standalone Markdown instruction primitives. Madari stores skill
metadata at `<config-root>/skills/<name>.toml` and the managed Markdown body
at `<config-root>/skills/<name>.md`.

- `name` (string, required): stable skill ID; same pattern as server names.
- `description` (string, optional): friendly description.

Skill manifests have no sections; unknown keys are rejected. The Markdown
body is arbitrary text, but it must be non-empty. Plain `skill render` emits
that managed body exactly. Client-native render/attach synthesize a `SKILL.md`
frontmatter block from the manifest metadata and the managed body. Skills can
be referenced by rings and materialized through `ring attach` for supported
skill targets. Skills are not written into MCP client configs and are not
consumed by `run`.

### Example

```toml
name = "release"
description = "Release workflow"
```

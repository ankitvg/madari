# Manifest Spec

Each managed server is stored as a TOML document.

## File Location

`<os.UserConfigDir()>/madari/servers/<name>.toml` (or `$MADARI_CONFIG_DIR/servers/<name>.toml`)

## Fields

- `name` (string, required): stable logical ID.
- `transport` (string, optional): `stdio`, `http`, or `sse`. Empty means
  `stdio` for legacy manifests.
- `command` (string, required for `stdio`): absolute executable path for
  reliable sync behavior.
- `args` (array of strings, optional for `stdio`): command arguments.
- `url` (string, required for `http`/`sse`): remote MCP server URL.
- `timeout_ms` (integer, optional for `http`/`sse`): remote MCP timeout in
  milliseconds. Emitted only for clients that support it.
- `oauth_resource` (string, optional for `http`/`sse`): OAuth resource value
  for clients that support it.
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
Remote `http`/`sse` manifests are stored and validated, but no sync or render
target materializes them yet; adapters keep remote entries ineligible until
their auth and config behavior is validated per client.

### `[headers]`

Key/value static HTTP headers for remote transports. Header names are limited
to letters, digits, `-`, and `_` so they round-trip through the manifest
format. Headers are stored in the manifest but emitted only for clients that
support header configuration.

### `[env]`

Key/value static environment variables for stdio transports.

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

Stdio server:

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

Remote HTTP server:

```toml
name = "cloud-sql"
transport = "http"
url = "https://sqladmin.googleapis.com/mcp"
oauth_resource = "https://sqladmin.googleapis.com/"
enabled = true
clients = ["codex"]
description = "Official Cloud SQL remote MCP server"
```

## Validation Rules

- `name` must be lowercase alphanumeric with `-` and `.` allowed as separators.
- `clients` must contain unique values.
- Unknown top-level keys are rejected.
- Empty `command` is invalid for `stdio`.
- `url` is required for `http` and `sse`.
- Remote transports reject `command`, `args`, `[env]`, `[required_env]`, and
  `[secret_env]` because they are local process settings.

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

## Skill Packages

Skills are official Agent Skill packages. Madari stores each managed skill at
`<config-root>/skills/<name>/`, with a required `SKILL.md` plus optional files
such as `references/`, `scripts/`, and `assets/`.

`SKILL.md` must contain YAML frontmatter followed by a non-empty Markdown body.
Madari validates these frontmatter fields:

- `name` (string, required): lowercase letters, numbers, and single hyphen
  separators; max 64 characters; must match the package directory name.
- `description` (string, required): non-empty; max 1024 characters.
- `license` (string, optional).
- `compatibility` (string, optional): max 500 characters.
- `metadata` (map of string keys to string values, optional).
- `allowed-tools` (string, optional).

Unknown top-level frontmatter keys are rejected; custom data belongs under
`metadata`. Package files must be regular files under the package root; symlinks,
absolute paths, and `..` escapes are rejected.

`skill render` prints the managed `SKILL.md` exactly. `skill attach` and ring
attach materialize the full package directory for supported skill targets.
Skills can be referenced by rings, but rings store only skill names and never
embed package files. Skills are not written into MCP client configs and are not
consumed by `run`.

Madari still reads legacy flat skills from `<config-root>/skills/<name>.toml`
and `<config-root>/skills/<name>.md`; the next save, update, or import migrates
them into the package directory shape.

### Example

```text
release/
  SKILL.md
  references/CHECKLIST.md
  scripts/release.sh
```

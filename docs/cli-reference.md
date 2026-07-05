# CLI Reference

Quick command reference for day-to-day Madari usage.

## Server Lifecycle

```bash
madari add <name> --command /abs/path/to/server --client <client>
madari add <name> --transport http --url https://example.com/mcp --client codex
madari list
madari enable <name>
madari disable <name>
madari remove <name>
```

`madari add` defaults to `stdio`, where `--command` is required and is resolved
to an absolute executable path. Remote manifests use `--transport http` or
`--transport sse` with `--url`; optional remote metadata includes `--header`,
`--secret-header`, `--timeout-ms`, `--oauth-resource`, and
`--bearer-token-env-var`. Remote manifests materialize for `codex` (`http`
only), `claude-code`, and `gemini` (`http` and `sse`); the remaining targets,
transports, and unsupported auth modes skip them until per-client support
lands. Well-known credential headers, and names marked with `--secret-header`,
are refused in repo-scoped configs. `--bearer-token-env-var` stores only the
runtime env key that contains a bearer token. `madari list` and `madari doctor`
show each server's transport and endpoint so remote entries are visible where
materialization is pending.

## Install Workflow

```bash
madari install <package>
madari install <package> --skip-install --command /abs/path/to/server
madari install @scope/pkg --manager npm --command executable-name
```

## Sync

```bash
madari sync claude-desktop --dry-run
madari sync claude-desktop
madari sync claude-code --dry-run
madari sync claude-code
madari sync claude-code --scope user
madari sync gemini --dry-run
madari sync gemini
madari sync gemini --scope user
madari sync codex --dry-run
madari sync codex
madari sync vibe --dry-run
madari sync vibe
```

`--scope` applies to clients with project and user configs: `claude-code`
(`.mcp.json` or `~/.claude.json`) and `gemini` (`.gemini/settings.json` or
`~/.gemini/settings.json`). `project` is the default. Each scope tracks its
managed entries independently. Servers carrying static values for
`[secret_env]` keys — or remote header values for credential headers and
`[secret_headers]` names — are refused per entry at project scope (other
servers keep syncing; a previously materialized secret entry is scrubbed)
and must sync with `--scope user`.

`claude-code` and `gemini` also materialize managed remote entries.
Claude Code writes `type`/`url` entries with `headers` into its config
(accepting existing unmanaged `streamable-http` entries as equal to `http`);
Gemini writes `httpUrl` (Streamable HTTP) or `url` (SSE) entries with
`headers`. `timeout_ms` is carried through as each client's per-server
`timeout` field (milliseconds). `oauth_resource` has no equivalent in either
client, and neither client has a validated `bearer_token_env_var` equivalent.
Remote entries that require either auth metadata shape stay pending for these
clients until an equivalent config shape is validated.

`codex` sync targets Codex's user config (`$CODEX_HOME/config.toml`, or
`~/.codex/config.toml` when `CODEX_HOME` is unset). Static non-secret `[env]`
values are written under `[mcp_servers.<name>.env]`; `[required_env]` and
`[secret_env]` keys are forwarded through `env_vars`, and static secret
values are not written into Codex config. Codex also materializes managed
remote Streamable HTTP entries as native `url` entries with optional
`oauth_resource`, optional `bearer_token_env_var`, and manifest `[headers]`
as Codex `http_headers`. `sse` manifests stay pending (Codex's documented
remote support is Streamable HTTP), and `timeout_ms` has no Codex equivalent
and is not emitted.

`vibe` sync targets Vibe's user config (`$VIBE_HOME/config.toml`, or
`~/.vibe/config.toml` when `VIBE_HOME` is unset). Static `[env]` values are
written into `[[mcp_servers]]` stdio entries. Existing unmanaged HTTP,
streamable HTTP, or hand-managed stdio entries are preserved and never adopted.

## Rings

```bash
madari ring create research --member stewreads --member arxiv --skill release --description "Research helpers"
madari ring list
madari ring show research
madari ring contract show research
madari ring contract set research --file contract.toml
madari ring contract clear research
madari ring attach research claude-code
madari ring attach research claude-code --scope user
madari ring attach research gemini
madari ring attach research gemini --scope user
madari ring attach research codex
madari ring attach research vibe
madari ring detach research claude-code
madari ring detach research gemini
madari ring detach research codex
madari ring detach research vibe
madari ring delete research
madari ring render research --client claude-code
madari ring render research --client gemini
madari ring render research --client codex
madari ring render research --client vibe
madari ring status
madari run codex --ring research -- "Use this ring"
madari run codex --ring research --ring release --dry-run --json -- "Use both rings"
```

Rings are named capability sets of servers and skills stored at
`<config-root>/rings/<name>.toml` (see the manifest spec). Server members
reference server registry entries by name; skill members reference managed
skill entries by name. Referenced entries must exist when the ring is created.

Ring contracts are advisory metadata for delegation. `ring contract set`
replaces the whole contract from a standalone TOML file, `ring contract show`
prints that same standalone file shape, and `ring contract clear` removes the
contract. Contract commands do not change members, skills, attach state, sync
behavior, or render output.

```toml
summary = "Collect source context and prepare a research brief."
good_for = ["source collection", "evidence review"]
not_for = ["deployments", "database mutation"]
required_context = ["research question"]
optional_context = ["time window", "known source URLs"]
expected_outputs = ["findings summary", "sources inspected"]
```

Attach records a `ring:<name>` ownership source for every server and skill
member. Eligible servers are materialized into MCP client config; skill members
are materialized as native skill package directories for `claude-code`,
`codex`, `gemini`, and `vibe`. Rings containing skills cannot attach to
`claude-desktop`. Detach releases the source, and a server config entry or
skill package leaves the client only when no source owns it. Ownership is
reference-counted: overlapping rings and standalone+ring combinations resolve
in any attach/detach order. Disabled, secret-refused, or missing server
members stay owned but absent until they become eligible. Ring membership
edits (including snapshot imports) reconcile on the next sync, attach, or
detach. Attaching onto an entry or skill package directory madari does not
manage is refused, even when values match.

`ring delete` removes the ring definition only after every target/scope has
released its `ring:<name>` ownership source. If the ring is still attached,
the command exits non-zero and prints scoped `ring detach` guidance. Deleting
a ring never edits client configs or managed state.

`ring render` prints a self-contained MCP config to stdout and mutates
nothing — no state, no refcounts. Server members are filtered by client
compatibility; disabled, missing, or command-invalid members are omitted
with stderr warnings, and static values for `[secret_env]` keys are never
emitted (the warning names the keys to provide via the runtime environment).
Render targets are independent from sync adapters. `claude-code`,
`claude-desktop`, and `gemini` emit JSON with top-level `mcpServers`;
`codex` emits TOML `[mcp_servers.<name>]` tables; `vibe` emits TOML
`[[mcp_servers]]` entries with `transport = "stdio"`.
Codex render output also emits `env_vars = [...]` for `[required_env]` and
`[secret_env]` keys so runtime-provided environment values can be forwarded.
Codex render also emits remote Streamable HTTP members as `url` entries with
optional `oauth_resource`, optional `bearer_token_env_var`, and `[headers]`
as `http_headers`. Claude Code render emits remote members as
`type`/`url`/`headers`; Gemini render emits `httpUrl` (Streamable HTTP) or
`url` (SSE) with `headers`. Secret header values are never emitted by render —
the warning names the headers to provide manually. Remote members are omitted
with warnings for targets, transports, or auth modes without support (codex
SSE, bearer-token-env auth outside Codex, claude-desktop, vibe).
Ring skill members are not embedded in MCP render output; use `ring attach`
for native skill materialization.
Ephemeral-session recipe:

```bash
claude --mcp-config <(madari ring render research --client claude-code)
```

`ring status` shows attached rings plus per-server and per-skill ownership
sources for every client and scope, flags rings whose file is missing (with the
`ring detach` command that releases the stale sources), and calls out
members pending sync, stale owners left by membership edits (`stale` for
servers, `stale-skills` for skills), and members missing from the registry.
Remediation hints assume default config paths — pass `--config-path` when the
ring was attached to a custom config. `madari doctor` reports
the same conditions as `ring_issues` (missing ring file = error, dangling
server member = warning).

## Run

```bash
madari run <client> --ring <ring> [--ring <ring> ...] [--dry-run] -- <prompt>
madari run codex --ring cloudsql-readonly -- "Who are the top 5 ebook creators?"
madari run codex --ring cloudsql-readonly --ring research --dry-run --json -- "Inspect the combined plan"
```

`madari run` is the launch primitive for using one or more rings with a target
client. Codex execution starts `codex exec --ephemeral --ignore-user-config
--sandbox read-only` with selected ring MCP servers injected as config
overrides. Other clients remain dry-run only for now.

The planner resolves every selected ring, deduplicates shared server and skill
members, validates the selected client can express each required capability,
checks runtime env keys by name, and reports the launch plan. Run never writes
client config, creates managed state, or materializes skill packages.

Unlike `ring render`, `run` is fail-closed. A disabled member, missing member,
unsupported remote transport or auth mode, missing runtime env key, or
unsupported skill target blocks the plan instead of silently omitting that
capability. Rings containing skills also block Codex execution until a later
skill-run implementation.

## Skills

```bash
madari skill add --dir ./release
madari skill add release --file ./SKILL.md --description "Release workflow"
madari skill update release --dir ./release
madari skill update release --file ./SKILL.md
madari skill update release --file ./SKILL.md --description "Updated workflow"
madari skill list
madari skill show release
madari skill render release
madari skill render release --client codex
madari skill attach release codex
madari skill attach release codex --scope user
madari skill detach release codex
madari skill remove release
```

Skills are official Agent Skill packages stored as managed local copies at
`<config-root>/skills/<name>/`. Each package has a required `SKILL.md` with
YAML frontmatter and may include bundled files such as `references/`,
`scripts/`, and `assets/`. `skill add --dir` copies a complete package;
the legacy `--file` form converts one Markdown file into package-backed
`SKILL.md` and requires a description unless the source already has valid
frontmatter. `skill update` fails if the skill does not exist.

Plain `skill render` prints the managed `SKILL.md` bytes exactly to stdout and
mutates nothing. `skill render --client <target>` validates target support and
prints the same managed `SKILL.md`.

Direct `skill attach` writes the full package to `<skills-dir>/<name>/` and
records `standalone` ownership in Madari state. Project scope is the default;
user scope writes to the target's user skill root. `--skills-dir` overrides the
root. Attach refuses to overwrite unmanaged package directories. `skill detach`
releases direct ownership and removes the package only when no source owns it
anymore; it refuses to delete packages modified since Madari last wrote them.
Ring attach uses the same native skill materialization state with
`ring:<name>` ownership sources. Skills are not written into MCP client configs
and are not materialized by the dry-run planner yet.

## Diagnostics

```bash
madari clients
madari status
madari doctor
madari doctor --client-config claude-desktop=/path/to/config.json
```

## Backup and Restore

```bash
madari export --file madari-snapshot.json
madari import --file madari-snapshot.json
madari import --file madari-snapshot.json --apply
```

Snapshots are versioned JSON documents containing server manifests, ring
definitions, and skill packages. Export writes the current snapshot version
with `servers`, `rings`, and `skills`. Snapshot version 6 stores skills as
deterministic package file entries with relative paths, base64 content, and
file modes; older snapshots with a single skill `content` string remain
importable and are converted to package-backed `SKILL.md`. Import is a dry-run
by default; `--apply` adds or updates listed servers, rings, and skills, never
deletes entries absent from the snapshot, and never attaches or syncs imported
rings.
Ring membership changes converge through the normal reconciliation pass on
the next sync, attach, or detach.

## JSON Output

`list`, `status`, `doctor`, `sync --dry-run`, `run --dry-run`, `ring list`,
`ring show`, `ring status`, `skill list`, and `skill show` accept `--json` and emit a
single JSON document on stdout with nothing else.
Every payload carries the envelope fields `schema_version` (currently `1`)
and `command`. Field additions are backward-compatible; renames or removals
bump `schema_version`. List-valued fields are always present (empty arrays,
never `null`) when their containing object is emitted. Optional objects such as
ring `contract` are omitted when absent.

```bash
madari list --json
madari status --json
madari doctor --json
madari sync claude-code --dry-run --json
madari run codex --ring research --dry-run --json -- "Use this ring"
madari ring list --json
madari ring show research --json
madari ring status --json
madari skill list --json
madari skill show release --json
```

`sync --json` requires `--dry-run`; the apply-mode output contract is not yet
defined. `run --json` also requires `--dry-run` until client execution lands.

### Schemas (schema_version 1)

`madari list --json`:

```json
{
  "schema_version": 1,
  "command": "list",
  "servers": [
    {
      "name": "stewreads",
      "enabled": true,
      "transport": "stdio",
      "command": "/abs/path/stewreads-mcp",
      "clients": ["claude-desktop"],
      "sources": ["standalone"]
    }
  ]
}
```

`madari status --json` (client configs include skipped targets; managed
summaries cover every sync target):

```json
{
  "schema_version": 1,
  "command": "status",
  "summary": {"total": 1, "ready": 1, "warning": 0, "error": 0, "skipped": 0},
  "client_configs": [
    {"target": "claude-code", "status": "skipped"},
    {"target": "codex", "status": "skipped"},
    {"target": "claude-desktop", "status": "ready"},
    {"target": "gemini", "status": "skipped"},
    {"target": "vibe", "status": "skipped"}
  ],
  "managed": [
    {"target": "claude-code", "scope": "default", "entries": 0, "sources": []},
    {"target": "claude-code", "scope": "user", "entries": 0, "sources": []},
    {"target": "codex", "scope": "default", "entries": 0, "sources": []},
    {"target": "claude-desktop", "scope": "default", "entries": 1, "sources": ["standalone"]},
    {"target": "gemini", "scope": "default", "entries": 0, "sources": []},
    {"target": "gemini", "scope": "user", "entries": 0, "sources": []},
    {"target": "vibe", "scope": "default", "entries": 0, "sources": []}
  ],
  "manifest_errors": 0,
  "drift": [
    {
      "target": "claude-desktop",
      "scope": "default",
      "config_path": "/path/to/claude_desktop_config.json",
      "status": "ready",
      "stale": [],
      "missing": [],
      "orphaned": [],
      "issue": ""
    }
  ]
}
```

Drift entries appear per target+scope that has managed entries: `stale`
(materialized value differs from the manifest), `missing` (managed entry
deleted from the client config), and `orphaned` (no longer desired; the next
sync removes it). Drift is warning-level and never changes the exit code by
itself.

`madari doctor --json`:

```json
{
  "schema_version": 1,
  "command": "doctor",
  "servers_dir": "/path/to/madari/servers",
  "servers": [
    {
      "name": "stewreads",
      "enabled": true,
      "transport": "stdio",
      "clients": ["claude-desktop"],
      "command": "/abs/path/stewreads-mcp",
      "status": "warn",
      "issues": [
        {
          "severity": "warn",
          "code": "missing_required_env",
          "message": "missing required env key STEWREADS_API_KEY"
        }
      ]
    }
  ],
  "manifest_errors": [
    {"file": "/path/to/bad.toml", "message": "unknown key \"oops\""}
  ],
  "client_configs": [
    {
      "target": "claude-desktop",
      "path": "/path/to/claude_desktop_config.json",
      "exists": true,
      "status": "ready",
      "message": "ok"
    }
  ],
  "drift": [
    {
      "target": "claude-desktop",
      "scope": "default",
      "config_path": "/path/to/claude_desktop_config.json",
      "status": "warn",
      "stale": ["stewreads"],
      "missing": [],
      "orphaned": [],
      "issue": ""
    }
  ],
  "summary": {"total": 1, "ready": 0, "warning": 1, "error": 1, "skipped": 0}
}
```

`madari sync <client> --dry-run --json`:

```json
{
  "schema_version": 1,
  "command": "sync",
  "target": "claude-code",
  "config_path": "/path/to/.mcp.json",
  "dry_run": true,
  "added": ["stewreads"],
  "updated": [],
  "removed": [],
  "unchanged": [],
  "skipped": [],
  "unsupported_remote": [],
  "refused": [],
  "skills_added": [],
  "skills_updated": [],
  "skills_removed": [],
  "skills_unchanged": []
}
```

`madari run <client> --ring <ring> --dry-run --json`:

```json
{
  "schema_version": 1,
  "command": "run",
  "target": "codex",
  "rings": ["cloudsql-readonly"],
  "ready": true,
  "runner_available": true,
  "prompt_provided": true,
  "servers": [
    {
      "name": "cloud-sql",
      "transport": "http",
      "endpoint": "https://sqladmin.googleapis.com/mcp",
      "status": "ready",
      "auth": "bearer_token_env_var",
      "runtime_env": ["CLOUDSQL_MCP_TOKEN"],
      "rings": ["cloudsql-readonly"],
      "issues": []
    }
  ],
  "skills": [],
  "env": [
    {"key": "CLOUDSQL_MCP_TOKEN", "present": true, "servers": ["cloud-sql"]}
  ],
  "warnings": [],
  "errors": []
}
```

`madari ring list --json`:

```json
{
  "schema_version": 1,
  "command": "ring list",
  "rings": [
    {
      "name": "research",
      "members": ["arxiv", "stewreads"],
      "skills": ["release"],
      "description": "Research helpers",
      "contract": {
        "summary": "Collect source context and prepare a research brief.",
        "good_for": ["source collection", "evidence review"],
        "not_for": ["deployments", "database mutation"],
        "required_context": ["research question"],
        "optional_context": ["time window", "known source URLs"],
        "expected_outputs": ["findings summary", "sources inspected"]
      }
    }
  ]
}
```

`madari ring show <name> --json` wraps the same ring object:

```json
{
  "schema_version": 1,
  "command": "ring show",
  "ring": {
    "name": "research",
    "members": ["arxiv", "stewreads"],
    "skills": ["release"],
    "description": "Research helpers",
    "contract": {
      "summary": "Collect source context and prepare a research brief.",
      "good_for": ["source collection", "evidence review"],
      "not_for": ["deployments", "database mutation"],
      "required_context": ["research question"],
      "optional_context": ["time window", "known source URLs"],
      "expected_outputs": ["findings summary", "sources inspected"]
    }
  }
}
```

`madari skill list --json`:

```json
{
  "schema_version": 1,
  "command": "skill list",
  "skills": [
    {
      "name": "release",
      "description": "Release workflow",
      "package_path": "/path/to/madari/skills/release"
    }
  ]
}
```

`madari skill show <name> --json` wraps one skill object and includes the
managed package and `SKILL.md` paths:

```json
{
  "schema_version": 1,
  "command": "skill show",
  "skill": {
    "name": "release",
    "description": "Release workflow",
    "content_path": "/path/to/madari/skills/release/SKILL.md",
    "package_path": "/path/to/madari/skills/release",
    "skill_path": "/path/to/madari/skills/release/SKILL.md"
  }
}
```

`madari ring status --json` reports per target+scope:

```json
{
  "schema_version": 1,
  "command": "ring status",
  "targets": [
    {
      "target": "claude-code",
      "scope": "default",
      "rings": [
        {
          "name": "research",
          "exists": true,
          "members": ["arxiv", "stewreads"],
          "skills": ["release"],
          "owned": ["arxiv", "stewreads"],
          "skills_owned": ["release"],
          "pending": [],
          "skills_pending": [],
          "stale": [],
          "skills_stale": [],
          "missing_members": [],
          "missing_skills": []
        }
      ],
      "servers": [
        {"name": "arxiv", "sources": ["ring:research"]},
        {"name": "stewreads", "sources": ["ring:research", "standalone"]}
      ],
      "skills": [
        {"name": "release", "sources": ["ring:research"]}
      ]
    }
  ]
}
```

`madari doctor --json` additionally carries a `ring_issues` array
(`{target, scope, ring, severity, message}`): an attached ring whose file is
missing is error-level; a ring referencing a deleted manifest is a warning.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0    | Success; for `status`/`doctor`, no error-level findings |
| 1    | Usage or input error, runtime failure, sync conflict, or error-level findings (`status`/`doctor`) |

Exit codes are identical in text and JSON modes. In JSON mode stdout carries
only the JSON document; human-readable error summaries (e.g.
`doctor found 1 error(s)`) go to stderr.

## Help

```bash
madari help
madari help <command>
madari version
```

## Supported Client Targets

- `claude-desktop`
- `claude-code`
- `codex`
- `gemini`
- `vibe`

Skill attach/render targets are `claude-code`, `codex`, `gemini`, and `vibe`.
`claude-desktop` is not a skill target until it has a stable local skill
directory contract.

# CLI Reference

Quick command reference for day-to-day Madari usage.

## Server Lifecycle

```bash
madari add <name> --command /abs/path/to/server --client <client>
madari list
madari enable <name>
madari disable <name>
madari remove <name>
```

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
```

`--scope` applies to `claude-code` only: `project` (default) targets the
repo-scoped `.mcp.json`, `user` targets the user-scoped `~/.claude.json`.
Each scope tracks its managed entries independently. Servers carrying static
values for `[secret_env]` keys are refused per entry at project scope (other
servers keep syncing; a previously materialized secret entry is scrubbed)
and must sync with `--scope user`.

## Rings

```bash
madari ring create research --member stewreads --member arxiv --description "Research helpers"
madari ring list
madari ring show research
madari ring attach research claude-code
madari ring attach research claude-code --scope user
madari ring detach research claude-code
```

Rings are named capability sets of servers stored at
`<config-root>/rings/<name>.toml` (see the manifest spec). Members reference
registry entries by name and must exist when the ring is created.

Attach records a `ring:<name>` ownership source for every member and
materializes the eligible ones; detach releases the source, and an entry
leaves the client config only when no source owns it. Ownership is
reference-counted: overlapping rings and standalone+ring combinations resolve
in any attach/detach order. Disabled, secret-refused, or missing members stay
owned but absent until they become eligible. Ring membership edits (including
snapshot imports) reconcile on the next sync, attach, or detach. Attaching
onto an entry madari does not manage is refused, even when values match.

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

## JSON Output

`list`, `status`, `doctor`, `sync --dry-run`, `ring list`, and `ring show`
accept `--json` and emit a single JSON document on stdout with nothing else.
Every payload carries the envelope fields `schema_version` (currently `1`)
and `command`. Field additions are backward-compatible; renames or removals
bump `schema_version`. List-valued fields are always present (empty arrays,
never `null`).

```bash
madari list --json
madari status --json
madari doctor --json
madari sync claude-code --dry-run --json
madari ring list --json
madari ring show research --json
```

`sync --json` requires `--dry-run`; the apply-mode output contract is not yet
defined.

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
    {"target": "claude-desktop", "status": "ready"}
  ],
  "managed": [
    {"target": "claude-code", "scope": "default", "entries": 0, "sources": []},
    {"target": "claude-code", "scope": "user", "entries": 0, "sources": []},
    {"target": "claude-desktop", "scope": "default", "entries": 1, "sources": ["standalone"]}
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
  "refused": []
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
      "description": "Research helpers"
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
    "description": "Research helpers"
  }
}
```

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

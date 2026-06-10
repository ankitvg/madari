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
```

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

`list`, `status`, `doctor`, and `sync --dry-run` accept `--json` and emit a
single JSON document on stdout with nothing else. Every payload carries the
envelope fields `schema_version` (currently `1`) and `command`. Field
additions are backward-compatible; renames or removals bump `schema_version`.
List-valued fields are always present (empty arrays, never `null`).

```bash
madari list --json
madari status --json
madari doctor --json
madari sync claude-code --dry-run --json
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
    {"target": "claude-code", "entries": 0, "sources": []},
    {"target": "claude-desktop", "entries": 1, "sources": ["standalone"]}
  ],
  "manifest_errors": 0
}
```

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
  "skipped": []
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

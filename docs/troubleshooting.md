# Troubleshooting

Common issues and quick fixes for `madari doctor` and `madari status`.

## `command path must be absolute`

Cause:
- The manifest command is relative (for example `stewreads-mcp`).

Fix:
```bash
madari add <name> --command /absolute/path/to/binary --client <client>
```

Or update an existing server:
```bash
madari remove <name>
madari add <name> --command /absolute/path/to/binary --client <client>
```

## `command path does not exist`

Cause:
- Binary moved, uninstalled, or path is incorrect.

Fix:
1. Confirm binary path with:
```bash
which <binary-name>
```
2. Re-register using the resolved absolute path:
```bash
madari remove <name>
madari add <name> --command /resolved/path --client <client>
```

## `command path is not executable` (non-Windows)

Cause:
- File exists but does not have execute bits.

Fix:
```bash
chmod +x /absolute/path/to/binary
madari doctor
```

## `missing required env key ...`

Cause:
- A key in `[required_env]` is not present in your current shell environment.

Fix:
```bash
export YOUR_KEY=your_value
madari doctor
```

Tip:
- Persist environment variables in your shell profile if needed.

## `config file not found`

Cause:
- Client config file has not been created yet or path is overridden incorrectly.

Fix:
1. Verify client default path with:
```bash
madari clients
```
2. If using an override, pass the right path:
```bash
madari doctor --client-config claude-desktop=/path/to/claude_desktop_config.json
```

## `invalid JSON` / `invalid mcpServers object`

Cause:
- Client config file is malformed JSON or `mcpServers` is not an object.

Fix:
1. Backup the config file.
2. Repair JSON syntax.
3. Re-run diagnostics:
```bash
madari doctor
madari status
```

## `unknown client config target`

Cause:
- Unsupported target passed to `--client-config`.

Fix:
- Use one of the supported targets shown by:
```bash
madari clients
```

## `sync conflict with unmanaged server`

Cause:
- Client config already has an entry with the same name that Madari does not manage.

Fix options:
1. Rename the Madari server.
2. Remove/rename the unmanaged client entry manually.
3. Align unmanaged entry values with Madari-managed values before sync.

## Reset diagnostics loop

If output is noisy and you want a clean cycle:
```bash
madari status
madari doctor
madari sync <client> --dry-run
```

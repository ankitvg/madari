# Troubleshooting

Use this guide when configuring or running an agent. Start with
`madari ring show <name>` and `madari run <client> --ring <name> --dry-run -- <task>`
to inspect the selected configuration. `doctor`, `status`, and `ring status`
inspect local readiness and drift; they do not monitor agent processes or
validate answers.

## The selected client cannot execute a run

Only `madari run codex` executes tasks today. Other clients accept `--dry-run`
where their capabilities support the selected ring. To use those capabilities
in an existing client, use `ring attach`, then open the client yourself.
Claude Desktop cannot attach or plan rings containing skills. Check the
[support matrix](../README.md#supported-clients).

## The launch plan is not ready

Read the plan's `errors` and per-member issues, or add `--json` to a dry run.
Common causes are missing or disabled registry members, missing runtime env
keys, an unsupported transport/auth/skill target, or an unsupported required
policy. Correct the named requirement and preview again. A run blocks rather
than silently dropping a selected capability.

For `codex executable not found in PATH`, install the Codex CLI and make it
available to the shell running Madari. Codex previews check for the executable
too. If an admin skill root contains skills, current Codex preflight blocks
because Madari cannot guarantee ring-only skill isolation. Inspect the named
root and resolve it with its owner rather than deleting managed system skills.

Access-bearing runs require a validated stable Codex CLI 0.139.x release. A newer
or prerelease version may therefore be rejected. Use the validated client for
that policy; removing a required policy changes the intended access guarantees.
See [Capability Policy V1](cli-reference.md#capability-policy-v1).

## The preview passes but execution fails or the answer is wrong

A preview does not call the model, authenticate remote services, or run tools.
Check the client's authentication, service credentials, and execution errors.
For poor answers, inspect the registered skill and contract, supply the expected
context, and compare the output with source evidence. Refresh an edited skill
using `madari skill update <name> --dir <package-directory>`.

Skills and contracts guide behavior; they do not guarantee answer quality.
A read-only client filesystem sandbox also does not prevent an MCP tool from
mutating an external service. Use appropriate service permissions and supported
capability policies for access restrictions.

## An attached ring does not behave like a Madari run

Attachment adds supported tools and skills to the existing client's environment.
It does not start another agent, remove unrelated capabilities, or install the
ring's contract preamble. Supply the task and context in the client yourself.
`ring render` includes only MCP config, so it cannot transfer a skill or contract.
An ephemeral run leaves no attachment; its absence from `ring status` is expected.

## A tutorial name already exists

`skill add` and `ring create` refuse to overwrite existing entries. Inspect them
with `skill show` or `ring show`. Use `skill update` to refresh a skill you own,
reuse the existing ring if appropriate, or choose a new name. The README and
[full walkthrough](configurable-agents.md) both use `briefing`; they are
alternative starting points.

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

Matching values do not grant Madari ownership. Inspect and back up the existing
entry before removing or renaming it; Madari will not adopt an unmanaged entry.

## Reset diagnostics loop

If output is noisy and you want a clean cycle:
```bash
madari status
madari doctor
madari sync <client> --dry-run
```

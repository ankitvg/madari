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

Server manifests can also carry an optional portable `[access]` profile. The
profile fields are `allowed_tools`, `denied_tools`, `oauth_scopes`,
`default_approval`, and `[access.tool_approvals]`. Access profiles are stored on
the server rather than duplicated into rings. See the manifest spec for strict
validation and presence semantics.

Both `madari add` and `madari install` can create a profile with repeatable
`--allow-tool`, `--deny-tool`, and `--tool-approval TOOL=BEHAVIOR` flags plus
`--default-tool-approval BEHAVIOR`. Remote-capable `madari add` also accepts
repeatable `--oauth-scope` values. Direct TOML or snapshot editing remains the
way to express an explicit empty list or table clear.

## Capability Policy V1

The portable server shape is:

```toml
[access]
allowed_tools = ["issues.get", "issues.list"]
denied_tools = ["issues.delete"]
oauth_scopes = ["issues:read"]
default_approval = "always-prompt"

[access.tool_approvals]
"issues.get" = "always-allow"
```

The supported approval values are `inherit`, `automatic`, `always-prompt`, and
`always-allow`. Raw client-native enum values are rejected. `inherit` explicitly
clears an approval override. Approval behavior controls client prompts and is
not an authorization boundary.

Presence is preserved across TOML, snapshots, the store, and JSON output. No
`[access]` section means no Madari policy declaration. An absent field preserves
the corresponding native field during persistent sync; render and run omit it.
Explicitly empty tool/scope arrays clear that native override, a present empty
`[access.tool_approvals]` table clears the per-tool table, and an empty or absent
allowlist is unbounded.

Rings request mandatory enforcement separately:

```toml
[policy]
enforcement = "required"
```

`madari ring create ... --enforcement required` writes the same ring policy.

A required ring must have an explicit non-empty allowlist on every server. All
members must exist, be enabled, target the selected client, and compile exactly
for the selected operation surface. Missing, disabled, wrong-target, unbounded,
unsupported, and unrepresentable members are errors; Madari never substitutes
an advisory prompt or a weaker native control.

Policy support is declared independently for persistent sync/attach, render,
and run. Codex compiles all five V1 fields on all three surfaces. Every policy
surface for other targets remains unsupported.
Required sync or attach fails before config, managed state, or skill mutation;
required render fails before partial output; and required run fails before skill
materialization or client execution. Detach remains available for cleanup.
Manifests without `[access]` make no portable access declaration and rings
without required enforcement make no mandatory access claim. Bounded Codex run
defaults apply independently.

Access profiles outside required rings are stored, validated, exported,
imported, reported, and compiled when the selected target surface supports
them. They do not turn an advisory ring into a mandatory enforcement claim.

`oauth_scopes` is requested and client-configured; it does not prove that an
OAuth provider granted the scopes or that a token contains them.

## Bounded Execution Policy

Rings may declare an optional complete execution contract:

```toml
[policy.execution]
ambient_env = "deny"
sandbox = "read-only"
max_duration = "15m"
credential_exposure = "run-process"
```

If `[policy.execution]` is present, all four fields are required and those are
the only supported string values except `max_duration`, which accepts any
positive Go duration such as `30s`, `15m`, or `1h30m`. Partial sections,
unknown fields, surrounding duration whitespace, zero/negative durations, and
other policy values are rejected.

Execution policy and enforcement are independently optional. A ring can carry
the section above as an advisory boundary, or combine it with:

```toml
[policy]
enforcement = "required"
```

to require every declared execution guarantee. `madari ring create` exposes the
same shape; all four execution flags must be provided together:

```bash
madari ring create bounded \
  --member remote-readonly \
  --enforcement required \
  --ambient-env deny \
  --sandbox read-only \
  --max-duration 15m \
  --credential-exposure run-process
```

Every Codex run applies the safe effective defaults even when no ring declares
the section: denied ambient environment, read-only Codex sandbox, 15-minute
maximum, and run-process credential exposure. Multiple selected rings use the
shortest declared maximum; when none declares one, the maximum is the default
15 minutes. Run-level `--max-duration` can only shorten that result.

Required execution policy blocks rings with local stdio members because the
Codex sandbox does not isolate the filesystem or network of a separately spawned
stdio server. Advisory execution policy can run, but those confinement controls
are reported as degraded and unverified.

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

Codex compiles `allowed_tools`, `denied_tools`, `oauth_scopes`,
`default_approval`, and per-tool approvals into `enabled_tools`,
`disabled_tools`, `scopes`, `default_tools_approval_mode`, and
`tools.<tool>.approval_mode`. Routine sync preserves undeclared native policy
fields, including on core command or URL updates. Explicit empty declarations
and portable `inherit` remove the corresponding native override. A required
ring refuses unknown behavior-affecting native fields or native approval values
that Madari cannot prove equivalent instead of rewriting them. Policy-only
differences are reported separately by doctor/status while remaining a subset
of ordinary stale entries.

`vibe` sync targets Vibe's user config (`$VIBE_HOME/config.toml`, or
`~/.vibe/config.toml` when `VIBE_HOME` is unset). Static `[env]` values are
written into `[[mcp_servers]]` stdio entries. Existing unmanaged HTTP,
streamable HTTP, or hand-managed stdio entries are preserved and never adopted.

## Rings

```bash
madari ring create research --member stewreads --member arxiv --skill release --description "Research helpers"
madari ring create thinking --member stewreads --description "Server-only helper"
madari ring create bounded --member remote-readonly --ambient-env deny --sandbox read-only --max-duration 15m --credential-exposure run-process
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
madari run codex --ring thinking --max-duration 5m -- "Use this ring"
madari run codex --ring research --ring release --max-duration 10m --dry-run --json -- "Use both rings"
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

Ring policy is distinct from the advisory contract. A ring file may set
`[policy] enforcement = "required"`, but each referenced server remains the
source of truth for its own `[access]` profile. Codex required attach, render,
and run are supported when every member compiles exactly. Unsupported target
surfaces fail during preflight. The optional `[policy.execution]` section is a
run-only boundary and can be advisory or combined with required enforcement.
It does not affect attach, detach, sync, or render.

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
Policy-required render does not use the legacy omit-and-warn behavior: if any
restriction cannot be represented exactly, it fails before writing partial
stdout. Codex render emits the five native policy fields, with dotted server and
tool names quoted as literal TOML keys. Other render targets remain unsupported
for required policy.
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
madari run <client> --ring <ring> [--ring <ring> ...] [--max-duration <duration>] [--dry-run] -- <prompt>
madari run codex --ring cloudsql-readonly --max-duration 5m -- "Who are the top 5 ebook creators?"
madari run codex --ring cloudsql-readonly --ring research --max-duration 10m --dry-run --json -- "Inspect the combined plan"
```

`madari run` is the launch primitive for using one or more rings with a target
client. Every Codex execution uses a validated stable 0.139.x CLI and starts
`codex exec --strict-config --ephemeral --ignore-user-config
--skip-git-repo-check --sandbox read-only` with selected ring MCP servers
injected as required config overrides from an isolated working root after
clearing inherited MCP server config. The version probe itself runs with an
isolated home and temporary directory plus the platform baseline, not caller
credentials. The selected executable path, version, and binary hash are frozen
in the launch artifact and the binary is checked before execution.

Selected ring skills are materialized under the temporary Codex run root as
project skills for that session. Stdio servers keep the original working
directory through `mcp_servers.<id>.cwd`. The Codex process receives isolated
`HOME`/`USERPROFILE`, `CODEX_HOME`, and temporary paths; on Windows its
application-data paths are isolated as well. The caller's ambient environment is
not inherited. Madari supplies only a documented platform baseline and values
explicitly named by selected manifests through static `[env]`, `[required_env]`,
`[secret_env]`, or `bearer_token_env_var` declarations.

Declared runtime values and the caller's `auth.json` bytes are frozen when the
immutable artifact is compiled. The frozen auth file is materialized owner-only
inside the isolated `CODEX_HOME`. Codex is also configured with
`shell_environment_policy.inherit = "none"` (and login shells disabled), so MCP
credential values are not automatically exposed to shell subprocesses. Static,
runtime, secret, and bearer credentials are still visible to the Codex run
process or the MCP recipient for which they were declared. Madari does not claim
a credential broker, per-invocation lease, or token TTL. Other clients remain
dry-run only for now.

The planner resolves every selected ring, deduplicates shared server and skill
members, validates the selected client can express each required capability,
captures declared runtime values, and compiles one immutable launch artifact.
The artifact owns normalized ring and manifest snapshots, complete skill
packages, frozen auth and environment inputs, the prompt preamble, Codex
overrides, deterministic component hashes, and a receipt-safe launch digest.
Execution consumes only the artifact and does not reread registry, auth, or
caller-environment state. Run never writes client config, creates managed state,
or permanently materializes skill packages.

The effective execution policy always denies ambient environment inheritance,
uses the read-only Codex sandbox, exposes declared credentials at run-process
scope, and imposes a finite duration. If no selected ring declares
`[policy.execution]`, the maximum is 15 minutes. Otherwise Madari takes the
shortest duration across the selected declarations.
`--max-duration` accepts a positive Go duration and may shorten, but never
extend, the result. Timeout and cancellation terminate the contained process
tree on supported Unix and Windows platforms.

Unlike `ring render`, `run` is fail-closed. A disabled member, missing member,
unsupported remote transport or auth mode, missing runtime env key, or
unsupported skill target blocks the plan instead of silently omitting that
capability.
Codex execution also blocks when a non-empty admin/system skill root is
present, because Madari cannot guarantee ring-only skill isolation in that
case.
Policy-required run adds a stricter preflight boundary: every declared member
restriction must compile exactly before any temporary skill package is
materialized or the client starts. Codex maps every V1 access field into the
single ephemeral `mcp_servers={...}` override.
`--strict-config` is an additional config-parser safeguard, not proof that
nested MCP policy fields retain their semantics outside the validated range.

Codex's read-only sandbox governs Codex-managed shell commands. It does not
sandbox a local stdio MCP server that Codex spawns as a separate process, so
Madari reports stdio filesystem and network confinement as unverified. A ring
that combines required enforcement with declared execution policy and a stdio
member is blocked. The same limitation under advisory execution policy is
allowed but classified as degraded and unverified. The process-tree boundary is
designed for ordinary timeout and cancellation cleanup; it is not adversarial
filesystem, network, container, or kernel containment.

Dry-run text and JSON report each server's portable declared policy, the
Codex-native effective policy, support state, and enforcement classification.
If a server is shared, any selected required ring wins and is listed in
`required_by`. The control labels are intentionally distinct: tool filtering is
client-enforced; OAuth scopes are requested/client-configured and
provider-unverified; approval modes are client controls rather than an
authorization boundary; contracts and skills remain advisory instructions.
Each authority entry reports `enforced_by` as `provider`, `client`, `process`,
`advisory`, or `none`, and `verification` as `observed`, `configured`, or
`unverified`.
For non-Codex dry-runs, `execution.supported` is `false`; requested execution
policy remains visible, but effective controls are reported as
none/unverified/degraded (or blocked when required), never as Codex-enforced.

Versioned opt-in execution receipts are planned separately. There is no receipt
flag or receipt file in the current run surface.

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
`ring:<name>` ownership sources. Skills are not written into MCP client
configs. `madari run --dry-run` validates selected ring skills, and
`madari run codex` temporarily materializes them as project skills for the
session without recording attachment state.

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
definitions, and skill packages. Export writes V11 with `servers`, `rings`, and
`skills`. V10 added server access profiles and ring policy; V11 adds bounded
ring execution policy. V1 through V10 remain importable. Any snapshot older than
V11 that carries execution policy is rejected so ambient-environment, sandbox,
lifetime, or credential-exposure requirements cannot be discarded silently.
The equivalent older-version checks remain for fields introduced before V11.
Snapshot version 6 introduced deterministic skill package file entries with
relative paths, base64 content, and file modes; older snapshots with a single
skill `content` string remain importable and are converted to package-backed
`SKILL.md`. Import validates the complete resulting server/ring graph before its
first write. It is a dry-run by default; `--apply` adds or updates listed
servers, rings, and skills, never deletes entries absent from the snapshot, and
never attaches or syncs imported rings.
Ring membership changes converge through the normal reconciliation pass on
the next sync, attach, or detach.

## JSON Output

`list`, `status`, `doctor`, `sync --dry-run`, `run --dry-run`, `ring list`,
`ring show`, `ring status`, `skill list`, and `skill show` accept `--json` and emit a
single JSON document on stdout with nothing else.
Every payload carries the envelope fields `schema_version` (currently `1`)
and `command`. Field additions are backward-compatible; renames or removals
bump `schema_version`. Except for presence-sensitive access-profile fields,
list-valued fields are always present (empty arrays, never `null`) when their
containing object is emitted. Optional objects such as server `access`, ring
`contract`, and ring `policy` are omitted when absent. Inside `access`, absent
fields are omitted while explicitly cleared arrays or the per-tool table are
emitted as `[]` or `{}` so presence semantics survive the JSON round trip.

```bash
madari list --json
madari status --json
madari doctor --json
madari sync claude-code --dry-run --json
madari run codex --ring research --max-duration 5m --dry-run --json -- "Use this ring"
madari ring list --json
madari ring show research --json
madari ring status --json
madari skill list --json
madari skill show release --json
```

`sync --json` requires `--dry-run`; the apply-mode output contract is not yet
defined. `run --json` also requires `--dry-run`; non-dry-run execution streams
the target client output unchanged.

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
      "access": {
        "allowed_tools": ["books.create"],
        "default_approval": "always-prompt"
      },
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
      "policy_stale": [],
      "missing": [],
      "orphaned": [],
      "issue": ""
    }
  ]
}
```

Drift entries appear per target+scope that has managed entries: `stale`
(materialized value differs from the manifest), `policy_stale` (the subset of
`stale` whose declared access policy differs from the target's materialized
policy), `missing` (managed entry deleted from the client config), and
`orphaned` (no longer desired; the next sync removes it). Policy drift covers
client-enforced tool filtering, requested/client-configured OAuth scopes, and
client approval controls. It does not prove scopes were granted by the OAuth
provider, and approval behavior is not an authorization boundary. Drift is
warning-level and never changes the exit code by itself.

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
      "policy_stale": [],
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
  "policy_updated": [],
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

For Codex, `policy_updated` is the sorted subset of `updated` whose declared
access fields differ from the native configuration. Undeclared preserved fields
do not appear as policy drift.

`madari run <client> --ring <ring> [--max-duration <duration>] --dry-run --json`:

```json
{
  "schema_version": 1,
  "command": "run",
  "target": "codex",
  "rings": ["cloudsql-readonly"],
  "ready": true,
  "runner_available": true,
  "prompt_provided": true,
  "policy_required": true,
  "policy_controls": {
    "tool_filtering": "client-enforced",
    "oauth_scopes": "requested/client-configured/provider-unverified",
    "approvals": "client-control/not-authorization",
    "instructions": "contracts-and-skills-advisory"
  },
  "launch_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "policy_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "content_hashes": {
    "rings": [{"name": "cloudsql-readonly", "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],
    "servers": [{"name": "cloud-sql", "sha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}],
    "skills": []
  },
  "authority": {
    "requested": [
      {"control": "ambient-environment", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "client-sandbox", "enforced_by": "client", "verification": "configured", "classification": "exact"},
      {"control": "credential-exposure", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "max-duration", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "mcp-tool-filtering", "enforced_by": "client", "verification": "configured", "classification": "exact"}
    ],
    "effective": [
      {"control": "ambient-environment", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "client-sandbox", "enforced_by": "client", "verification": "configured", "classification": "exact"},
      {"control": "credential-exposure", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "max-duration", "enforced_by": "process", "verification": "configured", "classification": "exact"},
      {"control": "mcp-tool-filtering", "enforced_by": "client", "verification": "configured", "classification": "exact"}
    ]
  },
  "execution": {
    "ambient_env": "deny",
    "sandbox": "read-only",
    "max_duration": "15m0s",
    "credential_exposure": "run-process",
    "supported": true,
    "declared": true,
    "required": true,
    "stdio_confinement": "not-applicable"
  },
  "servers": [
    {
      "name": "cloud-sql",
      "transport": "http",
      "endpoint": "https://sqladmin.googleapis.com/mcp",
      "status": "ready",
      "auth": "bearer_token_env_var",
      "runtime_env": ["CLOUDSQL_MCP_TOKEN"],
      "rings": ["cloudsql-readonly"],
      "issues": [],
      "policy": {
        "declared": {"allowed_tools": ["query"]},
        "ring_enforcement": "required",
        "required_by": ["cloudsql-readonly"],
        "effective": {
          "enabled_tools": ["query"],
          "disabled_tools": [],
          "requested_oauth_scopes": [],
          "tool_approval_modes": {}
        },
        "support_state": "supported",
        "enforcement_classification": "exact"
      }
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
      "policy": {
        "enforcement": "required",
        "execution": {
          "ambient_env": "deny",
          "sandbox": "read-only",
          "max_duration": "15m",
          "credential_exposure": "run-process"
        }
      },
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
    "policy": {"enforcement": "required"},
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

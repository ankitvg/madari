# ADR 004: Bounded Codex Execution Boundary

- Status: Accepted
- Date: 2026-07-10

## Context

Capability Policy Contract V1 defines which MCP access controls Madari can
compile for a target, but an ephemeral run also depends on mutable registry
files, caller environment, client authentication, process lifetime, and the
behavior of child processes. Previously, planning and execution reread some of
those sources at different times, inherited the caller environment, and had no
finite lifetime. That made it possible for launch inputs to change after
preflight and made the effective credential and process authority difficult to
describe honestly.

Codex provides a read-only sandbox for its own shell commands, but a local stdio
MCP server is a separately spawned process. The Codex sandbox is not evidence
that the server's filesystem or network access is confined. Madari must not turn
that gap into an exact-enforcement claim.

## Decision

### Immutable launch artifact

Planning compiles one immutable artifact containing normalized selected rings,
server manifests, complete skill packages, the prompt, Codex overrides, frozen
declared environment values, frozen Codex authentication, the validated client
identity, execution policy, authority explanations, and deterministic hashes.
Artifact fields are private and accessors return defensive copies.

Execution consumes only that artifact. It does not reread registry files,
`auth.json`, or caller environment values. Editing a manifest, ring, or skill
after compilation cannot broaden the pending run. The public launch digest is a
redacted configuration identity: it excludes the prompt and prompt hash,
runtime environment values, authentication bytes, output, URLs, header values,
and command arguments. It is evidence about what Madari configured, not a
cryptographic attestation of the host or provider.

### Execution-policy contract

A ring may declare:

```toml
[policy.execution]
ambient_env = "deny"
sandbox = "read-only"
max_duration = "15m"
credential_exposure = "run-process"
```

The section is optional, but when present all four fields are required. The only
supported values are `deny`, `read-only`, and `run-process`; `max_duration` must
be a positive Go duration without surrounding whitespace. Partial policies,
unknown fields, and unsupported values fail validation.

Execution policy and enforcement are independent. A ring may declare advisory
execution policy without an `enforcement` key. Adding:

```toml
[policy]
enforcement = "required"
```

requires Madari to block rather than run with a weaker execution guarantee.

Safe effective defaults apply to every Codex run, including rings without an
execution section:

- ambient environment denied;
- read-only Codex sandbox;
- 15-minute maximum duration; and
- credentials exposed at run-process scope.

Multiple selected rings compose by taking the shortest declared duration among
them. If none declares one, the maximum is the default 15 minutes. `madari run
--max-duration` may shorten that result but may never extend it.

### Declared-only environment and credentials

The Codex process environment starts empty and is populated with a documented
platform baseline, isolated paths, and explicitly declared server variables.
Madari creates isolated `HOME`, `USERPROFILE`, `CODEX_HOME`, `TMPDIR`, `TEMP`,
and `TMP` values; Windows application-data paths are isolated as well. Caller
values for those paths are never forwarded into stdio server configuration.

When set on the host, the baseline contains only these keys:

- Unix: `PATH`, `SHELL`, `USER`, `LOGNAME`, `LANG`, `LC_ALL`, `LC_CTYPE`,
  `TERM`, `TZ`, and `__CF_USER_TEXT_ENCODING`.
- Windows: `PATH`, `PATHEXT`, `COMSPEC`, `SYSTEMROOT`, `SYSTEMDRIVE`,
  `USERNAME`, `USERDOMAIN`, `PROGRAMFILES`, `PROGRAMFILES(X86)`,
  `PROGRAMW6432`, `PROGRAMDATA`, `POWERSHELL`, and `PWSH`.

Unset baseline keys stay absent. Generated isolation keys and explicitly
declared server variables are added separately; no other caller variables are
inherited.

Static stdio environment values remain scoped to the declared server. Values
named by `[required_env]`, `[secret_env]`, and `bearer_token_env_var` are captured
once during planning and frozen in the artifact. The caller's Codex `auth.json`
is also frozen, then materialized owner-only inside the isolated `CODEX_HOME`.

Codex receives a generated `shell_environment_policy` with `inherit = "none"`,
an explicit include/set baseline, and login shells disabled. This prevents MCP
credential variables from being automatically inherited by Codex shell
subprocesses. The native behavior is documented in the official
[Codex shell environment policy reference](https://learn.chatgpt.com/docs/config-file/config-advanced#shell-environment-policy).

`credential_exposure = "run-process"` is deliberately literal. Bearer tokens,
static server credentials, and declared runtime credentials remain visible to
the Codex run process or their declared MCP recipient. Madari does not claim a
credential broker, per-invocation release, provider-side revocation, or token
TTL.

### Codex client boundary

Every Codex run, with or without an `[access]` profile, requires a validated
stable 0.139.x CLI. The version probe runs from an isolated directory with only
the platform baseline and generated home/temporary paths. Planning records the
resolved executable path, parsed version, and binary hash. Execution uses that
exact path and checks the binary hash before materializing skills or starting
the client.

Every run uses `--strict-config`, `--ephemeral`, `--ignore-user-config`,
`--skip-git-repo-check`, and `--sandbox read-only`. Strict config is a parser and
configuration safeguard; it is not evidence of provider authorization or
operating-system containment.

### Bounded process lifetime

The effective maximum duration covers the Codex process tree. Timeout and
cancellation terminate the contained tree and reap the root process. Unix uses
an isolated process session plus same-session and PPID-ancestry snapshots, so
observed descendants that create another process group or session are still
reached. Windows starts the process suspended, assigns it to a kill-on-close Job
Object, then resumes it. On Unix, `SIGINT`, `SIGTERM`, `SIGHUP`, and `SIGQUIT`
request the same bounded cancellation path. Madari returns whether termination
of the observed containment set completed or cleanup was incomplete; the current
CLI does not persist that result.

This mechanism is intended to stop ordinary runaway commands and descendants.
It is not adversarial containment and does not claim protection against a
hostile kernel, privileged process, independently running external service, or
a Unix daemon that fully double-forks out of both the session and observed PPID
graph between snapshots.

### Stdio confinement and authority reporting

Codex's read-only sandbox does not sandbox a separately spawned local stdio MCP
server. Madari therefore reports stdio filesystem and network confinement with
`enforced_by = "none"` and `verification = "unverified"`.

- A required execution policy with any stdio member blocks before skill
  materialization or client execution.
- An advisory execution policy may proceed, but stdio confinement is classified
  as degraded and unverified.
- Remote MCP authorization remains enforced by the provider and is not made
  observable merely by this local boundary.

Other requested and effective controls continue to identify their enforcement
owner as `provider`, `client`, `process`, `advisory`, or `none`, and their
verification as `observed`, `configured`, or `unverified`.

### Compatibility

Snapshot V11 adds ring execution policy. V1 through V10 remain importable.
Snapshots older than V11 that contain execution policy are rejected so an older
version cannot silently discard environment, sandbox, duration, or credential
requirements.

The registry remains local-first and static. This decision adds a bounded run
path, not a daemon, proxy, generic credential-provider framework, or background
supervisor. Versioned opt-in execution receipts are planned separately and are
not part of the current CLI surface.

## Consequences

- Registry mutation between planning and execution cannot broaden a launch.
- Ambient caller credentials no longer reach Codex implicitly.
- All Codex runs have finite lifetime and cross-platform process-tree cleanup.
- Required policy fails closed when local stdio isolation cannot be enforced.
- Advisory stdio runs remain useful while reporting their confinement limits
  honestly.
- Credentials are less ambient but still visible at run-process scope; stronger
  isolation requires a real broker, container, or operating-system boundary.
- Madari's evidence remains self-reported configuration and observation, not
  cryptographic attestation.

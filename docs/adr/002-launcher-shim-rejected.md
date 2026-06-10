# ADR 002: Launcher Shim Rejected

- Status: Accepted (rejection recorded)
- Date: 2026-06-10

## Context

When designing secret handling and the upcoming Rings feature, one candidate
architecture was a launcher shim: instead of materializing real server
commands into client configs, every managed entry would point at `madari` as
its command (e.g. `madari launch <name>`), and madari would resolve the
manifest, inject env (including secrets from a local store), and exec the
real server at launch time.

The shim would have solved secret placement (secret values never written to
any client config) and made drift structurally impossible (configs only ever
reference madari).

## Decision

Reject the launcher shim. Materialized sync — writing real commands, args,
and env into client configs — remains the only sync mode.

Rationale:

- **Survivability.** Uninstalling or breaking madari would break every
  managed server in every client at once. With materialized sync, client
  configs keep working if madari disappears; madari is a manager, not a
  runtime dependency.
- **Transparency.** A user reading their client config should see what
  actually runs. A shim hides the real command behind an indirection that
  must be resolved through madari's own state.
- **Cost/benefit.** The shim pays the runtime-presence costs of a
  multiplexer (madari in every launch path, version skew between shim and
  state, debugging through an extra layer) without delivering mux payoffs
  such as connection sharing or fan-out.

The problems the shim would have solved are addressed by placement policy
and detection instead:

- Secrets: manifests mark secret env keys (`[secret_env]`); sync refuses to
  materialize their static values into repo-scoped configs and routes them
  to user-scoped configs (`madari sync claude-code --scope user`).
- Drift: `status`/`doctor` diff materialized client entries against
  manifests and report stale, missing, and orphaned entries with the sync
  command that reconciles them.

## Revisit Trigger

Revisit this decision if and when a multiplexer/proxy design lands on the
roadmap. A mux changes the cost/benefit calculus because madari would already
be in the runtime path, and the shim's marginal costs would largely be paid.

## Consequences

- No background daemon, proxy, or launch-time indirection in the current
  architecture (matches the non-goals in AGENTS.md).
- Secret env values can exist in user-scoped client configs; users who need
  stronger secret isolation should rely on runtime-provided env
  (`[required_env]`) instead of static `[env]` values.
- Drift is detected, not prevented; reconciliation stays an explicit
  `madari sync` away.

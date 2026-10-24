# Client Adapters

This package defines the shared `ClientAdapter` contract used by sync targets.

Purpose:
- provide one stable interface for client-specific sync behavior
- keep client config translation inside each adapter
- keep CLI sync wiring independent of client config schema details

Design rationale:
- see `docs/adr/001-client-adapter-interface.md`

## Adapter Checklist

When adding a new adapter:

1. Add a package for the target under `internal/clients/<target>/`.
2. Define a stable target id (used by `madari sync <client>`).
3. Implement an adapter type that satisfies `clients.ClientAdapter`.
4. Implement `DefaultConfigPath()` for the client default location.
5. Implement `Sync()` with `clients.SyncOptions` and `clients.SyncResult`.
6. Enforce safety invariants from `adapter.go` comments:
   - preserve unknown config blocks
   - fail closed on parse errors
   - dry-run must not mutate config/state
   - apply mode should backup + atomic write
7. Return errors wrapping `clients.ErrConflict` for unmanaged name collisions.
8. Wire the new adapter into command dispatch (separate change if needed).

## Required Tests

Minimum expected tests for each adapter:

1. Dry-run computes plan and does not mutate config or state files.
2. Add/update/remove lifecycle works across repeated sync runs.
3. Existing unmanaged entry with same name but different value returns conflict:
   `errors.Is(err, clients.ErrConflict)` should be true.
4. Unknown top-level config blocks are preserved after apply.
5. Managed state is updated consistently with desired managed entries.

## Client Compatibility Files

An adapter whose output depends on client CLI behavior keeps a
`compat.toml` in its package listing exactly what Madari relies on: CLI flags,
config keys, accepted values, and the supported version floor. A test checks
Madari's real output against that file in both directions, so adding,
renaming, or dropping an emitted flag or key fails until the file is updated.
When a client releases a new version, review its changes against this list.

- Codex: `codex/compat.toml`, checked by `cmd/madari/codex_compat_test.go`.
  Madari reads the policy-run version floor from this file.

# ADR 001: Client Adapter Interface

- Status: Accepted
- Date: 2026-02-22

## Context

Madari sync behavior is client-specific because MCP config locations and schemas
vary by client. Before this ADR, sync logic lived per client package with no
shared adapter contract at the boundary used by command flow.

Current supported sync targets:
- `claude-desktop`
- `claude-code`

Near-term need:
- introduce a clear abstraction for client sync targets
- keep scope small and avoid over-designing for not-yet-supported clients

## Decision

Introduce a shared `ClientAdapter` contract in `internal/clients`:

- `Target() string`
- `DefaultConfigPath() (string, error)`
- `Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error)`

Also introduce shared sync data types:
- `SyncOptions`
- `SyncResult`
- `ErrConflict`

Contract invariants are documented in `internal/clients/adapter.go`:
- preserve unknown config blocks
- fail closed on parse errors
- dry-run must not mutate config or managed state
- apply mode should perform backup + atomic write
- unmanaged name collisions should return errors wrapping `ErrConflict`

## Alternatives Considered

1. Keep no shared interface (status quo)
- Rejected: duplicates contract semantics and makes future extension harder.

2. Build a broad, future-heavy abstraction for file + hosted/remote clients now
- Rejected: adds complexity before real requirements exist.

3. Introduce only shared types and no interface
- Rejected: still leaves adapter boundary implicit and less discoverable.

## Consequences

Positive:
- one explicit, testable adapter boundary
- easier to add new sync targets without changing command semantics
- shared error/type vocabulary across adapters

Tradeoffs:
- one more package-level abstraction to maintain
- requires docs/tests to avoid contract drift across adapters

## Scope Notes

This ADR defines the interface and contract only.
Wiring existing clients to use adapter dispatch can be done in a separate
change so interface introduction remains reviewable in isolation.

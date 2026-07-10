# ADR 003: Capability Policy Contract V1

- Status: Accepted
- Date: 2026-07-10

## Context

Madari server manifests describe one MCP server and rings compose server and
skill references. Ring `[contract]` metadata is intentionally advisory. None of
those primitives currently states which server tools a client may expose or
whether a ring must fail when a target cannot reproduce the declared access
controls.

Client-native MCP policy fields are not portable as-is. Names, supported
features, and approval enums vary by client and client version. Copying one
client's values into Madari's registry would make that client the canonical
contract and would encourage other adapters to approximate unsupported
semantics. Approximation is unsafe for a ring that requires enforcement because
it can silently widen access.

## Decision

### Primitive ownership

- A server manifest owns one optional MCP access profile.
- A ring continues to compose server profiles and skills by reference.
- `[contract]` remains advisory and is never an authorization mechanism.
- A new ring `[policy]` section declares whether exact enforcement is required.
- `[policy.execution]` is reserved for a later runtime-policy goal and is
  rejected in V1.

The V1 server shape is:

```toml
[access]
allowed_tools = ["issues.get", "issues.list"]
denied_tools = ["issues.delete"]
oauth_scopes = ["issues:read"]
default_approval = "always-prompt"

[access.tool_approvals]
"issues.get" = "always-allow"
```

The V1 ring enforcement shape is:

```toml
[policy]
enforcement = "required"
```

`allowed_tools` is the exact tool allowlist. `denied_tools` is an optional
second-stage deny list. `oauth_scopes` contains scopes Madari asks the client to
request. `default_approval` and `tool_approvals` control client prompts; they do
not grant server-side permission.

### Portable approval vocabulary

Madari V1 defines these values:

| Madari value | Portable intent | Codex value |
| --- | --- | --- |
| `inherit` | Remove the explicit override and use the client default | field omitted |
| `automatic` | Let the client choose from its native tool metadata and policy | `auto` |
| `always-prompt` | Prompt for every invocation covered by the value | `prompt` |
| `always-allow` | Do not add an approval prompt for the invocation | `approve` |

Raw Codex values are not valid Madari values. In particular, V1 does not expose
Codex's version-dependent `writes` value as a portable behavior. A later schema
revision can add a portable write-classification behavior after its semantics
and target support are stable enough to compile without version guessing.
The native field names and values are tracked against the current
[Codex configuration reference](https://developers.openai.com/codex/config-reference).

Approval behavior is a client-side control, not an authorization boundary. A
server and its OAuth provider remain responsible for authorization.

### Presence and explicit clear

Presence is part of the contract and must survive TOML, JSON snapshots, store
round trips, and CLI JSON output.

- No `[access]` section means a legacy manifest makes no Madari access-policy
  declaration.
- An absent field inside `[access]` means persistent sync preserves that native
  field on an existing managed entry. Render and ephemeral run omit it and use
  the target default.
- An explicitly empty `allowed_tools`, `denied_tools`, or `oauth_scopes` array
  clears the corresponding native override. Clearing `allowed_tools` makes the
  server unbounded from Madari's perspective.
- An absent `[access.tool_approvals]` table preserves an existing native table.
  A present empty table clears all native per-tool approval overrides.
- `default_approval = "inherit"` explicitly clears the native default approval
  override.

Required enforcement therefore distinguishes an absent or empty allowlist from
a non-empty allowlist. Both absent and explicitly cleared allowlists are
unbounded and invalid for a required ring.

### Validation and contradictions

The registry rejects:

- unknown access or policy fields, unknown nested access sections, and
  malformed or duplicate tool-approval entries;
- blank, whitespace-padded, or duplicate tool and scope values;
- invalid portable approval values;
- a tool present in both allow and deny lists;
- a per-tool approval for a denied tool;
- a per-tool approval outside a declared non-empty allowlist; and
- a required ring whose server member lacks an explicit non-empty allowlist.

Set-like arrays and per-tool keys marshal in deterministic lexical order.
Dotted server and tool names are literal identifiers and must be quoted when a
target's syntax would otherwise interpret dots as nesting.

### Required enforcement

For a selected operation, a required ring is ready only when:

1. every server member exists, is enabled, and targets the selected client;
2. every server member has an explicit non-empty allowlist;
3. the target supports the member transport and every declared access field on
   that operation's surface;
4. the compiler can represent every declared value without approximation; and
5. existing behavior-affecting native fields do not prevent a fidelity claim.

Failure is a preflight error. Attach and routine sync of an already attached
required ring fail before client config, managed state, or skill files change.
Render fails before writing partial output. Run fails before skill
materialization or client execution. Detach remains available so a broken or
stale attachment can always be cleaned up.

Target support is declared centrally and separately for persistent sync/attach,
render, and run. A compiler must opt into a surface; an unspecified compiler is
unsupported. The policy-schema PR intentionally leaves every compiler disabled,
so required operations fail closed until the corresponding compiler lands.

For persistent sync, undeclared native policy fields are preserved. Declared
fields are compiled exactly. A required operation blocks on unknown
behavior-affecting fields inside the selected managed server entry rather than
deleting or guessing at them. Unmanaged entries and unrelated top-level client
configuration retain the existing ownership and preservation guarantees.

### OAuth and effective-policy reporting

`oauth_scopes` means requested and client-configured. Madari can verify that the
target config carries the requested values; it cannot prove that an OAuth
provider granted them or that a token contains them. Diagnostics and run plans
must use that language.

Dry-run reporting distinguishes:

- declared policy: the portable server profile plus ring enforcement;
- effective policy: the values the selected target will receive;
- support state: whether the target can represent those values; and
- enforcement classification: none, advisory, exact, or blocked.

Client-enforced tool filtering, requested OAuth scopes, approval prompting, and
advisory contract/skill instructions are reported as separate controls.

## Compatibility

Manifests without `[access]` and rings without `[policy]` preserve existing
behavior. Access profiles on non-required rings may be compiled and diagnosed
when a target supports them, but Madari does not claim mandatory enforcement.

Snapshot format V10 adds server access profiles and ring policies. V1 through
V9 snapshots remain importable. Older snapshot versions carrying V10 fields are
rejected so policy data cannot be silently discarded by version mismatch.
Import validates the complete resulting server/ring graph before its first
write.

## Consequences

- Madari gains a portable, client-independent access contract without turning
  rings into duplicate server configuration.
- Required policy is fail-closed across persistent, render, and run surfaces.
- Legacy native restrictions survive routine sync instead of being erased by a
  narrow serializer.
- Supporting another client requires an explicit compiler and fidelity tests;
  approximate mappings are not accepted for required rings.
- Environment sanitization, TTLs, receipts, credential brokers, audit, and
  runtime `[policy.execution]` semantics remain outside this decision.

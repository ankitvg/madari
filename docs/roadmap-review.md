# Madari Roadmap Review

Date: 2026-03-01

## Current State Assessment

Madari is a focused, local-first Go CLI for MCP server lifecycle management. The core value proposition is clear: install/register MCP servers once, sync configs to supported AI clients deterministically and safely.

### What's Solid

**Core Loop**
- The install → register → sync workflow works end-to-end for both `uv` and `npm` package managers.
- Manual `add` provides an escape hatch for servers not published to a package registry.
- Sync safety model (backup + atomic write + managed-state tracking + conflict detection) is well-designed and consistently applied across both adapters.

**Registry**
- One-file-per-server TOML format is human-readable, diff-friendly, and easy to version control.
- Custom strict parser rejects unknown fields — good schema hygiene for a config-critical tool.
- Snapshot export/import provides a portable backup/sharing mechanism.

**Client Adapters**
- `ClientAdapter` interface (ADR 001) is clean and minimal.
- Both adapters (Claude Desktop, Claude Code) enforce the same safety invariants.
- Shared sync utilities in `syncshared` reduce duplication for plan building, state management, backup, and atomic writes.

**Diagnostics**
- `doctor` and `status` cover the important failure modes: missing binaries, bad permissions, missing env vars, unparseable configs, unmanaged collisions.
- Troubleshooting docs map each diagnostic output to a concrete fix.

**Test Coverage**
- 85 test functions across 10 test files.
- All core paths (registry CRUD, sync lifecycle, parser round-trips, snapshot import/export, CLI dispatch) are covered.
- Edge cases like Windows path handling, name collision detection, and parser strictness are tested.

**Documentation**
- Architecture, manifest spec, CLI reference, troubleshooting, ADR, and RFC are all present and consistent with the implementation.

### Where It's Thin

1. **Two clients only.** Claude Desktop and Claude Code are the only sync targets. The adapter interface is ready for more, but the tool's utility is bounded by this.

2. **No update/edit command.** Changing a server's command, args, env, or clients requires `remove` + `add`. This is friction for the most common maintenance operation.

3. **No guided setup.** Users must know the exact command path, args, and env vars for every server. The local-catalog RFC addresses this, but it's still draft.

4. **No `sync --all`.** Syncing requires specifying one client at a time. Users with both Claude Desktop and Claude Code configured need two sync invocations.

5. **Version is hardcoded `0.0.0-dev`.** No release pipeline or version injection yet.

6. **Duplicate adapter code.** `claude-desktop/sync.go` and `claudecode/sync.go` share substantial logic beyond what `syncshared` already extracts. The JSON read/write/merge pattern is repeated.

---

## RFC Review: Local Catalog

The [local-catalog RFC](rfcs/local-catalog.md) is the most impactful proposed change. Assessment:

### Strengths
- Solves the biggest UX gap: users shouldn't have to read server READMEs to construct correct `--command`, `--arg`, and `--env` flags.
- Keeping the catalog local and embedded avoids network dependencies and aligns with the local-first principle.
- `env_schema` with required/optional and secret/non-secret distinctions is the right abstraction.

### Concerns
- **Staleness risk is real.** An embedded snapshot will drift from upstream server releases. The RFC acknowledges this but doesn't propose a mitigation path (even a manual `madari catalog update` from a known URL would help).
- **Catalog maintenance burden.** Who writes and validates entries? Community contribution process isn't addressed.
- **Schema scope.** The proposed fields (`slug`, `display_name`, `package_manager`, `package_name`, `install_command`, `default_command`, `default_args`, `env_schema`, `client_overrides`) look right for MVP. Resist adding more before validating with real usage.

### Recommendation
Proceed with the local-catalog MVP, but:
1. Start with 5-10 well-known servers (sequential-thinking, filesystem, memory, etc.) rather than trying to be comprehensive.
2. Add a `madari catalog list` command early so users can discover what's available.
3. Defer `client_overrides` to a follow-up unless a concrete use case exists today.
4. Plan for a network-optional refresh mechanism in v2 (even if it's just `curl URL > catalog.json`).

---

## Suggested Roadmap Priorities

### Near-Term (Next Release)

| Priority | Item | Rationale |
|----------|------|-----------|
| P0 | `madari edit <name>` command | Most requested workflow gap. Allow updating command, args, env, clients, description without remove+add. |
| P0 | `madari sync --all` | Convenience for multi-client users. Iterate over all adapters, aggregate results. |
| P1 | Local catalog MVP | Biggest UX improvement. Embed metadata for top servers. Wire into `install` and `add`. |
| P1 | Release pipeline + version injection | Replace `0.0.0-dev` with build-time ldflags. Tag releases. |
| P2 | Reduce adapter code duplication | Extract JSON config read/write/merge into `syncshared`. Keep adapter-specific logic minimal. |

### Medium-Term

| Priority | Item | Rationale |
|----------|------|-----------|
| P1 | Additional client adapters | Cursor, Windsurf, VS Code + Continue, or other MCP-compatible clients as they emerge. Each is a new adapter implementing `ClientAdapter`. |
| P1 | `madari upgrade <name>` | Re-run package manager install for an existing server (pin to latest or specific version). |
| P2 | Catalog refresh mechanism | Optional network fetch for catalog updates between CLI releases. |
| P2 | Shell completions | Bash/Zsh/Fish completions for commands, server names, and client targets. |
| P3 | `madari init` | Interactive first-run wizard: detect installed MCP servers, suggest registration. |

### Longer-Term Considerations

- **Server health probing.** Extend `doctor` to actually launch a server and verify it responds to MCP protocol handshake. This moves diagnostics from static checks to runtime validation. Keep it opt-in (`doctor --probe`).
- **Manifest versioning.** As the manifest schema evolves (e.g., adding catalog metadata, version pins), consider a `manifest_version` field and migration logic.
- **Multi-workspace support for Claude Code.** Currently syncs to CWD's `.mcp.json`. Users with multiple projects may want a global Claude Code config or workspace-aware sync.

---

## Documentation Gaps

1. **No CHANGELOG.** Start one before the first tagged release.
2. **No CONTRIBUTING guide.** If the catalog will accept community entries, contribution guidelines are needed.
3. **ADR index.** Only one ADR exists. As more decisions are recorded, add an index file or numbering convention doc.
4. **CLI reference is terse.** `docs/cli-reference.md` lists commands but doesn't document flags. The README is more complete. Consider either expanding the CLI reference or pointing it to the README.

---

## Code Quality Observations

1. **`cmd/madari/run.go` is 1,297 lines.** This single file contains all command implementations. Consider splitting into one file per command (e.g., `cmd_install.go`, `cmd_sync.go`, `cmd_doctor.go`) to improve navigability. The `dispatch()` pattern already makes this straightforward.
2. **Test helpers are duplicated across test files.** Common patterns like temp-dir setup, manifest creation, and store initialization could be extracted into a `testutil` package.
3. **Error messages are inconsistent.** Some use `fmt.Fprintf(os.Stderr, ...)` directly, others use `return fmt.Errorf(...)`. A consistent error-reporting convention would help.

---

## Summary

Madari's foundation is strong: the registry model, sync safety guarantees, and adapter interface are well-designed. The biggest opportunity is reducing setup friction through the local catalog, and the biggest quality-of-life gap is the missing `edit` command. The codebase is clean and well-tested, with room for structural improvements (file splitting, adapter deduplication) that can happen incrementally without architectural risk.

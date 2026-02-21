# RFC: Local Catalog For MCP Install Metadata

Status: Draft

## Why
Madari currently relies on users to know server-specific setup details (command, args, required env vars). That information is usually scattered across server READMEs and examples, which leads to trial-and-error and setup failures.

This RFC proposes a local catalog of server metadata so `madari install` and `madari add` can provide guided defaults without Madari owning package distribution.

## Problem Statement
Package managers know how to install packages.
MCP clients know how to launch servers.
The setup glue is inconsistent and rarely machine-readable:
- default executable and args
- required env vars
- optional client-specific launch overrides

## Goals
- Keep package installation with existing package managers (`uv`, `pip`, `npm`).
- Improve first-run setup reliability for known servers.
- Keep Madari local-first and usable without runtime API calls.

## Non-Goals
- Hosting or mirroring MCP packages.
- Replacing package managers.
- Introducing mandatory cloud sync for catalog usage.

## Proposed Design (MVP)
Madari ships with a built-in static catalog snapshot.

Each catalog entry describes install/config metadata for a known server:
- `slug`
- `display_name`
- `package_manager`
- `package_name`
- `install_command` template
- `default_command`
- `default_args` (optional)
- `env_schema` (required/optional, secret/non-secret, prompt label)
- `client_overrides` (optional)

Madari behavior:
- `madari install <slug>` uses catalog defaults to run package-manager install and then guided setup.
- `madari add <slug>` can prefill command/env hints from catalog where available.
- Unknown servers continue to work with explicit flags/manual config.

## Distribution Model
For initial rollout, catalog data is local:
- embedded snapshot in Madari releases
- optional local import for custom/community packs (file-based)

No automatic network fetch is required for MVP.

## Risks and Tradeoffs
- Embedded-only catalogs can become stale between Madari releases.
- Catalog quality depends on recipe maintenance.
- Incorrect metadata can produce confusing setup prompts.

## Open Questions
- Should catalog versioning be decoupled from CLI version in a follow-up?
- What validation rules are required for secret vs non-secret env fields?
- What minimum schema is sufficient before adding client-specific overrides?

## Rollout Notes
This is a direction document, not a committed timeline. Scope and implementation details may change based on user feedback.

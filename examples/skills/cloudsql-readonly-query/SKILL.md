---
name: cloudsql-readonly-query
description: Query Cloud SQL through the official remote MCP server using read-only tools and bounded result sets.
license: Apache-2.0
compatibility: Codex, Claude Code, or Gemini with the cloud-sql MCP server managed by Madari.
metadata:
  ring: cloudsql-readonly
allowed-tools: mcp__cloud-sql__list_instances,mcp__cloud-sql__get_instance,mcp__cloud-sql__execute_sql_readonly,Bash(python3 scripts/sql_readonly_check.py:*),Bash(sh scripts/cloudsql_target_context.sh:*)
---

# Cloud SQL Read-Only Query

Use this skill when the user asks for Cloud SQL inspection, counts, samples, reports, or debugging that can be answered with read-only SQL.

The `cloud-sql` MCP server advertises broader Cloud SQL tools. For this skill, only use:

- `list_instances`
- `get_instance`
- `execute_sql_readonly`

Do not use `execute_sql`, instance admin tools, user admin tools, backup/restore/import tools, or upgrade tools.

Before running SQL (script paths are relative to this skill's directory):

1. Read `references/QUERY_POLICY.md`.
2. Run `sh scripts/cloudsql_target_context.sh` when target context is not already explicit.
3. Draft the SQL and check it with `python3 scripts/sql_readonly_check.py`.
4. Use `execute_sql_readonly` only after the checker passes.

Query defaults:

- Prefer `SELECT`, `WITH`, `SHOW`, `DESCRIBE`, and metadata inspection.
- Include a `LIMIT` for exploratory row queries.
- Prefer counts, aggregates, and narrow projections over unrestricted table scans.
- Report the project, instance, database, SQL, row count, and any truncation or timeout signal in the final answer.

If the task requires schema changes, writes, user changes, backups, imports, or IAM setup, stop and explain that this ring is read-only.

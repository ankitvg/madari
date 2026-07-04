---
name: cloudsql-readonly-query
description: Inspect a known Cloud SQL MySQL or PostgreSQL target with read-only SQL, bounded result sets, and explicit query reporting.
license: Apache-2.0
compatibility: Codex or Claude Code with the cloud-sql MCP server managed by Madari; Gemini requires equivalent Cloud SQL MCP configuration with Google credentials auth fields.
metadata:
  ring: cloudsql-readonly
allowed-tools: mcp__cloud-sql__list_instances,mcp__cloud-sql__get_instance,mcp__cloud-sql__execute_sql_readonly,Bash(python3 scripts/sql_readonly_check.py:*),Bash(sh scripts/cloudsql_target_context.sh:*)
---

# Cloud SQL Read-Only Query

Use this skill when the user asks for Cloud SQL MySQL or PostgreSQL inspection, counts, schema checks, bounded samples, or debugging reports that can be answered with read-only SQL against a known target.

This skill is intentionally narrower than the full `cloud-sql` MCP server. Use only:

- `list_instances`
- `get_instance`
- `execute_sql_readonly`

Do not use `execute_sql`, instance admin tools, user admin tools, backup/restore/import tools, upgrade tools, or any tool that changes Cloud SQL state.

Workflow (script paths are relative to this skill's directory):

1. Read `references/QUERY_POLICY.md`.
2. Establish the target project, instance, database, and dialect from the prompt or by running `sh scripts/cloudsql_target_context.sh`.
3. If the target or question is ambiguous, ask for the missing context before querying.
4. Draft the narrowest SQL that answers the question and check it with `python3 scripts/sql_readonly_check.py`.
5. Use `execute_sql_readonly` only after the checker prints `OK`.

Query defaults:

- Prefer metadata queries, counts, grouped aggregates, and narrow projections.
- Include `LIMIT 100` or stricter for exploratory row queries.
- Prefer counts, aggregates, and narrow projections over unrestricted table scans.
- Avoid columns that are likely to contain credentials, tokens, secrets, or unnecessary PII.
- Prefer read replicas for routine analysis when the user gives that option.

Final response requirements:

- Report the project, instance, database, and dialect when known.
- Include the SQL that passed the checker.
- Summarize row count, limits, truncation, timeout, or partial-result signals.
- Call out caveats and suggest the next read-only follow-up when the result is inconclusive.

If the target is SQL Server, stop and explain that `execute_sql_readonly` is not supported for SQL Server. If the task requires schema changes, writes, migrations, stored procedure execution, user changes, backups, imports, exports, or IAM setup, stop and explain that this ring is read-only.

# Cloud SQL Read-Only Query Policy

This skill is for read-only Cloud SQL work through the official Google remote MCP server.

Allowed MCP tools:

- `list_instances`
- `get_instance`
- `execute_sql_readonly`

Disallowed MCP tools include `execute_sql`, create/update/delete instance tools, user administration tools, backup/restore/import tools, and upgrade tools.

SQL rules:

- Use read-only statements only.
- Reject DDL, DML, transactions, locks, grants, role changes, stored procedure calls, and session changes.
- Use explicit column lists for exploratory queries when practical.
- Add `LIMIT 100` or a stricter limit for sample queries unless the user asks for an aggregate.
- Prefer `COUNT(*)`, grouped aggregates, and metadata queries when answering broad questions.
- Keep expensive joins and full scans out of the first query unless the user has provided enough context to justify them.

Safety reminders:

- IAM permission is not the only boundary. The database user should also have read-only privileges.
- A read replica is preferred for routine analysis.
- Cloud SQL's `execute_sql_readonly` tool has response-size and timeout limits; summarize partial results instead of retrying with broader queries.

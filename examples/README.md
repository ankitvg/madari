# Examples

Each example directory is meant to stand on its own: start with its `README.md`,
then use the files beside it as the example's source material.

- `cloudsql-readonly/`: read-only Cloud SQL inspection through Google's remote
  MCP server, with a ring contract and bundled skill package.

Keep new examples grouped by workflow instead of by primitive. Prefer:

```text
examples/<example-name>/
  README.md
  servers/
  rings/
  skills/
```

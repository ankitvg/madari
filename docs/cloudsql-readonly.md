# cloudsql-readonly Ring

`cloudsql-readonly` is a ring for querying Cloud SQL through Google's
official remote MCP server. It is designed for routine read-only database
inspection: counts, samples, schema checks, debugging reports, and small
analytical queries. It works with every Madari client that materializes
remote MCP servers: `codex`, `claude-code`, and `gemini`.

The ring contains:

- `cloud-sql`: the remote Cloud SQL MCP server at
  `https://sqladmin.googleapis.com/mcp`
- `cloudsql-readonly-query`: a local skill package with query rules, a target
  context helper, a SQL read-only checker, and a short query policy

It intentionally does not include `gcloud-mcp`, fetch, time, filesystem, or
Database Insights. Those are useful capabilities, but they broaden the tool
surface beyond everyday read-only database querying.

## Setup

Add the skill package from this repo, add the remote MCP server, create the
ring, and record its delegation contract:

```bash
madari skill add --dir examples/skills/cloudsql-readonly-query

madari add cloud-sql \
  --transport http \
  --url https://sqladmin.googleapis.com/mcp \
  --client codex \
  --client claude-code \
  --client gemini \
  --oauth-resource https://sqladmin.googleapis.com/

madari ring create cloudsql-readonly \
  --member cloud-sql \
  --skill cloudsql-readonly-query \
  --description "Read-only Cloud SQL querying"

madari ring contract set cloudsql-readonly \
  --file examples/rings/cloudsql-readonly.contract.toml
```

The contract is advisory metadata for delegation: when this ring is the right
tool, what context to provide, and what output to expect. `madari ring
contract show cloudsql-readonly` prints it.

Render an ephemeral MCP config without attaching anything:

```bash
madari ring render cloudsql-readonly --client codex
```

Expected render shape per client — Codex:

```toml
[mcp_servers.cloud-sql]
url = "https://sqladmin.googleapis.com/mcp"
oauth_resource = "https://sqladmin.googleapis.com/"
```

Claude Code (`madari ring render cloudsql-readonly --client claude-code`,
also usable directly via `claude --mcp-config <(...)`):

```json
{
  "mcpServers": {
    "cloud-sql": {
      "type": "http",
      "url": "https://sqladmin.googleapis.com/mcp"
    }
  }
}
```

Gemini (`madari ring render cloudsql-readonly --client gemini`):

```json
{
  "mcpServers": {
    "cloud-sql": {
      "httpUrl": "https://sqladmin.googleapis.com/mcp"
    }
  }
}
```

`oauth_resource` is emitted for Codex only; Claude Code and Gemini run their
own OAuth flows for remote servers, so their entries carry just the URL.

Attach the ring to the client you want it persisted in:

```bash
madari ring attach cloudsql-readonly codex --dry-run
madari ring attach cloudsql-readonly codex
madari ring attach cloudsql-readonly claude-code
madari ring status
```

The skill is materialized as a native skill package on attach (Codex under
`.agents/skills/`, Claude Code under `.claude/skills/`, Gemini under
`.gemini/skills/`). `ring render` remains MCP-config-only and does not embed
skill files.

## Authentication

Madari configures the server entry; each client owns its OAuth token
lifecycle. After attaching, authenticate once per client — for example
`codex mcp login cloud-sql`, or the `/mcp` menu in Claude Code. Nothing
happens at attach time; the client prompts on first connection.

## Using the Ring

Recommended target context is passed through non-secret environment variables:

```bash
export MADARI_CLOUDSQL_PROJECT="my-gcp-project"
export MADARI_CLOUDSQL_INSTANCE="my-instance"
export MADARI_CLOUDSQL_DATABASE="app_db"
export MADARI_CLOUDSQL_DIALECT="postgres"
```

The bundled helper prints this context without exposing credentials:

```bash
sh examples/skills/cloudsql-readonly-query/scripts/cloudsql_target_context.sh
```

Check SQL before asking the model to call `execute_sql_readonly`:

```bash
python3 examples/skills/cloudsql-readonly-query/scripts/sql_readonly_check.py \
  'SELECT COUNT(*) FROM users'
```

A good prompt (any attached client) is:

```text
Use the cloudsql-readonly-query skill and the cloud-sql MCP server. Inspect the
configured target context, check this SQL with the bundled read-only checker,
then run it with execute_sql_readonly:

SELECT COUNT(*) FROM users;
```

## Safety Layers

The Cloud SQL MCP server exposes more than read-only SQL tools. This ring's
expected behavior is narrower than the server's full advertised capability.

Use layered controls:

- Grant only the Google Cloud IAM permissions needed for MCP tool calls and
  Cloud SQL access.
- Prefer an IAM database user or service account with database-level `SELECT`
  privileges only.
- Prefer a read replica for routine analysis.
- Consider a Google Cloud IAM deny policy that blocks non-read-only MCP tools
  using `tool.isReadOnly`.
- In prompts and skills, allow only `list_instances`, `get_instance`, and
  `execute_sql_readonly`.

The official remote MCP path does not require Cloud SQL Auth Proxy. Auth proxy
or connector setup is only needed for local database clients or local MCP
servers that connect through the Cloud SQL network path.

## Cleanup

Detach the ring from every client it was attached to before deleting it:

```bash
madari ring detach cloudsql-readonly codex
madari ring detach cloudsql-readonly claude-code
madari ring delete cloudsql-readonly
madari remove cloud-sql
madari skill remove cloudsql-readonly-query
```

## References

- [Google Cloud MCP supported products](https://docs.cloud.google.com/mcp/supported-products)
- [Use the Cloud SQL remote MCP server](https://docs.cloud.google.com/sql/docs/postgres/use-cloudsql-mcp)
- [execute_sql_readonly reference](https://docs.cloud.google.com/sql/docs/mysql/reference/mcp/mysql/mcp/tools_list/execute_sql_readonly)
- [Prevent read/write tool use](https://docs.cloud.google.com/mcp/prevent-read-write-tool-use)

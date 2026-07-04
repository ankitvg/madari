# stewreads-local

`stewreads-local` is a minimal example for registering a local StewReads MCP
server with Claude Desktop.

The example contains:

- `servers/stewreads.toml`: the equivalent server manifest for the local MCP
  server.

## Setup

Adjust the command path for your machine, then add the server:

```bash
madari add stewreads \
  --command /Users/me/.local/bin/stewreads-mcp \
  --client claude-desktop \
  --env STEWREADS_CONFIG_PATH=~/.config/stewreads/config.toml \
  --required-env STEWREADS_GMAIL_APP_PASSWORD \
  --description "Turn AI conversations into ebooks"
```

Then sync it into Claude Desktop:

```bash
madari sync claude-desktop --dry-run
madari sync claude-desktop
```

`STEWREADS_CONFIG_PATH` is non-secret configuration. Keep
`STEWREADS_GMAIL_APP_PASSWORD` in the runtime environment instead of writing it
into the manifest.

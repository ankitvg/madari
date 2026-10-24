# madari (muh-DAA-ree)

**Create configurable agents with the AI clients you already use.**

Madari is a local-first CLI that combines tools, skills, and task contracts into
reusable configurations called **rings**. Choose the capabilities your agent
needs, select a supported client, and give it a task.

Run agents directly with Codex, or attach supported tools and skills to other
clients. The client supplies the model interaction and execution loop; Madari
assembles the configuration and manages its lifecycle.

For example, a brief-writing agent can start with a skill for turning supplied
notes into a concise brief. Add an MCP server when it needs to retrieve source
material. Reuse the same ring for another task, or compose it with another ring
for a different workflow.

## Installation

Homebrew:

```bash
brew tap ankitvg/tap
brew install madari
```

Go:

```bash
go install github.com/ankitvg/madari/cmd/madari@latest
```

## Create your first agent

This example uses a POSIX shell and source text supplied in the task. Registering
and inspecting the ring needs only Madari. Previewing or launching it with
`madari run codex` also needs Codex CLI on `PATH`, and a real run needs an
authenticated Codex session. The run invokes the model through your Codex account:
it uses `--model <id>` when given, otherwise the `model` and
`model_reasoning_effort` set in your Codex `config.toml`.

Create a skill package describing how the agent should work:

```bash
mkdir -p brief-writer
cat > brief-writer/SKILL.md <<'SKILL'
---
name: brief-writer
description: Turn supplied source notes into a concise, evidence-based brief.
---
Use the source notes supplied with the task. Summarize the main finding,
supporting evidence, and open questions. Distinguish facts from inferences.
Do not invent missing facts or claim to have inspected sources not provided.
SKILL

madari skill add --dir ./brief-writer
madari ring create briefing --skill brief-writer \
  --description "Write a brief from supplied source notes"
madari ring show briefing
```

Preview the configuration, then launch the agent with a task:

```bash
madari run codex --ring briefing --dry-run -- \
  "Use brief-writer. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief and identify what is still unknown."

madari run codex --ring briefing -- \
  "Use brief-writer. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief and identify what is still unknown."
```

A dry run checks the launch plan without calling the model. It does not evaluate
the agent's answer or prove that external services are reachable. If Codex is
missing, the preview reports its runner as unavailable and blocks the plan. A run uses
temporary skill files and leaves client config and attachment state unchanged.

For task contracts, multiple rings, and adding retrieval tools, follow the
[complete configurable-agent walkthrough](docs/configurable-agents.md).

## How agents are configured

| Concept | Role |
| --- | --- |
| Agent | A client running a task with selected capabilities and instructions |
| Ring | A reusable configuration of servers, skills, and an optional task contract; multiple rings can compose one agent |
| MCP server | Provides tools the agent can use, through a local process or remote endpoint |
| Skill | Supplies procedures and domain guidance as an Agent Skill package |
| Contract | Describes intended tasks, context, and expected outputs; advisory instructions, not access controls |
| Client | Supplies the model interaction and execution loop |
| Run | Launches a task using the selected rings |

Rings reference registered servers and skills by name. A ring can contain tools,
skills, or both. It does not store a model, conversation, or running process.
Select the client and task when you run it. A ring can be a complete capability
configuration for a task or one reusable part of a larger configuration.

Skills contain `SKILL.md` and optional supporting files. Contracts add guidance
about when to use a ring and what to provide or return. Codex runs include ring
contracts in the task preamble. Attaching a ring does not install that preamble.

Server `[access]` profiles and ring `[policy]` requirements configure supported
access controls separately from these instructions. See the
[manifest spec](docs/manifest-spec.md) for the schema and enforcement boundaries.

## Reuse capabilities in an existing client

Attach the briefing skill to Claude Code in the current project:

```bash
madari ring attach briefing claude-code --dry-run
madari ring attach briefing claude-code
madari ring status
```

Then open the client and give it a task that uses `brief-writer`. Attachment
installs supported tools and skills into the existing client; it does not start
a separate agent, replace other capabilities, or install the ring contract.
Release the attachment when it is no longer needed:

```bash
madari ring detach briefing claude-code
```

Madari tracks ownership so overlapping rings and standalone attachments detach
cleanly. It refuses to adopt or overwrite unmanaged entries.

For tools used outside a ring, `madari install`, `madari add`, and `madari sync`
remain available. `madari ring render` emits only client-native MCP config; it
does not include skill packages or contracts. See the
[CLI reference](docs/cli-reference.md) for registration, remote authentication,
composition, sync, and rendering commands.

## Supported clients

| Client ID | MCP config sync | Skill attachment | `madari run` execution |
| --- | --- | --- | --- |
| `codex` | Yes | Yes | Yes |
| `claude-code` | Yes | Yes | Plan only (`--dry-run`) |
| `gemini` | Yes | Yes | Plan only (`--dry-run`) |
| `vibe` | Yes | Yes | Plan only (`--dry-run`) |
| `claude-desktop` | Yes | No | Plan only; rings containing skills are unsupported |

Remote servers materialize for Claude Code and Gemini (`http` and `sse`) and
Codex (`http`). Remote entries requiring `oauth_resource` or
`bearer_token_env_var` currently materialize only for Codex. Other combinations
remain pending or block a run. See [transport support](docs/manifest-spec.md).

Codex supports exact capability-policy compilation for sync/attach, render, and
run. Required policies block on other clients. Every access-bearing Codex run
requires a validated stable Codex CLI **0.139.x** release. Runs with no server
`[access]` profiles and no required ring `[policy]` have no version gate. This
applies to previews as well as execution. See the
[policy contract](docs/adr/003-capability-policy-contract-v1.md).

## Safety and boundaries

- Registry files are local and human-readable. Sync uses backups and atomic
  writes, with explicit ownership of managed entries.
- Runs preflight selected capabilities and fail closed when required members
  cannot be used. Codex runs isolate inherited MCP config and personal skills;
  this does not make a ring a restriction on every native client capability.
- Skills and contracts guide behavior. Required capability policies fail closed
  if an access restriction cannot be represented exactly. OAuth scopes are
  requested and client-configured, not proof of provider-granted permissions;
  tool approval settings control client prompts, not authorization.
- Secret env values, well-known credential headers, and headers marked with
  `--secret-header` stay out of repo-scoped configs. Remote credential headers
  are not emitted by ring rendering or Codex runs. Bearer-token env
  references store variable names, not token values.
- `doctor`, `status`, and `ring status` inspect configuration readiness and drift.
  They do not monitor running agents or grade task results.
- Madari has no background daemon or proxy. The client owns execution, including
  how skills are loaded and tools are selected during a task.

For exact isolation, environment, ownership, and policy behavior, read the
[run reference](docs/cli-reference.md#run), [architecture](docs/architecture.md),
and [troubleshooting guide](docs/troubleshooting.md).

## Documentation

- [Create your first configurable agent](docs/configurable-agents.md)
- [CLI reference](docs/cli-reference.md)
- [Architecture](docs/architecture.md)
- [Manifest spec](docs/manifest-spec.md)
- [Capability Policy Contract V1](docs/adr/003-capability-policy-contract-v1.md)
- [Troubleshooting](docs/troubleshooting.md)

## Development

```bash
make build
go test ./...
```

## License

Apache License 2.0. See `LICENSE` and `NOTICE`.

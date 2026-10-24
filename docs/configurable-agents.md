# Create your first configurable agent

An agent is a client running a task with selected capabilities and instructions.
Madari lets you assemble those capabilities into reusable **rings**. A ring can
hold tools, skills, or both, plus an optional task contract and capability-policy
requirement. Choose one or more rings, a client, and a task when you run.

This walkthrough creates a brief-writing agent, adds its task contract, and
composes it with an evidence-checking skill. It uses supplied text, so no external
MCP service or service credentials are needed. An optional final step adds a
source-retrieval tool server.

## Prerequisites

- Install Madari using the [README](../README.md#installation).
- For previewing and launching Codex runs, put Codex CLI on `PATH`. Authenticate
  Codex before a real run. Execution uses your account and may incur usage.
- Use a POSIX shell for these examples. From a Madari repository checkout, run
  the commands below in the repository root. Alternatively, download the
  [briefing example directory](../examples/briefing) and preserve the
  `examples/briefing/` paths relative to your working directory.
- Use unused ring names, or inspect existing entries before choosing new names.
  `ring create` does not overwrite an existing ring.

The [README quickstart](../README.md#create-your-first-agent) is an alternative
setup for the same `briefing` ring. If you already followed it, skip `ring create`
and `skill add` below; use the `skill update` command to refresh the registered
skill package instead.

The commands use your normal local Madari registry. For a disposable exercise,
set a separate registry for this shell first:

```bash
export MADARI_CONFIG_DIR="$(mktemp -d)"
```

This redirects registry/state files only. Attachment still writes to the selected
client's config or skill directory; use a scratch project for the attachment
section. Keep this variable set until any attachments made during the exercise
have been detached.

## 1. Register a skill and create a ring

The bundled [brief-writer skill](../examples/briefing/brief-writer/SKILL.md)
defines the agent's procedure and output structure:

```bash
madari skill add --dir ./examples/briefing/brief-writer
madari ring create briefing --skill brief-writer \
  --description "Write a brief from supplied source notes"
```

The registry stores a copy of the skill. Editing the example file later does not
change that copy until you register the update:

```bash
madari skill update brief-writer --dir ./examples/briefing/brief-writer
```

## 2. Describe the task contract

Apply the bundled [contract](../examples/briefing/contract.toml):

```bash
madari ring contract set briefing --file ./examples/briefing/contract.toml
madari ring contract show briefing
madari ring show briefing --json
```

The contract describes suitable tasks, useful context, and expected outputs.
Codex runs include it in the prompt preamble. `required_context` is guidance for
the caller, not an input-validation schema; `not_for` does not deny tool access.
Skills and contracts are advisory. Server `[access]` profiles and ring `[policy]`
requirements are separate [capability controls](manifest-spec.md#policy).

## 3. Preview the agent's configuration

```bash
madari run codex --ring briefing --dry-run --json -- \
  "Use brief-writer. Audience: product team. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief about what this tells us and what is still unknown."
```

Inspect `rings`, `skills`, `servers`, `ready`, and `errors`. This ring should
select `brief-writer` with no external MCP servers. The planner checks the local
launch requirements; it does not run the model, check source quality, or verify
remote connectivity. Missing Codex or incompatible admin skill roots can block
this preview. See [troubleshooting](troubleshooting.md).

## 4. Run the agent

Remove `--dry-run --json` to execute the same task:

```bash
madari run codex --ring briefing -- \
  "Use brief-writer. Audience: product team. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief about what this tells us and what is still unknown."
```

Madari launches Codex with temporary skills and selected MCP configuration,
includes the contract, and streams the client output. It does not attach the
ring persistently or store a conversation in the ring.

Review the answer: the notes support a 12-of-20 return count for the following
week, but do not establish long-term retention or explain why users returned.
That is a human quality check, not an assertion that every model run will return
identical text. The client owns the reasoning loop and skill loading.

## 5. Compose another ring

Add a separate evidence-checking procedure:

```bash
madari skill add --dir ./examples/briefing/evidence-check
madari ring create evidence-review --skill evidence-check \
  --description "Check conclusions against their evidence"
madari run codex --ring briefing --ring evidence-review --dry-run --json -- \
  "Use brief-writer and evidence-check. Audience: product team. Source notes: A pilot served 20 users; 12 returned the following week. Write a brief with an evidence check."
```

The plan should contain both skills. Omit `--dry-run --json` to execute it. Shared
server and skill members are deduplicated. Both rings configure the same agent
run; selecting two rings does not launch two subagents.

## 6. Add tools when the task needs them

To retrieve source material, register an existing MCP server you trust and are
already authorized to use. This is a template: replace `/absolute/path/to/sources-mcp`
with your installed server executable, add any required arguments/environment
settings, and use a source reference that server understands in the task.

```bash
madari add sources --command /absolute/path/to/sources-mcp --client codex
madari ring create source-tools --member sources \
  --description "Retrieve source material for briefs"
madari run codex --ring briefing --ring source-tools --dry-run --json -- \
  "Use brief-writer. Retrieve the source material at SOURCE_REFERENCE and prepare a brief for the product team."
```

`madari add` registers this server; it does not install or start it. The same
`briefing` ring now composes with retrieval tools without duplicating its skill
or contract. Choose and configure the actual server before executing the run.
For remote endpoints and token references, see the
[server lifecycle reference](cli-reference.md#server-lifecycle).

A skill's instruction to avoid writes is not an access restriction. Configure
server-side permissions and, where supported, explicit access profiles if the
agent must be limited to particular tools. Required policies currently compile
only for Codex, and access-bearing Codex runs require stable CLI 0.139.0 or
newer.

## 7. Reuse the skill in an existing client

From a scratch project, attach the briefing ring to Claude Code:

```bash
madari ring attach briefing claude-code --dry-run
madari ring attach briefing claude-code
madari ring status
```

Open Claude Code in that project and ask it to use `brief-writer` with your notes.
Attachment installs the skill package; it neither launches a separate agent nor
installs the contract preamble. Supply task context in your prompt. Existing
client tools and instructions remain available. `ring render` is different
again: it emits MCP config only, without skills or contracts.

Clean up that attachment before switching registries:

```bash
madari ring detach briefing claude-code
```

Detaching releases this ring's ownership. Other rings or standalone attachments
can keep shared entries present. Modified skill packages are protected from
removal. To remove the tutorial definitions once no longer needed:

```bash
madari ring delete briefing
madari ring delete evidence-review
madari skill remove brief-writer
madari skill remove evidence-check
```

If you completed the optional tool step, also delete `source-tools` and remove
`sources` once you no longer need that registration. These operations do not
uninstall the server executable. If you used a disposable registry, unset
`MADARI_CONFIG_DIR` after cleanup to return to your normal registry.

## Support boundaries

Codex is the only execution target for `madari run` today. Other clients support
planning where their capabilities permit it. Skill attachment is supported by
Claude Code, Codex, Gemini, and Vibe; Claude Desktop cannot consume skill rings.
See the [support matrix](../README.md#supported-clients).

Rings select MCP capabilities and skills; they do not define a new model,
guarantee identical behavior across clients, disable every native client tool,
or implement dynamic tool discovery. Madari's diagnostics inspect readiness
and drift, not agent health or task success. See the
[architecture](architecture.md) for execution and ownership details.

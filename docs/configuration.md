# Configuration — `.aracne/config.json`

Written by `arac init`, read by everything (`internal/helper/config.go`). A free-form
schema and a clean break from older formats: an invalid or outdated file is overwritten
with defaults rather than half-migrated.

The one key that matters most, **`mode`**, has its own page: [modes.md](modes.md).

**`contract_verbosity`** (`low` (the default) / `high`) is the other dial that shapes what a
model is told. It sits next to `mode` at the top level because there is exactly one contract:
`CLAUDE.md`, `AGENTS.md` and the system prompt `arac agent` sends are the same bytes, rendered
by `prompts.ContractContent`. `mode` decides *which* capabilities that document may name;
`contract_verbosity` decides *how much* it says about them.

- **`low`** — what the graph is, how it reaches this surface, and the one or two facts the
  model cannot derive from what it is already shown. Roughly 1-2 KB, and every byte of it is
  re-sent on every request, which is why it is the default: on a harness with tool schemas and
  a system prompt of its own, most of the long version is paid for twice.
- **`high`** — the same mode-shaped document at length: what a read returns and how to read a
  `# CONTEXT:` block, how the project's language spells a resource ID, what the graph's edges
  mean in that language, and the full guidelines. Roughly 4-5 KB. Reach for it on a harness
  with no prompt of its own, or with a model that needs the read discipline spelled out.

The language halves come from the topology's own languages, read from the database at
`arac init` time (`internal/prompts/languages.go`). A project scanned in several languages gets
each one's section; a project scanned in none yet — a fresh checkout, before the first scan —
gets the language-free form, and the next `arac init` after a scan fills it in.

Every tool name in this file is validated against the [`toolspec`](architecture.md#6-the-tool-catalog-internaltoolspec)
catalog at load/init time, so a typo fails fast instead of silently disabling a tool.

## Sections

- **`terminal`** — one key. `max_overserve`: the answer must stay within this multiple of
  the bytes the real command would have printed, or `arac cmd` passes through instead. The
  five former booleans (`intercept`, `enhance_files`, `enhance_resources`, `grep`,
  `prefer_resource_ids`) are facts about the mode now.

- **`scan`** / **`scanner`** — `scan.ignore`, `scan.workers` and `scan.progress` (each also
  a flag on `arac scan`, which wins); `scanner.update_frequency` for the `arac scanner run`
  watch loop; and
  **`scan.pre_tool`** (`default` (the default) / `none` / `full` / `hard`) — the scan
  the guard runs *before* every tool call it sees, on both harnesses. `default` is
  an incremental scan, so the usual case (nothing changed since the last call) is a
  no-op; `none` switches the freshness guarantee off for projects that keep the
  topology current another way (e.g. `arac scanner run`).
- **`read`** — `max_file_size`; **`kinds`** (allow-list of resource kinds that may be read —
  default `file`, `function`, `struct`, `interface`; also accepts `named_type`, `package`,
  `dependency`, `variable`). It gates **every** read entrance, in every mode: the MCP `read`
  tool, `arac read`, an intercepted shell read (`cat`, `head`, `sed -n` — by path *or* by
  resource ID), and the denial proxy. An id that resolves to a kind outside the list is
  refused, naming the kind and the allowed set, and the refusal exits non-zero rather than
  falling back to the plain command. An operand that resolves to *nothing* is a different
  answer and still passes through — there is no policy in a typo. `--kind` narrows which
  resource an id resolves to; it does not grant a kind the list excludes.
  **`context_filter`** — one of `off`, `normal` (the default) or `full`: how verbosely the
  `# CONTEXT:` block renders a read's neighbours. `full` also adds the `# USED BY:` section
  and keeps undescribed neighbours. **`file_mode`** (`full` / `skeleton`) decides what a
  whole-**file** read returns; the default depends on the surface — `skeleton` everywhere
  except `mcp`, which defaults to `full` — with `skeleton_threshold` and `max_symbol_lines`
  bounding what elides. **`pipe_passthrough`** (whether the guard exempts piped reads like
  `cmd | tail`).
- **`descriptions`** — which `kinds` to document + `style_exemplars` count; **who writes
  them** (`provider`, `base_url`, `cli_provider_command` — see
  [below](#who-writes-the-descriptions)); and
  **`lazy`** (default **true**): generate a missing description at the moment a read or a
  search is about to show it, instead of only in an `arac descriptions generate` sweep. The
  read plans the nodes its `# CONTEXT:` / `# USED BY:` sections will name, generates the
  missing ones with the description-executor's model (`haiku` by default), waits for them to
  land in the DB, and re-renders; a search does the same for the nodes it found by name or by
  content. Reads get slower on a cold repo and converge on the old speed as it warms up. `lazy`
  accepts `true`/`false` or an object (`enabled`, `max_nodes`, `timeout_seconds`,
  `batch_size`, `parallel`), and is a no-op when nothing is configured to write with. See
  `internal/lazydesc`.
- **`llm`** — per-harness agent config under `<any>` / `opencode` / `claude_code`,
  each with `main_agent` + named `agents`. Fields: `model`, `mcp_tools`,
  `blocked_tools`, `plugins`, `params`. Resolution: per-harness block beats
  `<any>`; `"<inherits>"`/absent fields fall back to the main agent
  (`EffectiveAgent`). This is what `arac init` and `arac serve --tool-profile`
  consult to decide what each agent can do.
- **`viz`** — `graph.optimization_rules`, the path to the graph's optimization-rules file.
- **`paths`** — a list of `{path, hidden}` rules (paths relative to the topology
  root) that hide/show subtrees. Hidden paths are skipped by the indexing stage
  (file discovery / manifest) and the scan stage in every mode (default/all/hard).
  More specific (more internal) rules win, so a parent can be hidden while a
  nested child stays visible (e.g. hide `my_example` but keep
  `my_example/another_layer`). Resolved by `domain.PathVisibility` and installed
  as the active filter by the topology manager before each scan.

Every tool name in the config is validated against the **`toolspec`** catalog at
load/init time so typos fail fast.

## Who writes the descriptions

Descriptions have two entry points — the **lazy fill** on the read path and the
**`arac descriptions generate`** sweep — and **one** provider block, on the `descriptions`
section itself:

| key | meaning |
|---|---|
| `provider` | `anthropic` \| `openai` \| `deepseek` \| `cli`. Absent: inferred from the executor's model, else from whichever API key is in the environment. |
| `base_url` | A gateway or proxy for the API providers. Ignored by the CLI ones. |
| `cli_provider_command` | The command `provider: "cli"` runs. Required by it, ignored by everything else. |

They live on the section rather than under `lazy` because both entry points describe the same
resources into the same database: a project that has answered "who writes my descriptions"
has answered it once, for both.

**The model is not here.** It is the `descriptions-generation-executor` agent's, and only its:

```jsonc
{ "llm": { "claude_code": { "agents": {
  "descriptions-generation-executor": { "model": "haiku" }
} } } }
```

That is what `arac init` writes, and it is the model both entry points describe with — the
sweep *runs* as that agent, and the lazy fill reads the same field. There was a
`descriptions.model` that said it a second time; it is gone. One answer in one place cannot
disagree with itself, and when the two did disagree there was nothing on screen to say which
had won.

> The older spelling — `descriptions.lazy.provider`, `.base_url` — is still read and still
> means what it meant, so an existing config keeps working. The section keys win where both
> are set.
>
> One name did go: **`provider: "claude_cli"`**, which was `cli` with the Claude invocation
> prefilled and unreadable. It is rejected with the command that replaces it —
> `"provider": "cli", "cli_provider_command": "claude --print --max-turns 1"` — rather than
> silently doing nothing.

The three API providers read `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and `DEEPSEEK_API_KEY`
respectively. Nothing else in aracne needs a key.

### Describing with the Claude CLI, no API key

An API key is a separate thing to buy from the Claude Code subscription you are probably
already paying for. `provider: "cli"` spends the subscription instead: aracne runs a command,
writes the batch to its **stdin**, and reads the descriptions off its **stdout**.

```jsonc
{
  "descriptions": {
    "provider": "cli",
    "cli_provider_command": "claude -p"
  }
}
```

That is the whole setup. `claude` must be on `PATH` and already logged in
(`claude` once, interactively, is enough). Both entry points now use it:

```sh
arac descriptions generate    # the sweep, through `claude -p`
arac read internal/server.go  # a lazy fill, through the same command
```

Pinning a model and keeping the CLI from wandering off to explore the repo is worth doing —
the batch already carries every resource's source, so there is nothing for a tool call to
fetch:

```jsonc
"cli_provider_command": "claude -p --model haiku --max-turns 1"
```

It does not have to be Claude. Any command that answers a prompt on stdout will do:

```jsonc
"cli_provider_command": "codex exec"
```

**What to know before switching:**

- **It is argv, not a shell line.** Words split on whitespace, `'` and `"` group, `\` escapes.
  No pipes, no redirection, no `$VAR`.
- **It spends interactive quota** — the same quota you are typing into — and adds a process
  launch per batch. That is why it is never inferred: aracne uses it only when asked.
- **The command must answer in one shot.** Its stdin carries the instructions and the batch;
  its stdout is parsed as `<resource id> :: <description>` lines, and anything else is
  ignored. A CLI that needs a flag to be non-interactive needs that flag here.
- **Lazy fills are on the read path.** A cold repo with a CLI provider means a process launch
  inside a read; `descriptions.lazy.timeout_seconds` (default 120) bounds it, and
  `descriptions.lazy.parallel` (default 4) decides how many run at once. Sweeping the repo
  once with `arac descriptions generate` first makes this a non-issue.

### One run, without touching the config

`arac descriptions generate --cli` describes the repository through a command for that run
only, overriding whatever `descriptions.provider` says:

```sh
arac descriptions generate --cli "claude -p"       # a command you named
arac descriptions generate --cli "codex exec"      # any command works here too
arac descriptions generate --cli                   # the default, `claude -p` — asks first
```

Quote a command that has flags of its own: bare `--cli` and `--cli <command>` are the same
flag, so `--cli claude -p` would read `-p` as one of *aracne's* flags.

A **bare** `--cli` is the one form that stops to ask, because it is the one where the command
being paid for is not one you typed:

```
WARNING: --cli with no command will describe this repository by running:

    claude -p

  * This spends the subscription behind that CLI, NOT an API key. On some plans that
    is a SEPARATE pool of credits from the one your API key bills, and it shares the
    interactive quota you type into. Check with your provider what a run like this
    costs you before answering.
  * A sweep can be thousands of resources, one process launch per batch.
  * Nothing here is undone by stopping halfway: descriptions already written stay
    written, and re-running only describes what is still missing.

To skip this question, name the command yourself: --cli "claude -p"

Describe this repository with `claude -p`? [y/N]
```

Naming the command (`--cli "claude -p"`) or passing `-y` skips the question. With no terminal
to ask — a pipe, a cron job, CI — a bare `--cli` refuses rather than assuming an answer, and
says which of those two to use instead.

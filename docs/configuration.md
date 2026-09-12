# Configuration — `.aracne/config.json`

Written by `arac init`, rendered into integration files by `arac setup`, and read by
everything (`internal/helper/config.go`). The schema is free-form: an invalid file is
overwritten with defaults rather than half-migrated.

Every tool name in this file is validated against the
[`toolspec`](architecture.md#6-the-tool-catalog-internaltoolspec) catalog at load/init time,
so a typo fails fast instead of silently disabling a tool.

## The top-level dials

### `mode`

Which tools exist, which shell commands aracne answers, and what vocabulary the generated
contract teaches. It has its own page: **[modes.md](modes.md)**. Absent resolves to `cli`.

### `contract_verbosity`

`low` (the default) or `high`. It sits next to `mode` at the top level because there is
exactly one contract: `CLAUDE.md`, `AGENTS.md` and the system prompt `arac agent` sends are
the same bytes, rendered by `prompts.ContractContent`. `mode` decides *which* capabilities
that document may name; `contract_verbosity` decides *how much* it says about them. The two
are orthogonal, and both settings render from the same four-way mode switch.

- **`low`** — what the graph is, how it reaches this surface, and the one or two facts the
  model cannot derive from what it is already shown. Roughly 1-2 KB, and every byte of it is
  re-sent on every request, which is why it is the default: on a harness with tool schemas and
  a system prompt of its own, most of the long version is paid for twice.
- **`high`** — the same mode-shaped document at length: what a read returns and how to read a
  `# CONTEXT:` block, how the project's language spells a resource ID, what the graph's edges
  mean in that language, and the full guidelines. Roughly 4-5 KB. Reach for it on a harness
  with no prompt of its own, or with a model that needs the read discipline spelled out.

The language halves come from the topology's own languages, read from the database at
`arac setup` time (`internal/prompts/languages.go`). A project scanned in several languages
gets each one's section; a project scanned in none yet — a fresh checkout, before the first
scan — gets the language-free form, and the next `arac setup` after a scan fills it in.
(`arac init` scans before it writes, so a wizard-generated contract already names the
languages.)

### `preload_mcp_tools`

`true`, `false`, or absent. Claude Code only, and `mode: "mcp"` only.

Claude Code defers tool schemas past a size threshold: a deferred tool reaches the model as a
bare name with no parameters and no description, and calling it costs a schema lookup first.
That tax lands on exactly the tools this mode exists to serve, and it is paid against a `grep`
sitting right there fully described — so the tool that is cheaper to *reach for* wins over the
one that is cheaper to *use*. Setting this to `true` makes `arac setup` write

```json
{ "env": { "ENABLE_TOOL_SEARCH": "false" } }
```

into `.claude/settings.json`, so every tool arrives with its schema. `arac init` asks for it as
question seven, on the one combination where it means anything.

The switch belongs to Claude Code and is not per-server: it loads *every* tool in the session up
front, including any from other MCP servers. On a session with several of them that is real
context spent on every request, which is why it is asked rather than assumed.

**Three states, and absent is not `false`.** Absent means aracne does not manage the variable,
so a project that predates this key — or one whose operator set `ENABLE_TOOL_SEARCH` themselves
— is left exactly as it is. `false` means aracne manages it and withdraws it. Only the literal
value aracne writes (`"false"`) is ever removed again: an `"auto:40"` you set yourself survives
`arac setup` and `arac disable` alike.

Setup applies this in every mode, not only `mcp` — that is what withdraws the variable when a
project moves off `mcp`, without needing the question re-answered. Moving back restores it.

## Sections

### `scan` & `scanner`

| key | default | meaning |
|---|---|---|
| `scan.ignore` | `[]` | `.gitignore`-style globs skipped on every **scanner** walk — discovery, manifest, parsing, and language detection. No `!` negation; use `paths` to re-include a subtree. It does **not** narrow `grep`: what a search may reach on disk is a separate question from what earns a place in the topology, and a search prunes only what the project's own `.gitignore` prunes. |
| `scan.workers` | `0` (NumCPU) | Max files parsed concurrently during a full scan. Lower it to cap peak RAM. |
| `scan.progress` | `auto` | `auto` (on a terminal, above 15 files), `always`, `never`. |
| `scan.pre_tool` | `default` | The scan the guard runs *before* every tool call it sees, on both harnesses. `default` is incremental, so the usual case — nothing changed since the last call — is a no-op. `none` switches the freshness guarantee off for projects keeping the topology current another way (e.g. `arac scanner run`). `full` re-scans every file, preserving descriptions and any outstanding warnings whose cause is still on disk. `hard` is **rejected**: it rebuilds from scratch, which would clear every description, bug and warning *before every tool call* — use `arac scan --hard` for a one-off rebuild. An unrecognized value resolves to `default`. |
| `scanner.update_frequency` | `200` (ms) | How often `arac scanner run` polls for changes. |

`ignore`, `workers` and `progress` are each also a flag on `arac scan`, which wins for that
run.

### `read`

| key | default | meaning |
|---|---|---|
| `max_file_size` | `524288` | Files above this are neither read nor indexed: every scan skips a source file over it, and a whole-file read of one is refused. |
| `kinds` | `["file","function","struct","interface"]` | Allow-list of resource kinds that may be read. Also accepts `named_type`, `package`, `dependency`, `variable`. An explicit `[]` is rejected by validation. |
| `context_filter` | `normal` | `off` (the code asked for, nothing around it), `normal` (each neighbour as `id: description`, undescribed ones omitted), `full` (neighbours as fenced source cuts, undescribed ones kept, plus a `# USED BY:` section). |
| `file_mode` | `skeleton`, except `full` in `mcp` | What a whole-*file* read returns. `skeleton` is each top-level declaration's signature with large bodies elided. |
| `skeleton_threshold` | `12` (lines) | How long a declaration may be before `skeleton` elides its body. |
| `max_symbol_lines` | `160` (lines) | Caps a *symbol* body the same way. `0` means no cap; absence and an explicit `0` are distinguishable. |
| `pipe_passthrough` | `true` | Exempt read/grep commands consuming piped stdin (`cmd \| tail`) from the guard — they operate on command output, which aracne cannot serve. |

**`kinds` gates every read entrance, in every mode**: the MCP `read` tool, `arac read`, an
intercepted shell read (`cat`, `head`, `sed -n` — by path *or* by resource ID), and the denial
proxy. An id that resolves to a kind outside the list is refused, naming the kind and the
allowed set, and the refusal exits non-zero rather than falling back to the plain command. An
operand that resolves to *nothing* is a different answer and still passes through. `--kind`
narrows which resource an id resolves to; it does not grant a kind the list excludes.

**Why `mcp` defaults `file_mode` to `full`.** A model that has only seen signatures must not
build an `edit` `old_string` from them, and on the MCP surface a file read is often the only
thing it sees before editing.

### `grep`

| key | default | meaning |
|---|---|---|
| `description_kinds` | `["function","method","struct","interface"]` | Which kinds may match the pattern on their stored *description*. |

A node found by its description is returned even when its source contains no matching line,
which is the whole point — descriptions live only in the topology database. The default is the
same set aracne *writes* descriptions for. `file`, `package` and `variable` are excluded
because their descriptions are thin or auto-seeded from doc comments and flood results without
answering anything. Absent means the default set; an explicit `[]` disables description
matching entirely.

### `terminal`

| key | default | meaning |
|---|---|---|
| `max_overserve` | `4` | An answer must stay within this multiple of the bytes the plain answer it replaces would have cost. `0` or negative disables the check. |

A narrow question answered disproportionately is not a cheaper read — it is a way to spend the
context window on one `head -1`. Every surface that answers in something else's place measures
itself here: intercepted commands, the guard's proxied read, and the read and search tools.
Over the ceiling a shell command runs for real, and a tool serves the plain answer instead —
source without its context block, matches without their node rows. Small answers always pass
(12KB for a read, 2KB for a search) and nothing exceeds 32KB; a search that matched only names
and descriptions is exempt, having no plain answer to be measured against.

### `descriptions`

| key | default | meaning |
|---|---|---|
| `kinds` | `["function","method","struct","interface"]` | Which kinds the description workflows target and the no-description tools list. |
| `style_exemplars` | `1` | How many already-written neighbour descriptions to feed the executor as house-style anchors. `0` disables. |
| `include_not_visible` | `false` | When false, skip undocumented targets `read.context_filter` would not render anyway. |
| `lazy` | `true` | See below. |
| `provider`, `base_url`, `api_key_env`, `cli_provider_command` | — | [Who writes the descriptions](#who-writes-the-descriptions). |

**`lazy`** generates a missing description at the moment a read or a search is about to show
it, instead of only in an `arac descriptions generate` sweep. The read plans the nodes its
`# CONTEXT:` / `# USED BY:` sections will name, generates the missing ones with the
description-executor's model, waits for them to land in the database, and re-renders; a search
does the same for the nodes it found by name or by content. Reads are slower on a cold repo
and converge on the old speed as it warms up. See `internal/lazydesc`.

It accepts **both** JSON shapes for one key — a bare `true`/`false`, or an object — and is
written back in whichever you used:

| field | default | meaning |
|---|---|---|
| `enabled` | `true` | The switch. |
| `max_nodes` | `40` | Nodes one fill may describe. `≤ 0` means no cap. |
| `timeout_seconds` | `8` | Bounds the whole fill, not one batch — and it sits on the read path, so it is the ceiling on how long a `cat` can hang. `≤ 0` disables the deadline. |
| `batch_size` | `5` | Resources per completion. Non-positive keeps the default. |
| `parallel` | `4` | Batches in flight at once. Non-positive keeps the default. |

With nothing configured to write with, the lazy fill is a silent no-op rather than an error.

### `llm`

Per-harness agent config under `<any>` / `opencode` / `claude_code`, each with a `main_agent`
and a map of named `agents`.

| field | type | meaning |
|---|---|---|
| `model` | string | Model for this agent. `"<inherits>"` copies the main agent's. |
| `mcp_tools` | string[] | Which MCP tools this agent gets. Validated against the catalog, then filtered through `Config.ServableMCPTools`, which drops the shell-served ones and returns nothing outside `mcp` mode. |
| `blocked_tools` | string[] | Native tools to deny: `read`, `grep`, `edit`, `write`, `bash`. Empty by default. Only bites in `mcp` and `cli`; `grep` is additionally dropped outside `mcp`. |
| `plugins` | string[] | Currently one: `edit-update-db-plugin`, which installs the native-edit topology-sync hook (and `arac setup` removes it again once it is no longer listed). An optimization, not the warning channel: it syncs inline with the edit, where the guard otherwise syncs on the PostToolUse that follows it. Warnings reach the model either way, once. |
| `params` | map[string]int | Integer knobs — `max-batch-size` for the description executor, `thinking` for chat sub-agents. Non-positive falls back to the default. |

Resolution: the per-harness block beats `<any>`; an absent field or the sentinel
`"<inherits>"` falls back to the main agent (`EffectiveAgent`). This is what `arac setup` and
`arac serve --tool-profile` consult to decide what each agent can do.

The main agent's default set is `read` + `warnings_list`
(`helper.DefaultAgentMCPTools`); the description executor's is `read` +
`update_description`.

### `viz`

| key | default | meaning |
|---|---|---|
| `graph.optimization_rules` | `.aracne/optimization_rules.json` | Where the graph's collapse rules live. |
| `chat.main_agent`, `chat.agents` | — | The Chat tab's agents: a flat `tools` list plus optional `model` and `params`, with no mcp/native split. Only read when `features.chat` is on. |

### `features`

Optional surfaces that are not part of the default product. An **absent section means every
feature is off**, which is what an existing project's config decodes to — so adding a feature
here never turns something on for an existing user.

| key | default | turns on |
|---|---|---|
| `bug_management` | `false` | The bug pipeline: `arac setup` writes the hunter/judge/solver agents and their commands, the `bug_*` tools become servable, the `arac bug` usage block prints, and viz exposes `/api/bugs`. `arac bug` stays dispatchable either way. |
| `chat` | `false` | The viz Chat tab: the `/api/chat`, `/api/chat/` and `/api/context-graph` routes, and the Chat nav item. |
| `agent` | `false` | `arac agent`, the self-contained REPL. This gates the *command*, not `internal/llm/agent` — `descriptions generate` runs its executors through the same package. |

> A new flag needs adding to `validConfig` as well as to the struct. See the comment there for
> the silent failure that omission causes.

### `paths`

A list of `{path, hidden}` rules, relative to the topology root, that hide or show subtrees.
Hidden paths are skipped by the indexing stage (file discovery, manifest) and the scan stage in
every mode. **More specific rules win**, so a parent can be hidden while a nested child stays
visible — which is the thing `scan.ignore` cannot express, since it has no negation:

```jsonc
"paths": [
  { "path": "my_example",               "hidden": true  },
  { "path": "my_example/another_layer", "hidden": false }
]
```

Resolved by `domain.PathVisibility` and installed as the active filter by the topology manager
before each scan.

## Who writes the descriptions

Descriptions have two entry points — the **lazy fill** on the read path and the
**`arac descriptions generate`** sweep — and **one** provider block, on the `descriptions`
section itself:

| key | meaning |
|---|---|
| `provider` | `anthropic` \| `openai` \| `deepseek` \| `cli`. **Absent means unanswered**, and `arac init` asks — see [the setup questions](#the-setup-questions). A typo, or `cli` with no command, is rejected by validation. |
| `base_url` | A gateway or proxy for the API providers. Ignored by the CLI ones. |
| `api_key_env` | The environment variable the API key is read from. Absent falls back to the provider's own name (`ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `DEEPSEEK_API_KEY`). Ignored by `cli`. |
| `cli_provider_command` | The command `provider: "cli"` runs. Required by it, ignored by everything else. |

They live on the section rather than under `lazy` because both entry points describe the same
resources into the same database: a project that has answered "who writes my descriptions" has
answered it once, for both.

`api_key_env` is a separate question from `provider` because the two are only accidentally
related. A great many vendors and gateways serve the *OpenAI format* while billing a key
OpenAI never issued, and a project pointed at one should not have to store that credential in
a variable named after a company it is not paying.

A provider the project **named** is never silently swapped for another vendor whose key
happens to be in the environment — it was asked, and that is the answer. (A provider that was
only ever *inferred* still falls back to the key the machine actually holds, for the sweep;
the lazy fill never falls back at all.)

The three API providers read `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and `DEEPSEEK_API_KEY`
unless `api_key_env` names another variable. Nothing else in aracne needs a key.

### The setup questions

Nothing is guessed. A fresh config names no provider and pins no model, and `arac init` asks —
full screen, with the arrow keys, one question per screen:

```
   ▄▀█ █▀█ ▄▀█ █▀▀ █▄░█ █▀▀
   █▀█ █▀▄ █▀█ █▄▄ █░▀█ ██▄

  Who writes your descriptions?                                              3/7

  Aracne describes every function, type and file so a read can show you what its
  neighbours are without opening them. Something has to write those, and it
  costs money either way -- so it is asked rather than assumed.

  > An API key     Call a provider's HTTP API.
    A CLI command
                   Bills the key's balance, per token. Fastest, and the only
                   option that parallelises properly.

                   You will be asked which wire format the endpoint speaks and
                   which environment variable holds the key.

  ↑/↓ move   ⏎ select   esc cancel
```

**An API key** asks which *wire format* the endpoint speaks (`anthropic` / `openai` /
`deepseek` — the format, not necessarily the company), then which environment variable holds
the key, offering that provider's usual name. **A CLI command** asks for the command, offering
`claude -p`, with what a CLI run actually spends spelled out beside it. Every open question
offers the default as the first row and `Other:` as the second, where you type; Enter on an
empty `Other:` says so and keeps the question open.

Then the model, written to the `descriptions-generation-executor` agent (see
[below](#the-model-is-not-here)). It defaults to the chosen provider's cheap tier —
`claude-haiku-4-5`, `gpt-5.4-mini`, `deepseek-v4-flash`. On the CLI branch it offers `haiku`
instead — the same model, spelled the way a command line wants it, because that answer is
appended to a command rather than sent as a request field. There is one more rule there, since
the CLI transport reads its model from the *command* and from nowhere else:

- A command that already names one (`claude -p --model sonnet`) **answers this question**. The
  model question is skipped and `sonnet` is recorded.
- Otherwise the answer is appended back onto the command — but only for the Claude CLI in
  print mode (`claude -p` / `claude --print`, with any other flags, by any path), whose flag
  aracne actually knows. `codex exec` and a hand-written script
  are left exactly as typed: guessing a flag onto someone else's program is how a wizard turns
  a working command into one that exits 2.

Question five — describe the repository now, or lazily as you read — **saves nothing**. Lazy
generation is on either way (`descriptions.lazy`, default `true`); the question only decides
whether this run also sweeps before it finishes. Enter means **now** at 800 source files or
fewer and **lazily** above that, which is a wall-clock judgement rather than a cost one: a
small repository is minutes and is better off fully described, while a large one becomes a
long unattended job whose benefit all arrives at the end. It is a *file* count and not a
resource count because the questions come before the scan; roughly ten describable resources
per source file is what aracne's own corpora come out at. Either answer is available at either
size.

Three things deliberately never reach these questions:

- **The lazy fill on the read path.** A read is not the place to stop and ask which vendor to
  bill, so it stays silent and does nothing until `arac init` — or a hand-written config — has
  answered.
- **`arac descriptions generate`.** The sweep asks nothing at all. Someone running it has asked
  for descriptions, not for a setup interview. An unconfigured project is told to run
  `arac init`, given the keys to write by hand, and the command exits non-zero — on a terminal
  and off it:

  ```
  $ arac descriptions generate
  Error: descriptions generation is not configured.

  Run `arac init` to set it up, or write it into .aracne/config.json yourself:

    "descriptions": {"provider": "anthropic", "api_key_env": "ANTHROPIC_API_KEY"}
    "descriptions": {"provider": "cli", "cli_provider_command": "claude -p"}

  (providers: anthropic, openai, deepseek, cli)
  ```
- **`--cli`.** The flag *is* the answer for that run, and it still works on a project that has
  answered nothing.

**An unattended `arac init`** is refused rather than half-run. A pipe, a cron job or CI gets a
message naming `arac setup` and the config keys it would have set, and exits non-zero; nothing
is written, not even the `.aracne` directory. A full-screen prompt with nobody at it is a hang.

### The model is not here

It is the `descriptions-generation-executor` agent's, and only its:

```jsonc
{ "llm": { "<any>": { "agents": {
  "descriptions-generation-executor": { "model": "claude-haiku-4-5" }
} } } }
```

That is the model both entry points describe with — the sweep *runs* as that agent, and the
lazy fill reads the same field. There is no `descriptions.model`: one answer in one place
cannot disagree with itself, and when two of them disagree there is nothing on screen to say
which won.

`arac init` writes it under `<any>` rather than under a harness block, because `EffectiveAgent`
merges `<any>` underneath whichever harness asks: one write covers Claude Code, OpenCode, and
the read-path lazy fill (which resolves against `claude_code` by default).

**The harness agent files get it only where the harness can run it.** `arac setup` also renders
the executor as the sub-agent `/descriptions-generate` fans out to, and there the value is read
per harness rather than copied — the wizard writes whatever the describer answer called for, and
`gpt-5.4-mini` is right for the API sweep and meaningless to Claude Code:

- `.claude/agents/descriptions-generation-executor.md` gets `model:` for a Claude Code alias
  (`haiku`, `sonnet`, `opus`, `inherit`) or a `claude-*` id; an `anthropic/claude-*` is unwrapped.
- `.opencode/agents/descriptions-generation-executor.md` gets it in OpenCode's `provider/model`
  form: a value that already has a `/` as written, and a bare id qualified with
  `descriptions.provider` when that is `anthropic`, `openai` or `deepseek` and no `base_url` is
  set (behind a gateway, `openai` names a wire format, not OpenCode's OpenAI provider).
- Anything else leaves the line out, and the sub-agent runs on the main agent's model. The lazy
  fill and `arac descriptions generate` describe with the configured model either way.

**It is unset out of the box**, and writing it is optional: without one, the chosen provider's
own fallback is used (`claude-haiku-4-5`, `gpt-5.4-mini`, `deepseek-v4-flash` — see
`lazydesc.providerFallbackModel`). Unset is load-bearing rather than an omission: a model
pinned here also picks the *provider* by inference, so a stock value would answer a setup
question on the project's behalf — and then fail naming a vendor the project never chose,
about a key it had no reason to hold.

### Describing with a CLI, no API key

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

That is the whole setup. `claude` must be on `PATH` and already logged in (`claude` once,
interactively, is enough). Both entry points then use it:

```sh
arac descriptions generate    # the sweep, through `claude -p`
arac read internal/server.go  # a lazy fill, through the same command
```

Pinning a model and capping the turns is worth doing — the batch already carries every
resource's source, so there is nothing for a tool call to fetch. It does not have to be Claude;
any command that answers a prompt on stdout will do:

```jsonc
"cli_provider_command": "claude -p --model haiku --max-turns 1"
"cli_provider_command": "codex exec"
```

**What to know before switching:**

- **It is argv, not a shell line.** Words split on whitespace, `'` and `"` group, `\` escapes.
  No pipes, no redirection, no `$VAR` — running it through a shell would hand a config file
  the power to run arbitrary shell, to buy expansion nobody asked for.
- **It spends interactive quota** — the same quota you are typing into — and adds a process
  launch per batch. That is why it is never inferred: aracne uses it only when asked.
- **The command must answer in one shot.** Its stdin carries the instructions and the batch;
  its stdout is parsed as `<resource id> :: <description>` lines, and anything else is
  ignored. A CLI that needs a flag to be non-interactive needs that flag here.
- **Lazy fills are on the read path.** A cold repo with a CLI provider means a process launch
  inside a read; `descriptions.lazy.timeout_seconds` bounds it and `parallel` decides how many
  run at once. Sweeping once with `arac descriptions generate` first makes this a non-issue.

**For one run, without touching the config**, `arac descriptions generate --cli` overrides
whatever `descriptions.provider` says:

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

# Configuration — `.aracne/config.json`

Written by `arac init`, read by everything (`internal/helper/config.go`). A free-form
schema and a clean break from older formats: an invalid or outdated file is overwritten
with defaults rather than half-migrated.

The one key that matters most, **`mode`**, has its own page: [modes.md](modes.md).

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
- **`read`** — `max_file_size`; **`kinds`** (allow-list of resource kinds the **MCP `read`
  tool** returns — default `file`, `function`, `struct`, `interface`; also accepts
  `named_type`, `package`, `dependency`, `variable`). Note the scope: every other read path
  — `arac read`, an intercepted shell read, the denial proxy — deliberately reads every
  kind, because the key exists to narrow what a *model* is offered through a tool schema,
  not to lock a person out. Outside `mcp` it therefore has no effect.
  **`context_filter`** — one of `off`, `normal` (the default) or `full`: how verbosely the
  `# CONTEXT:` block renders a read's neighbours. `full` also adds the `# USED BY:` section
  and keeps undescribed neighbours. **`file_mode`** (`full` / `skeleton`) decides what a
  whole-**file** read returns; the default depends on the surface — `skeleton` everywhere
  except `mcp`, which defaults to `full` — with `skeleton_threshold` and `max_symbol_lines`
  bounding what elides. **`pipe_passthrough`** (whether the guard exempts piped reads like
  `cmd | tail`).
- **`descriptions`** — which `kinds` to document + `style_exemplars` count, and
  **`lazy`** (default **true**): generate a missing description at the moment a read or a
  search is about to show it, instead of only in an `arac descriptions generate` sweep. The
  read plans the nodes its `# CONTEXT:` / `# USED BY:` sections will name, generates the
  missing ones with the description-executor's model (`haiku` by default), waits for them to
  land in the DB, and re-renders; a search does the same for the nodes it found by name or by
  content. Reads get slower on a cold repo and converge on the old speed as it warms up. It
  accepts `true`/`false` or an object (`enabled`, `max_nodes`, `timeout_seconds`,
  `batch_size`, `parallel`, `model`, `provider`), and is a no-op with no provider API key in
  the environment. See `internal/lazydesc` and `docs/design/plan-lazy-descriptions.md`.
- **`llm`** — per-harness agent config under `<any>` / `opencode` / `claude_code`,
  each with `main_agent` + named `agents`. Fields: `model`, `mcp_tools`,
  `blocked_tools`, `plugins`, `params`. Resolution: per-harness block beats
  `<any>`; `"<inherits>"`/absent fields fall back to the main agent
  (`EffectiveAgent`). This is what `arac init` and `arac serve --tool-profile`
  consult to decide what each agent can do.
- **`viz`** — `graph.optimization_rules` path + `chat` (self-contained
  proprietary-chat `main_agent` + sub-agent tool lists; never inherits from `llm`).
- **`paths`** — a list of `{path, hidden}` rules (paths relative to the topology
  root) that hide/show subtrees. Hidden paths are skipped by the indexing stage
  (file discovery / manifest) and the scan stage in every mode (default/all/hard).
  More specific (more internal) rules win, so a parent can be hidden while a
  nested child stays visible (e.g. hide `my_example` but keep
  `my_example/another_layer`). Resolved by `domain.PathVisibility` and installed
  as the active filter by the topology manager before each scan.

Every tool name in the config is validated against the **`toolspec`** catalog at
load/init time so typos fail fast.

## `features` — surfaces that are not part of 1.0

Optional surfaces are off unless switched on. An absent `features` block means every
feature is off, so adding one here never turns something on for an existing project.

| key | default | what it enables |
|---|---|---|
| `bug_management` | `false` | The bug pipeline as a whole: `arac init` writes the bug-hunter/judge/solver agents and their commands, the `bug_*` MCP tools become servable, the `arac bug` usage block prints, and viz exposes `/api/bugs`. |
| `chat` | `false` | The viz **Chat** tab: `/api/chat`, `/api/chat/`, `/api/context-graph`, and the Chat nav item in the SPA. |
| `agent` | `false` | `arac agent`, the self-contained REPL that talks straight to an LLM provider. |

These three ship in the source but not in the 1.0 product — they need more time in the
oven. Turning one on is supported and tested; it is simply not the default.

Two deliberate exceptions:

- **`arac bug` stays dispatchable** with `bug_management` off. It is the orchestration
  channel the generated slash commands use, and the debugging path. It is *undocumented*
  rather than removed — `PrintUsage` strips its blocks.
- **The `bugs` table is always created.** It is a leaf table nothing joins against, so an
  empty one costs nothing, and dropping the DDL would need a migration story for every
  existing `topology.db`.

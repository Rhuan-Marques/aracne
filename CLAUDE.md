---

# Project Reference: What Aracne *Is* and How It Works

> Everything above this line is the *injected integration contract* — it tells an
> LLM how to navigate a project that has aracne installed. Everything below is a
> reference for working *on aracne itself*: what the codebase contains, how the
> pieces fit, and the surfaces a user can drive.

## 1. One-paragraph summary

**Aracne** is a static-analysis engine that scans a multi-language source tree
(**Go, Python, JavaScript, TypeScript**) and builds a **"topology"**: a directed
graph of every package/module, file, function, method, struct (a.k.a. class in
JS/TS/Python), interface, named type, variable, and dependency, plus the edges
between them
(calls, uses, implements/implemented-by, imports, etc.). The graph is persisted
to an **SQLite database** (`.aracne/topology.db`) and kept current as files are
edited. On top of that graph it exposes **topology-aware code navigation** to
LLM agents (so the model queries a pre-analyzed graph with rich descriptions
instead of grepping raw files) and to humans (via a CLI and a web visualizer).
The shipped binary is **`arac`** (module `aracne`, Go 1.25).

## 2. Repository map

```
main.go                      Subcommand dispatcher → internal/cli.*
CLAUDE.md / AGENTS.md        Injected integration contract (Claude Code / OpenCode)
LLM_INTEGRATION_CHARTER.md   Long-form spec of the three integration modes
.aracne/                     Per-project state: topology.db, config.json, file_manifest.json,
                             optimization_rules.json, providers.json, agents/*.md, chat/*.json
.claude/ .opencode/          Harness integrations: agents, slash commands, hooks, plugins, MCP config
internal/
  cli/            Every `arac <cmd>` entry point (scan, serve, viz, agent, init, read, grep,
                  cmd, edit, write, descriptions, bug, analyze, guard, update-file, …) + tool-registry wiring
  shellcmd/       Pure argv→request parser for the terminal surface: decides whether a shell
                  read/search is one aracne models, and what window it asked for. Anything with
                  a flag it does not model is KindPassthrough — see §7E.
  topology/       The engine
    domain/         Pure model: Resource, Topology, ResourceKind, TopologyWarning, KnownBug, Location, Visibility, Cut
    scanner/        Registry + LanguageScanner/PartialUpdater interfaces
      goscanner/      Go parser (stdlib go/ast, go/parser, go/token)
      pyscanner/      Python parser (custom, no external grammar)
      jsscanner/      JS + TS parser (tree-sitter via CGO — needs gcc)
    golang/ python/ javascript/   Per-language managers + mapper/connections/resources/visibility (graph builders)
  helper/         Storage + plumbing: sqlite/db, manifest, incremental & partial writes, config, apply/edit, normalize
  llm/
    provider.go     Provider interface (Chat / StreamChat) + message/tool types
    providers/      anthropic, openai, deepseek implementations
    agent/          Internal REPL agent loop + sub-agent runner
    tools/          MCP tool implementations (read*, grep, edit, write, bug_*, warnings_list, ls) + Registry
    languages/      Per-language tool flavors: gotools / jstools / pythontools / universaltools
  mcp/            JSON-RPC 2.0 MCP server over stdio (initialize / tools/list / tools/call)
  chat/           Proprietary chat engine behind the viz UI (sessions, native tools, CreateTasks sub-agents)
  viz/            HTTP server + go:embed'd static SPA (graph + chat) + websocket + context-graph
  prompts/        Generators for CLAUDE.md/AGENTS.md, agent .md files, slash commands, system prompts
  toolspec/       Single source of truth catalog of tool names (MCP vs chat vs blockable-native)
  topogrep/       Topology-annotated grep: node name > node description > line match, path:line:match
tests/            Cross-cutting suites incl. atscale_* topology-consistency (3 scan modes) + per-scanner tests
testing_ground/   Hand-built multi-language corpus of edge cases (see its README.md)
```

## 3. Core model (`internal/topology/domain`)

- **`Resource`** — one node: `ID`, `Kind`, `Name`, `Language`, `Description`,
  `Location` (file + start/end line), free-form `Properties` (e.g. typed
  `input`/`output`/`underlying`), and `Connections` (`map[edgeType][]targetID`).
- **`ResourceKind`** — `package`, `file`, `function`, `method`, `struct`,
  `named_type`, `interface`, `variable`, `dependency`. (Python/JS are
  *modules-first*: they drop the `package` node in favor of file-first IDs and
  `imports_module` edges.)
- **`Topology`** — `Root`, `Language`/`Languages`, `Resources` map, `Warnings`
  map, `Errors` map. Multiple languages merge into one graph (`Language="multi"`).
- **`TopologyWarning`** — surfaced after edits: `use_missing_node`,
  `node_removed`, `signature_changed`. Tells an agent its change may have broken
  callers.
- **`KnownBug`** — a reported defect on a node with state `pending` →
  `acknowledged` / `dismissed`, driving the bug-hunter/judge/solver workflow.

## 4. The scanning pipeline (`internal/topology`)

- **`scanner.Registry`** holds the `LanguageScanner` implementations and detects
  which apply to a root (by detection file *and* by recursively finding source
  files, skipping `.git`/`.aracne`/`node_modules`/`vendor`/`__pycache__`).
- **`TopologyManager`** (`manager.go`) is the orchestrator:
  - `FullScan` / `FullReScan` (the latter preserves descriptions) / `IncrementalScan`.
  - **Incremental scan** diffs the on-disk tree against `file_manifest.json`,
    then takes one of two paths:
    - **Partial fast-path** (`tryPartialIncremental` + `PartialUpdater`): a single
      changed source file whose edit touches no cross-file signature/identity is
      persisted as a *scoped delta* without loading the whole graph.
    - **Full two-phase path**: parse every changed file, then re-resolve changed
      files **plus reverse-caller files** of any symbol whose signature/identity
      changed, so body edges (`calls`, `uses_*`) never go stale. Correctness over
      coverage — anything unsafe falls back here.
  - Per-file edit serialization via `WithFileLock` (parallel sub-agents editing
    the *same* file can't clobber each other; different files run concurrently).
  - Also the home of bug/description/warning CRUD that the tools call into.
- **Storage** lives in `internal/helper`: pure-Go `modernc.org/sqlite`,
  incremental/scoped writes (`WriteIncremental`, `WriteScopedResources`),
  manifest sync, orphan cleanup, and resource fingerprinting for minimal diffs.

## 5. Configuration — `.aracne/config.json` (`internal/helper/config.go`)

A free-form schema (a clean break from older formats — invalid/old files are
overwritten with defaults). Top-level sections:

- **`mode`** — `mcp` | `aracne_read` (default) | `intercept_id` | `line_range`. **The one dial.**
  It answers three questions at once — which tools exist, which shell commands aracne answers,
  and what vocabulary the contract teaches — and every generated artifact derives from it.

  | mode | MCP tools | shell reads | shell grep | `blocked_tools` | addressing |
  |------|-----------|-------------|------------|-----------------|------------|
  | `mcp` | one `read` (+ `warnings_list`, `bug_*`) | run as themselves | intercepted | **active** | resource IDs |
  | `aracne_read` *(default)* | none | run as themselves | intercepted | inert | resource IDs, via `arac read` |
  | `intercept_id` | none | **intercepted** | intercepted | inert | resource IDs, as command operands |
  | `line_range` | none | **intercepted** | intercepted | inert | `path:start-end` |

  Three things hold in every mode: shell `grep` is answered by the annotated grep (it is the one
  capability with no competing surface — no read tool answers "which node is DESCRIBED as X"),
  edits re-sync the topology through the `arac update-file` hook, and `arac read`/`arac grep`
  work from the CLI.

  `grep`, `edit` and `write` are **not** MCP tools in any mode (`toolspec.IsShellServedTool`):
  their shell forms are intercepted everywhere, so a tool for them offered a second way to ask
  one question and charged a schema block per request for it. `Config.ServableMCPTools` is the
  one filter every consumer goes through — the server registry, the generated agent markdown,
  both harnesses' permission blocks — so a name in one that the server does not register cannot
  drift in unnoticed.

  **WHY FOUR MODES AND NOT A CROSS-PRODUCT.** This was `integration.mode` (terminal/mcp/both)
  × `identification_mode` (id/line_range) × five `terminal` booleans, with nothing folding them
  together — so combinations existed that made no sense and shipped anyway. `mcp` +
  `line_range` printed `path:start-end` in every grep header while the only reader in the
  project was an MCP `read` that takes ids and has no line-range argument, and that project's
  contract never mentioned a range at all. The surfaces are not independent axes.

  **Both retired keys still map forward silently** (`Config.legacyMode`), so an existing config
  keeps behaving as it does today: `mcp` → `mcp`; `terminal`/`both` → `intercept_id` or
  `line_range` depending on the old `identification_mode`. `both` lands on an intercepting mode
  because that is what those projects were running; the tools it also had are the half being
  dropped. A config with neither key resolves to `aracne_read`.

  `line_range` carries a promise the slice reader enforces: reading an advertised span returns
  byte-for-byte what the resource read would have, imports and context included
  (`whollyContained` in `universaltools/slice.go`). It is the default addressing for the
  intercepting pair on evidence — over `ab-prefer-ids-20260902a` the model used a resource ID as
  a command operand **0 times in 408 shell commands** while addressing code as file+line in all
  408.
- **`terminal`** — `max_overserve` only: the answer must stay within this multiple of the bytes
  the real command would have printed, or `arac cmd` passes through instead. Its five former
  booleans (`intercept`, `enhance_files`, `enhance_resources`, `grep`, `prefer_resource_ids`)
  are facts about the mode now.

- **`scan`** / **`scanner`** — default mode (`default`/`hard`/`all`) for the
  one-shot `arac scan` and the live scanner; `update_frequency`; and
  **`scan.pre_tool`** (`default` (the default) / `none` / `full` / `hard`) — the scan
  the guard runs *before* every tool call it sees, on both harnesses. `default` is
  an incremental scan, so the usual case (nothing changed since the last call) is a
  no-op; `none` switches the freshness guarantee off for projects that keep the
  topology current another way (e.g. `arac scanner run`).
- **`read`** — `max_file_size`; **`kinds`** (project-wide allow-list of
  resource kinds `read` will return — default `file`, `function`, `struct`,
  `interface`; also accepts `named_type`, `package`, `dependency`, `variable`);
  **`context_filter`** (how verbosely the `# CONTEXT:` block renders neighbors,
  incoming "USED BY" edges, small-fn threshold, hide-undocumented,
  `max_inline_parent_lines`); **`pipe_passthrough`** (whether the guard
  exempts piped reads like `cmd | tail`).
- **`descriptions`** — which `kinds` to document + `style_exemplars` count, and
  **`lazy`** (default **true**): generate a missing description at the moment a read or a
  search is about to show it, instead of only in an `arac descriptions generate` sweep. The
  read plans the nodes its `# CONTEXT:` / `# USED BY:` sections will name, generates the
  missing ones with the description-executor's model (`haiku` by default), waits for them to
  land in the DB, and re-renders; a search does the same for the nodes it found by name or by
  content. Reads get slower on a cold repo and converge on the old speed as it warms up. It
  accepts `true`/`false` or an object (`enabled`, `max_nodes`, `timeout_seconds`,
  `batch_size`, `parallel`, `model`, `provider`), and is a no-op with no provider API key in
  the environment. See `internal/lazydesc` and `PLAN-lazy-descriptions.md`.
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

## 6. The tool catalog (`internal/toolspec`)

Single source of truth for tool names referenceable in config. Each `Spec`
marks whether it's a valid **MCP** tool (`llm.*.mcp_tools`) and/or a **chat**
tool (`viz.chat.*.tools`). Catalog highlights:

- **Reads** — a single `read`, taking a LIST of resource IDs and returning their
  source grouped by file under one `# CONTEXT:` section. It registers as
  `read_resource` when the harness keeps its own native read, and as `read` when
  `blocked_tools` denies that. Which kinds it resolves is `read.kinds`, not a
  per-agent tool list. `grep` is topology-annotated and searches node names and
  descriptions as well as file contents.
- **Mutations** — `edit`, `write` (MCP versions sync the topology DB inline).
- **Topology/maintenance** — `warnings_list`, `update_description`,
  `node_list_no_description`.
- **Bugs** — `bug_report`, `bug_list`, `bug_acknowledge`, `bug_dismiss`, `bug_delete`.
- **Chat-only native** — `ls`, `bash`, `glob`, `ask_user_question`, `CreateTasks`.
- **Blockable native** (for `blocked_tools` + the guard hook) — `read`, `grep`,
  `edit`, `write`, `bash`; the catalog also maps native tool names
  (`Read`/`Grep`/…), POSIX shell (`cat`/`head`/`grep`/`sed`/…) and PowerShell
  cmdlets to their aracne equivalents so the guard can warn/block shell bypasses.

`internal/cli/tool_profiles.go` maps each MCP tool name → constructor; most reads
use the **universal** implementations, and only `update_description` /
`node_list_no_description` dispatch per language (go/js/python managers).

## 7. User-facing surfaces (the harnesses)

There are **five** ways to use aracne, all over the same topology engine. The default is
`mode: aracne_read`; MCP (B) is opt-in via `mode: "mcp"` or `arac init --mcp`:

### A. CLI (`arac <subcommand>`) — `internal/cli`, dispatched from `main.go`
Direct, no LLM. Key commands (full list in `usage.go` / `PrintUsage`):
`scan` (`--all`/`--hard`/`--default`/`--debug`), `read`/`grep`,
`resource list`, `node count`, `warnings list`, `bug <report|list|acknowledge|
dismiss|delete>`, `descriptions <generate|apply|clear>`, `update-file`,
`update-description`, `edit`/`write` (stdin JSON), `analyze dead-code`,
`check-updates`, `init`, `disable`, `guard`, `serve`, `viz serve`, `agent`.

### E. Terminal surface (the default) — `internal/cli/cmd.go` + `internal/shellcmd`
The shell IS the tool surface. `arac cmd -- <command…>` runs a shell read or search and answers
it from the topology when it can: the exact lines the command asked for, framed by the
signature of whatever declaration they sit inside (with an elision marker for what was left
out), and a `# CONTEXT:` block restricted to the resources those lines actually mention. A
resource ID stands wherever a path does, so `head -20 app.Flask` is the first twenty lines of
the class body.

Anything else runs for real. `internal/shellcmd` returns `KindPassthrough` for every flag it
does not model (`head -c`, `tail -f`, `grep -o`, `sed` substitutions) and for the readers that
transform rather than window (`nl`, `tac`, `xxd`, `od`, `hexdump`, `strings`); `arac cmd` also
passes through for an unindexed file, an over-budget answer or a missing database, and execs
the real binary with its exit status. That fidelity is what makes interception safe on by
default — `tests/terminal_e2e_test.go` asserts byte-identical output for those cases.

The agent never types `arac cmd` itself. The `arac guard` PreToolUse hook rewrites its Bash
call via `hookSpecificOutput.updatedInput` (`internal/cli/guard_intercept.go`), so the model
writes `head -40 file.go` and reads real stdout. See §9.

### B. MCP server (`arac serve`) — `internal/mcp`
JSON-RPC 2.0 over **stdio** (`initialize`, `tools/list`, `tools/call`). This is
how **Claude Code** and **OpenCode** consume aracne (configured in `.mcp.json` /
`opencode.json`). Flags: `--tool-profile` (`main`, `all`, or a configured agent
name) and `--harness` (`claude_code`|`opencode`) — together they select which
config-resolved tool set is exposed. No API key needed; the host platform brings
its own model.

### C. Internal agent (`arac agent`) — `internal/llm/agent` + `providers`
A self-contained REPL agent that talks **directly to an LLM provider**
(`RunAgent` uses **DeepSeek**, requiring `DEEPSEEK_API_KEY`; anthropic & openai
providers also exist). It runs the same topology tool set and a topology-aware
system prompt, with a sub-agent runner for fan-out (e.g. description executors).

### D. Web visualizer + chat (`arac viz serve`) — `internal/viz` + `internal/chat`
A local HTTP server (default `127.0.0.1:7331`) serving a **`go:embed`'d static
SPA** (`internal/viz/static/`: `index.html`, `app.js`, `styles.css`) with two
screens — **Visualization** (graph) and **Chat** — plus a Settings page.
HTTP API: `/api/graph`, `/api/neighborhood`, `/api/context-graph`,
`/api/search`, `/api/node/…`, `/api/summary`, `/api/warnings`, `/api/bugs`,
`/api/config`, `/api/optimization-rules`, `/api/chat[/…]`, and `/api/ws`
(websocket for streaming). Graph "modes": *Packages & Modules*, *Data Flow*,
*Custom*; with language filtering, search, and neighborhood-depth controls.

The **chat backend** (`internal/chat`) is aracne's own agent harness powering
the viz Chat tab: session store (`.aracne/chat/*.json`), an LLM provider, an
**agent registry**, **native tools** (`ls`/`bash`/`glob`), a workspace-scoped
**permission policy**, and **`CreateTasks`** for spawning parallel sub-agents
(explorer, bug-hunter/judge/solver, descriptions executor). Its tool lists are
configured entirely under `viz.chat` and are independent of the `llm` section.

## 8. Agent workflows

These are defined as harness **slash commands** + **agent definitions** (markdown
under `.claude/`, `.opencode/`, `.aracne/agents/`) and are mirrored by the
in-repo skills:

- **Descriptions** — `descriptions generate` lists undocumented resources,
  batches them, and fans out **descriptions-generation-executor** sub-agents that
  read each resource and write a concise description; `descriptions apply` writes
  them back as source doc-comments; `descriptions clear` removes them.
- **Bug pipeline** — **bug-hunter** scans the topology and files bugs
  (`pending`); **bug-judge** triages them against dismissed patterns
  (`acknowledged`/`dismissed`); **bug-solver** fixes acknowledged bugs and
  deletes them. The main agent orchestrates the fan-out (hence it keeps read-only
  `bug_list`).

## 9. Harness integration & guards (`arac init`)

`arac init` (flags `--claude`, `--opencode`, `--global`, `-y`) wires aracne into
a project: generates the injected **CLAUDE.md / AGENTS.md**, the MCP config
(`.mcp.json` / `opencode.json`), agent + command markdown, and harness hooks/
plugins. Two hooks ship for Claude Code:

- **`arac-guard.sh`** → `arac guard --claude-hook`: the **Tool Guard**. What it does depends
  on `mode`:
  - **searches, every mode** — it **rewrites**. A single, unpiped, unredirected Bash `grep` that
    `shellcmd` models comes back as `<arac> cmd -- <the original text>` through `updatedInput`.
    The original text is reused verbatim so the shell re-splits it exactly as it would have.
    Guard rails: never a second time (`isAracCommand` stops the recursion), never across a pipe,
    a redirect, a heredoc, an `&&`, an env prefix or a wrapper, never a mutation, and never a
    file with no topology nodes.
  - **`intercept_id` / `line_range`** — the same rewrite additionally covers shell READS
    (`cat`/`head`/`tail`/`sed -n`/`awk`) on a target the topology knows.
  - **`mcp` / `aracne_read`** — reads are left alone. The read capability already has a surface
    in both (a tool, or `arac read`), and rewriting the model's `cat` on top of it would answer
    one question twice.
  - **`blocked_tools`** applies in **`mcp` only** (`Config.GuardBlocksNativeReads`). That is the
    one mode where a refusal has somewhere to send the model — an MCP tool that is in its list.
    In the other three a block would refuse a call aracne was about to answer itself, which is
    the two-turns-for-one-question failure interception was built to end. Where it does apply:
    blocking `grep` also blocks `rg`/`Select-String` run via Bash; blocking `bash` blocks the
    Bash tool entirely; a denied read is answered with its content where it can be
    (`guard_proxy.go`); piped reads (`cmd | grep`) are exempt unless
    `read.pipe_passthrough:false`. Interception is tried first, so a command aracne can serve is
    answered rather than refused however `blocked_tools` reads.
  - The **PostToolUse nudge** fires on native `Read`/`Grep`/`Edit`/`Write`, which interception
    never sees, and names the surface the mode actually has
    (`toolspec.WarningForSurface`). Note that `grep`/`edit`/`write` guidance is the `arac`
    subcommand in *every* mode including `mcp`, because no mode registers a tool for them.

  Note that `mode` governs the **main agent's** surface only. Generated sub-agents
  (`descriptions-generation-executor`, `bug-*`) declare their own scoped MCP server inline in
  their frontmatter (`mcpServers:` → `arac serve --tool-profile <agent>`), so the descriptions
  and bug pipelines keep working on the terminal surface. That is the right split: those agents
  exist to call `update_description` / `bug_*`, which have no shell equivalent worth teaching,
  and their schema cost is paid inside a short-lived sub-agent instead of on every request to
  the main one.
- **`arac-update-file.sh`** → `arac update-file --claude-hook`: re-parses a file
  into the topology after a **native** edit, keeping the graph current even when
  the change bypassed the MCP `edit`/`write` tools. Installed only when the main
  agent lists the `edit-update-db-plugin` plugin.
- OpenCode additionally gets `arac-native-edit-sync.js` (a plugin doing the same
  topology sync on native edits), under the same plugin flag.

Freshness itself is not a plugin. **Before** every tool call the guard sees, it runs the
`scan.pre_tool` scan (default: incremental) so the call is answered from a graph that
matches the code on disk — including changes nothing in the session made, like a
`git checkout`, a rebase or an editor save. Claude Code gets this from the PreToolUse
guard hook itself; OpenCode gets `arac-pre-tool-scan.js`, a plugin installed
unconditionally by `arac init` whose `tool.execute.before` runs `arac guard --pre-scan`.
Both read the same config key at call time, so `scan.pre_tool: "none"` disables it on
both surfaces without re-running init. The pre-call scan reports nothing (a PreToolUse
hook cannot address the model without blocking it); the warnings it finds are persisted
in the topology and surface through `warnings_list` / `arac warnings list`, and the
**post**-call drift check still reports the ones a shell write causes.

## 10. Languages & known quirks

- **Go** — stdlib `go/ast` parser; richest support (cross-package return-type
  inference, interface↔struct matching, generics, embedding).
- **Python** — custom parser; ABC/Protocol/dataclass, inheritance, decorators,
  cross-module resolution; *modules-first* (file IDs, `imports_module`).
- **JavaScript + TypeScript** — share one **tree-sitter (CGO)** scanner, so the
  build **requires gcc**. JS and TS are **independent topologies** (no
  cross-language import resolution). TS adds interfaces/type-aliases/enums and
  annotation-driven method resolution. Also *modules-first*.
- Scanners skip test/build artifacts: Go `*_test.go`, Python `test_*.py`, JS/TS
  `*.test.*`/`*.spec.*`/`*.min.*`, and dot/vendor dirs.

## 11. Building, testing, running

- **Build**: `go build -o bin/arac .` (CGO must be enabled for the JS/TS
  tree-sitter scanner — needs `gcc`). SQLite is pure-Go (`modernc.org/sqlite`),
  no C SQLite required.
- **Front-end**: the SPA is plain HTML/JS/CSS embedded via `go:embed`; rebuild
  the Go binary to pick up static changes. `package.json` only pulls
  `lucide-react` for icon assets.
- **Tests**: `go test ./...`. Notable: `tests/atscale_*` run a 3-scan-mode
  (full / incremental / hard) **topology-consistency** suite across languages;
  `internal/shellcmd/shellcmd_test.go` is the executable spec of which shell commands are
  modelled — and, just as importantly, which are not; `internal/cli/guard_intercept_test.go`
  pins what the hook may and may not rewrite; `tests/terminal_e2e_test.go` drives `arac cmd`
  against a real scanned project including the byte-identical passthrough cases;
  per-scanner tests under `tests/` and each `*scanner/`; `internal/**/_test.go`.
- **`testing_ground/`** is a deliberately edge-case-dense corpus (one topology
  per language family) used to exercise live edit/scan; see
  `testing_ground/README.md` for the resource-ID formats and the parser corner
  cases it documents.

## 12. Working on this codebase (orientation tips)

- The CLI is the index: to find what a command does, start at the `case` in
  `main.go`, jump to `internal/cli/<name>.go`.
- To change *what a resource lookup returns*, look at `internal/llm/languages/*`
  (formatting/context) and `internal/topology/<lang>/*` (graph building).
- To change *what gets stored or how incremental scans behave*, look at
  `internal/topology/manager.go` + `internal/helper/{db,incremental,partial,manifest}.go`.
- To change *which tools an agent gets*, edit `.aracne/config.json` (validated
  against `internal/toolspec`); registration lives in
  `internal/cli/tool_profiles.go`.
- The MCP server, the internal agent, and the viz chat all reuse the same tool
  layer — keep behavior at parity across them (per the integration charter).

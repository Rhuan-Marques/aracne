# Aracne Project Integration

This project uses **aracne** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

## Navigation Model

The topology is a directed graph can enhance your information about the repository you're using if you use it correctly.

**Navigation Flow:**
1. Use `ls` to understand the project file layout
2. Use lookup MCP tools to get a resource's full context with interconnected relationships

**Note: Never try to use `read` native tool, use MCP lookups instead**

## MCP Lookup tools:
- `mcp__aracne__read_function`: Reads the function and context for resources it uses, receives a function ID.
- `mcp__aracne__read_struct`: Reads the struct and context for resources it uses, receives a struct ID.
- `mcp__aracne__read_interface`: Reads the interface and context for which resources it is implemented by, receives an interface ID.
- `mcp__aracne__read_file`: Reads the content of a file, receives the file path.

Note: Do *not* use "cat", "Get-Content" or any other OS command to read files## Grep/Search

Use the MCP tool `mcp__aracne__grep` for content search. It returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.

Do *not* use your native `grep` tool.
Do not use `grep`, `Select-String` or `rg` in the terminal## Resource Context

When you call a MCP Lookup Tool, the output has two sections:

**Code Block:** The resource's full source code, plus relevant imports and enclosing type (for methods).

**`# CONTEXT:` Section:** A structured hierarchical listing of everything the resource touches. Each entry is keyed by the resource's full ID, which you can pass directly to a lookup tool to drill deeper:

```
# CONTEXT:
## pkg.InterfaceName: Description
    pkg.ImplStruct: Description
        pkg.(ImplStruct).Method: Description
## pkg.OtherStruct: Description
    pkg.(OtherStruct).Method: Description
## pkg.CalledFunction: Description
## pkg.ExtVarName = value
```

Use the CONTEXT section to understand relationships **without making additional tool calls**.

## Edit and Write:

You can edit files using the MCP tool `mcp__aracne__edit`.
You can write files using the MCP tool `mcp__aracne__write`.
After editing or writing, the context for the topology will be automatically updated to reflect your actions.

**Note: NEVER try to edit or write using your native tools**

## Other:

- If you find a bug that is not relevant to your task, *do not fix it*. Instead, report it using `mcp__aracne__bug_report`
- If you want to check for any topology warnings, you can do it using `mcp__aracne__warnings_list`

## Tool Guard

An `arac guard` hook watches your tool calls. Whenever you use a native tool (`Read`/`Grep`/`Edit`/`Write`) or a shell equivalent (`cat`/`head`/`tail`/`less`/`grep`/`rg`/`sed`/`awk`, or PowerShell `Get-Content`/`Select-String`), you are reminded to use the matching aracne MCP tool instead.

Any tool listed in `blocked_tools` for the `claude_code` harness is blocked outright. Blocking `grep` also blocks `grep`/`rg`/`Select-String` run through the Bash tool; blocking `bash` blocks the Bash tool entirely. A read/grep command that consumes piped output (e.g. `git log | tail`, `cmd | grep x`) is exempt — only direct file reads like `cat foo.go` are gated; set `read.pipe_passthrough` to `false` to gate piped reads too. The guard uses the main agent's `blocked_tools`; per-sub-agent blocking is enforced by each sub-agent's tool allow-list, not by this hook.

## How to Navigate:

### 1: Explore Topology, NOT Files
Use the topology manager to your advantage, only read entire files when:
    - They are NOT supported language files (.go and .py)
    - Your tasks requires you to know all the information from the entire file
    - You don't know the other resources IDs yet

### 2: Let Descriptions Guide You
- A resource's description can tell you whether it is relevant to your task
- If the resource's description makes it look irrelevant to your task, skip it
- If you only need to understand what a resource does / is, descriptions can be *enough*. You don't need to read resources when the descriptions already gave you the necessary context

### 3: Go Deeper with Intent
- When exploring, ask yourself *what* you need to discover and understand fully.
- Which resources do you need to **know the code** of.
- These questions should guide you to navigate deeper in the topology to do your task to its best

### 4: Beware of TopologyWarnings
- When **editing**, you'll usually receive helpful warnings on resources that might have been affected by your changes. Keep those in mind and solve them as they come up

## Behavioral Rules

1. **Be concise** — Prefer short answers. Show what you found and what you changed, not how you did it.
2. **Do not parse code yourself** — Always use topology tools. The database is the source of truth.
3. **Do not guess** — If a tool returns no results or an error, report it accurately. Do not fabricate code or relationships.
4. **One level deep** — Read the CONTEXT section and only drill deeper when essential. Descriptions are designed to answer most questions at the surface level.
5. **Topology is always current** — After any `edit`, the topology updates automatically. You never need to request a re-scan.
6. **Avoid circular exploration** — If you already read a resource, do not re-read it in the same session. Trust your context.

Good Luck in your task.

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
                  edit, write, descriptions, bug, analyze, guard, update-file, …) + tool-registry wiring
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
  topogrep/       Topology-annotated grep (path:line:match + ResourceID + Description)
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

- **`scan`** / **`scanner`** — default mode (`default`/`hard`/`all`) for the
  one-shot `arac scan` and the live scanner; `update_frequency`.
- **`read`** — `max_file_size`; **`scan`** (`none`/`default`/`full`/`hard` — run a
  topology scan *before* every read/grep); **`context_filter`** (how verbosely
  the `# CONTEXT:` block renders neighbors, incoming "USED BY" edges, small-fn
  threshold, hide-undocumented); **`pipe_passthrough`** (whether the guard
  exempts piped reads like `cmd | tail`).
- **`descriptions`** — which `kinds` to document + `style_exemplars` count.
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

- **Reads** — generic `read` + per-kind `read_function`, `read_struct`,
  `read_interface`, `read_named_type`, `read_file`, `read_package`,
  `read_dependency`. `grep` is topology-annotated.
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

There are **four** ways to use aracne, all over the same topology engine:

### A. CLI (`arac <subcommand>`) — `internal/cli`, dispatched from `main.go`
Direct, no LLM. Key commands (full list in `usage.go` / `PrintUsage`):
`scan` (`--all`/`--hard`/`--default`/`--debug`), `read`/`grep`,
`resource list`, `node count`, `warnings list`, `bug <report|list|acknowledge|
dismiss|delete>`, `descriptions <generate|apply|clear>`, `update-file`,
`update-description`, `edit`/`write` (stdin JSON), `analyze dead-code`,
`check-updates`, `init`, `disable`, `guard`, `serve`, `viz serve`, `agent`.

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

- **`arac-guard.sh`** → `arac guard --claude-hook`: the **Tool Guard**. Watches
  tool calls; reminds the model to use the aracne MCP tool whenever it reaches
  for a native `Read`/`Grep`/`Edit`/`Write` or a shell equivalent
  (`cat`/`grep`/`sed`/PowerShell …). Tools listed in a harness's `blocked_tools`
  are blocked outright (blocking `grep` also blocks `rg`/`Select-String` run via
  Bash; blocking `bash` blocks the Bash tool entirely). Piped reads
  (`cmd | grep`) are exempt unless `read.pipe_passthrough:false`.
- **`arac-update-file.sh`** → `arac update-file --claude-hook`: re-parses a file
  into the topology after a **native** edit, keeping the graph current even when
  the change bypassed the MCP `edit`/`write` tools.
- OpenCode additionally gets `arac-native-edit-sync.js` (a plugin doing the same
  topology sync on native edits).

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

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->
# Architecture: what Aracne is and how it works

> A reference for working *on aracne itself*: what the codebase contains, how the
> pieces fit, and the surfaces a user can drive. For using aracne on your own
> project, start at the [README](../README.md); for the config file see
> [configuration.md](configuration.md) and [modes.md](modes.md).

## 1. One-paragraph summary

**Aracne** is a static-analysis engine that scans a multi-language source tree
(**Go, Python, JavaScript, TypeScript, Rust, Java**) and builds a **"topology"**: a directed
graph of every package/module, file, function, method, struct (a.k.a. class in
JS/TS/Python), interface, named type, variable, and dependency, plus the edges
between them
(calls, uses, implements/implemented-by, imports, etc.). The graph is persisted
to an **SQLite database** (`.aracne/topology.db`) and kept current as files are
edited. On top of that graph it exposes **topology-aware code navigation** to
LLM agents (so the model queries a pre-analyzed graph with rich descriptions
instead of grepping raw files) and to humans (via a CLI and a web visualizer).
The shipped binary is **`arac`** (module `github.com/Rhuan-Marques/aracne`, Go 1.25),
in two flavours: **Basic** (engine only) and **Full** (engine + the web visualizer).
See [§11](#11-building-testing-running).

## 2. Repository map

```
cmd/arac/main.go             Subcommand dispatcher → internal/cli.*
docs/                        This reference, modes.md, configuration.md
CLAUDE.md / AGENTS.md        This repo's own orientation docs, each carrying the injected
                             contract block `arac setup` writes into them — see §9
bench/                       Paired A/B benchmark harness (Python). Not in any build.
testing_ground/              Multi-language edge-case corpus the test suite scans.
.aracne/                     Per-project state: topology.db, config.json, file_manifest.json,
                             optimization_rules.json, providers.json, agents/*.md, chat/*.json
.claude/ .opencode/          Harness integrations: agents, slash commands, hooks, plugins, MCP config
internal/
  cli/            Every `arac <cmd>` entry point (scan, serve, viz, agent, init, setup, read,
                  grep, cmd, edit, write, descriptions, bug, guard, update-file, …) + tool-registry wiring
  tui/            The full-screen, arrow-key question flow `arac init` asks through: raw mode,
                  the alternate screen, key decoding, and one picker widget. See §9.
  shellcmd/       Pure argv→request parser for the terminal surface: decides whether a shell
                  read/search is one aracne models, and what window it asked for. Anything with
                  a flag it does not model is KindPassthrough — see §7B.
  topology/       The engine
    domain/         Pure model: Resource, Topology, ResourceKind, TopologyWarning, KnownBug, Location, Visibility, Cut
    contract/       Per-language signature rules: what a call site records and when a signature
                    change still fits it. Drives `signature_changed` — see §3.
    idresolve/      Turns whatever a caller thinks an ID is into the one the graph holds:
                    trailing-part match, wrong root prefix, wrong separator, ranked candidates
    scanner/        Registry + LanguageScanner/PartialUpdater interfaces
      goscanner/      Go parser (stdlib go/ast, go/parser, go/token) + the only UpdateFilePartial
      pyscanner/      Python parser (drives the `python3` on PATH — see §10)
      jsscanner/      JS + TS parser (tree-sitter via CGO — needs gcc)
      rustscanner/    Rust parser (tree-sitter)
      javascanner/    Java parser (tree-sitter)
    golang/ python/ javascript/ rust/ java/
                    Per-language managers + mapper/connections/resources/visibility (graph builders)
  helper/         Storage + plumbing: sqlite/db, manifest, incremental & partial writes, config, edit, normalize
  lazydesc/       The read-path description fill: plans the nodes a response will name,
                  generates the missing ones, waits for them to land, re-renders. See §8.
  progress/       One in-place progress bar, shared by `arac scan` and `descriptions generate`
  llm/
    provider.go     Provider interface (Chat / StreamChat) + message/tool types
    providers/      anthropic, openai, deepseek implementations
    agent/          Internal REPL agent loop + sub-agent runner
    tools/          Tool implementations (read, bug_*, warnings_list, update_description, …) +
                    Registry. grep/edit/write live here too but are no longer MCP tools: they
                    back the chat harness and the `arac edit`/`arac write` verbs — see §6.
    languages/      Per-language tool flavors: gotools / jstools / pythontools / rusttools /
                    javatools / universaltools, over two shared pieces —
      readunit/       the language-neutral shape one resolved resource takes into a response
      renderstate/    per-response bookkeeping so the same bytes are never rendered twice
  mcp/            JSON-RPC 2.0 MCP server over stdio (initialize / tools/list / tools/call)
  chat/           Chat engine behind the viz Chat tab (sessions, native tools, CreateTasks sub-agents)
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
  `node_removed`, `signature_changed`, `interface_conflict`. Tells an agent its
  change may have broken callers.

  The last two are **derived from current state, not remembered from an event**, which is
  what lets them retire themselves. `signature_changed` names a callee (`SourceID`) and the
  caller to verify (`TargetID`), and stands only while a RECORDED CALL SITE still fails to
  fit — callers store what they pass in a private `__call_sites` edge
  (`internal/topology/contract`), one matcher per language because arity is binding in Go,
  Rust, Java, TypeScript and Python but *not* in JavaScript, where `f(1)` against
  `function f(a, b)` is legal. Where a call cannot be read, `TopologyWarning.Baseline` — the
  signature the callers were written against — is the fallback, and it remains the only
  mechanism for a return-type-only change, which no call site records.
  `interface_conflict` is the implementer's side: a type that DECLARES an interface and
  does not deliver it. It runs only where that declaration is a claim that can be wrong
  (Rust, Java, TypeScript, Python ABC); Go and Python `Protocol` are structural, so their
  `implements` edge is derived from satisfaction and a broken type simply has no edge.
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
      persisted as a *scoped delta* without loading the whole graph. **Go only** —
      `goscanner/partial.go` holds the sole `UpdateFilePartial` in the tree, so the
      other five languages always take the two-phase path below.
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
overwritten with defaults). It has its own two pages, because between them they are
longer than the rest of this reference:

- **[modes.md](modes.md)** — `mode`, the one dial: which tools exist, which shell
  commands aracne answers, what the generated contract teaches. Read this one first.
- **[configuration.md](configuration.md)** — every other section: `terminal`, `scan`,
  `scanner`, `read`, `grep`, `descriptions`, `llm`, `viz`, `paths`, and the `features`
  block that gates the optional surfaces. `contract_verbosity` — how much the generated
  contract says — is documented there too.

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
  per-agent tool list. **It is the only capability `mcp` mode serves as a tool.**
- **Search and mutation — no MCP tool, in any mode.** `grep`, `edit` and `write`
  are *retired* MCP names (`toolspec.retiredMCPTools`): a search is intercepted
  wherever the model types it and answered by the topology-annotated grep, and a
  mutation goes through the harness's native edit — which the `arac update-file`
  hook re-syncs the topology after — or through `arac edit` / `arac write`. A tool
  for any of them asked one question twice and charged a schema block per request
  to let the model pick. The names still *validate* in an existing config's
  `mcp_tools`, and are dropped rather than served.
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
`mode: cli`; the MCP server (C) is opt-in via `mode: "mcp"`, which `arac init` asks about:

### A. CLI (`arac <subcommand>`) — `internal/cli`, dispatched from `cmd/arac/main.go`
Direct, no LLM. `usage.go` / `PrintUsage` is the full list and the authority; it gates the
`bug`, `agent` and `viz serve` blocks on the feature flags and the build tag, so what a given
binary prints is what that binary can do. Grouped:

| | |
|---|---|
| Scan | `scan` (`--all`/`--hard`/`--default`/`--debug`/`--workers`/`--progress`), `scanner run` |
| Read & search | `read`, `grep`, `cmd -- <command…>`, `resource list`, `node count` |
| Descriptions | `descriptions <generate\|clear\|export\|import>`, `update-description` |
| Mutate | `edit`, `write` (stdin JSON), `update-file` |
| Health | `warnings list`, `check-updates`, `bug <report\|list\|acknowledge\|dismiss\|delete>` |
| Integration | `init`, `setup`, `disable`, `guard`, `serve`, `viz serve`, `agent` |

### B. Terminal surface (the default) — `internal/cli/cmd.go` + `internal/shellcmd`
The shell IS the tool surface. `arac cmd -- <command…>` runs a shell read or search and answers
it from the topology when it can: the exact lines the command asked for, framed by the
signature of whatever declaration they sit inside (with an elision marker for what was left
out), and a `# CONTEXT:` block restricted to the resources those lines actually mention. A
resource ID stands wherever a path does, so `head -20 app.Flask` is the first twenty lines of
the class body.

Anything else runs for real. `internal/shellcmd` returns `KindPassthrough` for every flag it
does not model (`head -c`, `tail -f`, `grep -o`, `sed` substitutions) and for the readers that
transform rather than window (`nl`, `tac`, `xxd`, `od`, `hexdump`, `strings`); `arac cmd` also
passes through for an unindexed file, an over-budget answer, a missing database, or a command
whose **binary is not installed** — serving `rg` on a box without ripgrep taught the model a
capability that vanished on the next unmodelled flag — and execs the real binary with its exit
status. That fidelity is what makes interception safe on by
default: everything aracne does not model runs exactly as it would have, and
`tests/terminal_e2e_test.go` asserts byte-identical output for those cases.

The agent never types `arac cmd` itself. The `arac guard` PreToolUse hook rewrites its Bash
call via `hookSpecificOutput.updatedInput` (`internal/cli/guard_intercept.go`), so the model
writes `head -40 file.go` and reads real stdout. See §9.

### C. MCP server (`arac serve`) — `internal/mcp`
JSON-RPC 2.0 over **stdio** (`initialize`, `tools/list`, `tools/call`). This is
how **Claude Code** and **OpenCode** consume aracne (configured in `.mcp.json` /
`.opencode/opencode.json`). Flags: `--tool-profile` (`main`, `all`, or a configured agent
name) and `--harness` (`claude_code`|`opencode`) — together they select which
config-resolved tool set is exposed. No API key needed; the host platform brings
its own model.

### D. Web visualizer (`arac viz serve`) — `internal/viz`
A local HTTP server (default `127.0.0.1:7331`) serving a **`go:embed`'d static
SPA** (`internal/viz/static/`: `index.html`, `app.js`, `styles.css`) with the
**Visualization** (graph) screen and a Settings page.
HTTP API: `/api/graph`, `/api/neighborhood`, `/api/context-graph`,
`/api/search`, `/api/node/…`, `/api/summary`, `/api/warnings`, `/api/bugs`,
`/api/config`, `/api/optimization-rules`, `/api/chat[/…]`, and `/api/ws`
(websocket for streaming). Graph "modes": *Packages & Modules*, *Data Flow*,
*Custom*; with language filtering, search, and neighborhood-depth controls.

`internal/chat` — a second agent harness powering a viz **Chat** tab — is **off by default**
behind `features.chat`, and with it off the tab and its three routes (`/api/chat`,
`/api/chat/`, `/api/context-graph`) are not served. `internal/viz` is its only importer, and
`cli/viz.go` is the only importer of `internal/viz`, so `-tags minimal` drops the whole
subtree.

### E. Internal agent (`arac agent`) — `internal/llm/agent` + `providers`
A self-contained REPL against an LLM provider, **off by default** behind `features.agent`. It
is the one surface that needs a provider API key of its own.

The flag gates the **command**, not the package. `internal/llm/agent` is not optional:
`descriptions generate` runs its executor fan-out through `agent.New` + `RunSubAgent`, so only
the REPL entry point (`cli/agent.go`) is exclusive to the feature. Its system prompt is not its
own either — `agent.BuildPrompt` returns `prompts.ContractContent`, the same document
`arac setup` writes into `CLAUDE.md` and `AGENTS.md`, at whatever
[`contract_verbosity`](configuration.md#contract_verbosity) the project set.

## 8. Agent workflows

These are defined as harness **slash commands** + **agent definitions** (markdown
under `.claude/`, `.opencode/`, `.aracne/agents/`) and are mirrored by the
in-repo skills:

- **Descriptions** — `descriptions generate` lists undocumented resources,
  batches them, and fans out **descriptions-generation-executor** sub-agents that
  read each resource and write a concise description; `descriptions clear` removes them.
  Descriptions live in the topology and are rendered from it; there is deliberately no
  command that writes them back into source. `descriptions apply` did, and it decided a
  file's comment syntax from the resource's `Language` tag rather than from the file's
  extension — which put Go `//` comments into a Python file in this very repository and
  left it unparseable. `descriptions export`/`import` carry descriptions across a rescan
  instead, without touching a byte of source.
- **Bug pipeline** — **off by default**, behind `features.bug_management`: a
  hunter/judge/solver fan-out over `KnownBug` nodes. With it off, `arac setup` writes none
  of its agents or commands, the `bug_*` tools are not servable, and the `arac bug` usage
  block does not print. `arac bug` itself stays dispatchable either way — it is the channel
  the generated slash commands orchestrate through, and the debugging path.

## 9. Harness integration & guards (`arac init` / `arac setup`)

Two commands, split along one line: **`arac init`** asks (`internal/cli/init.go`,
`init_questions.go`, drawn by `internal/tui`) and **`arac setup`** writes
(`internal/cli/setup.go`). The wizard collects the six answers, saves them to
`.aracne/config.json`, scans, and then calls `runSetup` itself; every later re-render is
`arac setup` alone. `arac setup` READS `mode` and never writes it — the command that
re-renders an integration must not be able to change which integration a project has.

`arac setup` (flags `--claude`, `--opencode`, `--global`, `-y`) wires aracne into
a project: generates the injected **CLAUDE.md / AGENTS.md**, the MCP config
(`.mcp.json` / `.opencode/opencode.json`), agent + command markdown, and harness hooks/
plugins. Two hooks ship for Claude Code:

- **`arac-guard.sh`** → `arac guard --claude-hook`: the **Tool Guard**. What it does depends
  on `mode`:
  - **searches, every mode** — it **rewrites**. A single, unpiped, unredirected Bash `grep` that
    `shellcmd` models comes back as `<arac> cmd -- <the original text>` through `updatedInput`.
    The original text is reused verbatim so the shell re-splits it exactly as it would have.
    Guard rails: never a second time (`isAracCommand` stops the recursion), never across a pipe,
    a redirect, a heredoc, an `&&`, an env prefix or a wrapper, never a mutation, and never a
    file with no topology nodes.
  - **`intercept_id` / `intercept_line_ranges`** — the same rewrite additionally covers shell READS
    (`cat`/`head`/`tail`/`sed -n`/`awk`) on a target the topology knows.
  - **`mcp` / `cli`** — reads are left alone. The read capability already has a surface
    in both (a tool, or `arac read`), and rewriting the model's `cat` on top of it would answer
    one question twice.
  - **`blocked_tools`** applies in **`mcp` and `cli`** (`Config.GuardBlocksNativeReads`):
    the two modes where a refusal has somewhere to send the model — an MCP tool in its list, or
    `arac read`, which is a real command in `cli` and where reads are not intercepted.
    The two intercepting modes block nothing: there the capability arrives AS the command the
    model typed, so a block would refuse a call aracne was about to answer — the
    two-turns-for-one-question failure interception was built to end. It stays **off by default
    everywhere** (`blocked_tools` is empty in a generated config), so this is an opt-in knob
    rather than a default. `Config.BlockableInMode` additionally drops `grep` from the set
    outside `mcp`, because search is intercepted in every mode and refusing it would deny a
    command the guard was one step from answering. Where a block does apply:
    blocking `grep` also blocks `rg`/`Select-String` run via Bash; blocking `bash` blocks the
    Bash tool entirely; a denied read is answered with its content where it can be
    (`guard_proxy.go`); piped reads (`cmd | grep`) are exempt unless
    `read.pipe_passthrough:false`. Interception is tried first, so a command aracne can serve is
    answered rather than refused however `blocked_tools` reads.
  - The **PostToolUse nudge** fires on native `Read`/`Grep`/`Edit`/`Write`, which interception
    never sees, and names the surface the mode actually has (`toolspec.WarningForSurface`).
    A **shell** read earns no nudge in any mode: benchmarking found the pointer fired hundreds
    of times against servable shell reads without moving the model onto `arac read` once, so it
    is pure per-call cost. The MCP fallback below it is keyed on the *mode* rather than on
    `!InterceptReads()`, which is also true in `cli`; keying it on the predicate would print
    the MCP pointer after a read `cli` was never going to refuse. Note that
    `grep`/`edit`/`write` guidance is the `arac` subcommand in *every* mode including `mcp`,
    because no mode registers a tool for them.

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
  agent lists the `edit-update-db-plugin` plugin — an optimization, not the
  warning channel: it syncs *inline* with the edit rather than on the next call.
  The warnings themselves come from the guard either way (below), and both hooks
  report through the same `guard-reported-warnings.json` ledger, so whichever
  runs first reports and the other adds nothing.
- OpenCode additionally gets `arac-native-edit-sync.js` (a plugin doing the same
  topology sync on native edits), under the same plugin flag.

Both hook commands **quote** the script path and, under `--global`, name the absolute path
`arac setup` actually wrote rather than `${CLAUDE_PROJECT_DIR}` — which expands to the
*project* root, not the user's home. Unquoted, a project under `~/My Projects` word-split and
every tool call failed with exit 127; under `--global`, the entry named a file that was never
created. See `hookScriptRef` / `bashHookCommand` in `internal/cli/native_hooks.go`.

Freshness itself is not a plugin. **Before** every tool call the guard sees, it runs the
`scan.pre_tool` scan (default: incremental) so the call is answered from a graph that
matches the code on disk — including changes nothing in the session made, like a
`git checkout`, a rebase or an editor save. Claude Code gets this from the PreToolUse
guard hook itself; OpenCode gets `arac-pre-tool-scan.js`, a plugin installed
unconditionally by `arac setup` whose `tool.execute.before` runs `arac guard --pre-scan`.
Both read the same config key at call time, so `scan.pre_tool: "none"` disables it on
both surfaces without re-running init. The pre-call scan reports nothing (a PreToolUse
hook cannot address the model without blocking it); the warnings it finds are persisted
in the topology and surface through `warnings_list` / `arac warnings list`, and the
**post**-call drift check reports whatever is new in the warnings table.

That check fires after a Bash command the classifier read as a write (or could not classify
at all) **and after a native `Edit`/`Write`/`MultiEdit`/`NotebookEdit`**. The native half was
missing: nothing else reported those, since the `arac update-file` hook that was supposed to
is behind a plugin flag no shipped path sets — so a native edit produced no warning when it
broke a caller, and the one the next pre-tool scan found was delivered by whichever later
shell command happened to be unclassified, and blamed on it. The header says
"Topology re-synced." and nothing about a cause, because the reporter reports what is new in
the table however it got there.

## 10. Languages & known quirks

- **Go** — stdlib `go/ast` parser; richest support (cross-package return-type
  inference, interface↔struct matching, generics, embedding).
- **Python** — custom parser that **shells out to `python3`** (then `python`), running an
  embedded script via `exec.CommandContext` and reading back a JSON AST
  (`pyscanner/parser.go`, `parse_script.go`). It is the only scanner with a runtime
  dependency outside the binary: with no interpreter on `PATH` there is no Python
  topology. ABC/Protocol/dataclass, inheritance, decorators,
  cross-module resolution; *modules-first* (file IDs, `imports_module`).
- **JavaScript + TypeScript** — share one **tree-sitter (CGO)** scanner, so the
  build **requires gcc**. JS and TS are **independent topologies** (no
  cross-language import resolution). TS adds interfaces/type-aliases/enums and
  annotation-driven method resolution. Also *modules-first*.
- **Rust** — **tree-sitter (CGO)**; module-path IDs, and a cross-file `impl`
  mechanism so methods defined away from their type still attach to it.
- **Java** — **tree-sitter (CGO)**; FQN + signature IDs, struct↔struct
  `inherits`, and the awkward declaration forms (enum constants with bodies,
  records, anonymous and local classes, initializer blocks).
- Every language is exercised by `tests/atscale_<lang>_test.go` across all three
  scan modes, against the `testing_ground/` corpus.
- Scanners skip test/build artifacts: Go `*_test.go`, Python `test_*.py`, JS/TS
  `*.test.*`/`*.spec.*`/`*.min.*`, and dot/vendor dirs.

## 11. Building, testing, running

The commands themselves live in [CONTRIBUTING.md](../CONTRIBUTING.md); what follows is why
they are shaped the way they are.

- **Two builds.** The default is **Full**; `make build-basic` (`-tags minimal`) produces
  **Basic** — the same engine and every language, with no web visualizer and no embedded SPA.

  | Build | Command | Front-end |
  |---|---|---|
  | Full *(default)* | `make build` | `arac viz serve` + embedded SPA |
  | Basic | `make build-basic` | none; `arac viz serve` exits with a pointer |

  The seam is one file: `internal/cli/viz.go` is the only importer of
  `internal/viz` (which is in turn the only importer of `internal/chat`), so
  tagging it out drops the whole subtree — `go:embed` payload included — from
  the binary. `go build ./...` and `go test ./...` still cover both, which is why
  `make test-minimal` exists: a default `go test ./...` does not exercise the tag.
- **Front-end**: the SPA is plain hand-written HTML/JS/CSS embedded via
  `go:embed` — no bundler, no npm install. Rebuild the Go binary to pick up
  static changes.
- **Tests**: `go test ./...`. Notable: `tests/atscale_*` run a 3-scan-mode
  (full / incremental / hard) **topology-consistency** suite across languages;
  `internal/shellcmd/shellcmd_test.go` is the executable spec of which shell commands are
  modelled — and, just as importantly, which are not; `internal/cli/guard_intercept_test.go`
  pins what the hook may and may not rewrite; `tests/terminal_e2e_test.go` drives `arac cmd`
  against a real scanned project including the byte-identical passthrough cases;
  `internal/cli/generator_drift_test.go` and `tests/setup_test.go` pin the generated
  markdown; per-scanner tests under `tests/` and each `*scanner/`; `internal/**/_test.go`.
- **`testing_ground/`** is a deliberately edge-case-dense corpus (one topology
  per language family) used to exercise live edit/scan; see
  `testing_ground/README.md` for the resource-ID formats and the parser corner
  cases it documents.

## 12. Working on this codebase (orientation tips)

- The CLI is the index: to find what a command does, start at the `case` in
  `cmd/arac/main.go`, jump to `internal/cli/<name>.go`.
- To change *what a resource lookup returns*, look at `internal/llm/languages/*`
  (formatting/context) and `internal/topology/<lang>/*` (graph building). If the shape of the
  answer changes, `internal/prompts/languages.go` describes it to the model and has to change
  with it.
- To change *what a model is told about this project*, there is one place:
  `internal/prompts/contract.go` (`low`) and `contract_high.go` (`high`), with the per-language
  halves in `languages.go`. `CLAUDE.md`, `AGENTS.md` and `arac agent`'s system prompt all
  render from it, and `internal/prompts/contract_test.go` asserts they are byte-identical.
- To change *what gets stored or how incremental scans behave*, look at
  `internal/topology/manager.go` + `internal/helper/{db,incremental,partial,manifest}.go`.
- To change *which tools an agent gets*, edit `.aracne/config.json` (validated
  against `internal/toolspec`); registration lives in
  `internal/cli/tool_profiles.go`.
- The MCP server, the internal agent, and the viz chat all reuse the same tool
  layer — keep behavior at parity across them (per the integration charter).

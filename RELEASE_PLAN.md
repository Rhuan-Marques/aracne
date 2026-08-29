# Aracne — Public Release Plan (v1 / v2)

> Internal planning document. Defines what ships in the **first public
> open-source release (v1)**, what is deferred to **v2**, and lists every
> remaining feature whose target version still needs a decision (§4).
> This is the source doc from which a public-facing `ROADMAP.md` and the v1
> `README.md` will later be distilled.

---

## 0. Strategy at a glance

- **Model:** open-source public repository, released in staged versions.
- **v1 — "Topology + Integrations + Graph".** The static-analysis engine, the
  Claude Code + OpenCode integrations (MCP server + init + guard/sync hooks),
  and the **graph** screen of the web visualizer. **No Chat tab. No bug
  pipeline. No self-hosted LLM agent.** v1 needs no API keys: the host harness
  (Claude Code / OpenCode) brings its own model.
- **v2 — "Agentic layer".** The viz **Chat** tab and its backend, and the
  **bug-finding pipeline** (hunter → judge → solver). These add a
  self-contained agent harness and therefore introduce provider/API-key
  configuration.
- **Versioning:** SemVer. v1 is `1.0.0`. Feature-flagged work that isn't ready
  can ship dark (present in the tree, not wired into the default binary/UI —
  see §5).

### Current-state snapshot (2026-08-17)

| Signal | State |
|---|---|
| Build (`go build`, CGO/gcc) | ✅ passes |
| `go test ./...` | ❌ 3 packages fail — `tests/` atscale incremental-scan mismatches (Python + TS + xlang), `internal/cli` config + edgecase suites |
| LICENSE | ❌ absent |
| Top-level README | ❌ absent |
| CI (`.github/workflows`) | ❌ absent |
| Release/distribution (versioning, prebuilt binaries) | ❌ absent |
| Tracked secrets | ✅ none (`.aracne/` gitignored) |
| Working tree | ⚠️ mid-refactor: uncommitted parallel scanner + progress bar, benchmark tooling; `AGENTS.md`/`.mcp.json` emptied |

---

## 1. v1 — "Topology + Integrations + Graph Viz"

### 1.1 One-paragraph scope

v1 is the aracne **engine** plus the two **agent-harness integrations** and a
**read-only graph explorer**. A user installs `arac`, runs `arac init --claude`
and/or `--opencode`, and their coding agent gains topology-aware navigation
(rich `read_*`/`grep` tools, topology-synced `edit`/`write`, post-edit
warnings). Separately they can run `arac viz serve` to browse the graph in a
local browser. Nothing in v1 calls an LLM on aracne's own account.

### 1.2 What ships in v1 (feature by feature)

**A. Core engine (always on)**
- Topology model + SQLite persistence (`.aracne/topology.db`).
- Full / full-rescan (descriptions preserved) / hard-rebuild / incremental
  scan, including the partial fast-path and the two-phase reverse-caller
  re-resolve. **Incremental correctness is a v1 blocker (see §1.4).**
- Per-file edit serialization (`WithFileLock`).
- Config system (`.aracne/config.json`) validated against `toolspec`.
- Path visibility rules (hide/show subtrees).

**B. CLI surface (v1 subcommands)**
- `arac scan` (`--all` / `--hard` / `--default` / `--debug` / `--verbose` /
  `--workers` / `--progress`)
- `arac serve` — MCP server (`--tool-profile`, `--harness`)
- `arac viz serve` — graph UI (`--addr`, `--db`) — **Chat tab hidden**
- `arac init` (`--claude` / `--opencode` / `--global` / `-y`)
- `arac disable` — remove integration
- `arac read`, `arac grep`, `arac resource list`, `arac node count`,
  `arac warnings list`, `arac update-description`, `arac update-file`,
  `arac edit`, `arac write`, `arac check-updates`

**C. MCP server tools (v1 default profiles)**
- Reads: a single `read` taking a list of resource IDs (registers as
  `read_resource` when the harness keeps its own native read); which kinds it
  resolves is `read.kinds`
- Search: topology-annotated `grep`
- Mutations (topology-synced): `edit`, `write`
- Maintenance: `warnings_list`, `update_description`,
  `node_list_no_description`
- **Excluded from v1 default profiles:** all `bug_*` tools (→ v2)

**D. Harness integration & guards**
- Generated `CLAUDE.md` / `AGENTS.md`, `.mcp.json` / `opencode.json`,
  agent + slash-command markdown.
- Claude Code hooks: `arac-guard.sh` (Tool Guard — nudge/​block native
  Read/Grep/Edit/Write and shell equivalents) and `arac-update-file.sh`
  (topology sync after native edits).
- OpenCode: `arac-native-edit-sync.js` plugin.

**E. Web visualizer — graph only**
- `go:embed`'d SPA, default bind `127.0.0.1:7331`.
- Screens: **Visualization** (graph) + **Settings**. **Chat tab removed/hidden.**
- Graph modes: *Packages & Modules*, *Data Flow*, *Custom*; language filtering,
  search, neighborhood-depth control, node inspection.
- HTTP API needed by graph: `/api/graph`, `/api/neighborhood`,
  `/api/context-graph`, `/api/search`, `/api/node/…`, `/api/summary`,
  `/api/warnings`, `/api/config`, `/api/optimization-rules`.
- **Not served in v1:** `/api/chat[...]`, `/api/ws` (chat streaming),
  `/api/bugs`.

**F. Post-edit warnings** — `use_missing_node`, `node_removed`,
`signature_changed` surfaced to the agent and in `warnings list`.

### 1.3 Explicitly excluded from v1 (deferred)

- Viz **Chat** tab + `internal/chat` backend (→ v2).
- **Bug pipeline** and `bug_*` tools/agents (→ v2).
- **Self-hosted internal agent** (`arac agent`, DeepSeek/providers) — see §4.
- Provider/API-key configuration (`llm` provider creds, `viz.chat`) — v1 keeps
  these out of the documented surface.

### 1.4 v1 acceptance criteria (release blockers)

1. **`go test ./...` green.** Land or revert the uncommitted scanner/progress
   WIP, then fix the incremental-scan correctness failures (`tests/atscale_*`
   Python + TS: e.g. `merge_iface.Combo` losing its `methods` under incremental
   scan; Python resources missing after incremental) and the `internal/cli`
   config/edgecase failures. A wrong graph after an edit is the worst-case
   failure for this product — this gates everything.
2. Confirmed **language scope** (see §4.1) with honest "supported vs
   experimental" labels in docs.
3. All §5 foundation items (LICENSE, README, CI, distribution, versioning,
   hygiene) complete.
4. Chat tab and bug tools verifiably absent from the default binary/UI/MCP
   profiles (§5 gating strategy).
5. Clean `arac init` → real editor session smoke test on Linux + macOS (+ a
   documented Windows story, §4).

---

## 2. v2 — "Agentic layer: Chat + Bug pipeline"

### 2.1 Scope

v2 turns on aracne's **own** agent harness. It adds the interactive Chat tab to
the visualizer and the automated bug-finding workflow. Both depend on a
configured LLM provider, so v2 is the version that introduces API-key setup and
the associated security surface.

### 2.2 What ships in v2

**A. Viz Chat**
- `internal/chat` backend: session store (`.aracne/chat/*.json`), agent
  registry, native tools (`ls` / `bash` / `glob`), workspace-scoped permission
  policy, `CreateTasks` parallel sub-agents.
- Chat tab in the SPA; `/api/chat[...]` + `/api/ws` endpoints re-enabled.
- `viz.chat` config section (its own `main_agent` + sub-agent tool lists).

**B. Bug pipeline**
- Agents: `bug-hunter` → `bug-judge` → `bug-solver` (+ `explorer`).
- MCP + chat tools: `bug_report`, `bug_list`, `bug_acknowledge`,
  `bug_dismiss`, `bug_delete`; `KnownBug` state machine
  (pending → acknowledged/dismissed).
- CLI: `arac bug <report|list|acknowledge|dismiss|delete>`.
- `/api/bugs` endpoint.

**C. LLM providers**
- `internal/llm/providers` (anthropic, openai, deepseek) + the `llm` provider
  config, exposed and documented for the chat/agent surfaces.

### 2.3 New v2 prerequisites

- Documented provider/API-key configuration and precedence.
- **Security review of the agent surface**: the chat `bash` native tool, the
  permission policy, and `CreateTasks` fan-out (arbitrary command execution
  from an LLM must be sandboxed/consented).
- Chat session storage format stability + migration notes.
- Cost/telemetry expectations documented (users pay their own provider bills).

---

## 3. Beyond v2 (parking lot)

Candidate future work, not yet scheduled — recorded so it isn't lost:
- Additional language scanners; deeper cross-language import resolution
  (currently JS↔TS are independent topologies).
- Editor extensions (VS Code / JetBrains) driving the topology directly.
- Remote/hosted viz with auth (v1 is localhost-only by design).
- Incremental-scan performance + memory tuning at very large repos.

---

## 4. Undecided — features that still need a version call

Each row is a real decision point. **Proposed** is my recommendation;
the **Open question** is what I need you to confirm.

### 4.1 Language support scope (highest impact)

Scanners exist for **Go, Python, JavaScript, TypeScript, Java, Rust**. Their
maturity differs, and the incremental suite currently fails for Python and TS.

- **Proposed:** v1 ships **Go** as fully-supported plus **Python / JS / TS**
  *after* the incremental failures are fixed; **Java** and **Rust** ship as
  **experimental** (documented as such) or hold for a later minor.
- **Open question:** Which languages carry the "supported" guarantee in v1, and
  which are labeled experimental (or excluded)?

### 4.2 Internal agent (`arac agent`, DeepSeek/providers)

A self-contained REPL agent that talks directly to an LLM provider. v1's premise
is "the harness brings the model," so this isn't needed for v1.

- **Proposed:** defer to **v2** (it naturally rides along with the provider
  config chat already needs), or drop it entirely if the chat tab supersedes it.
- **Open question:** v2, or cut it? If kept, is it a supported entry point or a
  power-user extra?

### 4.3 `arac descriptions generate` (LLM-authored descriptions)

Descriptions are a core value prop, but *generating* them needs an LLM. This can
run **through the host harness** (Claude Code / OpenCode sub-agents — no aracne
API key) or **through the internal agent** (needs a key).

- **Proposed:** **v1**, but **harness-driven only** (via the
  `descriptions-generation-executor` slash command / sub-agent). The
  internal-agent path follows §4.2. `descriptions apply` / `clear` are pure
  mechanics and are v1 regardless.
- **Open question:** Ship harness-driven description generation in v1, or defer
  all generation to v2 and let v1 only *store/apply/clear* descriptions?

### 4.4 `arac analyze dead-code`

Pure static analysis over the topology, no LLM.

- **Proposed:** **v1** (independent, genuinely useful, zero new surface).
- **Open question:** in or out for v1?

### 4.5 `arac scanner` subcommand

Introspection/debug entry point (dispatched in `main.go`).

- **Proposed:** keep as an **undocumented/debug** command in v1.
- **Open question:** document it, hide it, or remove it?

### 4.6 Guard hard-blocking vs warn-only

The guard can *warn* ("use the MCP tool") or *hard-block* tools listed in
`blocked_tools`.

- **Proposed:** v1 default = **warn-only**, hard-block opt-in via config.
- **Open question:** is warn-only the right default, or should certain native
  tools be blocked out of the box?

### 4.7 Benchmark harness (`bench/`) and `testing_ground/`

Dev/eval assets currently committed (`bench/` ~31 files, `testing_ground/` ~114
files).

- **Proposed:** `testing_ground/` **stays** (it's the test corpus).
  `bench/` **stays but is clearly marked dev-only** (not a shipped feature); move
  its ad-hoc docs (`prompt/`, `prompts/`) out of the repo root.
- **Open question:** keep `bench/` in the public repo, or split it into a
  separate internal repo?

### 4.8 Web viz binding / auth

Today the viz binds `127.0.0.1` with no auth.

- **Proposed:** v1 stays **localhost-only, no auth** (documented); remote/auth is
  post-v2 (§3).
- **Open question:** confirm localhost-only is acceptable for v1.

### 4.9 The "proprietary chat engine" wording

`internal/chat` is described in-code as the "proprietary chat engine." In an
open-source repo this phrasing is contradictory. Since chat is v2, this isn't a
v1 blocker, but it must be resolved before the chat code goes public.

- **Proposed:** open-source `internal/chat` under the same license as the rest
  and **strip the "proprietary" wording** when it ships in v2.
- **Open question:** open-source it with everything else in v2, or keep it a
  closed/dual-licensed carve-out (which would require a build-tag split)?

---

## 5. Staging strategy — keeping v2 out of the v1 binary

The chat backend and bug tools are already wired into the tree. Three ways to
ship them dark in v1:

1. **Config/profile gating (lightest).** Don't render the Chat tab in the SPA,
   don't register `/api/chat`·`/api/ws`·`/api/bugs`, and exclude `bug_*` from
   the default MCP/tool profiles. Code stays compiled in but unreachable.
2. **Build tags (`//go:build v2` / `chat`).** Exclude chat/bug code from the v1
   binary entirely — cleaner attack surface and smaller binary, more plumbing.
3. **Undocumented + unlisted.** Present but absent from `PrintUsage`, README,
   and generated harness configs.

**Recommendation:** combine **(1) + (3)** for v1 — gate the UI/API/tool-profiles
and leave the features undocumented. Reserve build tags (2) only if §4.9 lands
as a closed-source carve-out (which forces a compile-time split anyway).

---

## 6. Cross-cutting foundation (must land before the v1 tag)

One-time work that gates *any* public release:

- [ ] **LICENSE** file chosen and added (open-source; permissive vs copyleft is
      still an open call — flag if you want a recommendation).
- [ ] **README.md** at repo root: what/why, the surfaces, install, `arac init`
      quickstart, MCP setup snippet, screenshots (assets exist in `assets/`).
- [ ] **`arac --version` / `version`** subcommand, build-version injected via
      ldflags.
- [ ] **CI** (`.github/workflows`): CGO build on linux/macOS/Windows, `go vet`,
      `gofmt` check, `go test ./...`.
- [ ] **Distribution**: GoReleaser (or Makefile) producing prebuilt per-OS/arch
      binaries — the realistic install path given CGO+tree-sitter. Document the
      gcc/C-toolchain requirement; define the Windows story.
- [ ] **Reproducible builds**: stop gitignoring `go.sum`; commit it.
- [ ] **Repo hygiene**: remove stray artifacts (`scanout.txt`, `prompt/`,
      `prompts/`); populate or drop the emptied `AGENTS.md` / `.mcp.json`
      (they're regenerated by `arac init`).
- [ ] **OSS meta**: `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`, and (if
      accepting outside contributors) `CODE_OF_CONDUCT.md`.
- [ ] **Toolchain floor**: reconsider the `go 1.25.0` requirement if not
      strictly needed (it narrows who can build/distribute).
- [ ] **Config/setup docs**: document `.aracne/config.json` for the v1 surface.

---

## 7. Open decisions summary (for quick sign-off)

| # | Decision | Proposed |
|---|---|---|
| 4.1 | v1 language scope | Go supported; Py/JS/TS after fixes; Java/Rust experimental |
| 4.2 | Internal `arac agent` | Defer to v2 (or cut) |
| 4.3 | Description generation | v1, harness-driven only |
| 4.4 | `analyze dead-code` | v1 |
| 4.5 | `scanner` subcommand | v1, undocumented/debug |
| 4.6 | Guard blocking default | Warn-only, hard-block opt-in |
| 4.7 | `bench/` in public repo | Keep, marked dev-only |
| 4.8 | Viz binding | Localhost-only, no auth, documented |
| 4.9 | Chat engine licensing | Open-source with rest in v2, drop "proprietary" |
| — | License type | Open-source (type TBD) |

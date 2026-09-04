# Plan: `identification_mode` — address resources by line range, not by ID

> **Historical design note.** This plan shipped: the two keys it describes were folded
> into the four named modes (`mcp`, `aracne_read`, `intercept_id`, `line_range`). Kept for
> the measurements that motivated it. For current behaviour see `docs/modes.md`.

## Why

Measured over `ab-prefer-ids-20260902a` (27 repos × 3 arms, all graded):

- The model used a resource ID as a command operand **0 times in 408 shell commands**, even
  though intercepted greps printed `# <id> — description` above every hit. The contract's
  "prefer an ID over a path" sentence had **no measurable effect** — its A/B failed its own
  manipulation check.
- `arac grep` was invoked **0 times**; `arac read` 7 and 4 times across 27 cells each.
- The control, offered `Read`/`Edit`/`Write`/`Grep`, used them **0 times in 462 Bash calls**.
- Every one of those 408 commands addressed code as **file + line**: `sed -n '330,400p'`,
  `awk 'NR>=316 && NR<=332'`, `grep -n`.
- 35–42% of file reads were **re-reads of a file already read** — the model guessing line
  ranges it could have been told.

Cross-provider check: OpenAI's Codex CLI exposes **one shell tool** and its system prompt
teaches `cat`/`grep`/`find` for reading, reserving a dedicated tool only for mutation. So
bash-first addressing is not an Anthropic quirk — it is the industry direction, and a resource
ID is aracne-specific vocabulary while `file:line-range` is POSIX.

**Conclusion.** Stop asking the model to learn our addressing. Speak its own.

## The two modes

New top-level config key `identification_mode`:

| value | meaning |
|---|---|
| `id` | today's behaviour, plus a stronger push toward IDs (the "option 1" arm) |
| `line_range` | **default** — every surface that identified a resource by ID now identifies it by `path:start-end` |

`line_range` carries one promise, and the implementation must make it literally true:

> Reading those exact lines with bash returns exactly what an aracne resource read would have
> returned — same imports, same context block.

## Scope of change

### 1. Config — `internal/helper/config.go`
- `IdentificationMode string \`json:"identification_mode"\`` at top level.
- `EffectiveIdentificationMode()` → `line_range` unless explicitly `id`.
- `LineRangeIdentification() bool` helper used by every consumer.
- Accept in `Validate()`; normalize in `normalizeConfig`; add to `validConfig`'s sentinel list
  so a mode-only config is not judged legacy; stamp in `DefaultConfig()`.

### 2. Context blocks — ONE choke point, not 40
Entries are emitted from ~40 sites across `gotools`/`pythontools`/`jstools`/`rusttools`/
`javatools`. Editing each is error-prone and would drift. Instead post-process the rendered
section in `readunit.Render`, which is the single place every entry passes through.

- `readunit.Options` gains `Locate func(id string) string`, non-nil only in `line_range` mode.
- After `writeContext`/`writeIncoming`, rewrite each entry line that matches the grammar
  already pinned by `tests/context_dedup_test.go`:
  `^(?:##\s+|\t+)(\S+?)(?:\s+\([^)]*\))?[:=]`
- A token whose `Locate` returns `""` is left untouched. That is the safety net: headings
  (`## Implemented By`, `## Used By`, `## file <path>`) do not resolve, so they cannot be
  mangled.
- Output keeps BOTH, because in a context block the ID is what ties the entry to the symbol
  the reader just saw in the code:
  - `## pkg.Foo: desc` → `## pkg.Foo (src/x.go:120-160): desc`
  - `## pkg.Foo (function): desc` → `## pkg.Foo (function, src/x.go:120-160): desc`

### 3. The promise — `universaltools/slice.go`
When a requested window **fully contains** every declaration it touches, serve it through
`ReadIDs(those ids)` instead of the windowed renderer. That makes "read the advertised lines"
byte-identical to the resource read. A window that cuts a declaration keeps today's framed
slice.

### 4. Grep — `internal/topogrep`
`Match` gains `ResourceStart`/`ResourceEnd` (already known to `bestResource` via
`resourceLocation`). `FormatResult` gains a `LineRange bool` option:
- `id` mode: `# <id> — desc` (today)
- `line_range` mode: `# <path>:<start>-<end> — desc`

Wired from `internal/cli/grep.go`, `internal/llm/tools/grep.go` and `internal/cli/cmd.go`.

### 5. Elision markers
`universaltools.marker()` currently prints `⋯ +N lines of <lastIDSegment> ⋯`. In `line_range`
mode print the span instead: `⋯ +N lines — full declaration at src/x.go:120-160 ⋯`, which is
directly actionable.

### 6. Contract — `internal/prompts/terminal_md.go`
- `line_range`: drop the whole Resource IDs section (zero adoption, ~a third of the file) and
  replace it with a short statement that reads are enriched and that context entries carry the
  exact range to read next. Target ≤ 1,300 bytes vs today's 2,030 (792 tok/turn measured, so
  every 250 bytes saved is ~0.1 turns per task).
- `id`: keep the ID section and strengthen it (the "option 1" arm).

### 7. Benchmark
- `bench/configs/aracne/line-range.json` — terminal surface, `identification_mode: line_range`.
- `bench/configs/run/linerange-vs-baseline.yaml` — **two arms**, `baseline` and `aracne`, both
  `[Bash, Glob]`, control re-measured (no `baseline_from`: the old control ran with six tools
  and a different prefix, which is exactly the artifact that produced the bogus −45% and −21%).

## Risks

- **Regex post-processing of generated text.** Mitigated by resolve-or-skip: an unresolvable
  token is never rewritten, and the grammar is already pinned by a test.
- **Stale ranges after an edit.** The `update-file` hook re-syncs the topology after every edit,
  and a stale range self-corrects on the next read.
- **`id` mode must not regress.** Every existing test runs in `id` semantics unless the mode
  says otherwise; `Locate` is nil there, so the post-processor is a no-op.

## Verification

`go test ./...`, bench pytest, then a live check on a scanned fixture that a context entry
carries a range and that reading that range reproduces the resource read.

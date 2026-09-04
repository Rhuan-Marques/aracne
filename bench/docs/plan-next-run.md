# Plan: what the two blocked-read runs taught, and what to change

Evidence base: `compact-blocked-20260830c` (run A) and `compact-blocked-after-bs-20260830c`
(run B), 27 repos each, paired against the same imported `opus-medium-fixed` baseline.

## What we learned

1. **The guard silently stops working after the model `cd`s.** `loadGuardConfig`,
   `driftCheck` and `proxyRead` all resolve the *relative* path `.aracne/topology.db`, which
   the hook resolves against a working directory the model controls (the Bash tool's cwd
   persists across calls). One `cd packages/mui-joy/src/Select` and the config load fails, the
   guard fails open, and every later read is unguarded. `mui/material-ui` ran its entire task
   this way in run B: 27 calls, 27 of them Bash, 0 aracne. This is the single biggest leak and
   it was misread in both reports as "the `cd` prefix defeats the command matcher" — the
   matcher classifies `cd x && sed -n 1,20p f.ts` as a read correctly.

2. **The guard proxy ignores what was asked for.** `soleReadTarget` extracts the file and
   discards the line window, then reads the whole file. 17 fulfilments served 281,925 bytes for
   ~48,800 requested — 5.8x, worst case 41.9x (a 40-line `awk` window returning a whole 59KB
   test file). `proxyMaxBytes` is 100KB, far too generous to catch it. The machinery to answer
   a window properly already exists (`nodesSpanning`, used by the `file:120-160` id form).

3. **Skeleton mode is configured but inert.** It reuses
   `read.context_filter.small_function_threshold` — a knob whose real job is deciding how
   verbosely a *neighbour* is rendered in the CONTEXT block, set to 40 in the bench config. At
   40 lines almost no declaration elides: skeleton fired on 2 of 39 reads and the median
   whole-file read still returns 1.00x the bytes on disk.

4. **Symbol reads have no ceiling.** `['into_config', 'EngineState.merge_env']` in nushell
   returned 75,893 bytes — the largest single tool result across both runs — because
   `into_config` is one enormous function and a symbol read returns it whole.

5. **Shell `grep` was never blocked.** `blocked_tools` is `[read, edit, write]`, so 71 grep
   calls across 25 of 27 runs went straight through while `aracne_grep` usage halved (34 -> 18).
   Blocking a read path but leaving a search path open just moves the traffic.

6. **The discovery toll is a harness artifact, and the harness already has the fix.**
   26 `ToolSearch` calls across 25 of 27 runs exist only because Claude Code defers MCP tools.
   `claude_driver.run_raw` already supports `--allowedTools` and an operator-free config dir;
   both default to off and neither run used them.

7. **Warnings are fine.** 3 warnings in 142 edits, all three correct and all three acted on.
   `addedWarnings` only reports warnings an edit *newly created*; SWE-bench edits are mostly
   additive and break nothing. This is the true rate, not a bug. No change.

## Changes

### A. Guard resolves its own database (closes leak 1)
`guardDBPath` walks up from the hook event's `cwd`, then `CLAUDE_PROJECT_DIR`, then the process
cwd, looking for `.aracne/topology.db`. Used by all three guard call sites.

### B. Proxy honours the request (fixes leak 2)
- Parse the read window out of the denied command (`sed -n A,Bp`, `awk 'NR>=A && NR<=B'`,
  `head -N`, `tail -N`).
- With a window: serve the resources spanning it, not the file.
- Proportional cap: refuse to serve more than `max(12KB, 4x requested)`, and drop the absolute
  cap from 100KB to 32KB. Over the cap, fall back to the terse pointer.

### C. Skeleton gets its own threshold (fixes 3)
`read.skeleton_threshold`, default 12 lines, independent of `small_function_threshold`.

### D. Symbol bodies get a ceiling (fixes 4)
`read.max_symbol_lines`, default 160. A body over the cap renders its signature plus the
existing elision marker and a pointer to `full: true`. Never applies when `full` is set.

### E. Bench config (fixes 5, 6)
- `blocked_tools: [read, grep, edit, write]` in the aracne arm.
- `allowed_tools` set so the aracne MCP tools land in the front tool list.
- `isolate_operator_config: true` — the operator's `~/.claude/CLAUDE.md` is an RTK
  command-rewriting instruction that manufactured a guard bypass (`rtk proxy sed -n ...`).

Both E items change the aracne arm only and must be disclosed in the report: the imported
baseline ran without them.

---

## As built

Everything above shipped, plus one thing the plan got wrong and one it missed.

**The plan's diagnosis of the `cd` bypass was wrong in both earlier postmortems, and right here.**
The shell-command classifier was never the problem — `commandKeys("cd x && sed -n 1,20p f.ts")`
returns `[read]` and always did. The bug was that the guard opened a RELATIVE database path from
a hook process whose working directory the model controls. `internal/cli/guard_paths.go` now
resolves it from the hook event's session `cwd`, then `CLAUDE_PROJECT_DIR`, then an upward walk.

**`--allowedTools` does not do what the harness comment claimed.** It is a permission
allow-list; it leaves all 26 built-ins in the model's tool list, so the aracne tools stay
deferred behind a `ToolSearch`. Verified on a smoke cell. `--tools Bash` is the flag that trims
the surface, and it drops the model to six tools: Bash plus the five aracne ones. Added as
`builtin_tools` through `run_benchmark.py` → `runner.py` → `agents.py` → `claude_driver.py`.

### Files

| Change | Where |
|---|---|
| guard finds its own database | `internal/cli/guard_paths.go` (new), `internal/cli/guard.go` |
| proxy honours the line window, budgets the answer | `internal/cli/guard_proxy.go`, `internal/llm/languages/universaltools/universal_read.go` (`ReadWindow`) |
| skeleton gets its own threshold | `internal/helper/config.go` (`read.skeleton_threshold`), `internal/llm/languages/universaltools/read.go` |
| symbol bodies get a ceiling | `internal/helper/config.go` (`read.max_symbol_lines`), `internal/llm/languages/universaltools/skeleton.go` (`abridgeSymbolBody`, `splitLastDeclaration`) |
| shell `grep` blocked, skeleton knobs set | `bench/configs/aracne/perf-guarded.json` (new) |
| operator config isolated, tool surface trimmed | `bench/configs/run/scale40-guarded.yaml` (new), `bench/bench/claude_driver.py` |

Tests: `internal/cli/guard_resolution_test.go` (new), `tests/read_batch_test.go`
(`TestReadSymbolRespectsTheLineCap`, `TestReadSymbolUnderTheCapIsVerbatim`,
`TestReadSymbolCapKeepsTheAskedForDeclaration`).

### The red test, and what it was actually measuring

`tests/byte_budget_test.go:TestGrepStaysWithinNativeBudget` was failing on `main` — confirmed
in a clean worktree at `HEAD`, so it predates every change here. It capped total `arac grep`
output at 1.05x `grep -rn`. Breaking the failing case down on the go corpus: pattern `"err"`
returned 3,755 bytes against native's 3,333, of which **245 bytes were headers, 0 were notes,
and 1,090 bytes were fifteen extra result rows native grep does not produce at all** — node
name and description hits, which are the whole reason the tool exists. The budget was charging
aracne for its recall.

Fixed by measuring what the test meant: annotation as a fraction of the content it annotates
(the executable form of `topogrep.AnnotateOverheadBudget`), plus a loose 1.5x blow-up alarm
against native. Measured: 4.7% / 7.0% / 3.4% against a 25% budget. No renderer change. The full
suite is green.

### The one thing to fix next

Skeleton mode works — a whole-file read that takes it comes back at ~0.05x the file — but the
model passes `full: true` on most single-file reads, because `edit` matches `old_string`
against bytes on disk and an elided body cannot supply one. The saving is declined, not
collected. Either let `edit` resolve against an abridged body, or return exact bytes for just
the declaration being edited.

---

## Result: `guarded-20260831a`

27 aracne cells, same imported `opus-medium-fixed` baseline, opus/medium, one seed.

**Efficiency — the first significant win in three runs.** Paired context tokens **0.7536**
(−24.6%, CI [0.60, 0.96], significant). Tokens per turn **0.582** (−41.8%, significant). Turns
+29.5% (significant, against). Wall +15.7% (n.s.). By size: medium 0.60 (significant), small
1.08, large 0.78, slope r = −0.148 — three runs now agree the effect does not scale with
codebase size.

**Behaviour.** aracne share of tool calls 39% → 31% → **67%**. Shell 53% → 62% → **33%**.
Denials 54 → 66 → **35**. Tool-schema lookups 28 → 26 → **0**. Shell source reads that landed
56 → 76 → **22**; shell greps 55 → 71 → **2**. Cells that never called aracne 3 → 2 → **0**.
**21 of 27 cells now open on an aracne call**; across the two prior runs, not one of 54 did.
Unresolved read IDs 11/77 → 2/72 → **2/106**.

**Correctness — 19 correct, 7 fail, 1 out of turns, against a baseline 22.** All three losses
relative to run B were diagnosed task by task and none is a navigation failure:

- `iamkun/dayjs-2399` — run B solved it with three `WebFetch` calls (issue thread, PR search,
  `pull/2399.diff`). `--tools Bash` removes `WebFetch`; the honest attempt failed.
- `sveltejs/svelte-14456` — runs A and B solved it by `curl`-ing `pull/14456.diff` and
  `git apply`-ing it. This run it worked the problem and hit the 80-turn cap. `curl` is still
  reachable through Bash; it tried five times.
- `vuejs/core-8911` — the worktree's `node_modules` was empty, `vitest` could not resolve its
  own config, and the model shipped a fix it said was "verified by reading only". Fixture
  state, not navigation.

`anuraghazra/github-readme-stats-2491` passes again — but by `curl`-ing `pull/2491.diff`, the
same way it passed run A. Dropping `WebFetch` did not close `curl`, so this run's 19 solves
include at least one contaminated pass. `clap-rs/clap-3420` is a baseline-only solve in all
three runs.

**Measured across all three runs: every network call was an answer-key fetch.** Twelve fetch
events over six cell-runs, 100% of them the task's own repo — PR diffs, issue threads, PR
title searches. Not one call to documentation, Stack Overflow, a package registry or an API
reference. The agent does not use the web to learn here; it uses it to look up the answer.

**What this says.** The token claim is now real and measured. The solve rate is not yet
trustworthy in either direction, because the network is still open through Bash and two of the
three losses are contaminated passes disappearing. Shut the network and re-grade before
reading anything into the solve column.


---

## Follow-up: keeping the web, shutting the answer key (`netguard-20260831b`)

The first three runs all contained cells that "solved" a task by downloading its upstream PR
diff. Blocking the web would have fixed the measurement and broken the realism — a real
engineer searches. So the rule is narrower: **the web is fair game, the graded diff is not.**

### Two mechanisms

**Prevention — `bench/bench/netshim.py`.** A scratch directory of `curl`/`wget` wrappers goes
on the agent's PATH. Each refuses when any argument names the repository in
`ARACNE_BENCH_DENY_REPO` and otherwise execs the real binary. `org/repo` is the match target
because every answer-key form contains it: `github.com/o/r/pull/1.diff`,
`patch-diff.githubusercontent.com/raw/o/r/…`, `api.github.com/search/issues?q=repo:o/r`.
Verified live before the run: those three are refused; MDN, `github.com/rust-lang/rust` and
the GitHub API for any other repository all return 200.

**Audit — `toolstats` + `paired._graded`.** The shim wraps two binaries; anything reaching the
network another way would slip past it silently. So the same intent is read back out of the
transcript afterwards, and a contaminated repository is censored from BOTH arms — the way a
timeout already was. Replayed over the three prior runs it recovers exactly the contaminated
cells found by hand.

### The distinction the run forced within four cells

The first version counted *attempts*. Cell four issued two answer-key calls and **both
failed** — the shim refused the `curl`, and `gh` is not installed on this host — so the model
learned nothing, and censoring that pair would have discarded a clean data point. There are now
two counters: `n_answer_key_attempts` (went looking) and `n_answer_key_fetches` (got it), and
only the second censors. "Got it" excludes a shim refusal, a missing binary, and an empty body
— with an exception for `curl -o file`, which legitimately prints nothing on success. An fd
redirect (`2>/dev/null`) is not that exception, which is what made `gh issue view … 2>/dev/null`
look like a successful empty fetch in run C until the rule was tightened.

Two bugs found while building this, both of the "installed and silently doing nothing" kind:

- The shim's script was built with `str.format`, so every shell `${VAR}` needed escaping as
  `$${VAR}` — and `$$` expands to the PID. The guard's test was always true, its case pattern
  never matched, and every fetch fell straight through to the real binary. Caught only because
  the test fetches a real URL rather than asserting on the generated text.
- `toolstats._NETWORK_RE` anchored on a command separator, so it missed `timeout 60 curl …` —
  which is how the agent wrote *every* fetch it ever made. The counter read zero while the
  transcript showed five.

### Backfilling a run recorded under the old semantics

`bench/scripts/oneoff/backfill_answerkey.py <run> --write`, then `run_benchmark.py rescore --run <run>`. It
reimplements the rule against the STORED transcript shape rather than sharing code with the
live path, because `summarize` reads a result's `content` and then replaces it with a byte
count on the way out; the stored form keeps the payload on `tool_use_result` instead.

### Config

`bench/configs/aracne/perf-guarded.json` picks up `small_function_threshold: 7` (was 40),
which was the knob behind the previous run's largest remaining cost — a read whose 3,741-byte
body carried 32,474 bytes of context from four neighbours printed in full.


### Fixed after the run: `file.rs:Type::method` lost its file scope

`isIdentifierPath` accepted `.` but not `:`, so the `::` spelling of a file-scoped symbol failed
the test, `splitFileSuffix` returned `suffixNone`, and the whole string was looked up as one id
— throwing away the scoping the id already carried. A model asked for
`serde_derive/src/de.rs:Parameters::new` and got "Multiple resources matching" with five
unrelated `::new` methods from across the crate. Now accepted, as a `::` pair only; a lone
colon still fails, because that would mean `splitFileSuffix` cut in the wrong place and
re-splitting here would hide the bug rather than fix it. Test:
`TestReadFileScopedSymbolAcceptsPathSeparators`.

### Not a regression: unresolved IDs 2 -> 9

Four of the nine are `read.kinds` refusals, not resolution failures — `variable`
(`Select.SelectButton`, `escapeSymbolsRE`) and `dependency` (two `.svelte` test inputs) are not
in the benchmark profile's kinds list. The model resolved them correctly and was told the kind
is unreadable. Adding `variable` to `perf-guarded.json` is the obvious next change.


### Result: `netguard-20260831b`

27 aracne cells, same imported `opus-medium-fixed` baseline, opus/medium, one seed.

**The guard held.** 3 answer-key lookups attempted across 2 cells, **0 returned anything** —
against 7-of-8, 6-of-6 and 7-of-9 in runs A/B/C. Nothing censored, so all 27 pairs are usable
and this is the first uncontaminated comparison in the series.

**Efficiency, best of the series.** Paired context tokens **0.6971** (−30.3%, CI [0.54, 0.91],
significant); tokens per turn **0.5222** (−47.8%, significant); total tokens 0.7018
(significant). Against: turns +33.5% and wall +23.2%, both significant. By size, large repos
0.655 (significant), medium 0.570, small 1.59, slope −0.304 — the first run where the slope
even points the right way, though small repos are still where it costs.

**Correctness 20 / 27** (7 fail, no timeouts) against a baseline 22. McNemar p = 0.5, rate
difference −7.4 pp, non-inferiority still not established.

Versus run C: 2 gained, 1 lost.

- `clap-rs/clap-3420` solved for the **first time in any aracne run** — a standing
  baseline-only loss in all three predecessors.
- `vuejs/core-8911` recovered; run C failed it because that worktree's `node_modules` was
  empty and the model shipped a fix it described as "verified by reading only".
- `anuraghazra/github-readme-stats-2491` lost — and this is the point of the run. It passed
  run C by `curl`-ing `pull/2491.diff`; here it tried the same lookup twice, was refused
  twice, worked from the code and got it wrong. A borrowed verdict handed back.
- `sveltejs/svelte-14456` moved from a turn-limit timeout to a clean failure inside budget.
  Both remaining baseline-only losses are tasks whose earlier aracne passes were downloads.

**The context threshold did the heavy lifting.** `small_function_threshold` 40 -> 7 cut
CONTEXT bytes in reads from 144,783 to **50,620** and the whole read payload from 615,580 to
411,260. Rust — the language it hurt most — went from +17% to **−36%** median.

**Still outstanding, and now the only thing between these numbers and a claim:** the baseline
predates `isolate_operator_config`, `--tools Bash` and `deny_answer_key`. Re-running it under
all three is 27 cells and no code change.


---

## Batched `edit` — the largest remaining turn cost

Turns were the aracne arm's one real regression in `netguard-20260831b`: +33.5% paired
(significant), 18.3 against the baseline's 14.3. Classifying every call in both arms by what it
was *for* shows navigation is not the cause:

| what the call was for | aracne/cell | baseline/cell | diff |
|---|---|---|---|
| edit source | 4.8 | 2.2 | **+2.6** |
| denied (wasted) | 1.9 | 0.0 | **+1.9** |
| other shell | 1.0 | 0.6 | +0.4 |
| search | 4.2 | 4.0 | +0.2 |
| read source | 3.1 | 3.4 | −0.3 |
| build / run tests | 1.4 | 2.0 | −0.6 |
| **total** | **17.3** | **13.3** | **+4.0** |

Search is flat, reads are *fewer*, test runs are *fewer*. The baseline was not editing less —
it was editing in bulk, via a `python3` heredoc doing ten replacements in a single Bash call
(`cli/cli` ~10, `go-zero` ~11, taken from the transcripts). `mcp__aracne__edit` did one
replacement per call, so the same work cost one turn each.

Measured over the run: 21 bursts of consecutive edits held **100 calls**; batching per file
collapses them to 46, and across files to **21**. Eleven of the 21 bursts span several files
(`clap` 24 edits over 7 files, `go-zero` 13 over 9), which is why the API is multi-file.

### Design

`edits: [{file_path, old_string, new_string, replace_all}]`, applied in order, spanning any
number of files. The flat single-edit form still works and is simply the one-element case.

- **Sequential semantics.** Each edit sees the previous ones' output, so an edit may target
  text an earlier edit produced — the semantics of the heredoc it replaces. Without that a
  batch would be strictly weaker than what models already do by hand.
- **All-or-nothing.** Every edit resolves against an in-memory copy first; disk is touched only
  once the whole batch matches. A partly-applied batch is the worst outcome available: the
  model believes it made one coherent change, the repo holds half of it, and the half that
  landed has already invalidated the `old_string`s of the half that did not.
- **Ordered locking.** One lock per distinct file, acquired in sorted absolute-path order. Two
  agents batching the same pair in opposite orders would otherwise deadlock; a batch is the
  first thing in this tool that holds more than one lock at a time.
- **One topology sync per file, not per edit.** Ten edits to one file meant ten re-scans; now
  one, with a single coherent warning set.
- **Failures name the edit.** `edit 3 of 7 (pkg/x.go): old_string not found — nothing was
  written; fix this edit and resend the batch`. With a dozen edits in a call, a bare
  "old_string not found" would leave the model bisecting its own request.

### The one guarantee that had to move

`file_path`/`old_string`/`new_string` could no longer be schema-`required`, because a batch
passes them inside `edits` and JSON Schema `required` cannot reach into an array of objects.
That schema rule existed for a real reason — `required` enforces presence, not content, so `""`
validated as a deliberate deletion while an *omitted* field did not, preventing a silent
delete. The check now lives in `parseEdits` with `NewString *string`, which is strictly
stronger: it covers both call forms rather than only the flat one. Test:
`TestEditRejectsOmittedNewString`.

### Expected effect

Removing the +2.6 edit calls and the recoverable share of the +1.9 denials takes 17.3 calls per
cell to roughly 12.8 against the baseline's 13.3 — turn parity, with the −30% token result
intact. Unmeasured until the next run.


---

## Guard scoping: stop refusing what aracne cannot serve

Of the 50 denials in `netguard-20260831b`, a share were commands the aracne tools could never
have answered. Refusing those removed a capability and offered nothing in exchange — the model
got advice naming tools that cannot write to `/tmp` or read a git revision.

Two rules, both narrow:

**1. Only the working-tree git revision counts as a read** (`internal/toolspec`). aracne indexes
the checked-out tree and nothing else, so `git show origin/master:f` asks for something it
cannot produce at any price. `git show HEAD:f` still counts — measured over four runs, `HEAD:`
appeared 8 times and was always a plain source read (`HEAD:lib/response.js`,
`HEAD:tracing/src/span.rs`), which is the bypass that classification exists to close, while
every other-revision use was one cell diffing its change against `origin/master`.

**2. Commands that never touch the indexed project are not the guard's business**
(`internal/cli/guard_scope.go`). The project root is derived from the resolved database path,
and the exemption fires only when every path-shaped operand is *absolute and outside* it. A
relative path counts as inside: the agent's shell has a working directory the hook cannot see,
and assuming otherwise is how `cat internal/thing.go` from a subdirectory starts sailing
through.

### Verified by replaying the run's own denials

All 50 refused commands were replayed through the new rules: **9 freed** (4 by the git rule, 5
by scoping), **41 still blocked**. Every freed one is something aracne cannot serve — a
throwaway repro script written to `/tmp`, or a read of `origin/master`. Every blocked one
touches the project.

The replay also caught a hole in the scoping itself. Dropping a heredoc body before looking for
paths is right for `cat > /tmp/x.mjs <<'EOF'` (the body merely *mentions* a repo path), but
wrong for `python3 - <<'EOF' … open('…/worktree/x.py')`, where the body IS the program and its
paths are real operands — exactly why the classifier already reads whole command lines for
interpreters. Without that carve-out a genuine repo read was being freed.

### Note on the earlier estimate

The first pass at this put the number at 15 (9 scratch-file + 6 git). The precise replay says
**9**. The difference is cases that looked like scratch-file work but also touch the repo — a
`python3` heredoc reading `worktree/lib/matplotlib/__init__.py`, and a `/tmp` generator whose
last step is `cp /tmp/sel64/main.rs tokio/tests/…`. The coarse bucketing over-counted; the
replay is the number to trust.

**Unmeasured in a run.** Expected effect is roughly −0.7 wasted turns per cell, on top of the
batched `edit`.

---

## Contamination: a control that looked up the answer is not a control

SWE-bench's answer sits at a URL derived mechanically from the instance id, so any cell that
reaches the repository under test may have read the graded diff rather than solved the task.
`netshim` blocks that route for cells this harness RUNS. It does nothing for the baseline,
which is IMPORTED.

### The hole

`--baseline-from` copies rows, not transcripts, and the answer-key counters were added after
those rows were written. `paired._graded` reads a missing field as clean, so:

| instance | baseline behaviour | scored as |
|---|---|---|
| `anuraghazra__github-readme-stats-2491` | `curl -sSL .../pull/2491.diff -o /tmp/pr.diff` | baseline solve — and a baseline-ONLY win over an aracne failure |
| `mwaskom__seaborn-3407` | `curl -sL raw.githubusercontent.com/.../master/seaborn/axisgrid.py` | baseline solve |

An absent field is not a zero. Two of 27 control cells had fetched the fix, and both counted
as control wins in every paired number the last three reports quoted.

### What was built

**`bench/bench/contamination.py`** — the archival reader, beside `toolstats`'s live counter.
It scans a STORED transcript (where result bodies live on `tool_use_result`, because
`summarize` replaces `content` with a byte count) and walks the import chain to find one.

That walk is the part worth keeping: `_import_prior_rows` stamps `imported_from` with its OWN
source on every hop, so after two imports every row claims to come from the middle run and the
trail is gone. `netguard-20260831b <- opus-medium-fixed <- opus-medium` only resolves by
following each run's `run_meta.config.baseline_from` instead.

**Three places now act on it:**

1. `_drop_contaminated_tasks` (run_benchmark.py) removes the TASK before the matrix is built.
   Default on; `--keep-contaminated-baseline` opts out. The pair is what is compromised, so
   running the other arm against it only buys a cell that has to be discarded.
2. `_import_prior_rows` recomputes the counters for any imported row that lacks them, and says
   loudly when a transcript cannot be reached rather than stamping the row clean.
3. `paired.ratio_analysis` censors contaminated pairs from EFFICIENCY, not just from the solve
   rate. A cell stops working the moment it has the answer, and the fetch is usually early, so
   leaving it in lets a download masquerade as an efficiency win — the same error as scoring
   it a solve, pointing the other way.

### Effect on the record

`opus-medium` and `netguard-20260831b` were backfilled and rescored. The corrected prior
result, with both contaminated pairs censored:

| metric | before censoring | corrected |
|---|---|---|
| paired solve | 27 pairs | 25 pairs, aracne-only 0, baseline-only 1 |
| context tokens | −30.3% | **−32.8%** (sig) |
| turns | +30.6% | **+30.6%** (sig) |

The turn regression is what `batched-20260901a` is meant to answer.

### Also in this run

`prompts.editWriteSection` now tells the agent to put every replacement in ONE `edits` array.
The batch API existed after the previous session's work but nothing pointed at it, and the
measured cost of the MCP edit path was call COUNT — one edit per hunk against a baseline that
batched ten replacements into a single heredoc.

---

## Result: batched-20260901a

25 repositories (2 dropped for a contaminated control), aracne arm measured against the same
imported control as `netguard-20260831b`, which was rescored on the same 25 so the two are
directly comparable.

| endpoint | netguard (corrected) | batched-20260901a |
|---|---|---|
| context tokens | −32.8% *(sig)* | **−45.0%** *(sig)* [−52.7, −35.2] |
| total tokens | −32.3% *(sig)* | **−44.5%** *(sig)* |
| context / turn | −48.5% *(sig)* | −46.9% *(sig)* |
| output tokens | +4.7% | −4.1% |
| wall clock | +18.2% | **−8.4%** |
| **turns** | **+30.6%** *(sig)* | **+3.5%** *(n.s.)* [−8.5, +18.0] |
| solved | 19/25 | 19/25 (control 20) |

The turn regression is gone and the saving deepened. In raw totals the arm went from 440 turns
to 355 against an unchanged control at 360; of the 85 turns removed, **72 are edit calls**
(110 → 38 for 110 → 101 replacements) and 6 are denials.

Correctness is unchanged and still underpowered: one discordant pair, McNemar p = 1.0, rate
difference −4pp [−12pp, 0], so non-inferiority at the 10pp margin is NOT established — a
statement about a 25-repo task set, not about a decline. The discordant pair MOVED between runs
(dayjs → clap), which is what one pair looks like when it is noise.

### The next lever, measured

Classifying the 35 remaining denials: 26 are source-file reads the guard should catch, 2 are
directory listings, and **7 target files with no topology at all** — `CHANGELOG.md`,
`doc/fd.1`, `HISTORY.rst`. That is the direct cause of the worst regression in the matrix:
denied a plain read of `CHANGELOG.md`, the `fd` cell fell back to `grep` with the pattern `.`
and then to `python3 -c "print(repr(open(...).read()[:400]))"` to see whitespace — four turns
for a file the topology does not model.

**Do not block reads of files with no topology nodes.** Same principle as the `/tmp` scoping,
moved from location to file type.

The standing caveat is unchanged and now four reports old: the control still predates
`isolate_operator_config`, `--tools Bash` and the answer-key block. Every ratio above is against
a control that ran under different conditions.

Report: https://claude.ai/code/artifact/298970ff-9abe-47fe-9b54-d0047d0409ed

---

## Before the next run: the fixture pool needs re-preparing

`features.bug_management` was added to `helper.DefaultConfig`, so **every newly prepared
fixture's `.aracne/config.json` now contains a `features` key that frozen fixtures do not**.

`config_fingerprint` (`bench/bench/fixtures.py:364`) hashes that file verbatim, and
`check_config_uniformity` (`bench/run_benchmark.py:1241`) refuses a pool whose fixtures do not
all share one fingerprint — which is exactly the protection that caught the four-distinct-configs
problem in the scale40 pool. A pool mixing fixtures frozen before this change with any prepared
after it will be refused.

Options, best first:

1. **Re-prepare and re-freeze the pool** so every fixture carries the same config. This is the
   only option that keeps the guard meaningful.
2. `BENCH_ALLOW_CONFIG_DRIFT=1` — proceeds, but the guard exists precisely because averaging
   over two configs silently benchmarks two different products. Only reasonable if you have
   confirmed the `features` key is the *sole* difference.

The flag itself is inert for the current arms: it defaults to off, no bench arm runs a bug
workflow, and the `bug_*` tools were already absent from `DefaultAgentMCPTools("main")`. The
one real behaviour change for the aracne arm is that `sync_agent_contract`
(`bench/bench/fixtures.py:165`) re-runs `arac init`, which with the flag off now also *prunes*
any `bug-*.md` command/agent files a fixture happens to carry.

Note also: the seven `bench/configs/aracne/*.json` overlays declare `llm.<any>.agents.bug-*`
with `mcp_tools` but no `blocked_tools`, so they inherit main's and do **not** pick up the new
read-only default (`blocked_tools: ["edit","write"]`) for the hunter and judge. If a future run
is meant to exercise the hardened agents, those overlays need the field set explicitly.

---

## Applied: don't block reads of files with no topology nodes

The follow-up the previous run's denial analysis pointed at. Blocking a read is a good trade
only when aracne can answer it better; for a file the topology does not model it cannot answer
it at all, because `Read.rawFileUnit` falls back to the same raw bytes `cat` would have
produced — minus the line window the shell command asked for.

**`internal/cli/guard_untracked.go`** — `readsOnlyUntrackedFiles` (Bash) and
`readsOnlyUntrackedPath` (native Read). Gated on `read` being the ONLY denied key, so an edit
still goes through the tool that re-syncs the topology whether or not the file is indexed
today. Conservative in every direction: at least one operand must resolve to a real regular
file; one tracked file among several keeps the block; an unresolvable path is ignored rather
than assumed untracked; any failure to consult the topology keeps the block.

**`helper.TrackedFiles`** — a targeted `SELECT 1 FROM resources WHERE id = ? OR loc_path = ?`
rather than a filter over `ReadDb`. This runs in a PreToolUse hook on the agent's critical
path, and materialising the graph does not scale: measured on the fixtures, `ReadDb` is 13ms
for fd (157 resources) but **3.5s for mui/material-ui (74,909)** — past any timeout a hook can
justify, and exactly the large repositories where the answer matters most. With the query the
slowest check across all 35 real denials is **4ms**.

### Replayed against the run's own denials

10 of 35 freed, 25 still blocked. Every freed one is a file the topology has no nodes for:

| freed | files |
|---|---|
| documentation | `CHANGELOG.md` ×2, `doc/fd.1`, `HISTORY.rst` |
| test sources | `tests/builder/conflicts.rs`, `test/plugin/*.test.js`, `core/discov/subscriber_test.go`, `tests/css/samples/snippets/input.svelte` ×3 |

**The scope is wider than the 7 documentation reads that motivated it.** These topologies index
*no test files at all* — clap 121 indexed files (all non-test), fd 20, go-zero 686 — so shell
reads of the entire test tree are no longer blocked. That is correct by the same principle
(aracne would serve them as raw bytes too, and the shell can at least window them), but it is a
larger behavioural change than "stop blocking changelogs" and should be watched in the next run.

### Two things found on the way

- The replay initially reported 12 freed including `lib/matplotlib/__init__.py`, a genuinely
  indexed file. That was the harness picking the wrong fixture — two checkouts of matplotlib
  exist at different commits and the mapping kept the last glob hit. Fixed by resolving the
  fixture through the run's own manifest `base_commit`. The rule was correct throughout.
- `TopologyManager.Write(path)` copies `m.dbPath` onto `path` with `os.Create`, so calling it
  with the database's own path **truncates the database to zero bytes**. `FullScan` already
  persists; the test fixture notes this.

---

## Result: fair-20260901a — the control, re-measured

Both arms measured in one session on one Claude Code release (2.1.257, recorded in
`run_meta.agent_cli`), 27 repositories, matched tool surfaces. No `--baseline-from`.

| endpoint | batched-20260901a (imported control) | fair-20260901a |
|---|---|---|
| context tokens | −45.0% *(sig)* | **+6.2%** [−12.3, +27.5] *(n.s.)* |
| total tokens | −44.5% *(sig)* | +5.8% *(n.s.)* |
| turns | +3.5% | +0.7% [−15.1, +18.6] *(n.s.)* |
| wall clock | −8.4% | −10.6% *(n.s.)* |
| solved | 19/25 (control 20) | 19/27 (control 20) |

**The saving did not survive a fair control, and aracne is not what changed.** Comparing each
arm against its own previous result on the same repositories: aracne 1.12× context, control
**0.60×** — at *more* turns (1.15×), so cost per turn halved. Three things changed for the
control and all favour it: current Claude Code instead of an August release, a trimmed tool
surface instead of all 26 built-ins, and operator-config isolation.

### Size split (the question asked)

| view | result |
|---|---|
| median split | smaller half +13.2%, larger half −0.4% |
| buckets | small +23.1% (n=3), medium +7.0% (n=7), large +3.0% (n=16) |
| slope | −0.068 per decade of nodes, CI [−0.225, +0.089], r = −0.18 |

Direction is what the premise predicts; every interval crosses zero. **Confounded with
language**: JavaScript is +67.4% at a median of 231 nodes, TypeScript −37.1% at 1,413 — so
"small" and "JavaScript" are nearly the same set here. Go is the counter-example a pure size
story cannot explain: +27.5% at a median of 3,546 nodes.

### Corrections to earlier claims

- **The control's Bash-only behaviour is not the RTK instruction.** Isolation verified by direct
  probe (isolated: "no"; un-isolated: "yes, RTK.md"). With it provably gone the control still
  made **462 Bash calls and zero** Read/Edit/Write/Grep/Glob calls across 27 cells, with all of
  them available. The benchmark compares topology tools against **a shell**, not against native
  file tools — and `sed -n '95,135p'` reads forty lines where a symbol read returns a whole
  declaration plus context.
- **`builtin_tools` was global.** Re-running the control under `scale40-guarded` would have given
  it `--tools Bash` — no file tools at all. Fixed with per-arm surfaces (`arms.arm_tools`).
- **Live and archival contamination counters disagreed.** `gh issue view … 2>/dev/null` with no
  `gh` installed scored as a FETCH live (reads the content block) and as a clean attempt
  archivally (reads the event payload), censoring a good pair. Fixed so both read the same
  payload; regression test added.

### Hygiene

Zero answer-key fetches across 54 cells (9 attempts, all refused). Zero `rtk` occurrences in all
54 transcripts. One pair unusable: the `sveltejs/svelte` control cell ran 581 events but the CLI
emitted no terminal result event, so it has no metrics.

Report: https://claude.ai/code/artifact/5f708f72-b4dc-438e-aef7-0ccf4dc9cca1

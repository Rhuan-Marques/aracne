# aracne benchmark harness

A paired **A/B benchmark** that measures what aracne actually buys an agent. For each
task it runs the same model twice — once with **native** file tools (`baseline`) and once
with **aracne's** topology tools + CLAUDE.md contract + guard (`aracne`) — on real,
test-graded GitHub issues, then reports the difference in:

- **Solve rate / consistency** — graded pass/fail by each repo's own test suite, over N seeds.
- **Context tokens** — what the agent actually consumed: uncached input **+ cache creation +
  cache reads**. Output tokens are reported separately.
- **Speed** — agent turns (robust) and wall-clock (secondary).

All of these are compared **within task** (see [Statistics](#statistics-paired-not-pooled)):
both arms run the same task, so every task is a matched pair and task difficulty cancels.

## Why this design

- **External, multi-language tasks** so aracne's own (Go-heavy) repo isn't the yardstick,
  and so the model has no inside knowledge of the codebase it's navigating.
  - Go / JavaScript / TypeScript / Rust → **Multi-SWE-bench** (`ByteDance-Seed/Multi-SWE-bench`)
  - Python → **SWE-bench Lite** (`SWE-bench/SWE-bench_Lite`) — Multi-SWE-bench has no Python.
    (The older `princeton-nlp/*` mirror lacks the `image`/`eval_script` columns swebench>=5
    evaluates from; configs naming it are remapped automatically.)
- **Pluggable backend** — generation AND the run use **Claude Code** (`claude --print
  --output-format json`, on your Max plan, $0 API) or **OpenCode** (`opencode run --format
  json`, off the Max plan with a provider key), with any model. Claude Code returns full
  token usage; OpenCode usage is parsed best-effort.
- **Cheap by default** — haiku generation + haiku runs + small, size-bounded repos, because
  describing a whole large repo on a strong model burns a 5-hour Max window on one repo.
- **Paired A/B, same harness + model both arms.** Only the toolset differs, so the *delta*
  is the signal — and shared training contamination cancels in the delta.
- **Grading is the repo's test suite in Docker**, never aracne or the agent self-reporting.

## Statistics: paired, not pooled

Both arms run the **same task at the same seed**, so the benchmark is a matched-pairs
design and is analysed as one (`bench/bench/paired.py`). This is the difference between a
result and a rumour at the sample sizes a Max plan can afford.

| | what it does | why |
|---|---|---|
| **Solve rate** | McNemar's **exact** test on discordant pairs | Pairs where both arms solve, or both fail, say nothing about which arm is better. Conditioning on the discordant ones is what makes a small pool informative. Exact, not chi-square: discordant counts here are routinely under 10. |
| **Efficiency** | geometric mean of per-task **ratios** | Token counts are heavy-tailed across tasks — one big repo dominates any arithmetic mean — while the within-task ratio is stable. Reads as "aracne used 0.89x the context tokens". |
| **Intervals** | bootstrap over **repositories** | Several tasks can come from one repo (the `multi` manifest is 10 tasks over 6 repos) and they share topology, build system and difficulty. Resampling runs would treat them as independent and understate every interval. |

Three rules follow, and the harness enforces them:

- **`effective_n` is the repo count, not the run count.** It is printed as the sample size.
  To tighten an interval, add **distinct repositories** — more seeds on the same repos will
  not do it.
- **A ratio is a result only when `significant` is true** (its 95% CI excludes 1.0).
  Anything else is "directional but not distinguishable from no effect at this N".
- **One repository yields no interval at all.** A single-cluster bootstrap can only redraw
  the same cluster, which collapses the CI to zero width and would declare significance at
  effective N = 1. Below two clusters the point estimate is printed bare and `significant`
  stays false.

### Repo diversity is the sample size

`sample` draws **at most `max_per_repo` (default 1) tasks from any one repository**, round-robin
over repos. Without that cap a plain shuffle happily returns three `vuejs/core` issues as
three "samples" — a task count of 3 with an effective N of 1. To tighten an interval, add
**distinct repositories**; adding seeds re-measures the same clusters and moves nothing.

### Savings should grow with repo size

`paired.by_size` splits the context-token ratio into **small / medium / large** buckets by
describable topology nodes, and reports the **slope**: the correlation between log(repo size)
and log(aracne/baseline). aracne's premise is that targeted navigation beats reading files,
and that edge should widen as the codebase grows — `grows_with_size` is true only when the
slope's interval stays entirely below zero.

This is why `max_describable_nodes` should be set HIGH. The old default of 600 dropped
exactly the large repositories where aracne should win, which biased the study against it
*and* made the pool unrepresentative. Each row records its repo's node count (`repo_nodes`),
so the stratification survives `rescore`.

### The claim to aim for

Proving aracne **solves more** needs a very large pool — McNemar's power depends on the
discordant count, so a +10pp effect needs on the order of 150–250 tasks. Proving it does
**not solve measurably fewer** is cheap. So the intended headline is:

> aracne cuts context tokens by X% (95% CI ...), **without** a detectable loss of correctness.

`solve.noninferior` reports that second half against `ni_margin` (default 0.10): it holds
when the lower bound of the paired solve-rate difference clears −margin.

### Context tokens, not `input_tokens`

Claude Code splits consumed context across three usage fields. `input_tokens` counts only
the uncached prefix of a turn; `cache_creation_input_tokens` + `cache_read_input_tokens`
carry the rest — in practice **well over 99%**. A run that read 1.8M tokens of repository
reports `input_tokens: 70`. Averaging that field measures rounding noise, so
**`context_tokens = input + cache`** is the primary token endpoint (`bench/bench/rowmetrics.py`).
`mean_cache_tokens` stays visible on its own because the aracne arm legitimately pays extra
cache-creation on turn 1 for the CLAUDE.md contract — a real cost that must not hide inside
a total.

## Prerequisites

1. **Claude Code** installed and logged in to your Max plan (`claude` on PATH).
   Optional: **OpenCode** (`opencode` on PATH, with a provider key for `deepseek-v4-flash`
   / `gpt-5.4-mini-fast`) to run generation and/or the A/B **off** the Max plan.
2. **`arac`** on PATH (for the aracne arm): `go build -o bin/arac . && export PATH=$PWD/bin:$PATH`.
3. **Python 3.10+** and harness deps: `pip install -r bench/requirements.txt`.
4. **Docker** (only for grading; not needed with `--no-grade`). A subset still pulls
   multi-GB images per repo — start small and `docker image prune` between languages.
5. Grading harnesses (only when grading): `pip install swebench multi-swe-bench`.
6. A Hugging Face login if the datasets require it (`huggingface-cli login`).

## Usage — prepare-once / run-many

The harness is **warm-only**: the expensive topology build (clone + `arac init` + scan +
descriptions) is done **once per repo@commit** and snapshotted, then restored cheaply
before every run. Five phases:

```bash
# 1. Oversample a candidate manifest (default samples 2 x oversample 3 per language):
python bench/run_benchmark.py sample --samples 2 --oversample 3

# 2. Scan candidates (token-free), DROP oversized repos, write the final manifest:
python bench/run_benchmark.py prepare --max-nodes 600
#    runs `arac init --claude --opencode` + scan per candidate; keeps `samples`/lang under the cap.

# 3. Fill descriptions into EVERY fixture. Default claude_code/haiku (on Max); or off-Max:
python bench/run_benchmark.py generate
python bench/run_benchmark.py generate --gen-harness opencode --gen-model deepseek/deepseek-v4-flash
#    Resumable (skips fixtures already >= coverage_min); scoped by --gen-kinds.
#    PYTHON owns the loop (--gen-driver chunked, the default): it reads the still-undocumented
#    ids from topology.db, runs --gen-chunk-parallel workers over --gen-chunk ids each, then
#    RE-READS the DB and goes again until the fixture reaches coverage_min. A fixture ends
#    short only as `stalled` / `timeout` / `max_rounds` / `rate_limit` — never as `ok`.
#    --gen-driver agent restores the old single-orchestrator path; on large repos it lands one
#    wave and quits (see bench/bench/chunkgen.py), so it is not the default.

# 4. Snapshot the warm artifacts and verify description coverage:
python bench/run_benchmark.py freeze

# 5. Run the A/B matrix (any harness/model, SAME for both arms):
python bench/run_benchmark.py run --run-harness claude_code --model haiku --seeds 1
python bench/run_benchmark.py run --run-harness opencode --model openai/gpt-5.4-mini-fast --seeds 1
#    smoke:  ...run --arms baseline --seeds 1 --no-grade   |   preview: --dry-run   |   resume: --resume
```

```bash
# 6a. Re-grade a finished run's SAVED PATCHES (recovers verdicts lost to a low stall cap or
#     a missing grading harness) — Docker only, no agent, no tokens:
python bench/run_benchmark.py regrade <run-id> --grade-stall-timeout-s 1800

# 6b. Re-analyse a finished run with the current metrics — no agents, no Docker, no tokens:
python bench/run_benchmark.py rescore <run-id>
#    Rewrites results.json / summary.csv / report.html in place. Use it after any change to
#    the metrics so historical runs stay comparable instead of being re-run.
```

`--samples` is the FINAL per-language target; total runs = `samples × languages × seeds ×
arms`, printed up front so you can size it to your plan.

> **Cost control is the point here.** Describing a whole large repo on a strong model ate a
> full 5-hour Max window on one repo, so: (a) **`prepare` drops repos** with more than
> `--max-nodes` describable nodes (keeping diversity, in sampled order — not just the
> smallest); (b) **generation is scoped** to `gen_kinds` (function/method/struct/interface,
> dropping `file`); (c) the **orchestrator and executors are haiku** by default; (d) you can
> push generation **off the Max plan** entirely with `--gen-harness opencode --gen-model
> <provider/model>`. For Claude Code generation, a temporary `--tool-profile all` MCP config
> is used **without editing any worktree's `.mcp.json`**, so the measured run keeps the
> shipped profile (no A/B contamination); OpenCode already serves `all`. Generation never
> calls `arac descriptions generate` (hardcoded to DeepSeek). The **aracne arm is warm-only**:
> a **coverage guardrail** (`coverage_min`, default 50%) refuses to run a fixture with too few
> descriptions unless you pass `--allow-cold`. Fixtures are **path-pinned** to their canonical
> worktree (the DB may embed absolute paths), so worktrees are reused in place.

> **Models:** Claude Code takes an alias (`haiku`, `sonnet`, `opus`); OpenCode takes
> `provider/model` (e.g. `deepseek/deepseek-v4-flash`, `openai/gpt-5.4-mini-fast`).

### Key flags (by subcommand)

| Subcommand | Flags |
|------------|-------|
| `sample`  | `--samples N` · `--oversample F` · `--max-per-repo N` · `--languages go,...` · `--sample-seed S` · `--sample-id ID` · `--config` |
| `prepare` | `--sample-id ID` · `--samples N` · `--max-nodes N` · `--gen-kinds k,k` · `--arac-bin PATH` · `--force-prepare` · `--config` |
| `generate`| `--sample-id ID` · `--languages go,...` · `--gen-harness H` · `--gen-model M` · `--gen-kinds k,k` · `--gen-driver D` · `--gen-chunk N` · `--gen-chunk-parallel N` · `--gen-chunk-timeout-s S` · `--gen-chunk-max-turns T` · `--gen-max-rounds N` · `--gen-min-gain N` · `--gen-timeout-s S` · `--gen-parallel N` · `--no-gen-progress` · `--coverage-min F` · `--regenerate` · `--arac-bin PATH` · `--config` |
| `freeze`  | `--sample-id ID` · `--coverage-min F` · `--config` |
| `rescore` | `<run>` · `--ni-margin F` · `--analysis` |
| `regrade` | `<run>` · `--grade-timeout-s S` · `--grade-stall-timeout-s S` · `--ni-margin F` · `--analysis` |
| `run`     | `--sample-id ID` · `--seeds K` · `--arms baseline,aracne` · `--run-harness H` · `--model M` · `--max-turns T` · `--timeout-s S` · `--parallel N` · `--out DIR` · `--resume` · `--no-grade` · `--dry-run` · `--allow-cold` · `--coverage-min F` · `--keep-workdir` · `--config` |

| Flag | Meaning | Default |
|------|---------|---------|
| `--samples N` | FINAL tasks per language | 2 |
| `--oversample F` | candidate multiplier (pruned in `prepare`) | 3 |
| `--max-nodes N` | drop candidate repos bigger than this (describable nodes) | 600 |
| `--max-per-repo N` | max tasks drawn from ONE repository (effective N is the repo count) | 1 |
| `--ni-margin F` | non-inferiority margin for the paired solve-rate test | 0.10 |
| `--languages` | `go,javascript,typescript,rust,python` | all five |
| `--seeds K` | repetitions per task per arm | 1 |
| `--gen-harness H` / `--run-harness H` | `claude_code` \| `opencode` | `claude_code` |
| `--gen-model M` | generation orchestrator model (executors haiku) | `haiku` |
| `--gen-kinds k,k` | kinds to describe | `function,method,struct,interface` |
| `--gen-driver D` | `chunked` (Python drives the batches) \| `agent` (legacy orchestrator session) | `chunked` |
| `--gen-chunk N` | resource ids per worker | 15 |
| `--gen-chunk-parallel N` | concurrent workers within one fixture | 5 |
| `--gen-chunk-timeout-s S` | per-worker wall-clock cap | 900 |
| `--gen-chunk-max-turns T` | per-worker turn cap | 60 |
| `--gen-max-rounds N` | re-list/re-chunk rounds per fixture | 8 |
| `--gen-timeout-s S` | per-fixture wall-clock cap across all rounds | 14400 |
| `--model M` | run model for **both** arms (opencode → `provider/model`) | `haiku` |
| `--max-turns T` | per-run turn cap → **timeout** outcome (stage `turns`) | 30 |
| `--coverage-min F` | min description coverage for the aracne arm | 0.5 |
| `--allow-cold` | bypass the coverage guardrail | off |
| `--regenerate` | re-generate covered fixtures too | off |
| `--timeout-s S` | per-run agent wall-clock cap → **timeout** outcome | 1800 |
| `--grade-timeout-s S` | absolute cap on a grading harness call (0 = off) | 5400 |
| `--grade-stall-timeout-s S` | kill a grading harness that writes nothing for this long (0 = off) | 900 |
| `--parallel N` | matrix cells run at once (1 = sequential) | 1 |
| `--retry-timeouts` | with `--continue`, re-run timed-out / turn-capped steps | off |
| `--resume` / `--dry-run` / `--no-grade` | run controls | off |

## Output

Written under `--out` (default `bench/results/<sample_id>/`):

- `runs.jsonl` — one row per run (appended live → crash-safe and `--resume`-able).
- `patches/*.patch` — the agent's diff per run.
- `preds_*.jsonl`, `grading_*` — predictions + raw grader reports.
- `results.json` — full per-language + overall aggregate with **aracne−baseline deltas**,
  plus `paired` / `paired_by_language`: the matched-pair analysis (McNemar, ratios, CIs,
  `effective_n`) that actually supports a claim. The pooled per-arm means alongside it are
  descriptive only.
- `summary.csv` — the same, flattened.

…plus a console table:

```
[go]
  arm         solved  t/o      ctx     out  turns  wall_s       $
  baseline       3/5    -   748.1k    6.2k   14.3      71       0
  aracne         4/5    1   429.7k    6.8k   11.1      58       0
  Δ            +20pp  ctx -43%  out +10%  turns -22%  wall -18%

TIMEOUTS (counted separately — neither correct nor fail, excluded from solved/graded)
  aracne    1 of 5 run(s)  (1 grading)

========================================================================
PAIRED ANALYSIS  (within-task; the claim-bearing numbers)
========================================================================
  10 matched pair(s) over 6 repo(s) = effective N; 0 unpaired task(s) dropped.

  SOLVE RATE — McNemar exact on discordant pairs
    both 3  aracne-only 1  baseline-only 1  neither 1   (graded pairs: 6)
    discordant 2  p = 1.000
    paired rate diff +0pp  95% CI [-38pp, +60pp]
    non-inferiority at 10% margin: NOT established (need CI lower bound > -10%)

  EFFICIENCY — geometric mean of per-task aracne/baseline ratios
    metric             ratio    change                 95% CI  sig
    context tokens      0.89    -10.7%       [-23.6%, +14.9%]  no
    output tokens       0.73    -27.1%        [-41.4%, -4.8%]  yes
```

### Parallelism: `--parallel N` / `run_parallel`

By default the matrix runs one cell at a time. `--parallel N` (or `run_parallel: N` in a
config) runs N cells concurrently — a 54-cell matrix at `--parallel 4` finishes in roughly a
quarter of the wall-clock.

```bash
python bench/run_benchmark.py run --config scale40 --parallel 3
```

**What stays valid, and what does not.** Tokens, turns, cost and the pass/fail verdict are
properties of the agent session and are unaffected by what else the machine is doing.
`duration_ms` / `wall_s` is NOT: with N agents competing for CPU, disk and the same rate limit, every
run's clock inflates. The run header, the results table and the HTML report all say so when
`N > 1`. **Publish wall-clock numbers only from a `--parallel 1` run.**

**What is serialised anyway.** Two shared resources are taken under a keyed lock
(`bench/bench/locks.py`), so raising N is safe:

| resource | why it is exclusive | scope of the lock |
|---|---|---|
| a fixture's canonical worktree | path-pinned and reset in place before every aracne run — two at once would clobber each other's checkout | the whole aracne cell |
| a repo's `_repos` cache entry | concurrent cells from one repo would race to create/fetch it | the clone, inside setup |

So aracne cells of the SAME task serialise (this only bites with `seeds > 1`); everything
else overlaps freely. Baseline cells always work in their own ephemeral clone.

**Failure semantics are unchanged.** A timeout is still a verdict for its cell and the matrix
carries on. A hard error still stops the run — with cells in flight, "stop" means stop
*scheduling*: queued cells are dropped, the running ones are allowed to finish and be
recorded (their agent time is already spent), and the run is saved PENDING for `--continue`.
Rows land in `runs.jsonl` as they complete and are sorted back into matrix order at the end.

**Choosing N.** 2-4. The ceiling is not CPU, it's the plan rate limit — several headless
Claude Code sessions at once hit 429s and mid-session auth errors much sooner, and each of
those costs you a whole cell's spend.

### Outcomes: correct / fail / **timeout** / unknown

Every row in `runs.jsonl` carries an `outcome`. A run is `correct` or `fail` only when a
grading harness actually judged its patch. Anything that ran out of BUDGET is a
`timeout` — recorded, reported, and **excluded from the success-rate denominator**, because
a budget running out is not evidence the patch was wrong. Four caps can produce one:

| cap | flag | what it catches |
|---|---|---|
| agent | `--timeout-s` | the agent never finished the task |
| turns | `--max-turns` | the agent was still working when its turn cap cut it off |
| grading (total) | `--grade-timeout-s` | the Docker harness ran too long overall |
| grading (stall) | `--grade-stall-timeout-s` | the harness wrote **nothing** for that long — a wedged build, not a slow one |

`row["timeout_stage"]` (`agent` \| `turns` \| `grading`) says which fired. Rules that follow from this:

- A timeout never stops the matrix — it is a verdict for that cell, so the run carries on.
- A timed-out run is never sent to the grader (grading a half-written patch would convert a
  timeout into a fail).
- A grading timeout leaves `error` unset, so the agent's own token/turn metrics still count;
  only the verdict is missing.
- `--continue` keeps timed-out steps as-is; `--retry-timeouts` re-runs them (e.g. after
  raising `--timeout-s` or `--max-turns`).
- Killing the grading harness does not stop builds already running inside the Docker daemon;
  the run prints the surviving containers and the `docker rm -f` line to clear them.

## Methodology notes & caveats

- **A grading-side loss is recoverable.** A verdict lost to a low stall cap or an uninstalled
  harness costs a `regrade` (Docker only), not a re-run: the patches are already on disk.
  An AGENT timeout is not eligible — that patch was never finished, and grading it would
  manufacture a `fail` out of a run that merely ran out of clock.
- **Read `effective_n` before any percentage.** A run over 6 repositories cannot settle a
  10% question no matter how many seeds it holds.
- **Tokens move in opposite directions:** aracne should cut *input* tokens (targeted reads
  vs. whole-file dumps) but may add them (CLAUDE.md contract per call, `# CONTEXT:` blocks,
  extra tool round-trips). The net scales with repo size — repo-scale tasks are the point.
- **Prompt caching** distorts token/latency; cache tokens are captured separately. Disable
  caching for clean comparisons if you can.
- **The agent runs on the host; grading runs in Docker.** The agent can't run the real test
  suite while solving — but that's identical for both arms, so it doesn't bias the A/B.
- **Timeouts are not failures.** They are counted separately and dropped from the success
  denominator (see *Outcomes* above). A run with many timeouts is a run to re-do with bigger
  caps, not a run where the agent scored badly — read `n_timeout` before `success_rate`.
- **Rate limits:** Max 5x throttles on long matrices. Keep defaults small and use `--resume`
  across sessions. No GPU is ever needed.
- **Grading schema:** the Multi-SWE-bench grading config + report layout vary by version and
  are isolated in `bench/grade.py` (`_grade_multi` / `_parse_multi_reports`) — confirm them
  against the official repo for your installed version. Until then, `--no-grade` gives you
  full token/turn/speed numbers immediately.

## Layout

```
bench/
  run_benchmark.py     CLI entry: sample | prepare | generate | freeze | run subcommands
  test_timeouts.py     regression tests for the timeout rule (python3 bench/test_timeouts.py)
  test_chunkgen.py     regression tests for the chunked generation loop (python3 bench/test_chunkgen.py)
  test_paired.py       regression tests for context tokens + the paired analysis
  test_graderepos.py   regression tests for the Multi-SWE-bench clone cache
  samples/             task manifests (<sample_id>.jsonl)
  fixtures/            warm fixtures: <key>/{worktree,snapshot,meta.json} + _repos cache
                       + _mswe_repos: the clones the Multi-SWE-bench harness grades from
                       (seeded from _repos, shared by every run and arm — never per-run)
  bench/
    chunkgen.py        the description-generation loop: chunk -> workers -> re-read DB -> repeat
    sources.py         load Multi-SWE-bench + SWE-bench, normalize to Task, sample, manifest I/O
    gitutil.py         shared git helpers (run_git, ensure_repo_cache, clone_at)
    fixtures.py        warm-fixture lifecycle (scaffold/freeze/restore + coverage + kind scope)
    agents.py          backend dispatch: run_agent(harness, ...) -> RunResult
    claude_driver.py   run `claude --print --output-format json`, parse usage/turns/time
    opencode_driver.py run `opencode run --format json`, best-effort usage parse
    arms.py            per-arm workdir: baseline ephemeral clone vs aracne warm worktree
    runner.py          per (task,arm,seed): prepare workdir → drive → diff → row
    outcome.py         the correct / fail / timeout / unknown rule for a result row
    rowmetrics.py      row-level accessors; defines CONTEXT tokens = input + cache
    paired.py          matched-pair analysis: McNemar, ratio CIs, repo-clustered bootstrap
    grade.py           predictions + official Docker graders (under total + stall clocks)
    metrics.py         pooled descriptives per (language,arm) + prep summary; attaches `paired`
    report.py          results.json + summary.csv + console table (incl. the t/o column)
```

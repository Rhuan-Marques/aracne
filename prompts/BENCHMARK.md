# Aracne Benchmark — Summary & Handbook

A self-contained A/B benchmark that measures **what aracne's topology tools actually buy an
LLM agent** on real, test-graded software tasks, across all of aracne's languages. Everything
lives under `bench/`.

---

## 1. What it measures

For each task, the **same model** solves it **twice**, identical in every way except the toolset:

- **baseline** — vanilla agent with native file tools (read/grep/edit/bash).
- **aracne** — same agent, but with aracne's MCP topology tools + the injected `CLAUDE.md`
  contract + the guard hook (i.e. exactly what ships).

The reported signal is the **delta** between the two arms on:

- **Solve rate / consistency** — pass/fail graded by each repo's own test suite (in Docker), over N seeds.
- **Tokens** — input vs. output vs. cache, captured per run.
- **Speed** — agent turns (robust) and wall-clock (secondary).

Because both arms run the same task on the same model, task difficulty and training
contamination cancel in the delta. The **aracne arm is "warm"**: it uses a topology that has
been **scanned + described** (the real product), built once per repo and reused.

---

## 2. Architecture — prepare-once / run-many

The expensive warm-up (clone + scan + descriptions) is done **once per `repo@commit`** and
snapshotted, then restored cheaply before every run. Five phases (subcommands of
`bench/run_benchmark.py`):

| Phase | Command | What it does |
|-------|---------|--------------|
| 1. sample   | `sample`   | Pull tasks from the external benchmarks, oversample candidates → `bench/samples/<id>.candidates.jsonl`. |
| 2. prepare  | `prepare`  | For each unique repo@commit: clone a **canonical worktree**, `arac init --claude --opencode`, `arac scan`; **drop oversized repos**; write the final manifest `bench/samples/<id>.jsonl`. |
| 3. generate | `gen_descriptions.py` (preferred) | Fill descriptions into each fixture's topology DB. **Use `bench/gen_descriptions.py`** (see §6). |
| 4. freeze   | `freeze`   | Snapshot each warm worktree's aracne artifacts + report description coverage. |
| 5. run      | `run`      | Execute the baseline-vs-aracne matrix over the manifest + warm fixtures; grade in Docker; emit results. |

A **fixture** = `bench/fixtures/<org>__<repo>@<commit>/` containing `worktree/` (the canonical,
path-pinned checkout with `.aracne/topology.db`), `snapshot/` (the frozen aracne artifacts),
and `meta.json`. Fixtures are reused in place (the DB may embed absolute paths), never relocated.

---

## 3. Repo layout (`bench/`)

```
bench/
  run_benchmark.py     CLI: sample | prepare | generate | freeze | run
  gen_descriptions.py  RELIABLE description generator (chunked-direct, headless) — see §6
  config.example.yaml  all tunable defaults
  requirements.txt     datasets, huggingface_hub, PyYAML (+ optional swebench / multi-swe-bench)
  README.md            usage reference
  samples/             task manifests (<id>.candidates.jsonl, <id>.jsonl)
  fixtures/            warm fixtures: <key>/{worktree,snapshot,meta.json} + _repos cache
  bench/
    sources.py         load Multi-SWE-bench + SWE-bench Lite, normalize to Task, sample, manifest I/O
    gitutil.py         shared git helpers (run_git, ensure_repo_cache, clone_at)
    fixtures.py        fixture lifecycle (scaffold/freeze/restore) + description coverage + kind scope
    agents.py          backend dispatch: run_agent(harness, ...) -> RunResult
    claude_driver.py   `claude --print --output-format json`; parse usage/turns/time (run_raw / run_claude)
    opencode_driver.py `opencode run --format json`; best-effort usage parse
    arms.py            per-arm workdir: baseline ephemeral clone vs aracne warm worktree
    runner.py          per (task,arm,seed): prepare workdir -> drive agent -> git diff -> row
    grade.py           write predictions + invoke official Docker graders (per source)
    metrics.py         aggregate per (language,arm) + aracne-vs-baseline deltas (Wilson CI)
    report.py          results.json + summary.csv + console table
```

---

## 4. Task sources (multi-language)

External, test-graded GitHub issues, normalized into one `Task`:

- **Multi-SWE-bench** (`ByteDance-Seed/Multi-SWE-bench`) → **Go, JavaScript, TypeScript, Rust**.
  The dataset exposes only a `default` config; language is a top-level **directory**
  (`go/`, `js/`, `ts/`, `rust/`, …), so `sources.py` downloads `<dir>/*.jsonl` directly via
  `huggingface_hub` (not via an HF builder config). Fields: `org/repo/number/base/title/body/
  fix_patch/test_patch/f2p_tests/p2p_tests`.
- **SWE-bench Lite** (`princeton-nlp/SWE-bench_Lite`) → **Python** (Multi-SWE-bench has no Python).
  Classic fields: `instance_id/repo/base_commit/problem_statement/patch/test_patch/FAIL_TO_PASS/PASS_TO_PASS`.

`--max-nodes` in `prepare` drops repos with too many describable nodes (keeps diversity, not
just the smallest), so generation/run cost stays bounded.

---

## 5. Backends & models (Claude Code or OpenCode)

Both the **run** and **generation** can use either harness, any model — selected per invocation,
the **same for both A/B arms** (fairness):

- `claude_code` → `claude --print --output-format json --model <alias>` (e.g. `haiku`, `sonnet`).
  Runs on a Claude Max plan ($0 API) and returns full token usage.
- `opencode` → `opencode run --format json --model <provider/model>` (e.g.
  `deepseek/deepseek-v4-flash`, `openai/gpt-5.4-mini-fast`). Runs off the Max plan on a
  provider key; token usage parsed best-effort.

Config knobs: `run_harness` + `model` (the A/B run), `gen_harness` + `gen_model` (generation),
`coverage_min` (warm-enough guardrail for the aracne arm), `samples`/`seeds`/`max_turns`,
`max_describable_nodes`/`oversample`. See `bench/config.example.yaml`.

---

## 6. Description generation — use `gen_descriptions.py`

Descriptions are what make the aracne arm meaningful (rich `# CONTEXT:` blocks). They live only
in `.aracne/topology.db` (`resources.description`); generation only fills **undocumented** nodes,
so it is fully **resumable**.

**Use `bench/gen_descriptions.py`** — the chunked-direct generator. It lists undocumented ids
from the topology DB, splits them into small id-chunks, and runs several `claude --print` workers
that each `read` + `update_description` their own ids directly (no Claude Code sub-agents),
through a temporary `--tool-profile all` MCP config. This is the path that reliably **persists**
descriptions headlessly and in parallel.

```bash
# Warm go+python+js+ts fixtures to 97% coverage (haiku, 5 workers):
python bench/gen_descriptions.py

# Just one language / a bounded ~8-min burst / a different model:
python bench/gen_descriptions.py --languages python
python bench/gen_descriptions.py --time-budget 470
python bench/gen_descriptions.py --model sonnet --parallel 8 --chunk 20
```

It is idempotent (skips fixtures already at `--target`, only describes still-undocumented nodes),
so it can be run repeatedly / in bursts and simply continues. Throughput is ~80–90 ids/min at
5-way parallelism on haiku; small/medium fixtures finish in ~10–20 min, the largest repos
(django/sympy, tens of thousands of nodes) take hours.

> The harness also has a `generate` subcommand that drives the `/descriptions-generate` slash
> command, but that fans out to sub-agents which do not reliably persist headlessly on large
> repos. **Prefer `gen_descriptions.py`.** Folding the chunked-direct logic into the `generate`
> subcommand is a recommended follow-up (see §9).

Coverage is measured over the **describable kinds** (function/method/struct/interface) and is
always reconstructable from the DBs (see §8), so progress is never lost even if a run is interrupted.

---

## 7. How to run — full pipeline

**Prerequisites**

1. `claude` (Claude Code) installed and logged in to your Max plan, on `PATH`.
   Optional: `opencode` on `PATH` with a provider key for off-Max runs.
2. `arac` on `PATH`: `go build -o bin/arac . && export PATH=$PWD/bin:$PATH`.
3. Python venv: `bench/.venv` with `pip install -r bench/requirements.txt`.
4. Docker (only for the grading step). Optional graders: `pip install swebench multi-swe-bench`.

**Pipeline**

```bash
# 1. Pick tasks (oversample candidates; --samples is the FINAL per-language target)
python bench/run_benchmark.py sample --samples 2 --oversample 3 --languages go,python,javascript,typescript

# 2. Clone + scan candidates, drop oversized repos, write the final manifest
python bench/run_benchmark.py prepare --max-nodes 1600

# 3. Generate descriptions (the warm topology) — RELIABLE generator
python bench/gen_descriptions.py --languages go,python,javascript,typescript

# 4. Snapshot warm fixtures + verify coverage
python bench/run_benchmark.py freeze

# 5. Run the A/B matrix (same harness+model for both arms)
python bench/run_benchmark.py run --run-harness claude_code --model haiku --seeds 1
#   off-Max alternative: --run-harness opencode --model deepseek/deepseek-v4-flash
#   smoke:  ... run --arms baseline --seeds 1 --no-grade
#   preview/resume: ... run --dry-run   |   ... run --resume
```

Outputs land in `bench/results/<sample_id>/`: `runs.jsonl` (one row per run, resumable),
`patches/*.patch`, `predictions_*.jsonl` + grader reports, `results.json` (per-language + overall
deltas, Wilson CIs), `summary.csv`, and a console table.

---

## 8. Current state

52 fixtures prepared; warm coverage by language (describable kinds):

| Language | Fixtures | Warmed (≥97%) | Coverage |
|----------|----------|---------------|----------|
| go         | 5  | 3 | **87%** |
| typescript | 3  | 0 | 37% |
| python     | 15 | 1 | 31% |
| rust       | 5  | 1 | 15% |
| javascript | 24 | 1 | 1% |
| **total**  | 52 | 6 | 25% |

Go is effectively done. Python/TS/JS fixtures are prepared and partially warmed; running
`gen_descriptions.py` continues them to target. (Many JS fixtures are large `mui/material-ui`
repos that were scanned during candidate measurement but pruned from the manifest — only the
manifest fixtures need warming.)

The end-to-end machinery (sample → prepare → generate → freeze → run plumbing, fixtures,
metrics, reporting) is built and verified on synthetic data; what remains is warming the
fixtures and running the real A/B (below).

---

## 9. What's still needed

- **Warm the remaining fixtures** to `coverage_min` with `python bench/gen_descriptions.py`
  (resumable; run in bursts or to completion). Then `freeze`.
- **Fold the chunked-direct logic into the `generate` subcommand** so the in-harness generator
  is reliable headlessly (currently it uses the sub-agent slash-command path; `gen_descriptions.py`
  is the working reference implementation).
- **Rust:** the aracne Rust scanner is still in progress, so Rust description writes don't yet
  persist — keep Rust **excluded** from generation until the scanner lands, then re-warm.
- **Pin the grading invocations** in `bench/grade.py`: the Multi-SWE-bench Docker harness
  (`python -m multi_swe_bench.harness.run_evaluation --config ...`, predictions keyed by
  `org/repo/number/fix_patch`) for go/js/ts/rust, and the SWE-bench harness for python. Confirm
  the exact module/config for the installed versions.
- **Run the actual A/B** (`run` phase) once fixtures are warm; start with a small smoke
  (one language, `--no-grade`) then a graded matrix.
- **OpenCode arm:** confirm the real `opencode run --format json` event schema and tighten
  `opencode_driver._extract_usage` (token capture is best-effort there).
- **Prompt caching:** disable it (or account for cache tokens) for clean token comparisons.

---

## 10. Quick checks

```bash
# Reconstruct coverage from disk at any time (source of truth = the topology DBs):
bench/.venv/bin/python3 - <<'PY'
import sys,sqlite3,glob; sys.path.insert(0,'bench'); from bench import fixtures
K=['function','method','struct','interface']
def lg(db):
  c=sqlite3.connect(f'file:{db}?mode=ro',uri=True);r=c.execute('SELECT language,COUNT(*) FROM resources GROUP BY language ORDER BY 2 DESC LIMIT 1').fetchone();c.close();return r[0] if r else '?'
ag={}
for db in glob.glob('bench/fixtures/*/worktree/.aracne/topology.db'):
  l=lg(db); d,t,p=fixtures.description_coverage(db,K)
  if t: a=ag.setdefault(l,[0,0,0]); a[0]+=d; a[1]+=t; a[2]+=1
for l,(d,t,n) in sorted(ag.items()): print(f'{l:<11} {n:>2} fixtures  {d}/{t} = {d/t:.0%}')
PY

# Dry-run the task matrix without side effects:
python bench/run_benchmark.py sample --samples 2 --languages go
```

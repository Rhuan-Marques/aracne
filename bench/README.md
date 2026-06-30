# aracne benchmark harness

A paired **A/B benchmark** that measures what aracne actually buys an agent. For each
task it runs the same model twice — once with **native** file tools (`baseline`) and once
with **aracne's** topology tools + CLAUDE.md contract + guard (`aracne`) — on real,
test-graded GitHub issues, then reports the difference in:

- **Solve rate / consistency** — graded pass/fail by each repo's own test suite, over N seeds.
- **Tokens** — input vs. output vs. cache, captured per run.
- **Speed** — agent turns (robust) and wall-clock (secondary).

## Why this design

- **External, multi-language tasks** so aracne's own (Go-heavy) repo isn't the yardstick,
  and so the model has no inside knowledge of the codebase it's navigating.
  - Go / JavaScript / TypeScript / Rust → **Multi-SWE-bench** (`ByteDance-Seed/Multi-SWE-bench`)
  - Python → **SWE-bench Lite** (`princeton-nlp/SWE-bench_Lite`) — Multi-SWE-bench has no Python.
- **Pluggable backend** — generation AND the run use **Claude Code** (`claude --print
  --output-format json`, on your Max plan, $0 API) or **OpenCode** (`opencode run --format
  json`, off the Max plan with a provider key), with any model. Claude Code returns full
  token usage; OpenCode usage is parsed best-effort.
- **Cheap by default** — haiku generation + haiku runs + small, size-bounded repos, because
  describing a whole large repo on a strong model burns a 5-hour Max window on one repo.
- **Paired A/B, same harness + model both arms.** Only the toolset differs, so the *delta*
  is the signal — and shared training contamination cancels in the delta.
- **Grading is the repo's test suite in Docker**, never aracne or the agent self-reporting.

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

# 4. Snapshot the warm artifacts and verify description coverage:
python bench/run_benchmark.py freeze

# 5. Run the A/B matrix (any harness/model, SAME for both arms):
python bench/run_benchmark.py run --run-harness claude_code --model haiku --seeds 1
python bench/run_benchmark.py run --run-harness opencode --model openai/gpt-5.4-mini-fast --seeds 1
#    smoke:  ...run --arms baseline --seeds 1 --no-grade   |   preview: --dry-run   |   resume: --resume
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
| `sample`  | `--samples N` · `--oversample F` · `--languages go,...` · `--sample-seed S` · `--sample-id ID` · `--config` |
| `prepare` | `--sample-id ID` · `--samples N` · `--max-nodes N` · `--gen-kinds k,k` · `--arac-bin PATH` · `--force-prepare` · `--config` |
| `generate`| `--sample-id ID` · `--languages go,...` · `--gen-harness H` · `--gen-model M` · `--gen-kinds k,k` · `--gen-max-turns T` · `--gen-timeout-s S` · `--gen-parallel N` · `--coverage-min F` · `--regenerate` · `--arac-bin PATH` · `--config` |
| `freeze`  | `--sample-id ID` · `--coverage-min F` · `--config` |
| `run`     | `--sample-id ID` · `--seeds K` · `--arms baseline,aracne` · `--run-harness H` · `--model M` · `--max-turns T` · `--timeout-s S` · `--out DIR` · `--resume` · `--no-grade` · `--dry-run` · `--allow-cold` · `--coverage-min F` · `--keep-workdir` · `--config` |

| Flag | Meaning | Default |
|------|---------|---------|
| `--samples N` | FINAL tasks per language | 2 |
| `--oversample F` | candidate multiplier (pruned in `prepare`) | 3 |
| `--max-nodes N` | drop candidate repos bigger than this (describable nodes) | 600 |
| `--languages` | `go,javascript,typescript,rust,python` | all five |
| `--seeds K` | repetitions per task per arm | 1 |
| `--gen-harness H` / `--run-harness H` | `claude_code` \| `opencode` | `claude_code` |
| `--gen-model M` | generation orchestrator model (executors haiku) | `haiku` |
| `--gen-kinds k,k` | kinds to describe | `function,method,struct,interface` |
| `--model M` | run model for **both** arms (opencode → `provider/model`) | `haiku` |
| `--max-turns T` | per-run turn cap | 30 |
| `--coverage-min F` | min description coverage for the aracne arm | 0.5 |
| `--allow-cold` | bypass the coverage guardrail | off |
| `--regenerate` | re-generate covered fixtures too | off |
| `--resume` / `--dry-run` / `--no-grade` | run controls | off |

## Output

Written under `--out` (default `bench/results/<sample_id>/`):

- `runs.jsonl` — one row per run (appended live → crash-safe and `--resume`-able).
- `patches/*.patch` — the agent's diff per run.
- `preds_*.jsonl`, `grading_*` — predictions + raw grader reports.
- `results.json` — full per-language + overall aggregate with **aracne−baseline deltas**.
- `summary.csv` — the same, flattened.

…plus a console table:

```
[go]
  arm         solved      in     out  turns  wall_s       $
  baseline       3/5   48.1k    6.2k   14.3      71       0
  aracne         4/5   29.7k    6.8k   11.1      58       0
  Δ            +20pp    -38%    +10%   -22%    -18%
```

## Methodology notes & caveats

- **Tokens move in opposite directions:** aracne should cut *input* tokens (targeted reads
  vs. whole-file dumps) but may add them (CLAUDE.md contract per call, `# CONTEXT:` blocks,
  extra tool round-trips). The net scales with repo size — repo-scale tasks are the point.
- **Prompt caching** distorts token/latency; cache tokens are captured separately. Disable
  caching for clean comparisons if you can.
- **The agent runs on the host; grading runs in Docker.** The agent can't run the real test
  suite while solving — but that's identical for both arms, so it doesn't bias the A/B.
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
  samples/             task manifests (<sample_id>.jsonl)
  fixtures/            warm fixtures: <key>/{worktree,snapshot,meta.json} + _repos cache
  bench/
    sources.py         load Multi-SWE-bench + SWE-bench, normalize to Task, sample, manifest I/O
    gitutil.py         shared git helpers (run_git, ensure_repo_cache, clone_at)
    fixtures.py        warm-fixture lifecycle (scaffold/freeze/restore + coverage + kind scope)
    agents.py          backend dispatch: run_agent(harness, ...) -> RunResult
    claude_driver.py   run `claude --print --output-format json`, parse usage/turns/time
    opencode_driver.py run `opencode run --format json`, best-effort usage parse
    arms.py            per-arm workdir: baseline ephemeral clone vs aracne warm worktree
    runner.py          per (task,arm,seed): prepare workdir → drive → diff → row
    grade.py           predictions + official Docker graders, per source
    metrics.py         aggregate per (language,arm) + deltas (Wilson CI) + prep summary
    report.py          results.json + summary.csv + console table
```

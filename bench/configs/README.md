# bench/configs

Named, reusable benchmark configs. Select one when running:

```bash
python bench/run_benchmark.py run --config atlas-smoke
python bench/run_benchmark.py run --config atlas-smoke --run-name my-experiment
```

`--config` accepts either a **bare name** (resolved to `bench/configs/<name>.yaml`) or a
**path** to any YAML file. Each config is deep-merged over the built-in `DEFAULTS`, and CLI
flags override the config — so different configs (different `sample_id`, `model`, `arms`,
`max_turns`, …) produce different runs.

## Two config axes

- **This folder** (`bench/configs/*.yaml`, `--config`) configures the **benchmark harness**
  (model, arms, samples, seeds, …).
- **`bench/configs/aracne/*.json`** (`aracne_config` field / `--aracne-config`) is the
  **`.aracne/config.json`** applied to the *aracne arm* (which MCP tools it gets, guard
  blocking, context-filter verbosity, read-scan). Point a run YAML at one via its
  `aracne_config` field, e.g. `aracne_config: strict-tools`. See `aracne/README.md`.

Each run writes a self-contained `bench/results/<run_name>/` directory:

| file | what |
|------|------|
| `report.html` | human-readable report: baseline vs aracne vs Δ table, bars, embedded analysis |
| `analysis.md` | LLM-written narrative of the results (skip with `--no-analysis`) |
| `results.json` / `summary.csv` | full aggregate + flat rows |
| `runs.jsonl` | one row per run (live-appended, resume-able) |
| `run_meta.json` | run name, sample, timestamp, model, arms, harness, CLI args |
| `manifest.jsonl` | copy of the task manifest that was run |
| `config.used.yaml` | copy of the benchmark config file that was used (if any) |
| `aracne_config.used.json` | copy of the aracne-arm config overlay that was used (if any) |

If `--run-name` is omitted a sortable random id (`run-<timestamp>-<hex>`) is generated.

## Incremental runs & `--continue`

Each step's result is written to `runs.jsonl` the moment it finishes, so nothing is lost on a
crash. If a step hits a **machinery error** (agent error, timeout, runner crash — not just an
unsolved task), the run stops and is marked `pending` in `run_meta.json`; grading and the report
are produced only once a run completes cleanly.

Continue a pending (or any) run by id/name — it re-runs the errored + not-yet-run steps and leaves
the finished ones alone (errored steps restart from scratch), reusing that run's config snapshot
(`config.used.yaml` + `aracne_config.used.json`) so the continuation is faithful:

```bash
python bench/run_benchmark.py run --continue <run_name>
```

`--continue` ignores `--config` / `--aracne-config` (the saved snapshot wins). The older
`--resume` (skip every combo already in `runs.jsonl`, including errored ones, using the current
flags) still works.

## SWE-Atlas: the navigation variant

Every SWE-Atlas run is a **modified** version of SWE-Atlas Refactoring, and says so: `run_meta.json`
and the report carry `benchmark_variant` and `grading`. What changes, and why:

| | published benchmark | this harness |
|---|---|---|
| task prompt | issue text **plus an interface specification** (paths, names, signatures of the code the gold patch adds or re-signs) | issue text only — the specification is stripped at manifest load (`bench/bench/atlas_prompt.py`). It hands the agent the navigation answer this benchmark exists to measure. The sentence "I've already taken care of all changes to the test files" is removed too (the instruction not to modify tests stays): the agent's workspace holds the *base* tests, so the claim sends it looking there for an API that is not in them |
| file names | the issue text names files and directories (53 of 70 prompts; a median 30% of the files the gold patch edits) | **none**: each prompt with a file or directory reference is rewritten once by a model (`atlas_rewrite_prompts.py`, Haiku, batched), checked deterministically (no path, no extension-less file name, every symbol kept, nothing added) and stored in `configs/atlas/rf_prompts_no_paths.jsonl`, keyed to the hash of its source text. A prompt that still names files and has no matching rewrite stops the run |
| hidden tests | part of the reward | **not run**: they call the new code by the names the stripped specification gave away |
| rubric judge | the task's `evaluate_rubrics.py`, in the task container | the **same script**, prompts and aggregation, run on the host (`atlas_rubric.py`), plus one disclosed clarification: identifiers a rubric item names for *new or renamed* code are illustrative. `atlas_naming_lenient: false` grades strictly |
| localization | — | **deterministic**, always computed (`bench/bench/localization.py`): recall/precision of the files and declarations that exist at base and the gold patch modifies, mapped through the frozen base topology; plus API calls and tokens spent before the first edit of a gold file |

Grading modes (`atlas_grading` in a run config):

- `rubric` *(default)* — localization + rubric judge. `success` = every must-have rubric item passes.
- `verifier` — the published verifier (hidden tests + rubric) in the task container, plus localization.
- `--cheap-grade` (on `run` or `regrade`) — localization only: no model calls, no containers, no
  tokens. `success` stays unknown. `regrade <run> --cheap-grade` adds localization to any finished run.

A run from before the variant existed is labelled `SWE-Atlas Refactoring AS PUBLISHED` when regraded:
its agents saw the specification, and the label describes what the agents were given.

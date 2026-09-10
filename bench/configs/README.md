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

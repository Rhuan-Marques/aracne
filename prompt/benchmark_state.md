# Benchmark State — Snapshot

_Snapshot: 2026-07-01. Source of truth: live query of every `bench/fixtures/*/worktree/.aracne/topology.db`, cross-checked against `bench/report_20260701T050321Z.json` (that report is stale — it was written mid-run when generation was rate-limited)._

## What the benchmark is

A **paired A/B harness** (`bench/run_benchmark.py`, phases `sample → prepare → generate → freeze → run`) that runs the same model twice on real, test-graded GitHub issues:

- **baseline** — native file tools.
- **aracne** — topology tools + CLAUDE.md contract + guard.

The measured delta = solve rate, tokens, and turns. Tasks come from **Multi-SWE-bench** (Go/JS/TS/Rust) and **SWE-bench Lite** (Python). The aracne arm is **warm-only**: each fixture's topology must be pre-described past `coverage_min` (default **0.50**) or the `run` step refuses it (unless `--allow-cold`). So "description completeness" below is the gating readiness metric.

Coverage counts describable kinds only: **function, method, struct, interface** (`file` is intentionally dropped).

## Manifest benchmark set (the 15 official tasks)

From `bench/samples/default.jsonl` (5 Go) + `bench/samples/multi.jsonl` (3 JS · 3 TS · 3 Rust · 1 Python).

| Lang | Fixture (repo@commit) | Described / Total | Coverage | Ready |
|------|-----------------------|-------------------|----------|-------|
| go | cli__cli@0b9b1f710f73 | 2607 / 2607 | 100% | ✅ |
| go | cli__cli@8dbd07212c23 | 2099 / 2099 | 100% | ✅ |
| go | cli__cli@ebcf3a10225a | 2972 / 2972 | 100% | ✅ |
| go | cli__cli@f17d9672f50c | 735 / 735 | 100% | ✅ |
| go | cli__cli@fbe1487dd065 | 1501 / 1501 | 100% | ✅ |
| javascript | anuraghazra__github-readme-stats@112000667c01 | 111 / 111 | 100% | ✅ |
| javascript | sveltejs__svelte@37f249350c41 | 1622 / 1623 | 99.9% | ✅ |
| javascript | sveltejs__svelte@434a58711f7e | 1954 / 1956 | 99.9% | ✅ |
| typescript | vuejs__core@3be4e3cbe34b | 1413 / 1413 | 100% | ✅ |
| typescript | vuejs__core@68e5cc6ac8fc | 1480 / 1483 | 99.8% | ✅ |
| typescript | vuejs__core@a9893458ec51 | 1434 / 1434 | 100% | ✅ |
| rust | clap-rs__clap@135b15467eda | 1046 / 1046 | 100% | ✅ |
| rust | clap-rs__clap@6a56a82629d1 | 809 / 809 | 100% | ✅ |
| **rust** | **BurntSushi__ripgrep@041544853c86** | **0 / 1544** | **0%** | ❌ **blocker** |
| python | pallets__flask@4c288bc97ea3 | 467 / 467 | 100% | ✅ |

### Per-language rollup (manifest only)

| Lang | Fixtures | Coverage | Status |
|------|----------|----------|--------|
| Go | 5 | 100% | Ready to run |
| JavaScript | 3 | 99.9% | Ready to run |
| TypeScript | 3 | 99.9% | Ready to run |
| Python | 1 | 100% | Ready to run |
| Rust | 3 | 61.6% avg (2×100%, 1×0%) | **1 fixture below guardrail** |

**Bottom line: 14 of 15 manifest fixtures are fully described and run-ready. The single blocker is `ripgrep` (Rust) at 0%.**

## Non-manifest fixtures (candidates on disk, NOT in the run set)

`prepare`'s oversampling left many extra fixtures on disk that were pruned from the final manifest. They are only touched with `--all-fixtures` and do **not** gate a run, but they consume disk/time:

- **JavaScript** — `mui/material-ui` ×12 (mostly 0%, one at 19%), extra `svelte` ×9 (0–1%).
- **Python** — `django` ×5 (~27%), `sympy` ×4 (~28–38%), `scikit-learn` ×2 (~59%), `matplotlib` ×2 (~50%), `pytest` (33%), `flask` also here at 100%.
- **Rust** — `tokio` (0%), `tracing` (26%).

These are partial because generation targets manifest fixtures first and the default `--languages` list omits Rust.

## Known quirks

- **`gen_descriptions.py` manifest filter has a Python blind spot.** `manifest_keys()` requires `raw.org AND raw.repo AND base_commit`, but the SWE-bench Python task (flask) has `org=None` / `repo="pallets/flask"`, so it is **not** recognized as an in-scope manifest key. Flask is 100% anyway, but any *future* Python manifest fixture would be silently skipped by `--only-manifest`.
- **Rust is omitted from default generation** (`gen_descriptions.py --languages` defaults to `go,python,javascript,typescript`, "rust scanner WIP"). That is why `ripgrep` never got filled while `clap` did (clap must have been described via a targeted run).
- **Last generation run was rate-limited** (haiku on the Max plan) — see the `rate_limit` stop reason in the report. Current on-disk coverage is already well past what that report captured.

## NEXT STEPS

1. **Unblock Rust — describe `ripgrep` (1544 nodes, the only sub-guardrail manifest fixture).**
   `python bench/gen_descriptions.py --languages rust`
   To avoid burning the Max window on a rate-limit, run it **off-Max**: `--harness opencode --model deepseek/deepseek-chat`.
   _Alternative if the Rust scanner is deemed too WIP:_ drop `ripgrep` from `bench/samples/multi.jsonl` and run Rust with `clap` ×2 only.
2. **Freeze once Rust is green:** `python bench/run_benchmark.py freeze` to snapshot warm artifacts and assert coverage across all 15.
3. **Run what's already ready now** (don't wait on Rust):
   `python bench/run_benchmark.py run --languages go,javascript,typescript,python --model haiku --seeds 1`
   Smoke first with `--arms baseline --no-grade` to validate the pipeline cheaply.
4. **Fix the `manifest_keys()` Python blind spot** in `bench/gen_descriptions.py` (handle `org=None` / `repo="owner/name"`) so future SWE-bench Python fixtures aren't skipped by `--only-manifest`.
5. **Reclaim disk / cut warm-time:** prune the non-manifest candidate fixtures (mui ×12, extra svelte ×9, django/sympy/etc.) or keep them fenced behind `--all-fixtures`; `docker image prune` between languages when grading.
6. **When grading:** verify the Multi-SWE-bench grader schema in `bench/bench/grade.py` against your installed `multi-swe-bench` version before trusting solve rates; until then `--no-grade` still yields full token/turn numbers.

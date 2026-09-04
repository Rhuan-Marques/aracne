# One-off analysis scripts

Post-hoc scripts, each written to answer one question about one past run. They are
kept because the reasoning in their `WHY THIS EXISTS` docstrings is worth reading,
not because they are part of the harness.

**None of these is needed to run a benchmark.** The harness is `../../run_benchmark.py`
plus the `bench/bench/` package; see `../../README.md`.

Expect them to be pinned to a specific run, sample or ID scheme:

| Script | Written for |
|---|---|
| `backfill_answerkey.py` | a run already in flight when the answer key changed |
| `backfill_shellstats.py` | baseline rows imported from `fair-20260901a`, which carry no shell counters |
| `first_touch.py`, `select_hard_tasks.py` | the `linerange-20260902a` post-mortem |
| `replay_warnings.py` | the `compact-blocked-20260830c` run |
| `regen_oversized_descriptions.py` | hardcoded to the `scale40` sample |
| `migrate_descriptions.py` | the ID-scheme change, since completed |
| `census_repos.py`, `pick_fixtures.py`, `clean_descriptions.py` | fixture preparation |
| `navsize.py`, `ab_report_data.py`, `lr_report_data.py` | one-off report inputs |
| `finish_ab.sh` | rescoring a finished A/B run |

Run them from `bench/` with its venv active.

#!/usr/bin/env python3
"""Regression tests for per-seed grading (bench/bench/grade.py `_batches`).

THE BUG. Both grading harnesses key everything they produce by the INSTANCE and nothing else:
SWE-bench writes logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json, and
Multi-SWE-bench keys its reports by "org/repo:pr-N". A 3-seed run put all three seeds of an
instance in one harness call, so three identical prediction rows produced ONE verdict, and
`_grade_batch` then copied it onto all three rows. The run reported three graded patches while
one had been run.

It was visible in the data: across 54 instance x arm cells of the opus-medium-seeds3 run,
every single cell was 0/3 or 3/3 and never 1/3 or 2/3 — while the patches genuinely differed
(two flask seeds matched the passing baseline, the third used a different parameter name and
really did fail; all three were recorded as failures).

The fix is one harness call per seed, so each patch gets its own id-space and its own report.
These tests assert the batching, the per-seed harness identity, and the 1:1 verdict mapping.

No pytest, no network, no Docker:

    python3 bench/test_seedgrading.py
"""
from __future__ import annotations

import sys
import tempfile
import types
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import grade  # noqa: E402

_RESULTS: list[tuple[str, bool]] = []


def check(name: str, cond) -> None:
    _RESULTS.append((name, bool(cond)))
    print(("PASS  " if cond else "FAIL  ") + name)


def fake_task(key: str, org: str = "pallets", repo: str = "flask", number: int = 4992):
    return types.SimpleNamespace(
        key=key,
        source="swe_bench",
        base_commit="0" * 40,
        raw={"org": org, "repo": repo, "number": number},
        repo_slug=lambda: f"{org}__{repo}",
    )


def items_for(source: str, key: str, seeds, arm: str = "aracne", patches=None):
    """One (row, task, arm, patch) per seed, the shape grade_all is handed."""
    task = fake_task(key)
    task.source = source
    out = []
    for i, seed in enumerate(seeds):
        patch = patches[i] if patches else f"--- patch seed {seed}\n"
        out.append(({"instance_id": key, "arm": arm, "seed": seed}, task, arm, patch))
    return out


# --------------------------------------------------------------------------- #

def test_three_seeds_grade_in_three_batches():
    for source in ("swe_bench", "multi_swe_bench"):
        batches = grade._batches(source, items_for(source, "pallets__flask-4992", [0, 1, 2]))
        check(f"{source}: three seeds produce three independent batches",
              len(batches) == 3)
        check(f"{source}: each batch holds exactly one seed's run",
              all(len(group) == 1 for _tag, group in batches))
        check(f"{source}: batch tags are unique (separate dirs, reports and run ids)",
              len({tag for tag, _ in batches}) == 3)


def test_single_seed_keeps_its_old_layout():
    """A 1-seed run must batch exactly as it did before seeds existed, so existing result
    directories stay readable and a regrade of an old run still finds its files."""
    swe = grade._batches("swe_bench", items_for("swe_bench", "pallets__flask-4992", [0]))
    check("swe_bench single seed is still one untagged batch",
          swe == [("", swe[0][1])] and swe[0][0] == "")

    multi = grade._batches(
        "multi_swe_bench",
        items_for("multi_swe_bench", "cli__cli-4136", [0], patches=["p"])
        + items_for("multi_swe_bench", "svelte-14456", [0], patches=["p"]),
    )
    check("multi_swe_bench single seed still batches by repo only, untagged by seed",
          all("__s" not in tag for tag, _ in multi))


def test_multi_splits_by_repo_and_seed():
    items = (items_for("multi_swe_bench", "cli__cli-4136", [0, 1])
             + items_for("multi_swe_bench", "svelte-14456", [0, 1]))
    # Both instances answer with the same fake org/repo, so distinguish by seed count only.
    batches = grade._batches("multi_swe_bench", items)
    check("multi_swe_bench separates seeds as well as repos", len(batches) == 2)
    check("every multi batch is single-seeded",
          all(len({r["seed"] for r, *_ in group}) == 1 for _tag, group in batches))


def test_missing_seed_field_is_tolerated():
    """Rows written before `seed` existed must still grade, as one batch."""
    rows = [({"instance_id": "k", "arm": "aracne"}, fake_task("k"), "aracne", "p")]
    check("a row with no seed field batches without raising",
          grade._batches("swe_bench", rows) == [("", rows)])


def test_each_seed_gets_its_own_harness_identity(tmp: Path):
    """The predictions file, run id and model name must differ per seed — they are what the
    harness derives <model>.<run_id>.json and the per-instance report directory from. Sharing
    them is precisely how three patches collapsed onto one verdict."""
    cfg = {"sources": {"swe_bench": {"model_name": "m", "max_workers": 1,
                                     "dataset": "SWE-bench/SWE-bench_Lite"}}}
    seen: list[list[str]] = []
    original = grade._run_harness
    grade._run_harness = lambda cmd, *a, **k: seen.append(cmd)
    try:
        for tag in ("s0", "s1", "s2"):
            try:
                grade._grade_swe([({"seed": 0}, fake_task("pallets__flask-4992"), "aracne", "d")],
                                 "aracne", cfg, tmp, tag)
            except Exception:  # noqa: BLE001 — no report file to read; the cmd is the assertion
                pass
    finally:
        grade._run_harness = original

    run_ids = [cmd[cmd.index("--run_id") + 1] for cmd in seen]
    preds = [cmd[cmd.index("--predictions_path") + 1] for cmd in seen]
    check("each seed runs under its own harness run id", len(set(run_ids)) == 3)
    check("each seed writes its own predictions file", len(set(preds)) == 3)


def test_one_verdict_per_row_not_per_instance(tmp: Path):
    """The end-to-end shape: two seeds of one instance, one passing and one failing, must end
    up with two DIFFERENT success values. Before the fix both took whichever the harness
    happened to grade."""
    items = items_for("swe_bench", "pallets__flask-4992", [0, 1],
                      patches=["good patch", "bad patch"])
    verdicts = {"s0": True, "s1": False}

    original = grade._grade_swe
    grade._grade_swe = lambda batch, arm, cfg, out_dir, tag="": {
        batch[0][1].key: verdicts[tag]
    }
    try:
        grade.grade_all(items, {"sources": {"swe_bench": {"model_name": "m"}}}, tmp)
    finally:
        grade._grade_swe = original

    successes = [row.get("success") for row, *_ in items]
    check("seeds of one instance receive independent verdicts", successes == [True, False])


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="aracne-bench-seeds-") as td:
        tmp = Path(td)
        test_three_seeds_grade_in_three_batches()
        test_single_seed_keeps_its_old_layout()
        test_multi_splits_by_repo_and_seed()
        test_missing_seed_field_is_tolerated()
        test_each_seed_gets_its_own_harness_identity(tmp)
        test_one_verdict_per_row_not_per_instance(tmp)

    failed = [name for name, passed in _RESULTS if not passed]
    print("\n" + "=" * 66)
    print(f"{len(_RESULTS) - len(failed)}/{len(_RESULTS)} passed")
    for name in failed:
        print(f"  FAILED: {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

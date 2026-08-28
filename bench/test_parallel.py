#!/usr/bin/env python3
"""Regression tests for parallel matrix execution (`run_parallel` / `--parallel`).

What must hold when cells run concurrently:
  - every cell still runs, and runs.jsonl ends up in MATRIX order (not finish order);
  - the aracne arm never enters one fixture's canonical worktree twice at a time;
  - a TIMEOUT still lets the matrix carry on;
  - a HARD error stops SCHEDULING (queued cells are dropped) while in-flight cells are
    still recorded — their agent time is already spent;
  - `--parallel 1` behaves exactly like the old sequential loop.

No agent, no Docker, no network: `runner.run_one` is stubbed with a sleeping fake.

    python3 bench/test_parallel.py
"""
from __future__ import annotations

import sys
import tempfile
import threading
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import outcome, runner  # noqa: E402
import run_benchmark as rb  # noqa: E402

_RESULTS: list[tuple[str, bool]] = []


def check(name: str, cond) -> None:
    _RESULTS.append((name, bool(cond)))
    print(("PASS  " if cond else "FAIL  ") + name)


class FakeTask:
    def __init__(self, n: int, repo: str | None = None):
        self.key = f"org__repo{n}-1"
        self.language = "go"
        self.source = "multi_swe_bench"
        self.base_commit = "0" * 40
        self.problem_statement = "fix it"
        self._repo = repo or f"repo{n}"
        self.raw = {"org": "org", "repo": self._repo, "number": n}

    def repo_slug(self) -> str:
        return f"org__{self._repo}"


def _matrix(tasks, arms=("baseline", "aracne"), seeds=1):
    return [(t, a, s) for t in tasks for a in arms for s in range(seeds)]


def _drive(matrix, fake_run_one, par, tmp: Path, done=None):
    """Run `_run_matrix` with a stubbed cell body; returns (rows, stopped_error)."""
    rows: list[dict] = []
    runs_path = tmp / f"runs-{time.monotonic_ns()}.jsonl"
    prev = runner.run_one
    runner.run_one = fake_run_one
    try:
        stopped = rb._run_matrix(matrix, done or set(), rows, runs_path,
                                 {"run_parallel": par}, tmp, tmp, tmp, tmp)
    finally:
        runner.run_one = prev
    rb._sort_rows(rows, matrix)
    return rows, stopped


def _ok_row(task, arm, seed, **kw):
    row = runner.error_row(task, arm, seed, None)
    row.update(num_turns=5, duration_ms=1000, cost_usd=0.01, has_patch=True)
    row.update(kw)
    return outcome.stamp(row)


# --------------------------------------------------------------------------- #
# 1. every cell runs; order on disk is matrix order, not finish order
# --------------------------------------------------------------------------- #
def test_all_cells_run(tmp: Path) -> None:
    matrix = _matrix([FakeTask(i) for i in range(4)])

    def fake(task, arm, seed, *_a, **_kw):
        # Later cells finish FIRST, so a scheduler that trusted completion order would
        # write runs.jsonl in the wrong sequence.
        time.sleep(0.05 * (len(matrix) - matrix.index((task, arm, seed))))
        return _ok_row(task, arm, seed), ""

    rows, stopped = _drive(matrix, fake, par=4, tmp=tmp)
    check("no error is reported for a clean matrix", stopped is None)
    check("every cell produced a row", len(rows) == len(matrix))
    expected = [f"{t.key}|{a}|{s}" for t, a, s in matrix]
    check("rows come back in matrix order", [r["run_key"] for r in rows] == expected)

    already = {expected[0], expected[1]}
    rows2, _ = _drive(matrix, fake, par=4, tmp=tmp, done=already)
    check("cells already done are skipped, not re-run",
          {r["run_key"] for r in rows2} == set(expected) - already)


# --------------------------------------------------------------------------- #
# 2. parallelism is real, and the fixture worktree is still exclusive
# --------------------------------------------------------------------------- #
def test_concurrency(tmp: Path) -> None:
    matrix = _matrix([FakeTask(i) for i in range(6)], arms=("baseline",))
    live, peak, lock = 0, 0, threading.Lock()

    def fake(task, arm, seed, *_a, **_kw):
        nonlocal live, peak
        with lock:
            live += 1
            peak = max(peak, live)
        time.sleep(0.2)
        with lock:
            live -= 1
        return _ok_row(task, arm, seed), ""

    t0 = time.monotonic()
    rows, _ = _drive(matrix, fake, par=3, tmp=tmp)
    dur = time.monotonic() - t0
    check("cells actually overlap", peak > 1)
    check("concurrency is capped at --parallel", peak <= 3)
    check("6 x 0.2s cells at 3-way finish in well under sequential time", dur < 0.9)
    check("all rows still recorded", len(rows) == 6)


def test_fixture_is_exclusive(tmp: Path) -> None:
    """Two aracne cells of the SAME fixture must never overlap: they reset and reuse one
    path-pinned worktree. Exercised through the real `runner.run_one` locking wrapper."""
    task = FakeTask(0)
    matrix = [(task, "aracne", 0), (task, "aracne", 1), (task, "aracne", 2)]
    live, overlaps, lock = 0, 0, threading.Lock()

    def fake_cell(task, arm, seed, *_a, **_kw):
        nonlocal live, overlaps
        with lock:
            live += 1
            if live > 1:
                overlaps += 1
        time.sleep(0.15)
        with lock:
            live -= 1
        return _ok_row(task, arm, seed), ""

    prev = runner._run_cell
    runner._run_cell = fake_cell
    try:
        rows, _ = _drive(matrix, runner.run_one, par=3, tmp=tmp)
    finally:
        runner._run_cell = prev
    check("aracne cells sharing a fixture never run concurrently", overlaps == 0)
    check("they all still run, just serialised", len(rows) == 3)

    # ... while baseline cells of the same task are free to overlap (own ephemeral clone).
    matrix_b = [(task, "baseline", s) for s in range(3)]
    live = overlaps = 0
    runner._run_cell = fake_cell
    try:
        _drive(matrix_b, runner.run_one, par=3, tmp=tmp)
    finally:
        runner._run_cell = prev
    check("baseline cells are not blocked by the fixture lock", overlaps > 0)


# --------------------------------------------------------------------------- #
# 3. stop semantics
# --------------------------------------------------------------------------- #
def test_timeout_does_not_stop(tmp: Path) -> None:
    matrix = _matrix([FakeTask(i) for i in range(3)], arms=("baseline",))

    def fake(task, arm, seed, *_a, **_kw):
        if task.key.endswith("repo0-1"):
            return _ok_row(task, arm, seed, timeout_stage=outcome.AGENT,
                           error="agent timeout after 1800s"), ""
        return _ok_row(task, arm, seed), ""

    rows, stopped = _drive(matrix, fake, par=3, tmp=tmp)
    check("a timeout does not stop the parallel matrix", stopped is None)
    check("every cell still ran", len(rows) == 3)


def test_hard_error_stops_scheduling(tmp: Path) -> None:
    """A hard error must stop the run — but only by refusing to START new cells."""
    matrix = _matrix([FakeTask(i) for i in range(12)], arms=("baseline",))
    started, lock = [], threading.Lock()

    def fake(task, arm, seed, *_a, **_kw):
        with lock:
            started.append(task.key)
        if task.key.endswith("repo1-1"):
            time.sleep(0.05)
            return _ok_row(task, arm, seed,
                           error="agent_error: 403 Unable to verify organization membership"), ""
        time.sleep(0.2)
        return _ok_row(task, arm, seed), ""

    rows, stopped = _drive(matrix, fake, par=2, tmp=tmp)
    check("the hard error is reported", stopped is not None and "403" in stopped[1])
    check("it names the failing cell", stopped[0] == f"{matrix[1][0].key}|baseline|0")
    check("queued cells are dropped, not run", len(started) < len(matrix))
    check("cells already in flight are still recorded",
          len(rows) == len(started) and len(rows) >= 2)
    check("recorded rows are the ones that started",
          {r["run_key"].split("|")[0] for r in rows} == set(started))


def test_sequential_default(tmp: Path) -> None:
    """--parallel 1 keeps the old contract: one at a time, stop on the first hard error."""
    matrix = _matrix([FakeTask(i) for i in range(5)], arms=("baseline",))
    live, peak, order, lock = 0, 0, [], threading.Lock()

    def fake(task, arm, seed, *_a, **_kw):
        nonlocal live, peak
        with lock:
            live += 1
            peak = max(peak, live)
            order.append(task.key)
        time.sleep(0.02)
        with lock:
            live -= 1
        if task.key.endswith("repo2-1"):
            return _ok_row(task, arm, seed, error="agent_error: boom"), ""
        return _ok_row(task, arm, seed), ""

    rows, stopped = _drive(matrix, fake, par=1, tmp=tmp)
    check("parallel=1 never overlaps", peak == 1)
    check("parallel=1 runs in matrix order", order == [t.key for t, _, _ in matrix][:len(order)])
    check("parallel=1 stops at the first hard error", stopped is not None and len(rows) == 3)


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="aracne-bench-parallel-") as td:
        tmp = Path(td)
        test_all_cells_run(tmp)
        test_concurrency(tmp)
        test_fixture_is_exclusive(tmp)
        test_timeout_does_not_stop(tmp)
        test_hard_error_stops_scheduling(tmp)
        test_sequential_default(tmp)

    failed = [name for name, passed in _RESULTS if not passed]
    print("\n" + "=" * 66)
    print(f"{len(_RESULTS) - len(failed)}/{len(_RESULTS)} passed")
    for name in failed:
        print(f"  FAILED: {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

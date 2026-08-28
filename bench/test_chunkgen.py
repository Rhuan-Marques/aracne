#!/usr/bin/env python3
"""Regression tests for the chunked description driver (bench/bench/chunkgen.py).

The rule under test: PYTHON owns the generation loop. A fixture is not abandoned because a
worker quit mid-wave — chunkgen re-reads topology.db and re-chunks whatever is still
undocumented, round after round, until the fixture hits target or one of its explicit
bounds (max_rounds / deadline / stall / provider limit) fires. Anything short of target
must report a status that says so, never "ok".

No pytest, no network, no agent: the worker backend is stubbed and the topology DB is a
temp sqlite file, so this runs anywhere in under a second.

    python3 bench/test_chunkgen.py
"""
from __future__ import annotations

import sqlite3
import sys
import tempfile
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import chunkgen  # noqa: E402

KINDS = ["function"]
FAILED = []


class FakeResult:
    def __init__(self, is_error=False, text=""):
        self.is_error, self.result_text, self.raw = is_error, text, {}
        self.num_turns, self.duration_ms = 1, 10


def make_db(path, n=40):
    conn = sqlite3.connect(path)
    conn.execute("CREATE TABLE resources (id TEXT PRIMARY KEY, kind TEXT NOT NULL, "
                 "name TEXT NOT NULL, language TEXT DEFAULT '', description TEXT)")
    conn.executemany("INSERT INTO resources (id,kind,name) VALUES (?,?,?)",
                     [(f"r{i}", "function", f"fn{i}") for i in range(n)])
    conn.commit()
    conn.close()


def describe(db, ids):
    conn = sqlite3.connect(db)
    conn.executemany("UPDATE resources SET description='x' WHERE id=?", [(i,) for i in ids])
    conn.commit()
    conn.close()


def ids_in(prompt):
    return [ln.strip().split()[1] for ln in prompt.splitlines() if ln.strip().startswith("- r")]


def run(db, worker, **kw):
    """Drive generate_fixture with `worker(ids) -> FakeResult|None` in place of an agent."""
    def fake_run_agent(harness, prompt, cwd, model, max_turns, timeout_s, extra_args=None):
        return worker(ids_in(prompt))
    orig = chunkgen.agents.run_agent
    chunkgen.agents.run_agent = fake_run_agent
    try:
        kw.setdefault("chunk_size", 10)
        kw.setdefault("parallel", 2)
        kw.setdefault("target", 0.95)
        return chunkgen.generate_fixture(worktree=Path(db).parent, db=db, harness="claude_code",
                                         model="fake", kinds=KINDS, logfn=lambda m: None,
                                         progress=False, **kw)
    finally:
        chunkgen.agents.run_agent = orig


def check(name, cond, detail=""):
    print(f"  {'PASS' if cond else 'FAIL'}  {name}{'  ' + detail if detail and not cond else ''}")
    if not cond:
        FAILED.append(name)


# --------------------------------------------------------------------------- #

def test_finishes_the_fixture(tmp):
    """A worker that only ever completes HALF its ids still ends at 100%: the next round
    re-lists the leftovers. This is the mui failure — one wave, then abandoned — inverted."""
    db = str(tmp / "half.db")
    make_db(db, 40)

    def half(ids):
        describe(db, ids[:len(ids) // 2])
        return FakeResult()

    r = run(db, half)
    check("half-working worker still reaches target", r["after"] >= 0.95 and r["status"] == "ok",
          f"got {r}")
    check("it took several rounds to get there", r["rounds"] > 1, f"rounds={r['rounds']}")


def test_dead_workers_retry_then_stall(tmp):
    """Workers that write nothing must not loop forever: the run stops as `stalled` — and
    NOT as `ok` — with the failures recorded for postmortem."""
    db = str(tmp / "dead.db")
    make_db(db, 40)
    log = tmp / "gen_log.jsonl"
    r = run(db, lambda ids: FakeResult(is_error=True, text="boom"), log_path=log)
    check("no progress reports `stalled`", r["status"] == "stalled", f"got {r['status']}")
    check("never claims ok at 0%", r["after"] == 0.0 and r["status"] != "ok", f"got {r}")
    check("failed chunks are counted", r["failed_chunks"] == r["chunks"], f"got {r}")
    check("failures are logged for postmortem", log.exists() and log.read_text().count("\n") >= 1)


def test_rate_limit_stops_cleanly(tmp):
    """Half a wave reporting a provider limit stops the fixture instead of burning the rest."""
    db = str(tmp / "limit.db")
    make_db(db, 40)
    r = run(db, lambda ids: FakeResult(is_error=True, text="429 too many requests"))
    check("limit reports rate_limit", r["status"] == "rate_limit", f"got {r['status']}")
    check("limit stops early", r["chunks"] <= 2, f"chunks={r['chunks']}")


def test_deadline_is_honoured(tmp):
    """An expired deadline ends the fixture as `timeout`, keeping what was written."""
    db = str(tmp / "slow.db")
    make_db(db, 40)

    def slow(ids):
        describe(db, ids)
        time.sleep(0.15)
        return FakeResult()

    r = run(db, slow, deadline=time.monotonic() + 0.4, chunk_size=5, parallel=1,
            min_worker_s=0.05)
    check("deadline reports timeout", r["status"] == "timeout", f"got {r['status']}")
    check("work done before the deadline is kept", r["described"] > 0, f"got {r}")

    # A wave that could not finish is not launched at all: a worker killed at the deadline
    # discards whatever it had not persisted, so those tokens would be pure waste.
    r = run(db, slow, deadline=time.monotonic() + 0.2, chunk_size=5, parallel=1,
            min_worker_s=60)
    check("no doomed wave is launched", r["chunks"] == 0 and r["status"] == "timeout", f"got {r}")


def test_max_rounds_is_bounded(tmp):
    """A worker that trickles just enough to dodge the stall check still can't run forever."""
    db = str(tmp / "trickle.db")
    make_db(db, 400)

    def trickle(ids):
        describe(db, ids[:4])
        return FakeResult()

    r = run(db, trickle, max_rounds=3)
    check("max_rounds bounds the loop", r["rounds"] == 3 and r["status"] == "max_rounds",
          f"got {r}")


def test_already_warm_is_a_noop(tmp):
    db = str(tmp / "warm.db")
    make_db(db, 40)
    describe(db, [f"r{i}" for i in range(40)])
    r = run(db, lambda ids: FakeResult())
    check("a warm fixture spends nothing", r["chunks"] == 0 and r["status"] == "ok", f"got {r}")


def main() -> int:
    with tempfile.TemporaryDirectory() as d:
        tmp = Path(d)
        for fn in (test_finishes_the_fixture, test_dead_workers_retry_then_stall,
                   test_rate_limit_stops_cleanly, test_deadline_is_honoured,
                   test_max_rounds_is_bounded, test_already_warm_is_a_noop):
            print(f"{fn.__name__}:")
            fn(tmp)
    print(f"\n{'FAILED: ' + ', '.join(FAILED) if FAILED else 'all chunkgen tests passed'}")
    return 1 if FAILED else 0


if __name__ == "__main__":
    raise SystemExit(main())

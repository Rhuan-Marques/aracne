#!/usr/bin/env python3
"""Recompute `n_answer_key_attempts` / `n_answer_key_fetches` for a finished run.

WHY THIS EXISTS. The distinction between "went looking for the answer" and "found it" only
became necessary once bench/bench/netshim.py started refusing the answer key: the first netshim
run had a cell issue two answer-key calls, both of which failed, and censoring that pair would
have discarded a clean data point over a lookup that provably returned nothing. A run already
in flight keeps whatever semantics its process loaded at import, and an IMPORTED row may
predate the counter entirely -- which is worse than a wrong value, because `paired._graded`
reads a missing field as clean.

The scan itself lives in bench/contamination.py, which also walks the `--baseline-from` chain
to find transcripts a run does not hold itself.

USAGE
    python3 bench/backfill_answerkey.py <run-name> [--write]

Without `--write` it only reports. With it, runs.jsonl is rewritten in place; follow with
`python3 bench/run_benchmark.py rescore --run <run-name>` to regenerate results.json so the
paired analysis censors on the corrected field.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import contamination  # noqa: E402


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__)
        return 2
    run = argv[0]
    write = "--write" in argv[1:]
    root = contamination.run_dir(run)
    rows_path = root / "runs.jsonl"
    if not rows_path.is_file():
        print(f"no such run: {rows_path}")
        return 1

    rows = [json.loads(l) for l in rows_path.read_text().splitlines() if l.strip()]
    changed = missing = 0
    for row in rows:
        tpath = contamination.transcript_for(row, root)
        if tpath is None:
            missing += 1
            continue
        attempts, fetches = contamination.scan(
            tpath, contamination.answer_key_for(row["instance_id"]))
        before = row.get("n_answer_key_fetches", 0)
        if attempts or fetches or before:
            print(f"  {row['instance_id']:38s} {row['arm']:9s} "
                  f"attempts={attempts} fetches={fetches} (was {before})")
        if row.get("n_answer_key_attempts") != attempts or before != fetches:
            row["n_answer_key_attempts"] = attempts
            row["n_answer_key_fetches"] = fetches
            changed += 1

    if missing:
        print(f"\n{missing} row(s) have no reachable transcript -- left untouched rather than "
              f"stamped clean.")
    print(f"{changed} row(s) updated" + ("" if write else " -- dry run, pass --write to apply"))
    if write and changed:
        rows_path.write_text("\n".join(json.dumps(r) for r in rows) + "\n")
        print(f"wrote {rows_path}")
        print(f"now run: python3 bench/run_benchmark.py rescore --run {run}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

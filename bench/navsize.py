#!/usr/bin/env python3
"""Does navigation difficulty scale with repository size?

WHY THIS EXISTS
---------------
The first screen was run only on repos of 300-2500 describable nodes, because that is what
is affordable to DESCRIBE. That was the wrong filter to apply at screening time: the
control arm costs no descriptions at all, and repo size is exactly the variable most likely
to drive navigation difficulty. Screening only the small half biases the headroom estimate
downward -- the included repos have a median of 202 files, the excluded ones 951.

This joins both screens against the node census and reports first-touch by size bucket, so
the question is answered with a curve rather than a single aggregate.

Reads only transcripts and the census; no grading, no Docker, no descriptions.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

BUCKETS = [(0, 2500, "<=2500 (screened first)"),
           (2501, 5000, "2501-5000"),
           (5001, 10000, "5001-10000"),
           (10001, 20000, "10001-20000"),
           (20001, 10**9, "20001+")]


def first_touch_rows(results: Path, tmp: Path) -> list[dict]:
    """Delegate to first_touch.py so both reports share one definition of a 'touch'."""
    if not (results / "runs.jsonl").exists():
        return []
    subprocess.run([sys.executable, "first_touch.py", "--results", str(results),
                    "--arm", "baseline", "--out", str(tmp)],
                   capture_output=True, text=True)
    if not tmp.exists():
        return []
    return [json.loads(l) for l in tmp.read_text().splitlines() if l.strip()]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--results", nargs="+", required=True)
    ap.add_argument("--census", required=True)
    ap.add_argument("--easy-within", type=int, default=2)
    args = ap.parse_args()

    census = {}
    for line in Path(args.census).read_text().splitlines():
        if line.strip():
            r = json.loads(line)
            if r.get("ok"):
                census[r["repo"]] = r

    rows = []
    for i, res in enumerate(args.results):
        for r in first_touch_rows(Path(res), Path(f"/tmp/_ft{i}.jsonl")):
            slug = r["key"].rsplit("-", 1)[0]          # instance id -> org__repo
            c = census.get(slug)
            if c:
                r["nodes"], r["files"] = c["nodes"], c["files"]
                rows.append(r)

    if not rows:
        print("no scored rows yet")
        return 1

    print(f"{'size bucket':<26} {'n':>3} {'within2':>9} {'median':>7} {'never':>6} {'med files':>10}")
    for lo, hi, label in BUCKETS:
        b = [r for r in rows if lo <= r["nodes"] <= hi]
        if not b:
            continue
        found = [r["first_touch"] for r in b if r["first_touch"]]
        easy = sum(1 for f in found if f <= args.easy_within)
        never = sum(1 for r in b if not r["first_touch"])
        med = sorted(found)[len(found) // 2] if found else "-"
        medf = sorted(r["files"] for r in b)[len(b) // 2]
        print(f"{label:<26} {len(b):>3} {easy}/{len(b)} ({easy/len(b):>3.0%}) "
              f"{str(med):>7} {never:>6} {medf:>10}")

    small = [r for r in rows if r["nodes"] <= 2500]
    big = [r for r in rows if r["nodes"] > 2500]
    print()
    for label, grp in (("<=2500 nodes", small), (">2500 nodes", big)):
        if not grp:
            continue
        found = [r["first_touch"] for r in grp if r["first_touch"]]
        easy = sum(1 for f in found if f <= args.easy_within)
        never = sum(1 for r in grp if not r["first_touch"])
        print(f"{label:<14} n={len(grp):<3} within{args.easy_within}={easy/len(grp):.0%}  "
              f"never={never/len(grp):.0%}  addressable={1-easy/len(grp):.0%}")
    print("\nreference: SWE-bench-Lite (linerange-20260902a) was 81% within 2, 0% never.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

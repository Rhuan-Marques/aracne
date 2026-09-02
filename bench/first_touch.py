#!/usr/bin/env python3
"""Rank tasks by how hard the fix was to FIND, from a baseline transcript.

WHY THIS EXISTS
---------------
The `linerange-20260902a` post-mortem measured that 94% of solved baseline cells touched
the eventual patch file within their first two shell commands (median #1). That single
number explains why every aracne optimisation measured flat: on SWE-bench-Lite there is no
navigation to help with, because the model already knows -- or is told -- where to look.

So navigation difficulty has to be measured BEFORE committing a benchmark, not inferred
after it. This walks a baseline run's transcripts and asks one question per task:

    at which command did the model first touch a file the gold patch edits?

Position 1 means the fix location was free. Never means the model never found it inside
the turn cap. The spread between those is the only headroom a topology tool can occupy.

WHAT COUNTS AS A TOUCH
----------------------
Any tool call whose input mentions a non-test gold-patch file, by full path or by
distinctive basename. That deliberately includes `grep`/`find` hits: a model that greps
its way to the file in one command has navigated successfully and cheaply, which is
exactly the case aracne cannot improve.

The measurement runs on the CONTROL arm only, and needs no grading and no Docker images --
it reads transcripts, so it is cheap enough to run before paying for any descriptions.
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path

_DIFF_FILE = re.compile(r"^\+\+\+ [ab]/(.+?)\s*$", re.M)
_TEST_PATH = re.compile(
    r"(^|/)(tests?|testing|__tests__|spec|specs)(/|$)|(^|/)test_[^/]*$"
    r"|_test\.[a-z]+$|\.(test|spec)\.[a-z]+$", re.I)
_AMBIGUOUS = {"index", "mod", "lib", "main", "init", "__init__", "utils", "types", "core"}


def gold_files(raw: dict) -> list[str]:
    patch = raw.get("patch") or raw.get("fix_patch") or ""
    files = [f for f in _DIFF_FILE.findall(patch) if f != "/dev/null"]
    return [f for f in files if not _TEST_PATH.search(f)]


def touches(text: str, files: list[str]) -> bool:
    for f in files:
        if f in text:
            return True
        base = f.rsplit("/", 1)[-1]
        if base.rsplit(".", 1)[0].lower() in _AMBIGUOUS:
            continue
        if re.search(rf"(?<![\w/]){re.escape(base)}(?![\w])", text):
            return True
    return False


def walk_calls(path: Path):
    """Yield each tool_use input as text, in transcript order."""
    if not path or not path.exists():
        return
    for line in path.read_text(errors="replace").splitlines():
        if not line.strip():
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("type") != "assistant":
            continue
        for block in (ev.get("message") or {}).get("content") or []:
            if isinstance(block, dict) and block.get("type") == "tool_use":
                yield json.dumps(block.get("input") or {})


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--results", required=True, help="results/<run> directory")
    ap.add_argument("--arm", default="baseline")
    ap.add_argument("--easy-within", type=int, default=2,
                    help="a first touch at or before this command counts as NO navigation "
                         "problem (default: %(default)s, the linerange post-mortem's window)")
    ap.add_argument("--out", help="write per-task rows here as JSONL")
    args = ap.parse_args()

    res = Path(args.results)
    gold = {}
    for line in (res / "manifest.jsonl").read_text().splitlines():
        if line.strip():
            r = json.loads(line)
            gold[r["key"]] = (gold_files(r.get("raw") or {}), r.get("language", "?"))

    rows = []
    for line in (res / "runs.jsonl").read_text().splitlines():
        if not line.strip():
            continue
        r = json.loads(line)
        if r.get("arm") != args.arm:
            continue
        key = r.get("instance_id")
        files, lang = gold.get(key, ([], r.get("language", "?")))
        if not files:
            continue
        tp = r.get("transcript_path")
        first, n = None, 0
        for i, text in enumerate(walk_calls(Path(tp) if tp else None), start=1):
            n = i
            if first is None and touches(text, files):
                first = i
        rows.append({"key": key, "language": lang, "first_touch": first,
                     "n_calls": n, "gold_files": files, "n_gold": len(files),
                     "turns": r.get("num_turns"), "outcome": r.get("outcome"),
                     "has_transcript": n > 0})

    have = [r for r in rows if r["has_transcript"]]
    found = [r for r in have if r["first_touch"]]
    easy = [r for r in found if r["first_touch"] <= args.easy_within]
    never = [r for r in have if not r["first_touch"]]

    print(f"tasks with a transcript : {len(have)} of {len(rows)}")
    if not have:
        print("no transcripts -- nothing to rank")
        return 1
    print(f"found the patch file    : {len(found)} ({len(found)/len(have):.0%})")
    print(f"  within {args.easy_within} commands     : {len(easy)} "
          f"({len(easy)/len(have):.0%})   <- NO navigation headroom")
    print(f"  later                 : {len(found)-len(easy)}")
    print(f"never found it          : {len(never)} ({len(never)/len(have):.0%})")
    if found:
        pos = sorted(r["first_touch"] for r in found)
        print(f"first-touch position    : median {pos[len(pos)//2]}, max {pos[-1]}")

    # Hardest first: never-found ranks above any found, then by position.
    have.sort(key=lambda r: (0 if r["first_touch"] is None else 1,
                             -(r["first_touch"] or 0)))
    print(f"\n  {'first':>6} {'calls':>6} {'turns':>6}  {'lang':<11} task")
    for r in have[:30]:
        ft = "never" if r["first_touch"] is None else r["first_touch"]
        print(f"  {str(ft):>6} {r['n_calls']:>6} {str(r['turns']):>6}  "
              f"{r['language']:<11} {r['key'][:46]}")

    if args.out:
        Path(args.out).write_text("".join(json.dumps(r) + "\n" for r in have))
        print(f"\nwrote {len(have)} rows -> {args.out}")

    verdict = len(easy) / len(have)
    print(f"\nVERDICT: {verdict:.0%} of tasks had the fix location within "
          f"{args.easy_within} commands.")
    print("  >=80% -> this benchmark cannot show a navigation effect either.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

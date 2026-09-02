#!/usr/bin/env python3
"""Select benchmark tasks whose fix location is NOT handed to the model for free.

WHY THIS EXISTS
---------------
The `linerange-20260902a` post-mortem found that 94% (baseline) / 86% (aracne) of SOLVED
cells touched the eventual patch file within their first two shell commands, median #1.
On SWE-bench-Lite the model already knows where the bug lives -- from memorization of a
popular repo, or because the issue text names the file outright. A navigation aid cannot
show a gain against a task that requires no navigation, so every optimization measured
against that sample was tuned against a metric that could not move.

This is stage A of a two-stage funnel that fixes the sample without paying for it up front:

  stage A (this script, free)   static filters over the dataset row -- no clones, no runs
  stage B (baseline-only run)   measure real first-touch; the control arm needs only a git
                                clone (see bench/arms.py:prepare_workdir), so screening
                                costs ZERO description tokens
  stage C (pay here)            `arac scan` + gen_descriptions.py for the survivors only

Description cost is per-REPO and amortized -- a warm fixture is reused by every arm, seed
and future rerun -- so the funnel's job is to keep the REPO COUNT small while keeping the
tasks hard.

WHAT "HARD" MEANS HERE
----------------------
Not "hard to fix" -- hard to LOCALIZE. We drop a task when the problem statement already
gives away where to edit, and we prefer tasks whose patch spans more than one file. Those
are the only tasks on which a cross-file topology has anything to contribute.

HONEST SCOPE. The output is a TARGETED sample, not a representative one. A result measured
on it generalizes to "issues whose fix location is not stated", which is the claim aracne
actually wants to make -- but it is not a claim about software engineering in general, and
a report must say so.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from dataclasses import dataclass, asdict
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))


# --------------------------------------------------------------------------- #
# Dataset adapters
# --------------------------------------------------------------------------- #
# Each supported dataset family names its gold patch and its repo differently. Keeping the
# differences in one table means the signal code below never branches on dataset.

def _repo_of(row: dict) -> str:
    repo = row.get("repo") or ""
    if "/" in repo:                       # SWE-bench / SWE-bench-Live: "owner/name"
        return repo
    org = row.get("org")                  # Multi-SWE-bench / MultiLang: split fields
    return f"{org}/{repo}" if org else repo


def _patch_of(row: dict) -> str:
    for field in ("patch", "fix_patch", "gold_patch", "solution_patch"):
        val = row.get(field)
        if isinstance(val, str) and val.strip():
            return val
    return ""


def _statement_of(row: dict) -> str:
    stmt = (row.get("problem_statement") or "").strip()
    if stmt:
        return stmt
    # MultiLang-style rows carry title/body instead, exactly as _normalize_multi joins them.
    parts = [row.get("title") or "", row.get("body") or ""]
    return "\n\n".join(p for p in parts if p).strip()


def _key_of(row: dict) -> str:
    return row.get("instance_id") or f"{_repo_of(row)}#{row.get('pull_number') or row.get('number')}"


# --------------------------------------------------------------------------- #
# Patch parsing
# --------------------------------------------------------------------------- #
_DIFF_FILE = re.compile(r"^\+\+\+ [ab]/(.+?)\s*$", re.M)
_HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+\d+(?:,\d+)? @@(.*)$", re.M)

# Declaration forms across the five languages aracne scans. Used to pull the names a patch
# touches, so we can tell whether the issue text already names one of them.
_DECL = re.compile(
    r"\b(?:def|func|function|class|struct|interface|impl|fn|type)\s+"
    r"([A-Za-z_][A-Za-z0-9_]*)"
)
# Test files prove nothing about localization difficulty -- the harness applies the test
# patch itself, and an issue naming a test file has not given away the FIX site.
_TEST_PATH = re.compile(r"(^|/)(tests?|testing|__tests__|spec|specs)(/|$)|(^|/)test_[^/]*$|_test\.[a-z]+$|\.(test|spec)\.[a-z]+$", re.I)


def patch_files(patch: str) -> list[str]:
    """Non-test files the gold patch edits."""
    files = [f for f in _DIFF_FILE.findall(patch) if f != "/dev/null"]
    return [f for f in files if not _TEST_PATH.search(f)]


def patch_symbols(patch: str) -> set[str]:
    """Declaration names the patch touches, from hunk headers and changed lines.

    Hunk headers are the richer source: `@@ -10,7 +10,7 @@ def register_blueprint(self)`
    names the ENCLOSING declaration even when the edit itself changed no signature.
    """
    names: set[str] = set()
    for ctx in _HUNK.findall(patch):
        names.update(_DECL.findall(ctx))
    for line in patch.splitlines():
        if line[:1] in "+-" and line[:3] not in ("+++", "---"):
            names.update(_DECL.findall(line[1:]))
    # One-and-two-character names are noise (loop vars, `fn`), never a useful giveaway.
    return {n for n in names if len(n) > 2}


# --------------------------------------------------------------------------- #
# Giveaway signals
# --------------------------------------------------------------------------- #
# A traceback hands over file AND line. Covered: Python, JS/TS, Go, Rust, Java.
_TRACEBACK = re.compile(
    r"Traceback \(most recent call last\)"
    r"|^\s*File \"[^\"]+\", line \d+"
    r"|^\s*at [\w$.<>]+ ?\(.*:\d+:\d+\)"
    r"|^\s*at [\w.$]+\([\w.]+\.java:\d+\)"
    r"|goroutine \d+ \[running\]"
    r"|^panic: .*\n\n?goroutine"
    r"|thread '[^']*' panicked at",
    re.M,
)


def mentions_path(stmt: str, files: list[str]) -> list[str]:
    """Patch files the issue text names -- as a full path or as a distinctive basename.

    A bare basename counts: "the bug is in app.py" localizes just as well as the full path.
    Basenames that are ubiquitous across a repo (index.js, mod.rs, __init__.py) do NOT
    count, because naming one of those localizes nothing.
    """
    ambiguous = {"index", "mod", "lib", "main", "init", "__init__", "utils", "types", "core"}
    hits = []
    for f in files:
        if f in stmt:
            hits.append(f)
            continue
        base = f.rsplit("/", 1)[-1]
        stem = base.rsplit(".", 1)[0]
        if stem.lower() in ambiguous:
            continue
        if re.search(rf"(?<![\w/]){re.escape(base)}(?![\w])", stmt):
            hits.append(f)
    return hits


def mentions_symbol(stmt: str, symbols: set[str]) -> list[str]:
    """Patch symbols named verbatim in the issue text (word-boundary matched)."""
    return sorted(s for s in symbols if re.search(rf"\b{re.escape(s)}\b", stmt))


# --------------------------------------------------------------------------- #
# Scoring
# --------------------------------------------------------------------------- #
@dataclass
class Verdict:
    key: str
    repo: str
    n_files: int
    n_dirs: int
    leaks_path: list[str]
    leaks_symbol: list[str]
    has_traceback: bool
    stmt_chars: int
    hard: bool
    reasons: list[str]


def judge(row: dict, min_files: int, allow_symbol: bool) -> Verdict | None:
    patch = _patch_of(row)
    stmt = _statement_of(row)
    if not patch or not stmt:
        return None                       # cannot judge; caller counts these separately

    files = patch_files(patch)
    if not files:
        return None                       # test-only patch: nothing to localize

    dirs = {f.rsplit("/", 1)[0] if "/" in f else "." for f in files}
    leak_p = mentions_path(stmt, files)
    leak_s = mentions_symbol(stmt, patch_symbols(patch))
    tb = bool(_TRACEBACK.search(stmt))

    reasons = []
    if leak_p:
        reasons.append(f"statement names patch file(s): {', '.join(leak_p[:3])}")
    if tb:
        reasons.append("statement contains a traceback")
    if leak_s and not allow_symbol:
        reasons.append(f"statement names patch symbol(s): {', '.join(leak_s[:3])}")
    if len(files) < min_files:
        reasons.append(f"patch touches {len(files)} file(s) < min_files={min_files}")

    return Verdict(
        key=_key_of(row), repo=_repo_of(row),
        n_files=len(files), n_dirs=len(dirs),
        leaks_path=leak_p, leaks_symbol=leak_s, has_traceback=tb,
        stmt_chars=len(stmt), hard=not reasons, reasons=reasons,
    )


def draw_repo_diverse(verdicts: list[Verdict], samples: int, max_per_repo: int) -> list[Verdict]:
    """Round-robin over repos, mirroring sources._draw_repo_diverse.

    The paired analysis bootstraps over REPOSITORIES, so tasks from one repo are not
    independent observations. Round-robin keeps a truncated draw maximally diverse. Ties
    inside a repo break toward the widest patch, which is the strongest hardness signal we
    have without running anything.
    """
    by_repo: dict[str, list[Verdict]] = {}
    for v in sorted(verdicts, key=lambda v: (-v.n_dirs, -v.n_files, v.key)):
        by_repo.setdefault(v.repo, []).append(v)

    drawn: list[Verdict] = []
    for depth in range(max(1, max_per_repo)):
        for tasks in by_repo.values():
            if len(drawn) >= samples:
                return drawn
            if depth < len(tasks):
                drawn.append(tasks[depth])
    return drawn[:samples]


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #
def load_rows(dataset: str, split: str, jsonl: str | None) -> list[dict]:
    if jsonl:
        with open(jsonl) as fh:
            return [json.loads(line) for line in fh if line.strip()]
    try:
        import datasets  # noqa: F401
    except ImportError:
        raise SystemExit("pip install datasets  (or pass --jsonl a local dump)")
    from datasets import load_dataset
    return [dict(r) for r in load_dataset(dataset, split=split)]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dataset", default="SWE-bench-Live/SWE-bench-Live",
                    help="HF dataset name (default: %(default)s)")
    ap.add_argument("--split", default="verified",
                    help="frozen splits only, for A/B comparability across runs "
                         "(default: %(default)s)")
    ap.add_argument("--jsonl", help="read rows from a local JSONL dump instead of HF")
    ap.add_argument("--min-files", type=int, default=2,
                    help="require the patch to touch at least N non-test files "
                         "(default: %(default)s; use 1 to keep single-file tasks)")
    ap.add_argument("--allow-symbol", action="store_true",
                    help="do not reject a task merely because the issue names a patched symbol")
    ap.add_argument("--samples", type=int, default=40, help="tasks to draw (default: %(default)s)")
    ap.add_argument("--max-per-repo", type=int, default=1,
                    help="cap per repo; 1 makes every task an independent bootstrap cluster. "
                         "Raise it to cut description cost at the price of a wider CI "
                         "(default: %(default)s)")
    ap.add_argument("--out", help="write the drawn tasks to this JSONL")
    ap.add_argument("--verdicts-out", help="write ALL verdicts here, for auditing the filter")
    args = ap.parse_args()

    rows = load_rows(args.dataset, args.split, args.jsonl)
    verdicts, unjudgeable = [], 0
    for r in rows:
        v = judge(r, args.min_files, args.allow_symbol)
        if v is None:
            unjudgeable += 1
        else:
            verdicts.append(v)

    hard = [v for v in verdicts if v.hard]

    # Funnel report: which filter is doing the work, so the thresholds can be tuned on
    # evidence instead of taste.
    tally = Counter()
    for v in verdicts:
        if v.leaks_path:
            tally["statement names the patch file"] += 1
        if v.has_traceback:
            tally["statement carries a traceback"] += 1
        if v.leaks_symbol:
            tally["statement names a patched symbol"] += 1
        if v.n_files < args.min_files:
            tally[f"patch touches < {args.min_files} non-test files"] += 1

    print(f"dataset      {args.dataset} [{args.split}]")
    print(f"rows         {len(rows)}  ({unjudgeable} unjudgeable: no patch/statement, or test-only)")
    print(f"judged       {len(verdicts)}")
    print("\ngiveaway signals (overlapping):")
    for label, n in tally.most_common():
        print(f"  {n:5d}  {n / max(1, len(verdicts)):5.1%}  {label}")
    print(f"\nhard         {len(hard)}  ({len(hard) / max(1, len(verdicts)):.1%} of judged) "
          f"across {len({v.repo for v in hard})} repos")

    drawn = draw_repo_diverse(hard, args.samples, args.max_per_repo)
    print(f"drawn        {len(drawn)} tasks across {len({v.repo for v in drawn})} repos "
          f"(max_per_repo={args.max_per_repo})")
    if len(drawn) < args.samples:
        print(f"  SHORT by {args.samples - len(drawn)}: not enough distinct repos survive. "
              f"Raise --max-per-repo (widens the CI) or relax --min-files.")

    print("\n  {:<44} {:>7} {:>6} {}".format("task", "files", "dirs", "repo"))
    for v in drawn:
        print(f"  {v.key[:44]:<44} {v.n_files:>7} {v.n_dirs:>6}  {v.repo}")

    if args.out:
        keys = {v.key for v in drawn}
        with open(args.out, "w") as fh:
            for r in rows:
                if _key_of(r) in keys:
                    fh.write(json.dumps(r) + "\n")
        print(f"\nwrote {len(keys)} rows -> {args.out}")
    if args.verdicts_out:
        with open(args.verdicts_out, "w") as fh:
            for v in verdicts:
                fh.write(json.dumps(asdict(v)) + "\n")
        print(f"wrote {len(verdicts)} verdicts -> {args.verdicts_out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

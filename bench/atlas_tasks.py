#!/usr/bin/env python3
"""Inventory the SWE-Atlas refactoring tasks and emit a bench manifest.

WHY A SEPARATE ENTRY POINT. SWE-Atlas is not SWE-bench: a task is a DIRECTORY, its prompt is
prose in instruction.md rather than a GitHub issue, its repo arrives as a prebuilt Docker image
instead of a clone URL, and it is graded by a rubric judge plus a test comparison rather than by
FAIL_TO_PASS. Only the last stage of this harness -- run the agent, diff the worktree, score the
diff -- is shared, so the sampling and preparation are their own thing.
"""
from __future__ import annotations

import argparse
import json
import os
import re
from pathlib import Path

ATLAS = Path(os.environ.get(
    "ARACNE_SWE_ATLAS_DIR",
    Path.home() / ".cache" / "aracne-bench" / "tools" / "SWE-Atlas",
))

# aracne indexes these; C and C++ have no scanner, so those tasks can only ever measure the
# control against itself.
SUPPORTED = {"Go", "Python", "TypeScript", "JavaScript"}


def task_dirs(track: str = "rf"):
    root = ATLAS / "data" / track
    return sorted(p for p in root.glob("task-*") if (p / "tests" / "config.json").exists())


def read_task(path: Path) -> dict:
    """One SWE-Atlas task, in the shape bench.sources.read_manifest expects.

    That function does `Task(**rec)`, so the top level must be exactly Task's fields and every
    SWE-Atlas-specific value rides in `raw`.
    """
    cfg = json.loads((path / "tests" / "config.json").read_text())
    image = ""
    dockerfile = path / "environment" / "Dockerfile"
    if dockerfile.exists():
        m = re.search(r"^FROM\s+(\S+)", dockerfile.read_text(), re.M)
        image = m.group(1) if m else ""
    instruction = (path / "instruction.md").read_text() if (path / "instruction.md").exists() else ""
    toml = (path / "task.toml").read_text() if (path / "task.toml").exists() else ""
    diff = ""
    gold = path / "solution" / "gold.patch"
    if gold.exists():
        diff = gold.read_text(errors="replace")

    org, _, name = cfg["repo"].partition("_")
    return {
        # SWE-Atlas ids share a long common prefix and differ at the END
        # (…be1e531 vs …be1e533), so a leading slice collides across most of the set.
        "key": f"{cfg['repo']}-{cfg['task_id'][-8:]}",
        "language": _ARACNE_LANG.get(cfg["language"], cfg["language"].lower()),
        "source": f"swe_atlas_{path.parent.name}",
        # Never cloned from -- the tree comes out of the task image. It is here because
        # Task.repo_slug() derives the paired-analysis cluster from it, and because
        # deny_answer_key turns it into the host the agent is blocked from reaching, which is
        # exactly the repository holding the reference refactor.
        "clone_url": f"https://github.com/{org}/{name}.git",
        "base_commit": cfg["base_commit"],
        "problem_statement": instruction,
        "raw": {
            "task_id": cfg["task_id"],
            "task_dir": str(path),
            "repo": cfg["repo"],
            "atlas_language": cfg["language"],
            "complexity": cfg.get("initial_complexity", ""),
            "mount_path": cfg.get("mount_path", "/workspace"),
            "image": image,
            # What the reference solution touches: the honest measure of how much of the
            # codebase a task moves, and the reason these are worth running at all.
            "gold_files": sorted({
                m.group(1) for m in re.finditer(r"^\+\+\+ b/(.+)$", diff, re.M)
            }),
            "gold_lines": sum(1 for ln in diff.splitlines()
                              if ln.startswith(("+", "-")) and not ln.startswith(("+++", "---"))),
            # Declarations the reference solution REMOVES or rewrites, which is the half
            # that stalls callers: deleting or re-signing a declaration strands everything
            # pointing at it, while adding one strands nothing. Measured, not assumed -- the
            # 10-turn smoke agent added a whole new package of declarations and raised exactly
            # zero warnings, because nothing referred to them yet.
            #
            # A task that removes none of these cannot exercise the warning channel, which is
            # the whole reason for running refactors rather than bug fixes (every hard9 gold
            # patch scored zero here).
            "gold_decls": _net_removed_decls(diff),
            "gold_decls_removed_raw": _count_decls(diff, "-"),
            "gold_decls_added": _count_decls(diff, "+"),
            "timeout_sec": _toml_int(toml, "timeout_sec", 10800),
        },
    }


# `func`/`type` (Go), `class`/`def` (Python), `interface`/`export class` (TS), `impl`/`trait`
# (Rust). Verified against every gold patch in the pool: this misses nothing there, though
# plain `fn ` and `async def ` would need adding for a Rust- or async-Python-heavy sample.
_DECL = (r"(?:func\s*(?:\([^)]*\)\s*)?|type |class |interface |export function |export class "
         r"|def |impl |trait )")
_DECL_NAME = re.compile(rf"^([+-])(?!\+\+|--)\s*{_DECL}\s*([A-Za-z_][A-Za-z0-9_]*)")


def _net_removed_decls(diff: str) -> int:
    """Declarations the patch removes and does NOT put back under the same name.

    WHY NET, NOT RAW. A raw `-class Foo` count treats "delete and immediately re-add with a
    different body" as a removal, and that pattern is common enough to dominate the ranking.
    Measured on secdev/scapy-8265afb7: 24 raw removals, of which 23 are `PCO_*` subclasses
    deleted and re-added under the same name with a new `fields_desc`. Nothing is stranded, so
    aracne raised exactly zero warnings on that cell -- correctly. Ranked by raw removals the
    task placed 5th of 12; by net removals it places last, which is where it belongs.

    Net removals is what the warning channel keys on: a name that disappears strands every
    reference to it, and a name that is rewritten in place strands nothing. Verified across the
    pool -- every task with zero net removals also produced zero warnings.

    Still only an upper bound on OPPORTUNITY, never a prediction: warnings fire on the agent's
    intermediate states, so a task whose gold patch removes little can still raise many while
    the agent has a declaration half-moved.
    """
    removed, added = set(), set()
    for ln in diff.splitlines():
        m = _DECL_NAME.match(ln)
        if m:
            (removed if m.group(1) == "-" else added).add(m.group(2))
    return len(removed - added)


def _count_decls(diff: str, sign: str) -> int:
    """Raw declaration lines the patch adds (`+`) or removes (`-`)."""
    pat = re.compile(rf"^\{sign}(?!\+\+|--)\s*{_DECL}")
    return sum(1 for ln in diff.splitlines() if pat.match(ln))


# SWE-Atlas names languages the way a human would; aracne names them the way its scanners are
# registered, and every consumer downstream keys on the latter.
_ARACNE_LANG = {
    "Go": "go", "Python": "python", "TypeScript": "typescript",
    "JavaScript": "javascript", "C": "c", "C++": "cpp",
}


def _toml_int(text: str, key: str, default: int) -> int:
    m = re.search(rf"^{key}\s*=\s*(\d+)", text, re.M)
    return int(m.group(1)) if m else default


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--track", default="rf", choices=["rf", "tw", "qa"])
    ap.add_argument("--languages", default=",".join(sorted(SUPPORTED)))
    ap.add_argument("--exclude-repo", action="append", default=[],
                    help="repo slug to drop (e.g. grafana_grafana); repeatable")
    ap.add_argument("--complexity", default="", help="comma list, e.g. Mid,High,Very High")
    ap.add_argument("--min-decls", type=int, default=0,
                    help="reference solution must add/remove at least this many declarations")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--out", default="")
    ap.add_argument("--json", action="store_true", help="emit the manifest instead of a table")
    args = ap.parse_args()

    langs = {s.strip() for s in args.languages.split(",") if s.strip()}
    comps = {s.strip() for s in args.complexity.split(",") if s.strip()}

    rows = [read_task(p) for p in task_dirs(args.track)]
    kept = [r for r in rows
            if r["raw"]["atlas_language"] in langs
            and r["raw"]["repo"] not in set(args.exclude_repo)
            and (not comps or r["raw"]["complexity"] in comps)
            and r["raw"]["gold_decls"] >= args.min_decls]
    # Spread across repositories: the paired analysis clusters by repo, so ten tasks in one
    # codebase is an effective N of one.
    kept.sort(key=lambda r: (-r["raw"]["gold_decls"], r["raw"]["repo"]))
    if args.limit:
        seen: dict[str, int] = {}
        picked = []
        for r in sorted(kept, key=lambda r: -r["raw"]["gold_decls"]):
            n = seen.get(r["raw"]["repo"], 0)
            if n >= max(1, args.limit // 3):
                continue
            seen[r["raw"]["repo"]] = n + 1
            picked.append(r)
            if len(picked) >= args.limit:
                break
        kept = picked

    if args.json or args.out:
        out = "\n".join(json.dumps(r) for r in kept) + "\n"
        if args.out:
            Path(args.out).write_text(out)
            print(f"wrote {len(kept)} task(s) to {args.out}")
        else:
            print(out, end="")
        return 0

    print(f"{'key':40} {'lang':11} {'complexity':11} {'files':>5} {'lines':>6} "
          f"{'net -':>8} {'raw -':>6} {'+decls':>6}")
    print("-" * 84)
    for r in kept:
        raw = r["raw"]
        print(f"{r['key'][:40]:40} {raw['atlas_language']:11} {raw['complexity']:11} "
              f"{len(raw['gold_files']):5} {raw['gold_lines']:6} "
              f"{raw['gold_decls']:8} {raw['gold_decls_removed_raw']:6} "
              f"{raw['gold_decls_added']:6}")
    print("-" * 84)
    print(f"{len(kept)} of {len(rows)} task(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

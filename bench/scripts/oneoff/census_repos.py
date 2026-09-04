#!/usr/bin/env python3
"""Count describable nodes per candidate repo BEFORE paying to describe any of them.

WHY THIS EXISTS
---------------
Description generation is the only expensive stage of fixture preparation, and its cost is
linear in NODE COUNT, not in task count: the 36 warm fixtures on disk hold 81,309
describable nodes (function/method/struct/interface), and generating them took about a
week of wall clock -- dominated by provider rate limits, as `report_*.json` records
(`"reason": "rate_limit"`, ~15 min per burst).

`arac scan`, by contrast, is FREE: tree-sitter only, no model calls. So node count is
knowable before committing to it. This script scans candidates and reports the census, so
repo selection can be made against a real token budget instead of a guess.

THE BAND
--------
Node count cuts both ways, which is why the filter is a band and not a ceiling:

  too few nodes   the repo is trivially greppable -- a model finds any symbol in a
                  ~50-file repo without help, so the task cannot discriminate
  too many nodes  description cost (and wall clock) explodes

The default band, 500-1500, comes off the fixture census: it is roughly the p25-p75 of
what is already on disk, and 14 repos inside it cost ~12k nodes -- about a day at the
observed rate, versus the week that 81k nodes cost.

Clones land in the SAME `_repos` cache the benchmark itself uses (see
gitutil.ensure_repo_cache), so nothing done here is thrown away: a repo censused now is
already cloned when the run needs it.

Usage:
  # census the tasks a selector run picked, stopping once 20 in-band repos are found
  python bench/scripts/oneoff/select_hard_tasks.py --split go --min-files 2 --samples 60 --out cand.jsonl
  python bench/census_repos.py --tasks cand.jsonl --target 20
"""
from __future__ import annotations

import argparse
import json
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench.sources import Task  # noqa: E402
from bench.gitutil import ensure_repo_cache, clone_at, run_git  # noqa: E402

# The kinds gen_descriptions actually writes (chunkgen.DEFAULT_KINDS). Counting anything
# else would overstate the bill.
KINDS = ("function", "method", "struct", "interface")

# Observed: 81,309 nodes across the existing fixtures took roughly a week of wall clock,
# rate-limit stalls included. That is ~480/h; 500 is the round number, and --nodes-per-hour
# overrides it once a real run gives a better one.
DEFAULT_NODES_PER_HOUR = 500

def github_size_mb(repo: str) -> float | None:
    """Repo size in MB from the GitHub API, or None if unknown.

    Checked BEFORE cloning: the candidate pool ranges from 3 MB (fortio/fortio) to 2.7 GB
    (github/gh-aw), and on a WSL2 vhdx backed by ~96 GB of real host free space a handful
    of the large ones would fill the disk. Unauthenticated GitHub allows 60 requests/hour;
    a failed lookup returns None and the repo is cloned anyway rather than silently
    dropped, so a rate-limited census degrades to today's behaviour instead of quietly
    shrinking the pool.
    """
    import urllib.request, urllib.error
    try:
        req = urllib.request.Request(f"https://api.github.com/repos/{repo}",
                                     headers={"Accept": "application/vnd.github+json"})
        with urllib.request.urlopen(req, timeout=20) as fh:
            return json.load(fh).get("size", 0) / 1024
    except Exception:  # noqa: BLE001 - network/rate-limit/404 all mean "unknown"
        return None


_print_lock = threading.Lock()
_cache_lock = threading.Lock()


def task_from_row(row: dict) -> Task:
    repo = row.get("repo") or ""
    if "/" not in repo:
        repo = f"{row.get('org')}/{repo}"
    return Task(
        key=row.get("instance_id") or f"{repo}#{row.get('pull_number')}",
        language=row.get("language") or "unknown",
        source="census",
        clone_url=f"https://github.com/{repo}.git",
        base_commit=row.get("base_commit") or "",
        problem_statement="",
        raw=row,
    )


def count_nodes(db: Path) -> tuple[int, int]:
    """(describable nodes, distinct files) in a freshly scanned topology."""
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        placeholders = ",".join("?" * len(KINDS))
        nodes = con.execute(
            f"SELECT COUNT(*) FROM resources WHERE kind IN ({placeholders})", KINDS
        ).fetchone()[0]
        files = con.execute("SELECT COUNT(DISTINCT loc_path) FROM resources").fetchone()[0]
        return nodes, files
    finally:
        con.close()


def shallow_clone(task: Task, dest: Path) -> None:
    """Fetch just `base_commit`. Cheaper than a full clone, but NOT reusable by the run."""
    dest.mkdir(parents=True, exist_ok=True)
    run_git(["init", "--quiet"], cwd=dest)
    run_git(["remote", "add", "origin", task.clone_url], cwd=dest)
    run_git(["fetch", "--quiet", "--depth", "1", "origin", task.base_commit], cwd=dest)
    run_git(["checkout", "--quiet", "FETCH_HEAD"], cwd=dest)


def census_one(task: Task, repos_dir: Path, scratch: Path, arac_bin: str,
               shallow: bool, timeout: int, max_repo_mb: float) -> dict:
    """Clone at base_commit, `arac scan`, count. Never raises -- failures are recorded.

    The clone is deleted in `finally` whatever happens, so peak disk is bounded by
    workers x max_repo_mb rather than by the size of the whole candidate pool.
    """
    slug = task.repo_slug()
    work = scratch / f"{slug}@{task.base_commit[:12]}"
    t0 = time.monotonic()
    base = {"repo": slug, "base_commit": task.base_commit, "key": task.key,
            "language": task.language}
    if max_repo_mb:
        size = github_size_mb(slug.replace("__", "/", 1))
        if size is not None and size > max_repo_mb:
            return {**base, "nodes": None, "files": None, "size_mb": round(size),
                    "scan_s": 0.0, "ok": False,
                    "error": f"skipped: {size:.0f} MB > --max-repo-mb {max_repo_mb:.0f}"}
    try:
        if shallow:
            if work.exists():
                shutil.rmtree(work)
            shallow_clone(task, work)
        else:
            cache = ensure_repo_cache(task, repos_dir)
            clone_at(task, cache, work, task.base_commit)

        subprocess.run(
            [arac_bin, "scan", "--all", "--root", ".", "--output", ".aracne/topology.db"],
            cwd=str(work), check=True, capture_output=True, text=True, timeout=timeout,
        )
        nodes, files = count_nodes(work / ".aracne" / "topology.db")
        return {"repo": slug, "base_commit": task.base_commit, "key": task.key,
                "language": task.language, "nodes": nodes, "files": files,
                "scan_s": round(time.monotonic() - t0, 1), "ok": True}
    except Exception as e:  # noqa: BLE001 - a census must survive any one bad repo
        return {"repo": slug, "base_commit": task.base_commit, "key": task.key,
                "language": task.language, "nodes": None, "files": None,
                "scan_s": round(time.monotonic() - t0, 1), "ok": False,
                "error": f"{type(e).__name__}: {e}"[:300]}
    finally:
        if work.exists():
            shutil.rmtree(work, ignore_errors=True)


def load_cache(path: Path) -> dict[str, dict]:
    if not path.exists():
        return {}
    out = {}
    for line in path.read_text().splitlines():
        if line.strip():
            r = json.loads(line)
            out[f"{r['repo']}@{r['base_commit']}"] = r
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--tasks", required=True,
                    help="JSONL of candidate rows (select_hard_tasks.py --out)")
    ap.add_argument("--min-nodes", type=int, default=500,
                    help="floor: below this a repo is trivially greppable (default: %(default)s)")
    ap.add_argument("--max-nodes", type=int, default=1500,
                    help="ceiling: description budget (default: %(default)s)")
    ap.add_argument("--target", type=int, default=0,
                    help="stop once N in-band repos are found (0 = census everything). "
                         "This is the main way to keep the census itself cheap.")
    ap.add_argument("--repos-dir", default="fixtures/_repos",
                    help="clone cache, shared with the benchmark (default: %(default)s)")
    ap.add_argument("--cache", default="fixtures/_census.jsonl",
                    help="resumable census results (default: %(default)s)")
    ap.add_argument("--arac-bin", default="../bin/arac")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--full", action="store_true",
                    help="keep a FULL reusable clone in --repos-dir instead of a throwaway "
                         "depth-1 fetch. Faster later, but the candidate pool extrapolates to "
                         "~28 GB of full clones, so this is off by default.")
    ap.add_argument("--max-repo-mb", type=float, default=400,
                    help="skip repos larger than this (GitHub API, checked before cloning). "
                         "0 disables. Peak disk is roughly workers x this "
                         "(default: %(default)s)")
    ap.add_argument("--scan-timeout", type=int, default=1800)
    ap.add_argument("--nodes-per-hour", type=int, default=DEFAULT_NODES_PER_HOUR)
    ap.add_argument("--out", help="write in-band task rows here")
    args = ap.parse_args()

    # `arac scan` runs with cwd=<clone>, so a relative --arac-bin would resolve against the
    # clone rather than against bench/. Resolve it once, here.
    if "/" in args.arac_bin:
        args.arac_bin = str(Path(args.arac_bin).resolve())

    rows = [json.loads(l) for l in Path(args.tasks).read_text().splitlines() if l.strip()]
    tasks, seen = [], set()
    for r in rows:
        t = task_from_row(r)
        ident = f"{t.repo_slug()}@{t.base_commit}"
        if ident not in seen:
            seen.add(ident)
            tasks.append(t)

    cache_path = Path(args.cache)
    cache_path.parent.mkdir(parents=True, exist_ok=True)
    cache = load_cache(cache_path)
    todo = [t for t in tasks if f"{t.repo_slug()}@{t.base_commit}" not in cache]

    print(f"candidates   {len(rows)} tasks -> {len(tasks)} distinct repo@commit")
    print(f"cached       {len(tasks) - len(todo)}   to scan: {len(todo)}")
    print(f"band         {args.min_nodes}-{args.max_nodes} describable nodes\n")

    in_band = [r for r in cache.values()
               if r.get("ok") and args.min_nodes <= r["nodes"] <= args.max_nodes]
    scratch = Path(tempfile.mkdtemp(prefix="census-"))
    cache_fh = cache_path.open("a")
    done = 0
    try:
        with ThreadPoolExecutor(max_workers=args.workers) as pool:
            futures = {pool.submit(census_one, t, Path(args.repos_dir), scratch,
                                   args.arac_bin, not args.full, args.scan_timeout,
                                   args.max_repo_mb): t
                       for t in todo}
            for fut in as_completed(futures):
                res = fut.result()
                done += 1
                with _cache_lock:
                    cache_fh.write(json.dumps(res) + "\n")
                    cache_fh.flush()
                    cache[f"{res['repo']}@{res['base_commit']}"] = res
                band = res["ok"] and args.min_nodes <= res["nodes"] <= args.max_nodes
                if band:
                    in_band.append(res)
                with _print_lock:
                    mark = "IN " if band else ("   " if res["ok"] else "ERR")
                    n = res["nodes"] if res["ok"] else "-"
                    print(f"  [{done}/{len(todo)}] {mark} {str(n):>6} nodes  "
                          f"{res['repo']}  ({res['scan_s']}s)"
                          + ("" if res["ok"] else f"  {res.get('error','')}"))
                if args.target and len(in_band) >= args.target:
                    with _print_lock:
                        print(f"\nreached --target {args.target} in-band repos; "
                              f"cancelling the rest")
                    for f in futures:
                        f.cancel()
                    break
    finally:
        cache_fh.close()
        shutil.rmtree(scratch, ignore_errors=True)

    ok = [r for r in cache.values() if r.get("ok")]
    ok.sort(key=lambda r: r["nodes"])
    print(f"\nscanned      {len(ok)} ok, {len(cache) - len(ok)} failed")
    if ok:
        counts = [r["nodes"] for r in ok]
        print(f"nodes        min={counts[0]}  median={counts[len(counts)//2]}  max={counts[-1]}")
    print(f"in band      {len(in_band)} repos")

    if in_band:
        in_band.sort(key=lambda r: r["nodes"])
        total = sum(r["nodes"] for r in in_band)
        print(f"\n  {'nodes':>7} {'files':>7}  repo")
        for r in in_band:
            print(f"  {r['nodes']:>7} {r['files']:>7}  {r['repo']}")
        print(f"\nbudget       {total:,} nodes "
              f"~= {total / args.nodes_per_hour:.1f}h at {args.nodes_per_hour}/h")
        print(f"  (for scale: the 36 fixtures on disk are 81,309 nodes ~= "
              f"{81309 / args.nodes_per_hour:.0f}h)")

    if args.out:
        keep = {r["repo"] for r in in_band}
        with open(args.out, "w") as fh:
            n = 0
            for r in rows:
                t = task_from_row(r)
                if t.repo_slug() in keep:
                    fh.write(json.dumps(r) + "\n")
                    n += 1
        print(f"\nwrote {n} in-band task rows -> {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

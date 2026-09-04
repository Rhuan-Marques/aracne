#!/usr/bin/env python3
"""Choose the fixture set: maximise hard tasks under a description-node budget.

WHY THIS EXISTS
---------------
select_hard_tasks.py says which TASKS are worth running; census_repos.py says what each
REPO costs in describable nodes. Neither answers the actual question, which is a knapsack:

    pick repos so that total nodes <= budget, maximising the number of hard tasks

The distinction matters because cost and value attach to different things. Cost is per
REPO (descriptions are generated once and reused by every arm, seed and rerun), while
statistical value is per TASK -- and, because the paired analysis bootstraps over repos,
per DISTINCT repo. A repo carrying three hard tasks is three times the value at the same
price, which is why the greedy below ranks on tasks-per-node and not on nodes alone.

The budget is the real constraint. The 36 fixtures already on disk total 81,309 nodes and
took roughly a week of wall clock, rate-limit stalls included (`report_*.json` records
`"reason": "rate_limit"`). At that observed ~500 nodes/hour, a one-day budget is ~12,000
nodes -- which is the default here.

THE FLOOR IS ON FILES, NOT NODES
--------------------------------
A node floor discards repos for being cheap. But node count and file count decouple:
terraform-docs is 335 nodes across 65 files, and 65 files is not something a model greps
blind. What makes a task undiscriminating is a small FILE tree, so that is what is floored.

Usage:
  python bench/pick_fixtures.py --census fixtures/_census.jsonl --tasks 'cand_*.jsonl' \\
      --budget-nodes 12000 --repos 14
"""
from __future__ import annotations

import argparse
import glob
import json
from collections import defaultdict
from pathlib import Path


LANG_ALIAS = {"ts": "typescript", "js": "javascript"}


def repo_slug(row: dict) -> str:
    repo = row.get("repo") or ""
    if "/" not in repo:
        repo = f"{row.get('org')}/{repo}"
    return repo.replace("/", "__", 1)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--census", default="fixtures/_census.jsonl")
    ap.add_argument("--tasks", required=True,
                    help="glob of candidate task JSONLs (select_hard_tasks.py --out)")
    ap.add_argument("--budget-nodes", type=int, default=12000,
                    help="total describable nodes to pay for (default: %(default)s ~= 1 day)")
    ap.add_argument("--repos", type=int, default=14, help="max repos to pick")
    ap.add_argument("--max-per-repo", type=int, default=3,
                    help="tasks drawn per repo; >1 trades CI width for description cost")
    ap.add_argument("--min-files", type=int, default=50,
                    help="file-tree floor: below this a repo is trivially greppable")
    ap.add_argument("--max-nodes", type=int, default=2500, help="per-repo ceiling")
    ap.add_argument("--min-nodes", type=int, default=0, help="per-repo floor (default: off)")
    ap.add_argument("--nodes-per-hour", type=int, default=500)
    ap.add_argument("--out", help="write the chosen task rows here")
    ap.add_argument("--manifest-out",
                    help="ALSO write a bench manifest (sources.Task schema) here, ready for "
                         "`run_benchmark.py run --sample-id <stem>`. This is what lets a "
                         "SWE-bench-Live sample run without teaching load_tasks a new source: "
                         "the runner reads the manifest directly (run_benchmark.py:1299).")
    args = ap.parse_args()

    census = {}
    for line in Path(args.census).read_text().splitlines():
        if line.strip():
            r = json.loads(line)
            if r.get("ok"):
                census[r["repo"]] = r

    tasks_by_repo: dict[str, list[dict]] = defaultdict(list)
    files = sorted(glob.glob(args.tasks))
    lang_of: dict[str, str] = {}
    for f in files:
        stem = Path(f).stem
        lang = stem.split("_")[-1] if "_" in stem else "?"
        for line in Path(f).read_text().splitlines():
            if line.strip():
                row = json.loads(line)
                slug = repo_slug(row)
                tasks_by_repo[slug].append(row)
                lang_of.setdefault(slug, lang)

    # Eligible = censused, inside the node ceiling/floor, and a non-trivial file tree.
    eligible = []
    for slug, rows in tasks_by_repo.items():
        c = census.get(slug)
        if not c:
            continue
        if c["files"] < args.min_files:
            continue
        if not (args.min_nodes <= c["nodes"] <= args.max_nodes):
            continue
        n_tasks = min(len(rows), args.max_per_repo)
        eligible.append({"slug": slug, "nodes": c["nodes"], "files": c["files"],
                         "lang": lang_of.get(slug, "?"), "n_tasks": n_tasks, "rows": rows})

    # Greedy on value density (tasks per node). Optimal enough: costs are within one order
    # of magnitude, so the classic greedy/knapsack gap is not worth an exact solver here.
    eligible.sort(key=lambda e: (-e["n_tasks"] / max(1, e["nodes"]), e["nodes"]))

    chosen, spent = [], 0
    for e in eligible:
        if args.repos and len(chosen) >= args.repos:
            continue
        if args.budget_nodes and spent + e["nodes"] > args.budget_nodes:
            continue
        chosen.append(e)
        spent += e["nodes"]

    print(f"candidate task files : {len(files)}")
    print(f"repos with a census  : {len([s for s in tasks_by_repo if s in census])}"
          f" of {len(tasks_by_repo)}")
    print(f"eligible after band  : {len(eligible)}"
          f"  (files>={args.min_files}, nodes {args.min_nodes}-{args.max_nodes})")
    cap = f"{args.budget_nodes:,}" if args.budget_nodes else "unlimited"
    print(f"\nPICKED {len(chosen)} repos / {sum(e['n_tasks'] for e in chosen)} tasks"
          f"  budget {spent:,}/{cap} nodes"
          f"  ~= {spent/args.nodes_per_hour:.1f}h at {args.nodes_per_hour}/h\n")
    print(f"  {'nodes':>6} {'files':>6} {'tasks':>6}  {'lang':<6} repo")
    for e in sorted(chosen, key=lambda e: -e["n_tasks"]):
        print(f"  {e['nodes']:>6} {e['files']:>6} {e['n_tasks']:>6}  {e['lang']:<6} {e['slug']}")

    by_lang = defaultdict(int)
    for e in chosen:
        by_lang[e["lang"]] += e["n_tasks"]
    print("\ntasks per language:", dict(by_lang))
    print(f"bootstrap clusters : {len(chosen)} repos "
          f"(CI ~{(len(chosen)/40)**-0.5:.1f}x wider than 40 independent clusters)")

    if args.manifest_out:
        import re as _re
        with open(args.manifest_out, "w") as fh:
            n = 0
            for e in chosen:
                lang = LANG_ALIAS.get(e["lang"], e["lang"])
                for row in e["rows"][:args.max_per_repo]:
                    repo = row.get("repo") or ""
                    if "/" not in repo:
                        repo = f"{row.get('org')}/{repo}"
                    fh.write(json.dumps({
                        "key": row.get("instance_id")
                               or f"{repo}#{row.get('pull_number')}",
                        "language": lang,
                        "source": "swe_bench_live",
                        "clone_url": f"https://github.com/{repo}.git",
                        "base_commit": row.get("base_commit") or "",
                        "problem_statement": (row.get("problem_statement") or "").strip(),
                        "raw": row,
                    }) + "\n")
                    n += 1
        print(f"wrote {n} manifest rows -> {args.manifest_out}")

    if args.out:
        with open(args.out, "w") as fh:
            n = 0
            for e in chosen:
                for row in e["rows"][:args.max_per_repo]:
                    fh.write(json.dumps(row) + "\n")
                    n += 1
        print(f"\nwrote {n} task rows -> {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

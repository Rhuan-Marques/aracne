#!/usr/bin/env python3
"""Reliable headless description generation for warm fixtures (chunked-direct).

The per-fixture driver lives in `bench/bench/chunkgen.py` and is SHARED with
`run_benchmark.py generate` (--gen-driver chunked, the default) — this script is the
standalone CLI over it, adding fixture selection, a global time budget and the
rate-limit standings report.

The in-repo `/descriptions-generate` slash command fans work out to SUB-AGENTS, which in
headless mode do not reliably PERSIST `update_description` on larger repos (they report
success but write nothing). The reliable path is to have the MAIN agent write descriptions
directly. This tool does exactly that, in parallel:

  - it lists undocumented resources straight from each fixture's topology.db,
  - splits them into small id-chunks,
  - runs several agent workers, each reading + calling update_description on ITS own ids
    (no sub-agents),
  - via a tool profile that exposes read + update_description.

Backends (`--harness`):
  - claude_code (default): each worker is `claude --print` with a temporary
    `--tool-profile all` MCP config (`_gen_mcp_all.json`) + `--dangerously-skip-permissions`.
  - opencode: each worker is `opencode run` using the worktree's own `.opencode/opencode.json`
    MCP server. That config DENIES `aracne_update_description`, so before running we
    temporarily set `permission["aracne_*"] = "allow"` in it and restore afterwards
    (`patch_opencode_perms`). Model must be `provider/model` (e.g. `deepseek/deepseek-chat`).

Progress is reported live: a one-line bar on a TTY (chunks done, nodes described, elapsed,
ETA), or a periodic status LINE every --progress-interval seconds when stdout is piped to a
log. `--no-progress` turns it off.

Only MANIFEST fixtures are warmed by default (`--only-manifest`) so we don't grind pruned
repos; pass `--all-fixtures` to warm everything on disk. Idempotent / resumable: only
still-undocumented nodes are described.

If workers start hitting a provider RATE / USAGE LIMIT, the run writes
`bench/report_<UTC>.json` with the current per-fixture standings and STOPS cleanly instead
of spinning uselessly.

Examples:
  python bench/gen_descriptions.py                                   # claude/haiku, manifest fixtures
  python bench/gen_descriptions.py --languages python
  python bench/gen_descriptions.py --harness opencode --model deepseek/deepseek-chat --languages typescript
"""
from __future__ import annotations
import argparse, glob, json, os, sys, time
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from bench import agents, fixtures  # noqa: E402

from bench import chunkgen  # noqa: E402  (shared driver with run_benchmark.py generate)
from bench.chunkgen import (  # noqa: E402
    DEFAULT_KINDS as KINDS, generate_fixture, lang_of,
)


def manifest_keys():
    """Fixture keys present in the sample manifests, matching fixtures.fixture_key
    (repo_slug@base_commit[:12]). repo_slug is derived from clone_url exactly like
    fixtures.repo_slug() — NOT from raw['org']/raw['repo'], whose schema varies across
    sources (e.g. multi_swe_bench stores repo='pallets/flask', org=None, which the old
    code turned into a bogus key so flask was silently never warmed)."""
    keys = set()
    for mp in glob.glob(str(HERE / "samples" / "*.jsonl")):
        if mp.endswith(".candidates.jsonl"):
            continue
        try:
            for line in open(mp):
                line = line.strip()
                if not line:
                    continue
                r = json.loads(line)
                url = (r.get("clone_url") or "").rstrip("/")
                if url.endswith(".git"):
                    url = url[:-len(".git")]
                parts = url.split("/")
                base = r.get("base_commit") or ""
                if len(parts) >= 2 and base:
                    keys.add(f"{parts[-2]}__{parts[-1]}@{base[:12]}")
        except Exception:
            continue
    return keys


def main():
    ap = argparse.ArgumentParser(description="Chunked-direct headless description generation")
    ap.add_argument("--harness", default="claude_code", choices=["claude_code", "opencode"],
                    help="agent backend for the workers")
    ap.add_argument("--languages", default="go,python,javascript,typescript",
                    help="comma list (rust omitted by default: scanner WIP)")
    ap.add_argument("--target", type=float, default=0.97, help="stop a fixture once it reaches this coverage")
    ap.add_argument("--chunk", type=int, default=15, help="resource ids per worker")
    ap.add_argument("--parallel", type=int, default=5, help="concurrent workers")
    ap.add_argument("--chunk-timeout", type=int, default=900,
                    help="per-worker wall-clock cap (s) — generous on purpose: a worker owns "
                         "only --chunk ids, so this bounds a WEDGED worker, not a working one")
    ap.add_argument("--chunk-max-turns", type=int, default=60,
                    help="per-worker turn cap (~4 turns per id at --chunk 15)")
    ap.add_argument("--max-rounds", type=int, default=8,
                    help="re-list/re-chunk rounds per fixture before parking it")
    ap.add_argument("--time-budget", type=int, default=0, help="overall cap in seconds (0 = run to completion)")
    ap.add_argument("--model", default="haiku", help="worker model (opencode needs provider/model)")
    ap.add_argument("--only-manifest", dest="only_manifest", action="store_true", default=True,
                    help="warm only fixtures present in bench/samples/*.jsonl (default)")
    ap.add_argument("--all-fixtures", dest="only_manifest", action="store_false",
                    help="also warm non-manifest fixtures on disk")
    ap.add_argument("--fixtures-dir", default=str(HERE / "fixtures"))
    ap.add_argument("--no-progress", dest="progress", action="store_false", default=True,
                    help="disable the live progress bar / status lines")
    ap.add_argument("--progress-interval", type=float, default=30.0,
                    help="seconds between progress lines when stdout is NOT a TTY")
    args = ap.parse_args()

    if args.harness == "opencode" and "/" not in args.model:
        ap.error(f"opencode needs a provider/model (e.g. deepseek/deepseek-chat), got: {args.model!r}")

    langs = {s.strip() for s in args.languages.split(",") if s.strip()}
    fx = Path(args.fixtures_dir)
    fx.mkdir(parents=True, exist_ok=True)
    mkeys = manifest_keys() if args.only_manifest else set()
    if args.only_manifest and not mkeys:
        print("WARNING: no manifest keys found under samples/; run 'prepare' first. "
              "Falling back to all fixtures.", flush=True)

    # claude workers need a temp tool-profile-all MCP config; opencode workers use the
    # worktree's own .opencode config (temp-patched per fixture to allow update_description).
    if args.harness == "claude_code":
        mcp = os.path.abspath(str(fx / "_gen_mcp_all.json"))
        Path(mcp).write_text(json.dumps({"mcpServers": {"aracne": {
            "command": "arac", "args": ["serve", "--tool-profile", "all", "--harness", "claude_code"]}}}))
        extra = ["--mcp-config", mcp, "--strict-mcp-config"]
    else:
        extra = None

    t0 = time.monotonic()
    def el(): return time.monotonic() - t0
    def log(m):
        line = f"[{el()/60:5.1f}m] {m}"
        chunkgen.emit(line)   # lands ABOVE a live progress bar

    def in_scope(key):
        return not (args.only_manifest and mkeys and key not in mkeys)

    def under_target(skip):
        out = []
        for db in glob.glob(str(fx / "*/worktree/.aracne/topology.db")):
            key = db.split("/")[-4]; wt = str(Path(db).parents[1])
            if key in skip or not in_scope(key):
                continue
            l = lang_of(db); d, t, p = fixtures.description_coverage(db, KINDS)
            if l not in langs or t == 0 or p >= args.target:
                continue
            out.append((t, l, key, wt, db))
        out.sort(); return out

    def write_report(reason):
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        standings = []
        for db in sorted(glob.glob(str(fx / "*/worktree/.aracne/topology.db"))):
            key = db.split("/")[-4]
            if not in_scope(key):
                continue
            l = lang_of(db); d, t, p = fixtures.description_coverage(db, KINDS)
            if t:
                standings.append({"key": key, "language": l, "described": d, "total": t,
                                  "coverage": round(p, 4)})
        rep = {"stopped_at": stamp, "reason": reason, "harness": args.harness, "model": args.model,
               "elapsed_min": round(el() / 60, 1),
               "fixtures_below_target": [s for s in standings if s["coverage"] < args.target],
               "standings": sorted(standings, key=lambda x: x["key"])}
        outp = HERE / f"report_{stamp}.json"
        outp.write_text(json.dumps(rep, indent=2))
        log(f"WROTE standings report -> {outp}")

    skip = set()
    while True:
        left = (args.time_budget - el()) if args.time_budget else None
        if left is not None and left <= 0:
            log("time budget exhausted -> stopping"); write_report("time_budget"); break
        items = under_target(skip)
        if not items:
            log("all selected fixtures at target. DONE."); break
        total, l, key, wt, db = items[0]
        fixtures.set_describe_kinds(wt, KINDS)
        log(f"[{l}] {key}: {total} target nodes ({args.harness}, {args.parallel} workers "
            f"x{args.chunk} ids)")
        # One call per fixture: chunkgen re-lists from the DB and re-chunks every round,
        # so a worker that dies just leaves its ids for the next round.
        r = generate_fixture(
            worktree=wt, db=db, harness=args.harness, model=args.model, kinds=KINDS,
            chunk_size=args.chunk, parallel=args.parallel,
            chunk_timeout_s=args.chunk_timeout, chunk_max_turns=args.chunk_max_turns,
            extra_args=extra, target=args.target, max_rounds=args.max_rounds,
            deadline=(time.monotonic() + left) if left is not None else None,
            logfn=log, progress=args.progress, progress_interval=args.progress_interval,
            log_path=Path(wt).parent / "gen_log.jsonl", label=f"{key} ")
        log(f"  {key}: -> {r['described']}/{r['total']} ({r['after']:.0%})  "
            f"[{r['status']}, {r['rounds']}r/{r['chunks']}c, {r['failed_chunks']} failed]")
        if r["status"] == "rate_limit":
            log("rate/usage limit -> stopping"); write_report("rate_limit"); break
        if r["status"] == "timeout":
            log("time budget exhausted -> stopping"); write_report("time_budget"); break
        if r["status"] != "ok":
            # stalled / max_rounds: park this fixture so the budget goes to fixtures that
            # can still move, instead of grinding the same broken one forever.
            log(f"  {r['status']} -> park {key}"); skip.add(key)

    log("=== coverage now ===")
    for db in sorted(glob.glob(str(fx / "*/worktree/.aracne/topology.db"))):
        key = db.split("/")[-4]
        if not in_scope(key):
            continue
        l = lang_of(db); d, t, p = fixtures.description_coverage(db, KINDS)
        if l in langs and t:
            log(f"  [{l}] {key}: {d}/{t} = {p:.0%}")


if __name__ == "__main__":
    main()

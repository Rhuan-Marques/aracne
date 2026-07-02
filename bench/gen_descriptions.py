#!/usr/bin/env python3
"""Reliable headless description generation for warm fixtures (chunked-direct).

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
import argparse, glob, json, os, sqlite3, sys, time
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from bench import agents, fixtures  # noqa: E402

KINDS = ["function", "method", "struct", "interface"]
# Substrings that mark a provider usage/rate limit in a worker's error text/raw output.
LIMIT_SIGNS = ("rate limit", "rate_limit", "usage limit", "usage_limit", "overloaded",
               "429", "too many requests", "quota", "insufficient_quota", "resource_exhausted")


def lang_of(db):
    try:
        c = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        r = c.execute("SELECT language,COUNT(*) FROM resources GROUP BY language ORDER BY 2 DESC LIMIT 1").fetchone()
        c.close(); return r[0] if r else "?"
    except Exception:
        return "?"


def undocumented(db):
    c = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    ph = ",".join("?" * len(KINDS))
    rows = c.execute(
        f"SELECT id,kind FROM resources WHERE kind IN ({ph}) "
        "AND (description IS NULL OR TRIM(description)='')", KINDS).fetchall()
    c.close(); return rows


def chunk_prompt(rows):
    body = "\n".join(f"- {i}   (resource_name: {k.capitalize()})" for i, k in rows)
    return ("You have aracne MCP tools (read, update_description). For EACH resource id below: "
            "call read(<id>) to view its code, then call update_description with "
            "{id:<id>, resource_name:<the resource_name shown>, description:<one concise sentence>}. "
            "Document every id YOURSELF — never spawn sub-agents.\n\nResources:\n" + body)


def manifest_keys():
    """Fixture keys present in the sample manifests, matching fixtures.fixture_key
    ({org}__{repo}@{base_commit[:12]}). Used to warm only manifest fixtures."""
    keys = set()
    for mp in glob.glob(str(HERE / "samples" / "*.jsonl")):
        if mp.endswith(".candidates.jsonl"):
            continue
        try:
            for line in open(mp):
                line = line.strip()
                if not line:
                    continue
                r = json.loads(line); raw = r.get("raw") or {}
                org, repo, base = raw.get("org"), raw.get("repo"), r.get("base_commit") or ""
                if org and repo and base:
                    keys.add(f"{org}__{repo}@{base[:12]}")
        except Exception:
            continue
    return keys


def is_limit(rr):
    """True if a worker RunResult looks like a provider rate/usage-limit failure."""
    if rr is None or not getattr(rr, "is_error", False):
        return False
    blob = ((rr.result_text or "") + " " + json.dumps(rr.raw or {})).lower()
    return any(s in blob for s in LIMIT_SIGNS)


def patch_opencode_perms(wt):
    """Temporarily allow all aracne MCP tools in the worktree's opencode config; returns a
    restore() callable. The fixtures' .opencode/opencode.json denies aracne_update_description,
    so an opencode main agent can't persist descriptions without this. No-op if absent."""
    p = Path(wt) / ".opencode" / "opencode.json"
    if not p.exists():
        return lambda: None
    original = p.read_text()
    try:
        data = json.loads(original)
    except Exception:
        return lambda: None
    data.setdefault("permission", {})["aracne_*"] = "allow"
    bak = p.parent / (p.name + ".bak")
    bak.write_text(original)
    p.write_text(json.dumps(data, indent=2))

    def restore():
        try:
            p.write_text(original)
        finally:
            try:
                bak.unlink()
            except OSError:
                pass
    return restore


def main():
    ap = argparse.ArgumentParser(description="Chunked-direct headless description generation")
    ap.add_argument("--harness", default="claude_code", choices=["claude_code", "opencode"],
                    help="agent backend for the workers")
    ap.add_argument("--languages", default="go,python,javascript,typescript",
                    help="comma list (rust omitted by default: scanner WIP)")
    ap.add_argument("--target", type=float, default=0.97, help="stop a fixture once it reaches this coverage")
    ap.add_argument("--chunk", type=int, default=15, help="resource ids per worker")
    ap.add_argument("--parallel", type=int, default=5, help="concurrent workers")
    ap.add_argument("--chunk-timeout", type=int, default=150, help="per-worker wall-clock cap (s)")
    ap.add_argument("--time-budget", type=int, default=0, help="overall cap in seconds (0 = run to completion)")
    ap.add_argument("--model", default="haiku", help="worker model (opencode needs provider/model)")
    ap.add_argument("--only-manifest", dest="only_manifest", action="store_true", default=True,
                    help="warm only fixtures present in bench/samples/*.jsonl (default)")
    ap.add_argument("--all-fixtures", dest="only_manifest", action="store_false",
                    help="also warm non-manifest fixtures on disk")
    ap.add_argument("--fixtures-dir", default=str(HERE / "fixtures"))
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
    def log(m): print(f"[{el()/60:5.1f}m] {m}", flush=True)

    def gen_chunk(wt, rows):
        try:
            return agents.run_agent(args.harness, chunk_prompt(rows), wt, args.model,
                                    120, args.chunk_timeout, extra_args=extra)
        except Exception:
            return None

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
    stopped = False
    while not stopped and not (args.time_budget and el() >= args.time_budget):
        items = under_target(skip)
        if not items:
            log("all selected fixtures at target. DONE."); break
        total, l, key, wt, db = items[0]
        fixtures.set_describe_kinds(wt, KINDS)
        restore = patch_opencode_perms(wt) if args.harness == "opencode" else (lambda: None)
        saw_limit = False
        try:
            rows = undocumented(db)
            chunks = [rows[i:i + args.chunk] for i in range(0, len(rows), args.chunk)]
            d0 = fixtures.description_coverage(db, KINDS)[0]
            log(f"[{l}] {key}: {len(rows)} undoc -> {len(chunks)} chunks x{args.chunk} "
                f"({args.harness}, parallel {args.parallel})")
            i = 0
            with ThreadPoolExecutor(max_workers=args.parallel) as ex:
                while i < len(chunks):
                    if args.time_budget and el() >= args.time_budget:
                        break
                    batch = chunks[i:i + args.parallel]; i += args.parallel
                    results = list(ex.map(lambda ch: gen_chunk(wt, ch), batch))
                    hits = sum(1 for r in results if is_limit(r))
                    if hits:
                        saw_limit = True
                        if hits >= max(1, (len(results) + 1) // 2):
                            log(f"rate/usage limit detected ({hits}/{len(results)} workers) -> stopping")
                            write_report("rate_limit"); stopped = True; break
        finally:
            restore()
        if stopped:
            break
        d1, t1, p1 = fixtures.description_coverage(db, KINDS)
        log(f"  {key}: {d0}->{d1}/{t1} ({p1:.0%})  +{d1 - d0}")
        if d1 - d0 < 3:
            if saw_limit:
                log("  no progress + limit signal -> stopping"); write_report("rate_limit"); break
            log(f"  no progress -> skip {key}"); skip.add(key)

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

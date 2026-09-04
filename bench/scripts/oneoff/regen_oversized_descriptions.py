#!/usr/bin/env python3
"""Re-describe ONLY the over-budget descriptions in a sample's warm fixtures (default: scale40).

WHY. `bench/gen_descriptions.py` (and `run_benchmark.py generate`) only ever touch nodes with
NO description — a node that already has one is done, however long it is. But descriptions ride
along in every CONTEXT block, so an overlong one is paid for on every later lookup, and aracne
now enforces a per-kind character budget on the WRITE path only
(internal/topology/domain/description.go: function/method 120, struct/interface/named-type 100).
Every description written before that gate existed is unchecked, so warm fixtures still carry
thousands of over-budget ones. This tool regenerates exactly those.

SELECTION (both must hold):
  - the node's kind is one of --kinds (default: function, method, struct, interface), and
  - it HAS a description whose trimmed length exceeds that kind's budget.

Budgets are read straight out of internal/topology/domain/description.go so they cannot drift
from the enforcement constants; --budgets overrides them per kind.

HOW. Same shape as bench/bench/chunkgen.py — Python owns the loop, the model never orchestrates:
  - list over-budget ids from the fixture's topology.db (source of truth),
  - split them into small id-chunks, worst overshoot first,
  - run `--parallel` workers, each rewriting ONLY its own ids (no sub-agents),
  - re-read the DB, re-chunk whatever is STILL over budget, and go again.

Descriptions are rewritten IN PLACE — never blanked first. That matters: `update_description`
REJECTS an over-budget write, so a worker that fails leaves the old (long) description standing
instead of a hole, and the run stays idempotent and resumable. Every touched topology.db is
backed up to `<db>.regenbak` first (consistent sqlite copy, WAL included); --restore puts them back.

PREFLIGHT. Before spending a model on a fixture, the script speaks one MCP `initialize` to
`arac serve` inside it. A fixture whose .aracne/config.json still names the read_* tools that
collapsed into a single `read` makes arac refuse to start; the worker then reports "the aracne
MCP server is down", writes nothing, and the harness still exits 0. Those fixtures are reported
and skipped — `--fix-tool-names` migrates their configs (backed up to config.json.regenbak).

AFTER a run, re-freeze so the snapshots the benchmark actually restores carry the new text:
  python bench/run_benchmark.py freeze --config scale40

Examples:
  python bench/regen_oversized_descriptions.py --dry-run              # what would be rewritten
  python bench/regen_oversized_descriptions.py                        # scale40, claude/haiku
  python bench/regen_oversized_descriptions.py --languages python --parallel 3
  python bench/regen_oversized_descriptions.py --only flask,requests --time-budget 3600
  python bench/regen_oversized_descriptions.py --restore              # undo: restore *.regenbak
"""
from __future__ import annotations

import argparse
import json
import re
import sqlite3
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import agents, chunkgen, fixtures, sources  # noqa: E402

REPO_ROOT = HERE.parent
BUDGET_SRC = REPO_ROOT / "internal" / "topology" / "domain" / "description.go"

DEFAULT_KINDS = ["function", "method", "struct", "interface"]

# Which Go budget constant governs each kind (mirrors domain.DescriptionBudget).
KIND_CLASS = {
    "function": "function", "method": "function",
    "struct": "type", "named_type": "type", "interface": "type",
    "file": "type", "package": "type",
    "variable": "variable", "dependency": "variable",
}
FALLBACK_BUDGETS = {"function": 120, "type": 100, "variable": 80}


# --------------------------------------------------------------------------- #
# budgets
# --------------------------------------------------------------------------- #
def load_budgets(overrides: str | None = None) -> dict[str, int]:
    """Per-KIND character budgets, parsed from the Go constants (fallback: the values they
    had when this script was written). `overrides` is a comma list like
    'function=100,struct=90' and wins over both."""
    classes = dict(FALLBACK_BUDGETS)
    try:
        src = BUDGET_SRC.read_text(encoding="utf-8")
        for cls, const in (("function", "DescriptionBudgetFunction"),
                           ("type", "DescriptionBudgetType"),
                           ("variable", "DescriptionBudgetVariable")):
            m = re.search(rf"\b{const}\s*=\s*(\d+)", src)
            if m:
                classes[cls] = int(m.group(1))
    except OSError:
        print(f"WARNING: could not read {BUDGET_SRC}; using built-in budgets {classes}",
              flush=True)
    budgets = {kind: classes[cls] for kind, cls in KIND_CLASS.items()}
    for item in (overrides or "").split(","):
        item = item.strip()
        if not item:
            continue
        kind, _, val = item.partition("=")
        if not val.isdigit():
            raise SystemExit(f"--budgets: expected kind=NUMBER, got {item!r}")
        budgets[kind.strip()] = int(val)
    return budgets


def budget_of(budgets: dict[str, int], kind: str) -> int:
    """Budget for a kind; unknown kinds fall back to the most permissive one, exactly like
    domain.DescriptionBudget's default arm."""
    return budgets.get(kind, budgets.get("function", 120))


# --------------------------------------------------------------------------- #
# topology reads
# --------------------------------------------------------------------------- #
def over_budget(db, budgets, kinds) -> list[dict]:
    """Every resource of `kinds` whose stored description overruns its budget, worst first.

    Length is measured the way domain.ValidateDescription measures it: characters (not bytes)
    of the TRIMMED text, so a stray trailing newline never counts as an overrun.
    """
    conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        ph = ",".join("?" * len(kinds))
        rows = conn.execute(
            f"SELECT id,kind,description FROM resources WHERE kind IN ({ph}) "
            "AND description IS NOT NULL AND TRIM(description) != ''", list(kinds)).fetchall()
    finally:
        conn.close()
    out = []
    for rid, kind, desc in rows:
        text = (desc or "").strip()
        cap = budget_of(budgets, kind)
        if len(text) > cap:
            out.append({"id": rid, "kind": kind, "chars": len(text), "budget": cap,
                        "description": text})
    out.sort(key=lambda r: (-r["chars"], r["id"]))
    return out


def count_over(db, budgets, kinds) -> int:
    try:
        return len(over_budget(db, budgets, kinds))
    except sqlite3.Error:
        return -1


def backup_db(db: Path, force: bool = False) -> Path | None:
    """Consistent sqlite copy of a fixture DB to `<db>.regenbak` (WAL contents included).

    Kept only if absent — the FIRST backup is the pre-run state, and overwriting it on a
    resumed pass would quietly destroy the thing it exists to protect."""
    bak = Path(str(db) + ".regenbak")
    if bak.exists() and not force:
        return bak
    src = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        dst = sqlite3.connect(str(bak))
        try:
            src.backup(dst)
            # Fold the WAL into the file itself: the backup has to be readable on its own
            # (and copyable as one file), not a db plus two sidecars that may go missing.
            dst.execute("PRAGMA journal_mode=DELETE")
        finally:
            dst.close()
    finally:
        src.close()
    for suffix in ("-wal", "-shm"):
        stray = Path(str(bak) + suffix)
        if stray.exists():
            stray.unlink()
    return bak


def restore_db(db: Path) -> bool:
    """Put `<db>.regenbak` back over the fixture DB, dropping its stale -wal/-shm."""
    bak = Path(str(db) + ".regenbak")
    if not bak.exists():
        return False
    conn = sqlite3.connect(f"file:{bak}?mode=ro", uri=True)
    try:
        dst = sqlite3.connect(str(db))
        try:
            conn.backup(dst)
            # Checkpoint before dropping the -wal below, or the restore lands in a file
            # nobody reads back.
            dst.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        finally:
            dst.close()
    finally:
        conn.close()
    for suffix in ("-wal", "-shm"):
        stray = Path(str(db) + suffix)
        if stray.exists():
            stray.unlink()
    # Drop the used-up backup so the next run takes a fresh baseline of the restored DB.
    bak.unlink()
    return True


# --------------------------------------------------------------------------- #
# preflight: can `arac serve` even start in this fixture?
# --------------------------------------------------------------------------- #
# Tool names that the read family collapsed into a single `read` tool. A fixture whose
# .aracne/config.json still lists them is REJECTED at startup by a current arac
# ("Invalid .aracne/config.json: ... unknown MCP tool(s): read_file, ..."), which kills the
# worker's MCP server — the worker then reports "the aracne MCP server is down" and writes
# nothing, while the harness still exits 0. Preflight catches that before we spend a model.
LEGACY_READ_TOOLS = {"read_file", "read_function", "read_struct", "read_interface",
                     "read_named_type", "read_package", "read_dependency", "read_resource"}

_MCP_INIT = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
    "protocolVersion": "2024-11-05", "capabilities": {},
    "clientInfo": {"name": "regen-preflight", "version": "1"}}})


def preflight(worktree, arac_bin="arac", timeout_s=30) -> tuple[bool, str]:
    """Start `arac serve` in the fixture and speak one MCP `initialize` at it.

    Returns (ok, message). This is the exact server the workers get, so anything that stops
    it here — a stale config, a missing binary, an unreadable DB — would have silently
    produced empty rounds."""
    try:
        proc = subprocess.run(
            [arac_bin, "serve", "--tool-profile", "all", "--harness", "claude_code"],
            input=_MCP_INIT + "\n", cwd=str(worktree), text=True,
            capture_output=True, timeout=timeout_s)
    except FileNotFoundError:
        return False, f"{arac_bin}: not found on PATH"
    except subprocess.TimeoutExpired:
        return False, f"{arac_bin} serve did not answer initialize within {timeout_s}s"
    if '"result"' in (proc.stdout or ""):
        return True, "ok"
    err = (proc.stderr or proc.stdout or "").strip().splitlines()
    return False, (err[0] if err else f"exit {proc.returncode}, no output")


def migrate_read_tools(worktree) -> int:
    """Collapse the removed read_* tool names to `read` in a fixture's .aracne/config.json.

    Returns how many names were rewritten (0 = nothing to do). The original is kept at
    `config.json.regenbak`, and --restore puts it back. Every tool-name list in the file is
    rewritten (llm.* mcp_tools/blocked_tools and viz.chat tools), de-duplicated in place, so
    the config keeps its shape and only the vocabulary moves."""
    cfg = Path(worktree) / ".aracne" / "config.json"
    if not cfg.exists():
        return 0
    original = cfg.read_text(encoding="utf-8")
    data = json.loads(original)
    renamed = 0

    def fix(names):
        nonlocal renamed
        out = []
        for t in names:
            if t in LEGACY_READ_TOOLS:
                renamed += 1
                t = "read"
            if t not in out:
                out.append(t)
        return out

    def walk(node):
        if isinstance(node, dict):
            for k, v in node.items():
                if k in ("mcp_tools", "tools", "blocked_tools") and isinstance(v, list) \
                        and all(isinstance(x, str) for x in v):
                    node[k] = fix(v)
                else:
                    walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)

    walk(data)
    if not renamed:
        return 0
    bak = Path(str(cfg) + ".regenbak")
    if not bak.exists():
        bak.write_text(original, encoding="utf-8")
    cfg.write_text(json.dumps(data, indent=2), encoding="utf-8")
    return renamed


# --------------------------------------------------------------------------- #
# worker prompt
# --------------------------------------------------------------------------- #
def shrink_prompt(rows, show_current: int) -> str:
    """The worker prompt: rewrite exactly these descriptions, shorter, itself, no sub-agents."""
    body = []
    for r in rows:
        body.append(f"- {r['id']}   (resource_name: {r['kind'].capitalize()}, "
                    f"budget: {r['budget']} chars, current: {r['chars']} chars)")
        if show_current > 0:
            cur = " ".join(r["description"].split())
            if len(cur) > show_current:
                cur = cur[:show_current] + "…"
            body.append(f"    too long, replace: {cur}")
    return (
        "You have aracne MCP tools (read, update_description). Each resource below ALREADY has a "
        "description, but it OVERRUNS the character budget for its kind. Rewrite each one shorter.\n\n"
        "For EACH resource id below: call read(<id>) to view its code, then call update_description "
        "with {id:<id>, resource_name:<the resource_name shown>, description:<the new, shorter description>}.\n\n"
        "Rules — follow EXACTLY:\n"
        "- STAY WITHIN the budget shown for that id. update_description REJECTS an over-budget "
        "write; if it rejects yours, cut words and call it again until it is accepted.\n"
        "- ONE line only: no newlines, no code, no examples, no doctests ('>>>'), no "
        "'Examples/Parameters/Returns' sections, no reST/Sphinx roles (:func:, .. math::).\n"
        "- Write it in YOUR OWN words from the code — never paste back the existing docstring or "
        "doc-comment, and never merely restate the identifier name.\n"
        "- Functions/methods: what it does, plus any notable params, returns or side effects. "
        "Structs/types: what it represents and its key fields. Interfaces: the contract and key methods.\n"
        "- Keep it accurate: a shorter description that drops the point is worse than the long one.\n"
        "Rewrite every id YOURSELF — never spawn sub-agents.\n\nResources:\n" + "\n".join(body))


# --------------------------------------------------------------------------- #
# progress
# --------------------------------------------------------------------------- #
class ShrinkProgress(chunkgen.Progress):
    """chunkgen's bar, counting FIXED nodes (over-budget resolved) instead of described ones."""

    def __init__(self, total_chunks, db, budgets, kinds, initial_over, logfn=print,
                 line_interval=30.0, label=""):
        super().__init__(total_chunks, db, 0, max(1, initial_over), kinds=kinds, logfn=logfn,
                         line_interval=line_interval, label=label)
        self._budgets, self._kinds, self._start_over = budgets, kinds, initial_over

    def _refresh(self):
        try:
            now = count_over(self.db, self._budgets, self._kinds)
            if now >= 0:
                self.described = max(0, self._start_over - now)
        except Exception:  # noqa: BLE001  (worker may hold a write lock)
            pass


# --------------------------------------------------------------------------- #
# the driver (one fixture)
# --------------------------------------------------------------------------- #
def shrink_fixture(*, worktree, db, budgets, kinds, harness, model, chunk_size=15, parallel=5,
                   chunk_timeout_s=900, chunk_max_turns=60, extra_args=None, max_rounds=8,
                   min_gain=1, deadline=None, min_worker_s=90, logfn=print, progress=True,
                   progress_interval=30.0, log_path=None, label="", show_current=200) -> dict:
    """Rewrite a fixture's over-budget descriptions, Python owning the retry loop.

    Rounds repeat until: nothing is over budget ("ok"), a round fixed fewer than `min_gain`
    ("stalled"), half a wave reported a provider limit ("rate_limit"), `deadline` passed
    ("timeout"), or `max_rounds` was spent ("max_rounds"). Every terminal status keeps what
    was already written — the DB is the checkpoint.
    """
    t0 = time.monotonic()
    before = count_over(db, budgets, kinds)
    remaining = before
    rounds = chunks_run = failed = limit_hits = 0
    status = "ok"

    def out_of_time(reserve=0):
        return deadline is not None and time.monotonic() + reserve >= deadline

    def worker_timeout():
        if deadline is None:
            return chunk_timeout_s
        return max(30, min(chunk_timeout_s, int(deadline - time.monotonic())))

    def record(rr, ids):
        if not log_path:
            return
        try:
            with open(log_path, "a", encoding="utf-8") as fh:
                fh.write(json.dumps({
                    "round": rounds, "ids": ids,
                    "reason": "worker_died" if rr is None else "agent_error",
                    "is_error": bool(getattr(rr, "is_error", True)),
                    "num_turns": getattr(rr, "num_turns", 0),
                    "duration_ms": getattr(rr, "duration_ms", 0),
                    "result_text": (getattr(rr, "result_text", "") or "")[:2000],
                }, default=str) + "\n")
        except OSError:
            pass

    restore_perms = chunkgen.patch_opencode_perms(worktree) if harness == "opencode" else (lambda: None)
    try:
        while rounds < max_rounds:
            if out_of_time():
                status = "timeout"
                break
            rows = over_budget(db, budgets, kinds)
            if not rows:
                break
            rounds += 1
            round_start = len(rows)
            batches = [rows[i:i + chunk_size] for i in range(0, len(rows), chunk_size)]
            logfn(f"  round {rounds}: {len(rows)} over budget -> {len(batches)} chunks "
                  f"x{chunk_size} ({parallel} workers)")
            bar = (ShrinkProgress(len(batches), db, budgets, kinds, before, logfn=logfn,
                                  line_interval=progress_interval, label=label).start()
                   if progress else chunkgen.NullBar())
            stop_round = None
            chunkgen.ACTIVE_BAR = bar if isinstance(bar, chunkgen.Progress) else None
            try:
                with ThreadPoolExecutor(max_workers=parallel) as ex:
                    i = 0
                    while i < len(batches):
                        if out_of_time(reserve=min_worker_s):
                            stop_round = "timeout"
                            break
                        wave = batches[i:i + parallel]
                        i += parallel
                        futs = {ex.submit(_run_chunk, harness, ch, worktree, model,
                                          chunk_max_turns, worker_timeout(), extra_args,
                                          show_current): ch for ch in wave}
                        limited = 0
                        for fut in as_completed(futs):
                            rr = fut.result()
                            chunks_run += 1
                            bar.advance()
                            if rr is None or getattr(rr, "is_error", False):
                                failed += 1
                                record(rr, [r["id"] for r in futs[fut]])
                            if chunkgen.is_limit(rr):
                                limited += 1
                                limit_hits += 1
                        if limited >= max(1, (len(wave) + 1) // 2):
                            stop_round = "rate_limit"
                            break
            finally:
                chunkgen.ACTIVE_BAR = None
                bar.close()

            remaining = count_over(db, budgets, kinds)
            logfn(f"  round {rounds}: -{round_start - remaining} -> {remaining} still over budget"
                  f"{'  [' + stop_round + ']' if stop_round else ''}")
            if stop_round:
                status = stop_round
                break
            if round_start - remaining < min_gain:
                # A round that fixed nothing will not fix anything next round either: these
                # ids are ones the model cannot (or will not) shorten. Park the fixture.
                status = "stalled"
                break
        else:
            status = "max_rounds"
    finally:
        restore_perms()

    remaining = count_over(db, budgets, kinds)
    if remaining == 0:
        status = "ok"
    return {"status": status, "before": before, "after": remaining,
            "fixed": max(0, before - remaining), "rounds": rounds, "chunks": chunks_run,
            "failed_chunks": failed, "limit_hits": limit_hits,
            "dur_s": int(time.monotonic() - t0)}


def _run_chunk(harness, rows, worktree, model, max_turns, timeout_s, extra_args, show_current):
    """One worker: rewrite exactly `rows`. Never raises — a dead worker just leaves its ids
    over budget for the next round."""
    try:
        return agents.run_agent(harness, shrink_prompt(rows, show_current), worktree, model,
                                max_turns, timeout_s, extra_args=extra_args)
    except Exception:  # noqa: BLE001  (incl. subprocess.TimeoutExpired)
        return None


# --------------------------------------------------------------------------- #
# fixture selection
# --------------------------------------------------------------------------- #
def select_fixtures(args) -> list[tuple[str, str, Path, Path]]:
    """(key, language, worktree, db) for every manifest fixture that exists on disk and
    passes the --languages / --only filters. One entry per repo@commit."""
    manifest = sources.manifest_path(args.samples_dir, args.sample_id)
    tasks = sources.read_manifest(manifest)
    langs = {s.strip() for s in args.languages.split(",") if s.strip()} if args.languages else None
    only = [s.strip() for s in args.only.split(",") if s.strip()] if args.only else None

    seen, out, missing = set(), [], []
    for t in tasks:
        key = fixtures.fixture_key(t)
        if key in seen:
            continue
        seen.add(key)
        if langs and t.language not in langs:
            continue
        if only and not any(o in key for o in only):
            continue
        wt = Path(args.fixtures_dir) / key / "worktree"
        db = wt / ".aracne" / "topology.db"
        if not db.exists():
            missing.append(key)
            continue
        out.append((key, t.language, wt, db))
    if missing:
        print(f"NOTE: {len(missing)} manifest fixture(s) have no topology.db yet (not prepared): "
              + ", ".join(missing[:5]) + (" ..." if len(missing) > 5 else ""), flush=True)
    return out


# --------------------------------------------------------------------------- #
# main
# --------------------------------------------------------------------------- #
def main():
    ap = argparse.ArgumentParser(
        description="Regenerate ONLY the over-budget descriptions of a sample's warm fixtures",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--sample-id", default="scale40", help="manifest id under --samples-dir")
    ap.add_argument("--samples-dir", default=str(HERE / "samples"))
    ap.add_argument("--fixtures-dir", default=str(HERE / "fixtures"))
    ap.add_argument("--languages", default="", help="comma list; empty = every language in the manifest")
    ap.add_argument("--only", default="", help="comma list of substrings; keep only matching fixture keys")
    ap.add_argument("--kinds", default=",".join(DEFAULT_KINDS),
                    help="resource kinds to consider (default: function,method,struct,interface)")
    ap.add_argument("--budgets", default="",
                    help="override per-kind budgets, e.g. 'function=110,struct=90' "
                         "(default: parsed from internal/topology/domain/description.go)")
    ap.add_argument("--harness", default="claude_code", choices=["claude_code", "opencode"])
    ap.add_argument("--arac-bin", default="arac", help="arac binary the workers' MCP server runs")
    ap.add_argument("--fix-tool-names", action="store_true",
                    help="if a fixture's .aracne/config.json still names the removed read_* MCP "
                         "tools (which stops `arac serve` dead), collapse them to `read` "
                         "(config.json backed up to config.json.regenbak)")
    ap.add_argument("--skip-preflight", action="store_true",
                    help="don't verify `arac serve` starts in each fixture before spending a model")
    ap.add_argument("--model", default="haiku", help="worker model (opencode needs provider/model)")
    ap.add_argument("--chunk", type=int, default=15, help="resource ids per worker")
    ap.add_argument("--parallel", type=int, default=5, help="concurrent workers within a fixture")
    ap.add_argument("--chunk-timeout", type=int, default=900, help="per-worker wall-clock cap (s)")
    ap.add_argument("--chunk-max-turns", type=int, default=60, help="per-worker turn cap")
    ap.add_argument("--max-rounds", type=int, default=6, help="re-list/re-chunk rounds per fixture")
    ap.add_argument("--fixture-timeout", type=int, default=14400,
                    help="per-fixture wall-clock cap (s); 0 = uncapped")
    ap.add_argument("--time-budget", type=int, default=0, help="overall cap in seconds (0 = run to completion)")
    ap.add_argument("--show-current", type=int, default=200,
                    help="chars of the existing description shown to the worker (0 = don't show it)")
    ap.add_argument("--dry-run", action="store_true",
                    help="report what would be rewritten (and with --dump, list it); write nothing")
    ap.add_argument("--dump", default="", help="dry-run: write the offender list to this JSONL path")
    ap.add_argument("--no-backup", dest="backup", action="store_false", default=True,
                    help="skip the <db>.regenbak safety copy")
    ap.add_argument("--restore", action="store_true",
                    help="restore every selected fixture's <db>.regenbak and exit")
    ap.add_argument("--no-progress", dest="progress", action="store_false", default=True)
    ap.add_argument("--progress-interval", type=float, default=30.0,
                    help="seconds between progress lines when stdout is NOT a TTY")
    args = ap.parse_args()

    if args.harness == "opencode" and "/" not in args.model:
        ap.error(f"opencode needs a provider/model (e.g. deepseek/deepseek-chat), got: {args.model!r}")

    kinds = [k.strip() for k in args.kinds.split(",") if k.strip()]
    budgets = load_budgets(args.budgets)
    items = select_fixtures(args)
    if not items:
        print("No fixtures selected.")
        return 0

    shown = {k: budget_of(budgets, k) for k in kinds}
    print(f"sample_id={args.sample_id}  fixtures={len(items)}  kinds={kinds}")
    print(f"budgets: " + ", ".join(f"{k}<={v}" for k, v in shown.items()))

    # --- restore ---------------------------------------------------------- #
    if args.restore:
        n = c = 0
        for key, _lang, wt, db in items:
            if restore_db(db):
                n += 1
                print(f"  restored {key}")
            cfg_bak = wt / ".aracne" / "config.json.regenbak"
            if cfg_bak.exists():
                (wt / ".aracne" / "config.json").write_text(
                    cfg_bak.read_text(encoding="utf-8"), encoding="utf-8")
                cfg_bak.unlink()
                c += 1
        print(f"Restored {n} fixture DB(s) and {c} config.json from .regenbak.")
        return 0

    # --- survey ----------------------------------------------------------- #
    survey, total_over = [], 0
    for key, lang, wt, db in items:
        rows = over_budget(db, budgets, kinds)
        total_over += len(rows)
        survey.append({"key": key, "language": lang, "worktree": str(wt), "db": str(db),
                       "over": len(rows), "rows": rows})
    for s in sorted(survey, key=lambda s: -s["over"]):
        if s["over"]:
            worst = s["rows"][0]["chars"]
            print(f"  [{s['language']:<10}] {s['key']:<48} {s['over']:>5} over budget "
                  f"(worst {worst} chars)")
    print(f"TOTAL: {total_over} description(s) over budget across {len(items)} fixture(s)")

    if args.dump:
        with open(args.dump, "w", encoding="utf-8") as fh:
            for s in survey:
                for r in s["rows"]:
                    fh.write(json.dumps({"key": s["key"], "language": s["language"], **r}) + "\n")
        print(f"wrote offender list -> {args.dump}")
    if args.dry_run:
        print("\n--dry-run: nothing written.")
        return 0
    if not total_over:
        print("Nothing to do.")
        return 0

    # --- worker tool access ------------------------------------------------ #
    # claude workers need update_description, which the shipped `main` profile in each
    # worktree's .mcp.json does NOT expose; point them at a temp --tool-profile all config
    # instead of editing any fixture (so freeze/run still measure the shipped profile).
    extra = None
    if args.harness == "claude_code":
        mcp = Path(args.fixtures_dir) / "_regen_mcp_all.json"
        mcp.parent.mkdir(parents=True, exist_ok=True)
        mcp.write_text(json.dumps({"mcpServers": {"aracne": {
            "command": "arac",
            "args": ["serve", "--tool-profile", "all", "--harness", "claude_code"]}}}, indent=2),
            encoding="utf-8")
        extra = ["--mcp-config", str(mcp), "--strict-mcp-config"]
        print(f"tool access: temp --tool-profile all ({mcp})")

    # --- preflight ---------------------------------------------------------- #
    # A fixture whose `arac serve` refuses to start burns a full round of workers that all
    # report "the MCP server is down" and write nothing — and the harness still exits 0.
    todo_keys = {s["key"] for s in survey if s["over"]}
    blocked = []
    if not args.skip_preflight:
        for s in survey:
            if s["key"] not in todo_keys:
                continue
            ok, msg = preflight(s["worktree"], args.arac_bin)
            if not ok and args.fix_tool_names:
                renamed = migrate_read_tools(s["worktree"])
                if renamed:
                    ok, msg = preflight(s["worktree"], args.arac_bin)
                    print(f"  {s['key']}: rewrote {renamed} legacy read_* tool name(s) "
                          f"-> {'ok' if ok else msg}")
            if not ok:
                blocked.append((s["key"], msg))
                s["skip"] = True       # don't spend workers on a fixture with no MCP server
    if blocked:
        print(f"\nPREFLIGHT FAILED on {len(blocked)} fixture(s) — skipping them:")
        for key, msg in blocked[:10]:
            print(f"  {key}: {msg}")
        if len(blocked) > 10:
            print(f"  ... and {len(blocked) - 10} more")
        if any("unknown MCP tool" in m for _, m in blocked):
            print("  These configs still name the read_* tools that collapsed into `read`.\n"
                  "  Re-run with --fix-tool-names to migrate them (config.json is backed up).")
        if not any(s["over"] and not s.get("skip") for s in survey):
            print("\nNo fixture can serve MCP; nothing to do.")
            return 1

    t0 = time.monotonic()

    def el():
        return time.monotonic() - t0

    def log(m):
        chunkgen.emit(f"[{el() / 60:5.1f}m] {m}")

    results = []
    stop_reason = None
    todo = [s for s in sorted(survey, key=lambda s: s["over"])
            if s["over"] and not s.get("skip")]
    for i, s in enumerate(todo, 1):
        left = (args.time_budget - el()) if args.time_budget else None
        if left is not None and left <= 0:
            stop_reason = "time_budget"
            log("overall time budget exhausted -> stopping")
            break
        if args.backup:
            backup_db(Path(s["db"]))
        deadline = None
        caps = [c for c in (args.fixture_timeout or None, left) if c]
        if caps:
            deadline = time.monotonic() + min(caps)
        log(f"[{i}/{len(todo)}] [{s['language']}] {s['key']}: {s['over']} over budget "
            f"({args.harness}/{args.model}, {args.parallel} workers x{args.chunk} ids)")
        r = shrink_fixture(
            worktree=s["worktree"], db=s["db"], budgets=budgets, kinds=kinds,
            harness=args.harness, model=args.model, chunk_size=args.chunk,
            parallel=args.parallel, chunk_timeout_s=args.chunk_timeout,
            chunk_max_turns=args.chunk_max_turns, extra_args=extra,
            max_rounds=args.max_rounds, deadline=deadline, logfn=log,
            progress=args.progress, progress_interval=args.progress_interval,
            log_path=Path(s["worktree"]).parent / "regen_log.jsonl",
            label=f"{s['key']} ", show_current=args.show_current)
        log(f"  {s['key']}: {r['before']} -> {r['after']} over budget "
            f"({r['fixed']} fixed) [{r['status']}, {r['rounds']}r/{r['chunks']}c, "
            f"{r['failed_chunks']} failed, {r['dur_s']}s]")
        results.append({"key": s["key"], "language": s["language"], **r})
        if r["status"] == "rate_limit":
            stop_reason = "rate_limit"
            log("provider rate/usage limit -> stopping")
            break
        if r["status"] == "timeout" and left is not None and el() >= args.time_budget:
            stop_reason = "time_budget"
            break

    # --- report ------------------------------------------------------------ #
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    standings = []
    for s in survey:
        standings.append({"key": s["key"], "language": s["language"], "before": s["over"],
                          "after": count_over(Path(s["db"]), budgets, kinds),
                          "skipped": bool(s.get("skip"))})
    report = {"finished_at": stamp, "sample_id": args.sample_id, "reason": stop_reason or "done",
              "harness": args.harness, "model": args.model, "kinds": kinds,
              "budgets": shown, "elapsed_min": round(el() / 60, 1),
              "fixtures": results, "standings": sorted(standings, key=lambda x: x["key"])}
    out = HERE / f"regen_report_{stamp}.json"
    out.write_text(json.dumps(report, indent=2), encoding="utf-8")

    fixed = sum(r["fixed"] for r in results)
    still = sum(s["after"] for s in standings)
    print(f"\n=== done in {el() / 60:.1f}m: {fixed} rewritten, {still} still over budget "
          f"({stop_reason or 'complete'}) ===")
    print(f"report -> {out}")
    if fixed:
        print("NEXT: re-freeze so runs pick the new text up:\n"
              f"  python bench/run_benchmark.py freeze --config {args.sample_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

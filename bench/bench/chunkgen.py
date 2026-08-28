"""Chunked-direct description generation: the deterministic driver.

WHY THIS EXISTS. The in-repo `/descriptions-generate` slash command asks the MAIN agent to
orchestrate: list undocumented nodes, fan them out to executor sub-agents, then RE-LIST and
repeat until none remain. On large repos that loop is not reliable. A real observed failure
(mui/material-ui, 4,963 undocumented nodes): the orchestrator delegated orchestration to a
general-purpose sub-agent (which the prompt forbids), launched 20 of 98 batches, tried to
"wait" via ScheduleWakeup (a no-op in headless `--print`), then emitted a plain-text status
message. A text message with no tool call ENDS a headless turn, so the harness exited 0 —
the runner scored it `ok` — with 78 batches never launched and one killed in flight. ~$18 of
haiku for 958 of 4,963 descriptions, and the same orchestration overhead re-paid on every
retry pass.

The fix is to take the control loop away from the model. Here Python owns it:

  - read still-undocumented ids straight from the fixture's topology.db (source of truth),
  - split them into small id-chunks,
  - run `parallel` workers, each of which describes ONLY its own ids and never spawns
    sub-agents (so nothing can be abandoned mid-wave),
  - re-read the DB, re-chunk whatever is still undocumented, and go again — until the
    fixture hits `target`, stops making progress, or the caller's deadline expires.

Because the DB is re-read every round, the whole thing is idempotent and resumable: a worker
that dies just leaves its ids undocumented for the next round. Bounding is per-worker
(`chunk_timeout_s`, a handful of ids) plus per-fixture (`max_rounds`, `deadline`), never a
hard kill of a session that is still doing useful work.
"""
from __future__ import annotations

import json
import shutil
import sqlite3
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

from . import agents, fixtures

DEFAULT_KINDS = ["function", "method", "struct", "interface"]

# Substrings that mark a provider usage/rate limit in a worker's error text/raw output.
LIMIT_SIGNS = ("rate limit", "rate_limit", "usage limit", "usage_limit", "overloaded",
               "429", "too many requests", "quota", "insufficient_quota", "resource_exhausted")


# --------------------------------------------------------------------------- #
# topology reads
# --------------------------------------------------------------------------- #
def undocumented(db, kinds=None) -> list[tuple[str, str]]:
    """(id, kind) for every resource of `kinds` that still has no description."""
    kinds = list(kinds or DEFAULT_KINDS)
    conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        ph = ",".join("?" * len(kinds))
        return conn.execute(
            f"SELECT id,kind FROM resources WHERE kind IN ({ph}) "
            "AND (description IS NULL OR TRIM(description)='')", kinds).fetchall()
    finally:
        conn.close()


def lang_of(db) -> str:
    """Dominant language recorded in a topology DB (best-effort, '?' on any error)."""
    try:
        conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        try:
            row = conn.execute("SELECT language,COUNT(*) FROM resources GROUP BY language "
                               "ORDER BY 2 DESC LIMIT 1").fetchone()
        finally:
            conn.close()
        return row[0] if row else "?"
    except Exception:  # noqa: BLE001
        return "?"


# --------------------------------------------------------------------------- #
# worker prompt / result classification
# --------------------------------------------------------------------------- #
def chunk_prompt(rows) -> str:
    """The worker prompt: describe exactly these ids, yourself, no sub-agents."""
    body = "\n".join(f"- {i}   (resource_name: {k.capitalize()})" for i, k in rows)
    return ("You have aracne MCP tools (read, update_description). For EACH resource id below: "
            "call read(<id>) to view its code, then call update_description with "
            "{id:<id>, resource_name:<the resource_name shown>, description:<desc>}.\n\n"
            "Description rules — follow EXACTLY (do NOT just copy the existing docstring/comment):\n"
            "- Write ONE concise sentence (~180 chars max) in your own words describing what it does.\n"
            "- Single line ONLY: no newlines, no code, no examples, no doctests ('>>>'),\n"
            "  no 'Examples/Parameters/Returns' sections, no reST/Sphinx roles (:func:, .. math::).\n"
            "- Start with a verb where natural; don't merely restate the identifier name.\n"
            "Document every id YOURSELF — never spawn sub-agents.\n\nResources:\n" + body)


def is_limit(rr) -> bool:
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
    except Exception:  # noqa: BLE001
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


def _hms(secs) -> str:
    """Compact duration: 7m03s under an hour, 1h42m above it."""
    s = int(max(0, secs))
    return f"{s // 60}m{s % 60:02d}s" if s < 3600 else f"{s // 3600}h{(s % 3600) // 60:02d}m"


# --------------------------------------------------------------------------- #
# live progress
# --------------------------------------------------------------------------- #
class Progress:
    """Live progress for ONE fixture's generation pass.

    On a TTY it owns a single \\r-rewritten line; a daemon thread redraws every `tick`
    seconds so elapsed/ETA/described keep moving while workers sit inside a call that can
    take up to `chunk_timeout_s`. When stdout is piped (nohup, CI) it emits a plain status
    line through `logfn` at most every `line_interval` seconds instead of \\r spam.

    `described` is re-read from the topology DB rather than inferred from completed chunks:
    a worker can persist some ids and still fail, so the DB is the only honest counter.
    """

    def __init__(self, total_chunks, db, described0, total_nodes, kinds=None, logfn=print,
                 tick=2.0, line_interval=30.0, width=30, label=""):
        self.total = max(1, total_chunks)
        self.db, self.nodes = db, max(1, total_nodes)
        self.kinds = list(kinds or DEFAULT_KINDS)
        self.described = described0
        self.logfn, self.tick, self.line_interval, self.width = logfn, tick, line_interval, width
        self.label = label
        self.done = 0
        self.t0 = time.monotonic()
        self.tty = sys.stdout.isatty()
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._last_line = 0.0
        self._th = None

    # -- lifecycle -- #
    def start(self):
        if self.tty:
            self._th = threading.Thread(target=self._loop, daemon=True)
            self._th.start()
            self.draw()
        return self

    def close(self):
        self._stop.set()
        if self._th:
            self._th.join(timeout=self.tick + 1)
        self.clear()

    def _loop(self):
        while not self._stop.wait(self.tick):
            self._refresh()
            self.draw()

    # -- state -- #
    def _refresh(self):
        """Re-read described-count from the DB; workers may hold a write lock, so failure
        is non-fatal (we just keep the last known number)."""
        try:
            self.described = fixtures.description_coverage(self.db, self.kinds)[0]
        except Exception:  # noqa: BLE001
            pass

    def advance(self, n=1):
        self.done += n
        self._refresh()
        if self.tty:
            self.draw()
        else:
            now = time.monotonic()
            if now - self._last_line >= self.line_interval or self.done >= self.total:
                self._last_line = now
                self.logfn(self._render().strip())

    # -- rendering -- #
    def _render(self):
        el = time.monotonic() - self.t0
        tail = (f" {self.done}/{self.total} chunks  {self.described}/{self.nodes} nodes"
                f" ({self.described / self.nodes:.0%})  {_hms(el)}")
        if self.done:
            tail += f" eta {_hms(el / self.done * (self.total - self.done))}"
        cols = shutil.get_terminal_size((100, 24)).columns
        head = f"  {self.label}[" if self.label else "  ["
        width = max(8, min(self.width, cols - len(tail) - len(head) - 4))
        filled = int(round(self.done / self.total * width))
        return f"{head}{'=' * filled}{' ' * (width - filled)}]{tail}"[:cols - 1]

    def draw(self):
        if not self.tty:
            return
        with self._lock:
            sys.stdout.write("\r\033[2K" + self._render())
            sys.stdout.flush()

    def print_over(self, msg):
        """Print a normal log line ABOVE the bar. Clear+print+redraw happen under one
        lock so the repaint thread cannot interleave a bar into the message."""
        if not self.tty:
            print(msg, flush=True)
            return
        with self._lock:
            sys.stdout.write("\r\033[2K" + msg + "\n" + self._render())
            sys.stdout.flush()

    def clear(self):
        """Wipe the bar line (used on close, before the caller's next print)."""
        if not self.tty:
            return
        with self._lock:
            sys.stdout.write("\r\033[2K")
            sys.stdout.flush()


# The bar currently painting on this process's stdout, if any. A caller that logs while a
# fixture is running (gen_descriptions.py) routes lines through `emit` so they land ABOVE the
# bar instead of through it. Single-slot on purpose: progress is only enabled when one
# fixture runs at a time.
ACTIVE_BAR = None


def emit(line):
    """Print a log line without corrupting a live progress bar."""
    bar = ACTIVE_BAR
    if bar is None:
        print(line, flush=True)
    else:
        bar.print_over(line)


class NullBar:
    """Stand-in when progress is disabled: same surface, no output."""

    def advance(self, n=1): pass
    def draw(self): pass
    def clear(self): pass
    def close(self): pass
    def print_over(self, msg): print(msg, flush=True)


# --------------------------------------------------------------------------- #
# the driver
# --------------------------------------------------------------------------- #
def generate_fixture(*, worktree, db, harness, model, kinds=None, chunk_size=15, parallel=5,
                     chunk_timeout_s=600, chunk_max_turns=60, extra_args=None, target=0.95,
                     max_rounds=8, min_gain=3, deadline=None, min_worker_s=90,
                     logfn=print, progress=True,
                     progress_interval=30.0, log_path=None, label="") -> dict:
    """Describe a fixture's undocumented nodes with Python owning the retry loop.

    Rounds repeat until one of: coverage >= `target` / nothing left undocumented (status
    "ok"), a round added fewer than `min_gain` descriptions ("stalled"), most workers in a
    batch reported a provider limit ("rate_limit"), `deadline` (monotonic seconds) passed
    ("timeout"), or `max_rounds` was consumed ("max_rounds"). Every terminal status other
    than "ok" still keeps whatever was written — the DB is the checkpoint.

    Returns {status, before, after, described, total, rounds, chunks, failed_chunks,
    dur_s, limit_hits}.
    """
    kinds = list(kinds or DEFAULT_KINDS)
    t0 = time.monotonic()
    des0, total, cov0 = fixtures.description_coverage(db, kinds)
    described, coverage = des0, cov0
    rounds = chunks_run = failed = limit_hits = 0
    status = "ok"

    def out_of_time(reserve=0):
        """True when the fixture's wall-clock is up. `reserve` refuses to start a NEW wave
        that could not finish anyway — a worker killed at the deadline throws away
        everything it had not yet persisted, so it is cheaper not to launch it."""
        return deadline is not None and time.monotonic() + reserve >= deadline

    def worker_timeout():
        """Never hand a worker more wall-clock than the fixture has left."""
        if deadline is None:
            return chunk_timeout_s
        return max(30, min(chunk_timeout_s, int(deadline - time.monotonic())))

    def record(rr, ids):
        """Append a failed chunk to the postmortem log (why a worker produced nothing)."""
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
                    "raw": getattr(rr, "raw", None),
                }, default=str) + "\n")
        except OSError:
            pass

    restore = patch_opencode_perms(worktree) if harness == "opencode" else (lambda: None)
    try:
        while rounds < max_rounds:
            if coverage >= target:
                break
            if out_of_time():
                status = "timeout"
                break
            rows = undocumented(db, kinds)
            if not rows:
                break
            rounds += 1
            round_start = described
            batches = [rows[i:i + chunk_size] for i in range(0, len(rows), chunk_size)]
            logfn(f"  round {rounds}: {len(rows)} undocumented -> {len(batches)} chunks "
                  f"x{chunk_size} ({parallel} workers)")
            bar = (Progress(len(batches), db, described, total, kinds=kinds, logfn=logfn,
                            line_interval=progress_interval, label=label).start()
                   if progress else NullBar())
            stop_round = None
            globals()["ACTIVE_BAR"] = bar if isinstance(bar, Progress) else None
            try:
                with ThreadPoolExecutor(max_workers=parallel) as ex:
                    i = 0
                    while i < len(batches):
                        if out_of_time(reserve=min_worker_s):
                            stop_round = "timeout"
                            break
                        wave = batches[i:i + parallel]
                        i += parallel
                        futs = {}
                        for ch in wave:
                            futs[ex.submit(_run_chunk, harness, ch, worktree, model,
                                           chunk_max_turns, worker_timeout(), extra_args)] = ch
                        limited = 0
                        for fut in as_completed(futs):
                            rr = fut.result()
                            chunks_run += 1
                            bar.advance()
                            if rr is None or getattr(rr, "is_error", False):
                                failed += 1
                                record(rr, [r[0] for r in futs[fut]])
                            if is_limit(rr):
                                limited += 1
                                limit_hits += 1
                        # Half the wave hitting a provider limit means the account is
                        # throttled: stop cleanly instead of burning the remaining chunks.
                        if limited >= max(1, (len(wave) + 1) // 2):
                            stop_round = "rate_limit"
                            break
            finally:
                globals()["ACTIVE_BAR"] = None
                bar.close()

            described, total, coverage = fixtures.description_coverage(db, kinds)
            logfn(f"  round {rounds}: +{described - round_start} -> {described}/{total} "
                  f"({coverage:.0%}){'  [' + stop_round + ']' if stop_round else ''}")
            if stop_round:
                status = stop_round
                break
            # Stall = the round bought nothing. A SMALL gain is only a stall while there is
            # still a big backlog: near the end (a handful of stubborn nodes left) a round
            # legitimately lands one or two, and killing the fixture there would strand it
            # just short of target.
            gain, remaining = described - round_start, total - described
            if gain == 0 or (gain < min_gain and remaining > 10 * min_gain):
                status = "stalled"
                break
        else:
            status = "max_rounds"
    finally:
        restore()

    described, total, coverage = fixtures.description_coverage(db, kinds)
    if coverage >= target:
        status = "ok"
    return {"status": status, "before": cov0, "after": coverage, "described": described,
            "total": total, "rounds": rounds, "chunks": chunks_run, "failed_chunks": failed,
            "limit_hits": limit_hits, "dur_s": int(time.monotonic() - t0)}


def _run_chunk(harness, rows, worktree, model, max_turns, timeout_s, extra_args):
    """One worker: describe exactly `rows`. Never raises — a dead worker just leaves its
    ids undocumented for the next round."""
    try:
        return agents.run_agent(harness, chunk_prompt(rows), worktree, model,
                                max_turns, timeout_s, extra_args=extra_args)
    except Exception:  # noqa: BLE001  (incl. subprocess.TimeoutExpired)
        return None

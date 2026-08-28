"""Drive headless OpenCode and best-effort capture usage.

`opencode run -m <provider/model> --dir <cwd> --format json --auto
[--agent <a>] "<prompt>"` runs one non-interactive session. With `--format json` OpenCode
emits a stream of raw JSON events (one per line). The exact event schema is version-
dependent, so usage extraction here is DEFENSIVE/best-effort: we scan every JSON object on
stdout for usage-like fields. If none are present, tokens come back 0 (treated as "unknown"
downstream) while turns/duration/text may still be recovered.

Notes vs Claude Code:
  - The prompt is a positional argument (no stdin form), and OpenCode `run` has NO
    `--max-turns` equivalent — `max_turns` is accepted for signature parity but ignored;
    rely on `timeout_s` and the model instead.
  - Model must be `provider/model` (e.g. `deepseek/deepseek-v4-flash`).

Event stream (observed, opencode 1.17.x): newline-delimited JSON objects, each with a
`type` field; failures arrive as {"type":"error","error":{"name":...,"data":{"message":...}}}
and are surfaced below (is_error + message into result_text/raw, so rate/usage limits are
detectable). The token/turn field names for a SUCCESSFUL run are still unverified here (no
working provider key at authoring time): capture one good `opencode run --format json` and
tighten `_extract_usage` / turn counting if they differ.
"""
from __future__ import annotations

import json
import os
import subprocess
import tempfile
import time

from . import claude_driver
from .claude_driver import RunResult


def run_raw(prompt: str, cwd, model: str, max_turns: int, timeout_s: int, *,
            agent: str | None = None, extra_args: list[str] | None = None) -> RunResult:
    cmd = [
        "opencode", "run",
        "-m", model,
        "--dir", str(cwd),
        "--format", "json",
        "--auto",  # opencode 1.17.x auto-approve permissions flag
    ]
    if agent:
        cmd += ["--agent", agent]
    cmd += (extra_args or [])
    cmd.append(prompt)  # opencode run takes the prompt as a positional arg

    t0 = time.monotonic()
    # Same host-Python protection as the Claude driver — see claude_driver.isolated_env.
    proc = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout_s,
                          env=claude_driver.isolated_env())
    dur_ms = int((time.monotonic() - t0) * 1000)
    _maybe_dump_raw(proc.stdout)
    return parse_events(proc.stdout, proc.stderr, proc.returncode, dur_ms)


def _maybe_dump_raw(stdout: str) -> None:
    """When OPENCODE_BENCH_DEBUG is set, persist the raw event stream so the usage/turn
    schema can be verified (or `_extract_usage` tightened) without a special capture. The
    var may be a directory; otherwise the system temp dir is used. Best-effort/no-op on
    error — it must never affect a run."""
    dbg = os.environ.get("OPENCODE_BENCH_DEBUG")
    if not dbg:
        return
    root = dbg if os.path.isdir(dbg) else tempfile.gettempdir()
    path = os.path.join(root, f"opencode_events_{os.getpid()}_{time.monotonic_ns()}.jsonl")
    try:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(stdout or "")
        print(f"[opencode_driver] raw events -> {path}", flush=True)
    except OSError:
        pass


def parse_events(stdout: str, stderr: str, returncode: int, duration_ms: int) -> RunResult:
    events = []
    for line in (stdout or "").splitlines():
        line = line.strip()
        if line.startswith("{"):
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                continue

    inp, out, cache, cost, steps, text = _extract_usage(events)

    # Surface opencode error events (observed shape: {"type":"error","error":{"name":...,
    # "data":{"message":...}}}). Capturing the message lets callers detect provider
    # rate/usage limits (e.g. gen_descriptions.is_limit) from the RunResult.
    errors = [ev for ev in events if ev.get("type") == "error"]
    err_text = ""
    if errors:
        e = errors[-1].get("error") or {}
        data = e.get("data") if isinstance(e.get("data"), dict) else {}
        err_text = str(data.get("message") or e.get("name") or "opencode error")

    if events:
        raw = {"events": len(events)}
        if errors:
            raw["error"] = err_text
            raw["error_events"] = len(errors)
    else:
        raw = {"stdout_tail": (stdout or "")[-2000:], "stderr_tail": (stderr or "")[-2000:],
               "returncode": returncode}

    return RunResult(
        result_text=text or err_text,
        num_turns=steps,
        duration_ms=duration_ms,
        input_tokens=inp,
        output_tokens=out,
        cache_tokens=cache,
        cost_usd=cost,
        is_error=returncode != 0 or bool(errors),
        raw=raw,
    )


def _num(d: dict, *keys) -> int:
    for k in keys:
        v = d.get(k)
        if isinstance(v, (int, float)):
            return int(v)
    return 0


def _iter_dicts(obj):
    """Yield every dict nested anywhere inside obj (depth-first). OpenCode wraps the
    assistant message (which carries usage) deep inside each event, so a flat top-level
    scan misses it."""
    if isinstance(obj, dict):
        yield obj
        for v in obj.values():
            yield from _iter_dicts(v)
    elif isinstance(obj, list):
        for v in obj:
            yield from _iter_dicts(v)


def _usage_fields(src: dict):
    """Extract (input, output, cache) from a token/usage dict, or None if it carries
    none. Handles OpenCode's shape (input/output/reasoning + cache={read,write}) and
    flat OpenAI-style dicts (prompt_tokens/completion_tokens/cache_read...)."""
    inp = _num(src, "input", "input_tokens", "prompt_tokens")
    out = _num(src, "output", "output_tokens", "completion_tokens")
    c = src.get("cache")
    if isinstance(c, dict):  # OpenCode: cache={read,write}
        cache = _num(c, "read", "cache_read", "read_input_tokens") + _num(c, "write", "cache_write")
    else:
        cache = _num(src, "cache", "cache_read", "cache_read_input_tokens")
    if inp or out or cache:
        return inp, out, cache
    return None


def _extract_usage(events: list[dict]):
    """Best-effort usage/turn extraction, robust to nesting and streaming.

    OpenCode streams the SAME assistant message many times as it grows and emits one
    message per turn. So we (a) find each message's `tokens` object nested ANYWHERE in
    the event tree, (b) dedupe by the enclosing message id, keeping the largest (final)
    counts per id, then (c) SUM across distinct messages. Turns = number of distinct
    assistant messages that reported usage (fallback: count of step/tool events). Cost
    is summed per message the same way. Falls back to flat top-level `usage` dicts.
    """
    by_id: dict = {}
    anon: list = []
    step_events = 0
    text = ""

    def record(usage, cost, mid):
        rec = (usage[0], usage[1], usage[2], cost)
        if mid:
            prev = by_id.get(mid)
            if prev is None or sum(rec[:3]) >= sum(prev[:3]):
                by_id[mid] = rec  # keep the final (largest) streamed state for this message
        else:
            anon.append(rec)

    for ev in events:
        if ev.get("type") in ("step", "step-start", "step-finish", "tool", "tool-call"):
            step_events += 1
        for d in _iter_dicts(ev):
            cost = float(d["cost"]) if isinstance(d.get("cost"), (int, float)) else 0.0
            mid = d.get("id") or d.get("messageID") or d.get("message_id")
            tok = d.get("tokens")
            if isinstance(tok, dict):  # OpenCode message-info object
                u = _usage_fields(tok)
                if u:
                    record(u, cost, mid)
                    continue
            usage = d.get("usage")     # flat OpenAI-style event
            if isinstance(usage, dict):
                u = _usage_fields(usage)
                if u:
                    record(u, cost, mid)
        for key in ("result", "text", "content"):
            v = ev.get(key)
            if isinstance(v, str) and v:
                text = v

    records = list(by_id.values()) + anon
    inp = sum(r[0] for r in records)
    out = sum(r[1] for r in records)
    cache = sum(r[2] for r in records)
    cost = sum(r[3] for r in records)
    steps = len(by_id) or step_events
    return inp, out, cache, cost, steps, text

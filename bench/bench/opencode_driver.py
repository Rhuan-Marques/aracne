"""Drive headless OpenCode and best-effort capture usage.

`opencode run -m <provider/model> --dir <cwd> --format json --dangerously-skip-permissions
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

Confirm the real event schema once with `opencode run --format json` and tighten
`_extract_usage` if the field names differ.
"""
from __future__ import annotations

import json
import subprocess
import time

from .claude_driver import RunResult


def run_raw(prompt: str, cwd, model: str, max_turns: int, timeout_s: int, *,
            agent: str | None = None, extra_args: list[str] | None = None) -> RunResult:
    cmd = [
        "opencode", "run",
        "-m", model,
        "--dir", str(cwd),
        "--format", "json",
        "--dangerously-skip-permissions",
    ]
    if agent:
        cmd += ["--agent", agent]
    cmd += (extra_args or [])
    cmd.append(prompt)  # opencode run takes the prompt as a positional arg

    t0 = time.monotonic()
    proc = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout_s)
    dur_ms = int((time.monotonic() - t0) * 1000)
    return parse_events(proc.stdout, proc.stderr, proc.returncode, dur_ms)


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
    return RunResult(
        result_text=text,
        num_turns=steps,
        duration_ms=duration_ms,
        input_tokens=inp,
        output_tokens=out,
        cache_tokens=cache,
        cost_usd=cost,
        is_error=returncode != 0,
        raw={"events": len(events)} if events
        else {"stdout_tail": (stdout or "")[-2000:], "stderr_tail": (stderr or "")[-2000:],
              "returncode": returncode},
    )


def _num(d: dict, *keys) -> int:
    for k in keys:
        v = d.get(k)
        if isinstance(v, (int, float)):
            return int(v)
    return 0


def _extract_usage(events: list[dict]):
    """Best-effort: take the LAST event carrying usage info (usually cumulative)."""
    inp = out = cache = steps = 0
    cost = 0.0
    text = ""
    for ev in events:
        usage = ev.get("usage") or ev.get("tokens")
        if isinstance(usage, dict):
            inp = _num(usage, "input", "input_tokens", "prompt_tokens") or inp
            out = _num(usage, "output", "output_tokens", "completion_tokens") or out
            cache = _num(usage, "cache", "cache_read", "cache_read_input_tokens") or cache
        if isinstance(ev.get("cost"), (int, float)):
            cost = float(ev["cost"])
        if ev.get("type") in ("step", "step-finish", "tool", "message"):
            steps += 1
        for key in ("result", "text", "content"):
            v = ev.get(key)
            if isinstance(v, str) and v:
                text = v
    return inp, out, cache, cost, steps, text

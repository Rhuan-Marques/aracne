"""Drive headless Claude Code and capture usage metrics.

`claude --print --output-format json` runs one non-interactive agent session and prints
a single JSON result object that includes the final text, num_turns, duration_ms,
total_cost_usd and a `usage` block (input/output/cache tokens). Running on a Claude Max
plan, this costs no API dollars yet still reports tokens — which is exactly what we need
to answer "did aracne save tokens / make it faster".
"""
from __future__ import annotations

import json
import os
import re
import subprocess
import tempfile
from dataclasses import dataclass, field

PROMPT_TEMPLATE = """You are an experienced software engineer resolving a real GitHub \
issue in the repository in your current working directory.

Issue / pull-request description:
---
{problem}
---

Make the minimal source-code changes needed to resolve this issue, working directly in \
the files of this repository. Do NOT modify, add, or delete any test files — your change \
will be validated by a separate, hidden test suite. When you are confident the fix is \
complete, stop.
"""


@dataclass
class RunResult:
    result_text: str = ""
    num_turns: int = 0
    duration_ms: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    cache_tokens: int = 0
    cost_usd: float = 0.0
    is_error: bool = False
    raw: dict = field(default_factory=dict)
    # Raw agent stdout, populated only when the caller asked for `stream=True`. Carried on
    # the result (not parsed here) so bench/toolstats.py owns the event vocabulary.
    transcript: str = ""


# Friendly benchmark model names ("opus-4.8") aren't valid `claude --model` strings; the CLI
# wants an alias ("opus"/"sonnet"/"haiku") or a full id ("claude-opus-4-8").
_FRIENDLY_MODEL_RE = re.compile(r"^(opus|sonnet|haiku|fable)-(\d+)(?:\.(\d+))?$")


def normalize_model(model: str) -> str:
    """Map a friendly model name like 'opus-4.8' to a valid `claude --model` id
    ('claude-opus-4-8'). Plain aliases ('opus'/'sonnet'/'haiku') and full ids (anything
    already starting with 'claude-') are returned unchanged."""
    m = _FRIENDLY_MODEL_RE.match((model or "").strip())
    if not m:
        return model
    family, major, minor = m.group(1), m.group(2), m.group(3)
    parts = ["claude", family, major] + ([minor] if minor else [])
    return "-".join(parts)


def isolated_env() -> dict:
    """The environment an agent subprocess runs in, with the host's Python protected.

    WHY THIS EXISTS. The agent runs with `--dangerously-skip-permissions` and a shell, and
    SWE-bench tasks routinely build the repository under test — `pip install -e .` is a
    normal step. Unsandboxed, that writes into the HOST's user site-packages.

    It really happened: solving `pytest-dev__pytest-5495` installed the pytest fixture into
    ~/.local/lib/python3.12/site-packages, so every `pytest` on the account became pytest
    4.6 running from a benchmark checkout. When the ephemeral clone for that cell was
    deleted at the end of the run, the .pth was left pointing at nothing and the account had
    no working pytest at all.

    PYTHONUSERBASE redirects `pip install --user` (which is where pip lands under PEP 668)
    into a throwaway directory, and PYTHONNOUSERSITE stops the agent importing whatever the
    host happens to have installed — which also makes runs more reproducible.

    This is a guard rail, not a sandbox. An agent can still write anywhere the user can.
    Real isolation means running the agent in a container.
    """
    env = dict(os.environ)
    env["PYTHONUSERBASE"] = tempfile.mkdtemp(prefix="aracne-bench-userbase-")
    env["PYTHONNOUSERSITE"] = "1"
    env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    return env


def run_raw(prompt: str, cwd, model: str, max_turns: int, timeout_s: int,
            extra_args: list[str] | None = None, stream: bool = False,
            effort: str | None = None) -> RunResult:
    """Run one headless Claude Code session in `cwd`, feeding `prompt` verbatim on stdin
    (so it is never subject to argv length limits). `prompt` may be a slash command such
    as "/descriptions-generate".

    With `stream=True` the CLI emits NDJSON (one event per line) instead of a single
    object, and the whole stream is carried back on `RunResult.transcript` for
    bench/toolstats.py. The terminal `{"type":"result",...}` event has the SAME shape as
    the non-streaming output, and `parse_output` scans from the end, so token/turn/cost
    extraction is identical either way. `stream-json` requires `--verbose`.
    """
    model = normalize_model(model)
    fmt = ["--output-format", "stream-json", "--verbose"] if stream \
        else ["--output-format", "json"]
    cmd = [
        "claude", "--print", *fmt,
        "--model", model,
        "--max-turns", str(max_turns),
        *(["--effort", effort] if effort else []),
        "--dangerously-skip-permissions",
        *(extra_args or []),
    ]
    proc = subprocess.run(
        cmd, input=prompt, cwd=str(cwd), text=True,
        capture_output=True, timeout=timeout_s, env=isolated_env(),
    )
    rr = parse_output(proc.stdout, proc.stderr, proc.returncode)
    if stream:
        rr.transcript = proc.stdout or ""
    return rr


def run_claude(problem: str, cwd, model: str, max_turns: int, timeout_s: int,
               extra_args: list[str] | None = None, stream: bool = False,
               effort: str | None = None) -> RunResult:
    """Run one headless Claude Code session to solve a benchmark task (wraps the issue
    text in the task-solving template, then delegates to `run_raw`)."""
    return run_raw(PROMPT_TEMPLATE.format(problem=problem), cwd, model, max_turns,
                   timeout_s, extra_args, stream=stream, effort=effort)


def parse_output(stdout: str, stderr: str, returncode: int) -> RunResult:
    """Parse the JSON result object emitted by `claude --output-format json`."""
    data: dict = {}
    for line in reversed((stdout or "").strip().splitlines()):
        line = line.strip()
        if line.startswith("{"):
            try:
                data = json.loads(line)
                break
            except json.JSONDecodeError:
                continue

    usage = data.get("usage") or {}
    cache = int(usage.get("cache_creation_input_tokens", 0) or 0) + \
        int(usage.get("cache_read_input_tokens", 0) or 0)

    return RunResult(
        result_text=str(data.get("result", "") or ""),
        num_turns=int(data.get("num_turns", 0) or 0),
        duration_ms=int(data.get("duration_ms", 0) or 0),
        input_tokens=int(usage.get("input_tokens", 0) or 0),
        output_tokens=int(usage.get("output_tokens", 0) or 0),
        cache_tokens=cache,
        cost_usd=float(data.get("total_cost_usd", 0.0) or 0.0),
        is_error=bool(data.get("is_error", False)) or returncode != 0,
        raw=data or {
            "stdout_tail": (stdout or "")[-2000:],
            "stderr_tail": (stderr or "")[-2000:],
            "returncode": returncode,
        },
    )

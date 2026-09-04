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
import shutil
import subprocess
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

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


from . import netshim

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


def isolated_env(isolate_operator_config: bool = False, deny_repo: str | None = None,
                 guard_log: str | None = None, extra_env: dict | None = None) -> dict:
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

    `isolate_operator_config` additionally points the agent at a scratch Claude config dir --
    see _operator_free_config_dir for why that matters to any A/B claim this harness makes.

    `deny_repo` ("org/repo") puts netshim's `curl`/`wget` wrappers on PATH so the agent can
    still reach the whole web EXCEPT the repository whose PR diff is the answer to this task.
    See bench/bench/netshim.py for the measurement that motivated it.
    """
    env = dict(os.environ)
    # Applied BEFORE anything else so it cannot silently lose to an inherited value: this is
    # what makes the agent compile with the toolchain its verifier uses. See
    # atlas_prepare.probe_runtime for the failure it exists to prevent.
    env.update(extra_env or {})
    env["PYTHONUSERBASE"] = tempfile.mkdtemp(prefix="aracne-bench-userbase-")
    env["PYTHONNOUSERSITE"] = "1"
    env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    if isolate_operator_config:
        env["CLAUDE_CONFIG_DIR"] = _operator_free_config_dir()
    if guard_log:
        # The guard writes one line per decision here. It is the ONLY place that knows a
        # command was answered from the topology: the rewrite reaches the model as
        # `updatedInput`, and the transcript keeps what the model wrote, so counting
        # `arac cmd` in the transcript reports zero interceptions no matter how many fired.
        env["ARACNE_GUARD_LOG"] = guard_log
    return netshim.apply(env, deny_repo)


# Cached so every cell in a run shares one scratch config dir rather than re-copying
# credentials per subprocess.
_ISOLATED_CONFIG_DIR: str | None = None


def _operator_free_config_dir() -> str:
    """A Claude config dir carrying credentials and nothing else.

    WHY. The agent inherits HOME, so it reads the operator's `~/.claude/settings.json` and
    `~/.claude/CLAUDE.md` -- in BOTH arms. In the compact-blocked-20260830c run that meant a
    standing "prefer Bash `cat`/`grep` over the file tools" instruction was active throughout:
    the baseline made 357 Bash calls and *zero* Read calls, and the aracne arm spent one guard
    denial per run fighting the same instruction before it would touch the topology. Neither
    arm was measuring what the run was asking about.

    Credentials are copied because losing auth kills the entire matrix; everything that could
    carry an instruction -- settings, memory, agents, commands -- is deliberately left behind.
    """
    global _ISOLATED_CONFIG_DIR
    if _ISOLATED_CONFIG_DIR:
        return _ISOLATED_CONFIG_DIR
    scratch = tempfile.mkdtemp(prefix="aracne-bench-claude-cfg-")
    source = Path(os.environ.get("CLAUDE_CONFIG_DIR") or (Path.home() / ".claude"))
    for name in (".credentials.json", "credentials.json"):
        src = source / name
        if src.is_file():
            try:
                shutil.copy2(src, Path(scratch) / name)
            except OSError:
                pass
    _ISOLATED_CONFIG_DIR = scratch
    return scratch


def run_raw(prompt: str, cwd, model: str, max_turns: int, timeout_s: int,
            extra_args: list[str] | None = None, stream: bool = False,
            effort: str | None = None, isolate_operator_config: bool = False,
            allowed_tools: list[str] | None = None,
            builtin_tools: list[str] | None = None,
            deny_repo: str | None = None,
               guard_log: str | None = None, extra_env: dict | None = None) -> RunResult:
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
        # `--allowedTools` is a PERMISSION allow-list: it does not change which tools the
        # model is OFFERED. Measured directly -- a smoke cell with it set still opened with a
        # ToolSearch call, because all 26 built-ins were still in the list and the aracne
        # tools were still deferred behind it.
        *(["--allowedTools", ",".join(allowed_tools)] if allowed_tools else []),
        # `--tools` is the flag that trims the built-in SURFACE, and that is what keeps the
        # aracne MCP tools in the model's front tool list. ToolSearch calls made purely to
        # discover tools the project's own CLAUDE.md had already named by their exact
        # identifiers cost 28 calls across 24 of 27 cells in compact-blocked-20260830c and 26
        # across 25 of 27 in compact-blocked-after-bs-20260830c.
        *(["--tools", ",".join(builtin_tools)] if builtin_tools else []),
        *(extra_args or []),
    ]
    proc = subprocess.run(
        cmd, input=prompt, cwd=str(cwd), text=True,
        capture_output=True, timeout=timeout_s,
        env=isolated_env(isolate_operator_config, deny_repo, guard_log, extra_env),
    )
    rr = parse_output(proc.stdout, proc.stderr, proc.returncode)
    if stream:
        rr.transcript = proc.stdout or ""
    return rr


def run_claude(problem: str, cwd, model: str, max_turns: int, timeout_s: int,
               extra_args: list[str] | None = None, stream: bool = False,
               effort: str | None = None, isolate_operator_config: bool = False,
               allowed_tools: list[str] | None = None,
               builtin_tools: list[str] | None = None,
               deny_repo: str | None = None,
               guard_log: str | None = None, extra_env: dict | None = None) -> RunResult:
    """Run one headless Claude Code session to solve a benchmark task (wraps the issue
    text in the task-solving template, then delegates to `run_raw`)."""
    return run_raw(PROMPT_TEMPLATE.format(problem=problem), cwd, model, max_turns,
                   timeout_s, extra_args, stream=stream, effort=effort,
                   isolate_operator_config=isolate_operator_config,
                   allowed_tools=allowed_tools, builtin_tools=builtin_tools,
                   deny_repo=deny_repo, guard_log=guard_log, extra_env=extra_env)


def parse_output(stdout: str, stderr: str, returncode: int) -> RunResult:
    """Parse the JSON result object emitted by `claude --output-format json`."""
    # Take the terminal {"type":"result"} event, NOT simply the last JSON line. When the
    # session spawns a background task the CLI can emit a {"type":"system","subtype":
    # "task_notification"} object AFTER the result, and parsing that as the result yields
    # num_turns=0, an empty result_text and a bogus is_error -- the whole cell is then
    # recorded as `agent_error: {"type":"system","subtype":"task_notification",...}` and,
    # because a hard error stops scheduling, one such cell strands the entire matrix.
    # Falls back to the last parseable object so a truncated stream still reports something.
    data: dict = {}
    fallback: dict = {}
    for line in reversed((stdout or "").strip().splitlines()):
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict) and obj.get("type") == "result":
            data = obj
            break
        if not fallback and isinstance(obj, dict):
            fallback = obj
    if not data:
        data = fallback

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


def cli_version() -> dict:
    """The `claude` binary this run will drive, and its version.

    RECORDED, NOT ASSUMED. A paired comparison whose arms ran months apart also compares two
    Claude Code releases, and nothing in the harness made that visible: the imported baseline
    in every run through batched-20260901a came from `opus-medium` (19 August) while the
    treatment arm ran on whatever was installed the day of the run. Both arms of a single run
    invoke the same resolved binary, so recording it once per run is enough to prove it -- and
    a stored version is what lets a LATER run tell whether it is comparable to this one.
    """
    import shutil
    path = shutil.which("claude") or "claude"
    try:
        out = subprocess.run([path, "--version"], capture_output=True, text=True,
                             timeout=30).stdout.strip()
    except (OSError, subprocess.SubprocessError) as exc:
        out = f"unavailable: {exc}"
    return {"path": path, "version": out}

"""The agent subprocess must not be able to install into the host's Python.

This is not hypothetical. Solving `pytest-dev__pytest-5495` ran `pip install -e .` inside
its working copy, which wrote a .pth into ~/.local/lib/python3.12/site-packages pointing at
the benchmark's scratch directory. Every `pytest` on the account then ran pytest 4.6 from a
benchmark checkout — and when the ephemeral clone was deleted at the end of the cell, the
account was left with no working pytest at all.

The agent runs with --dangerously-skip-permissions and a shell, and building the repo under
test is a normal part of a SWE-bench task, so this will recur on any Python fixture unless
the environment is redirected.
"""
from __future__ import annotations

import os
import subprocess
import sys

from bench import claude_driver, opencode_driver


def test_user_base_is_redirected_away_from_the_host():
    env = claude_driver.isolated_env()
    base = env["PYTHONUSERBASE"]
    assert base, "PYTHONUSERBASE must be set"
    home = os.path.expanduser("~")
    assert not base.startswith(os.path.join(home, ".local")), \
        f"PYTHONUSERBASE {base} still points into the host's user site"
    assert env["PYTHONNOUSERSITE"] == "1"


def test_each_run_gets_its_own_user_base():
    # Sharing one directory across cells would let one task's install leak into the next.
    assert claude_driver.isolated_env()["PYTHONUSERBASE"] != \
        claude_driver.isolated_env()["PYTHONUSERBASE"]


def test_python_actually_honours_the_redirect():
    # Assert the interpreter agrees, not just that we set a variable.
    env = claude_driver.isolated_env()
    out = subprocess.run(
        [sys.executable, "-c", "import site; print(site.getuserbase())"],
        capture_output=True, text=True, env=env, timeout=60,
    ).stdout.strip()
    assert out == env["PYTHONUSERBASE"], f"interpreter used {out}"


def test_both_drivers_pass_the_isolated_env():
    # A driver that forgets `env=` silently reopens the hole.
    # Matched on the call NAME, not on `isolated_env()` exactly: the claude driver passes it
    # an argument now (operator-config isolation), and a test that pins the argument list
    # fails for a reason that has nothing to do with what it is guarding.
    for mod, name in ((claude_driver, "claude_driver"), (opencode_driver, "opencode_driver")):
        src = open(mod.__file__, encoding="utf-8").read()
        assert "isolated_env(" in src, f"{name} does not use isolated_env"
        run_call = src[src.index("subprocess.run("):]
        assert "env=" in run_call[:400], f"{name} calls subprocess.run without env="


def test_baseline_import_is_idempotent_under_resume(tmp_path):
    """--resume already loads every row from runs.jsonl, including previously imported
    ones. Without a run_key guard, --baseline-from would append them a second time and the
    paired analysis would see duplicate baseline rows for the same task."""
    import json
    import run_benchmark as rb

    src = tmp_path / "src-run"
    src.mkdir()
    row = {"run_key": "t|baseline|0", "instance_id": "t", "seed": 0, "arm": "baseline",
           "language": "python", "num_turns": 3, "input_tokens": 1, "cache_tokens": 2,
           "output_tokens": 1, "duration_ms": 1, "cost_usd": 0.0, "scan_time_s": 0.0,
           "success": True, "error": None, "outcome": "correct"}
    (src / "runs.jsonl").write_text(json.dumps(row) + "\n")
    (src / "run_meta.json").write_text(json.dumps({"model": "haiku", "max_turns": 80}))

    class T:
        key = "t"
    matrix = [(T(), "aracne", 0)]
    cfg = {"baseline_from": str(src), "arms": ["aracne"], "model": "haiku", "max_turns": 80}

    done, all_rows = {"t|baseline|0"}, [dict(row)]        # as --resume would leave it
    rb._import_prior_rows(cfg, matrix, tmp_path / "runs.jsonl", done, all_rows)
    assert len(all_rows) == 1, f"row was imported twice: {all_rows}"


def test_a_row_with_no_turns_and_no_tokens_is_not_a_measurement():
    """The CLI can exit without a usable result event — a dropped connection, or a crash
    after the last assistant message — leaving `error` unset on a row that recorded zero
    turns and zero tokens. `vuejs__core-8911` did exactly that after 67 real tool calls.

    Two things must treat it as unusable: --continue has to re-run it (keeping it would
    freeze a non-result into the run), and the metrics must exclude it (averaging a zero
    into mean_turns understates the arm it lands in)."""
    import run_benchmark as rb
    from bench import rowmetrics

    empty = {"run_key": "t|aracne|0", "num_turns": 0, "input_tokens": 0,
             "cache_tokens": 0, "error": None, "outcome": "unknown"}
    real = {"run_key": "u|aracne|0", "num_turns": 12, "input_tokens": 5,
            "cache_tokens": 400, "error": None, "outcome": "correct"}

    assert not rb._keep_on_continue(empty, False), "--continue must re-run an empty row"
    assert rb._keep_on_continue(real, False), "--continue must keep a real row"
    assert not rowmetrics.has_metrics(empty), "an empty row must not enter the means"
    assert rowmetrics.has_metrics(real)

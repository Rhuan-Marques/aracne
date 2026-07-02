"""Run the benchmark matrix.

For each (task, arm, seed): prepare the working directory (an ephemeral base-commit clone
for baseline, the warm canonical worktree for aracne), drive headless Claude Code, extract
the agent's diff as the prediction patch, and return a result row plus the patch (for later
Docker grading).

Baseline runs use ephemeral clones under <out>/work; the aracne arm reuses its path-pinned
canonical worktree under the fixtures tree and is never deleted here.
"""
from __future__ import annotations

import json
import re
import shutil
import subprocess
import time
from pathlib import Path

from . import agents, arms, fixtures
from .gitutil import run_git
from .sources import Task


def _short(key: str) -> str:
    return re.sub(r"[^A-Za-z0-9._-]", "-", key)[:80]


def _extract_patch(workdir: Path, base_commit: str) -> str:
    """Stage everything the agent changed and diff it against the pristine base commit,
    excluding aracne's own artifacts so the patch is pure source change."""
    run_git(["add", "-A"], cwd=workdir, check=False)
    excludes = [f":(exclude){p}" for p in fixtures.ARACNE_ARTIFACTS]
    res = run_git(["diff", "--cached", base_commit, "--", ".", *excludes], cwd=workdir, check=False)
    return res.stdout


def error_row(task: Task, arm: str, seed: int, error: str) -> dict:
    return {
        "run_key": f"{task.key}|{arm}|{seed}",
        "instance_id": task.key, "language": task.language, "source": task.source,
        "arm": arm, "seed": seed, "scan_time_s": 0.0,
        "input_tokens": 0, "output_tokens": 0, "cache_tokens": 0,
        "num_turns": 0, "duration_ms": 0, "cost_usd": 0.0,
        "has_patch": False, "patch_path": None, "success": None, "error": error,
        "org": task.raw.get("org"), "repo": task.raw.get("repo"),
        "number": task.raw.get("number"),
    }


def run_one(task: Task, arm: str, seed: int, cfg: dict, out_dir: Path,
            repos_dir: Path, work_dir: Path, fixtures_root: Path):
    """Execute one matrix cell. Returns (row, patch_text)."""
    row = error_row(task, arm, seed, None)
    patch = ""

    ephem = work_dir / f"{task.repo_slug()}__{_short(task.key)}__{arm}__s{seed}"

    t0 = time.monotonic()
    try:
        workdir = arms.prepare_workdir(arm, task, cfg, repos_dir, ephem, fixtures_root)
    except RuntimeError as e:  # guardrail / setup failure — record and skip
        row["error"] = str(e)
        return row, ""
    except subprocess.CalledProcessError as e:
        row["error"] = f"arm setup failed: {(e.stderr or str(e))[-400:]}"
        return row, ""
    row["scan_time_s"] = round(time.monotonic() - t0, 2)  # reuse field as setup time

    try:
        rr = agents.run_agent(
            cfg["run_harness"], agents.task_prompt(task.problem_statement),
            workdir, cfg["model"], cfg["max_turns"], cfg["timeout_s"],
        )
        row.update(
            input_tokens=rr.input_tokens, output_tokens=rr.output_tokens,
            cache_tokens=rr.cache_tokens, num_turns=rr.num_turns,
            duration_ms=rr.duration_ms, cost_usd=round(rr.cost_usd, 4),
        )
        if rr.is_error:
            # `claude --print` puts its failure message in `result` when is_error; keep it
            # (falling back to a slice of the raw JSON) so the cause isn't lost as "agent_error".
            detail = (rr.result_text or "").strip() or json.dumps(rr.raw)
            row["error"] = f"agent_error: {detail[:500]}"
    except subprocess.TimeoutExpired:
        row["error"] = "agent timeout"
    except Exception as e:  # noqa: BLE001 - record and continue the matrix
        row["error"] = f"agent error: {e}"

    patch = _extract_patch(workdir, task.base_commit)
    if patch.strip():
        patch_path = out_dir / "patches" / f"{_short(task.key)}__{arm}__s{seed}.patch"
        patch_path.parent.mkdir(parents=True, exist_ok=True)
        patch_path.write_text(patch)
        row["has_patch"] = True
        row["patch_path"] = str(patch_path)

    # Cleanup: ephemeral baseline checkouts only. NEVER delete the aracne canonical
    # worktree — it is path-pinned and reused across seeds/runs.
    if arm == "baseline" and not cfg.get("keep_workdir"):
        shutil.rmtree(ephem, ignore_errors=True)

    return row, patch

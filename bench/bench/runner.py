"""Run the benchmark matrix.

For each (task, arm, seed): prepare the working directory (an ephemeral base-commit clone
for baseline, the warm canonical worktree for aracne), drive headless Claude Code, extract
the agent's diff as the prediction patch, and return a result row plus the patch (for later
Docker grading).

Baseline runs use ephemeral clones under <out>/work; the aracne arm reuses its path-pinned
canonical worktree under the fixtures tree and is never deleted here.

Cells may run concurrently (`run_parallel`), so the two SHARED resources are taken under a
keyed lock: an aracne cell owns its fixture's canonical worktree for its whole run, and any
cell owns its repo's `_repos` cache entry while cloning. See bench/bench/locks.py.
"""
from __future__ import annotations

import json
import re
import shutil
import subprocess
import time
from pathlib import Path

from . import agents, arms, fixtures, locks, netshim, outcome, toolstats
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
        # Terminal outcome of the row; `timeout_stage` says WHERE the clock ran out
        # ("agent" | "grading" | None). See bench/bench/outcome.py.
        "outcome": outcome.UNKNOWN, "timeout_stage": None, "grade_error": None,
        "org": task.raw.get("org"), "repo": task.raw.get("repo"),
        "number": task.raw.get("number"),
        # Describable-node count of this repo's topology, from its fixture meta. Recorded on
        # the ROW (not looked up later) so size-stratified analysis works from runs.jsonl
        # alone — including in `rescore`, which never touches the fixtures dir.
        "repo_nodes": None,
        # Did `arac scanner run` watch this cell? Only the aracne arm starts one, and it is
        # best-effort — recorded so a freshness mechanism that did not run cannot be mistaken
        # for one that ran and had nothing to do.
        "bg_scanner": False,
        # Per-tool telemetry folded from the agent transcript (bench/toolstats.py). Always
        # present, so a backend that reports nothing is distinguishable from a real zero
        # via `has_transcript`/`transcript_path`.
        "transcript_path": None,
        **toolstats.row_fields(toolstats.empty_stats()),
    }


def _hit_turn_cap(rr, cfg: dict) -> bool:
    """Did this errored run die because it exhausted `max_turns` rather than breaking?

    Only Claude Code enforces the cap (OpenCode ignores `max_turns` — see opencode_driver),
    so the turn count is evidence of exhaustion for that harness alone; anywhere else an
    error is an error and stays fatal to the matrix.
    """
    return (cfg.get("run_harness") == "claude_code"
            and (rr.num_turns or 0) >= int(cfg["max_turns"]))


def run_one(task: Task, arm: str, seed: int, cfg: dict, out_dir: Path,
            repos_dir: Path, work_dir: Path, fixtures_root: Path):
    """Execute one matrix cell. Returns (row, patch_text).

    Thread-safe: an aracne cell holds its fixture's lock for the WHOLE run, because the
    canonical worktree is reset in place and reused — two concurrent aracne runs of one
    fixture would clobber each other's checkout. Baseline cells work in their own ephemeral
    clone and need no such lock. With `run_parallel: 1` the locks are uncontended no-ops.
    """
    if arms.is_aracne_arm(arm):
        with locks.fixture(fixtures.fixture_key(task)):
            return _run_cell(task, arm, seed, cfg, out_dir, repos_dir, work_dir, fixtures_root)
    return _run_cell(task, arm, seed, cfg, out_dir, repos_dir, work_dir, fixtures_root)


def _run_cell(task: Task, arm: str, seed: int, cfg: dict, out_dir: Path,
              repos_dir: Path, work_dir: Path, fixtures_root: Path):
    """The body of one matrix cell, already holding whatever lock the arm requires."""
    row = error_row(task, arm, seed, None)
    patch = ""

    ephem = work_dir / f"{task.repo_slug()}__{_short(task.key)}__{arm}__s{seed}"

    t0 = time.monotonic()
    try:
        # The repo lock covers the shared `_repos` clone cache: concurrent cells drawn from
        # the same repository must not race to create or fetch its cache entry.
        with locks.repo_cache(task.repo_slug()):
            workdir = arms.prepare_workdir(arm, task, cfg, repos_dir, ephem, fixtures_root)
    except RuntimeError as e:  # guardrail / setup failure — record and skip
        row["error"] = str(e)
        return row, ""
    except subprocess.CalledProcessError as e:
        row["error"] = f"arm setup failed: {(e.stderr or str(e))[-400:]}"
        return row, ""
    row["scan_time_s"] = round(time.monotonic() - t0, 2)  # reuse field as setup time
    # Both arms share one fixture per task, so this is a property of the TASK and is
    # identical across the pair — exactly what a size stratification needs.
    meta = fixtures.read_meta(task, fixtures_root) or {}
    row["repo_nodes"] = meta.get("total")

    try:
        # The watcher owns topology freshness for the aracne arm, so reads do not each pay
        # for an incremental scan. It must wrap the agent call and nothing else: started
        # after the workdir is restored (there is no DB to watch before that) and stopped
        # before grading reads the patch.
        with arms.background_scanner(arm, workdir, cfg) as scanning:
            row["bg_scanner"] = scanning
            rr = agents.run_agent(
                cfg["run_harness"], agents.task_prompt(task.problem_statement),
                workdir, cfg["model"], cfg["max_turns"], cfg["timeout_s"],
                stream=True, effort=cfg.get("effort"),
                isolate_operator_config=bool(cfg.get("isolate_operator_config")),
                allowed_tools=cfg.get("allowed_tools") or None,
                builtin_tools=cfg.get("builtin_tools") or None,
                # The web stays open; the repository holding this task's answer does not.
                deny_repo=(netshim.deny_target(task.clone_url)
                           if cfg.get("deny_answer_key", True) else None),
            )
        row.update(
            input_tokens=rr.input_tokens, output_tokens=rr.output_tokens,
            cache_tokens=rr.cache_tokens, num_turns=rr.num_turns,
            duration_ms=rr.duration_ms, cost_usd=round(rr.cost_usd, 4),
        )
        # Fold the transcript into counters and persist the size-reduced form. Written
        # under out_dir (NEVER inside the worktree: restore() git-cleans it before the
        # next run, which would delete the telemetry we just wrote).
        stats, reduced = toolstats.summarize(rr.transcript, netshim.deny_target(task.clone_url))
        row.update(toolstats.row_fields(stats))
        if reduced:
            tpath = out_dir / "transcripts" / f"{_short(task.key)}__{arm}__s{seed}.jsonl"
            tpath.parent.mkdir(parents=True, exist_ok=True)
            tpath.write_text(reduced)
            row["transcript_path"] = str(tpath)
        if rr.is_error and _hit_turn_cap(rr, cfg):
            # Turn exhaustion is a BUDGET outcome, not a broken step: the agent was still
            # working when the cap cut it off. The CLI reports it as a bare is_error with no
            # `result` text, so it is recognised by the turn count rather than a message.
            # Recorded like a wall-clock timeout so one capped task cannot strand the matrix.
            row["timeout_stage"] = outcome.TURNS
            row["error"] = (f"agent turn limit: hit max_turns={cfg['max_turns']} "
                            f"after {rr.num_turns} turns")
        elif rr.is_error:
            # `claude --print` puts its failure message in `result` when is_error; keep it
            # (falling back to a slice of the raw JSON) so the cause isn't lost as "agent_error".
            detail = (rr.result_text or "").strip() or json.dumps(rr.raw)
            row["error"] = f"agent_error: {detail[:500]}"
    except subprocess.TimeoutExpired:
        # A wall-clock timeout is its OWN outcome: the agent never got to finish, so the
        # run is neither a fail nor a pass. Recorded, never graded, never fatal to the matrix.
        row["timeout_stage"] = outcome.AGENT
        row["error"] = f"agent timeout after {cfg['timeout_s']}s"
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

    outcome.stamp(row)
    return row, patch

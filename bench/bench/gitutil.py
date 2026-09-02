"""Shared git helpers.

Lives in its own module so both fixtures.py (warm-fixture lifecycle) and runner.py
(per-run baseline checkouts) can reuse the exact same clone/checkout primitives without
importing each other (which would be circular).
"""
from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

from .sources import Task


def run_git(args: list[str], cwd, check: bool = True) -> subprocess.CompletedProcess:
    """Run `git <args>` in `cwd`, capturing output. Raises on failure when check=True."""
    return subprocess.run(["git", *args], cwd=str(cwd), check=check,
                          capture_output=True, text=True)


def ensure_repo_cache(task: Task, repos_dir: Path) -> Path:
    """Maintain one cached full clone per `task.repo_slug()` under `repos_dir`.

    Clones the repo (quietly) on first use and returns the cache path; subsequent calls
    are a no-op so re-running the matrix costs no extra network.
    """
    cache = repos_dir / task.repo_slug()
    if not (cache / ".git").exists():
        repos_dir.mkdir(parents=True, exist_ok=True)
        run_git(["clone", "--quiet", task.clone_url, str(cache)], cwd=repos_dir)
    return cache


def clone_at(task: Task, cache: Path, dest: Path, base_commit: str) -> None:
    """Create a working copy at `dest` containing `base_commit` and its ANCESTORS ONLY.

    WHY NOT `git clone` + `git checkout`. That is what this did, and it shipped the answer with
    the task. A clone carries every ref, so the fixture for casbin__casbin-1512 held 342 refs
    and 108 commits AFTER the base -- including a2a6c3a, "feat: add BLP (Bell-LaPadula) model
    support and test (#1512)", which is the PR the task asks the agent to reproduce. An agent
    that types `git log --all` finds it, and `git show <sha> | git apply -` solves the task
    without reading any code. That was observed, not hypothesised, in hard9-20260902b, and
    `git log --all` / `git show <sha>` appear in the transcripts of every prior run in this
    repository. `deny_answer_key` does not touch it: the answer never crosses the network.

    Fetching the base commit by SHA gets its whole ancestry and nothing else -- git fetches a
    commit's parents, never its children -- so the agent keeps the real history a developer
    would have while the future becomes unreachable AND absent: `git show` on a descendant
    fails with "unknown revision", because the object was never transferred.

    The fetch is local (from the shared `_repos` cache), so this costs an object copy, not a
    network round trip.
    """
    if dest.exists():
        shutil.rmtree(dest)
    cache = Path(cache).resolve()
    dest = Path(dest).resolve()
    dest.mkdir(parents=True, exist_ok=True)
    run_git(["init", "--quiet"], cwd=dest)
    # --no-tags matters: a tag pointing at a later release would drag its history back in.
    run_git(["fetch", "--quiet", "--no-tags", str(cache), base_commit], cwd=dest)
    run_git(["checkout", "--quiet", "--detach", "FETCH_HEAD"], cwd=dest)
    run_git(["reset", "--hard", "--quiet", base_commit], cwd=dest)
    run_git(["clean", "-ffdxq"], cwd=dest)

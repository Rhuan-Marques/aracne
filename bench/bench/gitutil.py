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
    """Create a fresh local clone of `cache` at `dest`, pinned to `base_commit`.

    `dest` is removed first if it exists; the clone uses --no-hardlinks so the working
    copy is fully independent of the cache, then is hard-reset to the base commit.
    """
    # Resolve both paths before running git. The clone runs with cwd=dest.parent, so a
    # RELATIVE dest (which is what a relative --out produces) would be re-resolved against
    # that cwd and the repo would land in a nested path -- the clone reports success and the
    # checkout below then fails with a bare ENOENT on a directory git never created.
    cache = Path(cache).resolve()
    dest = Path(dest).resolve()
    if dest.exists():
        shutil.rmtree(dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    run_git(["clone", "--quiet", "--no-hardlinks", str(cache), str(dest)], cwd=dest.parent)
    run_git(["checkout", "--quiet", base_commit], cwd=dest)
    run_git(["reset", "--hard", base_commit], cwd=dest)
    run_git(["clean", "-ffdxq"], cwd=dest)

"""Process-wide keyed locks for concurrent benchmark runs.

The A/B matrix can run several cells at once (`run_parallel`), but two resources are
SHARED and must not be entered twice at the same time:

  - the aracne arm's canonical worktree. It is path-pinned (the topology DB may embed
    absolute paths), so every aracne run of a fixture resets and reuses the SAME directory
    — two concurrent runs would overwrite each other's checkout mid-session. Held for the
    whole cell, from `git reset --hard` to patch extraction.
  - the `_repos` bare-clone cache. Two cells whose tasks share a repo would otherwise race
    to create/fetch the same cache entry.

Locks are per-key and created on demand; a key is never removed (the set is bounded by the
matrix, and a stale mutex costs nothing). Threads only — the matrix runs in one process.
"""
from __future__ import annotations

import threading

_registry: dict[str, threading.Lock] = {}
_registry_guard = threading.Lock()


def keyed(key: str) -> threading.Lock:
    """The one lock for `key`, creating it on first use. Use as a context manager."""
    with _registry_guard:
        lock = _registry.get(key)
        if lock is None:
            lock = _registry[key] = threading.Lock()
        return lock


def fixture(key: str) -> threading.Lock:
    """Guards one fixture's canonical worktree (the aracne arm's working directory)."""
    return keyed(f"fixture:{key}")


def repo_cache(slug: str) -> threading.Lock:
    """Guards one repository's entry in the shared `_repos` clone cache."""
    return keyed(f"repo:{slug}")

#!/usr/bin/env python3
"""Regression tests for base-commit hygiene in the warm-fixture lifecycle
(bench/bench/fixtures.py).

THE BUG. The canonical worktree is path-pinned and reused, so a benchmark run leaves the
agent's edits in it until the next `restore`. `freeze` copied `.aracne` out of that worktree
without checking, and the result was a snapshot describing SOLVED source: the pallets__flask
fixture's frozen DB indexed `Config` at lines 29-347 of a file that is 338 lines at
base_commit, and carried a description for `Config.from_file` documenting the `mode` parameter
the task exists to add.

Both halves hurt. The line numbers made every read of that class fail with a range error the
model could not distinguish from a bad ID — all three seeds of that instance were spent on it.
The descriptions leaked the answer.

The fix is that every operation which captures or generates from the worktree puts it back on
base_commit first, and that `restore` re-indexes what it just reset. These tests use a real
local git repo; no network, no Docker, no `arac`.

    python3 bench/test_fixturehygiene.py
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
import types
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import fixtures  # noqa: E402

_RESULTS: list[tuple[str, bool]] = []


def check(name: str, cond) -> None:
    _RESULTS.append((name, bool(cond)))
    print(("PASS  " if cond else "FAIL  ") + name)


def git(args, cwd):
    return subprocess.run(["git", *args], cwd=str(cwd), check=True,
                          capture_output=True, text=True)


def make_worktree(root: Path) -> tuple[Path, object]:
    """A git repo at a known base commit, with aracne's artifacts staged the way `arac init`
    plus a scan leave them (added to the index but not in the commit)."""
    wt = root / "worktree"
    (wt / "src").mkdir(parents=True)
    git(["init", "-q", "-b", "main"], wt)
    git(["config", "user.email", "t@example.com"], wt)
    git(["config", "user.name", "t"], wt)
    (wt / "src" / "config.py").write_text("def from_file():\n    return 1\n", encoding="utf-8")
    git(["add", "-A"], wt)
    git(["commit", "-qm", "base"], wt)
    base = git(["rev-parse", "HEAD"], wt).stdout.strip()

    (wt / ".aracne").mkdir()
    (wt / ".aracne" / "topology.db").write_text("WARM-DB", encoding="utf-8")
    (wt / ".aracne" / "file_manifest.json").write_text("{}", encoding="utf-8")
    (wt / "CLAUDE.md").write_text("contract", encoding="utf-8")
    git(["add", "-A"], wt)

    task = types.SimpleNamespace(base_commit=base, repo_slug=lambda: "acme__demo")
    return wt, task


def dirty(wt: Path) -> None:
    """What a finished benchmark run leaves behind: an edited source file and a new one."""
    (wt / "src" / "config.py").write_text(
        "def from_file(mode='r'):\n    return 1\n\n\ndef extra():\n    pass\n", encoding="utf-8")
    (wt / "src" / "new_module.py").write_text("x = 1\n", encoding="utf-8")


# --------------------------------------------------------------------------- #

def test_drift_sees_source_but_not_aracne_artifacts(root: Path):
    wt, task = make_worktree(root)
    check("a worktree at base_commit reports no drift", fixtures.source_drift(wt) == [])

    dirty(wt)
    drift = fixtures.source_drift(wt)
    check("an edited tracked file is drift", "src/config.py" in drift)
    check("an agent's new file is drift too", "src/new_module.py" in drift)
    check("aracne's own artifacts are never drift",
          not any(p.startswith((".aracne", "CLAUDE.md")) for p in drift))


def test_reset_restores_base_and_keeps_the_warm_db(root: Path):
    wt, task = make_worktree(root)
    dirty(wt)

    fixtures.reset_sources(task, wt)

    check("source is back at base_commit",
          (wt / "src" / "config.py").read_text(encoding="utf-8") == "def from_file():\n    return 1\n")
    check("the agent's new file is gone", not (wt / "src" / "new_module.py").exists())
    # `git reset --hard` deletes index-added files, which is exactly what the warm DB is —
    # losing it would throw away the expensive artifact the fixture exists to cache.
    check("the warm topology DB survives the reset",
          (wt / ".aracne" / "topology.db").read_text(encoding="utf-8") == "WARM-DB")
    check("the injected contract survives too", (wt / "CLAUDE.md").exists())
    check("and the worktree is clean afterwards", fixtures.source_drift(wt) == [])


def test_ensure_base_commit_resets_and_reindexes(root: Path):
    wt, task = make_worktree(root)
    dirty(wt)

    seen: list[list[str]] = []
    original = fixtures.reindex
    fixtures.reindex = lambda worktree, cfg: seen.append([str(worktree)])
    try:
        drift = fixtures.ensure_base_commit(task, {"arac_bin": "arac"}, wt, "test")
    finally:
        fixtures.reindex = original

    check("the drift it repaired is reported back to the caller", "src/config.py" in drift)
    check("a reset is always followed by a re-index", len(seen) == 1)
    check("the worktree is left on base_commit", fixtures.source_drift(wt) == [])


def test_a_clean_worktree_costs_nothing(root: Path):
    wt, task = make_worktree(root)

    seen: list = []
    original = fixtures.reindex
    fixtures.reindex = lambda worktree, cfg: seen.append(worktree)
    try:
        drift = fixtures.ensure_base_commit(task, {"arac_bin": "arac"}, wt, "test")
    finally:
        fixtures.reindex = original

    check("no drift means no reset and no re-index", drift == [] and seen == [])


def test_a_worktree_on_the_wrong_commit_is_drift(root: Path):
    """`git status` is clean when the worktree is checked out at a DIFFERENT commit, so the
    HEAD check is what catches a fixture left on the wrong revision entirely."""
    wt, task = make_worktree(root)
    (wt / "src" / "extra.py").write_text("y = 2\n", encoding="utf-8")
    git(["add", "-A"], wt)
    git(["commit", "-qm", "moved on"], wt)
    check("git status alone sees nothing wrong", fixtures.source_drift(wt) == [])

    original = fixtures.reindex
    fixtures.reindex = lambda worktree, cfg: None
    try:
        drift = fixtures.ensure_base_commit(task, {"arac_bin": "arac"}, wt, "test")
    finally:
        fixtures.reindex = original
    check("a worktree on the wrong commit is reported as drift", drift != [])
    check("and is put back on base_commit",
          not (wt / "src" / "extra.py").exists())


def main() -> int:
    cases = [
        test_drift_sees_source_but_not_aracne_artifacts,
        test_reset_restores_base_and_keeps_the_warm_db,
        test_ensure_base_commit_resets_and_reindexes,
        test_a_clean_worktree_costs_nothing,
        test_a_worktree_on_the_wrong_commit_is_drift,
    ]
    for case in cases:
        with tempfile.TemporaryDirectory(prefix="aracne-fixture-hygiene-") as td:
            case(Path(td))

    failed = [name for name, passed in _RESULTS if not passed]
    print("\n" + "=" * 66)
    print(f"{len(_RESULTS) - len(failed)}/{len(_RESULTS)} passed")
    for name in failed:
        print(f"  FAILED: {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

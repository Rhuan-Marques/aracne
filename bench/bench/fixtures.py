"""Warm-fixture lifecycle.

A "fixture" is a CANONICAL worktree of a repo@commit plus a snapshot of the aracne
artifacts it produced. The expensive, one-time work (clone + `arac init` + `arac scan`
+ the user's own /descriptions-generate) is done once per unique repo@commit; the warm
topology DB is then snapshotted and restored cheaply before every benchmark run.

KEY FACTS:
  - Descriptions live ONLY in .aracne/topology.db (resources.description). They reach the
    agent through MCP read/grep output; `descriptions generate` writes the DB, never source
    (`apply`, which we never call, is what edits source). So snapshotting the aracne
    artifacts captures the warm topology in full.
  - Fixtures are PATH-PINNED to their canonical worktree (the DB may embed absolute paths),
    so we always reuse the same worktree path and never relocate it.
"""
from __future__ import annotations

import json
import shutil
import sqlite3
import subprocess
import time
from pathlib import Path

from .gitutil import clone_at, ensure_repo_cache, run_git

# SINGLE SOURCE OF TRUTH for the files/dirs aracne adds. arms.py and runner.py import
# this from here; nothing should redefine it.
ARACNE_ARTIFACTS = [".aracne", ".claude", ".opencode", ".mcp.json", "CLAUDE.md", "AGENTS.md"]


# --------------------------------------------------------------------------- #
# Paths
# --------------------------------------------------------------------------- #

def fixture_key(task) -> str:
    return f"{task.repo_slug()}@{task.base_commit[:12]}"


def fixture_dir(fixtures_root: Path, task) -> Path:
    return Path(fixtures_root) / fixture_key(task)


def worktree_path(fixtures_root: Path, task) -> Path:
    return fixture_dir(fixtures_root, task) / "worktree"


def snapshot_path(fixtures_root: Path, task) -> Path:
    return fixture_dir(fixtures_root, task) / "snapshot"


def meta_path(fixtures_root: Path, task) -> Path:
    return fixture_dir(fixtures_root, task) / "meta.json"


# --------------------------------------------------------------------------- #
# Description coverage
# --------------------------------------------------------------------------- #

def description_coverage(db_path, kinds=None) -> tuple[int, int, float]:
    """Return (described, total, pct) for a topology DB.

    If `kinds` is given, restrict to those resource kinds so coverage tracks exactly the
    nodes generation targets (`gen_kinds`); otherwise count all resources. Read-only
    sqlite3; a resource counts as described when its description is non-null and non-blank.
    Tolerates a missing/unreadable DB by returning (0, 0, 0.0).
    """
    db_path = Path(db_path)
    if not db_path.exists():
        return (0, 0, 0.0)
    where, params = "", []
    if kinds:
        where = f" WHERE kind IN ({','.join('?' for _ in kinds)})"
        params = list(kinds)
    described_clause = (" AND" if where else " WHERE") + \
        " description IS NOT NULL AND TRIM(description) != ''"
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        try:
            total = conn.execute(f"SELECT COUNT(*) FROM resources{where}", params).fetchone()[0]
            described = conn.execute(
                f"SELECT COUNT(*) FROM resources{where}{described_clause}", params).fetchone()[0]
        finally:
            conn.close()
    except sqlite3.Error:
        return (0, 0, 0.0)
    pct = described / total if total else 0.0
    return (described, total, pct)


def describable_count(db_path, kinds) -> int:
    """Number of resources whose kind is in `kinds` — i.e. the description workload size,
    used to drop oversized repos. Read-only sqlite3; tolerates a missing DB -> 0."""
    db_path = Path(db_path)
    if not db_path.exists() or not kinds:
        return 0
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        try:
            placeholders = ",".join("?" for _ in kinds)
            n = conn.execute(
                f"SELECT COUNT(*) FROM resources WHERE kind IN ({placeholders})",
                list(kinds),
            ).fetchone()[0]
        finally:
            conn.close()
    except sqlite3.Error:
        return 0
    return int(n)


def set_describe_kinds(worktree, kinds) -> None:
    """Scope description generation by writing `descriptions.kinds` into the worktree's
    .aracne/config.json (so `/descriptions-generate` + node_list only target these kinds)."""
    cfg_path = Path(worktree) / ".aracne" / "config.json"
    if not cfg_path.exists():
        return
    try:
        data = json.loads(cfg_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return
    if not isinstance(data.get("descriptions"), dict):
        data["descriptions"] = {}
    data["descriptions"]["kinds"] = list(kinds)
    cfg_path.write_text(json.dumps(data, indent=2), encoding="utf-8")


def _gen_kinds(cfg: dict) -> list[str]:
    return cfg.get("gen_kinds") or ["function", "method", "struct", "interface"]


# --------------------------------------------------------------------------- #
# Lifecycle
# --------------------------------------------------------------------------- #

def scaffold(task, cfg: dict, fixtures_root) -> dict:
    """Build (or reuse) the structure-only canonical worktree for `task`.

    Idempotent: if the worktree's topology DB already exists and force_prepare is off,
    the existing fixture is reported as-is (re-applying the kind scope). Otherwise the repo
    is cloned at base_commit, `arac init` injects BOTH the --claude and --opencode
    integrations, `arac scan --hard` builds the structure-only topology, and the description
    kind scope is written. Descriptions are NOT generated here (that's `generate`).
    """
    fixtures_root = Path(fixtures_root)
    wt = worktree_path(fixtures_root, task)
    db = wt / ".aracne" / "topology.db"
    arac_bin = cfg["arac_bin"]
    gen_kinds = _gen_kinds(cfg)

    fresh = not (db.exists() and not cfg.get("force_prepare"))
    if fresh:
        cache = ensure_repo_cache(task, Path(fixtures_root) / "_repos")
        clone_at(task, cache, wt, task.base_commit)

    # Always ensure BOTH harness integrations exist (cheap + idempotent) — this also repairs
    # fixtures created before a harness was wired in. Only the expensive clone+scan is skipped
    # for an already-prepared fixture.
    if fresh or not (wt / ".mcp.json").exists() or not (wt / ".opencode" / "opencode.json").exists():
        for harness_flag in ("--claude", "--opencode"):
            subprocess.run(
                [arac_bin, "init", harness_flag, "-y"],
                cwd=str(wt), check=True, capture_output=True, text=True,
            )

    scan_time = _carry_scan_time(fixtures_root, task)
    if fresh:
        t0 = time.monotonic()
        subprocess.run(
            [arac_bin, "scan", f"--{cfg.get('scan_mode', 'hard')}", "--root", ".",
             "--output", ".aracne/topology.db"],
            cwd=str(wt), check=True, capture_output=True, text=True,
        )
        scan_time = round(time.monotonic() - t0, 2)

    set_describe_kinds(wt, gen_kinds)   # written AFTER init (which may rewrite config.json)
    described, total, cov = description_coverage(db, gen_kinds)
    return {
        "key": fixture_key(task),
        "repo_slug": task.repo_slug(),
        "base_commit": task.base_commit,
        "scan_time_s": scan_time,
        "described": described,
        "total": total,
        "coverage": round(cov, 3),
        "describable": total,
    }


def freeze(task, cfg: dict, fixtures_root) -> dict:
    """Snapshot the warmed worktree's aracne artifacts and write meta.json.

    Requires the canonical worktree to exist (run `scaffold`/`prepare` first). Replaces
    any prior snapshot, copies every ARACNE_ARTIFACT that is present, and records the
    current description coverage.
    """
    fixtures_root = Path(fixtures_root)
    wt = worktree_path(fixtures_root, task)
    if not wt.exists():
        raise RuntimeError(f"no worktree for {fixture_key(task)}; run prepare first")

    described, total, cov = description_coverage(wt / ".aracne" / "topology.db", _gen_kinds(cfg))

    snap = snapshot_path(fixtures_root, task)
    if snap.exists():
        shutil.rmtree(snap)
    snap.mkdir(parents=True, exist_ok=True)
    for p in ARACNE_ARTIFACTS:
        src = wt / p
        if not src.exists():
            continue
        dest = snap / p
        if src.is_dir():
            shutil.copytree(src, dest)
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)

    prior = read_meta(task, fixtures_root) or {}
    meta = {
        "key": fixture_key(task),
        "repo_slug": task.repo_slug(),
        "base_commit": task.base_commit,
        "described": described,
        "total": total,
        "coverage": round(cov, 3),
        "frozen": True,
        "scan_time_s": prior.get("scan_time_s"),
    }
    meta_path(fixtures_root, task).write_text(json.dumps(meta, indent=2), encoding="utf-8")
    return meta


def restore(task, fixtures_root) -> Path:
    """Reset the canonical worktree to base_commit and restore the warm aracne artifacts.

    Returns the worktree path. Raises if no snapshot exists (freeze first). The worktree
    is reused in place (path-pinned), never relocated.
    """
    fixtures_root = Path(fixtures_root)
    wt = worktree_path(fixtures_root, task)
    snap = snapshot_path(fixtures_root, task)
    if not snap.exists():
        raise RuntimeError(f"no snapshot for {fixture_key(task)}; freeze first")

    run_git(["reset", "--hard", task.base_commit], wt)
    run_git(["clean", "-ffdxq"], wt, check=False)

    for p in ARACNE_ARTIFACTS:
        src = snap / p
        if not src.exists():
            continue
        dest = wt / p
        if dest.is_dir():
            shutil.rmtree(dest, ignore_errors=True)
        elif dest.exists():
            dest.unlink()
        if src.is_dir():
            shutil.copytree(src, dest)
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)
    return wt


def ensure_snapshot(task, cfg: dict, fixtures_root) -> dict:
    """Return the fixture meta, lazily freezing the snapshot if it does not exist yet."""
    fixtures_root = Path(fixtures_root)
    if not snapshot_path(fixtures_root, task).exists():
        return freeze(task, cfg, fixtures_root)
    return read_meta(task, fixtures_root) or freeze(task, cfg, fixtures_root)


def read_meta(task, fixtures_root) -> dict | None:
    """Load meta.json for a fixture, or None if it has not been written."""
    mp = meta_path(Path(fixtures_root), task)
    if not mp.exists():
        return None
    try:
        return json.loads(mp.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return None


def _carry_scan_time(fixtures_root: Path, task):
    prior = read_meta(task, fixtures_root)
    return prior.get("scan_time_s") if prior else None

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

import hashlib
import json
import re
import shutil
import sqlite3
import subprocess
import tempfile
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


def _merge_json(base: dict, override: dict) -> dict:
    """Deep-merge `override` onto `base`: dicts recurse, lists/scalars replace, None is skipped."""
    out = dict(base)
    for k, v in override.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _merge_json(out[k], v)
        elif v is not None:
            out[k] = v
    return out


def apply_aracne_config(worktree, overlay_path) -> None:
    """Overlay a benchmark-selected aracne config onto the aracne arm's .aracne/config.json.

    Deep-merges the overlay JSON on top of the fixture's already-valid config and writes it
    back, so a partial overlay tweaks only the keys it names while the config keeps the
    'sentinel' fields aracne requires (else `arac` would treat it as legacy and overwrite it
    with defaults). Runs after restore(), before the agent starts, so `arac serve`/`arac guard`
    read the merged config live. Tolerant of a missing/unreadable base (starts from {})."""
    cfg_path = Path(worktree) / ".aracne" / "config.json"
    overlay = json.loads(Path(overlay_path).read_text(encoding="utf-8"))
    base: dict = {}
    if cfg_path.exists():
        try:
            base = json.loads(cfg_path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            base = {}
    cfg_path.parent.mkdir(parents=True, exist_ok=True)
    cfg_path.write_text(json.dumps(_merge_json(base, overlay), indent=2), encoding="utf-8")


def sync_agent_contract(worktree, arac_bin: str = "arac") -> bool:
    """Regenerate the fixture's Claude Code integration files from its CURRENT config.

    WHY. `restore()` puts back the CLAUDE.md that was frozen at prepare time, and
    `apply_aracne_config()` then changes the config out from under it. The contract is not
    static text: `prompts.ClaudeMdContentForAgent` names the read tool `read` or
    `read_resource` depending on blocked_tools (see toolspec.ResolveReadToolName), and the
    settings allow-list is derived from mcp_tools. Skip this and a run overlay that blocks
    native read ships a contract telling the model to call a tool that is not in its list --
    the single most effective way to get zero adoption.

    `arac init --claude` writes integration files only (no scan, no topology write), and it
    reinstalls the guard hook, so it is safe and cheap to re-run per cell. Best-effort:
    returns False if it could not run, leaving the frozen contract in place.
    """
    try:
        subprocess.run([arac_bin, "init", "--claude", "-y"], cwd=str(worktree),
                       check=True, capture_output=True, text=True, timeout=120)
        return True
    except (subprocess.SubprocessError, OSError):
        return False


# --------------------------------------------------------------------------- #
# Base-commit hygiene
# --------------------------------------------------------------------------- #

def source_drift(worktree) -> list[str]:
    """Repo paths that differ from HEAD, EXCLUDING aracne's own artifacts.

    A benchmark run leaves its agent's edits in the canonical worktree — it is path-pinned and
    reused, so nothing cleans up until the next `restore`. Any fixture operation that reads the
    worktree between runs is therefore reading SOLVED source unless it checks. That is not
    hypothetical: the pallets__flask fixture was frozen from a worktree still carrying the
    agent's fix, so its snapshot DB indexed `Config` at lines 29-347 of a file that is 338 lines
    at base_commit. Every read of any symbol in that class then failed with a range error that
    looked to the model exactly like a bad ID — and the description the DB carried for
    `Config.from_file` documented the `mode` parameter the task exists to add.
    """
    wt = Path(worktree)
    res = run_git(["status", "--porcelain", "--untracked-files=all"], wt, check=False)
    if res.returncode != 0:
        return []
    drift = []
    for line in res.stdout.splitlines():
        path = line[3:].strip().strip('"')
        # Rename/copy entries read "old -> new"; the destination is what matters.
        if " -> " in path:
            path = path.split(" -> ", 1)[1]
        if not path or path.split("/", 1)[0] in ARACNE_ARTIFACTS:
            continue
        drift.append(path)
    return sorted(drift)


def reset_sources(task, worktree) -> None:
    """Hard-reset the worktree to `base_commit`, keeping the aracne artifacts in place.

    `git reset --hard` DELETES a path that is in the index but not in the target commit, and
    the fixture's artifacts are staged that way — so they are moved aside for the reset and
    moved back, which is the same dance `restore` does with the snapshot.
    """
    wt = Path(worktree)
    holding = Path(tempfile.mkdtemp(prefix="aracne-fixture-"))
    moved = []
    try:
        for name in ARACNE_ARTIFACTS:
            src = wt / name
            if src.exists():
                shutil.move(str(src), str(holding / name))
                moved.append(name)
        run_git(["reset", "--hard", task.base_commit], wt)
        run_git(["clean", "-ffdxq"], wt, check=False)
    finally:
        for name in moved:
            dest = wt / name
            if dest.exists():
                shutil.rmtree(dest, ignore_errors=True) if dest.is_dir() else dest.unlink()
            shutil.move(str(holding / name), str(dest))
        shutil.rmtree(holding, ignore_errors=True)


def reindex(worktree, cfg: dict) -> None:
    """Re-index whatever has drifted from the topology, incrementally.

    Best-effort and never fatal: a fixture that cannot be re-indexed is still usable, just
    stale, and `arac check-updates` will say so. The default (incremental) mode is deliberate —
    it re-parses only the files whose mtime moved, so this costs a manifest diff on the common
    path where nothing changed.
    """
    try:
        subprocess.run(
            [cfg.get("arac_bin", "arac"), "scan", "--default", "--root", ".",
             "--output", ".aracne/topology.db"],
            cwd=str(worktree), check=True, capture_output=True, text=True, timeout=900,
        )
    except (OSError, subprocess.SubprocessError) as e:  # noqa: BLE001
        print(f"[fixtures] warning: could not re-index {worktree}: {e}")


def ensure_base_commit(task, cfg: dict, worktree, label: str) -> list[str]:
    """Put the worktree back on base_commit and re-index if it had drifted. Returns the drift.

    Every fixture operation that inspects or captures the worktree calls this first, so no
    aracne artifact can ever be built from — or frozen against — source the benchmark does not
    hand the agent.
    """
    drift = source_drift(worktree)
    head = run_git(["rev-parse", "HEAD"], Path(worktree), check=False).stdout.strip()
    # A worktree left on the wrong commit is drift too, and `git status` cannot see it.
    if head and not head.startswith(task.base_commit[:12]):
        drift = drift + [f"HEAD is {head[:12]}, not {task.base_commit[:12]}"]
    if not drift:
        return []
    shown = ", ".join(drift[:5]) + (f" … and {len(drift) - 5} more" if len(drift) > 5 else "")
    print(f"[fixtures] {label}: worktree for {fixture_key(task)} was NOT at base_commit "
          f"({len(drift)} path(s) changed: {shown}). Resetting and re-indexing — a fixture "
          f"captured in this state leaks the solution and indexes lines that do not exist.")
    reset_sources(task, worktree)
    reindex(worktree, cfg)
    return drift


# --------------------------------------------------------------------------- #
# Lifecycle
# --------------------------------------------------------------------------- #

def scaffold(task, cfg: dict, fixtures_root) -> dict:
    """Build (or reuse) the structure-only canonical worktree for `task`.

    Idempotent: if the worktree's topology DB already exists and force_prepare is off,
    the existing fixture is reported as-is (re-applying the kind scope). Otherwise the repo
    is cloned at base_commit, `arac init` injects BOTH the --claude and --opencode
    integrations, `arac scan --all` builds the structure-only topology (preserving any
    descriptions already present, and remapping them across an id-scheme change), and the description
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

    # A reused fixture may still be carrying the last run's agent edits. Everything below —
    # `arac init`, the scan, the description kind scope, and the coverage count this returns —
    # reads the worktree, so put it back on base_commit first.
    if not fresh:
        ensure_base_commit(task, cfg, wt, "scaffold")

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
            # Default "all" (FullReScan), never "hard": hard drops every description.
            [arac_bin, "scan", f"--{cfg.get('scan_mode', 'all')}", "--root", ".",
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


def arac_version(cfg: dict) -> str:
    """`arac --version`, trimmed to one line. "" when the binary cannot be run."""
    try:
        res = subprocess.run([cfg.get("arac_bin", "arac"), "--version"],
                             capture_output=True, text=True, timeout=30)
        return (res.stdout or res.stderr or "").strip().splitlines()[0][:120]
    except Exception:  # noqa: BLE001 — provenance is best-effort, never fatal
        return ""


def config_fingerprint(wt) -> str:
    """SHA-256 (first 12 hex) of the fixture's `.aracne/config.json`.

    WHY. `scaffold` only re-runs `arac init` when .mcp.json/.opencode/opencode.json is
    missing, so every fixture stays frozen at whatever arac build first touched it. In the
    scale40 pool that produced FOUR distinct configs across 36 fixtures — including a
    6-fixture group with `blocked_tools: []`, `include_incoming: true` and
    `small_functions_visibility: full`. Those fixtures were not running the shipped
    contract, so the "aracne arm" was silently a mixture of two different products.
    Recording the fingerprint lets `run` refuse a mixed pool instead of averaging over it.
    """
    cfgp = Path(wt) / ".aracne" / "config.json"
    if not cfgp.exists():
        return ""
    return hashlib.sha256(cfgp.read_bytes()).hexdigest()[:12]


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

    # THE fix for poisoned fixtures. What gets frozen here is what every run of this instance
    # will be handed, so it must describe base_commit and nothing else. See source_drift.
    drift = ensure_base_commit(task, cfg, wt, "freeze")

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
        # Provenance: which arac produced this fixture, and which config it will run under.
        "arac_version": arac_version(cfg),
        "config_fingerprint": config_fingerprint(wt),
        # Whether this freeze had to undo a previous run's edits. A non-empty list means the
        # DESCRIPTIONS in the snapshot may still have been generated against solved source —
        # locations are repaired by the re-index, prose is not — so the fixture is worth
        # regenerating before it is trusted for a correctness claim.
        "reset_from_drift": drift,
    }
    meta_path(fixtures_root, task).write_text(json.dumps(meta, indent=2), encoding="utf-8")
    return meta


# Files a lazily-generated description must never be harvested from: the diff regex below
# names them, and _run_cell hands the set in.
_DIFF_FILE_RE = re.compile(r"^\+\+\+ [ab]/(.+?)\s*$", re.M)


def patch_paths(patch: str) -> set[str]:
    """Repo-relative paths a unified diff writes to."""
    return {p for p in _DIFF_FILE_RE.findall(patch or "") if p != "/dev/null"}


def harvest_descriptions(task, cfg: dict, fixtures_root, changed_paths=None) -> int:
    """Copy descriptions the run generated back into the fixture SNAPSHOT.

    WHY. `descriptions.lazy` writes a description the moment a read or a search is about to
    show one, straight into the worktree's topology DB. That DB is then thrown away: `restore`
    copies `.aracne` from the snapshot over the worktree before every cell, so without this
    the same nodes would be described again on every run, and every arm would pay the latency
    of describing what the last arm already described. Harvesting makes the cost one-time per
    node per repo, across runs, which is the whole point of generating lazily instead of
    sweeping up front.

    WHAT IS REFUSED. A description generated from source the agent had already edited
    describes the SOLVED code, and persisting it would leak the fix into every later run of
    that fixture -- the same poisoning `freeze` warns about under `reset_from_drift`, except
    silent and permanent. So a resource is harvested only when its file is untouched by this
    run's patch. Passing `changed_paths=None` means "nothing is known to be safe" and harvests
    nothing, which is the conservative reading, not the convenient one.

    Only fills HOLES: a description already in the snapshot wins, so a warm fixture's curated
    prose is never overwritten by a lazy one-liner. Returns how many were written.
    """
    wt_db = worktree_path(fixtures_root, task) / ".aracne" / "topology.db"
    snap_db = snapshot_path(fixtures_root, task) / ".aracne" / "topology.db"
    if not wt_db.exists() or not snap_db.exists():
        return 0
    # None and the empty set are NOT the same claim. An empty set is a caller saying "this run
    # edited nothing, all of it is safe"; None is a caller that does not know, and guessing
    # "safe" there is how a description of patched source gets persisted forever.
    if changed_paths is None:
        return 0

    try:
        src = sqlite3.connect(f"file:{wt_db}?mode=ro", uri=True)
        try:
            rows = src.execute(
                "SELECT id, kind, description, loc_path FROM resources "
                "WHERE description IS NOT NULL AND TRIM(description) != ''"
            ).fetchall()
        finally:
            src.close()

        fresh = [(rid, kind, desc) for rid, kind, desc, path in rows
                 if (path or "") not in changed_paths]
        if not fresh:
            return 0

        dst = sqlite3.connect(str(snap_db))
        try:
            written = 0
            for rid, _kind, desc in fresh:
                cur = dst.execute(
                    "UPDATE resources SET description = ? WHERE id = ? "
                    "AND (description IS NULL OR TRIM(description) = '')",
                    (desc, rid),
                )
                written += cur.rowcount
            dst.commit()
        finally:
            dst.close()
    except sqlite3.Error as e:  # noqa: BLE001 - a harvest failure must never fail the cell
        print(f"[fixtures] warning: could not harvest descriptions for "
              f"{fixture_key(task)}: {e}")
        return 0

    if written:
        refresh_meta_coverage(task, cfg, fixtures_root)
    return written


def refresh_meta_coverage(task, cfg: dict, fixtures_root) -> None:
    """Re-stamp described/total/coverage in meta.json from the snapshot DB.

    Lazy harvesting moves coverage between runs, and `arms.prepare_workdir` reads exactly
    these fields to decide whether a fixture is warm enough to run. Leaving them at their
    freeze-time values would make a fixture that has since filled itself in look cold forever.
    """
    snap_db = snapshot_path(fixtures_root, task) / ".aracne" / "topology.db"
    meta_file = meta_path(fixtures_root, task)
    if not meta_file.exists():
        return
    described, total, cov = description_coverage(snap_db, _gen_kinds(cfg))
    try:
        meta = json.loads(meta_file.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return
    meta.update(described=described, total=total, coverage=round(cov, 3))
    meta_file.write_text(json.dumps(meta, indent=2), encoding="utf-8")


def restore(task, cfg: dict, fixtures_root) -> Path:
    """Reset the canonical worktree to base_commit and restore the warm aracne artifacts.

    Returns the worktree path. Raises if no snapshot exists (freeze first). The worktree
    is reused in place (path-pinned), never relocated.

    Finishes with an incremental re-index, because restoring a DB next to source is exactly the
    operation that can produce a topology describing the wrong bytes: the reset rewrites every
    file the last run edited, giving them new mtimes, and the copied manifest still claims they
    were indexed. That is a manifest diff plus a re-parse of only the drifted files, so an
    already-consistent fixture pays a tree walk and nothing more — and it is what makes an
    older, poisoned snapshot usable instead of silently wrong for a whole run.
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

    reindex(wt, cfg)
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

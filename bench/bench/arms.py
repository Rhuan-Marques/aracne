"""Per-arm working-directory setup and per-arm runtime sidecars.

Two arms, identical in every way except the toolset the agent is given:

  - baseline : vanilla Claude Code with native Read/Grep/Edit/Bash. No aracne files. Each
               run gets an ephemeral clone of the repo at its base commit.
  - aracne   : the WARM canonical worktree of a prepared fixture — `arac init --claude`
               injected the CLAUDE.md contract, .mcp.json (which makes Claude Code spawn
               `arac serve`), and the guard hook, and `arac scan` + the user's own
               /descriptions-generate filled the topology DB. The worktree is reset and
               its warm aracne artifacts restored before each run.

Measuring the shipped package as a whole (contract + tools + guard) is intentional.
"""
from __future__ import annotations

import contextlib
import subprocess
from pathlib import Path

from . import fixtures
from .gitutil import clone_at, ensure_repo_cache
from .fixtures import ARACNE_ARTIFACTS  # noqa: F401 - re-exported for back-compat

ARMS = ("baseline", "aracne")


def prepare_workdir(arm: str, task, cfg: dict, repos_dir: Path, ephemeral_dir: Path,
                    fixtures_root: Path) -> Path:
    """Produce the working directory for one run of `arm` and return its path.

    baseline: a fresh ephemeral clone at base_commit (caller owns/cleans `ephemeral_dir`).
    aracne:   the canonical warm worktree, reset and restored from its snapshot. Enforces
              the description-coverage guardrail unless cfg["allow_cold"] is set.

    Raises RuntimeError on a coverage-guardrail violation (so the caller can record it as
    a setup error and move on).
    """
    if arm == "baseline":
        cache = ensure_repo_cache(task, Path(repos_dir))
        clone_at(task, cache, Path(ephemeral_dir), task.base_commit)
        return Path(ephemeral_dir)

    # aracne arm: lazily freeze if needed, enforce coverage, then restore the warm worktree.
    meta = fixtures.ensure_snapshot(task, cfg, fixtures_root)
    cov = meta.get("coverage", 0.0) or 0.0
    if cov < cfg["coverage_min"] and not cfg["allow_cold"]:
        raise RuntimeError(
            f"aracne fixture for {task.key} has {cov:.0%} description coverage "
            f"(< {cfg['coverage_min']:.0%}); run prepare + your /descriptions-generate, "
            f"or pass --allow-cold"
        )
    wt = fixtures.restore(task, cfg, fixtures_root)
    # Optionally overlay a benchmark-selected .aracne/config.json for this run (aracne arm only),
    # then regenerate the contract so what the agent is TOLD matches the tools it is GIVEN --
    # the overlay can rename the read tool and change the native-tool policy.
    if cfg.get("aracne_config_path"):
        fixtures.apply_aracne_config(wt, cfg["aracne_config_path"])
        fixtures.sync_agent_contract(wt, cfg.get("arac_bin", "arac"))
    return wt


@contextlib.contextmanager
def background_scanner(arm: str, workdir, cfg: dict):
    """Run `arac scanner run` beside an aracne-arm cell; yield True if it started.

    WHY. The alternative is `read.scan`, which re-indexes on the way into EVERY read and
    grep -- freshness paid for per call, on the measured path, by the arm under test. The
    watcher moves that work off the read path: it polls `scanner.update_frequency` ms and
    incrementally re-scans only what changed. It also covers the mutations the guard cannot
    see: `blocked_tools` classifies a shell command by its command WORD, so `sed -i` is an
    edit but `python3 - <<EOF` writing the same file is just python -- and heredoc patching
    is what agents actually do.

    Only the aracne arm has a topology to watch; baseline is a no-op, which keeps the arms'
    single difference the aracne package itself.

    Best-effort by design: if the watcher cannot start, the cell still runs (reads fall back
    to whatever read.scan says) and the caller records that it did not, because a freshness
    mechanism that silently fails to run is exactly how a benchmark ends up measuring
    nothing. It is NOT detached, so an interrupt to the harness takes it down too, and it is
    always terminated -- a leaked poller would keep rewriting a fixture's topology between
    runs.
    """
    proc = None
    if arm == "aracne":
        db = Path(workdir) / ".aracne" / "topology.db"
        if db.exists():
            try:
                proc = subprocess.Popen(
                    [cfg.get("arac_bin", "arac"), "scanner", "run", "--db", str(db)],
                    cwd=str(workdir),
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                )
            except OSError:
                proc = None
    try:
        yield proc is not None
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=10)

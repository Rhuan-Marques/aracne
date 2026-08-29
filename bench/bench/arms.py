"""Per-arm working-directory setup.

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
    # Optionally overlay a benchmark-selected .aracne/config.json for this run (aracne arm only).
    if cfg.get("aracne_config_path"):
        fixtures.apply_aracne_config(wt, cfg["aracne_config_path"])
    return wt

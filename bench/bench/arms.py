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

ARMS = ("baseline", "aracne", "aracne-open", "aracne-noprefer")


def is_aracne_arm(arm: str) -> bool:
    """Every arm except the control gets the topology, the MCP server and the contract.

    Written as "not baseline" rather than a membership test on ARMS because the arm name is
    also how a run selects its aracne config preset: `aracne` blocks the native read/edit/write
    tools, `aracne-open` offers the same topology and blocks nothing. Keeping the split at one
    predicate is what stops a new arm silently running against a stale topology, which is how
    the `background_scanner` check read before the third arm existed.
    """
    return arm != "baseline"


def aracne_config_for(arm: str, cfg: dict):
    """The aracne config preset this arm runs under.

    `arm_aracne_config` maps an arm name to a preset path, which is what lets two aracne-like
    arms differ in the ONE thing under test -- whether the native tools are blocked -- while
    sharing every other setting. Without it a run can only compare aracne against no-aracne,
    which conflates "the topology helps" with "the guard hurts": in the
    compact-blocked-20260830c run those two had opposite signs and cancelled.
    """
    per_arm = cfg.get("arm_aracne_config") or {}
    return per_arm.get(arm) or cfg.get("aracne_config_path")


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
    overlay = aracne_config_for(arm, cfg)
    if overlay:
        fixtures.apply_aracne_config(wt, overlay)
        fixtures.sync_agent_contract(wt, cfg.get("arac_bin", "arac"))
    return wt


@contextlib.contextmanager
def background_scanner(arm: str, workdir, cfg: dict):
    """Run `arac scanner run` beside an aracne-arm cell; yield True if it started.

    WHY. The alternative is `scan.pre_tool`, which re-indexes on the way into EVERY guarded
    tool call -- freshness paid for per call, on the measured path, by the arm under test. The
    watcher moves that work off the read path: it polls `scanner.update_frequency` ms and
    incrementally re-scans only what changed. It also covers the mutations the guard cannot
    see: `blocked_tools` classifies a shell command by its command WORD, so `sed -i` is an
    edit but `python3 - <<EOF` writing the same file is just python -- and heredoc patching
    is what agents actually do.

    Only the aracne arm has a topology to watch; baseline is a no-op, which keeps the arms'
    single difference the aracne package itself.

    Best-effort by design: if the watcher cannot start, the cell still runs (reads fall back
    to whatever scan.pre_tool says) and the caller records that it did not, because a freshness
    mechanism that silently fails to run is exactly how a benchmark ends up measuring
    nothing. It is NOT detached, so an interrupt to the harness takes it down too, and it is
    always terminated -- a leaked poller would keep rewriting a fixture's topology between
    runs.
    """
    proc = None
    if is_aracne_arm(arm):
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


def arm_tools(cfg: dict, arm: str, key: str):
    """The tool surface for ONE arm: `arm_<key>` overrides the global `<key>`.

    WHY PER-ARM. `builtin_tools` (claude --tools) exists to trim the built-in surface so the
    aracne MCP tools stay in the model's front list instead of being deferred behind a
    ToolSearch. Applied globally it also strips Read/Edit/Write from the CONTROL, which does
    not isolate anything -- it just measures aracne against an agent that has no file tools at
    all. The arms are supposed to differ in how files are read and written and in nothing else,
    so each names its own surface.

    Returns None (the driver's "pass no flag") rather than an empty list, so an arm that names
    nothing keeps the CLI default.
    """
    per_arm = (cfg.get("arm_" + key) or {}).get(arm)
    if per_arm is None:
        per_arm = cfg.get(key)
    return list(per_arm) if per_arm else None

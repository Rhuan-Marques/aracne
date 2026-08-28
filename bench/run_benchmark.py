#!/usr/bin/env python3
"""aracne A/B benchmark — entry point (prepare-once / run-many).

Five phases, driven by subcommands, so the expensive warm-up is paid once per repo and
reused across every run. Both description generation AND the A/B run can use Claude Code
(haiku) or OpenCode (off the Max plan), with any model:

  sample    oversample a candidate manifest -> bench/samples/<id>.candidates.jsonl
  prepare   clone + `arac init --claude --opencode` + scan each candidate (token-free),
            DROP repos with too many describable nodes, write the final manifest.
  generate  fill descriptions into every fixture via the chosen backend (default
            claude_code/haiku; or opencode/<provider/model>). Resumable; scoped by kind.
  freeze    snapshot each warmed worktree's aracne artifacts + report coverage.
  run       the A/B matrix (baseline vs aracne) over the manifest + warm fixtures.

The harness never calls `arac descriptions generate` (hardcoded to DeepSeek).

Examples:
  python bench/run_benchmark.py sample --samples 2 --oversample 3
  python bench/run_benchmark.py prepare --max-nodes 600
  python bench/run_benchmark.py generate --gen-harness opencode --gen-model deepseek/deepseek-v4-flash
  python bench/run_benchmark.py freeze
  python bench/run_benchmark.py run --run-harness claude_code --model haiku --seeds 1
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import shutil
import subprocess
import sys
import threading
import time
import uuid
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

# Allow running as `python bench/run_benchmark.py` from the repo root.
sys.path.insert(0, str(Path(__file__).resolve().parent))

from bench import (agents, analysis, chunkgen, fixtures, grade, htmlreport,  # noqa: E402
                   metrics, outcome, report, runner, sources, toolstats)
from bench.sources import ALL_LANGUAGES, load_tasks  # noqa: E402

HERE = Path(__file__).resolve().parent
CONFIGS_DIR = HERE / "configs"
RUN_CONFIGS_DIR = CONFIGS_DIR / "run"         # benchmark run configs (YAML)
ARACNE_CONFIGS_DIR = CONFIGS_DIR / "aracne"   # .aracne/config.json overlays for the aracne arm

DEFAULTS = {
    "samples": 2,                # tasks per language (small by default — scale up deliberately)
    "languages": ALL_LANGUAGES,
    "seeds": 1,                  # repetitions per task per arm
    "sample_seed": 0,
    "sample_id": "default",
    "samples_dir": str(HERE / "samples"),
    "fixtures_dir": str(HERE / "fixtures"),
    "oversample": 3,             # sample N x this, then prune oversized repos in `prepare`
    "max_describable_nodes": 600,  # drop candidate repos with more describable nodes than this
    "arms": ["baseline", "aracne"],
    "run_harness": "claude_code",  # claude_code | opencode (same for both arms)
    "model": "haiku",            # run model for BOTH arms (cheap by default)
    "effort": None,              # claude_code --effort (low|medium|high|xhigh|max); None =
                                 # CLI default. Applies to BOTH arms, like `model`: it
                                 # changes what a run costs, so rows from different efforts
                                 # are not comparable (see _import_prior_rows).
    "max_turns": 30,
    "timeout_s": 1800,          # per-run agent wall-clock cap; exceeding it = outcome "timeout"
    # Grading caps. A cold Docker cache can legitimately take an hour of steady work, so the
    # STALL cap (no harness output at all) is the one that catches a wedged build; the total
    # cap is the backstop. Either at 0 disables that clock. Blowing either records the
    # affected runs as outcome "timeout" — never as failures.
    "grade_timeout_s": 5400,
    "grade_stall_timeout_s": 900,
    "retry_timeouts": False,    # --continue: re-run timed-out steps instead of keeping them
    # Matrix cells executed CONCURRENTLY. 1 = strictly sequential (the default, and the only
    # setting whose wall-clock numbers are contention-free). Raising it multiplies throughput
    # but inflates every run's `duration_ms`, and several headless agents at once hit plan
    # rate limits fast — 2-4 is the useful range.
    "run_parallel": 1,
    "arac_bin": "arac",
    # "all" = FullReScan: rescans everything but PRESERVES descriptions, and auto-remaps
    # them across an id-scheme change. It was "hard" (= FullScan), which never reads the
    # old DB and drops every description unconditionally — so re-preparing a fixture
    # silently destroyed hours of LLM-authored descriptions. Only ever set this to "hard"
    # on a fixture you intend to describe from scratch.
    "scan_mode": "all",
    "coverage_min": 0.95,
    "allow_cold": False,
    "force_prepare": False,
    # --- description generation (the prep step) ---
    "gen_harness": "claude_code",  # claude_code (haiku, on Max) | opencode (off Max)
    "gen_model": "haiku",          # orchestrator model; executors are haiku via agent frontmatter
    "gen_kinds": ["function", "method", "struct", "interface"],  # scope (drop `file` etc.)
    "gen_max_turns": 300,          # `agent` driver only: orchestrator turn cap
    "gen_timeout_s": 14400,        # per-worktree wall-clock cap for generation (all rounds)
    "gen_parallel": 1,             # worktrees warmed concurrently (rate-limits, keep low)
    # --- driver ---------------------------------------------------------- #
    # "chunked" (default): Python lists undocumented nodes, splits them into id-chunks and
    # drives workers itself, re-listing from topology.db each round until the fixture hits
    # coverage_min. "agent": the old path — hand `/descriptions-generate` to one
    # orchestrator session and hope it loops. It does not, on large repos: a single wave
    # lands and the session ends (see bench/bench/chunkgen.py for the observed failure).
    "gen_driver": "chunked",       # chunked | agent
    "gen_chunk": 15,               # resource ids per worker
    "gen_chunk_parallel": 5,       # concurrent workers WITHIN one fixture
    "gen_chunk_timeout_s": 900,    # per-worker cap; generous — a worker owns only `gen_chunk` ids
    "gen_chunk_max_turns": 60,     # per-worker turn cap (~4 turns/id at chunk 15)
    "gen_max_rounds": 8,           # re-list/re-chunk rounds before giving up on a fixture
    "gen_min_gain": 3,             # a round adding fewer than this many descriptions = stalled
    "gen_progress": True,          # live per-fixture progress bar / status lines
    "gen_mcp_override": True,      # claude_code only: serve --tool-profile all during gen
    "regenerate": False,           # re-generate even if a fixture is already covered
    "out": None,
    "keep_workdir": False,
    "dry_run": False,
    "no_grade": False,
    "resume": False,
    "continue_run": None,        # continue a prior run by id/name (re-run its errored + unfinished steps)
    "run_name": None,            # unique id for this run; None -> random; also names results/<id>
    "aracne_config": None,       # aracne arm's .aracne/config.json overlay (name in configs/aracne/ or path)
    "max_per_repo": 1,           # max tasks drawn from ONE repository during `sample`.
                                 # Effective N in the paired analysis is the REPO count, so
                                 # >1 buys task count without buying statistical power.
    "baseline_from": None,       # reuse a prior run's rows for arms not being measured
    "no_analysis": False,        # skip the end-of-run LLM results analysis
    "ni_margin": 0.10,           # non-inferiority margin for the PAIRED solve-rate test:
                                 # aracne is "non-inferior" when the lower bound of the
                                 # paired rate difference clears -margin. Superiority needs
                                 # a far larger pool than a Max plan affords; this is the
                                 # correctness guardrail beside the efficiency claim.
    "analysis_model": "haiku",   # model for the one-shot results analysis (claude_code)
    "analysis_max_turns": 6,     # small — it writes prose from the embedded numbers, but
                                 # needs room to read the per-language and by-size splits
                                 # rather than latching onto the pooled ratio
    "sources": {
        "multi_swe_bench": {
            "dataset": "ByteDance-Seed/Multi-SWE-bench",
            "lang_config": {"go": "go", "javascript": "js", "typescript": "ts", "rust": "rust"},
            "split": "test",
            "local_dir": None,
            "eval_config": {},
        },
        "swe_bench": {
            "dataset": "princeton-nlp/SWE-bench_Lite",
            "split": "test",
            "model_name": "aracne-bench",
            "max_workers": 4,
        },
    },
}


def _resolve_config_path(value) -> Path | None:
    """`--config` accepts a PATH or a bare NAME.

    An existing path is used verbatim; a bare name (no separator, no .yaml/.yml) resolves
    to bench/configs/run/<name>.yaml (or .yml), falling back to bench/configs/<name>.yaml
    for the older flat layout. Anything unresolved is returned as-is so build_config's
    `.exists()` check simply skips it (falls back to DEFAULTS)."""
    if value is None:
        return None
    p = Path(value)
    if p.exists():
        return p
    s = str(value)
    if "/" not in s and "\\" not in s and not s.endswith((".yaml", ".yml")):
        for d in (RUN_CONFIGS_DIR, CONFIGS_DIR):   # bench/configs/run first, then legacy bench/configs
            for cand in (d / f"{s}.yaml", d / f"{s}.yml"):
                if cand.exists():
                    return cand
    for d in (RUN_CONFIGS_DIR, CONFIGS_DIR):
        cand = d / s
        if cand.exists():
            return cand
    return p


def _resolve_aracne_config_path(value) -> str | None:
    """Resolve the aracne-config overlay: a bare NAME -> bench/configs/aracne/<name>.json,
    or a PATH used verbatim. Returns the path string only if it exists, else None (the caller
    fails loudly when a value was given but nothing resolved)."""
    if not value:
        return None
    p = Path(value)
    if p.exists():
        return str(p)
    s = str(value)
    if "/" not in s and "\\" not in s and not s.endswith(".json"):
        cand = ARACNE_CONFIGS_DIR / f"{s}.json"
        if cand.exists():
            return str(cand)
    cand = ARACNE_CONFIGS_DIR / s
    return str(cand) if cand.exists() else None


def _load_yaml(path: Path) -> dict:
    try:
        import yaml
    except ImportError:
        print("PyYAML not installed; ignoring config file. pip install -r bench/requirements.txt")
        return {}
    return yaml.safe_load(path.read_text(encoding="utf-8")) or {}


def _deep_merge(base: dict, override: dict) -> dict:
    out = dict(base)
    for k, v in override.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        elif v is not None:
            out[k] = v
    return out


def parse_args(argv=None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="aracne A/B benchmark harness")
    sub = p.add_subparsers(dest="command", required=True)

    def add_config(sp):
        sp.add_argument("--config", type=Path, default=HERE / "config.yaml",
                        help="YAML config (defaults to bench/config.yaml if present)")

    # sample -------------------------------------------------------------- #
    sp = sub.add_parser("sample", help="write an oversampled candidate manifest")
    sp.add_argument("--max-per-repo", type=int, dest="max_per_repo", default=None,
                    help="max tasks drawn from any ONE repository (default 1). Tasks from a "
                         "repo are correlated and the paired analysis clusters on repo, so "
                         "raising this inflates the task count without raising effective N.")
    sp.add_argument("--samples", type=int, default=None, help="final tasks per language (target)")
    sp.add_argument("--oversample", type=int, default=None,
                    help="candidate multiplier: sample samples x this, prune oversized in `prepare`")
    sp.add_argument("--languages", default=None, help="comma list: go,javascript,typescript,rust,python")
    sp.add_argument("--sample-seed", type=int, dest="sample_seed", default=None,
                    help="RNG seed selecting which tasks are drawn")
    sp.add_argument("--sample-id", dest="sample_id", default=None, help="manifest id (filename stem)")
    add_config(sp)

    # prepare ------------------------------------------------------------- #
    sp = sub.add_parser("prepare",
                        help="scan candidates (token-free), drop oversized repos, write the final manifest")
    sp.add_argument("--sample-id", dest="sample_id", default=None)
    sp.add_argument("--samples", type=int, default=None, help="final tasks to keep per language")
    sp.add_argument("--max-nodes", type=int, dest="max_describable_nodes", default=None,
                    help="drop candidate repos with more describable nodes than this")
    sp.add_argument("--gen-kinds", dest="gen_kinds", default=None,
                    help="comma list of kinds to scope descriptions to (e.g. function,struct)")
    sp.add_argument("--arac-bin", dest="arac_bin", default=None, help="path to the arac binary")
    sp.add_argument("--force-prepare", action="store_true", dest="force_prepare",
                    help="rebuild worktrees even if a topology DB already exists")
    add_config(sp)

    # freeze -------------------------------------------------------------- #
    sp = sub.add_parser("freeze", help="snapshot warm artifacts + report description coverage")
    sp.add_argument("--sample-id", dest="sample_id", default=None)
    sp.add_argument("--coverage-min", type=float, dest="coverage_min", default=None,
                    help="warn when a fixture is below this coverage")
    add_config(sp)

    # generate ------------------------------------------------------------ #
    sp = sub.add_parser("generate",
                        help="batch-generate descriptions in EVERY fixture via Claude Code or OpenCode")
    sp.add_argument("--sample-id", dest="sample_id", default=None)
    sp.add_argument("--languages", default=None, help="limit to these languages (comma list)")
    sp.add_argument("--gen-harness", dest="gen_harness", default=None,
                    help="claude_code (haiku, on Max) | opencode (off Max)")
    sp.add_argument("--gen-kinds", dest="gen_kinds", default=None,
                    help="comma list of kinds to describe (e.g. function,struct)")
    sp.add_argument("--arac-bin", dest="arac_bin", default=None, help="path to the arac binary")
    sp.add_argument("--gen-model", dest="gen_model", default=None,
                    help="orchestrator model: claude alias (haiku) or opencode provider/model")
    sp.add_argument("--gen-max-turns", type=int, dest="gen_max_turns", default=None)
    sp.add_argument("--gen-timeout-s", type=int, dest="gen_timeout_s", default=None,
                    help="per-worktree wall-clock cap for generation")
    sp.add_argument("--gen-parallel", type=int, dest="gen_parallel", default=None,
                    help="worktrees to warm concurrently (keep low; Max rate-limits)")
    sp.add_argument("--gen-driver", dest="gen_driver", default=None,
                    choices=["chunked", "agent"],
                    help="chunked (default; Python drives the batches) | agent (legacy "
                         "single /descriptions-generate orchestrator session)")
    sp.add_argument("--gen-chunk", type=int, dest="gen_chunk", default=None,
                    help="resource ids per worker (chunked driver)")
    sp.add_argument("--gen-chunk-parallel", type=int, dest="gen_chunk_parallel", default=None,
                    help="concurrent workers WITHIN one fixture (chunked driver)")
    sp.add_argument("--gen-chunk-timeout-s", type=int, dest="gen_chunk_timeout_s", default=None,
                    help="per-worker wall-clock cap (chunked driver)")
    sp.add_argument("--gen-chunk-max-turns", type=int, dest="gen_chunk_max_turns", default=None,
                    help="per-worker turn cap (chunked driver)")
    sp.add_argument("--gen-max-rounds", type=int, dest="gen_max_rounds", default=None,
                    help="re-list/re-chunk rounds per fixture before giving up")
    sp.add_argument("--gen-min-gain", type=int, dest="gen_min_gain", default=None,
                    help="a round adding fewer than this many descriptions counts as stalled")
    # store_true (not store_false): build_config drops False overrides, so a negative flag
    # needs its own truthy dest.
    sp.add_argument("--no-gen-progress", action="store_true", dest="no_gen_progress",
                    help="disable the live per-fixture progress bar")
    sp.add_argument("--coverage-min", type=float, dest="coverage_min", default=None,
                    help="skip fixtures already at/above this description coverage")
    sp.add_argument("--regenerate", action="store_true", dest="regenerate",
                    help="re-visit fixtures already at/above coverage_min. NOTE: the chunked "
                         "driver only ever describes nodes that have NO description, so this "
                         "re-checks a warm fixture rather than rewriting its descriptions")
    add_config(sp)

    # run ----------------------------------------------------------------- #
    sp = sub.add_parser("regrade",
                        help="re-grade a finished run's SAVED PATCHES (recovers verdicts lost to "
                             "a low stall cap or a missing harness) without re-running any agent")
    sp.add_argument("regrade_run", metavar="run",
                    help="run id/name under bench/results/, or a path to the results dir")
    sp.add_argument("--grade-timeout-s", type=int, dest="grade_timeout_s", default=None)
    sp.add_argument("--grade-stall-timeout-s", type=int, dest="grade_stall_timeout_s", default=None)
    sp.add_argument("--ni-margin", dest="ni_margin", type=float, default=None)
    sp.add_argument("--analysis", action="store_true", dest="want_analysis",
                    help="regenerate the LLM prose analysis after re-scoring")
    sp.add_argument("--config", default=None)

    sp = sub.add_parser("rescore",
                        help="re-analyse a finished run's runs.jsonl with the current metrics "
                             "(no agents, no grading, no tokens spent)")
    sp.add_argument("rescore_run", metavar="run",
                    help="run id/name under bench/results/, or a path to the results dir")
    sp.add_argument("--ni-margin", dest="ni_margin", type=float, default=None,
                    help="non-inferiority margin for the paired solve-rate test (default 0.10)")
    sp.add_argument("--no-analysis", action="store_true", dest="no_analysis",
                    help="skip the LLM prose analysis (rescore skips it by default)")
    sp.add_argument("--analysis", action="store_true", dest="want_analysis",
                    help="regenerate the LLM prose analysis too (costs a Claude Code call)")
    sp.add_argument("--config", default=None)

    sp = sub.add_parser("run", help="execute the A/B matrix over the manifest + warm fixtures")
    sp.add_argument("--sample-id", dest="sample_id", default=None)
    sp.add_argument("--seeds", type=int, default=None, help="repetitions per task per arm")
    sp.add_argument("--arms", default=None, help="comma list: baseline,aracne")
    sp.add_argument("--run-harness", dest="run_harness", default=None,
                    help="claude_code | opencode (same for both arms)")
    sp.add_argument("--model", default=None,
                    help="model for BOTH arms: claude alias (haiku/sonnet) or opencode provider/model")
    sp.add_argument("--max-turns", type=int, dest="max_turns", default=None)
    sp.add_argument("--effort", default=None,
                    choices=["low", "medium", "high", "xhigh", "max"],
                    help="claude_code reasoning effort for BOTH arms; omitted -> CLI default")
    sp.add_argument("--timeout-s", type=int, dest="timeout_s", default=None)
    sp.add_argument("--out", default=None, help="results directory (overrides results/<run-name>)")
    sp.add_argument("--run-name", dest="run_name", default=None,
                    help="unique name for this run; results go to results/<run-name> (default: random id)")
    sp.add_argument("--aracne-config", dest="aracne_config", default=None,
                    help="aracne arm's .aracne/config.json overlay: a name in bench/configs/aracne/ or a path")
    sp.add_argument("--resume", action="store_true", help="skip combos already in runs.jsonl")
    sp.add_argument("--baseline-from", dest="baseline_from", metavar="RUN",
                    help="reuse rows for arms NOT in --arms from a prior run (id/name/path). "
                         "The baseline arm never loads aracne, so its result is unchanged by "
                         "an aracne change and does not need re-measuring. Requires the same "
                         "model and max_turns.")
    sp.add_argument("--continue", dest="continue_run", default=None, metavar="RUN",
                    help="continue a prior run by id/name: re-run its errored + not-yet-run steps "
                         "using that run's saved config snapshot (config flags are ignored)")
    sp.add_argument("--no-grade", action="store_true", dest="no_grade",
                    help="run agents + capture metrics but skip Docker grading")
    sp.add_argument("--no-analysis", action="store_true", dest="no_analysis",
                    help="skip the end-of-run LLM results analysis")
    sp.add_argument("--dry-run", action="store_true", dest="dry_run",
                    help="print the run matrix and exit")
    sp.add_argument("--grade-timeout-s", type=int, dest="grade_timeout_s", default=None,
                    help="wall-clock cap for a grading harness call (0 = no cap); "
                         "runs it covered are recorded as timeouts, not failures")
    sp.add_argument("--grade-stall-timeout-s", type=int, dest="grade_stall_timeout_s", default=None,
                    help="kill a grading harness that writes NO output for this long (0 = off) — "
                         "catches wedged docker builds without punishing slow ones")
    sp.add_argument("--parallel", type=int, dest="run_parallel", default=None,
                    help="matrix cells to run concurrently (default 1 = sequential). Speeds up "
                         "a run but inflates wall-clock metrics and burns rate limits; "
                         "tokens/turns stay unaffected")
    sp.add_argument("--retry-timeouts", action="store_true", dest="retry_timeouts",
                    help="with --continue: re-run steps that timed out or hit --max-turns "
                         "(default: keep them as recorded timeouts)")
    sp.add_argument("--allow-cold", action="store_true", dest="allow_cold",
                    help="bypass the description-coverage guardrail for the aracne arm")
    sp.add_argument("--coverage-min", type=float, dest="coverage_min", default=None,
                    help="minimum description coverage required for the aracne arm")
    sp.add_argument("--keep-workdir", action="store_true", dest="keep_workdir",
                    help="keep ephemeral baseline checkouts for debugging")
    add_config(sp)

    return p.parse_args(argv)


def build_config(args: argparse.Namespace) -> dict:
    cfg = dict(DEFAULTS)
    cfg["sources"] = json.loads(json.dumps(DEFAULTS["sources"]))  # deep copy
    config_path = _resolve_config_path(getattr(args, "config", None))
    if config_path and config_path.exists():
        cfg = _deep_merge(cfg, _load_yaml(config_path))

    # Use identity checks: drop unset args (None) and unset store_true flags (False),
    # but KEEP zero-valued numerics — `0 == False` would otherwise swallow --seeds 0,
    # --coverage-min 0, --max-turns 0, etc. (`0 is False` is False, so identity is safe).
    overrides = {k: v for k, v in vars(args).items()
                 if k not in ("config", "command") and v is not None and v is not False}
    for listkey in ("languages", "arms", "gen_kinds"):
        if isinstance(overrides.get(listkey), str):
            overrides[listkey] = [s.strip() for s in overrides[listkey].split(",") if s.strip()]
    cfg = _deep_merge(cfg, overrides)
    # Runtime-only: the resolved config file (if any), so cmd_run can copy it into the run dir.
    cfg["config_path"] = str(config_path) if (config_path and config_path.exists()) else None
    # Runtime-only: the resolved aracne-config overlay (from the YAML or --aracne-config).
    cfg["aracne_config_path"] = _resolve_aracne_config_path(cfg.get("aracne_config"))
    return cfg


# --------------------------------------------------------------------------- #
# runs.jsonl helpers (live append, crash-safe, resume-able)
# --------------------------------------------------------------------------- #

def _read_done(runs_path: Path) -> tuple[set, list]:
    keys, rows = set(), []
    if runs_path.exists():
        for line in runs_path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            r = json.loads(line)
            keys.add(r["run_key"])
            rows.append(r)
    return keys, rows


def _append(runs_path: Path, row: dict) -> None:
    with runs_path.open("a", encoding="utf-8") as f:
        f.write(json.dumps(row) + "\n")


def _rewrite(runs_path: Path, rows: list) -> None:
    with runs_path.open("w", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")


def _default_run_id() -> str:
    """Sortable + human-readable + unique id when --run-name is not given."""
    ts = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    return f"run-{ts}-{uuid.uuid4().hex[:6]}"


# --------------------------------------------------------------------------- #
# Run status + --continue (re-run errored / not-yet-run steps from a snapshot)
# --------------------------------------------------------------------------- #

# Runtime-only / ephemeral keys excluded from the config snapshot saved in run_meta.json.
_SNAPSHOT_SKIP = {"config_path", "aracne_config_path", "continue_run", "dry_run", "resume", "out"}


def _cfg_snapshot(cfg: dict) -> dict:
    """JSON-safe copy of the effective config (minus ephemeral keys), embedded in run_meta.json
    at the start of a run so `--continue` can replay it faithfully."""
    return {k: v for k, v in cfg.items() if k not in _SNAPSHOT_SKIP}


def _update_status(out_dir: Path, status: str) -> None:
    """Patch run_meta.json's status/updated fields (running -> complete | pending)."""
    mp = out_dir / "run_meta.json"
    try:
        m = json.loads(mp.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        m = {}
    m["status"] = status
    m["updated"] = datetime.datetime.now().isoformat(timespec="seconds")
    mp.write_text(json.dumps(m, indent=2), encoding="utf-8")


def _continue_dir(cont: str) -> Path:
    """Resolve a --continue value (run id/name, or a path to a results dir) to that directory."""
    p = Path(cont)
    if (p / "run_meta.json").exists():
        return p
    return HERE / "results" / cont


def _load_snapshot_cfg(out_dir: Path, live: dict) -> tuple[dict, dict]:
    """Rebuild cfg from a run's saved snapshot so --continue matches the original run exactly.

    Uses run_meta.json's embedded `config` snapshot plus the config.used.yaml /
    aracne_config.used.json copies saved at the start of that run. A couple of harmless toggles
    (dry_run / no_analysis) are carried over from the current invocation. Returns (cfg, meta)."""
    mp = out_dir / "run_meta.json"
    if not mp.exists():
        raise SystemExit(f"cannot continue: {out_dir} has no run_meta.json (is the run id/name correct?)")
    meta = json.loads(mp.read_text(encoding="utf-8"))
    snap = meta.get("config")
    if not snap:
        raise SystemExit(f"cannot continue: {mp} has no saved 'config' snapshot (run predates --continue).")
    cfg = _deep_merge(json.loads(json.dumps(DEFAULTS)), snap)   # snapshot over a fresh DEFAULTS copy
    aracne_snap = out_dir / "aracne_config.used.json"
    cfg["aracne_config_path"] = str(aracne_snap) if aracne_snap.exists() else None
    used_yaml = out_dir / "config.used.yaml"
    cfg["config_path"] = str(used_yaml) if used_yaml.exists() else None
    # Operational toggles belong to the INVOCATION, not the experiment: a snapshot taken
    # before the harness wedged must not force grading on every later --continue.
    for k in ("dry_run", "no_analysis", "retry_timeouts", "no_grade", "keep_workdir"):
        if live.get(k):
            cfg[k] = live[k]
    # Grading caps and the concurrency level are operational, not part of the experiment:
    # honour them when the current invocation set them explicitly (i.e. they differ from
    # DEFAULTS), else keep the snapshot's.
    for k in ("grade_timeout_s", "grade_stall_timeout_s", "run_parallel"):
        if live.get(k) != DEFAULTS[k]:
            cfg[k] = live[k]
    cfg["run_name"] = meta.get("run_name") or out_dir.name
    return cfg, meta


def _keep_on_continue(row: dict, retry_timeouts: bool) -> bool:
    """Should `--continue` keep this prior row as done, or re-run its matrix cell?

    Done-OK rows stay and errored rows are re-run from scratch. A TIMEOUT — wall-clock or
    turn-cap exhaustion — is a recorded outcome rather than a broken step, so it is kept by
    default: re-running it silently would turn "this task runs out of budget" into a result
    that depends on when you last continued. `--retry-timeouts` opts into running them again
    (e.g. after raising `timeout_s` or `max_turns`)."""
    if outcome.is_timeout(row):
        return not retry_timeouts
    if row.get("error"):
        return False
    # A row with no turns AND no context is not a measurement, whatever it claims. The CLI
    # can exit without a usable result event (a dropped connection, a crash after the last
    # assistant message) and leave `error` unset — `vuejs__core-8911` did exactly that after
    # 67 real tool calls. Keeping it would count a zero-turn row in the turn mean.
    if not row.get("num_turns") and not (row.get("input_tokens") or row.get("cache_tokens")):
        return False
    return True


def _collect_to_regrade(all_rows: list[dict], tasks: list) -> list[tuple]:
    """(row, task, arm, patch) for rows whose VERDICT is missing but whose patch survives.

    Recovers runs lost to a grading-side problem — a stall cap set too low, a grading
    harness that was not installed — WITHOUT re-running the agent. The patch is the
    expensive artifact and it is already on disk; only the Docker verdict is missing.

    A run cut off on the AGENT side (wall-clock or turn cap) is deliberately NOT eligible.
    That run never finished writing its patch, so grading it would manufacture a `fail` out
    of a run that simply ran out of budget — exactly the conversion the rule exists to
    prevent. Rows that already
    carry a real verdict are left untouched, so re-grading is idempotent.
    """
    by_key = {t.key: t for t in tasks}
    out: list[tuple] = []
    for r in all_rows:
        if r.get("success") is not None or r.get("error") or not r.get("has_patch"):
            continue
        if r.get("timeout_stage") in outcome.AGENT_STAGES:
            continue
        task, pp = by_key.get(r.get("instance_id")), r.get("patch_path")
        if not task or not pp:
            continue
        try:
            patch = Path(pp).read_text(encoding="utf-8")
        except OSError:
            continue
        out.append((r, task, r["arm"], patch))
    return out


def _cell_done_line(i: int, total: int, row: dict) -> str:
    """One line summarising a finished cell — the only progress a parallel run can print in
    a meaningful order, since cells no longer complete in matrix order."""
    oc = row.get("outcome") or outcome.classify(row)
    if oc == outcome.TIMEOUT:
        stage = row.get("timeout_stage") or "?"
        label = "TURN LIMIT" if stage == outcome.TURNS else "TIMEOUT"
        return (f"  <- [{i}/{total}] {label} ({stage}): {row['error']}; "
                f"recorded as timeout (neither pass nor fail), continuing.")
    if row.get("error"):
        return f"  <- [{i}/{total}] ERROR {row['run_key']}: {str(row['error'])[:160]}"
    return (f"  <- [{i}/{total}] done {row['run_key']}  turns={row.get('num_turns')} "
            f"{row.get('duration_ms', 0) / 1000:.0f}s ${row.get('cost_usd', 0):.2f}"
            f"{'' if row.get('has_patch') else '  (no patch)'}")


def _run_matrix(matrix: list, done: set, all_rows: list, runs_path: Path, cfg: dict,
                out_dir: Path, repos_dir: Path, work_dir: Path, fixtures_root: Path):
    """Execute every not-yet-done cell, `run_parallel` at a time. Returns (run_key, error)
    for the first HARD error, or None if the matrix finished.

    Concurrency is bounded by a thread pool (the work is all subprocesses, so threads are
    the right tool) and made safe by the keyed locks in bench/bench/locks.py. Two rules are
    preserved exactly as in the sequential path:

      - a TIMEOUT (wall-clock or turn cap) is a verdict for its cell, so the matrix carries on;
      - a hard error stops the run — but with cells in flight, "stop" means STOP SCHEDULING:
        queued cells are dropped and the already-running ones are allowed to finish and be
        recorded, since their agent time is already spent. The first error is the reported one.

    Rows are appended to runs.jsonl the moment each cell lands (crash-safety is worth more
    than file order — `_sort_rows` restores matrix order at the end of the run).
    """
    par = max(1, int(cfg.get("run_parallel") or 1))
    total = len(matrix)
    pending = []
    for i, (task, arm, seed) in enumerate(matrix, 1):
        run_key = f"{task.key}|{arm}|{seed}"
        if run_key in done:
            print(f"[{i}/{total}] skip (done) {run_key}")
        else:
            pending.append((i, task, arm, seed))
    if not pending:
        return None

    io_lock = threading.Lock()      # guards runs.jsonl, all_rows and stdout interleaving
    stop = threading.Event()
    errors: list[tuple[str, str]] = []

    def cell(i: int, task, arm: str, seed: int):
        if stop.is_set():
            return                  # a hard error landed before this cell started: never run it
        with io_lock:
            print(f"[{i}/{total}] {task.language:<11} {arm:<8} seed{seed}  {task.key}")
        try:
            row, _patch = runner.run_one(task, arm, seed, cfg, out_dir, repos_dir,
                                         work_dir, fixtures_root)
        except Exception as e:  # noqa: BLE001
            row = runner.error_row(task, arm, seed, f"runner crash: {e}")
        with io_lock:
            _append(runs_path, row)
            all_rows.append(row)
            print(_cell_done_line(i, total, row))
            if row.get("error") and not outcome.is_timeout(row):
                errors.append((row["run_key"], row["error"]))
                stop.set()

    if par > 1:
        print(f"Running {len(pending)} step(s) {par} at a time — wall-clock per run is "
              f"inflated by contention; tokens and turns are not.")
    with ThreadPoolExecutor(max_workers=par) as ex:
        futs = [ex.submit(cell, *item) for item in pending]
        try:
            for f in futs:
                f.result()          # cells swallow their own errors; this just joins them
        except KeyboardInterrupt:
            # The pool's shutdown waits for whatever is running, so drop the queue first —
            # otherwise Ctrl-C would appear to hang while N more cells started and finished.
            # Cells already in flight are left to land: their rows are worth keeping.
            stop.set()
            for f in futs:
                f.cancel()
            print("\nInterrupted — waiting for the in-flight step(s) to land ...")
            raise
    return errors[0] if errors else None


def _sort_rows(all_rows: list, matrix: list) -> None:
    """Put rows back in matrix order, in place. Parallel cells finish out of order, and a
    results file whose order depends on scheduling would make two identical runs diff."""
    order = {f"{t.key}|{a}|{s}": i for i, (t, a, s) in enumerate(matrix)}
    all_rows.sort(key=lambda r: order.get(r.get("run_key"), len(order)))


def _collect_to_grade(all_rows: list[dict], tasks: list, out_dir: Path) -> list[tuple]:
    """(row, task, arm, patch) tuples for every patched-but-ungraded, non-errored row.

    Covers both freshly-run rows and rows carried over from a --continue, and never re-grades an
    already-graded row (success is not None), so grading stays correct across continuations.
    Timed-out runs are skipped outright: their outcome is already decided, and grading a patch
    the agent never finished writing would turn a timeout into a fail."""
    by_key = {t.key: t for t in tasks}
    out: list[tuple] = []
    for r in all_rows:
        if r.get("error") or not r.get("has_patch") or r.get("success") is not None:
            continue
        if outcome.is_timeout(r):
            continue
        task = by_key.get(r.get("instance_id"))
        pp = r.get("patch_path")
        if not task or not pp:
            continue
        try:
            patch = Path(pp).read_text(encoding="utf-8")
        except OSError:
            continue
        out.append((r, task, r["arm"], patch))
    return out


def _filter_languages(tasks: list, cfg: dict, what: str) -> list:
    """Restrict a manifest's tasks to cfg["languages"].

    A manifest is per sample_id and can span more languages than a given config wants
    (e.g. the `multi` manifest holds js/ts/rust/python while backend.yaml asks for go+rust),
    so every consumer of a manifest has to honour the language list itself."""
    wanted = cfg.get("languages")
    if not wanted:
        return tasks
    kept = [t for t in tasks if t.language in wanted]
    if len(kept) != len(tasks):
        dropped = sorted({t.language for t in tasks} - set(wanted))
        print(f"Languages {list(wanted)}: {what} {len(kept)}/{len(tasks)} task(s) "
              f"(dropped languages: {', '.join(dropped)})")
    missing = [lang for lang in wanted if not any(t.language == lang for t in kept)]
    if missing:
        print(f"  note: no task(s) for {', '.join(missing)} in this manifest "
              f"(sample_id={cfg['sample_id']}) — sample/prepare it first to include them.")
    return kept


def _unique_tasks(tasks: list) -> list:
    """Collapse the manifest to one task per unique fixture key (repo@commit)."""
    seen: set[str] = set()
    out = []
    for t in tasks:
        key = fixtures.fixture_key(t)
        if key in seen:
            continue
        seen.add(key)
        out.append(t)
    return out


# --------------------------------------------------------------------------- #
# Subcommands
# --------------------------------------------------------------------------- #

def _import_prior_rows(cfg: dict, matrix: list, runs_path: Path,
                       done: set, all_rows: list) -> None:
    """Seed rows for an arm from a PRIOR run instead of re-running it.

    WHY. The baseline arm is plain Claude Code: it never loads aracne, never touches a
    fixture's topology, and works in a throwaway clone at the task's base commit. Its result
    for a given (task, seed, model, max_turns) therefore does not change when aracne does —
    so re-running it to re-measure an aracne change is pure spend. On the pyjsts matrix that
    is half the cells.

    Rows are matched on (instance_id, seed) and only for arms NOT being run now. Model and
    max_turns must agree, because both change what a run costs; a mismatch is refused rather
    than silently producing an incomparable pair.

    Imported rows are tagged `imported_from` so a reader can tell measured from reused.
    """
    src = cfg.get("baseline_from")
    if not src:
        return
    src_dir = _continue_dir(src)
    src_runs = src_dir / "runs.jsonl"
    if not src_runs.exists():
        raise SystemExit(f"--baseline-from {src}: no runs.jsonl at {src_runs}")

    src_meta = {}
    meta_path = src_dir / "run_meta.json"
    if meta_path.exists():
        src_meta = json.loads(meta_path.read_text(encoding="utf-8"))
    for key in ("model", "max_turns", "effort"):
        theirs, ours = src_meta.get(key), cfg.get(key)
        # `effort` is checked even when the SOURCE predates the key. An unrecorded effort
        # means "whatever the CLI defaulted to", which is not the same run as one pinned
        # with --effort -- so a missing value must not read as agreement.
        if theirs is None and key != "effort":
            continue
        if str(theirs) != str(ours):
            raise SystemExit(
                f"--baseline-from {src}: {key}={theirs!r} but this run uses {ours!r}. "
                f"Rows from a different {key} are not comparable; re-run the arm instead.")

    running = set(cfg["arms"])
    wanted = {(t.key, seed) for t, arm, seed in matrix}
    zero = toolstats.row_fields(toolstats.empty_stats())

    imported, no_telemetry, already = 0, 0, 0
    for line in src_runs.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        if row.get("arm") in running:
            continue                      # this run is measuring that arm itself
        if (row.get("instance_id"), row.get("seed")) not in wanted:
            continue                      # not a task in this matrix
        if row.get("run_key") in done:
            already += 1                  # --resume already loaded it; do not duplicate
            continue
        # Older runs predate tool telemetry; fill the keys so every row has one shape.
        if "n_tool_calls" not in row:
            row.update(zero)
            no_telemetry += 1
        row["imported_from"] = str(src_dir.name)
        outcome.stamp(row)
        all_rows.append(row)
        done.add(row["run_key"])
        imported += 1

    if imported == 0 and already == 0:
        raise SystemExit(f"--baseline-from {src}: no reusable rows matched this matrix.")
    if imported == 0:
        # Everything was already present from --resume. That is the normal steady state on
        # a resumed run, not an error.
        print(f"Reusing {already} row(s) already carried over from '{src_dir.name}'.")
        return
    _rewrite(runs_path, all_rows)
    note = ""
    if no_telemetry:
        note = (f"  ({no_telemetry} predate tool telemetry — their per-tool counters are "
                f"zero, so MCP-vs-native comparisons cover the measured arm only)")
    carried = f" ({already} already present)" if already else ""
    print(f"Imported {imported} row(s) from '{src_dir.name}' instead of re-running them"
          f"{carried}.{note}")


def _drops_path(cfg: dict) -> Path:
    return Path(cfg["samples_dir"]) / f"{cfg['sample_id']}.drops.jsonl"


def _read_drops(cfg: dict) -> list[dict]:
    path = _drops_path(cfg)
    if not path.exists():
        return []
    out = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line:
            try:
                out.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return out


def _write_drops(cfg: dict, drops: list[dict]) -> Path:
    """Persist why every candidate that did not reach the final manifest was dropped.

    WHY. `scale40` asked for 40 tasks and ran 27; go went 8->3, typescript 8->3. Ten
    distinct drop paths exist across sample/prepare/freeze/run and, before this, exactly
    one of them (the coverage guardrail) recorded a reason anywhere durable — the rest
    printed to stdout and vanished. A language-correlated silent shortfall is exactly the
    kind of thing that quietly biases a benchmark, so it belongs on disk next to the
    manifest it explains.
    """
    path = _drops_path(cfg)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as fh:
        for d in drops:
            fh.write(json.dumps(d, sort_keys=True) + "\n")
    return path


def cmd_sample(cfg: dict) -> int:
    oversample = max(1, int(cfg.get("oversample", 1)))
    n = cfg["samples"] * oversample
    print(f"Sampling {n}/lang (= {cfg['samples']} target x oversample {oversample}) "
          f"x {cfg['languages']} (seed={cfg['sample_seed']}) ...")
    max_per_repo = int(cfg.get("max_per_repo", 1))
    tasks_by_lang = load_tasks(cfg["languages"], n, cfg["sample_seed"], cfg,
                               max_per_repo=max_per_repo)
    path = sources.candidates_path(cfg["samples_dir"], cfg["sample_id"])
    sources.write_manifest(tasks_by_lang, path)
    total = sum(len(v) for v in tasks_by_lang.values())
    drops: list[dict] = []
    for lang, tasks in tasks_by_lang.items():
        repos = sources.repo_counts(tasks)
        note = ""
        if len(tasks) < n:
            drops.append({
                "stage": "sample", "language": lang, "key": None,
                "reason": "repo_pool_exhausted",
                "detail": (f"asked {n} candidates, dataset yielded {len(tasks)} over "
                           f"{len(repos)} distinct repo(s) at max_per_repo={max_per_repo}"),
                "requested": n, "got": len(tasks), "distinct_repos": len(repos),
            })
            # The pool ran out of DISTINCT repos before it ran out of tasks. Say so loudly:
            # silently returning fewer candidates would shrink the final effective N.
            note = (f"  <- short of {n}: only {len(repos)} distinct repo(s) available "
                    f"at max_per_repo={max_per_repo}")
        print(f"  [{lang}] {len(tasks)} candidate(s) over {len(repos)} repo(s){note}")
    if drops:
        print(f"Recorded {len(drops)} sample-stage shortfall(s) -> {_write_drops(cfg, drops)}")
    else:
        _write_drops(cfg, [])
    print(f"\nWrote {total} candidate(s) to {path}")
    print(f"Repo diversity: max_per_repo={max_per_repo} "
          f"(effective N for the paired analysis is the REPO count, not the task count).")
    print("NEXT: python bench/run_benchmark.py prepare   (scans, measures size, prunes to final manifest)")
    return 0


def cmd_prepare(cfg: dict) -> int:
    fixtures_root = Path(cfg["fixtures_dir"])
    candidates = sources.read_manifest(sources.candidates_path(cfg["samples_dir"], cfg["sample_id"]))
    cap = cfg["max_describable_nodes"]
    target = cfg["samples"]
    print(f"Preparing fixtures: target {target}/lang, drop repos with > {cap} describable nodes "
          f"(kinds={cfg['gen_kinds']}) ...")

    kept_by_lang: dict[str, list] = defaultdict(list)
    seen: set[str] = set()
    # Carries the sample-stage shortfalls forward so one file explains the whole gap
    # between "tasks requested" and "tasks in the final manifest".
    drops: list[dict] = _read_drops(cfg)

    def _drop(t, key, reason, detail):
        drops.append({"stage": "prepare", "language": t.language, "key": key,
                      "reason": reason, "detail": detail})

    for t in candidates:
        key = fixtures.fixture_key(t)
        if t.language not in cfg["languages"]:
            _drop(t, key, "language_not_selected",
                  f"candidate language {t.language!r} not in {cfg['languages']}")
            continue
        if len(kept_by_lang[t.language]) >= target:
            # language already filled — skip remaining candidates (saves clone+scan)
            _drop(t, key, "language_target_filled", f"target {target} already reached")
            continue
        if key in seen:
            _drop(t, key, "duplicate_fixture_key", "same repo@commit already prepared")
            continue
        seen.add(key)
        try:
            meta = fixtures.scaffold(t, cfg, fixtures_root)
        except subprocess.CalledProcessError as e:
            detail = (e.stderr or str(e))[-400:].strip()
            print(f"  fail  {key:<46} (scaffold: {detail[-160:]})")
            _drop(t, key, "scaffold_failed", detail)
            continue
        n = meta.get("describable", 0)
        wt = fixtures.worktree_path(fixtures_root, t)
        if cap and n > cap:
            print(f"  drop  {key:<46} ({n} nodes > {cap})")
            _drop(t, key, "node_cap_exceeded", f"{n} describable nodes > cap {cap}")
            continue
        kept_by_lang[t.language].append(t)
        print(f"  keep  {key:<46} ({n} nodes) -> {wt}")

    final = {lang: kept_by_lang.get(lang, []) for lang in cfg["languages"]}
    sources.write_manifest(final, sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    total = sum(len(v) for v in final.values())
    print(f"\nFinal manifest: {total} fixture(s).")
    for lang in cfg["languages"]:
        got = len(final.get(lang, []))
        flag = "" if got >= target else f"   (only {got}/{target} — raise --oversample or --max-nodes)"
        print(f"  [{lang}] {got}/{target}{flag}")
    print(f"\nDrop ledger ({len(drops)} entr(y/ies)) -> {_write_drops(cfg, drops)}")
    for reason in dict.fromkeys(d["reason"] for d in drops):
        n_r = sum(1 for d in drops if d["reason"] == reason)
        print(f"  {reason}: {n_r}")
    print("\nNEXT: python bench/run_benchmark.py generate   (fills descriptions; then freeze, then run)")
    return 0


def cmd_freeze(cfg: dict) -> int:
    fixtures_root = Path(cfg["fixtures_dir"])
    tasks = sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    tasks = _unique_tasks(_filter_languages(tasks, cfg, "freezing"))
    print(f"Freezing {len(tasks)} fixture(s) ...\n")
    print(f"  {'fixture':<48} {'described/total':>16} {'coverage':>9}")
    low = 0
    for t in tasks:
        meta = fixtures.freeze(t, cfg, fixtures_root)
        cov = meta.get("coverage") or 0.0
        frac = f"{meta.get('described')}/{meta.get('total')}"
        flag = "  (LOW)" if cov < cfg["coverage_min"] else ""
        if cov < cfg["coverage_min"]:
            low += 1
        print(f"  {meta['key']:<48} {frac:>16} {cov:>8.0%}{flag}")
    if low:
        print(f"\nWARNING: {low} fixture(s) below coverage_min={cfg['coverage_min']:.0%}. "
              f"`run` will REFUSE these (bench/bench/arms.py) unless you pass --allow-cold — "
              f"and an aracne arm with half its topology undescribed measures nothing. "
              f"Re-run `generate` (resumable), then freeze again.")
    return 0


def _gen_mcp_config(cfg: dict, fixtures_root: Path) -> tuple[list[str], Path | None]:
    """Build the extra `claude` args that expose the FULL aracne toolset during generation.

    The orchestrator needs `node_list_no_description` and the executors need
    `update_description`; the shipped `main` profile in each worktree's `.mcp.json` exposes
    neither. We point `claude` at a temporary `--tool-profile all` MCP config (and
    `--strict-mcp-config` so only it is used) WITHOUT editing any worktree's `.mcp.json`,
    so `freeze`/`run` still measure the shipped profile (no A/B contamination)."""
    if not cfg.get("gen_mcp_override", True):
        return [], None
    fixtures_root.mkdir(parents=True, exist_ok=True)
    mcp_path = fixtures_root / "_gen_mcp_all.json"
    mcp_path.write_text(json.dumps({
        "mcpServers": {
            "aracne": {
                "command": cfg["arac_bin"],
                "args": ["serve", "--tool-profile", "all", "--harness", "claude_code"],
            }
        }
    }, indent=2), encoding="utf-8")
    return ["--mcp-config", str(mcp_path), "--strict-mcp-config"], mcp_path


def _gen_line(prefix: str, r: dict) -> str:
    """One per-fixture line for `generate`. The chunked driver reports how many rounds and
    worker chunks it spent (and how many workers came back empty), because those are what
    explain a fixture that ended short of target."""
    tail = ""
    if r.get("rounds"):
        tail = f", {r['rounds']}r/{r['chunks']}c"
        if r.get("failed_chunks"):
            tail += f", {r['failed_chunks']} failed"
    return (f"{prefix} {r['key']:<46} {r['status']:>11}  "
            f"{r['before']:.0%}->{r['after']:.0%} "
            f"({r['described']}/{r['total']}, {r['dur_s']}s{tail})")


def cmd_generate(cfg: dict) -> int:
    fixtures_root = Path(cfg["fixtures_dir"])
    tasks = sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    tasks = _unique_tasks(_filter_languages(tasks, cfg, "generating"))

    harness = cfg["gen_harness"]
    if harness == "claude_code":
        extra_args, mcp_path = _gen_mcp_config(cfg, fixtures_root)
    else:
        extra_args, mcp_path = [], None  # opencode.json already serves --tool-profile all
    driver = cfg.get("gen_driver", "chunked")
    show_progress = bool(cfg.get("gen_progress", True)) and not cfg.get("no_gen_progress")
    print(f"Generating descriptions in {len(tasks)} fixture(s): {harness} / {cfg['gen_model']} "
          f"(kinds={cfg['gen_kinds']}, driver={driver}, fixtures in parallel={cfg['gen_parallel']}).")
    if driver == "chunked":
        print(f"  workers: {cfg['gen_chunk_parallel']} x {cfg['gen_chunk']} ids, "
              f"<= {cfg['gen_chunk_timeout_s']}s / {cfg['gen_chunk_max_turns']} turns each; "
              f"up to {cfg['gen_max_rounds']} rounds or {cfg['gen_timeout_s']}s per fixture, "
              f"target {cfg['coverage_min']:.0%}")
    else:
        print("  WARNING: the legacy `agent` driver hands each fixture to ONE orchestrator "
              "session, which typically abandons large repos after a single wave.")
    if mcp_path:
        print(f"  tool access: temp --tool-profile all ({mcp_path})")
    print()

    def warm(t) -> dict:
        key = fixtures.fixture_key(t)
        wt = fixtures.worktree_path(fixtures_root, t)
        db = wt / ".aracne" / "topology.db"
        if not db.exists():
            fixtures.scaffold(t, cfg, fixtures_root)   # generate subsumes prepare if needed
        fixtures.set_describe_kinds(wt, cfg["gen_kinds"])   # scope to the configured kinds
        des0, tot0, cov0 = fixtures.description_coverage(db, cfg["gen_kinds"])
        if cov0 >= cfg["coverage_min"] and not cfg.get("regenerate"):
            return {"key": key, "status": "skip", "before": cov0, "after": cov0,
                    "described": des0, "total": tot0, "dur_s": 0}

        if driver == "chunked":
            # Python owns the loop: chunk -> workers -> re-read the DB -> repeat. Bounded by
            # gen_max_rounds + gen_timeout_s, not by a session that quits mid-wave.
            # One fixture at a time gets the live bar; with several in flight the bars
            # would fight over stdout, so each fixture logs prefixed round lines instead
            # of going silent.
            log = ((lambda m: print(m, flush=True)) if par == 1
                   else (lambda m, k=key: print(f"  [{k}]{m}", flush=True)))
            r = chunkgen.generate_fixture(
                worktree=wt, db=db, harness=harness, model=cfg["gen_model"],
                kinds=cfg["gen_kinds"], chunk_size=cfg["gen_chunk"],
                parallel=cfg["gen_chunk_parallel"], chunk_timeout_s=cfg["gen_chunk_timeout_s"],
                chunk_max_turns=cfg["gen_chunk_max_turns"], extra_args=extra_args,
                target=cfg["coverage_min"], max_rounds=cfg["gen_max_rounds"],
                min_gain=cfg["gen_min_gain"],
                deadline=time.monotonic() + cfg["gen_timeout_s"] if cfg["gen_timeout_s"] else None,
                logfn=log, progress=show_progress and par == 1,
                log_path=wt.parent / "gen_log.jsonl")
            return {"key": key, **r}

        # --- legacy: one orchestrator session per fixture ---------------------- #
        status, dur_s = "ok", 0
        try:
            rr = agents.run_agent(harness, "/descriptions-generate", wt, cfg["gen_model"],
                                  cfg["gen_max_turns"], cfg["gen_timeout_s"], extra_args=extra_args)
            dur_s = rr.duration_ms // 1000
            if rr.is_error:
                status = "agent_error"
        except subprocess.TimeoutExpired:
            status = "timeout"
        except Exception as e:  # noqa: BLE001
            status = f"error: {e}"
        des1, tot1, cov1 = fixtures.description_coverage(db, cfg["gen_kinds"])
        if status == "ok" and cov1 < cfg["coverage_min"]:
            status = "partial"      # the session ended clean but the fixture is NOT warm
        return {"key": key, "status": status, "before": cov0, "after": cov1,
                "described": des1, "total": tot1, "dur_s": dur_s}

    results: list[dict] = []
    par = max(1, int(cfg.get("gen_parallel", 1)))
    if par > 1:
        # Pre-warm the shared _repos cache sequentially so concurrent worktrees from the
        # same repo can't race to clone it (idempotent no-op once a cache exists).
        from bench import gitutil
        repos_dir = fixtures_root / "_repos"
        for slug in sorted({t.repo_slug() for t in tasks}):
            seed_task = next(t for t in tasks if t.repo_slug() == slug)
            try:
                gitutil.ensure_repo_cache(seed_task, repos_dir)
            except Exception as e:  # noqa: BLE001
                print(f"  [pre-warm] {slug}: {e}")
    if par == 1:
        for i, t in enumerate(tasks, 1):
            r = warm(t)
            print(_gen_line(f"[{i}/{len(tasks)}]", r))
            results.append(r)
    else:
        from concurrent.futures import ThreadPoolExecutor, as_completed
        with ThreadPoolExecutor(max_workers=par) as ex:
            futs = {ex.submit(warm, t): t for t in tasks}
            for done, fut in enumerate(as_completed(futs), 1):
                r = fut.result()
                print(_gen_line(f"[{done}/{len(tasks)}]", r))
                results.append(r)

    short = [r for r in results if r["after"] < cfg["coverage_min"]]
    print(f"\nDone. {len(results)-len(short)}/{len(results)} fixture(s) at/above coverage_min="
          f"{cfg['coverage_min']:.0%}.")
    if short:
        print(f"WARNING: {len(short)} still below — do NOT freeze yet:")
        for r in short:
            print(f"  {r['key']:<46} {r['after']:>4.0%}  ({r['status']})")
        print("Re-run `generate` (resumable — warm fixtures are skipped). Persistent "
              "'stalled' means workers are failing: see fixtures/<key>/gen_log.jsonl. "
              "'timeout' / 'max_rounds' just need more room (--gen-timeout-s, --gen-max-rounds).")
    print("\nNEXT: python bench/run_benchmark.py freeze   # snapshot the warmed fixtures")
    return 0


def _check_fixture_configs(tasks, fixtures_root: Path, cfg: dict) -> None:
    """Refuse to run the aracne arm over fixtures that disagree on `.aracne/config.json`.

    `fixtures.scaffold` only re-runs `arac init` when .mcp.json/.opencode.json is missing,
    so every fixture stays pinned to whatever arac build first touched it. In the scale40
    pool that produced four distinct configs across 36 fixtures — two of them in the run,
    with `blocked_tools: []` where the rest had the shipped
    ["read","grep","edit","write"]. The aracne arm was therefore a MIXTURE of two different
    products, silently averaged into one ratio. This makes that state loud instead.

    Set BENCH_ALLOW_CONFIG_DRIFT=1 to proceed anyway (e.g. a deliberate A/B of two configs).
    """
    groups: dict[str, list[str]] = defaultdict(list)
    for t in tasks:
        wt = fixtures.worktree_path(fixtures_root, t)
        meta = fixtures.read_meta(t, fixtures_root) or {}
        fp = meta.get("config_fingerprint") or fixtures.config_fingerprint(wt)
        groups[fp or "<missing>"].append(fixtures.fixture_key(t))
    if len(groups) <= 1:
        only = next(iter(groups), "")
        print(f"Fixture config: uniform ({only or 'n/a'}) across {len(tasks)} fixture(s).")
        return

    lines = ["Fixture .aracne/config.json is NOT uniform across the manifest:"]
    for fp, keys in sorted(groups.items(), key=lambda kv: -len(kv[1])):
        sample = ", ".join(keys[:4]) + (f", +{len(keys) - 4} more" if len(keys) > 4 else "")
        lines.append(f"  {fp}: {len(keys)} fixture(s)  [{sample}]")
    lines.append("")
    lines.append("The aracne arm would measure a MIXTURE of configurations, so the ratio "
                 "would not describe any single product.")
    lines.append("Fix: re-run `arac init` on the odd fixtures (delete their .mcp.json and "
                 "re-run `prepare --force-prepare`), then `freeze` again.")
    lines.append("Override: BENCH_ALLOW_CONFIG_DRIFT=1 to proceed anyway.")
    msg = "\n".join(lines)
    if os.environ.get("BENCH_ALLOW_CONFIG_DRIFT") == "1":
        print(msg + "\n[proceeding: BENCH_ALLOW_CONFIG_DRIFT=1]")
        return
    raise SystemExit(msg)


def cmd_run(cfg: dict) -> int:
    # --continue replays a prior run's saved config snapshot, then re-runs only its errored /
    # not-yet-run steps. It must rebuild cfg before the matrix is built, so handle it first.
    continuing = bool(cfg.get("continue_run"))
    out_dir = None
    meta = None
    if continuing:
        out_dir = _continue_dir(cfg["continue_run"])
        cfg, meta = _load_snapshot_cfg(out_dir, cfg)
        print(f"Continuing run '{cfg['run_name']}'  <-  {out_dir}")

    # OpenCode expects provider/model (e.g. deepseek/deepseek-chat); haiku/sonnet are
    # Claude Code aliases and would fail at the opencode CLI.
    if cfg["run_harness"] == "opencode" and "/" not in cfg["model"]:
        raise SystemExit(
            f"opencode --run-harness needs a provider/model for --model "
            f"(e.g. deepseek/deepseek-chat); got {cfg['model']!r}.")
    fixtures_root = Path(cfg["fixtures_dir"])

    # Continue reads the run's FROZEN manifest snapshot so the task set is identical to the original.
    if continuing and (out_dir / "manifest.jsonl").exists():
        tasks = sources.read_manifest(out_dir / "manifest.jsonl")
    else:
        tasks = sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    # The manifest can hold more languages than this config asks for (the same way `generate`
    # and `prepare` scope themselves). Applied to the frozen snapshot too, so --continue with the
    # saved config replays exactly the same task set.
    tasks = _filter_languages(tasks, cfg, "running")
    if not tasks:
        raise SystemExit(
            f"no tasks left after the languages filter {list(cfg['languages'])} on manifest "
            f"{sources.manifest_path(cfg['samples_dir'], cfg['sample_id'])} — "
            f"either widen `languages` or sample/prepare a manifest that has them.")
    if "aracne" in cfg["arms"]:
        _check_fixture_configs(tasks, fixtures_root, cfg)

    matrix = [(t, arm, seed)
              for t in tasks
              for arm in cfg["arms"]
              for seed in range(cfg["seeds"])]

    print(f"Matrix: {len(tasks)} tasks x {len(cfg['arms'])} arms x {cfg['seeds']} seeds = {len(matrix)} runs  "
          f"(harness={cfg['run_harness']}, model={cfg['model']}"f"{', effort=' + cfg['effort'] if cfg.get('effort') else ''}"f", max_turns={cfg['max_turns']}"
          f", parallel={max(1, int(cfg.get('run_parallel') or 1))})")

    if cfg["dry_run"]:
        for t, arm, seed in matrix:
            print(f"  {t.language:<11} {arm:<8} seed{seed}  {t.key}  @ {t.base_commit[:10]}")
        return 0

    if not continuing:
        # Resume needs an explicit target dir; a fresh random id would resume nothing.
        if cfg["resume"] and not cfg.get("run_name") and not cfg["out"]:
            raise SystemExit("--resume needs --run-name <id> (or --out <dir>) to locate an existing run.")
        run_id = cfg.get("run_name") or _default_run_id()
        cfg["run_name"] = run_id

        # Validate the aracne-arm config overlay up front so a typo/bad JSON fails before the matrix runs.
        if cfg.get("aracne_config") and not cfg.get("aracne_config_path"):
            raise SystemExit(
                f"aracne config {cfg['aracne_config']!r} not found under {ARACNE_CONFIGS_DIR} "
                f"(pass a name in bench/configs/aracne/ or a path to a JSON file).")
        if cfg.get("aracne_config_path"):
            try:
                json.loads(Path(cfg["aracne_config_path"]).read_text(encoding="utf-8"))
            except (json.JSONDecodeError, OSError) as e:
                raise SystemExit(f"aracne config {cfg['aracne_config_path']} is not valid JSON: {e}")

        out_dir = Path(cfg["out"]) if cfg["out"] else (HERE / "results" / run_id)
        out_dir.mkdir(parents=True, exist_ok=True)
        print(f"Run: {run_id}  ->  {out_dir}")
        if cfg.get("aracne_config_path"):
            print(f"aracne arm config overlay: {cfg['aracne_config']}  ({cfg['aracne_config_path']})")

        # Make the result dir self-contained: snapshot the run's inputs + config at the START.
        manifest_src = sources.manifest_path(cfg["samples_dir"], cfg["sample_id"])
        meta = {
            "run_name": run_id,
            "sample_id": cfg["sample_id"],
            "timestamp": datetime.datetime.now().isoformat(timespec="seconds"),
            "status": "running",
            "run_harness": cfg["run_harness"],
            "model": cfg["model"],
            "effort": cfg.get("effort"),
            "arms": cfg["arms"],
            "seeds": cfg["seeds"],
            "max_turns": cfg["max_turns"],
            "run_parallel": cfg["run_parallel"],
            "languages": cfg["languages"],
            "manifest": str(manifest_src),
            "config_path": cfg.get("config_path"),
            "aracne_config": cfg.get("aracne_config"),
            "aracne_config_path": cfg.get("aracne_config_path"),
            "cli_args": sys.argv[1:],
            "config": _cfg_snapshot(cfg),   # replayed verbatim by --continue
        }
        (out_dir / "run_meta.json").write_text(json.dumps(meta, indent=2), encoding="utf-8")
        if manifest_src.exists():
            shutil.copy2(manifest_src, out_dir / "manifest.jsonl")
        if cfg.get("config_path"):
            shutil.copy2(cfg["config_path"], out_dir / "config.used.yaml")
        if cfg.get("aracne_config_path"):
            shutil.copy2(cfg["aracne_config_path"], out_dir / "aracne_config.used.json")

    run_id = cfg["run_name"]
    runs_path = out_dir / "runs.jsonl"
    repos_dir = fixtures_root / "_repos"   # shared with prepare's cache
    work_dir = out_dir / "work"
    work_dir.mkdir(parents=True, exist_ok=True)

    # Decide which matrix cells are already done (and shouldn't be re-run).
    if continuing:
        _, prior = _read_done(runs_path)
        keep = [r for r in prior if _keep_on_continue(r, cfg["retry_timeouts"])]
        kept_timeouts = sum(1 for r in keep if outcome.is_timeout(r))
        done = {r["run_key"] for r in keep}
        all_rows = keep
        _rewrite(runs_path, keep)                          # drop errored rows so they're replaced cleanly
        note = f", {kept_timeouts} kept as timeout(s)" if kept_timeouts else ""
        if cfg["retry_timeouts"]:
            note = " (--retry-timeouts: timed-out steps will run again)"
        print(f"Continue: {len(done)} step(s) already done, {len(matrix) - len(done)} to (re)run "
              f"(errored steps restart from scratch){note}.")
    elif cfg["resume"]:
        done, all_rows = _read_done(runs_path)
    else:
        done, all_rows = set(), []

    # Reuse rows for arms this run is not measuring (see _import_prior_rows).
    _import_prior_rows(cfg, matrix, runs_path, done, all_rows)

    # Run the matrix, persisting each step immediately. Stop on the first machinery error and
    # leave the run PENDING so the user can --continue the remaining steps.
    stopped_error = _run_matrix(matrix, done, all_rows, runs_path, cfg,
                                out_dir, repos_dir, work_dir, fixtures_root)
    _sort_rows(all_rows, matrix)

    if stopped_error is not None:
        _update_status(out_dir, "pending")
        rk, err = stopped_error
        print(f"\n[stop] step {rk} errored: {err}")
        print(f"Run '{run_id}' saved as PENDING ({runs_path}). Continue the remaining steps with:")
        print(f"  python bench/run_benchmark.py run --continue {run_id}")
        return 0

    # Completed the matrix with no errors -> grade every patched-but-ungraded run, then report.
    to_grade = _collect_to_grade(all_rows, tasks, out_dir)
    if not cfg["no_grade"] and to_grade:
        print(f"\nGrading {len(to_grade)} patched run(s) in Docker ...")
        grade.grade_all(to_grade, cfg, out_dir)
    elif cfg["no_grade"]:
        print("\nSkipping grading (--no-grade); success left unknown.")

    # Persist success + outcome together. Rows carried over from a runs.jsonl written before
    # `outcome` existed get stamped here too, so every row on disk carries its verdict.
    for r in all_rows:
        outcome.stamp(r)
    _rewrite(runs_path, all_rows)

    tally = outcome.tally(all_rows)
    if tally[outcome.TIMEOUT]:
        print(f"\n{tally[outcome.TIMEOUT]} run(s) ran out of budget (wall-clock or turns) and "
              f"are recorded as outcome=timeout (not counted as failures):")
        for r in all_rows:
            if outcome.is_timeout(r):
                why = r.get("error") or r.get("grade_error") or "?"
                print(f"  {r['run_key']:<52} [{r.get('timeout_stage') or '?'}] {why[:90]}")
        print("  re-run just these with: "
              f"python bench/run_benchmark.py run --continue {run_id} --retry-timeouts")

    prep = metrics.prep_summary(tasks, fixtures_root)
    agg = metrics.aggregate(all_rows, cfg, prep=prep)
    report.write_results(agg, all_rows, out_dir)
    report.print_table(agg)

    analysis_text = ""
    if not cfg.get("no_analysis"):
        print("\nGenerating results analysis ...")
        analysis_text = analysis.generate_analysis(agg, cfg, out_dir, meta)
    htmlreport.write_html(agg, all_rows, meta, analysis_text, out_dir)
    _update_status(out_dir, "complete")

    print(f"\nSaved: {out_dir/'report.html'} , {out_dir/'results.json'} , "
          f"{out_dir/'summary.csv'} , {out_dir/'run_meta.json'} , {runs_path}")
    return 0


def cmd_regrade(cfg: dict) -> int:
    """Re-grade a finished run's saved patches, then re-score it.

    The agent runs are the expensive half of this benchmark and their patches are already
    on disk; a verdict lost to a too-low stall cap or a missing grading harness should cost
    a Docker rebuild, not another matrix. Raise the caps (or install the harness) and run
    this instead of `run --continue --retry-timeouts`.
    """
    out_dir = _continue_dir(cfg["regrade_run"])
    runs_path = out_dir / "runs.jsonl"
    manifest = out_dir / "manifest.jsonl"
    if not runs_path.exists():
        raise SystemExit(f"cannot regrade: {runs_path} does not exist")
    if not manifest.exists():
        raise SystemExit(f"cannot regrade: {manifest} does not exist (run predates manifest snapshots)")

    meta: dict = {}
    if (out_dir / "run_meta.json").exists():
        meta = json.loads((out_dir / "run_meta.json").read_text(encoding="utf-8"))
        snap = meta.get("config") or {}
        for key in ("samples", "languages", "seeds", "sample_seed", "arms", "run_harness",
                    "model", "effort", "max_turns", "sample_id", "sources"):
            if key in snap:
                cfg[key] = snap[key]

    all_rows = [outcome.stamp(json.loads(line))
                for line in runs_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    tasks = sources.read_manifest(manifest)
    pending = _collect_to_regrade(all_rows, tasks)
    if not pending:
        print("Nothing to re-grade: every patched run already has a verdict.")
    else:
        print(f"Re-grading {len(pending)} run(s) with grade_timeout_s="
              f"{cfg['grade_timeout_s']}s, grade_stall_timeout_s={cfg['grade_stall_timeout_s']}s ...")
        for row, _task, _arm, _patch in pending:
            print(f"  {row['run_key']:<52} (was: {row.get('outcome')})")
            # Clear the STALE verdict fields so a fresh timeout is recorded honestly and a
            # previous failure message cannot survive alongside a new pass.
            row["timeout_stage"] = None
            row.pop("grade_error", None)
            row["error"] = None
        grade.grade_all(pending, cfg, out_dir)

    for r in all_rows:
        outcome.stamp(r)
    _rewrite(runs_path, all_rows)

    recovered = sum(1 for r, *_ in pending if r.get("success") is not None)
    if pending:
        print(f"\nRecovered {recovered}/{len(pending)} verdict(s); "
              f"{len(pending) - recovered} still ungraded.")

    cfg["rescore_run"] = str(out_dir)
    return cmd_rescore(cfg)


def cmd_rescore(cfg: dict) -> int:
    """Recompute results.json / summary.csv / report.html from a finished run's rows.

    Exists because the analysis is cheap and the runs are not: when a metric is corrected or
    a new statistic is added, every historical run should be re-scored rather than re-run.
    Reads only runs.jsonl + run_meta.json, so it never touches fixtures, Docker or an agent.
    """
    out_dir = _continue_dir(cfg["rescore_run"])
    runs_path = out_dir / "runs.jsonl"
    if not runs_path.exists():
        raise SystemExit(f"cannot rescore: {runs_path} does not exist")

    meta: dict = {}
    mp = out_dir / "run_meta.json"
    if mp.exists():
        meta = json.loads(mp.read_text(encoding="utf-8"))
        # The run's OWN config decides which languages/arms its rows are grouped under; the
        # ambient config here is only carrying rescore flags.
        snap = meta.get("config") or {}
        for key in ("samples", "languages", "seeds", "sample_seed", "arms", "run_harness",
                    "model", "effort", "max_turns", "sample_id"):
            if key in snap:
                cfg[key] = snap[key]

    all_rows = [outcome.stamp(json.loads(line))
                for line in runs_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    if not all_rows:
        raise SystemExit(f"cannot rescore: {runs_path} is empty")

    # Languages present in the rows win over the snapshot: an older snapshot may not list a
    # language the rows contain, which would silently drop those rows from every table.
    seen = [r.get("language") for r in all_rows]
    cfg["languages"] = [lang for lang in dict.fromkeys(cfg.get("languages") or [])
                        if lang in seen] + [lang for lang in dict.fromkeys(seen)
                                            if lang and lang not in (cfg.get("languages") or [])]

    # Pass `prep` so rescoring does not silently drop the "preparation" block (coverage,
    # node counts, scan times) from results.json. It is recoverable from the fixtures, so
    # losing it on every rescore quietly degraded the record.
    prep = None
    try:
        tasks = sources.read_manifest(out_dir / "manifest.jsonl")
        prep = metrics.prep_summary(tasks, Path(cfg["fixtures_dir"]))
    except Exception:  # noqa: BLE001 — the manifest snapshot may be absent on old runs
        prep = None
    agg = metrics.aggregate(all_rows, cfg, prep=prep)
    report.write_results(agg, all_rows, out_dir)
    report.print_table(agg)

    analysis_text = ""
    # `--no-analysis` used to be inert here: it sets cfg["no_analysis"], but this branch
    # only ever consulted want_analysis, so the flag documented a behaviour it did not have.
    if cfg.get("no_analysis"):
        cfg["want_analysis"] = False
    if cfg.get("want_analysis"):
        print("\nRegenerating results analysis ...")
        analysis_text = analysis.generate_analysis(agg, cfg, out_dir, meta)
    elif (out_dir / "analysis.md").exists():
        # Keep the prior prose rather than blanking the report, but say it is stale: it was
        # written against the OLD numbers and may contradict the table above.
        # The banner is written back to analysis.md TOO. Previously it was prepended only to
        # the in-memory string handed to write_html, so the .md on disk stayed silently
        # stale — which is exactly how run-20260824-125118's analysis.md ended up asserting
        # "Python entirely ungraded" against a results.json where all 16 python rows graded.
        stale = ("_(carried over from the previous scoring — re-run with `--analysis` to "
                 "regenerate against these numbers)_")
        prior = (out_dir / "analysis.md").read_text(encoding="utf-8")
        body = prior.split("\n\n", 1)[1] if prior.lstrip().startswith("_(carried over") else prior
        analysis_text = stale + "\n\n" + body.lstrip()
        (out_dir / "analysis.md").write_text(analysis_text.rstrip() + "\n", encoding="utf-8")
    htmlreport.write_html(agg, all_rows, meta, analysis_text, out_dir)

    print(f"\nRescored {len(all_rows)} row(s) -> {out_dir/'results.json'} , "
          f"{out_dir/'summary.csv'} , {out_dir/'report.html'}")
    return 0


_COMMANDS = {
    "sample": cmd_sample,
    "prepare": cmd_prepare,
    "generate": cmd_generate,
    "freeze": cmd_freeze,
    "run": cmd_run,
    "rescore": cmd_rescore,
    "regrade": cmd_regrade,
}


def main(argv=None) -> int:
    args = parse_args(argv)
    cfg = build_config(args)
    return _COMMANDS[args.command](cfg)


if __name__ == "__main__":
    raise SystemExit(main())

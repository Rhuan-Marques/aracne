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
import json
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

# Allow running as `python bench/run_benchmark.py` from the repo root.
sys.path.insert(0, str(Path(__file__).resolve().parent))

from bench import agents, fixtures, grade, metrics, report, runner, sources  # noqa: E402
from bench.sources import ALL_LANGUAGES, load_tasks  # noqa: E402

HERE = Path(__file__).resolve().parent

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
    "max_turns": 30,
    "timeout_s": 1800,
    "arac_bin": "arac",
    "scan_mode": "hard",
    "coverage_min": 0.5,
    "allow_cold": False,
    "force_prepare": False,
    # --- description generation (the prep step) ---
    "gen_harness": "claude_code",  # claude_code (haiku, on Max) | opencode (off Max)
    "gen_model": "haiku",          # orchestrator model; executors are haiku via agent frontmatter
    "gen_kinds": ["function", "method", "struct", "interface"],  # scope (drop `file` etc.)
    "gen_max_turns": 300,          # generation loops over waves; needs headroom
    "gen_timeout_s": 3600,         # per-worktree wall-clock cap for generation
    "gen_parallel": 1,             # worktrees warmed concurrently (rate-limits, keep low)
    "gen_mcp_override": True,      # claude_code only: serve --tool-profile all during gen
    "regenerate": False,           # re-generate even if a fixture is already covered
    "out": None,
    "keep_workdir": False,
    "dry_run": False,
    "no_grade": False,
    "resume": False,
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
    sp.add_argument("--coverage-min", type=float, dest="coverage_min", default=None,
                    help="skip fixtures already at/above this description coverage")
    sp.add_argument("--regenerate", action="store_true", dest="regenerate",
                    help="generate even for fixtures already at/above coverage_min")
    add_config(sp)

    # run ----------------------------------------------------------------- #
    sp = sub.add_parser("run", help="execute the A/B matrix over the manifest + warm fixtures")
    sp.add_argument("--sample-id", dest="sample_id", default=None)
    sp.add_argument("--seeds", type=int, default=None, help="repetitions per task per arm")
    sp.add_argument("--arms", default=None, help="comma list: baseline,aracne")
    sp.add_argument("--run-harness", dest="run_harness", default=None,
                    help="claude_code | opencode (same for both arms)")
    sp.add_argument("--model", default=None,
                    help="model for BOTH arms: claude alias (haiku/sonnet) or opencode provider/model")
    sp.add_argument("--max-turns", type=int, dest="max_turns", default=None)
    sp.add_argument("--timeout-s", type=int, dest="timeout_s", default=None)
    sp.add_argument("--out", default=None, help="results directory")
    sp.add_argument("--resume", action="store_true", help="skip combos already in runs.jsonl")
    sp.add_argument("--no-grade", action="store_true", dest="no_grade",
                    help="run agents + capture metrics but skip Docker grading")
    sp.add_argument("--dry-run", action="store_true", dest="dry_run",
                    help="print the run matrix and exit")
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
    config_path = getattr(args, "config", None)
    if config_path and Path(config_path).exists():
        cfg = _deep_merge(cfg, _load_yaml(Path(config_path)))

    # Use identity checks: drop unset args (None) and unset store_true flags (False),
    # but KEEP zero-valued numerics — `0 == False` would otherwise swallow --seeds 0,
    # --coverage-min 0, --max-turns 0, etc. (`0 is False` is False, so identity is safe).
    overrides = {k: v for k, v in vars(args).items()
                 if k not in ("config", "command") and v is not None and v is not False}
    for listkey in ("languages", "arms", "gen_kinds"):
        if isinstance(overrides.get(listkey), str):
            overrides[listkey] = [s.strip() for s in overrides[listkey].split(",") if s.strip()]
    cfg = _deep_merge(cfg, overrides)
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

def cmd_sample(cfg: dict) -> int:
    oversample = max(1, int(cfg.get("oversample", 1)))
    n = cfg["samples"] * oversample
    print(f"Sampling {n}/lang (= {cfg['samples']} target x oversample {oversample}) "
          f"x {cfg['languages']} (seed={cfg['sample_seed']}) ...")
    tasks_by_lang = load_tasks(cfg["languages"], n, cfg["sample_seed"], cfg)
    path = sources.candidates_path(cfg["samples_dir"], cfg["sample_id"])
    sources.write_manifest(tasks_by_lang, path)
    total = sum(len(v) for v in tasks_by_lang.values())
    for lang, tasks in tasks_by_lang.items():
        print(f"  [{lang}] {len(tasks)} candidate(s)")
    print(f"\nWrote {total} candidate(s) to {path}")
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
    for t in candidates:
        if len(kept_by_lang[t.language]) >= target:
            continue  # language already filled — skip remaining candidates (saves clone+scan)
        key = fixtures.fixture_key(t)
        if key in seen:
            continue
        seen.add(key)
        try:
            meta = fixtures.scaffold(t, cfg, fixtures_root)
        except subprocess.CalledProcessError as e:
            print(f"  fail  {key:<46} (scaffold: {(e.stderr or str(e))[-160:].strip()})")
            continue
        n = meta.get("describable", 0)
        wt = fixtures.worktree_path(fixtures_root, t)
        if cap and n > cap:
            print(f"  drop  {key:<46} ({n} nodes > {cap})")
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
    print("\nNEXT: python bench/run_benchmark.py generate   (fills descriptions; then freeze, then run)")
    return 0


def cmd_freeze(cfg: dict) -> int:
    fixtures_root = Path(cfg["fixtures_dir"])
    tasks = _unique_tasks(sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"])))
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
              f"Run your /descriptions-generate and freeze again (or run with --allow-cold).")
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


def cmd_generate(cfg: dict) -> int:
    fixtures_root = Path(cfg["fixtures_dir"])
    tasks = sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    if cfg.get("languages"):
        tasks = [t for t in tasks if t.language in cfg["languages"]]
    tasks = _unique_tasks(tasks)

    harness = cfg["gen_harness"]
    if harness == "claude_code":
        extra_args, mcp_path = _gen_mcp_config(cfg, fixtures_root)
    else:
        extra_args, mcp_path = [], None  # opencode.json already serves --tool-profile all
    print(f"Generating descriptions in {len(tasks)} fixture(s): {harness} / {cfg['gen_model']} "
          f"(kinds={cfg['gen_kinds']}, parallel={cfg['gen_parallel']}).")
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
            print(f"[{i}/{len(tasks)}] {r['key']:<46} {r['status']:>11}  "
                  f"{r['before']:.0%}->{r['after']:.0%} ({r['described']}/{r['total']}, {r['dur_s']}s)")
            results.append(r)
    else:
        from concurrent.futures import ThreadPoolExecutor, as_completed
        with ThreadPoolExecutor(max_workers=par) as ex:
            futs = {ex.submit(warm, t): t for t in tasks}
            for done, fut in enumerate(as_completed(futs), 1):
                r = fut.result()
                print(f"[{done}/{len(tasks)}] {r['key']:<46} {r['status']:>11}  "
                      f"{r['before']:.0%}->{r['after']:.0%} ({r['described']}/{r['total']}, {r['dur_s']}s)")
                results.append(r)

    low = sum(1 for r in results if r["after"] < cfg["coverage_min"])
    print(f"\nDone. {len(results)-low}/{len(results)} fixture(s) at/above coverage_min="
          f"{cfg['coverage_min']:.0%}.")
    if low:
        print(f"WARNING: {low} still below — re-run `generate` (resumable) or pass --regenerate / raise --gen-timeout-s.")
    print("\nNEXT: python bench/run_benchmark.py freeze   # snapshot the warmed fixtures")
    return 0


def cmd_run(cfg: dict) -> int:
    # OpenCode expects provider/model (e.g. deepseek/deepseek-chat); haiku/sonnet are
    # Claude Code aliases and would fail at the opencode CLI.
    if cfg["run_harness"] == "opencode" and "/" not in cfg["model"]:
        raise SystemExit(
            f"opencode --run-harness needs a provider/model for --model "
            f"(e.g. deepseek/deepseek-chat); got {cfg['model']!r}.")
    fixtures_root = Path(cfg["fixtures_dir"])
    tasks = sources.read_manifest(sources.manifest_path(cfg["samples_dir"], cfg["sample_id"]))
    matrix = [(t, arm, seed)
              for t in tasks
              for arm in cfg["arms"]
              for seed in range(cfg["seeds"])]

    print(f"Matrix: {len(tasks)} tasks x {len(cfg['arms'])} arms x {cfg['seeds']} seeds = {len(matrix)} runs  "
          f"(harness={cfg['run_harness']}, model={cfg['model']}, max_turns={cfg['max_turns']})")

    if cfg["dry_run"]:
        for t, arm, seed in matrix:
            print(f"  {t.language:<11} {arm:<8} seed{seed}  {t.key}  @ {t.base_commit[:10]}")
        return 0

    out_dir = Path(cfg["out"] or (HERE / "results" / cfg["sample_id"]))
    out_dir.mkdir(parents=True, exist_ok=True)
    runs_path = out_dir / "runs.jsonl"
    repos_dir = fixtures_root / "_repos"   # shared with prepare's cache
    work_dir = out_dir / "work"
    work_dir.mkdir(parents=True, exist_ok=True)

    done, all_rows = (_read_done(runs_path) if cfg["resume"] else (set(), []))
    pending_grade: list[tuple] = []

    for i, (task, arm, seed) in enumerate(matrix, 1):
        run_key = f"{task.key}|{arm}|{seed}"
        if run_key in done:
            print(f"[{i}/{len(matrix)}] skip (resume) {run_key}")
            continue
        print(f"[{i}/{len(matrix)}] {task.language:<11} {arm:<8} seed{seed}  {task.key}")
        try:
            row, patch = runner.run_one(task, arm, seed, cfg, out_dir, repos_dir, work_dir, fixtures_root)
        except Exception as e:  # noqa: BLE001
            row = runner.error_row(task, arm, seed, f"runner crash: {e}")
            patch = ""
        _append(runs_path, row)
        all_rows.append(row)
        if patch and patch.strip():
            pending_grade.append((row, task, arm, patch))

    if not cfg["no_grade"] and pending_grade:
        print(f"\nGrading {len(pending_grade)} patched run(s) in Docker ...")
        grade.grade_all(pending_grade, cfg, out_dir)
        _rewrite(runs_path, all_rows)  # persist success
    elif cfg["no_grade"]:
        print("\nSkipping grading (--no-grade); success left unknown.")

    prep = metrics.prep_summary(tasks, fixtures_root)
    agg = metrics.aggregate(all_rows, cfg, prep=prep)
    report.write_results(agg, all_rows, out_dir)
    report.print_table(agg)
    print(f"\nSaved: {out_dir/'results.json'} , {out_dir/'summary.csv'} , {runs_path}")
    return 0


_COMMANDS = {
    "sample": cmd_sample,
    "prepare": cmd_prepare,
    "generate": cmd_generate,
    "freeze": cmd_freeze,
    "run": cmd_run,
}


def main(argv=None) -> int:
    args = parse_args(argv)
    cfg = build_config(args)
    return _COMMANDS[args.command](cfg)


if __name__ == "__main__":
    raise SystemExit(main())

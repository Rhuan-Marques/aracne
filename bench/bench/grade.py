"""Grade prediction patches with each benchmark's official Docker harness.

Grouped by (source, arm): write a predictions file in the harness's expected schema,
run the harness, parse which instances were resolved, and write `success` back onto the
result rows. Grading is best-effort and never fatal — if a harness is missing or errors, the affected
rows keep success=None ("unknown") and the run still reports tokens/turns.

Every harness call runs under two clocks (see `_run_harness`): an absolute cap
(`grade_timeout_s`) and a stall cap (`grade_stall_timeout_s` — no output at all for that
long). When either fires the harness is killed and its instances are recorded as
outcome="timeout" — neither correct nor fail, and excluded from the success rate — because
a clock running out says nothing about whether the patch was right.

Pinned to the INSTALLED harness versions:
  - SWE-bench (python): predictions JSONL {instance_id, model_name_or_path, model_patch};
    `python -m swebench.harness.run_evaluation`; report `<model>.<run_id>.json` with a
    `resolved_ids` list. (Not exercised in the current go/js/ts/rust matrix.)
  - Multi-SWE-bench (go/js/ts/rust), v1.1.x: the CLI takes INDIVIDUAL FLAGS (there is NO
    --config flag in this version):
        python -m multi_swe_bench.harness.run_evaluation \
          --mode evaluation --workdir W --output_dir O --repo_dir R \
          --dataset_files DS --patch_files PP --log_dir L
    * DS (dataset_files): the original Multi-SWE-bench records. We re-emit `task.raw`,
      which already carries base/fix_patch/test_patch/*_tests/*_result — verified to
      deserialize via Dataset.from_json for every task in our manifest.
    * PP (patch_files): JSONL of {org, repo, number(INT), fix_patch}; matched to
      instances by PullRequestBase.id == f"{org}/{repo}:pr-{number}".
    * Output: output_dir/final_report.json (FinalReport) with resolved_ids /
      unresolved_ids / empty_patch_ids / error_ids, each a list of "org/repo:pr-N" ids.
    NOTE: the harness does `docker.from_env()` at MODULE IMPORT and, when run as a module,
    pulls an `mswebench/nix_swe:v1.0` base container and builds a per-repo image before
    running tests — so a working Docker daemon (socket access) + network are required.
    The patch/dataset wiring below is unit-tested; the Docker run path needs one live
    validation pass once the daemon is reachable.
"""
from __future__ import annotations

import json
import os
import shutil
import signal
import subprocess
import sys
import time
from collections import defaultdict
from functools import lru_cache
from pathlib import Path

from . import outcome
from .sources import swe_dataset_name

# How often the supervisor checks a running harness for progress.
_POLL_S = 15


class GradeTimeout(RuntimeError):
    """A grading harness blew its wall-clock cap or went silent past the stall cap.

    Distinct from a harness FAILURE: the instances it covered are recorded as
    outcome=timeout (neither correct nor fail), never as unresolved.

    `partial` carries the verdicts the harness DID reach before the clock fired (task.key ->
    resolved bool). One wedged image build must not throw away the instances that already
    finished, so those keep their real verdict and only the unjudged ones become timeouts.
    """

    def __init__(self, message: str, partial: dict | None = None):
        super().__init__(message)
        self.partial = partial or {}


@lru_cache(maxsize=None)
def _harness_python(module: str) -> str:
    """Return a Python interpreter path that can `import <module>`.

    The grading harnesses (swebench / multi_swe_bench) are typically installed into
    bench/.venv, but the runner may be launched by any interpreter (e.g. the system
    python3), so a bare `sys.executable` can miss them. Probe likely interpreters in
    order and return the first that has the module, raising a clear, actionable error
    if none does."""
    candidates = []
    override = os.environ.get("ARACNE_BENCH_PYTHON")
    if override:
        candidates.append(override)
    candidates.append(sys.executable)
    venv = os.environ.get("VIRTUAL_ENV")
    if venv:
        candidates.append(str(Path(venv) / "bin" / "python"))
    # bench/.venv relative to this file: bench/bench/grade.py -> bench/.venv
    candidates.append(str(Path(__file__).resolve().parent.parent / ".venv" / "bin" / "python"))

    tried: list[str] = []
    for cand in candidates:
        if not cand or cand in tried:
            continue
        tried.append(cand)
        try:
            probe = subprocess.run([cand, "-c", f"import {module}"], capture_output=True)
        except OSError:
            continue
        if probe.returncode == 0:
            return cand

    raise RuntimeError(
        f"no Python interpreter with '{module}' installed (tried: {', '.join(tried)}). "
        f"Install the grading harness (pip install swebench multi-swe-bench) into the "
        f"interpreter you run the benchmark with, or set ARACNE_BENCH_PYTHON to one that has it."
    )


# Directories that hold checkouts/caches rather than harness output: walking them costs
# tens of thousands of stat() calls per poll and tells us nothing about liveness.
_SKIP_DIRS = {".git", "repos", "_repos", "node_modules", "target", "work", "patches"}


def _newest_mtime(dirs) -> float:
    """Most recent mtime under `dirs` — the harness's liveness signal.

    Multi-SWE-bench streams every build/test line into log files under log_dir and each
    image's build_image.log under workdir, so a frozen newest-mtime means the harness has
    genuinely stopped making progress (e.g. a wedged `pnpm install` postinstall inside a
    docker build), not merely that it is slow.
    """
    newest = 0.0
    for root_dir in dirs:
        root_dir = Path(root_dir)
        if not root_dir.exists():
            continue
        for root, dirnames, filenames in os.walk(root_dir):
            dirnames[:] = [d for d in dirnames if d not in _SKIP_DIRS]
            for name in filenames:
                try:
                    newest = max(newest, os.stat(os.path.join(root, name)).st_mtime)
                except OSError:
                    continue
    return newest


def _terminate(proc: subprocess.Popen) -> None:
    """SIGTERM the harness's whole process group, then SIGKILL what survives."""
    for sig, wait in ((signal.SIGTERM, 15), (signal.SIGKILL, 5)):
        if proc.poll() is not None:
            return
        try:
            os.killpg(os.getpgid(proc.pid), sig)
        except (ProcessLookupError, PermissionError):
            proc.kill()
        try:
            proc.wait(timeout=wait)
            return
        except subprocess.TimeoutExpired:
            continue


def _run_harness(cmd: list[str], cwd: Path, cfg: dict, watch, label: str,
                 env: dict | None = None) -> None:
    """Run a grading harness under two clocks, raising GradeTimeout when either fires.

    total  — `grade_timeout_s`: an absolute cap on the whole grading call.
    stall  — `grade_stall_timeout_s`: no new output anywhere under `watch` for that long.

    The stall clock is the one that matters in practice: a cold Docker cache can legitimately
    take an hour of steady work, while a hung build sits at zero CPU writing nothing. Either
    cap set to 0 disables that clock.
    """
    total_cap = float(cfg.get("grade_timeout_s") or 0)
    stall_cap = float(cfg.get("grade_stall_timeout_s") or 0)

    t0 = time.monotonic()
    last_change = t0
    last_seen = _newest_mtime(watch)
    # start_new_session so the whole harness process group can be signalled at once. It also
    # detaches the harness from the terminal, so Ctrl-C no longer reaches it on its own —
    # hence the KeyboardInterrupt handler below, which must kill it explicitly.
    proc = subprocess.Popen(cmd, cwd=str(cwd), start_new_session=True,
                            env={**os.environ, **(env or {})} if env else None)
    try:
        while True:
            try:
                proc.wait(timeout=_POLL_S)
                break
            except subprocess.TimeoutExpired:
                pass
            now = time.monotonic()
            seen = _newest_mtime(watch)
            if seen > last_seen:
                last_seen, last_change = seen, now
            if total_cap and now - t0 > total_cap:
                _terminate(proc)
                raise GradeTimeout(f"{label}: exceeded grade_timeout_s={int(total_cap)}s "
                                   f"(ran {int(now - t0)}s)")
            if stall_cap and now - last_change > stall_cap:
                _terminate(proc)
                raise GradeTimeout(f"{label}: no harness output for {int(now - last_change)}s "
                                   f"(grade_stall_timeout_s={int(stall_cap)}s); "
                                   f"the build/test step is wedged, not slow")
    except KeyboardInterrupt:
        print(f"\n[grade] interrupted — stopping {label} ...")
        _terminate(proc)
        raise
    if proc.returncode != 0:
        raise subprocess.CalledProcessError(proc.returncode, cmd)


def _warn_orphans() -> None:
    """Killing the harness client does NOT stop builds already running in the Docker daemon,
    so name any survivors instead of silently leaving them to burn CPU and confuse the next run."""
    try:
        res = subprocess.run(["docker", "ps", "--format", "{{.ID}}  {{.Image}}  {{.Status}}"],
                             capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        return
    lines = [ln for ln in (res.stdout or "").splitlines() if ln.strip()]
    if not lines:
        return
    print("[grade] NOTE: docker is still running these containers (the daemon keeps building "
          "after the client is killed):")
    for ln in lines:
        print(f"          {ln}")
    print("        stop them with: docker rm -f $(docker ps -q)")


def _seed_of(row) -> int:
    """The seed a result row came from. Missing/garbled -> 0, so a single-seed run behaves
    exactly as it did before seeds existed."""
    try:
        return int(row.get("seed") or 0)
    except (TypeError, ValueError):
        return 0


def _batches(source: str, items: list[tuple]) -> list[tuple[str, list]]:
    """Split one (source, arm) group into independently-graded harness calls.

    SPLIT BY SEED — this is a correctness requirement, not an optimization. BOTH harnesses key
    everything they produce by the INSTANCE: SWE-bench writes
    logs/run_evaluation/<run>/<model>/<instance_id>/report.json and a final report listing
    resolved instance_ids, and Multi-SWE-bench keys its reports by "org/repo:pr-N". Neither id
    carries a seed. Putting three seeds of one instance in one call therefore wrote three
    identical prediction rows, got ONE verdict back, and `_grade_batch` copied it onto all
    three — so the run reported 3 graded patches while exactly one had been run. The signature
    was visible in the data: across 54 instance x arm cells of a 3-seed run, every cell was 0/3
    or 3/3 and never 1/3 or 2/3, while the patches themselves genuinely differed (two flask
    seeds matched the passing baseline, the third used a different parameter name and really
    did fail — all three were recorded as failures). One seed per call gives each patch its own
    harness id-space, its own report, and its own verdict.

    SPLIT BY REPO (multi_swe_bench only) — blast radius. Multi-SWE-bench builds EVERY image
    before it runs ANY instance, so a single wedged build (a hung postinstall, an unreachable
    registry) takes the whole call down with it and no instance is ever judged. Splitting per
    repo means a bad repo costs only its own instances.

    Each batch gets its own dirs, but they share the Docker image cache and the repo clones, so
    splitting costs little beyond a few extra harness startups.
    """
    # Only disambiguate when there is something to disambiguate: a single-seed run keeps the
    # exact tags (and therefore the result-directory layout) it had before.
    multi_seed = len({_seed_of(item[0]) for item in items}) > 1

    grouped: dict[tuple, list] = defaultdict(list)
    for item in items:
        row, task = item[0], item[1]
        repo = ""
        if source == "multi_swe_bench":
            repo = f"{task.raw.get('org')}__{task.raw.get('repo')}"
        grouped[(repo, _seed_of(row))].append(item)

    batches = []
    for (repo, seed), group in sorted(grouped.items()):
        # The tag names the batch's dirs, files and harness run id, so it must be unique per
        # batch and stable across a regrade.
        parts = [p for p in (repo, f"s{seed}" if multi_seed else "") if p]
        batches.append(("__".join(parts), group))
    return batches


def grade_all(pending: list[tuple], cfg: dict, out_dir: Path) -> None:
    """pending: list of (row, task, arm, patch_text).

    Sets row['success'] (True/False/None) and row['outcome'] in place; a harness timeout
    marks its rows outcome="timeout" via row['timeout_stage'] instead of failing them.

    Graded in independent batches (see `_batches`) so one wedged repo cannot strand the rest."""
    groups: dict[tuple, list] = defaultdict(list)
    for row, task, arm, patch in pending:
        groups[(task.source, arm)].append((row, task, arm, patch))

    for (source, arm), group_items in groups.items():
        batches = _batches(source, group_items)
        if len(batches) > 1:
            print(f"[grade] {source}/{arm}: {len(group_items)} run(s) in {len(batches)} "
                  f"independent batches ({', '.join(t for t, _ in batches)}); "
                  f"a batch that wedges does not block the others.")
        for tag, items in batches:
            _grade_batch(source, arm, tag, items, cfg, out_dir)


def _grade_batch(source: str, arm: str, tag: str, items: list[tuple],
                 cfg: dict, out_dir: Path) -> None:
    """Grade one batch, writing verdicts onto its rows. Never raises: a timeout or a broken
    harness is recorded on the rows and the next batch still runs."""
    label = f"{source}/{arm}" + (f"/{tag}" if tag else "")
    try:
        if source.startswith("swe_atlas"):
            resolved = _grade_atlas(items, arm, cfg, out_dir, tag)
        elif source == "swe_bench":
            resolved = _grade_swe(items, arm, cfg, out_dir, tag)
        elif source == "swe_bench_live":
            resolved = _grade_live(items, arm, cfg, out_dir, tag)
        else:
            resolved = _grade_multi(items, arm, cfg, out_dir, tag)
    except GradeTimeout as e:
        # The clock ran out. Instances the harness DID finish keep their real verdict;
        # only the ones it never judged become timeouts — one wedged build must not
        # discard the rest of the batch. Either way the run keeps going.
        salvaged = e.partial or {}
        for row, task, _arm, _patch in items:
            if row.get("success") is None and task.key in salvaged:
                row["success"] = bool(salvaged[task.key])
        timed_out = [r for r, _t, _a, _p in items if r.get("success") is None]
        print(f"[grade] {label} TIMED OUT: {e}")
        if salvaged:
            print(f"        kept {len(items) - len(timed_out)} verdict(s) the harness "
                  f"had already written before the clock fired.")
        print(f"        {len(timed_out)} run(s) recorded as outcome=timeout "
              f"(excluded from the success rate, not counted as failures).")
        _warn_orphans()
        for row, _task, _arm, _patch in items:
            if row.get("success") is None:
                row["timeout_stage"] = outcome.GRADING
                # NOT row["error"]: the agent itself ran fine, so its tokens/turns stay
                # in the metric means; only the verdict is missing.
                row["grade_error"] = str(e)
            outcome.stamp(row)
        return
    except Exception as e:  # noqa: BLE001
        print(f"[grade] {label} grading failed: {e}; leaving success=unknown")
        for row, _task, _arm, _patch in items:
            row["grade_error"] = str(e)
            outcome.stamp(row)
        return
    for row, task, _arm, _patch in items:
        if task.key in resolved:
            row["success"] = bool(resolved[task.key])
        outcome.stamp(row)


def _grade_atlas(items: list[tuple], arm: str, cfg: dict, out_dir: Path,
                 tag: str) -> dict[str, bool]:
    """Grade SWE-Atlas refactoring tasks, one run at a time.

    THREE MODES (`atlas_grading`, and `--cheap-grade` on top of any of them):

      - "rubric" (default) -- the navigation variant. LOCALIZATION (deterministic, see
        bench/localization.py) plus the task's own LLM rubric judge run on the host with no
        hidden tests (atlas_rubric.py). success = every must-have rubric item passes. The hidden
        tests are dropped because the variant withholds the interface specification they
        depend on; see bench/atlas_prompt.py.
      - "verifier" -- the benchmark as published: the task's own container runs `test.sh`
        (tests AND rubric) and success = its reward. Localization is still recorded.
      - cheap_grade -- localization only. No model, no container, no tokens; success stays
        unknown, so the solve rate is simply not reported for the run.

    Localization is computed for every row in every mode: it is free, and it is the number that
    no naming choice can move.
    """
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
    from . import atlas_tests, fixtures, localization

    mode = "localization" if cfg.get("cheap_grade") else cfg.get("atlas_grading", "rubric")
    outdir = out_dir / "atlas" / (f"{arm}__{tag}" if tag else arm)
    outdir.mkdir(parents=True, exist_ok=True)
    fixtures_root = Path(cfg.get("fixtures_dir") or "")
    resolved: dict[str, bool] = {}

    httpd = judge_url = None
    if mode == "verifier":
        import atlas_grade
        import atlas_judge
        httpd = atlas_judge.serve(0, "0.0.0.0")
        judge_url = f"http://host.docker.internal:{httpd.server_address[1]}/v1"
    elif mode == "rubric":
        import atlas_rubric
    try:
        for row, task, _arm, patch in items:
            rec = dict(task.raw)
            rec["key"] = task.key
            snap = fixtures.snapshot_path(fixtures_root, task) / ".aracne" / "topology.db"
            tpath = Path(row["transcript_path"]) if row.get("transcript_path") else None
            tests_in_scope = atlas_tests.in_scope(task.key, cfg)
            loc = localization.score_row(rec, patch, snap, tpath, include_tests=tests_in_scope)
            row.update(loc["row"])
            row["grading"] = mode
            row["tests_in_scope"] = tests_in_scope
            result: dict = {"key": task.key, "arm": arm, "grading": mode,
                            "localization": loc["detail"]}
            print(f"[grade] atlas/{arm}: {task.key}  files R={loc['row']['loc_file_recall']} "
                  f"P={loc['row']['loc_file_precision']}  decls R={loc['row']['loc_decl_recall']} "
                  f"P={loc['row']['loc_decl_precision']}")

            if mode == "rubric":
                res = atlas_rubric.grade(rec["task_dir"], patch,
                                         naming_lenient=cfg.get("atlas_naming_lenient", True),
                                         tests_in_scope=tests_in_scope,
                                         drop_rubrics=atlas_tests.dropped_rubrics(task.key)
                                         if tests_in_scope else (),
                                         log=lambda m: print(f"        {m}"))
                result["rubric"] = res
                row["rubric_agg_score"] = res.get("agg_score")
                row["rubric_must_have_pass"] = res.get("must_have_pass")
                if res.get("must_have_pass") is not None:
                    resolved[task.key] = bool(res["must_have_pass"])
                else:
                    row["grade_error"] = res.get("error") or "no rubric verdict"
                print(f"        rubric: must_have={res.get('must_have_pass')} "
                      f"agg={res.get('agg_score')} ({res.get('n_judge_calls')} judge calls)"
                      + (f" error={res['error']}" if res.get("error") else ""))
            elif mode == "verifier":
                timeout_s = int(rec.get("timeout_sec") or 7200)
                res = atlas_grade.grade(rec, patch, judge_url, timeout_s=timeout_s,
                                        log=lambda m: print(f"        {m}"))
                result["verifier"] = res
                # A reward the verifier never produced is unknown, not a failure -- the same
                # rule the other sources follow when a harness leaves an instance unjudged.
                if res.get("reward") is not None:
                    resolved[task.key] = float(res["reward"]) >= 1.0
                else:
                    row["grade_error"] = res.get("error") or "no reward"
                print(f"        {task.key}: reward={res.get('reward')} "
                      f"tests={res.get('tests_reward')} must_have={res.get('must_have_pass')}"
                      + (f" error={res['error']}" if res.get("error") else ""))
            (outdir / f"{task.key}.json").write_text(json.dumps(result, indent=2))
    finally:
        if httpd is not None:
            httpd.shutdown()
    return resolved


def _grade_swe(items: list[tuple], arm: str, cfg: dict, out_dir: Path,
               tag: str = "") -> dict[str, bool]:
    """Grade one SWE-bench batch. `tag` (from `_batches`) separates one seed's harness
    id-space from another's: the run id, the model name, the predictions file and therefore
    logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json are all per batch. Without
    it three seeds of an instance collapsed onto one instance_id and shared one verdict."""
    src = cfg["sources"]["swe_bench"]
    suffix = f"-{tag}" if tag else ""
    model = f"{src['model_name']}-{arm}{suffix}"
    run_id = f"aracne-{arm}{suffix}"
    preds = out_dir / f"preds_swe_{arm}{suffix}.jsonl"
    with preds.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            f.write(json.dumps({
                "instance_id": task.key,
                "model_name_or_path": model,
                "model_patch": patch,
            }) + "\n")

    try:
        _run_harness(
            [_harness_python("swebench"), "-m", "swebench.harness.run_evaluation",
             "--dataset_name", swe_dataset_name(src),
             "--predictions_path", str(preds),
             "--run_id", run_id,
             "--max_workers", str(src.get("max_workers", 4))],
            out_dir, cfg, watch=[out_dir], label=f"swe_bench/{arm}" + (f"/{tag}" if tag else ""),
        )
    except GradeTimeout as e:
        raise GradeTimeout(str(e), _parse_swe_logs(out_dir, run_id, model, items)) from None
    except subprocess.CalledProcessError:
        partial = _parse_swe_logs(out_dir, run_id, model, items)
        if not partial:
            raise
        print(f"[grade] swe_bench/{arm}{'/' + tag if tag else ''} harness aborted; "
              f"keeping {len(partial)} instance verdict(s) it had already written.")
        return partial

    report = out_dir / f"{model}.{run_id}.json"
    data = json.loads(report.read_text(encoding="utf-8"))
    resolved_ids = set(data.get("resolved_ids", []))
    return {task.key: (task.key in resolved_ids) for row, task, _arm, patch in items}


# Where the SWE-bench-Live harness lives. It is NOT pip-installed on purpose: its pyproject
# declares `name = "swebench"`, so installing it would shadow the real swebench package the
# SWE-bench path depends on (it in fact depends on that package itself). Running it from its
# source tree keeps both usable at once.
LIVE_HARNESS_DIR = Path(
    os.environ.get("ARACNE_SWE_LIVE_DIR")
    or Path.home() / ".cache" / "aracne-bench" / "tools" / "SWE-bench-Live"
)


def _grade_live(items: list[tuple], arm: str, cfg: dict, out_dir: Path,
                tag: str = "") -> dict[str, bool]:
    """Grade one SWE-bench-Live batch (`SWE-bench-Live/*`, including MultiLang).

    Its harness differs from SWE-bench's in three ways that matter here:

      dataset      it accepts a LOCAL .jsonl as --dataset, so the batch's own rows are handed
                   over directly. That is what lets a hand-picked, cross-language sample be
                   graded in one call -- via Hugging Face it would need one call per language
                   split, because MultiLang stores each language as its own split.
      predictions  --patch_dir is a single JSON object {instance_id: {"model_patch": diff}},
                   not SWE-bench's JSONL of prediction records.
      verdicts     no aggregate report file is guaranteed; the per-instance truth is
                   <output_dir>/<instance_id>/report.json with a "resolved" boolean.

    Each instance builds its own Docker image (1-3.5 GB compressed), so the stall clock in
    _run_harness is doing real work here.
    """
    src = (cfg.get("sources") or {}).get("swe_bench_live") or {}
    harness = Path(src.get("harness_dir") or LIVE_HARNESS_DIR)
    if not (harness / "evaluation" / "evaluation.py").is_file():
        raise SystemExit(
            f"SWE-bench-Live harness not found at {harness}.\n"
            f"  git clone https://github.com/microsoft/SWE-bench-Live {harness}\n"
            f"  git -C {harness} submodule update --init --depth 1   # RepoLaunch\n"
            f"Do NOT `pip install` it: its pyproject is named 'swebench' and would shadow the "
            f"real package. Set ARACNE_SWE_LIVE_DIR to override the location."
        )

    suffix = f"-{tag}" if tag else ""
    dataset = out_dir / f"live_dataset_{arm}{suffix}.jsonl"
    preds = out_dir / f"live_preds_{arm}{suffix}.json"
    logs = out_dir / f"live_logs_{arm}{suffix}"
    logs.mkdir(parents=True, exist_ok=True)

    # The harness reads every eval column off the row (docker_image, rebuild_cmds, test_cmds,
    # log_parser, FAIL_TO_PASS, PASS_TO_PASS), which is exactly what task.raw preserved.
    with dataset.open("w", encoding="utf-8") as f:
        for _row, task, _arm, _patch in items:
            f.write(json.dumps(task.raw) + "\n")
    preds.write_text(json.dumps(
        {task.key: {"model_patch": patch} for _row, task, _arm, patch in items},
        indent=1), encoding="utf-8")

    label = f"swe_bench_live/{arm}" + (f"/{tag}" if tag else "")
    cmd = [_harness_python("swebench"), "-m", "evaluation.evaluation",
           "--dataset", str(dataset),
           "--patch_dir", str(preds),
           "--platform", src.get("platform", "linux"),
           "--output_dir", str(logs),
           "--workers", str(src.get("max_workers", 2)),
           "--overwrite", "0"]
    try:
        # cwd is the harness root because evaluation.py does
        # sys.path.insert(0, os.getcwd()/"launch") to reach its RepoLaunch submodule.
        _run_harness(cmd, harness, cfg, watch=[logs], label=label,
                     env={"PYTHONPATH": str(harness)})
    except GradeTimeout as e:
        raise GradeTimeout(str(e), _parse_live_logs(logs, items)) from None
    except subprocess.CalledProcessError:
        partial = _parse_live_logs(logs, items)
        if not partial:
            raise
        print(f"[grade] {label} harness aborted; keeping {len(partial)} verdict(s) "
              f"it had already written.")
        return partial

    return _parse_live_logs(logs, items)


def _parse_live_logs(logs: Path, items: list[tuple]) -> dict[str, bool]:
    """Per-instance verdicts from <logs>/<instance_id>/report.json.

    An instance with no report, or a report whose "resolved" is null, is left OUT of the
    result rather than recorded as a failure: the harness never judged it, and calling that a
    failure would quietly charge the arm for a build that did not run.
    """
    out: dict[str, bool] = {}
    for _row, task, _arm, _patch in items:
        report = logs / task.key / "report.json"
        if not report.is_file():
            continue
        try:
            resolved = json.loads(report.read_text(encoding="utf-8")).get("resolved")
        except (OSError, ValueError):
            continue
        if resolved is not None:
            out[task.key] = bool(resolved)
    return out


def _parse_swe_logs(out_dir: Path, run_id: str, model: str, items: list[tuple]) -> dict[str, bool]:
    """Per-instance verdicts from SWE-bench's run logs, for when the final report was never
    written (the harness was killed or aborted).

    SWE-bench writes logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json as each
    instance finishes: {"<instance_id>": {"resolved": bool, ...}}."""
    root = out_dir / "logs" / "run_evaluation" / run_id / model
    if not root.is_dir():
        return {}
    wanted = {task.key for _row, task, _arm, _patch in items}
    result: dict[str, bool] = {}
    for report in root.glob("*/report.json"):
        try:
            data = json.loads(report.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        if not isinstance(data, dict):
            continue
        for key, val in data.items():
            if key in wanted and isinstance(val, dict) and "resolved" in val:
                result[key] = bool(val["resolved"])
    return result


def _mswe_repo_root(cfg: dict) -> Path:
    """The ONE directory the Multi-SWE-bench harness clones repos into, for every run and arm.

    The harness needs `<repo_dir>/<org>/<repo>` to be a clean checkout containing each
    instance's base sha; if it is missing it warns "Repository not found: ..." and clones it
    from GitHub itself. Pointing that at a per-run, per-arm directory made every run re-download
    hundreds of megabytes twice, and an interrupted clone left nothing behind to resume from —
    so the same repo was cloned again on the next attempt. Keep it next to the fixtures instead,
    beside the `_repos` cache prepare/generate already maintain."""
    fixtures = cfg.get("fixtures_dir")
    root = Path(fixtures) if fixtures else Path(__file__).resolve().parent.parent / "fixtures"
    return root / "_mswe_repos"


def _has_commit(repo: Path, sha: str) -> bool:
    """Is the commit OBJECT in this repo's database (reachable or not)?"""
    return subprocess.run(["git", "-C", str(repo), "cat-file", "-e", f"{sha}^{{commit}}"],
                          capture_output=True).returncode == 0


def _pin_commit(repo: Path, sha: str) -> None:
    """Give `sha` a ref so the harness can SEE it.

    The harness validates base commits with GitPython's `iter_commits("--all")` — i.e.
    `git rev-list --all`, which walks refs, not the object database. Two of our own paths
    produce a commit that exists but is reachable from nothing:

      - a local clone of the `_repos` cache copies/hardlinks every object, but only clones
        the source's refs/heads. A base commit that lives on some other branch (the cache
        knows it as refs/remotes/origin/<branch>) arrives orphaned.
      - `git fetch origin <sha>` writes the object and no ref at all.

    Either way the harness reports "Commit hash not found" and aborts the whole batch. A ref
    under refs/ fixes it — `--all` covers refs/ generally — and costs one file."""
    subprocess.run(["git", "-C", str(repo), "update-ref", f"refs/mswe-base/{sha}", sha],
                   capture_output=True, text=True)


# A network `git clone`/`fetch` fails for reasons that have nothing to do with the patch under
# test (a dropped WSL bridge, a DNS hiccup, GitHub rate-limiting). Retry before giving up:
# losing a repo's verdicts to a three-second blip is the expensive outcome, not the wait.
_NET_RETRIES = 3
_NET_BACKOFF_S = 5


def _git_net(args: list[str], label: str, check: bool) -> bool:
    """Run a network git command with retries. Returns True on success.

    Raises CalledProcessError only when `check` and every attempt failed."""
    last: subprocess.CalledProcessError | None = None
    for attempt in range(1, _NET_RETRIES + 1):
        res = subprocess.run(["git", *args], capture_output=True, text=True)
        if res.returncode == 0:
            return True
        last = subprocess.CalledProcessError(res.returncode, res.args, res.stdout, res.stderr)
        if attempt < _NET_RETRIES:
            tail = (res.stderr or "").strip().splitlines()
            print(f"[grade] {label} failed (attempt {attempt}/{_NET_RETRIES}): "
                  f"{tail[-1] if tail else res.returncode}; retrying in {_NET_BACKOFF_S}s ...",
                  flush=True)
            time.sleep(_NET_BACKOFF_S)
    if check and last is not None:
        raise last
    return False


def _seed_repo(org: str, repo: str, shas: set[str], dest: Path, cache_root: Path) -> None:
    """Make `dest` a clean clone of org/repo in which every sha in `shas` is REACHABLE.

    Reachable, not merely present: see `_pin_commit`. Prefers the local `_repos` cache (a plain local clone: no network, and it already holds the
    base commits, since that is the same clone the agent runs were cut from) and falls back to
    GitHub. `origin` always ends up pointing at GitHub so a later fetch can find missing shas."""
    url = f"https://github.com/{org}/{repo}.git"
    if not (dest / ".git").exists():
        if dest.exists():
            shutil.rmtree(dest)            # a clone killed mid-flight leaves a partial tree
        dest.parent.mkdir(parents=True, exist_ok=True)
        cache = cache_root / f"{org}__{repo}"
        local = (cache / ".git").exists()
        print(f"[grade] seeding harness repo {org}/{repo} from "
              f"{'the local _repos cache' if local else 'github'} ...", flush=True)
        if local:
            subprocess.run(["git", "clone", "--quiet", str(cache), str(dest)],
                           check=True, capture_output=True, text=True)
        else:
            _git_net(["clone", "--quiet", url, str(dest)], f"clone {org}/{repo}", check=True)
        if local:
            subprocess.run(["git", "-C", str(dest), "remote", "set-url", "origin", url],
                           check=True, capture_output=True, text=True)

    missing = sorted(s for s in shas if not _has_commit(dest, s))
    for sha in shas - set(missing):
        _pin_commit(dest, sha)          # present, but possibly reachable from nothing
    if not missing:
        return
    # The cache can predate the instance's base commit (or the clone came from a stale mirror).
    print(f"[grade] fetching {len(missing)} missing base commit(s) for {org}/{repo} ...", flush=True)
    _git_net(["-C", str(dest), "fetch", "--quiet", "--tags", "origin"],
             f"fetch {org}/{repo}", check=False)
    for sha in missing:
        if _has_commit(dest, sha):
            _pin_commit(dest, sha)
            continue
        # Loose shas are only fetchable when the server allows it; harmless when it does not.
        _git_net(["-C", str(dest), "fetch", "--quiet", "origin", sha],
                 f"fetch {org}/{repo}@{sha[:10]}", check=False)
        if _has_commit(dest, sha):
            _pin_commit(dest, sha)
        else:
            print(f"[grade] WARNING: {org}/{repo} is missing base commit {sha}; the harness "
                  f"will report that instance as an error.")


def _ensure_repos(items: list[tuple], repodir: Path, cache_root: Path) -> list[str]:
    """Pre-seed every repo this batch needs, BEFORE the timed harness call.

    Done here rather than left to the harness for three reasons: the clone is not on the stall
    clock (`_run_harness` deliberately does not watch `repo_dir`, so a slow cold clone used to
    look like a wedged build); it reuses the clone the rest of the bench already has, so a
    healthy cache needs no network at all; and a network failure here is retried, where the
    harness's own single-shot clone crashes the whole batch on the first blip.

    Returns the "org/repo" names it could not seed (empty when all are ready)."""
    wanted: dict[tuple[str, str], set[str]] = defaultdict(set)
    for _row, task, _arm, _patch in items:
        raw = task.raw
        org, repo = raw.get("org"), raw.get("repo")
        sha = ((raw.get("base") or {}) if isinstance(raw.get("base"), dict) else {}).get("sha")
        if not org or not repo:
            continue
        entry = wanted[(org, repo)]
        if sha:
            entry.add(sha)
    failed: list[str] = []
    for (org, repo), shas in sorted(wanted.items()):
        try:
            _seed_repo(org, repo, shas, repodir / org / repo, cache_root)
        except (OSError, subprocess.SubprocessError) as e:
            print(f"[grade] could not seed {org}/{repo}: {e}")
        if not (repodir / org / repo / ".git").exists():
            failed.append(f"{org}/{repo}")
    return failed


def _mswe_id(task) -> str:
    """The PullRequestBase.id the harness uses to key reports: 'org/repo:pr-number'."""
    raw = task.raw
    return f"{raw.get('org')}/{raw.get('repo')}:pr-{raw.get('number')}"


def _grade_multi(items: list[tuple], arm: str, cfg: dict, out_dir: Path,
                 tag: str = "") -> dict[str, bool]:
    src = (cfg.get("sources") or {}).get("multi_swe_bench", {}) or {}
    base = out_dir / f"mswe_{arm}"
    # workdir (built images) is SHARED across batches, and the clones are shared across runs
    # AND arms, so splitting the group per repo re-uses the caches instead of rebuilding them. Logs and reports are
    # per-batch, so a killed batch can never leave a stale final_report.json behind for
    # the next one to misread.
    workdir = base / "workdir"
    # Repos are shared with every other run and arm (see `_mswe_repo_root`), not per-batch.
    repodir = _mswe_repo_root(cfg)
    logdir = base / "logs" / tag if tag else base / "logs"
    outdir = base / "out" / tag if tag else base / "out"
    for d in (workdir, repodir, logdir, outdir):
        d.mkdir(parents=True, exist_ok=True)

    # Clone what the harness needs up front, off the stall clock.
    fixtures = cfg.get("fixtures_dir")
    cache_root = (Path(fixtures) if fixtures
                  else Path(__file__).resolve().parent.parent / "fixtures") / "_repos"
    unseeded = _ensure_repos(items, repodir, cache_root)
    if unseeded:
        # The harness would only re-attempt the same clone, un-retried, and die on it — with a
        # traceback that buries the cause. Stop here so the batch is recorded as ungraded with
        # a message that says what to do: the rows stay success=unknown and `regrade` picks
        # them up once the repo is reachable again.
        raise RuntimeError(
            f"could not obtain the source repo(s) {', '.join(unseeded)} "
            f"(no entry under {cache_root} and github is unreachable). "
            f"Left ungraded; re-run `python bench/run_benchmark.py regrade {out_dir.name}` "
            f"once the network is back."
        )

    # dataset_files: re-emit the original Multi-SWE-bench records (task.raw).
    dataset = base / (f"dataset_{tag}.jsonl" if tag else "dataset.jsonl")
    with dataset.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            f.write(json.dumps(task.raw) + "\n")

    # patch_files: predictions. `number` MUST be an int (harness validates the type).
    preds = base / (f"preds_{tag}.jsonl" if tag else "preds.jsonl")
    with preds.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            raw = task.raw
            f.write(json.dumps({
                "org": raw.get("org"),
                "repo": raw.get("repo"),
                "number": int(raw.get("number")),
                "fix_patch": patch or "",
            }) + "\n")

    workers = str(src.get("max_workers", 4))
    cmd = [
        _harness_python("multi_swe_bench"), "-m", "multi_swe_bench.harness.run_evaluation",
        "--mode", "evaluation",
        "--workdir", str(workdir),
        "--output_dir", str(outdir),
        "--repo_dir", str(repodir),
        "--dataset_files", str(dataset),
        "--patch_files", str(preds),
        "--log_dir", str(logdir),
        "--max_workers", workers,
        "--max_workers_build_image", workers,
        "--max_workers_run_instance", workers,
    ]
    # Watch the dirs the harness writes into — but NOT repodir, whose git clones hold tens of
    # thousands of files and would make every poll expensive without adding a liveness signal.
    try:
        _run_harness(cmd, out_dir, cfg, watch=[logdir, workdir, outdir],
                     label=f"multi_swe_bench/{arm}" + (f"/{tag}" if tag else ""))
    except GradeTimeout as e:
        # The harness writes a report.json per instance as it finishes, so instances that
        # completed before the clock fired still have a real verdict. Carry them out with
        # the exception instead of condemning the whole batch to outcome=timeout.
        raise GradeTimeout(str(e), _parse_multi_reports(outdir, items)) from None
    except subprocess.CalledProcessError:
        # The harness aborted (a wedged build, a crashed worker). Whatever it managed to
        # judge is still valid; the rest stay unknown.
        partial = _parse_multi_reports(outdir, items)
        if not partial:
            raise
        print(f"[grade] multi_swe_bench/{arm}{'/' + tag if tag else ''} harness aborted; "
              f"keeping {len(partial)} "
              f"instance verdict(s) it had already written.")
        return partial
    return _parse_multi_reports(outdir, items)


def _parse_multi_reports(outdir: Path, items: list[tuple]) -> dict[str, bool]:
    """Map the harness's FinalReport id-lists back onto our task keys.

    resolved_ids -> True; unresolved_ids/empty_patch_ids -> False; error_ids and ids the
    harness never reported -> omitted (left as success=unknown, so a broken build is not
    counted as an agent failure)."""
    index = {_mswe_id(task): task.key for row, task, _arm, patch in items}
    result: dict[str, bool] = {}

    final = outdir / "final_report.json"
    if final.exists():
        try:
            data = json.loads(final.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            data = None
        if isinstance(data, dict):
            resolved = set(data.get("resolved_ids") or [])
            failed = set(data.get("unresolved_ids") or []) | set(data.get("empty_patch_ids") or [])
            for mid, key in index.items():
                if mid in resolved:
                    result[key] = True
                elif mid in failed:
                    result[key] = False
            if result:
                return result

    # Fallback: per-instance report.json files (Report.valid == resolved).
    for report in sorted(outdir.rglob("report.json")):
        try:
            data = json.loads(report.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        if not isinstance(data, dict):
            continue
        mid = f"{data.get('org')}/{data.get('repo')}:pr-{data.get('number')}"
        if mid in index and data.get("valid") is not None:
            result[index[mid]] = bool(data.get("valid"))
    return result

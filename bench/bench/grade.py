"""Grade prediction patches with each benchmark's official Docker harness.

Grouped by (source, arm): write a predictions file in the harness's expected schema,
run the harness, parse which instances were resolved, and write `success` back onto the
result rows. Grading is best-effort and never fatal — if a harness is missing or errors,
the affected rows keep success=None ("unknown") and the run still reports tokens/turns.

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
import subprocess
from collections import defaultdict
from pathlib import Path


def grade_all(pending: list[tuple], cfg: dict, out_dir: Path) -> None:
    """pending: list of (row, task, arm, patch_text). Sets row['success'] in place."""
    groups: dict[tuple, list] = defaultdict(list)
    for row, task, arm, patch in pending:
        groups[(task.source, arm)].append((row, task, arm, patch))

    for (source, arm), items in groups.items():
        try:
            if source == "swe_bench":
                resolved = _grade_swe(items, arm, cfg, out_dir)
            else:
                resolved = _grade_multi(items, arm, cfg, out_dir)
        except Exception as e:  # noqa: BLE001
            print(f"[grade] {source}/{arm} grading failed: {e}; leaving success=unknown")
            continue
        for row, task, arm, patch in items:
            if task.key in resolved:
                row["success"] = bool(resolved[task.key])


def _grade_swe(items: list[tuple], arm: str, cfg: dict, out_dir: Path) -> dict[str, bool]:
    src = cfg["sources"]["swe_bench"]
    model = f"{src['model_name']}-{arm}"
    run_id = f"aracne-{arm}"
    preds = out_dir / f"preds_swe_{arm}.jsonl"
    with preds.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            f.write(json.dumps({
                "instance_id": task.key,
                "model_name_or_path": model,
                "model_patch": patch,
            }) + "\n")

    subprocess.run(
        ["python", "-m", "swebench.harness.run_evaluation",
         "--dataset_name", src["dataset"],
         "--predictions_path", str(preds),
         "--run_id", run_id,
         "--max_workers", str(src.get("max_workers", 4))],
        cwd=str(out_dir), check=True,
    )

    report = out_dir / f"{model}.{run_id}.json"
    data = json.loads(report.read_text(encoding="utf-8"))
    resolved_ids = set(data.get("resolved_ids", []))
    return {task.key: (task.key in resolved_ids) for row, task, _arm, patch in items}


def _mswe_id(task) -> str:
    """The PullRequestBase.id the harness uses to key reports: 'org/repo:pr-number'."""
    raw = task.raw
    return f"{raw.get('org')}/{raw.get('repo')}:pr-{raw.get('number')}"


def _grade_multi(items: list[tuple], arm: str, cfg: dict, out_dir: Path) -> dict[str, bool]:
    src = (cfg.get("sources") or {}).get("multi_swe_bench", {}) or {}
    base = out_dir / f"mswe_{arm}"
    workdir = base / "workdir"
    repodir = base / "repos"
    logdir = base / "logs"
    outdir = base / "out"
    for d in (workdir, repodir, logdir, outdir):
        d.mkdir(parents=True, exist_ok=True)

    # dataset_files: re-emit the original Multi-SWE-bench records (task.raw).
    dataset = base / "dataset.jsonl"
    with dataset.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            f.write(json.dumps(task.raw) + "\n")

    # patch_files: predictions. `number` MUST be an int (harness validates the type).
    preds = base / "preds.jsonl"
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
        "python", "-m", "multi_swe_bench.harness.run_evaluation",
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
    subprocess.run(cmd, cwd=str(out_dir), check=True)
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

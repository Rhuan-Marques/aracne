"""Grade prediction patches with each benchmark's official Docker harness.

Grouped by (source, arm): write a predictions file in the harness's expected schema,
run the harness, parse which instances were resolved, and write `success` back onto the
result rows. Grading is best-effort and never fatal — if a harness is missing or errors,
the affected rows keep success=None ("unknown") and the run still reports tokens/turns.

Schemas pinned from the dataset/harness docs:
  - SWE-bench:      predictions JSONL {instance_id, model_name_or_path, model_patch};
                    `python -m swebench.harness.run_evaluation`; report `<model>.<run_id>.json`
                    with a `resolved_ids` list.
  - Multi-SWE-bench: predictions JSONL {org, repo, number, fix_patch};
                    `python -m multi_swe_bench.harness.run_evaluation --config <cfg.json>`.
                    The config schema + final-report layout vary by version, so that part
                    is isolated here and parsed defensively (confirm against the repo).
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


def _grade_multi(items: list[tuple], arm: str, cfg: dict, out_dir: Path) -> dict[str, bool]:
    src = cfg["sources"]["multi_swe_bench"]
    preds = out_dir / f"preds_multi_{arm}.jsonl"
    with preds.open("w", encoding="utf-8") as f:
        for row, task, _arm, patch in items:
            f.write(json.dumps({
                "org": task.raw.get("org"),
                "repo": task.raw.get("repo"),
                "number": str(task.raw.get("number")),
                "fix_patch": patch,
            }) + "\n")

    work = out_dir / f"mswe_eval_{arm}"
    work.mkdir(parents=True, exist_ok=True)
    config = dict(src.get("eval_config") or {})
    # Minimal config; confirm required keys (dataset paths, workdir, log dir, docker
    # settings, max_workers) against multi_swe_bench/harness README for your version.
    config.update({"patch_files": [str(preds)], "output_dir": str(work)})
    config_path = out_dir / f"mswe_config_{arm}.json"
    config_path.write_text(json.dumps(config, indent=2))

    subprocess.run(
        ["python", "-m", "multi_swe_bench.harness.run_evaluation", "--config", str(config_path)],
        cwd=str(out_dir), check=True,
    )
    return _parse_multi_reports(work, items)


def _parse_multi_reports(work: Path, items: list[tuple]) -> dict[str, bool]:
    """Defensively locate a final report and map org/repo/number -> resolved -> task.key."""
    by_id: dict[str, bool] = {}
    # Index our items by (org, repo, number) so we can match harness output back.
    index = {
        (str(task.raw.get("org")), str(task.raw.get("repo")), str(task.raw.get("number"))): task.key
        for row, task, _arm, patch in items
    }
    for report in sorted(work.rglob("*.json")):
        try:
            data = json.loads(report.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        for entry in _iter_resolved_entries(data):
            triple = (str(entry.get("org")), str(entry.get("repo")), str(entry.get("number")))
            if triple in index:
                by_id[index[triple]] = bool(entry.get("resolved"))
    return by_id


def _iter_resolved_entries(data):
    """Yield {org, repo, number, resolved} dicts from a few plausible report shapes."""
    if isinstance(data, list):
        yield from (e for e in data if isinstance(e, dict))
    elif isinstance(data, dict):
        for v in data.values():
            if isinstance(v, list):
                yield from (e for e in v if isinstance(e, dict))
            elif isinstance(v, dict) and {"org", "repo"} <= set(v):
                yield v

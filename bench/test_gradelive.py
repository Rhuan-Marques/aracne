"""The SWE-bench-Live grading adapter.

Its harness differs from SWE-bench's in every input and output format, and all three
differences are silent failures if got wrong: a JSONL where a JSON object is expected loads as
nothing, and a missing per-instance report is indistinguishable from a failed instance unless
the reader is careful. These pin the contract without running Docker.
"""
import json, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import grade


class T:
    def __init__(self, key, raw): self.key, self.raw = key, raw
    source = "swe_bench_live"


def check(label, cond):
    assert cond, label
    print("  PASS  " + label)


def test_live_verdicts_come_from_per_instance_reports(tmp: Path):
    logs = tmp / "logs"
    items = []
    for key, resolved in (("a__a-1", True), ("b__b-2", False), ("c__c-3", None)):
        items.append(({}, T(key, {}), "aracne", "diff"))
        if resolved is not None:
            d = logs / key
            d.mkdir(parents=True)
            (d / "report.json").write_text(json.dumps({"instance_id": key, "resolved": resolved}))
    # d__d-4 has a report whose verdict is null: judged by nothing, so not a verdict.
    items.append(({}, T("d__d-4", {}), "aracne", "diff"))
    (logs / "d__d-4").mkdir(parents=True)
    (logs / "d__d-4" / "report.json").write_text(json.dumps({"resolved": None}))

    got = grade._parse_live_logs(logs, items)
    check("resolved instance reads True", got.get("a__a-1") is True)
    check("unresolved instance reads False", got.get("b__b-2") is False)
    check("missing report is omitted, not a failure", "c__c-3" not in got)
    check('"resolved": null is omitted, not a failure', "d__d-4" not in got)
    check(f"exactly the two judged instances (got {len(got)})", len(got) == 2)


def test_live_harness_absence_is_actionable(tmp: Path):
    items = [({}, T("a__a-1", {}), "aracne", "diff")]
    cfg = {"sources": {"swe_bench_live": {"harness_dir": str(tmp / "nope")}}}
    try:
        grade._grade_live(items, "aracne", cfg, tmp)
    except SystemExit as e:
        msg = str(e)
        check("names the missing harness path", str(tmp / "nope") in msg)
        check("tells you to init the submodule", "submodule update --init" in msg)
        check("warns against pip installing it", "pip install" in msg)
        return
    raise AssertionError("expected SystemExit when the harness is absent")


def test_live_writes_the_formats_the_harness_expects(tmp: Path):
    """--dataset is JSONL of rows; --patch_dir is ONE JSON object keyed by instance id."""
    harness = tmp / "h" / "evaluation"
    harness.mkdir(parents=True)
    (harness / "evaluation.py").write_text("# stub")
    captured = {}

    def fake_run(cmd, cwd, cfg, watch, label, env=None):
        captured.update(cmd=cmd, cwd=cwd, env=env)

    orig = grade._run_harness
    grade._run_harness = fake_run
    try:
        items = [({}, T("a__a-1", {"instance_id": "a__a-1", "docker_image": "img/a"}), "aracne", "DIFF-A"),
                 ({}, T("b__b-2", {"instance_id": "b__b-2", "docker_image": "img/b"}), "aracne", "DIFF-B")]
        cfg = {"sources": {"swe_bench_live": {"harness_dir": str(tmp / "h")}}}
        out = tmp / "out"; out.mkdir()
        grade._grade_live(items, "aracne", cfg, out)
    finally:
        grade._run_harness = orig

    ds = out / "live_dataset_aracne.jsonl"
    rows = [json.loads(l) for l in ds.read_text().splitlines() if l.strip()]
    check(f"dataset is JSONL, one row per instance (got {len(rows)})", len(rows) == 2)
    check("dataset rows keep the eval columns", rows[0].get("docker_image") == "img/a")

    preds = json.loads((out / "live_preds_aracne.json").read_text())
    check("predictions are ONE object keyed by instance id", isinstance(preds, dict))
    check("each value nests the diff under model_patch",
          preds["a__a-1"] == {"model_patch": "DIFF-A"})

    check("runs from the harness root (evaluation.py does sys.path tricks on cwd)",
          str(captured["cwd"]) == str(tmp / "h"))
    check("PYTHONPATH points at the harness", captured["env"]["PYTHONPATH"] == str(tmp / "h"))
    check("invokes the live evaluation module",
          "evaluation.evaluation" in captured["cmd"] and "--patch_dir" in captured["cmd"])


if __name__ == "__main__":
    import tempfile
    with tempfile.TemporaryDirectory() as d:
        test_live_verdicts_come_from_per_instance_reports(Path(d) / "a")
        test_live_harness_absence_is_actionable(Path(d))
        test_live_writes_the_formats_the_harness_expects(Path(d) / "fmt")
    print("\nRESULT: ALL PASS")

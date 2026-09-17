"""Tests in scope for the hand-checked SWE-Atlas tasks (bench/bench/atlas_tests.py)."""
import json
from pathlib import Path

import pytest

from bench import agents, atlas_tests, localization
from bench.atlas_prompt import neutralize_test_claim

SAMPLE = Path(__file__).parent / "samples" / "atlas-warnreads5.jsonl"
ON = {"atlas_tests_in_scope": True}


def _sample():
    return [json.loads(l) for l in SAMPLE.read_text().splitlines() if l.strip()]


def test_only_the_five_sample_tasks_are_in_scope():
    assert set(atlas_tests.TASKS) == {t["key"] for t in _sample()}


def test_scope_needs_the_run_flag_and_a_listed_task():
    key = next(iter(atlas_tests.TASKS))
    assert atlas_tests.in_scope(key, ON)
    assert not atlas_tests.in_scope(key, {})
    assert not atlas_tests.in_scope("grafana_grafana-8265afb9", ON)


def test_in_scope_prompt_does_not_mention_tests():
    on = agents.task_prompt("P", "swe_atlas_rf", tests_in_scope=True)
    off = agents.task_prompt("P", "swe_atlas_rf")
    assert "test" not in on.lower()
    assert "Do not modify, add, or delete any test files" in off


def test_gold_includes_the_test_patch(tmp_path):
    (tmp_path / "solution").mkdir()
    (tmp_path / "tests").mkdir()
    (tmp_path / "solution" / "gold.patch").write_text(
        "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-x\n+y\n")
    (tmp_path / "tests" / "test_patch.diff").write_text(
        "diff --git a/a_test.go b/a_test.go\n--- a/a_test.go\n+++ b/a_test.go\n@@ -1,1 +1,1 @@\n-x\n+y")
    rec = {"task_dir": str(tmp_path)}
    agent = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-x\n+z\n"
    off = localization.score_row(rec, agent, None, None)["detail"]
    on = localization.score_row(rec, agent, None, None, include_tests=True)["detail"]
    assert off["gold_files"] == ["a.go"] and off["file_recall"] == 1.0
    assert on["gold_files"] == ["a.go", "a_test.go"] and on["file_recall"] == 0.5
    # Harness files never count, tests in scope or not.
    assert not localization.is_source("CLAUDE.md", include_tests=True)


@pytest.mark.parametrize("task", _sample(), ids=lambda t: t["key"])
def test_real_tasks_judge_prompt_loses_the_tests_claim(task):
    tests = Path(task["raw"]["task_dir"]) / "tests"
    if not tests.exists():
        pytest.skip("SWE-Atlas data not downloaded")
    text, _ = neutralize_test_claim((tests / "prompt.txt").read_text())
    assert "taken care of all changes to the test files" not in text
    assert "modify any test files" not in text.lower()
    ids = {r["id"] for r in json.loads((tests / "rubrics.json").read_text())}
    assert atlas_tests.dropped_rubrics(task["key"]) <= ids

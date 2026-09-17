#!/usr/bin/env python3
"""Tests for the SWE-Atlas navigation variant's grading: bench/localization.py, atlas_rubric.py,
and the --cheap-grade plumbing.

    .venv/bin/pytest bench/test_localization.py
"""
from __future__ import annotations

import json
import sqlite3
from pathlib import Path

import pytest

import atlas_rubric
import run_benchmark
from bench import localization as L
from bench.sources import Task

WT = "/cache/fixtures/org_repo-1/worktree"


def _db(tmp_path: Path, rows: list[tuple]) -> Path:
    p = tmp_path / "topology.db"
    con = sqlite3.connect(p)
    con.execute("create table resources (id text primary key, kind text, name text, language text, "
                "description text, properties_json text, starts_at int, ends_at int, loc_path text)")
    for rid, kind, path, start, end in rows:
        con.execute("insert into resources values (?,?,?,?,?,?,?,?,?)",
                    (rid, kind, rid, "go", "", "{}", start, end, f"{WT}/{path}"))
    con.commit()
    con.close()
    return p


def _hunk(path: str, old_start: int, removed: list[str], added: list[str]) -> str:
    body = "".join(f"-{x}\n" for x in removed) + "".join(f"+{x}\n" for x in added)
    return (f"diff --git a/{path} b/{path}\n--- a/{path}\n+++ b/{path}\n"
            f"@@ -{old_start},{len(removed)} +{old_start},{len(added)} @@\n{body}")


@pytest.fixture
def index(tmp_path):
    return L.BaseIndex(_db(tmp_path, [
        ("pkg.File", "file", "pkg/a.go", 1, 100),          # containers are never fix sites
        ("pkg.Engine", "struct", "pkg/a.go", 10, 30),
        ("pkg.(Engine).Run", "method", "pkg/a.go", 12, 20),  # nested: innermost wins
        ("pkg.Helper", "function", "pkg/a.go", 40, 50),
        ("pkg.Other", "function", "pkg/b.go", 5, 9),
    ]))


def test_parse_patch_tracks_base_lines_and_statuses():
    patch = (_hunk("pkg/a.go", 15, ["old"], ["new", "more"])
             + "diff --git a/x/y.go b/z/y.go\nsimilarity index 100%\nrename from x/y.go\nrename to z/y.go\n"
             + "diff --git a/n.go b/n.go\nnew file mode 100644\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1 @@\n+package n\n"
             + "diff --git a/d.go b/d.go\ndeleted file mode 100644\n--- a/d.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package d\n")
    f = L.parse_patch(patch)
    assert (f["pkg/a.go"].status, sorted(f["pkg/a.go"].old_lines)) == ("modified", [15])
    assert f["x/y.go"].status == "renamed"
    assert f["n.go"].status == "added"
    assert f["d.go"].status == "deleted"


def test_innermost_declaration_wins(index):
    assert index.decls_at("pkg/a.go", {15}) == {"pkg.(Engine).Run"}
    assert index.decls_at("pkg/a.go", {25}) == {"pkg.Engine"}
    assert index.decls_at("pkg/a.go", {99}) == set()  # only the file covers it


def test_perfect_partial_and_empty_patches(index):
    gold = _hunk("pkg/a.go", 15, ["x"], ["y"]) + _hunk("pkg/b.go", 6, ["x"], ["y"])
    same = L.score(gold, gold, index)
    assert (same["file_recall"], same["file_precision"], same["decl_recall"], same["decl_precision"]) == (1, 1, 1, 1)

    wrong = L.score(gold, _hunk("pkg/a.go", 45, ["x"], ["y"]), index)
    assert wrong["file_recall"] == 0.5 and wrong["file_precision"] == 1.0
    assert wrong["decl_recall"] == 0.0 and wrong["decl_precision"] == 0.0
    assert wrong["missed_decls"] == ["pkg.(Engine).Run", "pkg.Other"]

    empty = L.score(gold, "", index)
    assert empty["file_recall"] == 0.0 and empty["decl_recall"] == 0.0
    assert empty["file_precision"] is None  # nothing claimed, nothing to be precise about


def test_new_files_and_tests_are_not_scored(index):
    gold = _hunk("pkg/a.go", 15, ["x"], ["y"])
    agent = (gold + "diff --git a/pkg/new.go b/pkg/new.go\n--- /dev/null\n+++ b/pkg/new.go\n@@ -0,0 +1 @@\n+x\n"
             + _hunk("pkg/a_test.go", 3, ["x"], ["y"]) + _hunk(".aracne/config.json", 1, ["x"], ["y"]))
    s = L.score(gold, agent, index)
    assert s["file_precision"] == 1.0 and s["agent_new_files"] == ["pkg/new.go"]


def test_unindexed_language_has_no_declaration_score(tmp_path):
    empty_index = L.BaseIndex(_db(tmp_path, []))
    s = L.score(_hunk("src/x.c", 3, ["x"], ["y"]), _hunk("src/x.c", 3, ["x"], ["y"]), empty_index)
    assert s["file_recall"] == 1.0
    assert s["decl_recall"] is None and s["decl_unscorable_files"] == 1


@pytest.mark.parametrize("cmd,is_edit", [
    ("ls x 2>/dev/null", False), ("go build ./... 2>&1 | tail", False), ("sed -n 1,5p a.go", False),
    ("cat > api/v1/x.go <<'EOF'", True), ("sed -i 's/a/b/' a.go", True), ("git mv a.go b.go", True),
    ("python3 - <<'EOF'\nopen(p,'w').write(s)\nEOF", True),
])
def test_edit_detector(cmd, is_edit):
    assert bool(L._EDIT_CMD.search(cmd)) is is_edit


def test_navigation_counts_calls_and_tokens_before_first_gold_edit(tmp_path):
    def assistant(mid, cmd, tokens):
        return json.dumps({"type": "assistant", "message": {
            "id": mid, "usage": {"input_tokens": tokens},
            "content": [{"type": "tool_use", "input": {"command": cmd}}]}})
    t = tmp_path / "t.jsonl"
    t.write_text("\n".join([
        assistant("m1", "grep -rn Engine . 2>/dev/null", 100),
        assistant("m2", "cat pkg/a.go", 200),
        assistant("m3", "sed -i 's/x/y/' pkg/a.go", 300),
    ]))
    nav = L.navigation(t, ["pkg/a.go", "pkg/b.go"])
    assert nav["nav_turns_to_first_gold_edit"] == 3
    assert nav["nav_tokens_to_first_gold_edit"] == 600
    assert nav["nav_gold_files_named_before_edit"] == 0.5


# ---------------------------------------------------------------------------------------------
# atlas_rubric: the official evaluate_rubrics.py, with a stubbed judge.
# ---------------------------------------------------------------------------------------------

REAL_TASK = Path.home() / ".cache/aracne-bench/tools/SWE-Atlas/data/rf/task-696719205599a51110d4b451"
needs_task = pytest.mark.skipif(not (REAL_TASK / "tests" / "evaluate_rubrics.py").exists(),
                                reason="SWE-Atlas data not checked out")


@needs_task
def test_rubric_runs_the_official_scorer_with_the_naming_clarification(monkeypatch):
    seen = []

    def judge(model, messages, wants_json):
        seen.append(messages)
        return json.dumps({"ratings": [{"status": "YES", "justification": "stub"}]})
    monkeypatch.setattr(atlas_rubric.atlas_judge, "complete", judge)
    r = atlas_rubric.grade(REAL_TASK, _hunk("api/server.go", 1, ["x"], ["y"]))
    rubrics = json.loads((REAL_TASK / "tests" / "rubrics.json").read_text())
    checks = [x for x in rubrics if x["annotations"]["type"] != "high level intent"]
    assert r["n_judge_calls"] == len(seen) == len(checks)
    # All-YES passes every positive item and FAILS every negative one: the official inversion.
    negatives = sum("negative" in x["annotations"]["type"] for x in checks)
    assert r["must_have_pass"] is (negatives == 0)
    system = seen[0][0]["content"]
    assert "Identifier Names in This Evaluation" in system
    assert system.index("Identifier Names") < system.index("## Final Instructions")
    assert "Use the below" not in seen[0][1]["content"]


@needs_task
def test_rubric_strict_naming_leaves_the_official_prompt_alone(monkeypatch):
    seen = []
    monkeypatch.setattr(atlas_rubric.atlas_judge, "complete",
                        lambda m, msgs, wants_json: seen.append(msgs) or '{"ratings":[{"status":"YES","justification":"x"}]}')
    atlas_rubric.grade(REAL_TASK, _hunk("a.go", 1, ["x"], ["y"]), naming_lenient=False)
    assert seen[0][0]["content"] == (REAL_TASK / "tests" / "rubrics_system_prompt.txt").read_text()


@needs_task
def test_a_judge_that_never_answered_is_unknown_not_a_pass(monkeypatch):
    def broken(*_a, **_k):
        raise atlas_rubric.atlas_judge.JudgeError(502, "cli down")
    monkeypatch.setattr(atlas_rubric.atlas_judge, "complete", broken)
    r = atlas_rubric.grade(REAL_TASK, _hunk("a.go", 1, ["x"], ["y"]), log=lambda _m: None)
    assert r["must_have_pass"] is None and "judge calls failed" in r["error"]


def test_empty_patch_fails_without_calling_the_judge(monkeypatch):
    monkeypatch.setattr(atlas_rubric.atlas_judge, "complete", lambda *a: pytest.fail("judge called"))
    r = atlas_rubric.grade("/nonexistent", "  \n")
    assert r["must_have_pass"] is False


# ---------------------------------------------------------------------------------------------
# --cheap-grade plumbing and labels.
# ---------------------------------------------------------------------------------------------

def _task(key, source="swe_atlas_rf"):
    return Task(key=key, language="go", source=source, clone_url="u", base_commit="c",
                problem_statement="p", raw={})


def test_localization_collects_graded_and_unpatched_rows_but_not_broken_agents(tmp_path):
    patch = tmp_path / "p.patch"
    patch.write_text("diff")
    rows = [
        {"instance_id": "a", "arm": "aracne", "success": True, "patch_path": str(patch)},
        {"instance_id": "a", "arm": "baseline", "success": None, "patch_path": None},
        {"instance_id": "a", "arm": "aracne", "error": "crashed"},
        {"instance_id": "a", "arm": "aracne", "timeout_stage": "turns"},
        {"instance_id": "s", "arm": "aracne", "patch_path": str(patch)},
    ]
    got = run_benchmark._collect_for_localization(rows, [_task("a"), _task("s", "swe_bench")])
    assert [(r["arm"], p) for r, _t, _a, p in got] == [("aracne", "diff"), ("baseline", "")]


def test_labels_name_the_variant_and_grading_mode():
    atlas, swe = [_task("a")], [_task("s", "swe_bench")]
    assert "navigation variant" in run_benchmark.atlas_variant_label(atlas)
    assert run_benchmark.atlas_variant_label(swe) is None
    assert "localization only" in run_benchmark.grading_label({"cheap_grade": True}, atlas)
    assert "no hidden tests" in run_benchmark.grading_label({}, atlas)
    assert "hidden tests + rubric" in run_benchmark.grading_label({"atlas_grading": "verifier"}, atlas)


def test_navigation_records_when_a_gold_file_is_first_found(tmp_path):
    def assistant(mid, cmd, tokens):
        return json.dumps({"type": "assistant", "message": {
            "id": mid, "usage": {"input_tokens": tokens},
            "content": [{"type": "tool_use", "input": {"command": cmd}}]}})
    t = tmp_path / "t.jsonl"
    t.write_text("\n".join([assistant("m1", "ls", 10), assistant("m2", "cat pkg/a.go", 20),
                            assistant("m3", "sed -i 's/x/y/' pkg/a.go", 30)]))
    nav = L.navigation(t, ["pkg/a.go"])
    assert (nav["nav_turns_to_first_gold_touch"], nav["nav_tokens_to_first_gold_touch"]) == (2, 30)
    assert nav["nav_turns_to_first_gold_edit"] == 3

#!/usr/bin/env python3
"""Regression tests for bench/atlas_prompt.strip_interface.

THE BUG. SWE-Atlas instructions end with an interface specification -- every declaration the
reference solution adds or re-signs, with its path, name and signature. It was passed to both
arms verbatim, handing the agent the navigation answer aracne exists to find.

    python3 -m pytest bench/test_atlasprompt.py
"""
from __future__ import annotations

import glob
import json
import re
from pathlib import Path

import json

import pytest

from bench.atlas_prompt import strip_interface
from bench import sources

ISSUE = "The engine is tangled.\n\nCan you split it into a module?\n"
TESTS = ("I've already taken care of all changes to the test files. Do NOT modify any test "
         "files or testing logic in any way.")


def test_lead_in_block_runs_to_the_end_across_separators():
    text = (ISSUE + "\n" + TESTS + "\n\nUse the below Interface:\n\n- Path: `a/b.go`\n- Name: `New`\n"
            "- Type: function\n- Input: `NA`\n- Output: `*T`\n- Description: makes one.\n\n---\n\n"
            "- Path: `a/c.go`\n- Name: `Other`\n- Type: function\n- Description: another.\n")
    out, removed = strip_interface(text)
    assert "Path:" not in out and "Interface" not in out and "---" not in out
    assert out.startswith("The engine is tangled.") and TESTS in out
    assert removed > 0


@pytest.mark.parametrize("lead", ["Use the below interface for your solution:", "Use the interface below:",
                                  "Use the below interface:"])
def test_lead_in_spellings(lead):
    out, _ = strip_interface(ISSUE + "\n" + lead + "\n\n- Path: `x.py`\n- Name: `f`\n")
    assert "Path:" not in out and lead not in out and "Can you split it" in out


@pytest.mark.parametrize("heading", ["# Interface", "# Public Interfaces", "# Public Interface Specifications"])
def test_heading_block_ends_at_the_tests_sentence_which_is_kept(heading):
    text = ("# Problem Statement\n\n" + ISSUE + "\n---\n\n" + heading + "\n\n## Modified Functions\n\n"
            "- Path: `x.c`\n- Name: `decode`\n\n---\n\n- Path: `y.c`\n- Name: `other`\n\n---\n\n"
            + TESTS + "\n\nYour task is to make the minimal changes to non-tests files.\n")
    out, _ = strip_interface(text)
    assert "Path:" not in out and heading not in out and "Modified Functions" not in out
    assert TESTS in out and "minimal changes to non-tests files" in out
    assert "\n---" not in out


def test_unheaded_numbered_entries():
    text = (ISSUE + "\n" + TESTS + "\n\n## 1. useThing\n\n- **Path:** `client/x.ts`\n"
            "- **Signature:** `() => boolean`\n\n## 2. Props\n\n- **File:** `client/y.tsx`\n")
    out, _ = strip_interface(text)
    assert "useThing" not in out and "**Path:**" not in out and TESTS in out


def test_numbered_prose_headings_are_not_interfaces():
    text = ISSUE + "\n## 1. Background\n\nThe engine grew organically.\n\n## 2. Goal\n\nSplit it.\n"
    assert strip_interface(text) == (text, 0)


def test_no_block_is_untouched_and_idempotent():
    assert strip_interface(ISSUE) == (ISSUE, 0)
    once, _ = strip_interface(ISSUE + "\nUse the below Interface:\n\n- Path: `a`\n")
    assert strip_interface(once) == (once, 0)


def test_read_manifest_strips_atlas_tasks_only(tmp_path):
    block = "\nUse the below Interface:\n\n- Path: `a.go`\n- Name: `New`\n"
    recs = [
        {"key": "a", "language": "go", "source": "swe_atlas_rf", "clone_url": "u", "base_commit": "c",
         "problem_statement": ISSUE + block, "raw": {}},
        {"key": "b", "language": "python", "source": "swe_bench", "clone_url": "u", "base_commit": "c",
         "problem_statement": ISSUE + block, "raw": {}},
    ]
    p = tmp_path / "m.jsonl"
    p.write_text("".join(json.dumps(r) + "\n" for r in recs))
    atlas, other = sources.read_manifest(p)
    assert "Path:" not in atlas.problem_statement
    assert other.problem_statement == ISSUE + block


REAL = sorted(glob.glob(str(Path.home() / ".cache/aracne-bench/tools/SWE-Atlas/data/*/task-*/instruction.md")))


@pytest.mark.skipif(not REAL, reason="SWE-Atlas data not checked out")
def test_every_real_instruction_is_clean():
    import re
    from bench.atlas_prompt import _FIELD, _TESTS_LINE
    for f in REAL:
        src = Path(f).read_text()
        out, _ = strip_interface(src)
        assert not re.search(r"use the (below )?interface|^\s*\\?#{1,4}\s*(public\s+)?interfaces?", out, re.I | re.M), f
        assert sum(1 for ln in out.split("\n") if _FIELD.match(ln)) < 2, f
        if any(_TESTS_LINE.match(ln) for ln in src.split("\n")):
            assert any(_TESTS_LINE.match(ln) for ln in out.split("\n")), f


from bench.atlas_prompt import neutralize_test_claim, sanitize


@pytest.mark.parametrize("claim", [
    "I've already taken care of all changes to the test files. Do NOT modify any test files or testing logic "
    "in any way. Your task is to make the minimal changes to non-test source files only.",
    "I have already taken care of all changes to the test files. Do NOT modify any test files in any way.",
    "I've already taken care of all changes to the test files. This means you DON'T have to modify the "
    "testing logic or any of the tests in any way!",
])
def test_the_issue_text_says_nothing_about_tests(claim):
    out, changed = neutralize_test_claim(ISSUE + "\n" + claim + "\n")
    assert changed and "Can you split it" in out
    assert not re.search(r"test files|testing logic|taken care", out, re.I)


def test_sanitize_strips_the_interface_before_the_claim_it_is_anchored_on():
    text = ("# Problem Statement\n\n" + ISSUE + "\n# Public Interfaces\n\n- Path: `x.c`\n- Name: `f`\n\n---\n\n"
            + TESTS + "\n\nYour task is to make the minimal changes to non-tests files.\n")
    out, changes = sanitize(text)
    assert "Path:" not in out and "taken care of" not in out and "test files" not in out
    assert changes["interface_removed_chars"] > 0 and changes["test_claim_removed"]
    assert sanitize(out) == (out, {"interface_removed_chars": 0, "test_claim_removed": False, "paths_removed": []})


@pytest.mark.skipif(not REAL, reason="SWE-Atlas data not checked out")
def test_no_real_instruction_keeps_the_claim():
    import re
    # Every split, so the deterministic steps: only rf is benchmarked, so only rf has rewrites.
    for f in REAL:
        out = AP.strip_and_neutralize(Path(f).read_text())
        assert not re.search(r"already taken care of all changes to the test files", out, re.I), f


from bench import atlas_prompt as AP


@pytest.mark.parametrize("text,paths", [
    ("the handler in `api/v1/routes.go` and `cmd/run.go`", ["api/v1/routes.go", "cmd/run.go"]),
    ("update CMakeLists.txt and the Makefile", ["CMakeLists.txt", "Makefile"]),
    ("everything under `apps/plugins/`", ["apps/plugins/"]),
    ("move it out of backend.go", ["backend.go"]),
    ("GET/POST, I/O, Rust/C, SetMain/RunMain, cipher/AKM and the /api/dashboards route", []),
    ("use `metrics.Registry` e.g. via `engine.New`", []),
])
def test_find_paths(text, paths):
    assert sorted(set(AP.find_paths(text))) == sorted(paths)


def test_file_stems_are_files_only_when_the_sentence_is_about_files():
    text = ("It extracted `rrdset-slots` and `rrd-database-mode` into their own files. "
            "Refactor it to use `react-hook-form` for state. The `marketplace-redesign` feature flag is in 15 files.")
    assert AP.file_stems([], text) == {"rrdset-slots", "rrd-database-mode"}
    assert AP.file_stems(["client/overview-card/index.tsx"]) == {"overview-card"}


def test_a_prompt_naming_files_without_a_rewrite_stops_the_run(monkeypatch, tmp_path):
    monkeypatch.setattr(AP, "REWRITES_PATH", tmp_path / "none.jsonl")
    with pytest.raises(AP.PromptNotRewritten):
        AP.sanitize(ISSUE + "\nThe handler lives in `api/v1/routes.go`.\n", "some-task")
    # Nothing to remove: no rewrite needed.
    assert AP.sanitize(ISSUE, "some-task")[0] == ISSUE


def test_a_stale_rewrite_is_refused_and_a_matching_one_is_used(monkeypatch, tmp_path):
    import hashlib
    src = ISSUE + "\nThe handler lives in `api/v1/routes.go`.\n"
    store = tmp_path / "rw.jsonl"
    monkeypatch.setattr(AP, "REWRITES_PATH", store)
    good = {"key": "t", "source_sha256": hashlib.sha256(src.encode()).hexdigest(),
            "prompt": ISSUE + "\nThe v1 route handler.\n", "removed": ["api/v1/routes.go"]}
    store.write_text(json.dumps(good) + "\n")
    out, ch = AP.sanitize(src, "t")
    assert out == good["prompt"] and ch["paths_removed"] == ["api/v1/routes.go"]
    store.write_text(json.dumps({**good, "source_sha256": "0" * 64}) + "\n")
    with pytest.raises(AP.PromptNotRewritten):
        AP.sanitize(src, "t")


@pytest.mark.skipif(not REAL, reason="SWE-Atlas data not checked out")
def test_every_real_rf_prompt_names_no_files():
    import re
    for f in [x for x in REAL if "/rf/" in x]:
        d = Path(f).parent
        cfg = json.loads((d / "tests" / "config.json").read_text())
        key = f"{cfg['repo']}-{cfg['task_id'][-8:]}"
        src = Path(f).read_text()
        try:
            out, _ = sanitize(src, key)
        except AP.PromptNotRewritten:
            continue  # no valid rewrite yet: the loader refuses the task, so it can never be run
        assert not AP.find_paths(out), (key, AP.find_paths(out))
        for stem in AP.file_stems(AP.find_paths(src), src):
            assert not re.search(r"(?<![\w-])%s(?![\w-])" % re.escape(stem), out), (key, stem)

"""A control that looked up the answer cannot be a control.

The scan itself is covered by test_answerkey.py, which tests the LIVE counter in toolstats.
What is tested here is the archival half: reading contamination back out of stored
transcripts, across the import chain, for rows written before the counter existed.

That gap was not hypothetical. `netguard-20260831b` imported 27 baseline rows from
`opus-medium-fixed`, which imported them from `opus-medium`, where two of them had curl'd the
answer -- and because the field was simply ABSENT rather than zero, `paired._graded` scored
both as clean baseline solves. One was a baseline-only win over an aracne failure.
"""
from __future__ import annotations

import json

import pytest

from bench import contamination, paired, toolstats

REPO = "sveltejs/svelte"
INST = "sveltejs__svelte-14456"


def _write_run(root, name, rows, transcripts=None, baseline_from=None):
    d = root / name
    (d / "transcripts").mkdir(parents=True)
    (d / "runs.jsonl").write_text("\n".join(json.dumps(r) for r in rows) + "\n")
    meta = {"run_name": name, "config": {"baseline_from": baseline_from}}
    (d / "run_meta.json").write_text(json.dumps(meta))
    for fname, body in (transcripts or {}).items():
        (d / "transcripts" / fname).write_text(body)
    return d


def _transcript(command, result):
    call = {"type": "assistant", "message": {"content": [
        {"type": "tool_use", "id": "1", "name": "Bash", "input": {"command": command}}]}}
    res = {"type": "user",
           "tool_use_result": result,
           "message": {"content": [{"type": "tool_result", "tool_use_id": "1"}]}}
    return json.dumps(call) + "\n" + json.dumps(res) + "\n"


def _row(arm, **kw):
    return {"instance_id": INST, "arm": arm, "seed": 0,
            "run_key": f"{INST}__{arm}__s0", "success": True, "outcome": "correct", **kw}


def test_answer_key_is_derived_from_the_instance_id():
    assert contamination.answer_key_for(INST) == REPO
    assert contamination.answer_key_for("pytest-dev__pytest-5495") == "pytest-dev/pytest"


def test_a_fetch_that_returned_the_diff_is_contamination(tmp_path):
    t = _transcript(f"curl -sL https://github.com/{REPO}/pull/14456.diff",
                    "diff --git a/x b/x\n+++ b/x")
    d = _write_run(tmp_path, "r", [_row("baseline")],
                   {f"{INST}__baseline__s0.jsonl": t})
    assert contamination.scan(d / "transcripts" / f"{INST}__baseline__s0.jsonl", REPO) == (1, 1)


def test_a_refused_fetch_is_an_attempt_but_not_contamination(tmp_path):
    t = _transcript(f"curl -sL https://github.com/{REPO}/pull/14456.diff",
                    "aracne-bench: refusing to fetch sveltejs/svelte")
    d = _write_run(tmp_path, "r", [_row("baseline")],
                   {f"{INST}__baseline__s0.jsonl": t})
    attempts, fetches = contamination.scan(
        d / "transcripts" / f"{INST}__baseline__s0.jsonl", REPO)
    assert (attempts, fetches) == (1, 0), "a lookup that provably failed is still a clean cell"


# The chain is the whole point. `imported_from` is STAMPED with the importer's own source on
# every hop, so after two imports every row claims to come from the middle run and the trail
# to the transcripts is gone. Walking run_meta's config instead is what keeps it findable.
def test_transcripts_are_found_two_imports_away(tmp_path):
    t = _transcript(f"curl -sL https://github.com/{REPO}/pull/14456.diff", "diff --git a/x b/x")
    _write_run(tmp_path, "origin", [_row("baseline")], {f"{INST}__baseline__s0.jsonl": t})
    _write_run(tmp_path, "middle", [_row("baseline", imported_from="origin")],
               baseline_from="origin")
    last = _write_run(tmp_path, "last", [_row("baseline", imported_from="middle")],
                      baseline_from="middle")

    monkey = contamination.RESULTS
    contamination.RESULTS = tmp_path
    try:
        row = _row("baseline", imported_from="middle")
        found = contamination.transcript_for(row, last)
        assert found is not None and found.parent.parent.name == "origin"
        hits = contamination.contaminated_instances("last")
        assert INST in hits and hits[INST]["fetches"] == 1
    finally:
        contamination.RESULTS = monkey


def test_a_missing_transcript_is_reported_not_assumed_clean(tmp_path):
    d = _write_run(tmp_path, "orphan", [_row("baseline")])
    monkey = contamination.RESULTS
    contamination.RESULTS = tmp_path
    try:
        info = contamination.audit("orphan")[(INST, "baseline", 0)]
        assert info["transcript"] is None, "an unreachable transcript must be visible as such"
        assert info["fetches"] == 0
    finally:
        contamination.RESULTS = monkey


def test_a_missing_counter_is_not_the_same_as_a_zero_one():
    # The exact shape of the bug: the imported row has full tool telemetry and no answer-key
    # field, and _graded reads it as a clean solve.
    stale = {"success": True, "outcome": "correct", "n_tool_calls": 91}
    assert paired._graded(stale), "documents the hole: absent reads as clean"
    assert not paired._graded({**stale, "n_answer_key_fetches": 1}), \
        "once the field is filled in from the transcript, the cell is censored"


def test_the_real_import_chain_is_still_auditable():
    """Regression against the actual runs on disk, not a synthetic fixture.

    Skipped rather than failed when the historical runs are absent, so the suite stays green
    on a fresh checkout.
    """
    try:
        hits = contamination.contaminated_instances("netguard-20260831b")
    except FileNotFoundError:
        pytest.skip("historical runs not present")
    assert "anuraghazra__github-readme-stats-2491" in hits
    assert "mwaskom__seaborn-3407" in hits
    for info in hits.values():
        assert info["fetches"] >= 1 and info["success"] is True


def test_efficiency_excludes_contaminated_pairs_too():
    """A fetched answer makes a cell CHEAP, so leaving it in flatters whichever arm cheated.

    Censoring only the solve rate would remove the wrong half of the damage: the pair still
    contributes tokens and turns, and the fetch typically lands early, so the contaminated arm
    looks efficient for having stopped working.
    """
    def rows(fetches):
        base = {"cluster": "a/b",
                "baseline": {"success": True, "outcome": "correct", "n_tool_calls": 5,
                             "context_tokens": 100000, "num_turns": 10},
                "aracne": {"success": True, "outcome": "correct", "n_tool_calls": 5,
                           "context_tokens": 10000, "num_turns": 2, **fetches}}
        return [base]

    clean = paired.ratio_analysis(rows({}), "context_tokens",
                                  lambda r: r["context_tokens"])
    dirty = paired.ratio_analysis(rows({"n_answer_key_fetches": 3}), "context_tokens",
                                  lambda r: r["context_tokens"])
    assert clean["n_pairs"] == 1 and clean["ratio"] is not None
    assert dirty["n_pairs"] == 0 and dirty["ratio"] is None, \
        "a 10x 'saving' bought by curl'ing the diff must not enter the ratio"


def test_the_live_counter_and_the_archival_reader_agree(tmp_path):
    """Two implementations of one question must not answer it differently.

    `gh issue view ... 2>/dev/null` on a host without `gh` returns an empty result: the binary
    is missing and its error goes to the void. Nothing came back, so it is an ATTEMPT and not
    contamination. The archival reader scored it correctly because it reads the event's
    `tool_use_result`; the live counter read the content BLOCK, which for a Bash result is the
    rendering shown to the model, and scored it as a fetch -- censoring a clean pair out of
    fair-20260901a.
    """
    cmd = f"gh issue view 1 --repo {REPO} 2>/dev/null | head -60"
    call = {"type": "assistant", "message": {"content": [
        {"type": "tool_use", "id": "1", "name": "Bash", "input": {"command": cmd}}]}}
    # The shape that broke it: a block rendering that is neither empty nor a stdout payload,
    # beside an event payload that plainly says nothing came back.
    res = {"type": "user",
           "tool_use_result": {"stdout": "", "stderr": "", "interrupted": False},
           "message": {"content": [
               {"type": "tool_result", "tool_use_id": "1", "content": "(no content)"}]}}
    transcript = json.dumps(call) + "\n" + json.dumps(res) + "\n"

    live, _ = toolstats.summarize(transcript, REPO)
    path = tmp_path / "t.jsonl"
    path.write_text(transcript)
    archival = contamination.scan(path, REPO)

    assert (live["n_answer_key_attempts"], live["n_answer_key_fetches"]) == archival
    assert live["n_answer_key_fetches"] == 0, "an empty result is not contamination"

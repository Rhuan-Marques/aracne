"""The web stays open; the repository holding the graded diff does not.

Searching the web is legitimate engineering behaviour and a benchmark that forbids it is
measuring an agent with one hand tied. But SWE-bench's answer sits at a URL derived
mechanically from the instance id, and across three `scale40` runs every single network call
the agent made was one of those: twelve fetch events over six cells, one hundred percent of
them the task's own repository, none of them documentation or a package registry. Three
"solves" came from reading the diff, and one was the only aracne-only win in the matrix.

Two mechanisms, tested here together because either alone is insufficient:

  netshim  -- prevention. Wraps `curl`/`wget` so the repository under test is unreachable and
              everything else is not.
  toolstats -- audit. Reads attempts to reach it back out of the transcript, catching whatever
              route the shim does not wrap. An attempt only CONTAMINATES when something came
              back: a refusal, a missing binary or an empty body leaves the model exactly where
              it was, and paired._graded censors on fetches rather than attempts.
"""
from __future__ import annotations

import json
import os
import subprocess

from bench import netshim, paired, toolstats

REPO = "sveltejs/svelte"


def _run(cmd: str, deny: str | None = REPO):
    env = netshim.apply(dict(os.environ), deny)
    return subprocess.run(cmd, shell=True, capture_output=True, text=True, env=env)


def test_shim_refuses_the_repository_under_test():
    # The three shapes the answer key actually took across the three runs.
    for cmd in (
        f"curl -sL https://github.com/{REPO}/pull/14456.diff",
        f'curl -s "https://api.github.com/search/issues?q=repo:{REPO}+render+tags"',
        f"wget -qO- https://patch-diff.githubusercontent.com/raw/{REPO}/pull/14456.diff",
    ):
        p = _run(cmd)
        assert p.returncode == 3, f"{cmd!r} was not refused (rc={p.returncode})"
        assert "refusing to fetch" in p.stderr, p.stderr
        assert "diff --git" not in p.stdout, "the shim let the diff through"


def test_shim_leaves_the_rest_of_the_web_alone():
    # `curl --version` needs no network, so this half of the contract is always testable.
    p = _run("curl --version")
    assert p.returncode == 0 and "curl" in p.stdout

    # A different repository on the very same host must still be reachable. Skipped rather
    # than failed without a network, so the suite stays green offline.
    probe = _run('curl -s -m 20 -o /dev/null -w "%{http_code}" https://github.com/rust-lang/rust')
    if probe.returncode == 0 and probe.stdout.strip() == "200":
        assert True
    else:
        import pytest
        pytest.skip("no network; the deny half of the contract is covered above")


def test_shim_is_a_no_op_without_a_deny_target():
    env = netshim.apply(dict(os.environ), None)
    assert netshim.DENY_ENV not in env


def test_deny_target_reads_the_clone_url():
    assert netshim.deny_target("https://github.com/sveltejs/svelte.git") == REPO
    assert netshim.deny_target("https://github.com/sveltejs/svelte/") == REPO
    assert netshim.deny_target("") == ""


def _transcript(*calls):
    ev = {"type": "assistant", "message": {"content": [
        {"type": "tool_use", "id": str(i), "name": n, "input": inp}
        for i, (n, inp) in enumerate(calls)]}}
    return json.dumps(ev)


def test_audit_records_every_attempt():
    # Both routes to the answer key are recognised as attempts. Whether an attempt also counts
    # as CONTAMINATION depends on what came back -- see the exchange tests further down, which
    # is the distinction the first netshim run forced within four cells.
    t = _transcript(
        ("Bash", {"command": f"curl -sL https://github.com/{REPO}/pull/14456.diff"}),
        ("WebFetch", {"url": f"https://github.com/{REPO}/issues/14399"}),
    )
    stats, _ = toolstats.summarize(t, REPO)
    assert stats["n_answer_key_attempts"] == 2
    # No results in this transcript, so nothing is known to have come back.
    assert stats["n_answer_key_fetches"] == 0


def test_audit_ignores_other_hosts_and_local_commands():
    t = _transcript(
        ("Bash", {"command": "curl -s https://developer.mozilla.org/en-US/"}),
        ("Bash", {"command": "curl -s https://github.com/rust-lang/rust"}),
        # Naming the repo locally is not a fetch -- grepping the worktree must stay free.
        ("Bash", {"command": f"grep -rn {REPO} ."}),
        ("mcp__aracne__grep", {"pattern": REPO}),
    )
    stats, _ = toolstats.summarize(t, REPO)
    assert stats["n_answer_key_fetches"] == 0


def test_audit_is_inert_without_an_answer_key():
    t = _transcript(("Bash", {"command": f"curl -sL https://github.com/{REPO}/pull/1.diff"}))
    stats, _ = toolstats.summarize(t, "")
    assert stats["n_answer_key_fetches"] == 0


def test_contaminated_cell_is_censored_not_failed():
    clean = {"success": True, "outcome": "correct"}
    dirty = {"success": True, "outcome": "correct", "n_answer_key_fetches": 1}
    assert paired._graded(clean)
    assert not paired._graded(dirty), "a cell that fetched the answer must not count as a solve"


def test_censoring_drops_the_whole_pair():
    # solve_analysis requires both arms graded, so censoring one arm removes the repository
    # from the comparison entirely. Dropping it from one side only would bias the result
    # toward whichever arm stayed clean.
    pairs = [
        {"cluster": "a/b", "baseline": {"success": True, "outcome": "correct"},
         "aracne": {"success": True, "outcome": "correct", "n_answer_key_fetches": 2}},
        {"cluster": "c/d", "baseline": {"success": False, "outcome": "fail"},
         "aracne": {"success": True, "outcome": "correct"}},
    ]
    res = paired.solve_analysis(pairs)
    assert res["n_pairs"] == 1, "the contaminated repository should be gone from both arms"
    assert res["aracne_only"] == 1


def _exchange(command, result, name="Bash"):
    """One tool call and its result, as a two-event transcript."""
    inp = {"command": command} if name == "Bash" else {"url": command}
    call = {"type": "assistant", "message": {"content": [
        {"type": "tool_use", "id": "1", "name": name, "input": inp}]}}
    res = {"type": "user", "message": {"content": [
        {"type": "tool_result", "tool_use_id": "1", "content": result}]}}
    return json.dumps(call) + "\n" + json.dumps(res)


def _counts(command, result):
    stats, _ = toolstats.summarize(_exchange(command, result), REPO)
    return stats["n_answer_key_attempts"], stats["n_answer_key_fetches"]


# "Went looking" and "found it" are different facts, and only the second invalidates a pair.
# The first netshim run made this concrete within four cells: a cell issued two answer-key
# calls, the shim refused the curl and `gh` was not installed, so the model got nothing.
# Censoring it would have discarded a clean data point over a lookup that provably failed.
def test_a_refused_fetch_is_an_attempt_but_not_contamination():
    assert _counts(f"curl -sL https://github.com/{REPO}/pull/1.diff",
                   "aracne-bench: refusing to fetch sveltejs/svelte") == (1, 0)


def test_a_successful_fetch_is_contamination():
    assert _counts(f"curl -sL https://github.com/{REPO}/pull/1.diff",
                   "diff --git a/x b/x\n+++ b/x") == (1, 1)


def test_an_empty_result_is_not_contamination():
    # `gh issue view … 2>/dev/null` sends its own "not installed" error to the void and comes
    # back looking like a clean, empty success. It taught the model nothing either way.
    empty = json.dumps({"stdout": "", "stderr": "", "interrupted": False})
    assert _counts(f"gh issue view 1 --repo {REPO} 2>/dev/null | head -60", empty) == (1, 0)


def test_an_fd_redirect_is_not_writing_the_answer_somewhere():
    # The exception to the empty-result rule is a command that writes the body to a FILE, where
    # empty stdout still means success. `2>/dev/null` must not be mistaken for one.
    from bench.toolstats import _WRITES_TO_FILE
    assert not _WRITES_TO_FILE.search(f"gh issue view 1 --repo {REPO} 2>/dev/null")
    assert not _WRITES_TO_FILE.search("curl -s https://x 2>&1 | head")
    assert _WRITES_TO_FILE.search("curl -sSL -o pr.diff https://x")
    assert _WRITES_TO_FILE.search("curl https://x > out.diff")


def test_censoring_uses_fetches_not_attempts():
    assert paired._graded({"success": True, "outcome": "correct",
                           "n_answer_key_attempts": 3, "n_answer_key_fetches": 0}), \
        "a cell whose every answer-key lookup was refused is still a clean data point"
    assert not paired._graded({"success": True, "outcome": "correct",
                               "n_answer_key_attempts": 3, "n_answer_key_fetches": 1})

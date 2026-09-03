"""Tests for bench/bench/toolstats.py — the per-tool telemetry folded out of a
Claude Code `stream-json` transcript.

Run with `--assert=plain` in this environment: the bench fixtures leaked an editable
install of pytest 4.6 into user site-packages, and its assertion rewriter cannot parse
Python 3.12 ASTs.
"""
from __future__ import annotations

import json

from bench import toolstats


def _assistant(*tool_uses):
    return json.dumps({
        "type": "assistant",
        "message": {"content": [
            {"type": "tool_use", "id": tid, "name": name, "input": {}}
            for tid, name in tool_uses
        ]},
    })


def _result(tid, text, is_error=None):
    block = {"type": "tool_result", "tool_use_id": tid, "content": text}
    if is_error is not None:
        block["is_error"] = is_error
    return json.dumps({"type": "user", "message": {"content": [block]}})


def _terminal(**kw):
    return json.dumps({"type": "result", "subtype": "success", **kw})


MCP_READ = "mcp__aracne__read_function"
MCP_GREP = "mcp__aracne__grep"


def test_empty_stats_shape_matches_row_fields():
    stats = toolstats.empty_stats()
    row = toolstats.row_fields(stats)
    assert set(row) == set(toolstats.ROW_FIELDS)
    assert all(v == 0 for v in row.values())
    assert stats["has_transcript"] is False


def test_blank_transcript_yields_zero_row():
    stats, reduced = toolstats.summarize("")
    assert reduced == ""
    assert stats["has_transcript"] is False
    assert stats["n_tool_calls"] == 0


def test_counts_mcp_vs_native_and_bytes():
    stream = "\n".join([
        _assistant(("t1", MCP_READ), ("t2", "Read")),
        _result("t1", "x" * 100),
        _result("t2", "y" * 40),
        _assistant(("t3", MCP_GREP)),
        _result("t3", "z" * 7),
        _terminal(),
    ])
    stats, _ = toolstats.summarize(stream)
    assert stats["n_tool_calls"] == 3
    assert stats["n_mcp_calls"] == 2
    assert stats["n_native_calls"] == 1
    assert stats["mcp_result_bytes"] == 107
    assert stats["native_result_bytes"] == 40
    # read_/grep_result_bytes span BOTH arms on purpose (native Read counts too), so the
    # two arms' discovery cost is directly comparable: 100 (MCP read) + 40 (native Read).
    assert stats["read_result_bytes"] == 140
    assert stats["grep_result_bytes"] == 7
    assert stats["tool_calls"][MCP_READ] == 1
    assert stats["has_transcript"] is True


def test_detects_id_miss_ambiguity_and_guard_denial():
    stream = "\n".join([
        _assistant(("t1", MCP_READ), ("t2", MCP_READ), ("t3", "Grep")),
        _result("t1", 'resource "src/flask/app.Flask.x" not found in topology', True),
        _result("t2", 'Multiple resources matching "__init__" found:\n- a\n- b\n'),
        _result("t3", "Blocked by aracne config (blocked_tools: grep). Use the aracne MCP tools."),
        _terminal(),
    ])
    stats, _ = toolstats.summarize(stream)
    assert stats["n_id_misses"] == 1
    assert stats["n_ambiguous"] == 1
    assert stats["n_guard_denials"] == 1
    assert stats["tool_errors"][MCP_READ] == 1


def test_permission_denials_on_terminal_event_win():
    """A PreToolUse deny never reaches the model as a tool_result, so the CLI's own
    `permission_denials` list is the authoritative count and must not be undercounted."""
    stream = "\n".join([
        _assistant(("t1", "Read")),
        _result("t1", "Blocked by aracne config (blocked_tools: read)."),
        _terminal(permission_denials=[{"tool_name": "Read"}, {"tool_name": "Grep"},
                                      {"tool_name": "Edit"}]),
    ])
    stats, _ = toolstats.summarize(stream)
    assert stats["n_guard_denials"] == 3


def test_reduced_transcript_elides_bodies_but_keeps_events():
    big = "q" * 50_000
    stream = "\n".join([
        _assistant(("t1", MCP_READ)),
        _result("t1", big),
        _terminal(),
    ])
    stats, reduced = toolstats.summarize(stream)
    assert stats["read_result_bytes"] == 50_000
    assert big not in reduced
    assert "50000 bytes elided" in reduced
    assert len(reduced.splitlines()) == 3
    # still valid NDJSON
    assert [json.loads(l)["type"] for l in reduced.splitlines()] == \
        ["assistant", "user", "result"]


def test_survives_malformed_and_truncated_lines():
    stream = "\n".join([
        "not json at all",
        _assistant(("t1", MCP_READ)),
        '{"type":"assistant","message":{"content":',      # truncated
        _result("t1", "ok"),
    ])
    stats, _ = toolstats.summarize(stream)
    assert stats["n_tool_calls"] == 1
    assert stats["mcp_result_bytes"] == 2


def test_result_content_as_block_list():
    """Claude Code may send tool_result content as a list of text parts rather than a
    bare string; both must be measured the same way."""
    stream = "\n".join([
        _assistant(("t1", MCP_READ)),
        json.dumps({"type": "user", "message": {"content": [
            {"type": "tool_result", "tool_use_id": "t1",
             "content": [{"type": "text", "text": "abc"}, {"type": "text", "text": "de"}]},
        ]}}),
    ])
    stats, _ = toolstats.summarize(stream)
    assert stats["read_result_bytes"] == 5


def test_unknown_tool_use_id_does_not_crash():
    stream = "\n".join([_result("orphan", "hello"), _terminal()])
    stats, _ = toolstats.summarize(stream)
    assert stats["tool_result_bytes"]["?"] == 5
    assert stats["n_tool_calls"] == 0


def _bash(tid, command):
    return json.dumps({
        "type": "assistant",
        "message": {"content": [
            {"type": "tool_use", "id": tid, "name": "Bash", "input": {"command": command}},
        ]},
    })


def test_terminal_surface_is_visible_on_the_row():
    """The terminal arm delivers aracne through Bash, so every name-keyed counter reads it as
    a plain baseline. Without these two the report cannot tell a terminal arm from the control.
    """
    transcript = "\n".join([
        _bash("1", "/usr/local/bin/arac cmd -- head -20 pkg/shapes.go"),
        _result("1", "```pkg/shapes.go\nfunc Total() {}\n```\n\n# CONTEXT:\n## pkg.Shape: a shape\n"),
        _bash("2", "go test ./..."),
        _result("2", "ok  \tdemo\t0.1s\n"),
    ]) + "\n"

    stats, _ = toolstats.summarize(transcript, "org/repo")
    assert stats["n_intercepted"] == 1, "an intercepted read must be counted"
    assert stats["terminal_result_bytes"] > 0
    assert stats["n_mcp_calls"] == 0, "the terminal surface serves no MCP tools"
    row = toolstats.row_fields(stats)
    assert "n_intercepted" in row and "terminal_result_bytes" in row


def test_an_intercepted_call_counts_once_from_either_signature():
    """The call form and the result form are two views of one exchange.

    It is not settled whether a transcript records a Bash call's input before or after a
    PreToolUse hook rewrites it, so both are matched -- and a transcript carrying both must
    not double-count.
    """
    both = "\n".join([
        _bash("1", "arac cmd -- tail -5 pkg/shapes.go"),
        _result("1", "```pkg/shapes.go\nx\n```\n\n# CONTEXT:\n## pkg.Y: y\n"),
    ]) + "\n"
    call_only = "\n".join([
        _bash("1", "arac cmd -- tail -5 pkg/shapes.go"),
        _result("1", "x\n"),
    ]) + "\n"
    result_only = "\n".join([
        _bash("1", "tail -5 pkg/shapes.go"),
        _result("1", "```pkg/shapes.go\nx\n```\n\n# CONTEXT:\n## pkg.Y: y\n"),
    ]) + "\n"

    for label, transcript in (("both", both), ("call", call_only), ("result", result_only)):
        stats, _ = toolstats.summarize(transcript, "org/repo")
        assert stats["n_intercepted"] == 1, f"{label}: got {stats['n_intercepted']}"


def test_an_ordinary_shell_command_is_not_counted_as_intercepted():
    transcript = "\n".join([
        _bash("1", "grep -rn TODO ."),
        _result("1", "app.go:3:// TODO\n"),
    ]) + "\n"
    stats, _ = toolstats.summarize(transcript, "org/repo")
    assert stats["n_intercepted"] == 0
    assert stats["terminal_result_bytes"] == 0


def test_an_enriched_answer_with_no_context_block_still_counts():
    """The exact shape a live session produced, and the one that broke the first version.

    `tail -4` on a function whose neighbours are all undocumented comes back framed by its
    signature and an elision marker, with NO `# CONTEXT:` section at all. Keying interception
    on the context block alone scored that run as zero interceptions while every read in it had
    in fact been answered by aracne.

    Note the tool_use input: it is the command the MODEL typed. A PreToolUse hook's
    `updatedInput` changes what runs, not what the transcript records, so the call side cannot
    be relied on.
    """
    result = (
        "```pkg/shapes.go\n"
        "func Total(shapes []Shape) float64 {\n"
        "⋯ demo/pkg.Total not shown here (2 lines before this window) — "
        'read "demo/pkg.Total" for its source ⋯\n'
        "\t\tsum += s.Area()\n\t}\n\treturn sum\n}\n```\n"
    )
    transcript = "\n".join([
        _bash("1", "tail -4 pkg/shapes.go"),
        _result("1", result),
    ]) + "\n"

    stats, _ = toolstats.summarize(transcript, "org/repo")
    assert stats["n_intercepted"] == 1, "an enriched answer without a context block must count"
    assert stats["terminal_result_bytes"] > 0
    assert stats["shell_read_result_bytes"] > 0, "and it is discovery cost, counted as such"


def test_a_real_cat_is_not_mistaken_for_an_enriched_answer():
    """The counters must not turn plain shell output into evidence of interception."""
    transcript = "\n".join([
        _bash("1", "cat NOTES.md"),
        _result("1", "alpha\nbeta\ngamma\n"),
        _bash("2", "grep -rn TODO ."),
        _result("2", "app.go:3:// TODO\napp.go:9:// TODO\n"),
    ]) + "\n"
    stats, _ = toolstats.summarize(transcript, "org/repo")
    assert stats["n_intercepted"] == 0
    assert stats["terminal_result_bytes"] == 0
    # Both are still discovery, and both are still counted as such.
    assert stats["shell_read_result_bytes"] > 0
    assert stats["shell_grep_result_bytes"] > 0


def test_a_package_download_counts_as_reaching_outside_the_worktree():
    """The gap that cost a result in linerange-20260902a.

    That run's seaborn cell ran `pip download seaborn==0.13.2`, unzipped the wheel and diffed
    the released module against the checkout, then patched the region the diff pointed at. A
    later release of the package under test contains the fix, so this is the answer key
    arriving through the package registry. The cell recorded n_network_calls=0 because the
    pattern only knew about curl/wget/gh/git, and the solve was credited to aracne.
    """
    transcript = "\n".join([
        _bash("1", "pip download seaborn==0.13.2 -d /tmp/sb --no-deps"),
        _result("1", "Saved /tmp/sb/seaborn-0.13.2-py3-none-any.whl\n"),
    ]) + "\n"
    stats, _ = toolstats.summarize(transcript, "mwaskom/seaborn")
    assert stats["n_network_calls"] == 1, "a pinned package download reaches outside the worktree"


def test_ordinary_environment_setup_is_not_a_network_signal():
    """The counter is only useful if it stays quiet for what nearly every cell does.

    Installing a requirements file or unpinned dependencies is how a cell gets a runnable
    environment; counting it would make n_network_calls fire everywhere and mean nothing.
    """
    for cmd in ("pip install -r requirements.txt",
                "npm ci",
                "pip install matplotlib pandas numpy",
                "cargo build --all-features"):
        transcript = _bash("1", cmd) + "\n" + _result("1", "ok\n") + "\n"
        stats, _ = toolstats.summarize(transcript, "org/repo")
        assert stats["n_network_calls"] == 0, f"{cmd!r} should not count as reaching outside"


def test_reaching_past_the_base_commit_is_counted():
    """The contamination that outranked every other one in this harness.

    A fixture built with a plain `git clone` carries every ref, so `git log --all` reaches the
    commits AFTER the task's base -- including the PR the task asks the agent to reproduce.
    Observed in hard9-20260902b: the agent ran `git log --all`, found
    a2a6c3a "feat: add BLP (Bell-LaPadula) model support and test (#1512)" for
    casbin__casbin-1512, and applied it with `git show <sha> | git apply -`. deny_answer_key
    never fires, because the answer never crosses the network.
    """
    transcript = "\n".join([
        _bash("1", "git log --all --oneline | head -20"),
        _result("1", "a2a6c3a feat: add BLP support (#1512)\n"),
        _bash("2", "git show a2a6c3a -- model/function.go | git apply -"),
        _result("2", ""),
    ]) + "\n"
    stats, _ = toolstats.summarize(transcript, "casbin/casbin")
    assert stats["n_future_history"] == 2, "both history reaches should be counted"


def test_ordinary_git_use_is_not_counted():
    """`git log` and `git diff` on the checked-out history are how anyone reads a repo."""
    for cmd in ("git log --oneline -8", "git status", "git diff --stat", "git show HEAD"):
        transcript = _bash("1", cmd) + "\n" + _result("1", "ok\n") + "\n"
        stats, _ = toolstats.summarize(transcript, "org/repo")
        assert stats["n_future_history"] == 0, f"{cmd!r} should not count"


# --- the guard event log ------------------------------------------------------
#
# n_intercepted used to be counted by matching `arac cmd --` in the transcript, and it
# reported zero for every run ever measured -- including runs where interception fired on
# most commands. The cause is structural: a PreToolUse hook substitutes the command through
# `hookSpecificOutput.updatedInput`, and the transcript records what the MODEL wrote.
# Confirmed by running a session against an intercepting fixture: the model typed
# `grep -rn NewMarkdownTable --include=*.go .`, the result was aracne's answer (it began at
# format/doc.go, which real grep does not match, and omitted the _test.go hit real grep
# returns first), and the transcript showed the typed command. So the guard records its own
# decisions instead.

def _write_log(tmp_path, *decisions):
    p = tmp_path / "guard.jsonl"
    p.write_text("".join(
        json.dumps({"t": "2026-09-03T00:00:00Z", "tool": "Bash",
                    "decision": d, "command": "grep -rn x ."}) + "\n"
        for d in decisions
    ))
    return p


def test_guard_counts_reads_the_log(tmp_path):
    p = _write_log(tmp_path, "rewrite", "rewrite", "passthrough", "deny", "nudge")
    got = toolstats.guard_counts(p)
    assert got["n_intercepted"] == 2
    # nudge is a PostToolUse event and is NOT a command the guard was offered to answer.
    assert got["n_guard_seen"] == 4
    assert got["n_guard_passthrough"] == 1
    assert got["n_guard_denials_logged"] == 1
    assert got["n_guard_nudges"] == 1


def test_guard_counts_absent_log_is_empty_not_zero(tmp_path):
    """A baseline cell runs no guard. Reporting 0 there would state that aracne answered
    nothing, when the truth is that aracne was never asked."""
    assert toolstats.guard_counts(tmp_path / "nope.jsonl") == {}
    assert toolstats.guard_counts(None) == {}


def test_guard_counts_survives_a_truncated_line(tmp_path):
    """The log is appended to by concurrent hook processes; a partial final line must not
    lose the counts already recorded."""
    p = tmp_path / "guard.jsonl"
    p.write_text(
        json.dumps({"decision": "rewrite"}) + "\n"
        + json.dumps({"decision": "rewrite"}) + "\n"
        + '{"decision": "rewr'
    )
    assert toolstats.guard_counts(p)["n_intercepted"] == 2


def test_guard_counts_ignores_unknown_decisions(tmp_path):
    """A newer aracne recording a decision kind this harness does not know must not be
    counted as an interception."""
    p = _write_log(tmp_path, "rewrite", "teleport")
    got = toolstats.guard_counts(p)
    assert got["n_intercepted"] == 1
    assert got["n_guard_seen"] == 1


def test_guard_counts_counts_warnings_shown(tmp_path):
    """Topology warnings are counted from the guard log, not the transcript: a hook returns
    them through additionalContext, which stream-json does not record."""
    p = tmp_path / "guard.jsonl"
    p.write_text(
        json.dumps({"decision": "passthrough", "tool": "Bash"}) + "\n"
        + json.dumps({"decision": "drift", "tool": "Bash", "warnings": 11,
                      "warning_kinds": {"signature_changed": 11}}) + "\n"
        + json.dumps({"decision": "drift", "tool": "Bash", "warnings": 2,
                      "warning_kinds": {"use_missing_node": 2}}) + "\n"
    )
    got = toolstats.guard_counts(p)
    assert got["n_warnings_shown"] == 13
    assert got["n_warning_reports"] == 2
    assert got["warning_kinds"] == {"signature_changed": 11, "use_missing_node": 2}
    # a drift report is not a command the guard was offered to answer
    assert got["n_guard_seen"] == 1

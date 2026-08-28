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

"""Per-run tool telemetry, folded out of a Claude Code `stream-json` transcript.

WHY THIS EXISTS. The `scale40` run reported a +14% context-token regression for the
aracne arm and could not say *why*, because `--output-format json` returns one summary
object and nothing about tool calls. Every diagnosis had to be reconstructed by hand from
the engine. These counters make the next run self-explaining: they say whether the agent
used the aracne MCP tools at all, how many bytes each tool returned, and how often a
lookup missed or came back ambiguous.

The transcript is NDJSON, one event per line. The events we care about:

  {"type":"assistant","message":{"content":[{"type":"tool_use","id":…,"name":…,"input":…}]}}
  {"type":"user",     "message":{"content":[{"type":"tool_result","tool_use_id":…,
                                             "content":…,"is_error":…}]}}
  {"type":"result", …,"permission_denials":[…]}          <- terminal summary

`summarize` returns (stats, reduced) where `reduced` is the same NDJSON with tool_result
BODIES replaced by their byte count. We only ever need the sizes, and a full 80-turn
transcript on a large repo is tens of MB of duplicated source text.
"""
from __future__ import annotations

import json

# Substrings that identify an aracne resolution failure in a tool result. These are the
# exact strings the engine emits — see internal/llm/languages/universaltools/universal_read.go
# and the per-language read_*.go tools.
_MISS_MARKERS = ("not found in topology", "does not exist")
_AMBIGUOUS_MARKER = "Multiple resources matching"
_GUARD_MARKER = "Blocked by aracne config"

MCP_PREFIX = "mcp__aracne__"


def _result_text(block: dict) -> str:
    """Flatten a tool_result `content` (str, or a list of {type:text,text:…} parts)."""
    content = block.get("content")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for part in content:
            if isinstance(part, dict) and isinstance(part.get("text"), str):
                parts.append(part["text"])
            elif isinstance(part, str):
                parts.append(part)
        return "".join(parts)
    return "" if content is None else json.dumps(content)


def _blocks(event: dict) -> list:
    content = (event.get("message") or {}).get("content")
    return content if isinstance(content, list) else []


def empty_stats() -> dict:
    """The zero row — used for non-Claude backends and for runs with no transcript, so
    every row carries the same keys and a missing transcript never looks like a zero."""
    return {
        "tool_calls": {},
        "tool_result_bytes": {},
        "tool_errors": {},
        "n_tool_calls": 0,
        "n_mcp_calls": 0,
        "n_native_calls": 0,
        "mcp_result_bytes": 0,
        "native_result_bytes": 0,
        "grep_result_bytes": 0,
        "read_result_bytes": 0,
        "n_id_misses": 0,
        "n_ambiguous": 0,
        "n_guard_denials": 0,
        "has_transcript": False,
    }


def summarize(stdout: str) -> tuple[dict, str]:
    """Fold a stream-json transcript into counters, and return a size-reduced transcript.

    Never raises: a malformed or truncated stream still yields whatever was parseable, so
    a crashed run does not also lose its telemetry.
    """
    stats = empty_stats()
    if not (stdout or "").strip():
        return stats, ""

    names: dict[str, str] = {}      # tool_use_id -> tool name
    reduced: list[str] = []
    saw_event = False

    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        saw_event = True
        etype = event.get("type")

        if etype == "assistant":
            for block in _blocks(event):
                if not isinstance(block, dict) or block.get("type") != "tool_use":
                    continue
                name = block.get("name") or "?"
                names[block.get("id")] = name
                stats["tool_calls"][name] = stats["tool_calls"].get(name, 0) + 1
                stats["n_tool_calls"] += 1
                if name.startswith(MCP_PREFIX):
                    stats["n_mcp_calls"] += 1
                else:
                    stats["n_native_calls"] += 1

        elif etype == "user":
            for block in _blocks(event):
                if not isinstance(block, dict) or block.get("type") != "tool_result":
                    continue
                name = names.get(block.get("tool_use_id"), "?")
                text = _result_text(block)
                nbytes = len(text.encode("utf-8", "replace"))
                stats["tool_result_bytes"][name] = \
                    stats["tool_result_bytes"].get(name, 0) + nbytes
                if name.startswith(MCP_PREFIX):
                    stats["mcp_result_bytes"] += nbytes
                else:
                    stats["native_result_bytes"] += nbytes
                # These two span BOTH arms deliberately — native `Read`/`Grep` are
                # counted alongside `mcp__aracne__read_*`/`grep`, so "bytes spent on
                # discovery" is directly comparable between baseline and aracne.
                bare = name.rsplit("__", 1)[-1].lower()
                if "grep" in bare:
                    stats["grep_result_bytes"] += nbytes
                elif bare.startswith("read"):
                    stats["read_result_bytes"] += nbytes
                if block.get("is_error"):
                    stats["tool_errors"][name] = stats["tool_errors"].get(name, 0) + 1
                if any(m in text for m in _MISS_MARKERS):
                    stats["n_id_misses"] += 1
                if _AMBIGUOUS_MARKER in text:
                    stats["n_ambiguous"] += 1
                if _GUARD_MARKER in text:
                    stats["n_guard_denials"] += 1
                # Drop the body: we needed only its size.
                block["content"] = f"<{nbytes} bytes elided by toolstats>"

        elif etype == "result":
            # The CLI reports permission-hook denials directly on the terminal event; it
            # is a superset of the in-band "Blocked by aracne config" text (a PreToolUse
            # deny never reaches the model as a tool_result at all), so prefer it.
            denials = event.get("permission_denials")
            if isinstance(denials, list) and denials:
                stats["n_guard_denials"] = max(stats["n_guard_denials"], len(denials))

        reduced.append(json.dumps(event, separators=(",", ":")))

    stats["has_transcript"] = saw_event
    return stats, "\n".join(reduced)


# The subset of counters that is flat enough to sit on a result row and be aggregated.
ROW_FIELDS = (
    "n_tool_calls", "n_mcp_calls", "n_native_calls",
    "mcp_result_bytes", "native_result_bytes",
    "grep_result_bytes", "read_result_bytes",
    "n_id_misses", "n_ambiguous", "n_guard_denials",
)


def row_fields(stats: dict) -> dict:
    """Project `stats` onto the flat scalar fields stored in runs.jsonl.

    The per-tool dicts stay out of the row: they are variable-width, they would bloat
    every line, and the transcript on disk still has them.
    """
    return {k: stats.get(k, 0) for k in ROW_FIELDS}

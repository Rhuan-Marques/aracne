"""Row-level metric accessors shared by the pooled (metrics.py) and paired (paired.py)
analyses.

WHY THIS EXISTS — the context-token trap. Claude Code splits the context an agent actually
consumed across THREE usage fields: `input_tokens` counts only the uncached prefix of a
turn, while `cache_creation_input_tokens` + `cache_read_input_tokens` (summed by the driver
into `cache_tokens`) carry the rest — in practice well over 99% of it. A run that read
1.8M tokens of repository reports `input_tokens: 70`. Averaging `input_tokens` alone
therefore measures rounding noise, not context, and any delta computed from it is
meaningless. CONTEXT tokens = input + cache is the honest denominator and is the PRIMARY
token endpoint of this benchmark.

`cache_creation` is kept visible separately (via mean_cache_tokens) because the aracne arm
legitimately pays extra creation cost on turn 1 for the CLAUDE.md contract — that is a real
cost of the shipped package and must not be hidden inside a single total.
"""
from __future__ import annotations


def _i(row: dict, key: str) -> int:
    return int(row.get(key) or 0)


def context_tokens(row: dict) -> int:
    """Everything the model read: uncached input + cache creation + cache reads."""
    return _i(row, "input_tokens") + _i(row, "cache_tokens")


def output_tokens(row: dict) -> int:
    return _i(row, "output_tokens")


def total_tokens(row: dict) -> int:
    """Context + output — the full token footprint of a run."""
    return context_tokens(row) + output_tokens(row)


def turns(row: dict) -> float:
    return float(row.get("num_turns") or 0)


def wall_s(row: dict) -> float:
    return float(row.get("duration_ms") or 0) / 1000.0


def cost_usd(row: dict) -> float:
    return float(row.get("cost_usd") or 0)


def tokens_per_turn(row: dict) -> float:
    """Context tokens divided by turns — the ONLY token endpoint that is not confounded
    by turn count.

    Context tokens are a sum over turns of (input + cache create + cache read), so a run
    that takes 16% more turns mechanically reads ~16% more context even if every single
    turn were identically sized. Reporting `context_tokens` and `turns` side by side
    therefore double-counts one effect. This ratio isolates "how much context did each
    turn cost", which is what a navigation tool actually claims to improve.
    """
    t = turns(row)
    return context_tokens(row) / t if t > 0 else 0.0


def has_metrics(row: dict) -> bool:
    """A row whose agent metrics are usable — i.e. the run did not hard-error.

    A GRADING timeout leaves the agent's own token/turn numbers intact, so `error` (not the
    outcome) is the right filter here.

    A row with neither turns nor context is also excluded even when `error` is unset: the
    harness can record a cell whose CLI exited without a usable result event, and averaging
    a zero into `mean_turns` understates every arm it lands in.
    """
    if row.get("error"):
        return False
    return bool(row.get("num_turns")) or context_tokens(row) > 0


def has_tokens(row: dict) -> bool:
    """Some backends (OpenCode) report no usage; a 0 would poison a token mean."""
    return has_metrics(row) and context_tokens(row) > 0


# Efficiency endpoints reported as paired ratios. (name, label, extractor, lower_is_better)
EFFICIENCY_METRICS = (
    ("context_tokens", "context tokens", context_tokens, True),
    ("output_tokens", "output tokens", output_tokens, True),
    ("total_tokens", "total tokens", total_tokens, True),
    ("turns", "turns", turns, True),
    ("tokens_per_turn", "context tokens / turn", tokens_per_turn, True),
    ("wall_s", "wall seconds", wall_s, True),
)

# Tool-telemetry endpoints (bench/toolstats.py). Reported as paired ratios ONLY when both
# arms recorded a transcript; `ratio_analysis` already drops non-positive pairs, which is
# the right behaviour here (a baseline run makes zero MCP calls by construction).
TELEMETRY_FIELDS = (
    ("n_tool_calls", "tool calls"),
    ("n_mcp_calls", "aracne MCP calls"),
    ("n_native_calls", "native tool calls"),
    ("mcp_result_bytes", "MCP result bytes"),
    ("native_result_bytes", "native result bytes"),
    ("grep_result_bytes", "grep result bytes"),
    ("read_result_bytes", "read result bytes"),
    ("n_id_misses", "resource-ID misses"),
    ("n_ambiguous", "ambiguous lookups"),
    ("n_guard_denials", "guard denials"),
)


def telemetry(row: dict, field: str) -> int:
    return _i(row, field)

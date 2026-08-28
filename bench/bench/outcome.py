"""The terminal outcomes of a benchmark run, and how a result row maps onto them.

A run is CORRECT or FAIL only when a grading harness actually judged its patch. Anything
that ran out of BUDGET before the agent or the grader could finish is a TIMEOUT: the agent
exceeding `timeout_s`, the agent exhausting `max_turns` mid-task, or the Docker grading
harness exceeding `grade_timeout_s` / going silent past `grade_stall_timeout_s`.
A timeout is NOT evidence that the patch was wrong, so it must never land in the fail
bucket and never in the success-rate denominator; it is counted and reported on its own.
It is also not a machinery failure, so it must never halt the rest of the matrix.
Everything else (setup failures, ungraded runs, instances the harness reported under
`error_ids`) stays UNKNOWN.

`success` keeps its original tri-state meaning (True / False / None) so existing consumers
are unaffected; `outcome` is the field to read when a timeout must be told apart from an
ordinary unknown. Rows carry `timeout_stage` ("agent" | "turns" | "grading" | None) to say
WHERE the budget ran out.
"""
from __future__ import annotations

CORRECT = "correct"
FAIL = "fail"
TIMEOUT = "timeout"
UNKNOWN = "unknown"

ALL = (CORRECT, FAIL, TIMEOUT, UNKNOWN)

# Stages that can run out of budget; recorded on the row as `timeout_stage`.
AGENT = "agent"        # agent exceeded the wall-clock cap (`timeout_s`)
TURNS = "turns"        # agent exhausted its turn cap (`max_turns`) with work still pending
GRADING = "grading"    # Docker grading harness timed out or stalled

# Stages where the AGENT never finished, so its patch is half-written and must not be graded.
AGENT_STAGES = (AGENT, TURNS)


def classify(row: dict) -> str:
    """Map one result row onto an outcome. Timeout wins over everything else."""
    if row.get("timeout_stage"):
        return TIMEOUT
    success = row.get("success")
    if success is True:
        return CORRECT
    if success is False:
        return FAIL
    # Rows written before `timeout_stage` existed recorded the timeout only in `error`.
    if str(row.get("error") or "").startswith(("agent timeout", "agent turn limit")):
        return TIMEOUT
    return UNKNOWN


def stamp(row: dict) -> dict:
    """Set row["outcome"] from the row's current state and return the row."""
    row["outcome"] = classify(row)
    return row


def is_timeout(row: dict) -> bool:
    return classify(row) == TIMEOUT


def tally(rows: list[dict]) -> dict:
    """Count rows per outcome; every key in ALL is always present."""
    counts = {name: 0 for name in ALL}
    for row in rows:
        counts[classify(row)] += 1
    return counts

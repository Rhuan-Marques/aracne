"""Aggregate per-run rows into per-(language, arm) and overall stats, plus aracne-vs-
baseline deltas.

Conventions (from the benchmark methodology):
  - success_rate is over GRADED runs only (success is not None, and never a timeout); a
    Wilson 95% interval is reported because samples are small.
  - TIMEOUTS are a third outcome, not a failure: a run whose agent or grading harness ran
    out of wall-clock is counted in n_timeout (split by stage) and kept OUT of the
    success-rate denominator entirely. See bench/bench/outcome.py.
  - token/turn/wall means are over runs that produced metrics (no hard error), so a
    crashed run doesn't poison the averages. `n_metric` reports that denominator.
  - CONTEXT tokens (uncached input + cache creation + cache reads, see rowmetrics.py) are
    the primary token endpoint. `mean_input_tokens` is retained only for continuity with
    older reports and must not be read as "how much the agent consumed": Claude Code puts
    >99% of consumed context in the cache fields, so it is nearly always a two-digit number.
  - turns is the robust speed proxy, wall-clock secondary.
  - the POOLED numbers here are descriptive only. Because both arms run the same task, the
    defensible comparison is the PAIRED one in paired.py, which is attached to the
    aggregate under "paired" — read that for any claim about aracne vs. baseline.
"""
from __future__ import annotations

import math
from collections import defaultdict

from . import fixtures, outcome, paired, rowmetrics, toolstats


def _wilson(k: int, n: int) -> tuple[float, float]:
    if n == 0:
        return (0.0, 0.0)
    z = 1.96
    p = k / n
    denom = 1 + z * z / n
    center = (p + z * z / (2 * n)) / denom
    half = (z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))) / denom
    return (max(0.0, center - half), min(1.0, center + half))


def _mean(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def _arm_stats(rows: list[dict]) -> dict:
    counts = outcome.tally(rows)
    timeouts = [r for r in rows if outcome.is_timeout(r)]
    # A timeout is never graded, so excluding it here is belt-and-braces: it keeps the
    # success rate honest even if some future path sets both success and timeout_stage.
    graded = [r for r in rows if r.get("success") is not None and not outcome.is_timeout(r)]
    # Timed-out AGENT runs report no usable tokens/turns; a grading timeout leaves the
    # agent's own metrics intact, so `error` (not the outcome) stays the right filter.
    metric = [r for r in rows if not r.get("error")]
    # Token means only over runs that actually reported tokens — the OpenCode backend may
    # not, and a 0 would otherwise poison the average. turns/wall come from all metric runs.
    tok = [r for r in metric if rowmetrics.has_tokens(r)]
    # Tool telemetry is present only for rows whose agent transcript was captured
    # (claude_code + stream=True). Averaging over ALL metric rows would silently dilute the
    # means with structural zeros from backends that report nothing.
    tooled = [r for r in metric if r.get("transcript_path")]
    solved = sum(1 for r in graded if r["success"])
    lo, hi = _wilson(solved, len(graded))
    ctx_tok = [rowmetrics.context_tokens(r) for r in tok]
    in_tok = [r["input_tokens"] for r in tok]
    cache_tok = [r.get("cache_tokens", 0) or 0 for r in tok]
    out_tok = [rowmetrics.output_tokens(r) for r in tok]
    return {
        "n_runs": len(rows),
        "n_graded": len(graded),
        "n_timeout": len(timeouts),
        "n_timeout_agent": sum(1 for r in timeouts if r.get("timeout_stage") == outcome.AGENT),
        "n_timeout_turns": sum(1 for r in timeouts if r.get("timeout_stage") == outcome.TURNS),
        "n_timeout_grading": sum(1 for r in timeouts if r.get("timeout_stage") == outcome.GRADING),
        "timeout_rate": (len(timeouts) / len(rows)) if rows else None,
        "outcomes": counts,
        "n_metric": len(metric),
        "n_tokens": len(tok),
        "solved": solved,
        "success_rate": (solved / len(graded)) if graded else None,
        "success_ci": [round(lo, 3), round(hi, 3)] if graded else None,
        "mean_context_tokens": round(_mean(ctx_tok)) if tok else None,
        "mean_cache_tokens": round(_mean(cache_tok)) if tok else None,
        "mean_input_tokens": round(_mean(in_tok)) if tok else None,
        "mean_output_tokens": round(_mean(out_tok)) if tok else None,
        "mean_total_tokens": round(_mean([c + o for c, o in zip(ctx_tok, out_tok)])) if tok else None,
        "mean_turns": round(_mean([r["num_turns"] for r in metric]), 1),
        "mean_wall_s": round(_mean([r["duration_ms"] / 1000 for r in metric]), 1),
        "mean_cost_usd": round(_mean([r["cost_usd"] for r in metric]), 4),
        "mean_scan_s": round(_mean([r["scan_time_s"] for r in metric]), 1),
        **_tool_stats(tooled),
        **_localization_stats(metric),
    }


# Localization (SWE-Atlas, bench/localization.py) and the host rubric judge. Each mean is over the
# rows that carry the field, and its denominator is reported: a task aracne cannot index has no
# declaration score, and averaging it in as a zero would be a claim the scorer never made.
LOC_FIELDS = ("loc_file_recall", "loc_file_precision", "loc_file_f1", "loc_decl_recall",
              "loc_decl_precision", "loc_decl_f1", "rubric_agg_score",
              "nav_turns_to_first_gold_touch", "nav_tokens_to_first_gold_touch",
              "nav_turns_to_first_gold_edit", "nav_tokens_to_first_gold_edit",
              "nav_gold_files_named_before_edit")


def _localization_stats(rows: list[dict]) -> dict:
    out: dict = {}
    for field in LOC_FIELDS:
        vals = [r[field] for r in rows if isinstance(r.get(field), (int, float))]
        out[f"mean_{field}"] = round(_mean(vals), 4) if vals else None
        out[f"n_{field}"] = len(vals)
    return out


def _tool_stats(tooled: list[dict]) -> dict:
    """Per-tool telemetry means over runs that actually captured a transcript.

    `n_tool_runs` is the denominator and is reported alongside, so a zero mean caused by
    "no transcripts" is never mistaken for "the agent made no calls".
    """
    out = {"n_tool_runs": len(tooled)}
    for field in toolstats.ROW_FIELDS:
        # n_tool_calls -> mean_tool_calls ; mcp_result_bytes -> mean_mcp_result_bytes
        key = "mean_" + (field[2:] if field.startswith("n_") else field)
        vals = [rowmetrics.telemetry(r, field) for r in tooled]
        out[key] = round(_mean(vals), 1) if tooled else None
    return out


def _pct_delta(aracne, baseline):
    if aracne is None or not baseline:
        return None
    return round((aracne - baseline) / baseline * 100, 1)


def _delta(a: dict | None, b: dict | None) -> dict | None:
    """Aracne (a) minus baseline (b)."""
    if not a or not b:
        return None
    d = {
        "solved_abs": a["solved"] - b["solved"],
        "timeout_abs": a["n_timeout"] - b["n_timeout"],
        "context_tokens_pct": _pct_delta(a["mean_context_tokens"], b["mean_context_tokens"]),
        "input_tokens_pct": _pct_delta(a["mean_input_tokens"], b["mean_input_tokens"]),
        "output_tokens_pct": _pct_delta(a["mean_output_tokens"], b["mean_output_tokens"]),
        "total_tokens_pct": _pct_delta(a["mean_total_tokens"], b["mean_total_tokens"]),
        "turns_pct": _pct_delta(a["mean_turns"], b["mean_turns"]),
        "wall_pct": _pct_delta(a["mean_wall_s"], b["mean_wall_s"]),
    }
    if a["success_rate"] is not None and b["success_rate"] is not None:
        d["success_rate_abs"] = round(a["success_rate"] - b["success_rate"], 3)
    # Localization scores are rates already, so the delta is absolute (points), not a percent.
    for field in LOC_FIELDS:
        av, bv = a.get(f"mean_{field}"), b.get(f"mean_{field}")
        if av is None or bv is None:
            continue
        if field.startswith("nav_turns") or field.startswith("nav_tokens"):
            d[f"{field}_pct"] = _pct_delta(av, bv)
        else:
            d[f"{field}_abs"] = round(av - bv, 4)
    return d


def prep_summary(tasks: list, fixtures_root) -> list[dict]:
    """One row per UNIQUE fixture (by key) reading its meta.json: key, coverage, scan_time.

    Skips tasks whose fixture has no meta.json (not yet frozen). Used to surface the
    one-time preparation cost/quality alongside the A/B results.
    """
    out: list[dict] = []
    seen: set[str] = set()
    for t in tasks:
        key = fixtures.fixture_key(t)
        if key in seen:
            continue
        seen.add(key)
        meta = fixtures.read_meta(t, fixtures_root)
        if not meta:
            continue
        out.append({
            "key": key,
            "coverage": meta.get("coverage"),
            "described": meta.get("described"),
            "total": meta.get("total"),
            "scan_time_s": meta.get("scan_time_s"),
        })
    return out


def aggregate(rows: list[dict], cfg: dict, prep: list[dict] | None = None) -> dict:
    # Arms present in the ROWS win over the config's arm list, which records only the arms
    # this run MEASURED. `--arms aracne --baseline-from <prior>` produces a complete A/B
    # whose config lists one arm; keying the pooled tables off the config alone silently
    # drops the imported arm from every table and reports `delta: null`, making a finished
    # comparison read as a single-arm run. (Same rule cmd_rescore applies to languages.)
    seen = dict.fromkeys(r["arm"] for r in rows)
    arms = list(cfg["arms"]) + [a for a in seen if a not in cfg["arms"]]
    languages = cfg["languages"]

    per_lang: dict[str, dict] = {}
    by_lang_arm: dict[tuple, list] = defaultdict(list)
    by_arm: dict[str, list] = defaultdict(list)
    for r in rows:
        by_lang_arm[(r["language"], r["arm"])].append(r)
        by_arm[r["arm"]].append(r)

    for lang in languages:
        arms_stats = {arm: _arm_stats(by_lang_arm.get((lang, arm), [])) for arm in arms}
        per_lang[lang] = {
            "arms": arms_stats,
            "delta": _delta(arms_stats.get("aracne"), arms_stats.get("baseline")),
            "delta_by_arm": {a: _delta(arms_stats.get(a), arms_stats.get("baseline"))
                             for a in arms if a != "baseline"},
        }

    overall_arms = {arm: _arm_stats(by_arm.get(arm, [])) for arm in arms}
    agg = {
        # `run_parallel` rides along because it qualifies the wall-clock column: >1 means
        # the runs contended for one machine. Tolerated as missing on older configs.
        "config": {k: cfg.get(k) for k in ("samples", "languages", "seeds", "sample_seed",
                                          "arms", "run_harness", "model", "max_turns",
                                          "run_parallel")},
        "per_language": per_lang,
        "overall": {
            "arms": overall_arms,
            "delta": _delta(overall_arms.get("aracne"), overall_arms.get("baseline")),
            "delta_by_arm": {a: _delta(overall_arms.get(a), overall_arms.get("baseline"))
                             for a in arms if a != "baseline"},
        },
    }
    # The paired analysis is the one that supports a claim; attach it alongside the pooled
    # descriptives so every consumer (console, HTML, LLM analysis) can reach it.
    margin = cfg.get("ni_margin", paired.DEFAULT_MARGIN)
    # One paired block per treatment arm, all against the same control. `paired` stays the
    # primary arm's block so every existing consumer keeps working; `paired_by_arm` is what a
    # three-arm run needs, and without it a third arm runs, lands in runs.jsonl, shows up in
    # the pooled tables -- and is then silently absent from every number that supports a claim.
    treatments = paired.treatment_arms(rows) or [paired.ARACNE]
    primary = paired.ARACNE if paired.ARACNE in treatments else treatments[0]
    agg["paired"] = paired.analyse(rows, margin, primary)
    agg["paired_by_language"] = paired.analyse_by_language(rows, languages, margin, primary)
    agg["paired_by_arm"] = {t: paired.analyse(rows, margin, t) for t in treatments}
    agg["paired_by_arm_by_language"] = {
        t: paired.analyse_by_language(rows, languages, margin, t) for t in treatments
    }
    # A/B between two treatment arms. `paired_by_arm` already reports each against the control,
    # but reading a difference off two overlapping intervals is not the same test: when both
    # arms ran here, on the same tasks, they pair directly and the cluster bootstrap sees the
    # per-task difference instead of the difference of two noisy estimates.
    ab = cfg.get("ab_arms") or []
    if len(ab) == 2 and all(a in {r.get("arm") for r in rows} for a in ab):
        agg["paired_ab"] = paired.analyse(rows, margin, treatment=ab[1], control=ab[0])
    for lang, block in per_lang.items():
        block["paired"] = agg["paired_by_language"].get(lang)
    if prep is not None:
        agg["preparation"] = prep
    return agg

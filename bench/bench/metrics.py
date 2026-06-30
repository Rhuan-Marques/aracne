"""Aggregate per-run rows into per-(language, arm) and overall stats, plus aracne-vs-
baseline deltas.

Conventions (from the benchmark methodology):
  - success_rate is over GRADED runs only (success is not None); a Wilson 95% interval
    is reported because samples are small.
  - token/turn/wall means are over runs that produced metrics (no hard error), so a
    crashed run doesn't poison the averages. `n_metric` reports that denominator.
  - tokens are split input/output; turns is the robust speed proxy, wall-clock secondary.
"""
from __future__ import annotations

import math
from collections import defaultdict

from . import fixtures


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
    graded = [r for r in rows if r.get("success") is not None]
    metric = [r for r in rows if not r.get("error")]
    # Token means only over runs that actually reported tokens — the OpenCode backend may
    # not, and a 0 would otherwise poison the average. turns/wall come from all metric runs.
    tok = [r for r in metric if r.get("input_tokens", 0) > 0]
    solved = sum(1 for r in graded if r["success"])
    lo, hi = _wilson(solved, len(graded))
    in_tok = [r["input_tokens"] for r in tok]
    out_tok = [r["output_tokens"] for r in tok]
    return {
        "n_runs": len(rows),
        "n_graded": len(graded),
        "n_metric": len(metric),
        "n_tokens": len(tok),
        "solved": solved,
        "success_rate": (solved / len(graded)) if graded else None,
        "success_ci": [round(lo, 3), round(hi, 3)] if graded else None,
        "mean_input_tokens": round(_mean(in_tok)) if tok else None,
        "mean_output_tokens": round(_mean(out_tok)) if tok else None,
        "mean_total_tokens": round(_mean([a + b for a, b in zip(in_tok, out_tok)])) if tok else None,
        "mean_turns": round(_mean([r["num_turns"] for r in metric]), 1),
        "mean_wall_s": round(_mean([r["duration_ms"] / 1000 for r in metric]), 1),
        "mean_cost_usd": round(_mean([r["cost_usd"] for r in metric]), 4),
        "mean_scan_s": round(_mean([r["scan_time_s"] for r in metric]), 1),
    }


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
        "input_tokens_pct": _pct_delta(a["mean_input_tokens"], b["mean_input_tokens"]),
        "output_tokens_pct": _pct_delta(a["mean_output_tokens"], b["mean_output_tokens"]),
        "total_tokens_pct": _pct_delta(a["mean_total_tokens"], b["mean_total_tokens"]),
        "turns_pct": _pct_delta(a["mean_turns"], b["mean_turns"]),
        "wall_pct": _pct_delta(a["mean_wall_s"], b["mean_wall_s"]),
    }
    if a["success_rate"] is not None and b["success_rate"] is not None:
        d["success_rate_abs"] = round(a["success_rate"] - b["success_rate"], 3)
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
    arms = cfg["arms"]
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
        }

    overall_arms = {arm: _arm_stats(by_arm.get(arm, [])) for arm in arms}
    agg = {
        "config": {k: cfg[k] for k in ("samples", "languages", "seeds", "sample_seed",
                                       "arms", "run_harness", "model", "max_turns")},
        "per_language": per_lang,
        "overall": {
            "arms": overall_arms,
            "delta": _delta(overall_arms.get("aracne"), overall_arms.get("baseline")),
        },
    }
    if prep is not None:
        agg["preparation"] = prep
    return agg

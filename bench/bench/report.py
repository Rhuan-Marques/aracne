"""Persist and print benchmark results.

Writes results.json (full aggregate) and summary.csv (flat per-language/arm rows), and
prints a compact console table with aracne-vs-baseline deltas.

The `ctx` column is CONTEXT tokens (input + cache creation + cache reads) — the tokens the
agent actually consumed. The old `in` column showed `input_tokens` alone, which excludes the
cache fields where Claude Code reports >99% of context, and was therefore a two-digit number
unrelated to what aracne changes.

The PAIRED block below the table is the part that supports a claim. The per-arm rows are
pooled descriptives; the paired numbers difference each task against itself, so task
difficulty cancels. See bench/bench/paired.py.

The `t/o` column counts TIMEOUTS — runs whose agent or grading harness ran out of
wall-clock. They are deliberately not folded into solved/graded: a timeout is neither a
correct nor a failed answer, so it is reported on its own line instead of quietly
depressing a success rate.
"""
from __future__ import annotations

import csv
import json
from pathlib import Path

from .rowmetrics import EFFICIENCY_METRICS


def write_results(agg: dict, rows: list[dict], out_dir: Path) -> None:
    (out_dir / "results.json").write_text(json.dumps(agg, indent=2), encoding="utf-8")
    _write_summary_csv(agg, out_dir / "summary.csv")


def _write_summary_csv(agg: dict, path: Path) -> None:
    cols = ["scope", "arm", "n_runs", "n_graded", "n_timeout", "n_timeout_agent",
            "n_timeout_grading", "solved", "success_rate",
            "mean_context_tokens", "mean_cache_tokens",
            "mean_input_tokens", "mean_output_tokens", "mean_total_tokens",
            "mean_turns", "mean_wall_s", "mean_cost_usd", "mean_scan_s"]
    with path.open("w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(cols)
        for lang, block in agg["per_language"].items():
            for arm, st in block["arms"].items():
                w.writerow([lang, arm] + [st.get(c) for c in cols[2:]])
        for arm, st in agg["overall"]["arms"].items():
            w.writerow(["OVERALL", arm] + [st.get(c) for c in cols[2:]])


def _fmt_k(n) -> str:
    if n is None:
        return "-"
    return f"{n / 1000:.1f}k" if n >= 1000 else str(n)


def _fmt_rate(st: dict) -> str:
    if st["success_rate"] is None:
        return f"{st['solved']}/{st['n_graded']}?"
    return f"{st['solved']}/{st['n_graded']}"


def _fmt_timeout(st: dict) -> str:
    """Timeouts are shown apart from solved/graded — they are neither correct nor fail."""
    n = st.get("n_timeout") or 0
    return "-" if not n else str(n)


def _print_delta(d: dict | None) -> None:
    if not d:
        print(f"  {'Δ':<9} (need both arms graded)")
        return
    sr = d.get("success_rate_abs")
    sr_s = f"{sr:+.0%}".replace("%", "pp") if sr is not None else f"{d['solved_abs']:+d} solved"
    print(f"  {'Δ':<9} {sr_s:>8}"
          f"  ctx {_p(d['context_tokens_pct'])}  out {_p(d['output_tokens_pct'])}"
          f"  turns {_p(d['turns_pct'])}  wall {_p(d['wall_pct'])}")


def _p(v) -> str:
    return "-" if v is None else f"{v:+.0f}%"


def _print_timeouts(agg: dict) -> None:
    """Spell out the timeout tally when there is one, so it cannot be mistaken for a fail."""
    arms = agg.get("overall", {}).get("arms", {})
    total = sum((st.get("n_timeout") or 0) for st in arms.values())
    if not total:
        return
    print("\nTIMEOUTS (counted separately — neither correct nor fail, excluded from solved/graded)")
    for arm, st in arms.items():
        n = st.get("n_timeout") or 0
        if not n:
            continue
        parts = []
        if st.get("n_timeout_agent"):
            parts.append(f"{st['n_timeout_agent']} agent")
        if st.get("n_timeout_grading"):
            parts.append(f"{st['n_timeout_grading']} grading")
        print(f"  {arm:<9} {n} of {st.get('n_runs')} run(s)"
              + (f"  ({', '.join(parts)})" if parts else ""))


def _fmt_p(p) -> str:
    """A p-value at benchmark scale is coarse; print it that way rather than to 4dp."""
    if p is None:
        return "n/a"
    return "<0.001" if p < 0.001 else f"{p:.3f}"


def _print_paired(agg: dict) -> None:
    """The paired verdict — the only block here that supports a claim.

    Pooled per-arm means cannot separate "aracne helped" from "aracne drew easier tasks".
    These numbers difference every task against ITSELF, and bootstrap over REPOSITORIES,
    so `effective_n` (repos), not the run count, is the honest sample size.
    """
    pr = agg.get("paired")
    if not pr or not pr.get("n_pairs"):
        return
    print("\n" + "=" * 72)
    print("PAIRED ANALYSIS  (within-task; the claim-bearing numbers)")
    print("=" * 72)
    print(f"  {pr['n_pairs']} matched pair(s) over {pr['effective_n']} repo(s) "
          f"= effective N; {pr['n_unpaired']} unpaired task(s) dropped.")

    s = pr["solve"]
    print("\n  SOLVE RATE — McNemar exact on discordant pairs")
    print(f"    both {s['both_solved']}  aracne-only {s['aracne_only']}  "
          f"baseline-only {s['baseline_only']}  neither {s['neither_solved']}"
          f"   (graded pairs: {s['n_pairs']})")
    print(f"    discordant {s['n_discordant']}  p = {_fmt_p(s['mcnemar_p'])}")
    if s["rate_diff"] is not None:
        ci = s["rate_diff_ci"]
        ci_s = f"[{ci[0]:+.0%}, {ci[1]:+.0%}]".replace("%", "pp") if ci else "[n/a]"
        print(f"    paired rate diff {s['rate_diff']:+.0%}".replace("%", "pp")
              + f"  95% CI {ci_s}")
    verdict = ("non-inferior" if s["noninferior"]
               else f"NOT established (need CI lower bound > -{s['margin']:.0%})")
    print(f"    non-inferiority at {s['margin']:.0%} margin: {verdict}")

    print("\n  EFFICIENCY — geometric mean of per-task aracne/baseline ratios")
    print(f"    {'metric':<16} {'ratio':>7} {'change':>9} {'95% CI':>22}  sig")
    for name, label, _extract, _lower in EFFICIENCY_METRICS:
        e = pr["efficiency"].get(name) or {}
        if e.get("ratio") is None:
            print(f"    {label:<16} {'-':>7} {'-':>9} {'-':>22}")
            continue
        ci = e["pct_change_ci"]
        ci_s = f"[{ci[0]:+.1f}%, {ci[1]:+.1f}%]" if ci else "-"
        print(f"    {label:<16} {e['ratio']:>7.2f} {e['pct_change']:>+8.1f}% {ci_s:>22}"
              f"  {'yes' if e['significant'] else 'no'}")
    print("\n  (sig = the ratio's 95% CI excludes 1.0. A wide CI means too few REPOS,")
    print("   not too few runs — add distinct repositories, not seeds, to tighten it.)")
    _print_size(pr.get("by_size"))


def _print_size(sz: dict | None) -> None:
    """Context savings against repo size — the claim a single pooled percentage can't make.

    aracne should win MORE on big codebases, where reading files stops being viable. If that
    is true, this table trends downward and `slope_r` is negative; if it is not true, better
    to see it here than to average it away.
    """
    if not sz:
        return
    if not sz.get("available"):
        print(f"\n  BY REPO SIZE: unavailable ({sz.get('reason')})")
        return
    print("\n  CONTEXT TOKENS BY REPO SIZE (describable topology nodes)")
    print(f"    {'bucket':<10} {'nodes':>14} {'pairs':>6} {'repos':>6} {'ratio':>7} {'95% CI':>22}")
    for name, b in sz["buckets"].items():
        lo, hi = b["range"]
        rng = f"{lo}-{hi}" if hi else f"{lo}+"
        if b.get("ratio") is None:
            print(f"    {name:<10} {rng:>14} {b.get('n_pairs', 0):>6} {'-':>6} {'-':>7} {'-':>22}")
            continue
        ci = b.get("pct_change_ci")
        ci_s = f"[{ci[0]:+.1f}%, {ci[1]:+.1f}%]" if ci else "-"
        print(f"    {name:<10} {rng:>14} {b['n_pairs']:>6} {b['n_clusters']:>6} "
              f"{b['ratio']:>7.2f} {ci_s:>22}")
    r, ci = sz.get("slope_r"), sz.get("slope_ci")
    if r is None:
        print("    slope: not estimable (needs >= 3 sized pairs)")
    else:
        ci_s = f" 95% CI [{ci[0]:+.2f}, {ci[1]:+.2f}]" if ci else ""
        print(f"    slope: r = {r:+.2f}{ci_s}  "
              f"(negative => savings GROW with repo size: {sz['grows_with_size']})")


def print_table(agg: dict) -> None:
    hdr = (f"{'arm':<9} {'solved':>8} {'t/o':>4} {'ctx':>8} {'out':>7} "
           f"{'turns':>6} {'wall_s':>7} {'$':>7}")
    cfgd = agg["config"]
    print("\n" + "=" * 72)
    print("aracne benchmark results  (harness=%s, model=%s, samples=%s/lang, seeds=%s)" % (
        cfgd.get("run_harness", "?"), cfgd["model"], cfgd["samples"], cfgd["seeds"]))
    print("=" * 72)

    for lang, block in agg["per_language"].items():
        print(f"\n[{lang}]")
        print("  " + hdr)
        for arm, st in block["arms"].items():
            print(f"  {arm:<9} {_fmt_rate(st):>8} {_fmt_timeout(st):>4} "
                  f"{_fmt_k(st['mean_context_tokens']):>8} "
                  f"{_fmt_k(st['mean_output_tokens']):>7} {st['mean_turns']:>6} "
                  f"{st['mean_wall_s']:>7} {st['mean_cost_usd']:>7}")
        _print_delta(block["delta"])

    print("\n[OVERALL]")
    print("  " + hdr)
    for arm, st in agg["overall"]["arms"].items():
        print(f"  {arm:<9} {_fmt_rate(st):>8} {_fmt_timeout(st):>4} "
              f"{_fmt_k(st['mean_context_tokens']):>8} "
              f"{_fmt_k(st['mean_output_tokens']):>7} {st['mean_turns']:>6} "
              f"{st['mean_wall_s']:>7} {st['mean_cost_usd']:>7}")
    _print_delta(agg["overall"]["delta"])
    print("\n(Δ shown as aracne − baseline; token/turn/wall deltas are percentages.)")
    par = int(cfgd.get("run_parallel") or 1)
    if par > 1:
        print(f"(wall_s was measured with {par} runs in flight at once — it reflects machine "
              "contention, not agent speed. ctx/out/turns are unaffected.)")

    _print_timeouts(agg)
    _print_paired(agg)

    if any(st.get("n_tokens", 0) < st.get("n_metric", 0)
           for st in agg["overall"]["arms"].values()):
        print("(some runs reported no token usage — typically the OpenCode backend; "
              "turns/wall are still comparable.)")

    prep = agg.get("preparation")
    if prep:
        print("\nPREPARE (one-time per repo)")
        print(f"  {'fixture':<48} {'coverage':>9} {'scan_s':>8}")
        for p in prep:
            cov = p.get("coverage")
            cov_s = "-" if cov is None else f"{cov:.0%}"
            scan = p.get("scan_time_s")
            scan_s = "-" if scan is None else f"{scan}"
            print(f"  {str(p.get('key', '')):<48} {cov_s:>9} {scan_s:>8}")

"""Persist and print benchmark results.

Writes results.json (full aggregate) and summary.csv (flat per-language/arm rows), and
prints a compact console table with aracne-vs-baseline deltas.
"""
from __future__ import annotations

import csv
import json
from pathlib import Path


def write_results(agg: dict, rows: list[dict], out_dir: Path) -> None:
    (out_dir / "results.json").write_text(json.dumps(agg, indent=2), encoding="utf-8")
    _write_summary_csv(agg, out_dir / "summary.csv")


def _write_summary_csv(agg: dict, path: Path) -> None:
    cols = ["scope", "arm", "n_runs", "n_graded", "solved", "success_rate",
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


def _print_delta(d: dict | None) -> None:
    if not d:
        print(f"  {'Δ':<9} (need both arms graded)")
        return
    sr = d.get("success_rate_abs")
    sr_s = f"{sr:+.0%}".replace("%", "pp") if sr is not None else f"{d['solved_abs']:+d} solved"
    print(f"  {'Δ':<9} {sr_s:>8}  in {_p(d['input_tokens_pct'])}  out {_p(d['output_tokens_pct'])}"
          f"  tot {_p(d['total_tokens_pct'])}  turns {_p(d['turns_pct'])}  wall {_p(d['wall_pct'])}")


def _p(v) -> str:
    return "-" if v is None else f"{v:+.0f}%"


def print_table(agg: dict) -> None:
    hdr = f"{'arm':<9} {'solved':>8} {'in':>7} {'out':>7} {'turns':>6} {'wall_s':>7} {'$':>7}"
    cfgd = agg["config"]
    print("\n" + "=" * 72)
    print("aracne benchmark results  (harness=%s, model=%s, samples=%s/lang, seeds=%s)" % (
        cfgd.get("run_harness", "?"), cfgd["model"], cfgd["samples"], cfgd["seeds"]))
    print("=" * 72)

    for lang, block in agg["per_language"].items():
        print(f"\n[{lang}]")
        print("  " + hdr)
        for arm, st in block["arms"].items():
            print(f"  {arm:<9} {_fmt_rate(st):>8} {_fmt_k(st['mean_input_tokens']):>7} "
                  f"{_fmt_k(st['mean_output_tokens']):>7} {st['mean_turns']:>6} "
                  f"{st['mean_wall_s']:>7} {st['mean_cost_usd']:>7}")
        _print_delta(block["delta"])

    print("\n[OVERALL]")
    print("  " + hdr)
    for arm, st in agg["overall"]["arms"].items():
        print(f"  {arm:<9} {_fmt_rate(st):>8} {_fmt_k(st['mean_input_tokens']):>7} "
              f"{_fmt_k(st['mean_output_tokens']):>7} {st['mean_turns']:>6} "
              f"{st['mean_wall_s']:>7} {st['mean_cost_usd']:>7}")
    _print_delta(agg["overall"]["delta"])
    print("\n(Δ shown as aracne − baseline; token/turn/wall deltas are percentages.)")

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

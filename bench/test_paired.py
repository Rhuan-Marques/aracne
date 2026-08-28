#!/usr/bin/env python3
"""Regression tests for the CONTEXT-token accounting and the PAIRED analysis.

Two rules are locked down here, both of which silently produced wrong headline numbers
before:

  1. Context tokens = input + cache. Claude Code reports >99% of consumed context in the
     cache fields, so averaging `input_tokens` alone measures nothing. A run that read
     1.8M tokens must not be summarised as having read 70.

  2. The comparison is PAIRED and CLUSTERED. Both arms run the same task, so statistics
     are computed on per-task differences; and the bootstrap resamples REPOSITORIES,
     because tasks from one repo are not independent observations.

No pytest, no network, no Docker, no agent.

    python3 bench/test_paired.py
"""
from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import metrics, paired, rowmetrics, sources  # noqa: E402

FAILURES: list[str] = []


def check(name: str, cond: bool, detail: str = "") -> None:
    print(f"  {'PASS' if cond else 'FAIL'}  {name}" + (f"  ({detail})" if detail and not cond else ""))
    if not cond:
        FAILURES.append(name)


def row(instance, arm, *, ctx_in=10, cache=0, out=100, turns=10, ms=1000,
        success=None, seed="0", org="acme", repo="widget", lang="go", error=None,
        timeout_stage=None):
    return {
        "instance_id": instance, "arm": arm, "seed": seed, "language": lang,
        "org": org, "repo": repo, "input_tokens": ctx_in, "cache_tokens": cache,
        "output_tokens": out, "num_turns": turns, "duration_ms": ms,
        "cost_usd": 0.0, "scan_time_s": 0.0, "success": success,
        "error": error, "timeout_stage": timeout_stage,
    }


# --------------------------------------------------------------- context tokens
def test_context_tokens():
    print("\ncontext tokens (input + cache)")
    real = {"input_tokens": 70, "cache_tokens": 1826759, "output_tokens": 22053}
    check("context = input + cache", rowmetrics.context_tokens(real) == 1826829)
    check("total = context + output", rowmetrics.total_tokens(real) == 1848882)
    check("context dwarfs bare input_tokens",
          rowmetrics.context_tokens(real) > 1000 * real["input_tokens"])

    # The aggregate must report the same thing the accessor does.
    rows = [row("a", "baseline", ctx_in=70, cache=1826759, success=True),
            row("a", "aracne", ctx_in=70, cache=900000, success=True)]
    agg = metrics.aggregate(rows, {"arms": ["baseline", "aracne"], "languages": ["go"],
                                   "samples": 1, "seeds": 1, "sample_seed": 0,
                                   "run_harness": "claude_code", "model": "haiku",
                                   "max_turns": 30})
    ov = agg["overall"]["arms"]
    check("mean_context_tokens aggregated", ov["baseline"]["mean_context_tokens"] == 1826829,
          str(ov["baseline"]["mean_context_tokens"]))
    check("cache reported separately", ov["baseline"]["mean_cache_tokens"] == 1826759)
    check("context delta is not the input delta",
          agg["overall"]["delta"]["context_tokens_pct"] < -40,
          str(agg["overall"]["delta"]["context_tokens_pct"]))


# ---------------------------------------------------------------------- pairing
def test_pairing():
    print("\npairing and clustering")
    rows = [row("a", "baseline"), row("a", "aracne"),
            row("b", "baseline"), row("b", "aracne"),
            row("c", "baseline")]  # unpaired: aracne arm missing
    res = paired.analyse(rows)
    check("only complete pairs are used", res["n_pairs"] == 2, str(res["n_pairs"]))
    check("unpaired tasks are counted, not hidden", res["n_unpaired"] == 1)

    # Two tasks from ONE repo must be one cluster: effective N is repos, not runs.
    check("same repo collapses to one cluster", res["effective_n"] == 1, str(res["clusters"]))

    multi = [row("a", "baseline", repo="one"), row("a", "aracne", repo="one"),
             row("b", "baseline", repo="two"), row("b", "aracne", repo="two")]
    check("distinct repos are distinct clusters", paired.analyse(multi)["effective_n"] == 2)

    # SWE-bench rows carry no org/repo; the instance_id prefix must still cluster them.
    check("cluster falls back to instance_id prefix",
          paired.cluster_id({"instance_id": "pallets__flask-4045"}) == "pallets__flask")


# ------------------------------------------------------------------- solve rate
def test_mcnemar():
    print("\nMcNemar (solve rate)")
    # Concordant pairs carry no information; only discordant ones move the p-value.
    rows = []
    for i in range(20):  # 20 tasks, all solved by both arms
        rows += [row(f"t{i}", "baseline", success=True, repo=f"r{i}"),
                 row(f"t{i}", "aracne", success=True, repo=f"r{i}")]
    s = paired.analyse(rows)["solve"]
    check("all-concordant => no discordant pairs", s["n_discordant"] == 0)
    check("all-concordant => p is undefined, not 0", s["mcnemar_p"] is None)
    check("all-concordant => zero rate difference", s["rate_diff"] == 0.0)

    # A clean sweep for aracne on 8 discordant pairs is significant (2 * 0.5**8 = 0.0078).
    rows = []
    for i in range(8):
        rows += [row(f"t{i}", "baseline", success=False, repo=f"r{i}"),
                 row(f"t{i}", "aracne", success=True, repo=f"r{i}")]
    s = paired.analyse(rows)["solve"]
    check("sweep counts aracne-only wins", s["aracne_only"] == 8 and s["baseline_only"] == 0)
    check("sweep is significant", s["mcnemar_p"] is not None and s["mcnemar_p"] < 0.01,
          str(s["mcnemar_p"]))
    check("sweep is non-inferior", s["noninferior"] is True)

    # A 1-1 split is maximally uninformative.
    rows = [row("t0", "baseline", success=False, repo="r0"), row("t0", "aracne", success=True, repo="r0"),
            row("t1", "baseline", success=True, repo="r1"), row("t1", "aracne", success=False, repo="r1")]
    s = paired.analyse(rows)["solve"]
    check("1-1 split gives p = 1.0", s["mcnemar_p"] == 1.0, str(s["mcnemar_p"]))

    # A timeout is censored: it must never enter the solve analysis as a fail.
    rows = [row("t0", "baseline", success=True, repo="r0"),
            row("t0", "aracne", success=None, timeout_stage="grading", repo="r0")]
    s = paired.analyse(rows)["solve"]
    check("timed-out pair is excluded from solve", s["n_pairs"] == 0, str(s["n_pairs"]))


# ------------------------------------------------------------------- efficiency
def test_ratios():
    print("\nefficiency ratios")
    # Every task halves => ratio exactly 0.5, regardless of the tasks' absolute sizes.
    rows = []
    for i, size in enumerate([1_000, 10_000, 1_000_000]):
        # ctx_in=0 so the ratio is exactly 0.5 and the assertion is not softened by the
        # default handful of uncached tokens.
        rows += [row(f"t{i}", "baseline", ctx_in=0, cache=size, repo=f"r{i}"),
                 row(f"t{i}", "aracne", ctx_in=0, cache=size // 2, repo=f"r{i}")]
    e = paired.analyse(rows)["efficiency"]["context_tokens"]
    check("consistent halving => ratio 0.5", abs(e["ratio"] - 0.5) < 1e-6, str(e["ratio"]))
    check("reported as -50%", abs(e["pct_change"] + 50) < 0.05, str(e["pct_change"]))

    # The headline is NOT a ratio of pooled means: one huge task must not dominate.
    # Here aracne wins 2:1 on two small tasks and loses 1:2 on one enormous one. The
    # arithmetic ratio-of-means is dragged above 1; the geometric mean of ratios is not.
    rows = [row("s1", "baseline", cache=1000, repo="r1"), row("s1", "aracne", cache=500, repo="r1"),
            row("s2", "baseline", cache=1000, repo="r2"), row("s2", "aracne", cache=500, repo="r2"),
            row("big", "baseline", cache=1_000_000, repo="r3"), row("big", "aracne", cache=2_000_000, repo="r3")]
    e = paired.analyse(rows)["efficiency"]["context_tokens"]
    pooled = (500 + 500 + 2_000_000) / (1000 + 1000 + 1_000_000)
    check("pooled ratio is dominated by the big task", pooled > 1.9)
    check("paired ratio resists the outlier", e["ratio"] < 1.3, str(e["ratio"]))

    # A zero-valued or errored arm cannot enter a log-ratio.
    rows = [row("t0", "baseline", cache=1000, repo="r0"), row("t0", "aracne", cache=0, ctx_in=0, repo="r0"),
            row("t1", "baseline", cache=1000, repo="r1"), row("t1", "aracne", cache=500, repo="r1", error="boom")]
    e = paired.analyse(rows)["efficiency"]["context_tokens"]
    check("zero and errored pairs are dropped", e["n_pairs"] == 0, str(e["n_pairs"]))


def test_cluster_widens_ci():
    print("\nclustering widens the interval (the whole point)")
    # 12 identical-effect tasks. Spread over 12 repos they are 12 independent observations;
    # packed into 2 repos they are 2. The clustered CI must be the wider of the two.
    def build(n_repos):
        rows = []
        for i in range(12):
            ratio = 0.5 if i % 2 == 0 else 0.9  # some within-task variation
            rows += [row(f"t{i}", "baseline", cache=10_000, repo=f"r{i % n_repos}"),
                     row(f"t{i}", "aracne", cache=int(10_000 * ratio), repo=f"r{i % n_repos}")]
        return paired.analyse(rows)["efficiency"]["context_tokens"]

    wide, narrow = build(2), build(12)
    check("same point estimate either way", abs(wide["ratio"] - narrow["ratio"]) < 1e-9)
    w = wide["ratio_ci"][1] - wide["ratio_ci"][0]
    n = narrow["ratio_ci"][1] - narrow["ratio_ci"][0]
    check("2 repos give a wider CI than 12", w > n, f"{w:.4f} vs {n:.4f}")
    check("effective N reflects repos, not tasks",
          paired.analyse([row("t0", "baseline", repo="r0"), row("t0", "aracne", repo="r0")])["effective_n"] == 1)


def test_single_cluster_claims_nothing():
    print("\nsingle cluster reports no interval (no false significance)")
    # Five tasks, all from ONE repo, all showing a large consistent effect. Resampling one
    # cluster can only ever redraw that cluster, so the interval would collapse to zero
    # width and declare significance at effective N = 1. It must refuse instead.
    rows = []
    for i in range(5):
        rows += [row(f"t{i}", "baseline", ctx_in=0, cache=10_000, repo="only"),
                 row(f"t{i}", "aracne", ctx_in=0, cache=13_000, repo="only")]
    res = paired.analyse(rows)
    e = res["efficiency"]["context_tokens"]
    check("effective N is 1", res["effective_n"] == 1)
    check("point estimate is still reported", abs(e["ratio"] - 1.3) < 1e-6, str(e["ratio"]))
    check("no CI from one cluster", e["ratio_ci"] is None, str(e["ratio_ci"]))
    check("never significant from one cluster", e["significant"] is False)

    # Same rule for the solve-rate difference and the non-inferiority verdict.
    rows = [row(f"t{i}", arm, success=True, repo="only")
            for i in range(5) for arm in ("baseline", "aracne")]
    s = paired.analyse(rows)["solve"]
    check("no solve CI from one cluster", s["rate_diff_ci"] is None)
    check("non-inferiority not claimed from one cluster", s["noninferior"] is False)


def test_size_stratification():
    print("\nsize stratification")
    rows = [row("t0", "baseline", repo="r0"), row("t0", "aracne", repo="r0")]
    check("absent repo_nodes => unavailable, not invented",
          paired.analyse(rows)["by_size"]["available"] is False)

    # Savings that grow with repo size: tiny repos break even, huge repos halve.
    rows = []
    for i, (nodes, ratio) in enumerate([(50, 1.05), (80, 1.0), (150, 0.98),
                                        (400, 0.85), (700, 0.8),
                                        (3000, 0.55), (9000, 0.5), (20000, 0.45)]):
        base = 100_000
        rows += [dict(row(f"t{i}", "baseline", ctx_in=0, cache=base, repo=f"r{i}"),
                      repo_nodes=nodes),
                 dict(row(f"t{i}", "aracne", ctx_in=0, cache=int(base * ratio), repo=f"r{i}"),
                      repo_nodes=nodes)]
    sz = paired.analyse(rows)["by_size"]
    check("stratification available", sz["available"] is True)
    check("buckets are populated", all(sz["buckets"][b]["n_pairs"] > 0
                                       for b in ("small", "medium", "large")), str(sz["buckets"]))
    small = sz["buckets"]["small"]["ratio"]
    large = sz["buckets"]["large"]["ratio"]
    check("large repos save more than small", large < small, f"{large} vs {small}")
    check("slope is negative", sz["slope_r"] < 0, str(sz["slope_r"]))
    check("growth-with-size is detected", sz["grows_with_size"] is True, str(sz["slope_ci"]))

    # A flat effect must NOT be reported as growing with size.
    rows = []
    for i, nodes in enumerate([50, 300, 5000, 60, 400, 8000]):
        rows += [dict(row(f"t{i}", "baseline", ctx_in=0, cache=100_000, repo=f"r{i}"),
                      repo_nodes=nodes),
                 dict(row(f"t{i}", "aracne", ctx_in=0, cache=80_000, repo=f"r{i}"),
                      repo_nodes=nodes)]
    sz = paired.analyse(rows)["by_size"]
    check("flat effect is not reported as size-growing", sz["grows_with_size"] is False,
          str(sz["slope_ci"]))


def test_determinism():
    print("\ndeterminism")
    rows = []
    for i in range(6):
        rows += [row(f"t{i}", "baseline", cache=10_000, repo=f"r{i}"),
                 row(f"t{i}", "aracne", cache=6_000 + i * 100, repo=f"r{i}")]
    a = paired.analyse(rows)["efficiency"]["context_tokens"]["ratio_ci"]
    b = paired.analyse(rows)["efficiency"]["context_tokens"]["ratio_ci"]
    check("re-scoring reproduces the same CI", a == b, f"{a} vs {b}")


def test_repo_diverse_sampling():
    print("\nrepo-diverse sampling")

    def task(key, repo):
        return sources.Task(key=key, language="go", source="multi_swe_bench",
                            clone_url=f"https://github.com/acme/{repo}.git",
                            base_commit="abc", problem_statement="")

    # A pool dominated by one repo: 6 issues from `big`, 1 each from three others.
    pool = ([task(f"big-{i}", "big") for i in range(6)]
            + [task(f"{r}-1", r) for r in ("one", "two", "three")])

    drawn = sources._draw_repo_diverse(pool, samples=4, max_per_repo=1)
    counts = sources.repo_counts(drawn)
    check("draws 4 tasks", len(drawn) == 4, str(len(drawn)))
    check("never twice from one repo at max_per_repo=1", max(counts.values()) == 1, str(counts))
    check("covers 4 distinct repos", len(counts) == 4, str(counts))

    # Round-robin: a second task per repo only after every repo has given one.
    drawn = sources._draw_repo_diverse(pool, samples=6, max_per_repo=2)
    counts = sources.repo_counts(drawn)
    check("max_per_repo=2 is respected", max(counts.values()) <= 2, str(counts))
    check("still spreads over all 4 repos", len(counts) == 4, str(counts))

    # Fewer distinct repos than requested samples => a SHORT list, not a correlated one.
    drawn = sources._draw_repo_diverse(pool, samples=8, max_per_repo=1)
    check("short draw rather than repeat repos", len(drawn) == 4, str(len(drawn)))

    # The old behaviour that produced 3-of-3 from vuejs/core.
    naive = pool[:3]
    check("plain slicing would have picked one repo 3x",
          max(sources.repo_counts(naive).values()) == 3)


def main() -> int:
    print("paired-analysis regression tests")
    test_context_tokens()
    test_pairing()
    test_mcnemar()
    test_ratios()
    test_cluster_widens_ci()
    test_single_cluster_claims_nothing()
    test_repo_diverse_sampling()
    test_size_stratification()
    test_determinism()
    print("\n" + ("ALL PASSED" if not FAILURES else f"{len(FAILURES)} FAILED: {FAILURES}"))
    return 1 if FAILURES else 0


if __name__ == "__main__":
    raise SystemExit(main())

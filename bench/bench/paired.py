"""Paired (within-task) analysis of the A/B matrix.

WHY PAIRED. The benchmark runs BOTH arms on the SAME task at the SAME seed, so every task
yields a matched pair. Task difficulty is by far the largest source of variance — some
issues are trivial, some are hopeless — and in a paired design that variance cancels
exactly. The pooled analysis in metrics.py does not exploit this: it builds an independent
Wilson interval per arm and subtracts, which throws the pairing away and leaves intervals
so wide (e.g. [0.30, 0.90] at n=10) that no run of a realistic size can say anything.
Everything here is computed on per-task DIFFERENCES instead.

THREE CHOICES THAT MATTER:

1. Solve rate -> McNemar's exact test. Pairs where both arms solve, or both fail, carry no
   information about which arm is better; only DISCORDANT pairs do. Conditioning on them
   is what makes a small pool informative.

2. Efficiency -> geometric mean of per-task RATIOS, not a ratio of pooled means. Token
   counts are heavy-tailed across tasks (one big repo dominates an arithmetic mean), while
   the within-task ratio aracne/baseline is stable. We average log(aracne/baseline) and
   exponentiate, so the headline reads "aracne uses 0.62x the context tokens".

3. Bootstrap resamples REPOSITORIES, not runs. Several tasks can come from one repo (the
   `multi` manifest is 10 tasks over 6 repos) and tasks in a repo share topology, build
   system and difficulty. Resampling runs would treat them as independent and understate
   every interval. `effective_n` reports the cluster count — the honest N of the study.

NON-INFERIORITY. Proving aracne SOLVES MORE needs a very large pool (McNemar power depends
on the discordant count, which is small). Proving it does not solve MEASURABLY FEWER is
cheap. `noninferiority` reports that verdict against `margin`: it holds when the lower
bound of the paired solve-rate difference sits above -margin.
"""
from __future__ import annotations

import math
import random
from collections import defaultdict

from . import outcome
from .rowmetrics import EFFICIENCY_METRICS, has_metrics

BASELINE = "baseline"
ARACNE = "aracne"

N_BOOT = 10000
BOOT_SEED = 0
DEFAULT_MARGIN = 0.10

# A cluster bootstrap needs at least two clusters to have anything to resample. With one,
# every resample is the SAME cluster, the interval collapses to zero width, and a run over a
# single repository reports its point estimate as a significant result. That failure is
# silent and badly misleading, so below this threshold we report NO interval at all —
# the point estimate stands alone and `significant` stays false.
MIN_CLUSTERS = 2


# --------------------------------------------------------------------------- pairing

def cluster_id(row: dict) -> str:
    """The unit of independence: the repository a task came from.

    Rows carry org/repo for Multi-SWE-bench; SWE-bench rows may not, so fall back to the
    instance_id prefix (`pallets__flask-4045` -> `pallets__flask`).
    """
    org, repo = row.get("org"), row.get("repo")
    if org and repo:
        return f"{org}/{repo}"
    iid = str(row.get("instance_id") or row.get("run_key") or "")
    return iid.rsplit("-", 1)[0] or iid


def pair_key(row: dict) -> tuple:
    """One matched observation: the same task at the same seed, run under both arms."""
    return (row.get("instance_id"), str(row.get("seed")))


def build_pairs(rows: list[dict]) -> list[dict]:
    """Collapse result rows into matched pairs, dropping any task missing an arm.

    An unmatched row is silently unusable for a paired analysis (there is nothing to
    difference it against); `n_unpaired` in the summary reports how many were dropped so a
    half-finished matrix cannot masquerade as a complete one.
    """
    by_key: dict[tuple, dict] = defaultdict(dict)
    for row in rows:
        arm = row.get("arm")
        if arm in (BASELINE, ARACNE):
            by_key[pair_key(row)][arm] = row
    pairs = []
    for key, arms in sorted(by_key.items(), key=lambda kv: str(kv[0])):
        if BASELINE in arms and ARACNE in arms:
            pairs.append({
                "key": key,
                "cluster": cluster_id(arms[BASELINE]),
                "language": arms[BASELINE].get("language"),
                BASELINE: arms[BASELINE],
                ARACNE: arms[ARACNE],
            })
    return pairs


def _unpaired_count(rows: list[dict]) -> int:
    by_key: dict[tuple, set] = defaultdict(set)
    for row in rows:
        if row.get("arm") in (BASELINE, ARACNE):
            by_key[pair_key(row)].add(row["arm"])
    return sum(1 for arms in by_key.values() if len(arms) < 2)


# ------------------------------------------------------------------- cluster bootstrap

def _bootstrap_ci(units: list, stat, n_boot: int = N_BOOT, alpha: float = 0.05):
    """Percentile CI for `stat`, resampling CLUSTERS of observations with replacement.

    `units` is a list of clusters, each a list of per-pair observations. Deterministic:
    seeded RNG, so re-scoring a run reproduces its intervals exactly.
    """
    if len(units) < MIN_CLUSTERS:
        return None
    rng = random.Random(BOOT_SEED)
    n = len(units)
    samples = []
    for _ in range(n_boot):
        drawn = []
        for _ in range(n):
            drawn.extend(units[rng.randrange(n)])
        value = stat(drawn)
        if value is not None:
            samples.append(value)
    if not samples:
        return None
    samples.sort()
    lo = samples[max(0, int((alpha / 2) * len(samples)) - 1)]
    hi = samples[min(len(samples) - 1, int((1 - alpha / 2) * len(samples)))]
    return [lo, hi]


def _by_cluster(pairs: list[dict], observe) -> list[list]:
    """Group per-pair observations by repository, dropping pairs `observe` rejects."""
    groups: dict[str, list] = defaultdict(list)
    for pair in pairs:
        value = observe(pair)
        if value is not None:
            groups[pair["cluster"]].append(value)
    return [group for group in groups.values() if group]


# ------------------------------------------------------------------------ solve rate

def _graded(row: dict) -> bool:
    """Only a real grader verdict counts. A timeout is censored, never a failure."""
    return row.get("success") is not None and not outcome.is_timeout(row)


def _exact_mcnemar(b: int, c: int) -> float | None:
    """Two-sided exact McNemar p-value. b = aracne-only wins, c = baseline-only wins.

    Under the null the discordant pairs split 50/50, so this is an exact binomial sign
    test on b out of b+c. The exact form (not the chi-square approximation) is required
    here: discordant counts in a benchmark of this size are routinely under 10.
    """
    n = b + c
    if n == 0:
        return None
    k = min(b, c)
    tail = sum(math.comb(n, i) for i in range(k + 1)) * (0.5 ** n)
    return min(1.0, 2.0 * tail)


def solve_analysis(pairs: list[dict], margin: float = DEFAULT_MARGIN) -> dict:
    """McNemar on discordant pairs + a cluster-bootstrapped paired rate difference."""
    usable = [p for p in pairs if _graded(p[BASELINE]) and _graded(p[ARACNE])]
    both = aracne_only = baseline_only = neither = 0
    for pair in usable:
        a, b = bool(pair[ARACNE]["success"]), bool(pair[BASELINE]["success"])
        if a and b:
            both += 1
        elif a and not b:
            aracne_only += 1
        elif b and not a:
            baseline_only += 1
        else:
            neither += 1

    def diff(obs: list[int]) -> float | None:
        return (sum(obs) / len(obs)) if obs else None

    units = _by_cluster(usable, lambda p: int(bool(p[ARACNE]["success"])) - int(bool(p[BASELINE]["success"])))
    point = diff([v for group in units for v in group])
    ci = _bootstrap_ci(units, diff)
    result = {
        "n_pairs": len(usable),
        "n_clusters": len(units),
        "both_solved": both,
        "aracne_only": aracne_only,
        "baseline_only": baseline_only,
        "neither_solved": neither,
        "n_discordant": aracne_only + baseline_only,
        "mcnemar_p": _exact_mcnemar(aracne_only, baseline_only),
        "rate_diff": round(point, 4) if point is not None else None,
        "rate_diff_ci": [round(ci[0], 4), round(ci[1], 4)] if ci else None,
        "margin": margin,
    }
    # Non-inferiority holds only when the whole interval clears the margin. With no
    # interval (too few clusters) the verdict is unknown, never a silent pass.
    result["noninferior"] = bool(ci and ci[0] > -margin)
    return result


# ------------------------------------------------------------------------- efficiency

def ratio_analysis(pairs: list[dict], name: str, extract) -> dict:
    """Geometric mean of per-task aracne/baseline ratios, with a cluster-bootstrap CI.

    Both arms must have usable agent metrics and a strictly positive value: a zero would
    make the log undefined, and a hard-errored run has no meaningful count to compare.
    """
    def observe(pair):
        a_row, b_row = pair[ARACNE], pair[BASELINE]
        if not (has_metrics(a_row) and has_metrics(b_row)):
            return None
        a, b = extract(a_row), extract(b_row)
        if a <= 0 or b <= 0:
            return None
        return math.log(a / b)

    def geo(logs: list[float]) -> float | None:
        return math.exp(sum(logs) / len(logs)) if logs else None

    units = _by_cluster(pairs, observe)
    logs = [v for group in units for v in group]
    point = geo(logs)
    ci = _bootstrap_ci(units, geo)
    out = {
        "metric": name,
        "n_pairs": len(logs),
        "n_clusters": len(units),
        "ratio": round(point, 4) if point is not None else None,
        "ratio_ci": [round(ci[0], 4), round(ci[1], 4)] if ci else None,
        "pct_change": round((point - 1) * 100, 1) if point is not None else None,
        "pct_change_ci": [round((ci[0] - 1) * 100, 1), round((ci[1] - 1) * 100, 1)] if ci else None,
    }
    # A ratio interval excluding 1.0 is the claim that survives peer scrutiny.
    out["significant"] = bool(ci and (ci[1] < 1.0 or ci[0] > 1.0))
    return out


# ------------------------------------------------------------------- size stratification

# Repo size buckets, in describable topology nodes. aracne's value proposition is that
# targeted navigation beats reading files, and that advantage should GROW with the size of
# the codebase. A single pooled percentage cannot show that; a per-bucket breakdown plus a
# slope can, and it is far more robust to which repos happened to be sampled.
SIZE_BUCKETS = (
    ("small", 0, 200),
    ("medium", 200, 1000),
    ("large", 1000, float("inf")),
)


def _repo_nodes(pair: dict):
    """Topology size of the pair's repo. Identical across arms; baseline is authoritative."""
    for arm in (BASELINE, ARACNE):
        n = pair[arm].get("repo_nodes")
        if n:
            return float(n)
    return None


def size_analysis(pairs: list[dict], metric: str = "context_tokens") -> dict:
    """Per-size-bucket ratios plus the size SLOPE for one efficiency metric.

    The slope is the correlation between log(repo size) and log(aracne/baseline). A negative
    slope is the claim that matters: the bigger the repository, the more aracne saves.
    Returns `available: False` when rows carry no `repo_nodes` (runs recorded before that
    field existed), rather than inventing a stratification.
    """
    extract = dict((name, fn) for name, _l, fn, _b in EFFICIENCY_METRICS)[metric]
    sized = [p for p in pairs if _repo_nodes(p) is not None]
    if not sized:
        return {"available": False, "metric": metric,
                "reason": "rows carry no repo_nodes (re-run to record repo size)"}

    buckets = {}
    for name, lo, hi in SIZE_BUCKETS:
        subset = [p for p in sized if lo <= _repo_nodes(p) < hi]
        buckets[name] = {"range": [lo, None if hi == float("inf") else hi],
                         **ratio_analysis(subset, metric, extract)}

    # Slope: Pearson r between log(size) and log(ratio), bootstrapped over repos.
    def observe(pair):
        a, b = extract(pair[ARACNE]), extract(pair[BASELINE])
        if not (has_metrics(pair[ARACNE]) and has_metrics(pair[BASELINE])) or a <= 0 or b <= 0:
            return None
        n = _repo_nodes(pair)
        if not n or n <= 0:
            return None
        return (math.log(n), math.log(a / b))

    def pearson(obs):
        if len(obs) < 3:
            return None
        xs = [o[0] for o in obs]
        ys = [o[1] for o in obs]
        mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
        sxy = sum((x - mx) * (y - my) for x, y in obs)
        sxx = sum((x - mx) ** 2 for x in xs)
        syy = sum((y - my) ** 2 for y in ys)
        if sxx <= 0 or syy <= 0:
            return None
        return sxy / math.sqrt(sxx * syy)

    units = _by_cluster(sized, observe)
    flat = [o for group in units for o in group]
    r = pearson(flat)
    ci = _bootstrap_ci(units, pearson)
    return {
        "available": True,
        "metric": metric,
        "buckets": buckets,
        "n_sized_pairs": len(flat),
        "n_clusters": len(units),
        "slope_r": round(r, 3) if r is not None else None,
        "slope_ci": [round(ci[0], 3), round(ci[1], 3)] if ci else None,
        # A negative correlation whose interval stays below 0 is the "savings grow with
        # repo size" claim. Anything else is not yet evidence for it.
        "grows_with_size": bool(ci and ci[1] < 0),
    }


# ---------------------------------------------------------------------------- top level

def analyse(rows: list[dict], margin: float = DEFAULT_MARGIN) -> dict:
    """Full paired analysis of a row set: solve rate + every efficiency endpoint."""
    pairs = build_pairs(rows)
    clusters = sorted({p["cluster"] for p in pairs})
    return {
        "n_pairs": len(pairs),
        "n_unpaired": _unpaired_count(rows),
        "effective_n": len(clusters),
        "clusters": clusters,
        "solve": solve_analysis(pairs, margin),
        "efficiency": {name: ratio_analysis(pairs, name, extract)
                       for name, _label, extract, _lower in EFFICIENCY_METRICS},
        "by_size": size_analysis(pairs),
    }


def analyse_by_language(rows: list[dict], languages: list[str],
                        margin: float = DEFAULT_MARGIN) -> dict:
    out = {}
    for lang in languages:
        subset = [r for r in rows if r.get("language") == lang]
        if subset:
            out[lang] = analyse(subset, margin)
    return out

"""End-of-run LLM analysis: turn the aggregate into a short, digestible narrative.

One best-effort request through the same agent backend the benchmark already uses
(Claude Code on the Max plan — no API key). It runs in `out_dir`, which contains no
`.mcp.json`, so no aracne tools load and the model simply writes prose from the numbers
embedded in the prompt. Any failure is swallowed (returns "") so it can never abort a run,
mirroring the best-effort grading step (grade.grade_all).
"""
from __future__ import annotations

from pathlib import Path

from . import agents


def _k(n) -> str:
    if n is None:
        return "n/a"
    return f"{n / 1000:.1f}k" if n >= 1000 else str(n)


def _rate(st: dict) -> str:
    solved, graded = st.get("solved"), st.get("n_graded")
    base = f"{solved}/{graded}"
    sr = st.get("success_rate")
    if sr is None:
        return f"{base} (ungraded)"
    ci = st.get("success_ci")
    ci_s = f" [95% CI {ci[0]:.0%}-{ci[1]:.0%}]" if ci else ""
    return f"{base} = {sr:.0%}{ci_s}"


def _pct(v) -> str:
    return "n/a" if v is None else f"{v:+.0f}%"


def _arm_line(name: str, st: dict) -> str:
    return (f"  {name}: success {_rate(st)}; "
            f"tokens context {_k(st.get('mean_context_tokens'))} / out {_k(st.get('mean_output_tokens'))} "
            f"/ total {_k(st.get('mean_total_tokens'))}; "
            f"turns {st.get('mean_turns')}; wall {st.get('mean_wall_s')}s; "
            f"timeouts {st.get('n_timeout', 0)} "
            f"(agent {st.get('n_timeout_agent', 0)}, grading {st.get('n_timeout_grading', 0)}); "
            f"(n_runs={st.get('n_runs')}, graded={st.get('n_graded')}, with_tokens={st.get('n_tokens')})"
            + _tool_line(st))


def _tool_line(st: dict) -> str:
    """Per-tool telemetry, appended to an arm line when transcripts were captured.

    This is what makes a regression diagnosable rather than merely visible: it says whether
    the agent actually reached for the aracne tools, how many bytes they returned, and how
    often a lookup missed or came back ambiguous.
    """
    if not st.get("n_tool_runs"):
        return ""
    return ("\n      tools: "
            f"{st.get('mean_tool_calls')} calls "
            f"(mcp {st.get('mean_mcp_calls')}, native {st.get('mean_native_calls')}); "
            f"bytes mcp {_k(st.get('mean_mcp_result_bytes'))} / native {_k(st.get('mean_native_result_bytes'))} "
            f"/ grep {_k(st.get('mean_grep_result_bytes'))} / read {_k(st.get('mean_read_result_bytes'))}; "
            f"id-misses {st.get('mean_id_misses')}, ambiguous {st.get('mean_ambiguous')}, "
            f"guard-denials {st.get('mean_guard_denials')}")


def _delta_line(d: dict | None) -> str:
    if not d:
        return "  delta: not available (need both arms graded)"
    sr = d.get("success_rate_abs")
    sr_s = f"{sr * 100:+.1f}pp" if sr is not None else f"{d.get('solved_abs', 0):+d} solved"
    return (f"  delta (aracne - baseline): success {sr_s}; "
            f"context {_pct(d.get('context_tokens_pct'))}, output {_pct(d.get('output_tokens_pct'))}, "
            f"total {_pct(d.get('total_tokens_pct'))}, turns {_pct(d.get('turns_pct'))}, "
            f"wall {_pct(d.get('wall_pct'))}")


def _summary(agg: dict, meta: dict | None) -> str:
    lines: list[str] = []
    m = meta or {}
    lines.append(
        f"Run: {m.get('run_name', '?')}  harness={m.get('run_harness', '?')}  "
        f"model={m.get('model', '?')}  sample={m.get('sample_id', '?')}  "
        f"seeds={m.get('seeds', '?')}  arms={m.get('arms', '?')}")

    ov = agg.get("overall", {})
    lines.append("\nOVERALL")
    for arm, st in ov.get("arms", {}).items():
        lines.append(_arm_line(arm, st))
    lines.append(_delta_line(ov.get("delta")))

    lines.append("\nPER LANGUAGE")
    for lang, block in agg.get("per_language", {}).items():
        arms = block.get("arms", {})
        if all(st.get("n_runs", 0) == 0 for st in arms.values()):
            continue
        lines.append(f"[{lang}]")
        for arm, st in arms.items():
            lines.append(_arm_line(arm, st))
        lines.append(_delta_line(block.get("delta")))
        lines.append(_lang_paired_line(block.get("paired")))
    lines.append(_paired_block(agg.get("paired")))
    return "\n".join(lines)


def _lang_paired_line(pr: dict | None) -> str:
    """The PAIRED ratios for one language.

    Without this the model only ever saw pooled per-language means and one pooled paired
    block — so a result that is two large opposite SIGNIFICANT effects cancelling to a
    null looks like "no effect". That is exactly what happened in run-20260824-125118.
    """
    if not pr or not pr.get("n_pairs"):
        return "  paired: unavailable (no matched pairs for this language)"
    eff = pr.get("efficiency") or {}
    parts = []
    for name in ("context_tokens", "tokens_per_turn", "turns"):
        e = eff.get(name)
        if not e:
            continue
        star = " SIGNIFICANT" if e.get("significant") else ""
        parts.append(f"{name} {e.get('ratio')}x CI {e.get('pct_change_ci')}{star}")
    if not parts:
        return "  paired: no efficiency ratios"
    head = f"  paired (n={pr['n_pairs']}, repos={pr['effective_n']}): "
    return head + "; ".join(parts)


def _paired_block(pr: dict | None) -> str:
    """The within-task analysis, rendered for the summariser.

    Given to the model AFTER the pooled numbers and explicitly labelled as authoritative:
    pooled per-arm means confound aracne's effect with which tasks each arm happened to
    draw, while these difference each task against itself.
    """
    if not pr or not pr.get("n_pairs"):
        return "\nPAIRED ANALYSIS: unavailable (no matched task pairs)."
    s = pr["solve"]
    out = [
        "\nPAIRED ANALYSIS (AUTHORITATIVE — prefer these over the pooled means above)",
        f"  matched pairs {pr['n_pairs']} over {pr['effective_n']} distinct repositories "
        f"(effective N = {pr['effective_n']}; unpaired dropped {pr['n_unpaired']})",
        f"  solve: both {s['both_solved']}, aracne-only {s['aracne_only']}, "
        f"baseline-only {s['baseline_only']}, neither {s['neither_solved']}; "
        f"McNemar exact p = {s['mcnemar_p']}",
        f"  solve rate diff {s['rate_diff']} 95% CI {s['rate_diff_ci']}; "
        f"non-inferior at {s['margin']} margin: {s['noninferior']}",
    ]
    for name, e in (pr.get("efficiency") or {}).items():
        out.append(f"  {name}: ratio {e.get('ratio')}x ({_pct(e.get('pct_change'))}), "
                   f"95% CI {e.get('pct_change_ci')}, significant={e.get('significant')} "
                   f"(pairs={e.get('n_pairs')}, clusters={e.get('n_clusters')})")
    out.append(_size_block(pr.get("by_size")))
    return "\n".join(out)


def _size_block(bs: dict | None) -> str:
    """Context-token ratio split by repo size, plus the size slope.

    aracne's premise is that targeted navigation beats reading files and that the edge
    WIDENS with codebase size. This block is the direct test of that premise, and it was
    missing from the prompt entirely until now.
    """
    if not bs or not bs.get("available"):
        reason = (bs or {}).get("reason", "no repo_nodes recorded")
        return f"  by size: unavailable ({reason})"
    out = ["  by repo size (context tokens, describable nodes):"]
    for label, b in (bs.get("buckets") or {}).items():
        if not b:
            continue
        lo, hi = b.get("range", [None, None])
        star = " SIGNIFICANT" if b.get("significant") else ""
        out.append(f"    {label} [{lo}..{hi}]: ratio {b.get('ratio')}x "
                   f"({_pct(b.get('pct_change'))}), 95% CI {b.get('pct_change_ci')}{star} "
                   f"(pairs={b.get('n_pairs')})")
    out.append(f"    slope log(size) vs log(ratio): r = {bs.get('slope_r')} "
               f"CI {bs.get('slope_ci')}; grows_with_size = {bs.get('grows_with_size')}")
    return "\n".join(out)


PROMPT = """\
You are analyzing A/B benchmark results for **aracne**, a static-analysis engine that gives
coding agents a topology-aware navigation graph (functions, types, and their edges, each with
a description) instead of grepping and reading raw source files. The benchmark runs real
SWE-bench issue-fixing tasks under two arms that share the SAME model and harness:
`baseline` (vanilla Claude Code) vs `aracne` (Claude Code + aracne's MCP tools). The only
difference is aracne's presence, so the delta isolates aracne's effect. The win condition is
FEWER tokens / turns / wall-clock at an EQUAL-OR-BETTER success rate.
TIMEOUTS are a THIRD outcome, never a failure: a timed-out run is excluded from the
success-rate denominator entirely, so never describe one as a failed or incorrect result.

HOW TO READ THE NUMBERS — this matters more than any single figure:
- Both arms run the SAME tasks, so the PAIRED ANALYSIS block is authoritative. The pooled
  per-arm means confound aracne's effect with task difficulty; the paired ratios do not.
- A pooled null can HIDE two large opposite effects that cancel. Before calling the overall
  result "no effect", check the per-language `paired:` lines and the `by repo size` block.
  If two languages (or two size buckets) move significantly in OPPOSITE directions, the
  pooled ratio is a cancellation artifact with no referent — say so, and lead with the
  split instead of the pooled number.
- `tokens_per_turn` is the only token endpoint NOT confounded by turn count: context tokens
  are a sum over turns, so a run with 16% more turns reads ~16% more context even if each
  turn were identically sized. Report context_tokens and turns as ONE finding, and use
  tokens_per_turn to say whether each turn actually got cheaper.
- `by repo size` tests aracne's core premise (the edge should WIDEN with codebase size).
  A positive ratio in the `small` bucket with a negative one in `large` means the overhead
  is fixed and the benefit proportional — name that pattern when you see it.
- The `tools:` lines say what the agent actually DID. If the aracne arm shows near-zero
  `mcp` calls, a large `native` count, many `id-misses`, or many `ambiguous` lookups, that
  is the mechanism behind any regression — lead with it rather than with the token totals.
- "context tokens" (uncached input + cache creation + cache reads) is the PRIMARY token
  endpoint — it is what the agent actually consumed. Ignore any bare "input tokens" figure.
- The honest sample size is `effective N` = distinct REPOSITORIES, not the run count. Tasks
  from one repo are correlated. Say the effective N out loud when it is small.
- An efficiency ratio is a real result ONLY if `significant=true` (its 95% CI excludes 1.0).
  Report a non-significant ratio as "directionally X% but not distinguishable from no
  effect at this sample size" — never as a headline win.
- For solve rate, prefer the McNemar p and the non-inferiority verdict over the raw rates.
  With few discordant pairs the correct conclusion is usually "no detectable difference in
  correctness", which is the intended result if efficiency improved.

Write a concise, digestible analysis for an engineer skimming the report:
- 3 to 6 short markdown bullet points (or two short paragraphs).
- LEAD with the paired context-token ratio (with its CI and whether it is significant) —
  unless the per-language or by-size splits disagree in sign, in which case LEAD with that
  split and explain that the pooled figure is a cancellation artifact.
- Then tokens_per_turn and turns (as one finding), then wall time, then correctness.
- If tool telemetry is present, say in one line what the agent actually did differently.
- Interpret the numbers rather than restating them: say whether aracne helped, hurt, or was neutral, and where.
- Explicitly flag the effective N (repositories) and any caveats (ungraded runs, timeouts,
  runs reporting no token data). If effective N is under ~15, state plainly that the
  intervals are too wide to settle the question and that more DISTINCT REPOS are needed.
- Do NOT emphasize dollar cost (it is ~0 on the Max plan). No preamble, no headings, no sign-off.

Results:

{summary}
"""


def generate_analysis(agg: dict, cfg: dict, out_dir: Path, meta: dict | None = None) -> str:
    """One best-effort LLM request that writes analysis.md and returns its text ("" on failure)."""
    try:
        prompt = PROMPT.format(summary=_summary(agg, meta))
        rr = agents.run_agent(
            "claude_code", prompt, out_dir,
            cfg.get("analysis_model", "haiku"),
            cfg.get("analysis_max_turns", 2),
            cfg.get("timeout_s", 1800),
        )
        text = (rr.result_text or "").strip()
        if rr.is_error or not text:
            print(f"[analysis] skipped (agent returned {'error' if rr.is_error else 'no text'}).")
            return ""
        (out_dir / "analysis.md").write_text(text + "\n", encoding="utf-8")
        return text
    except Exception as e:  # noqa: BLE001 — analysis is best-effort, never fatal
        print(f"[analysis] skipped ({e}).")
        return ""

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
            f"tokens in {_k(st.get('mean_input_tokens'))} / out {_k(st.get('mean_output_tokens'))} "
            f"/ total {_k(st.get('mean_total_tokens'))}; "
            f"turns {st.get('mean_turns')}; wall {st.get('mean_wall_s')}s "
            f"(n_runs={st.get('n_runs')}, graded={st.get('n_graded')}, with_tokens={st.get('n_tokens')})")


def _delta_line(d: dict | None) -> str:
    if not d:
        return "  delta: not available (need both arms graded)"
    sr = d.get("success_rate_abs")
    sr_s = f"{sr * 100:+.1f}pp" if sr is not None else f"{d.get('solved_abs', 0):+d} solved"
    return (f"  delta (aracne - baseline): success {sr_s}; "
            f"input {_pct(d.get('input_tokens_pct'))}, output {_pct(d.get('output_tokens_pct'))}, "
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
    return "\n".join(lines)


PROMPT = """\
You are analyzing A/B benchmark results for **aracne**, a static-analysis engine that gives
coding agents a topology-aware navigation graph (functions, types, and their edges, each with
a description) instead of grepping and reading raw source files. The benchmark runs real
SWE-bench issue-fixing tasks under two arms that share the SAME model and harness:
`baseline` (vanilla Claude Code) vs `aracne` (Claude Code + aracne's MCP tools). The only
difference is aracne's presence, so the delta isolates aracne's effect. The win condition is
FEWER tokens / turns / wall-clock at an EQUAL-OR-BETTER success rate.

Write a concise, digestible analysis for an engineer skimming the report:
- 3 to 6 short markdown bullet points (or two short paragraphs).
- LEAD with the success-rate change, then token savings (input / output / total), then turns and wall time.
- Interpret the numbers rather than restating them: say whether aracne helped, hurt, or was neutral, and where.
- Explicitly flag small sample sizes and any caveats (ungraded runs; some runs reporting no token data).
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

"""Render a self-contained HTML report for one benchmark run.

No third-party deps: plain f-string templating + html.escape + inline CSS/SVG-free bars.
The report shows, per the run's `meta`, how the aracne arm changed success rate, token
usage, turns, and wall time vs the baseline arm (the deltas metrics.aggregate computes),
with an OVERALL table + per-language breakdown and the LLM analysis text embedded.

Robust to partial data: missing arms, `None` rates/CIs/token means, `n_graded == 0`,
single-arm runs (delta is None). Everything user- or model-supplied is html-escaped.
"""
from __future__ import annotations

import html
import re
from pathlib import Path

# Restrained palette (one accent per series), light theme, WCAG-legible.
C_BASELINE = "#64748b"   # slate
C_ARACNE = "#4f46e5"     # indigo
C_GOOD = "#15803d"       # green  (improvement)
C_BAD = "#b91c1c"        # red    (regression)


def _esc(s) -> str:
    return html.escape(str(s))


def _k(n) -> str:
    if n is None:
        return "&ndash;"
    return f"{n / 1000:.1f}k" if n >= 1000 else str(n)


def _num(n) -> str:
    if n is None:
        return "&ndash;"
    return f"{n:g}"


def _rate_cell(st: dict) -> str:
    solved, graded = st.get("solved"), st.get("n_graded")
    sr = st.get("success_rate")
    if sr is None:
        return f"{solved}/{graded} <span class='muted'>(ungraded)</span>"
    ci = st.get("success_ci")
    ci_s = f"<span class='muted'> [{ci[0]:.0%}&ndash;{ci[1]:.0%}]</span>" if ci else ""
    return f"<strong>{sr:.0%}</strong> <span class='muted'>({solved}/{graded})</span>{ci_s}"


def _timeout_cell(st: dict) -> str:
    """Timeouts are their own outcome — shown next to, never inside, the success rate."""
    n = st.get("n_timeout") or 0
    if not n:
        return "<span class='muted'>0</span>"
    parts = []
    if st.get("n_timeout_agent"):
        parts.append(f"{st['n_timeout_agent']} agent")
    if st.get("n_timeout_turns"):
        parts.append(f"{st['n_timeout_turns']} turns")
    if st.get("n_timeout_grading"):
        parts.append(f"{st['n_timeout_grading']} grading")
    detail = f" <span class='muted'>({', '.join(parts)})</span>" if parts else ""
    return f"<strong>{n}</strong>{detail}"


def _delta_timeout_td(delta: dict | None) -> str:
    v = (delta or {}).get("timeout_abs")
    if not v:
        return "<td class='delta neutral'>&ndash;</td>"
    # Fewer timeouts is better.
    return f"<td class='delta {'good' if v < 0 else 'bad'}'>{v:+d}</td>"


def _delta_class(v, good_when_negative: bool) -> str:
    if v is None or v == 0:
        return "neutral"
    improved = (v < 0) if good_when_negative else (v > 0)
    return "good" if improved else "bad"


def _delta_pct_td(v, good_when_negative: bool) -> str:
    if v is None:
        return "<td class='delta neutral'>&ndash;</td>"
    return f"<td class='delta {_delta_class(v, good_when_negative)}'>{v:+.0f}%</td>"


def _delta_success_td(delta: dict | None) -> str:
    if not delta:
        return "<td class='delta neutral'>&ndash;</td>"
    sr = delta.get("success_rate_abs")
    if sr is not None:
        cls = "good" if sr > 0 else ("bad" if sr < 0 else "neutral")
        return f"<td class='delta {cls}'>{sr * 100:+.1f}pp</td>"
    solved = delta.get("solved_abs", 0)
    cls = "good" if solved > 0 else ("bad" if solved < 0 else "neutral")
    return f"<td class='delta {cls}'>{solved:+d} solved</td>"


def _arms_table(arms: dict, delta: dict | None) -> str:
    """A metric-rows x (arm columns + optional Δ) table for one scope."""
    arm_names = list(arms.keys())
    has_delta = delta is not None
    head = "".join(f"<th>{_esc(a)}</th>" for a in arm_names)
    if has_delta:
        head += "<th>Δ</th>"

    rows: list[str] = []

    def add(label: str, cell_fn, delta_td: str, emph: bool = False) -> None:
        tds = "".join(cell_fn(arms[a]) for a in arm_names)
        rows.append(f"<tr class='{'emph' if emph else ''}'>"
                    f"<th class='metric'>{label}</th>{tds}{delta_td if has_delta else ''}</tr>")

    add("Success rate", lambda st: f"<td>{_rate_cell(st)}</td>",
        _delta_success_td(delta), emph=True)
    add("Mean context tokens", lambda st: f"<td>{_k(st.get('mean_context_tokens'))}</td>",
        _delta_pct_td(delta.get("context_tokens_pct") if delta else None, True), emph=True)
    add("Mean output tokens", lambda st: f"<td>{_k(st.get('mean_output_tokens'))}</td>",
        _delta_pct_td(delta.get("output_tokens_pct") if delta else None, True))
    add("Mean total tokens", lambda st: f"<td>{_k(st.get('mean_total_tokens'))}</td>",
        _delta_pct_td(delta.get("total_tokens_pct") if delta else None, True))
    add("Mean turns", lambda st: f"<td>{_num(st.get('mean_turns'))}</td>",
        _delta_pct_td(delta.get("turns_pct") if delta else None, True))
    add("Mean wall (s)", lambda st: f"<td>{_num(st.get('mean_wall_s'))}</td>",
        _delta_pct_td(delta.get("wall_pct") if delta else None, True))
    add("Timeouts", lambda st: f"<td>{_timeout_cell(st)}</td>",
        _delta_timeout_td(delta))
    add("Runs (graded)", lambda st: f"<td class='muted'>{st.get('n_runs')} ({st.get('n_graded')})</td>",
        "<td class='delta neutral'>&ndash;</td>")

    return (f"<table class='arms'><thead><tr><th class='metric'></th>{head}</tr></thead>"
            f"<tbody>{''.join(rows)}</tbody></table>")


def _bar_pair(label: str, baseline_val, aracne_val, unit: str, lower_is_better: bool,
              aracne_label: str = "aracne") -> str:
    """Two proportional bars (control vs treatment) for one headline metric."""
    def w(v, mx):
        return 0 if (v is None or not mx) else max(2, round(v / mx * 100))

    def fmt(v):
        if v is None:
            return "&ndash;"
        if unit == "%":
            return f"{v:.0%}"
        if unit == "k":
            return _k(v)
        return f"{v:g}{unit}"

    mx = max(baseline_val or 0, aracne_val or 0)
    good_hint = "lower is better" if lower_is_better else "higher is better"
    return (
        f"<div class='barblock'><div class='barlabel'>{_esc(label)} "
        f"<span class='muted'>({good_hint})</span></div>"
        f"<div class='barrow'><span class='barname'>baseline</span>"
        f"<span class='bartrack'><span class='bar' style='width:{w(baseline_val, mx)}%;background:{C_BASELINE}'></span></span>"
        f"<span class='barval'>{fmt(baseline_val)}</span></div>"
        f"<div class='barrow'><span class='barname'>{_esc(aracne_label)}</span>"
        f"<span class='bartrack'><span class='bar' style='width:{w(aracne_val, mx)}%;background:{C_ARACNE}'></span></span>"
        f"<span class='barval'>{fmt(aracne_val)}</span></div></div>")


def _primary_treatment(arms: dict) -> str:
    """The treatment arm the headline tiles and bars describe.

    Prefers the arm literally named "aracne" so existing two-arm runs render exactly as
    before, and otherwise takes the first non-control arm -- without which a run whose
    treatment is named anything else renders headline tiles full of blanks.
    """
    if "aracne" in arms:
        return "aracne"
    others = sorted(a for a in arms if a != "baseline")
    return others[0] if others else "aracne"


def _tiles(overall: dict) -> str:
    d = overall.get("delta")
    arms = overall.get("arms", {})
    primary = _primary_treatment(arms)
    b, a = arms.get("baseline", {}), arms.get(primary, {})
    if not overall.get("delta") and overall.get("delta_by_arm"):
        d = overall["delta_by_arm"].get(primary)

    def tile(label, value, cls, sub=""):
        return (f"<div class='tile'><div class='tlabel'>{_esc(label)}</div>"
                f"<div class='tval {cls}'>{value}</div>"
                f"<div class='tsub muted'>{sub}</div></div>")

    tiles = []
    # Success rate delta
    if d and d.get("success_rate_abs") is not None:
        sr = d["success_rate_abs"]
        cls = "good" if sr > 0 else ("bad" if sr < 0 else "neutral")
        tiles.append(tile("Success rate Δ", f"{sr * 100:+.1f}pp", cls,
                          f"{_pct_of(a.get('success_rate'))} vs {_pct_of(b.get('success_rate'))}"))
    elif d:
        s = d.get("solved_abs", 0)
        cls = "good" if s > 0 else ("bad" if s < 0 else "neutral")
        tiles.append(tile("Solved Δ", f"{s:+d}", cls, "rates unavailable"))
    else:
        tiles.append(tile("Success rate Δ", "&ndash;", "neutral", "need both arms graded"))

    for label, key in (("Context tokens Δ", "context_tokens_pct"),
                       ("Turns Δ", "turns_pct"),
                       ("Wall time Δ", "wall_pct")):
        v = d.get(key) if d else None
        cls = _delta_class(v, good_when_negative=True)
        val = "&ndash;" if v is None else f"{v:+.0f}%"
        tiles.append(tile(label, val, cls, "aracne vs baseline"))

    return f"<div class='tiles'>{''.join(tiles)}</div>"


def _pct_of(v) -> str:
    return "&ndash;" if v is None else f"{v:.0%}"


def _render_markdown(text: str) -> str:
    """Tiny, injection-safe markdown -> HTML (escape FIRST, then decorate)."""
    if not text:
        return "<p class='muted'>No analysis was generated for this run.</p>"
    esc = _esc(text)
    esc = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", esc)
    esc = re.sub(r"(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)", r"<em>\1</em>", esc)
    esc = re.sub(r"`([^`]+)`", r"<code>\1</code>", esc)

    out: list[str] = []
    in_list = False
    for raw in esc.splitlines():
        line = raw.rstrip()
        m = re.match(r"^\s*(?:[-*]|\d+\.)\s+(.*)$", line)
        if m:
            if not in_list:
                out.append("<ul>")
                in_list = True
            out.append(f"<li>{m.group(1)}</li>")
        else:
            if in_list:
                out.append("</ul>")
                in_list = False
            if line.strip():
                out.append(f"<p>{line.strip()}</p>")
    if in_list:
        out.append("</ul>")
    return "\n".join(out)


_STYLE = """
:root { color-scheme: light; }
* { box-sizing: border-box; }
body { margin: 0; padding: 2rem 1.25rem 3rem; background: #f1f5f9; color: #334155;
       font: 15px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
.wrap { max-width: 960px; margin: 0 auto; }
h1 { font-size: 1.5rem; margin: 0 0 .25rem; color: #0f172a; }
h2 { font-size: 1.05rem; margin: 2rem 0 .75rem; color: #0f172a; }
.sub { color: #64748b; margin: 0 0 1rem; }
.muted { color: #64748b; }
.card { background: #fff; border: 1px solid #e2e8f0; border-radius: 12px;
        padding: 1.1rem 1.25rem; box-shadow: 0 1px 2px rgba(15,23,42,.04); }
.chips { display: flex; flex-wrap: wrap; gap: .4rem; margin-top: .5rem; }
.chip { background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 999px;
        padding: .18rem .6rem; font-size: .82rem; color: #475569; }
.chip b { color: #0f172a; font-weight: 600; }
.tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
         gap: .75rem; margin: 1rem 0; }
.tile { background: #fff; border: 1px solid #e2e8f0; border-radius: 12px; padding: .85rem 1rem; }
.tlabel { font-size: .78rem; color: #64748b; text-transform: uppercase; letter-spacing: .03em; }
.tval { font-size: 1.6rem; font-weight: 700; margin: .15rem 0; }
.tsub { font-size: .78rem; }
table.arms { width: 100%; border-collapse: collapse; background: #fff;
             border: 1px solid #e2e8f0; border-radius: 12px; overflow: hidden; }
table.arms th, table.arms td { padding: .55rem .8rem; text-align: right; border-bottom: 1px solid #eef2f7; }
table.arms thead th { background: #f8fafc; color: #475569; font-size: .82rem; text-align: right; }
table.arms th.metric { text-align: left; color: #334155; font-weight: 600; white-space: nowrap; }
table.arms tr.emph td, table.arms tr.emph th.metric { background: #fafaff; font-size: 1.02rem; }
table.arms tr:last-child td, table.arms tr:last-child th { border-bottom: none; }
td.delta { font-variant-numeric: tabular-nums; font-weight: 600; }
.good { color: #15803d; }
.bad { color: #b91c1c; }
.neutral { color: #64748b; }
.bars { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
        gap: 1rem 1.5rem; margin-top: .5rem; }
.barblock { min-width: 0; }
.barlabel { font-size: .85rem; margin-bottom: .35rem; color: #334155; }
.barrow { display: flex; align-items: center; gap: .5rem; margin: .2rem 0; }
.barname { width: 64px; font-size: .78rem; color: #64748b; }
.bartrack { flex: 1; height: 12px; background: #eef2f7; border-radius: 6px; overflow: hidden; }
.bar { display: block; height: 100%; border-radius: 6px; }
.barval { width: 72px; text-align: right; font-size: .82rem; font-variant-numeric: tabular-nums; }
.legend { display: flex; gap: 1rem; font-size: .8rem; color: #64748b; margin-top: .4rem; }
.legend .sw { display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: .3rem; vertical-align: middle; }
.analysis { line-height: 1.6; }
.analysis ul { margin: .3rem 0 .3rem 1.1rem; padding: 0; }
.analysis li { margin: .25rem 0; }
.analysis code { background: #f1f5f9; padding: .05rem .3rem; border-radius: 4px; font-size: .9em; }
.foot { margin-top: 2rem; font-size: .8rem; color: #94a3b8; }
.foot li { margin: .2rem 0; }
.langgrid { display: grid; gap: 1rem; }
.lang h3 { margin: 0 0 .5rem; font-size: .95rem; color: #334155; }
"""


def _chips(meta: dict) -> str:
    fields = [
        ("run", meta.get("run_name")),
        ("harness", meta.get("run_harness")),
        ("model", meta.get("model")),
        ("sample", meta.get("sample_id")),
        ("aracne cfg", meta.get("aracne_config")),
        ("seeds", meta.get("seeds")),
        ("arms", ", ".join(meta.get("arms", []) or [])),
        ("max_turns", meta.get("max_turns")),
        # Shown because >1 means wall-clock was measured under contention.
        ("parallel", meta.get("run_parallel") if (meta.get("run_parallel") or 1) > 1 else None),
        ("time", meta.get("timestamp")),
    ]
    return "".join(f"<span class='chip'><b>{_esc(k)}</b> {_esc(v)}</span>"
                   for k, v in fields if v not in (None, ""))


def _lang_sections(agg: dict) -> str:
    blocks = []
    for lang, block in agg.get("per_language", {}).items():
        arms = block.get("arms", {})
        if all(st.get("n_runs", 0) == 0 for st in arms.values()):
            continue
        blocks.append(f"<div class='lang'><h3>{_esc(lang)}</h3>"
                      f"{_arms_table(arms, block.get('delta'))}</div>")
    if not blocks:
        return "<p class='muted'>No per-language rows.</p>"
    return f"<div class='langgrid'>{''.join(blocks)}</div>"


def _sig_td(significant: bool) -> str:
    cls = "good" if significant else "neutral"
    label = "yes" if significant else "no"
    return f"<td class='delta {cls}'>{label}</td>"


def _ratio_rows(eff: dict) -> str:
    """One row per efficiency endpoint: ratio, % change, CI, significance."""
    rows = []
    for name, e in (eff or {}).items():
        if not e:
            continue
        ci = e.get("pct_change_ci")
        ci_s = f"[{ci[0]:+.0f}%, {ci[1]:+.0f}%]" if ci else "&ndash;"
        pct = e.get("pct_change")
        rows.append(
            f"<tr><td class='metric'>{_esc(name.replace('_', ' '))}</td>"
            f"<td>{e.get('ratio')}&times;</td>"
            f"{_delta_pct_td(pct, True)}"
            f"<td class='muted'>{ci_s}</td>"
            f"{_sig_td(bool(e.get('significant')))}"
            f"<td class='muted'>{e.get('n_pairs')} / {e.get('n_clusters')}</td></tr>")
    return "".join(rows)


def _paired_table(pr: dict | None, title: str) -> str:
    """The within-task analysis — the block that actually supports a claim.

    It lived only in console output and results.json before; the HTML report showed the
    pooled means and the prose and nothing else, which is how a reader could come away
    with the pooled number and never see that it was two opposite effects cancelling.
    """
    if not pr or not pr.get("n_pairs"):
        return f"<div class='card'><b>{_esc(title)}</b>" \
               "<p class='muted'>No matched task pairs.</p></div>"
    s_ = pr["solve"]
    head = (f"<p class='muted'>{pr['n_pairs']} matched pairs over "
            f"<b>{pr['effective_n']}</b> distinct repositories "
            f"(effective N; unpaired dropped {pr['n_unpaired']}). "
            f"A ratio is a result only when its 95% CI excludes 1.0.</p>")
    solve = (f"<p class='muted'>solve: both {s_['both_solved']}, aracne-only "
             f"{s_['aracne_only']}, baseline-only {s_['baseline_only']}, neither "
             f"{s_['neither_solved']} &mdash; McNemar exact p = {s_['mcnemar_p']}; "
             f"non-inferior at {s_['margin']} margin: <b>{s_['noninferior']}</b></p>")
    return (f"<div class='card'><b>{_esc(title)}</b>{head}"
            "<table class='arms'><tr><th>metric</th><th>ratio</th><th>change</th>"
            "<th>95% CI</th><th>sig</th><th>pairs / repos</th></tr>"
            f"{_ratio_rows(pr.get('efficiency'))}</table>{solve}</div>")


def _size_table(bs: dict | None) -> str:
    """Context-token ratio by repo size — the direct test of aracne's core premise that
    the navigation edge WIDENS with codebase size."""
    if not bs or not bs.get("available"):
        reason = (bs or {}).get("reason", "no repo_nodes recorded")
        return f"<div class='card'><b>Context tokens by repo size</b>" \
               f"<p class='muted'>Unavailable ({_esc(reason)}).</p></div>"
    rows = []
    for label, b in (bs.get("buckets") or {}).items():
        if not b or not b.get("n_pairs"):
            rows.append(f"<tr><td class='metric'>{_esc(label)}</td>"
                        "<td class='muted' colspan='5'>no pairs</td></tr>")
            continue
        lo, hi = b.get("range", [None, None])
        ci = b.get("pct_change_ci")
        ci_s = f"[{ci[0]:+.0f}%, {ci[1]:+.0f}%]" if ci else "&ndash;"
        rows.append(
            f"<tr><td class='metric'>{_esc(label)}</td>"
            f"<td class='muted'>{lo}&ndash;{hi if hi else '&infin;'}</td>"
            f"<td>{b.get('ratio')}&times;</td>"
            f"{_delta_pct_td(b.get('pct_change'), True)}"
            f"<td class='muted'>{ci_s}</td>"
            f"{_sig_td(bool(b.get('significant')))}</tr>")
    slope = (f"<p class='muted'>slope of log(size) vs log(ratio): r = "
             f"{bs.get('slope_r')} CI {bs.get('slope_ci')} &mdash; "
             f"grows_with_size = <b>{bs.get('grows_with_size')}</b> "
             f"(true only when the interval stays entirely below zero)</p>")
    return ("<div class='card'><b>Context tokens by repo size</b>"
            "<table class='arms'><tr><th>bucket</th><th>nodes</th><th>ratio</th>"
            "<th>change</th><th>95% CI</th><th>sig</th></tr>"
            f"{''.join(rows)}</table>{slope}</div>")


def _paired_sections(agg: dict) -> str:
    """Overall paired block, the size split, then a paired block per language.

    Per-language is rendered because a pooled null can be two large opposite significant
    effects cancelling — which is exactly what run-20260824-125118 turned out to be.
    """
    parts = [_paired_table(agg.get("paired"), "Overall (paired, authoritative)"),
             _size_table((agg.get("paired") or {}).get("by_size"))]
    langs = []
    for lang, block in agg.get("per_language", {}).items():
        pr = block.get("paired")
        if pr and pr.get("n_pairs"):
            langs.append(_paired_table(pr, lang))
    if langs:
        parts.append(f"<div class='langgrid'>{''.join(langs)}</div>")
    return "".join(parts)


def _footnotes(overall: dict) -> str:
    notes = [
        "Success rate is over graded runs only; small samples carry wide confidence intervals.",
        "Timeouts (agent or grading wall-clock) are a separate outcome — excluded from the "
        "success-rate denominator, and never counted as failures.",
        "Δ is aracne − baseline. For tokens / turns / wall, negative (green) means aracne used less.",
        "Dollar cost is ~0 on the Max plan and is excluded from the headline metrics.",
    ]
    arms = overall.get("arms", {})
    if any(st.get("n_tokens", 0) < st.get("n_metric", 0) for st in arms.values()):
        notes.insert(1, "Some runs reported no token usage (typically the OpenCode backend); "
                        "token means cover only runs that did, while turns / wall cover all runs.")
    return "<ul>" + "".join(f"<li>{n}</li>" for n in notes) + "</ul>"


def _render(agg: dict, rows: list[dict], meta: dict, analysis_text: str | None) -> str:
    overall = agg.get("overall", {})
    arms = overall.get("arms", {})
    primary = _primary_treatment(arms)
    b, a = arms.get("baseline", {}), arms.get(primary, {})

    bars = "".join([
        _bar_pair("Success rate", b.get("success_rate"), a.get("success_rate"), "%", False, primary),
        _bar_pair("Mean context tokens", b.get("mean_context_tokens"), a.get("mean_context_tokens"), "k", True, primary),
        _bar_pair("Mean turns", b.get("mean_turns"), a.get("mean_turns"), "", True, primary),
        _bar_pair("Mean wall (s)", b.get("mean_wall_s"), a.get("mean_wall_s"), "s", True, primary),
    ])
    legend = (f"<div class='legend'><span><span class='sw' style='background:{C_BASELINE}'></span>baseline</span>"
              f"<span><span class='sw' style='background:{C_ARACNE}'></span>aracne</span></div>")

    return f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>aracne benchmark &mdash; {_esc(meta.get('run_name', 'run'))}</title>
<style>{_STYLE}</style></head>
<body><div class="wrap">
  <h1>aracne benchmark report</h1>
  <p class="sub">A/B: <b>baseline</b> (vanilla Claude Code) vs <b>aracne</b> (Claude Code + aracne tooling), same model &amp; harness.</p>
  <div class="card"><div class="chips">{_chips(meta)}</div></div>

  <h2>Headline &mdash; how aracne changed the run</h2>
  {_tiles(overall)}

  <h2>Overall</h2>
  {_arms_table(arms, overall.get('delta'))}

  <h2>Paired analysis &mdash; within-task, the claim-bearing numbers</h2>
  {_paired_sections(agg)}

  <h2>Visual comparison</h2>
  <div class="card"><div class="bars">{bars}</div>{legend}</div>

  <h2>Analysis</h2>
  <div class="card analysis">{_render_markdown(analysis_text or '')}</div>

  <h2>Per language</h2>
  {_lang_sections(agg)}

  <div class="foot"><b>Notes</b>{_footnotes(overall)}</div>
</div></body></html>
"""


def write_html(agg: dict, rows: list[dict], meta: dict, analysis_text: str | None, out_dir: Path) -> None:
    (out_dir / "report.html").write_text(_render(agg, rows, meta, analysis_text), encoding="utf-8")

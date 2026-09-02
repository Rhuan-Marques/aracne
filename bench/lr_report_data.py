#!/usr/bin/env python3
"""Collect the line-range trial into one JSON blob for the report."""
from __future__ import annotations
import json, sys, math, statistics, glob, re, os, sqlite3, collections
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import rowmetrics

RUN = Path(sys.argv[1])
rows = [json.loads(l) for l in (RUN/"runs.jsonl").read_text().splitlines() if l.strip()]
results = json.loads((RUN/"results.json").read_text()) if (RUN/"results.json").exists() else {}
meta = json.loads((RUN/"run_meta.json").read_text()) if (RUN/"run_meta.json").exists() else {}
ARMS = ["baseline", "aracne"]
by = {a: {r["instance_id"]: r for r in rows if r.get("arm") == a} for a in ARMS}
common = sorted(set(by["baseline"]) & set(by["aracne"]))
ctx = rowmetrics.context_tokens

def med(v): return statistics.median(v) if v else None
def geo(v): return math.exp(statistics.fmean(math.log(x) for x in v)) if v else None

def ratio(pred=None):
    out = []
    for i in common:
        b, a = by["baseline"][i], by["aracne"][i]
        if ctx(b) <= 0 or ctx(a) <= 0: continue
        if pred and not pred(b, a): continue
        out.append(ctx(a)/ctx(b))
    return geo(out), len(out)

solved = lambda a: sum(1 for i in common if by[a][i].get("success"))
graded = lambda a: sum(1 for i in common if by[a][i].get("success") is not None)

D = {
  "run": meta.get("run_name"), "ts": meta.get("timestamp"),
  "cli": (meta.get("agent_cli") or {}).get("version"),
  "model": meta.get("model"), "turns": meta.get("max_turns"), "effort": meta.get("effort"),
  "n": len(common),
  "solved": {a: solved(a) for a in ARMS}, "graded": {a: graded(a) for a in ARMS},
  "cost": [], "telemetry": [], "tasks": [], "paired": None,
}
for k, lab, fn, d in [("context","Context tokens",ctx,0),
                      ("total","Total tokens",rowmetrics.total_tokens,0),
                      ("output","Output tokens",rowmetrics.output_tokens,0),
                      ("turns","Turns",rowmetrics.turns,1),
                      ("cost","Cost (USD)",rowmetrics.cost_usd,3),
                      ("wall","Wall seconds",rowmetrics.wall_s,0)]:
    D["cost"].append({"key":k,"label":lab,"dec":d,
                      "arms":{a: med([fn(by[a][i]) for i in common]) for a in ARMS}})
for k, lab in [("n_tool_calls","Tool calls"),("shell_read_result_bytes","Shell read bytes"),
               ("shell_grep_result_bytes","Shell grep bytes"),("n_intercepted","Reads aracne answered"),
               ("terminal_result_bytes","Bytes aracne returned"),("native_result_bytes","All tool-result bytes"),
               ("n_mcp_calls","MCP calls"),("n_guard_denials","Guard denials")]:
    D["telemetry"].append({"label":lab,
                           "arms":{a: med([by[a][i].get(k) or 0 for i in common]) for a in ARMS}})
g_all, n_all = ratio()
g_solved, n_solved = ratio(lambda b,a: b.get("success") and a.get("success"))
D["ratios"] = {"all": g_all, "n_all": n_all, "both_solved": g_solved, "n_solved": n_solved}
D["split"] = {a: {"solved": med([ctx(by[a][i]) for i in common if by[a][i].get("success")]),
                  "unsolved": med([ctx(by[a][i]) for i in common if by[a][i].get("success") is False]),
                  "n_solved": sum(1 for i in common if by[a][i].get("success")),
                  "n_unsolved": sum(1 for i in common if by[a][i].get("success") is False)} for a in ARMS}
for i in common:
    D["tasks"].append({"id": i, "lang": by["aracne"][i].get("language"), "repo": by["aracne"][i].get("repo"),
                       **{a: {"ctx": ctx(by[a][i]), "turns": rowmetrics.turns(by[a][i]),
                              "ok": bool(by[a][i].get("success"))} for a in ARMS}})
p = (results.get("paired_by_arm") or {}).get("aracne") or results.get("paired")
if p:
    e = p["efficiency"]; s = p["solve"]
    D["paired"] = {"n": p["n_pairs"], "clusters": p["effective_n"],
                   "metrics": {k: {"ratio": (e.get(k) or {}).get("ratio"),
                                   "ci": (e.get(k) or {}).get("ratio_ci"),
                                   "pct": (e.get(k) or {}).get("pct_change"),
                                   "sig": (e.get(k) or {}).get("significant")}
                               for k in ("context_tokens","total_tokens","output_tokens","turns","wall_s")},
                   "solve": {"both": s["both_solved"], "aracne_only": s["aracne_only"],
                             "baseline_only": s["baseline_only"], "neither": s["neither_solved"],
                             "p": s["mcnemar_p"], "ni": s["noninferior"]}}

# --- behavioural: do read ranges land on declaration boundaries? -------------
inst2repo = {r["instance_id"]: f"{r.get('org')}__{r.get('repo')}" for r in rows}
byrepo = collections.defaultdict(list)
for d in glob.glob("fixtures/*/worktree/.aracne/topology.db"):
    byrepo[d.split("/")[1].split("@")[0]].append(d)
SED = re.compile(r"sed -n\s*'?(\d+)\s*,\s*(\d+)\s*p"); AWK = re.compile(r"NR\s*>=\s*(\d+)\s*&&\s*NR\s*<=\s*(\d+)")
cache = {}
def spans(repo):
    if repo not in cache:
        s = []
        for db in byrepo.get(repo, []):
            try: s += list(sqlite3.connect(db).execute(
                "select starts_at,ends_at from resources where starts_at>0 and ends_at>=starts_at"))
            except Exception: pass
        cache[repo] = s
    return cache[repo]
align = {}
for arm in ARMS:
    tot = 0; hit = collections.Counter()
    for p_ in glob.glob(f"{RUN}/transcripts/*__{arm}__s0.jsonl"):
        S = spans(inst2repo.get(os.path.basename(p_).split("__"+arm)[0]))
        if not S: continue
        for l in open(p_, errors="replace"):
            l = l.strip()
            if not l: continue
            try: e = json.loads(l)
            except Exception: continue
            if e.get("type") != "assistant": continue
            for blk in e.get("message", {}).get("content", []) or []:
                if isinstance(blk, dict) and blk.get("type") == "tool_use" and blk.get("name") == "Bash":
                    c = (blk.get("input") or {}).get("command", "")
                    if not isinstance(c, str): continue
                    for rx in (SED, AWK):
                        for m in rx.finditer(c):
                            a_, b_ = int(m.group(1)), int(m.group(2)); tot += 1
                            for t in (0, 2, 5):
                                if any(abs(sa-a_) <= t and abs(sb-b_) <= t for sa, sb in S): hit[t] += 1
    align[arm] = {"ranged": tot, **{f"t{t}": hit[t] for t in (0, 2, 5)}}
D["alignment"] = align
print(json.dumps(D, indent=2, default=str))

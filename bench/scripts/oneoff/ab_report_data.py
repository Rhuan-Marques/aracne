#!/usr/bin/env python3
"""Collect everything the A/B cost report needs into one JSON blob."""
from __future__ import annotations
import json, sys, statistics
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import rowmetrics

RUN = Path(sys.argv[1])
rows = [json.loads(l) for l in (RUN / "runs.jsonl").read_text().splitlines() if l.strip()]
results = json.loads((RUN / "results.json").read_text()) if (RUN / "results.json").exists() else {}
meta = json.loads((RUN / "run_meta.json").read_text()) if (RUN / "run_meta.json").exists() else {}

ARMS = ["baseline", "aracne", "aracne-noprefer"]
by_arm = {a: [r for r in rows if r.get("arm") == a] for a in ARMS}

def graded(r):
    return r.get("outcome") == "graded" or (r.get("success") is not None and not r.get("error"))

METRICS = [
    ("context_tokens", "Context tokens", rowmetrics.context_tokens),
    ("total_tokens",   "Total tokens",   rowmetrics.total_tokens),
    ("output_tokens",  "Output tokens",  rowmetrics.output_tokens),
    ("turns",          "Turns",          rowmetrics.turns),
    ("cost_usd",       "Cost (USD)",     rowmetrics.cost_usd),
    ("wall_s",         "Wall seconds",   rowmetrics.wall_s),
]
TELEM = [
    ("n_tool_calls", "Tool calls"),
    ("shell_read_result_bytes", "Shell read bytes"),
    ("shell_grep_result_bytes", "Shell grep bytes"),
    ("n_intercepted", "Intercepted reads"),
    ("terminal_result_bytes", "Aracne answer bytes"),
    ("n_mcp_calls", "MCP calls"),
    ("n_guard_denials", "Guard denials"),
]

def med(v): return statistics.median(v) if v else None
def mean(v): return sum(v)/len(v) if v else None

# Per-arm pooled descriptives over cells every arm has, so the columns compare like with like.
common = set.intersection(*[{r["instance_id"] for r in by_arm[a] if graded(r)} for a in ARMS if by_arm[a]]) \
         if all(by_arm[a] for a in ARMS) else set()

out = {
    "run_name": meta.get("run_name"), "timestamp": meta.get("timestamp"),
    "updated": meta.get("updated"),
    "agent_cli": (meta.get("agent_cli") or {}).get("version"),
    "model": meta.get("model"), "max_turns": meta.get("max_turns"), "effort": meta.get("effort"),
    "n_common_tasks": len(common),
    "counts": {a: len(by_arm[a]) for a in ARMS},
    "solved": {a: sum(1 for r in by_arm[a] if r.get("success")) for a in ARMS},
    "graded": {a: sum(1 for r in by_arm[a] if graded(r)) for a in ARMS},
    "metrics": [], "telemetry": [], "per_task": [], "languages": {},
}

for key, label, fn in METRICS:
    entry = {"key": key, "label": label, "arms": {}}
    for a in ARMS:
        vals = [fn(r) for r in by_arm[a] if r["instance_id"] in common]
        vals = [v for v in vals if v is not None]
        entry["arms"][a] = {"median": med(vals), "mean": mean(vals), "n": len(vals)}
    out["metrics"].append(entry)

for key, label in TELEM:
    entry = {"key": key, "label": label, "arms": {}}
    for a in ARMS:
        vals = [r.get(key) or 0 for r in by_arm[a] if r["instance_id"] in common]
        entry["arms"][a] = {"median": med(vals), "mean": mean(vals), "total": sum(vals)}
    out["telemetry"].append(entry)

# Per-task context tokens, for the scatter/table.
idx = {a: {r["instance_id"]: r for r in by_arm[a]} for a in ARMS}
for inst in sorted(common):
    row = {"instance_id": inst, "language": idx["aracne"][inst].get("language"),
           "repo": idx["aracne"][inst].get("repo")}
    for a in ARMS:
        r = idx[a][inst]
        row[a] = {"context": rowmetrics.context_tokens(r), "turns": rowmetrics.turns(r),
                  "cost": rowmetrics.cost_usd(r), "success": bool(r.get("success"))}
    out["per_task"].append(row)

langs = {}
for row in out["per_task"]:
    langs.setdefault(row["language"], []).append(row)
for lang, rs in langs.items():
    out["languages"][lang] = {
        "n": len(rs),
        **{a: {"context": med([r[a]["context"] for r in rs]),
               "solved": sum(1 for r in rs if r[a]["success"])} for a in ARMS},
    }

out["paired_ab"] = results.get("paired_ab")
out["paired_by_arm"] = results.get("paired_by_arm")
print(json.dumps(out, indent=2, default=str))

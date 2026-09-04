#!/usr/bin/env python3
"""Recompute the post-hoc toolstats counters for rows imported from an older run.

WHY. `n_intercepted`, `terminal_result_bytes`, `shell_read_result_bytes` and
`shell_grep_result_bytes` were added after fair-20260901a. Rows imported from that run have no
value for them, and `row_fields` fills 0 -- so a baseline arm that in fact spent ~600 KB reading
files through the shell reports `shell_read_result_bytes: 0`. That is not a small error in a
diagnostic; it is the exact opposite of the truth, in the one column that makes the control's
discovery cost comparable to an aracne arm's.

It is recoverable because the reduced transcript keeps what these counters need. `toolstats`
elides result BODIES but writes their size into the placeholder text ("<N bytes elided by
toolstats>"), and it never touches the `tool_use` blocks, so the command that produced each
result is still there. Byte counts and command words are all these four fields read.

Usage:
    python backfill_shellstats.py results/<run-name>            # rewrite runs.jsonl in place
    python backfill_shellstats.py results/<run-name> --dry-run  # report only
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import toolstats  # noqa: E402

BACKFILLED = ("n_intercepted", "terminal_result_bytes",
              "shell_read_result_bytes", "shell_grep_result_bytes")

# toolstats replaces every result body with this, preserving the size it removed.
ELIDED = re.compile(r"<(\d+) bytes elided by toolstats>")


def _blocks(event: dict):
    msg = event.get("message")
    return (msg or {}).get("content") or [] if isinstance(msg, dict) else []


def recount(transcript: Path) -> dict:
    """Re-derive the four fields from a size-reduced transcript."""
    stats = {k: 0 for k in BACKFILLED}
    names: dict[str, str] = {}
    shell_kind: dict[str, str] = {}
    intercepted: set[str] = set()

    for line in transcript.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") == "assistant":
            for b in _blocks(event):
                if not isinstance(b, dict) or b.get("type") != "tool_use":
                    continue
                names[b.get("id")] = b.get("name") or "?"
                if b.get("name") != "Bash":
                    continue
                cmd = (b.get("input") or {}).get("command", "")
                kind = toolstats._shell_discovery_kind(cmd)
                if kind:
                    shell_kind[b.get("id")] = kind
                if isinstance(cmd, str) and toolstats._INTERCEPT_RE.search(cmd):
                    intercepted.add(b.get("id"))
        elif event.get("type") == "user":
            for b in _blocks(event):
                if not isinstance(b, dict) or b.get("type") != "tool_result":
                    continue
                tid = b.get("tool_use_id")
                text = b.get("content") if isinstance(b.get("content"), str) else ""
                m = ELIDED.search(text or "")
                # A body that was elided reports its own size; one small enough to survive
                # verbatim is measured directly.
                nbytes = int(m.group(1)) if m else len((text or "").encode("utf-8", "replace"))
                # An elided body cannot be inspected for aracne's signatures, so interception
                # is detected from the command side only. On a control arm that is exactly
                # right -- it never intercepts anything -- and it is why this script is for
                # BACKFILL and not a substitute for measuring a run properly.
                if not m and toolstats._looks_enriched(text or ""):
                    intercepted.add(tid)
                    stats["terminal_result_bytes"] += nbytes
                kind = shell_kind.get(tid)
                if kind == "read":
                    stats["shell_read_result_bytes"] += nbytes
                elif kind == "grep":
                    stats["shell_grep_result_bytes"] += nbytes
    stats["n_intercepted"] = len(intercepted)
    return stats


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("run_dir", type=Path)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    runs = args.run_dir / "runs.jsonl"
    if not runs.exists():
        print(f"no runs.jsonl at {runs}", file=sys.stderr)
        return 1

    out, patched, skipped, missing = [], 0, 0, 0
    for line in runs.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        # Only rows that predate the fields. A row measured by this build already has them and
        # must not be second-guessed by a reconstruction.
        if not row.get("imported_from") or all(k in row for k in BACKFILLED):
            skipped += 1
            out.append(row)
            continue
        tp = row.get("transcript_path")
        path = Path(tp) if tp else None
        if path and not path.is_absolute():
            path = args.run_dir.parent.parent / tp
        if not path or not path.exists():
            # Source the transcript from the run the row came from.
            src = Path("results") / row["imported_from"] / "transcripts"
            cand = src / f"{row['instance_id']}__{row['arm']}__s{row.get('seed', 0)}.jsonl"
            path = cand if cand.exists() else None
        if not path:
            missing += 1
            out.append(row)
            continue
        row.update(recount(path))
        row["shellstats_backfilled_from"] = str(path)
        patched += 1
        out.append(row)

    print(f"patched {patched}, already-current {skipped}, no transcript {missing}")
    if patched:
        tot = {k: sum(r.get(k, 0) for r in out if r.get("shellstats_backfilled_from")) for k in BACKFILLED}
        print("backfilled totals:", tot)
    if args.dry_run:
        print("(dry run: runs.jsonl not written)")
        return 0
    runs.write_text("".join(json.dumps(r) + "\n" for r in out), encoding="utf-8")
    print(f"rewrote {runs}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

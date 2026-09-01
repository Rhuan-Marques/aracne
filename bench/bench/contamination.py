"""Which cells reached the answer key, read back out of stored transcripts.

WHY THIS IS A SEPARATE MODULE. SWE-bench's answer is a public URL derived mechanically from
the instance id, so "did this cell look it up" is a property of the transcript, not of the
harness that produced it. Three facts forced the logic out of `toolstats` and into its own
place:

  1. A run IMPORTS its baseline (`--baseline-from`), and imported rows predate whatever the
     counter meant when they were written. `opus-medium-fixed` carried 27 baseline rows with
     no answer-key field at all, and `paired._graded` reads a missing field as clean -- so two
     baseline cells that curl'd the fix were being scored as baseline WINS, one of them a
     baseline-only win over an aracne failure. That is a full point of solve-rate handed to
     the control by a lookup.
  2. The stored transcript is not the live one. `toolstats.summarize` REPLACES a result body
     with its byte count on the way out (which is what keeps transcripts small enough to
     keep), so an after-the-fact scan has to read the sibling `tool_use_result` field instead.
  3. The answer is only worth acting on BEFORE the matrix runs -- skipping a compromised task
     costs nothing, while discovering it afterwards costs the cell.

`toolstats` still counts live, during a run. This module is the archival reader.
"""
from __future__ import annotations

import json
import re
from pathlib import Path

RESULTS = Path(__file__).resolve().parent.parent / "results"

# Kept in step with toolstats._NETWORK_RE / _BLOCKED_FETCH / _WRITES_TO_FILE. Deliberately a
# copy, not an import: this module must be able to read a run recorded by an older harness,
# which is the whole reason it exists.
NET_RE = re.compile(r"(?<![\w./-])(?:curl|wget|gh)\s|git\s+(?:fetch|clone|pull)\b")
BLOCKED = ("aracne-bench: refusing to fetch", "No such file or directory", "command not found")
# `curl -o file` prints nothing on success, so an empty result only proves nothing came back
# when the command was not writing the body somewhere else.
WRITES_TO_FILE = re.compile(
    r"(?:^|\s)(?:-o|--output)(?:\s|=)|(?<![\d&])>>?\s*(?!&|/dev/null)[^\s|;&]+")


def answer_key_for(instance_id: str) -> str:
    """`org/repo` from an instance id like `sveltejs__svelte-14456`."""
    org, rest = instance_id.split("__", 1)
    return f"{org}/{rest.rsplit('-', 1)[0]}"


def returned_nothing(command, text: str) -> bool:
    """Did the attempt come back empty? An attempt that failed is not contamination."""
    if any(m in text for m in BLOCKED):
        return True
    body = text.strip()
    if body in ("", "{}"):
        return True
    try:
        payload = json.loads(body)
    except (ValueError, TypeError):
        return False
    if not isinstance(payload, dict) or "stdout" not in payload:
        return False
    if (payload.get("stdout") or "").strip() or (payload.get("stderr") or "").strip():
        return False
    return not (isinstance(command, str) and WRITES_TO_FILE.search(command))


def _result_text(event: dict) -> str:
    """The tool result payload as STORED, which lives on `tool_use_result` after reduction."""
    raw = event.get("tool_use_result")
    if isinstance(raw, list):
        return "".join(p.get("text", "") for p in raw if isinstance(p, dict))
    if isinstance(raw, str):
        return raw
    if raw is None:
        return ""
    return json.dumps(raw)


def scan(path: Path, answer_key: str) -> tuple[int, int]:
    """(attempts, fetches) for one transcript. An attempt only counts as a fetch if data came back."""
    attempts = fetches = 0
    pending: dict = {}
    for line in path.read_text(errors="replace").splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") == "assistant":
            for block in (event.get("message") or {}).get("content") or []:
                if not isinstance(block, dict) or block.get("type") != "tool_use":
                    continue
                name = block.get("name") or ""
                inp = block.get("input") or {}
                command = inp.get("command")
                is_net = name == "WebFetch" or (
                    isinstance(command, str) and NET_RE.search(command))
                if not is_net:
                    continue
                blob = command if isinstance(command, str) else json.dumps(inp)
                if answer_key in blob:
                    attempts += 1
                    pending[block.get("id")] = command
        elif event.get("type") == "user":
            text = _result_text(event)
            for block in (event.get("message") or {}).get("content") or []:
                if not isinstance(block, dict) or block.get("type") != "tool_result":
                    continue
                tid = block.get("tool_use_id")
                if tid in pending and not returned_nothing(pending.pop(tid), text):
                    fetches += 1
    return attempts, fetches


def run_dir(name) -> Path:
    """Resolve a run name or results-directory path to that directory."""
    p = Path(name)
    return p if (p / "run_meta.json").exists() else RESULTS / str(name)


def baseline_source(start: Path) -> str | None:
    """The run this one imported its non-measured arms from, or None.

    Read from run_meta's embedded config rather than from a row: `_import_prior_rows` STAMPS
    `imported_from` with its own source, overwriting whatever the row carried in, so after two
    hops every row claims to come from the middle run and the trail to the transcripts is
    gone. The run's config snapshot is the only place the chain survives intact.
    """
    meta = start / "run_meta.json"
    if not meta.is_file():
        return None
    try:
        data = json.loads(meta.read_text(encoding="utf-8"))
    except (ValueError, OSError):
        return None
    return data.get("baseline_from") or (data.get("config") or {}).get("baseline_from")


def transcript_for(row: dict, start: Path) -> Path | None:
    """Find a row's transcript, walking the import chain when the row was reused.

    netguard-20260831b <- opus-medium-fixed <- opus-medium, where only the last actually holds
    the baseline transcripts. Without the walk every imported baseline reads as "no transcript,
    therefore clean" -- silence mistaken for evidence, which is precisely how two curl'd
    baseline solves stayed in the scored matrix.
    """
    name = f"{row['instance_id']}__{row['arm']}__s{row['seed']}.jsonl"
    seen = set()
    here = start
    while here is not None and str(here) not in seen:
        seen.add(str(here))
        candidate = here / "transcripts" / name
        if candidate.is_file():
            return candidate
        nxt = baseline_source(here) or (row.get("imported_from") if here == start else None)
        here = run_dir(nxt) if nxt else None
    return None


def audit(run, arms=None) -> dict:
    """Per-cell (attempts, fetches) for a finished run, computed from transcripts.

    Returns {(instance_id, arm, seed): {...}}. A cell whose transcript cannot be found is
    reported with `transcript=None` rather than as clean, because those are exactly the
    imported rows this exists to stop trusting blindly.
    """
    start = run_dir(run)
    rows_path = start / "runs.jsonl"
    if not rows_path.is_file():
        raise FileNotFoundError(f"no runs.jsonl at {rows_path}")
    out = {}
    for line in rows_path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        if arms and row.get("arm") not in arms:
            continue
        path = transcript_for(row, start)
        attempts, fetches = scan(path, answer_key_for(row["instance_id"])) if path else (0, 0)
        out[(row["instance_id"], row["arm"], row.get("seed", 0))] = {
            "attempts": attempts,
            "fetches": fetches,
            "transcript": str(path) if path else None,
            "recorded": row.get("n_answer_key_fetches"),
            "success": row.get("success"),
            "outcome": row.get("outcome"),
        }
    return out


def contaminated_instances(run, arms=("baseline",)) -> dict:
    """{instance_id: info} for cells in `arms` that actually got answer-key data back.

    Keyed by instance so the caller can drop the TASK: a compromised control makes the pair
    uninterpretable, and running the other arm against it only spends money to produce a
    number that has to be thrown away.
    """
    hits = {}
    for (inst, arm, seed), info in audit(run, arms=arms).items():
        if info["fetches"]:
            prior = hits.get(inst)
            if prior is None or info["fetches"] > prior["fetches"]:
                hits[inst] = {**info, "arm": arm, "seed": seed}
    return hits

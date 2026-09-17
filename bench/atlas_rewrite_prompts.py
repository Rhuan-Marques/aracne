#!/usr/bin/env python3
"""Rewrite SWE-Atlas task prompts so they name no files: no file names, file or directory paths,
module/import paths or extensions. Symbols stay.

WHY. A navigation benchmark measures how an agent finds the code a change belongs in. Across the
70 refactoring tasks, the prompt names a median 30% of the files the reference patch edits (60%
counting directories), and 53 of the 70 contain at least one path. A task that says "the handler
in `api/v1/routes.go`" has done the finding for the agent. Symbols (`GetServer`, `NewMetricsEngine`)
stay: an issue a developer would write names the code it is about, and finding where a symbol
lives is still the agent's job.

WHY A MODEL, ONCE, AND A FILE. Deleting paths with a regex leaves "The `GetServer` function in
takes ...". So each prompt is rewritten by a model ONE time and the result is committed to
REWRITES_PATH, keyed by task and by the hash of the text it was rewritten from. Every run reads
the same bytes; a rewrite whose source text changed is refused at load rather than silently
reused (bench/bench/atlas_prompt.py).

WHAT IS CHECKED, deterministically, before a rewrite is accepted (see `problems`):
  - no path-like token remains (PATHLIKE, with the false positives measured on the corpus --
    `GET/POST`, `I/O`, `Rust/C`, `SetMain/RunMain` -- allowed);
  - every identifier of the original that was not part of a path is still present;
  - nothing new: no backticked token the original did not have, and no large growth in length.
A failing rewrite is retried with the problems listed. One that still fails is not written.

    .venv/bin/python bench/atlas_rewrite_prompts.py            # all rf tasks missing a rewrite
    .venv/bin/python bench/atlas_rewrite_prompts.py --force    # regenerate everything
"""
from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import re
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import atlas_judge  # noqa: E402
from bench.atlas_prompt import REWRITES_PATH, file_stems, find_paths, identifiers, strip_and_neutralize  # noqa: E402

ATLAS_RF = Path.home() / ".cache/aracne-bench/tools/SWE-Atlas/data/rf"
# Haiku, batched: the job is mechanical (find file references, reword around them) and every result
# is checked deterministically below, so a cheap model that is caught when it slips costs less than
# an expensive one. BATCH_CHARS bounds one request's input.
MODEL = "haiku"
MAX_ATTEMPTS = 3
BATCH_CHARS = 12000

SYSTEM = """You edit software issue descriptions for a code-navigation benchmark.

Rewrite the description so that it contains NO file names, file paths, directory paths, module or import paths, or file extensions -- nothing that tells the reader where in the repository the code lives. The reader must find the code themselves.

Keep everything else:
- Every code identifier exactly as written: types, functions, methods, fields, variables, constants, package names written without slashes, CLI flags, config keys, environment variables, HTTP routes written as URLs beginning with '/'.
- Every requirement, constraint, number, example and instruction, including the instruction not to modify tests.
- The original wording and markdown wherever no file is involved.

A file's base name is a file name too, with or without its extension: do not write `rrdset-slots`, "the rrdset-slots source" or "the engine.go file". Refer to the code instead.

Where a sentence locates code by file or directory (for example "the `GetServer` function in `api/server.go`" or "update CMakeLists.txt"), refer to it by its identifiers or by what the code does ("the `GetServer` function", "update the build configuration for the new files"). Never invent identifiers, components or details that are not in the original, and never add hints about where code lives. Do not summarise, reorder or drop anything.

You will receive one or more descriptions, each as <task id="...">...</task>. Rewrite each independently and output every one as <description id="...">...</description> with the same id, and nothing else."""


def source_tasks() -> list[tuple[str, str]]:
    out = []
    for d in sorted(ATLAS_RF.glob("task-*")):
        cfg_path = d / "tests" / "config.json"
        if not cfg_path.exists() or not (d / "instruction.md").exists():
            continue
        cfg = json.loads(cfg_path.read_text())
        key = f"{cfg['repo']}-{cfg['task_id'][-8:]}"
        out.append((key, strip_and_neutralize((d / "instruction.md").read_text())))
    return out


def problems(original: str, rewritten: str) -> list[str]:
    found = []
    paths = find_paths(rewritten)
    if paths:
        found.append("file or directory references remain: " + ", ".join(sorted(set(paths))[:15]))
    stems = sorted(s for s in file_stems(find_paths(original), original)
                   if re.search(r"(?<![\w-])%s(?![\w-])" % re.escape(s), rewritten, re.I))
    if stems:
        found.append("file base names remain (a file name without its extension is still a file name): "
                     + ", ".join(stems[:15]))
    kept = identifiers(original)
    missing = sorted(i for i in kept if not re.search(r"(?<![\w])%s(?![\w])" % re.escape(i), rewritten))
    if missing:
        found.append("identifiers from the original are missing: " + ", ".join(missing[:20]))
    ticks_in = set(re.findall(r"`([^`\n]+)`", original))
    added = sorted(t for t in set(re.findall(r"`([^`\n]+)`", rewritten)) - ticks_in
                   if not any(t in x for x in ticks_in))
    if added:
        found.append("backticked terms not in the original: " + ", ".join(added[:10]))
    if len(rewritten) > len(original) * 1.1 + 200:
        found.append(f"rewrite grew from {len(original)} to {len(rewritten)} characters")
    return found


def _batches(items: list[tuple[str, str]]) -> list[list[tuple[str, str]]]:
    out, cur, size = [], [], 0
    for key, text in items:
        if cur and size + len(text) > BATCH_CHARS:
            out.append(cur)
            cur, size = [], 0
        cur.append((key, text))
        size += len(text)
    if cur:
        out.append(cur)
    return out


def rewrite_batch(batch: list[tuple[str, str]]) -> dict[str, dict]:
    """Rewrite several prompts in one model call; retry only the ones that fail, together, with
    each one's problems attached."""
    results: dict[str, dict] = {}
    pending = dict(batch)
    feedback: dict[str, list[str]] = {}
    for attempt in range(1, MAX_ATTEMPTS + 1):
        if not pending:
            break
        parts = []
        for key, text in pending.items():
            note = ""
            if feedback.get(key):
                note = ("\n\n[Your previous rewrite of this task had problems -- fix them:\n- "
                        + "\n- ".join(feedback[key]) + "]")
            parts.append(f'<task id="{key}">\n{text}{note}\n</task>')
        messages = [{"role": "system", "content": SYSTEM}, {"role": "user", "content": "\n\n".join(parts)}]
        try:
            reply = atlas_judge.complete(MODEL, messages, wants_json=False)
        except atlas_judge.JudgeError as e:
            feedback = {k: [f"model call failed: {e}"] for k in pending}
            continue
        got = {m.group(1): m.group(2) for m in
               re.finditer(r'<description id="([^"]+)">\s*(.*?)\s*</description>', reply, re.S)}
        for key in list(pending):
            if key not in got:
                feedback[key] = ["no <description> with this id was returned"]
                continue
            candidate = got[key].strip() + "\n"
            issues = problems(pending[key], candidate)
            if issues:
                feedback[key] = issues
            else:
                results[key] = {"ok": True, "attempts": attempt, "prompt": candidate}
                del pending[key]
    for key in pending:
        results[key] = {"ok": False, "attempts": MAX_ATTEMPTS, "problems": feedback.get(key, [])}
    return results


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--force", action="store_true", help="regenerate rewrites that already exist")
    ap.add_argument("--only", nargs="*", help="task keys to process")
    ap.add_argument("--parallel", type=int, default=6)
    args = ap.parse_args()

    existing = {}
    if REWRITES_PATH.exists():
        for line in REWRITES_PATH.read_text().splitlines():
            if line.strip():
                rec = json.loads(line)
                existing[rec["key"]] = rec

    todo = []
    for key, text in source_tasks():
        if args.only and key not in args.only:
            continue
        sha = hashlib.sha256(text.encode()).hexdigest()
        if not find_paths(text):
            # Nothing to remove: the loader uses the text as it is, and no record is needed.
            existing.pop(key, None)
            continue
        rec = existing.get(key)
        # A stored rewrite is kept only if it still passes today's checks: tightening `problems`
        # must re-open the rewrites it now rejects, not grandfather them in.
        if rec and rec.get("source_sha256") == sha and not args.force and not problems(text, rec["prompt"]):
            continue
        todo.append((key, text, sha))
    print(f"{len(todo)} prompt(s) to rewrite")

    failed = []
    batches = _batches([(k, t) for k, t, _ in todo])
    print(f"{len(batches)} batch(es) to {MODEL}")
    shas = {k: sha for k, _, sha in todo}
    texts = {k: t for k, t, _ in todo}
    with ThreadPoolExecutor(max_workers=max(1, args.parallel)) as pool:
        for batch_result in pool.map(rewrite_batch, batches):
            for key, res in batch_result.items():
                removed = sorted(set(find_paths(texts[key])))
                if res["ok"]:
                    existing[key] = {"key": key, "source_sha256": shas[key], "prompt": res["prompt"],
                                     "removed": removed, "model": MODEL, "attempts": res["attempts"],
                                     "generated": datetime.date.today().isoformat()}
                    print(f"  ok   {key} ({res['attempts']} attempt(s), {len(removed)} path token(s) removed)")
                else:
                    failed.append(key)
                    print(f"  FAIL {key}: {res['problems']}")

    REWRITES_PATH.parent.mkdir(parents=True, exist_ok=True)
    REWRITES_PATH.write_text("".join(json.dumps(existing[k], ensure_ascii=False) + "\n" for k in sorted(existing)))
    print(f"wrote {len(existing)} rewrite(s) to {REWRITES_PATH}; {len(failed)} failed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

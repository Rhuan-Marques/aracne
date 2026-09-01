#!/usr/bin/env python3
"""Replay a run's patches through the topology and count the warnings that SHOULD have fired.

WHY THIS EXISTS. The compact-blocked-20260830c run produced exactly 2 topology warnings across
75 `edit`/`write` calls. Both were correct and the model acted on both immediately -- which
makes the warning mechanism the single most effective thing in that run, and its 2/75 firing
rate the obvious thing to fix. But "fires too rarely" and "these patches genuinely break
nothing" look identical from the transcript alone: most of those 27 patches were small
additive edits, and an added struct field breaks no caller.

So measure before building. This applies each run's final patch to its fixture worktree one
file at a time, through the same `arac update-file` path the MCP edit tool uses
(TopologyManager.UpdateFile), and reports the warnings that path produces. If the expected
count is also ~2, the mechanism is fine and simply under-exercised -- and the lever is the
PostToolUse drift check that makes out-of-band writes warn too, not warning generation. If it
is much higher, there is a suppression bug worth hunting.

The fixture worktree is mutated and restored with `git checkout`/`git clean` in a finally, and
the topology DB is copied to a scratch path first so the fixture's own DB is never written.

Usage:
    python bench/replay_warnings.py --run compact-blocked-20260830c
    python bench/replay_warnings.py --run <name> --instance tokio-rs__tracing-1252
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
RESULTS = REPO_ROOT / "bench" / "results"
FIXTURES = REPO_ROOT / "bench" / "fixtures"

# `arac update-file` prints "Warning: [kind] message (source: ..., target: ...)" per warning.
WARN_RE = re.compile(r"^Warning: \[(?P<kind>\w+)\] (?P<msg>.*?) \(source: (?P<src>.*?), target: (?P<tgt>.*?)\)$")


def run(cmd, cwd, timeout=600):
    return subprocess.run(cmd, cwd=str(cwd), text=True, capture_output=True, timeout=timeout)


# What `arac init` drops into a prepared fixture. Mirrors bench/bench/fixtures.py
# ARACNE_ARTIFACTS -- a change to one of these does not mean the worktree drifted from base.
ARACNE_ARTIFACTS = (".aracne", ".claude", ".opencode", ".mcp.json", "CLAUDE.md", "AGENTS.md")


def is_aracne_artifact(path: str) -> bool:
    path = path.strip().strip('"')
    return any(path == a or path.startswith(a + "/") for a in ARACNE_ARTIFACTS)


def fixture_for(instance: str) -> Path | None:
    """The fixture worktree for an instance id, matched on the org__repo prefix.

    Instance ids are `org__repo-<number>`; fixture dirs are `org__repo@<commit12>`. Several
    commits of the same repo can be present, so prefer the one whose worktree exists.
    """
    repo = instance.rsplit("-", 1)[0]
    for d in sorted(FIXTURES.glob(f"{repo}@*")):
        if (d / "worktree").is_dir() and (d / "worktree" / ".aracne" / "topology.db").is_file():
            return d / "worktree"
    return None


def patched_files(patch: Path, worktree: Path) -> list[str]:
    """The files a patch touches, as paths that exist in the worktree."""
    out = []
    for line in patch.read_text(errors="replace").splitlines():
        if line.startswith("+++ b/"):
            rel = line[6:].strip()
            if (worktree / rel).exists() or rel != "/dev/null":
                out.append(rel)
    return out


def edit_order(files: list[str], instance: str) -> list[str]:
    """`files` ordered the way the agent edited them, from its transcript.

    Falls back to patch order when the transcript is unavailable or names nothing useful --
    the sequence still exercises intermediate states either way, just not the exact ones.
    """
    order: list[str] = []
    for p in sorted((RESULTS).glob(f"*/transcripts/{instance}__*.jsonl")):
        for line in p.open(errors="replace"):
            if "mcp__aracne__" not in line:
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            if ev.get("type") != "assistant":
                continue
            for block in ev.get("message", {}).get("content", []):
                if not isinstance(block, dict) or block.get("type") != "tool_use":
                    continue
                if block.get("name") not in ("mcp__aracne__edit", "mcp__aracne__write"):
                    continue
                fp = (block.get("input") or {}).get("file_path", "")
                for rel in files:
                    if fp.endswith(rel) and rel not in order:
                        order.append(rel)
        if order:
            break
    return order + [f for f in files if f not in order]


def replay_one(instance: str, patch: Path, arac: str) -> dict:
    worktree = fixture_for(instance)
    if worktree is None:
        return {"instance": instance, "status": "no-fixture"}

    db_src = worktree / ".aracne" / "topology.db"
    scratch_db = Path(tempfile.mkdtemp(prefix="aracne-replay-")) / "topology.db"
    scratch_db.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(db_src, scratch_db)

    # The fixture worktree is NOT pristine: the benchmark's aracne arm reuses one canonical
    # worktree per repo and leaves the agent's edits in place until the next `fixtures.restore`.
    # So reset exactly the files this patch touches back to the base commit first, rather than
    # demanding a clean tree. Their post-run content is what the patch already holds, so
    # nothing is lost, and the aracne artifacts (staged, untouched here) survive.
    files = patched_files(patch, worktree)
    if not files:
        return {"instance": instance, "status": "empty-patch"}
    for rel in files:
        run(["git", "checkout", "HEAD", "--", rel], worktree)

    # The fixture's topology.db is POST-run: the agent's own edits updated it in place, so it
    # already contains the renamed symbols. Replaying the patch against it would be a no-op
    # and report nothing. Re-sync the scratch copy to the base-commit sources first -- an
    # incremental scan, since only the patch's files moved.
    resync = run([arac, "scan", "-output", str(scratch_db), "--progress", "never"],
                 worktree, timeout=900)
    if resync.returncode != 0:
        return {"instance": instance, "status": "resync-failed",
                "detail": (resync.stderr or "").strip()[:200]}

    # Apply and sync ONE FILE AT A TIME, in the order the agent actually edited them.
    #
    # This is the whole point. Applying the finished patch in one shot and syncing afterwards
    # reports ~nothing, because a correct patch is internally consistent by definition -- it
    # renames a symbol AND fixes its callers. The warnings that matter fire in between: the
    # vuejs/core run got its `node_removed` warnings the moment `getEscapedKey` disappeared
    # from utils.ts while defineProps.ts still called it, and used them to find the call
    # sites. Replaying that sequence is the only way to measure what the mechanism would say
    # during a realistic edit, rather than after one.
    warnings, errors = [], []
    try:
        for rel in edit_order(files, instance):
            applied = run(["git", "apply", "--verbose", f"--include={rel}", str(patch)], worktree)
            if applied.returncode != 0:
                errors.append(f"apply {rel}: {(applied.stderr or '').strip()[:120]}")
                continue
            r = run([arac, "update-file", rel, "--db", str(scratch_db)], worktree)
            if r.returncode != 0:
                errors.append(f"sync {rel}: {(r.stderr or '').strip()[:120]}")
                continue
            for line in r.stdout.splitlines():
                m = WARN_RE.match(line.strip())
                if m:
                    warnings.append({"after_file": rel, "kind": m.group("kind"),
                                     "source": m.group("src"), "target": m.group("tgt"),
                                     "message": m.group("msg")[:160]})
        return {"instance": instance, "status": "ok", "n_files": len(files),
                "n_warnings": len(warnings), "warnings": warnings, "errors": errors}
    finally:
        # Restore surgically. A blanket `git clean -fd` would delete the fixture's own
        # .aracne/.claude artifacts, which the bench expects to survive between runs.
        for rel in files:
            target = worktree / rel
            r = run(["git", "checkout", "HEAD", "--", rel], worktree)
            if r.returncode != 0 and target.exists():
                target.unlink()  # the patch created it; it is not in HEAD to restore
        shutil.rmtree(scratch_db.parent, ignore_errors=True)


def observed_warning_counts(run_dir: Path) -> dict[str, int]:
    """How many warnings the agent actually SAW, recovered from the run's transcripts."""
    counts: dict[str, int] = {}
    tdir = run_dir / "transcripts"
    if not tdir.is_dir():
        return counts
    for p in sorted(tdir.glob("*.jsonl")):
        inst = p.name.split("__aracne__")[0]
        n = 0
        for line in p.open(errors="replace"):
            if "Topology warnings" in line:
                n += 1
        counts[inst] = n
    return counts


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", required=True, help="run name under bench/results/")
    ap.add_argument("--instance", help="replay only this instance")
    ap.add_argument("--arac", default=str(REPO_ROOT / "bin" / "arac"))
    ap.add_argument("--out", help="write the full JSON report here")
    args = ap.parse_args()

    run_dir = RESULTS / args.run
    patch_dir = run_dir / "patches"
    if not patch_dir.is_dir():
        print(f"no patches under {patch_dir}", file=sys.stderr)
        return 1

    observed = observed_warning_counts(run_dir)
    rows = []
    patches = sorted(patch_dir.glob("*.patch"))
    for patch in patches:
        instance = patch.name.split("__aracne__")[0]
        if args.instance and instance != args.instance:
            continue
        row = replay_one(instance, patch, args.arac)
        row["observed"] = observed.get(instance, 0)
        rows.append(row)
        exp = row.get("n_warnings", "-")
        kinds = ",".join(sorted({w["kind"] for w in row.get("warnings", [])})) or "-"
        print(f"{instance[:40]:40s} status={row['status']:14s} expected={exp:>3} "
              f"observed={row['observed']:>2}  kinds={kinds}", flush=True)

    ok = [r for r in rows if r["status"] == "ok"]
    total_exp = sum(r["n_warnings"] for r in ok)
    total_obs = sum(r["observed"] for r in ok)
    print()
    print(f"replayed {len(ok)}/{len(rows)} instances")
    print(f"warnings the topology WOULD emit: {total_exp}")
    print(f"warning blocks the agent SAW:     {total_obs}")
    for r in rows:
        if r["status"] != "ok":
            print(f"  skipped {r['instance']}: {r['status']} {r.get('detail','')}")

    if args.out:
        Path(args.out).write_text(json.dumps(rows, indent=1))
        print(f"\nfull report: {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

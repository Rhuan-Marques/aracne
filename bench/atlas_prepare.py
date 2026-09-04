#!/usr/bin/env python3
"""Turn SWE-Atlas tasks into aracne benchmark fixtures.

WHY THE REPO COMES OUT OF THE IMAGE. SWE-Atlas gives a repo SLUG and a base commit, not a clone
URL, and the slug is ambiguous (`simple-login_app` is simple-login/app, `grafana_k6` is
grafana/k6 -- the separator also occurs inside names). More importantly the image's tree is not
simply that commit: images carry vendored modules, generated files and a squashed history, and
the grader diffs against THAT tree. Cloning from GitHub would hand the agent a different
starting point than the one it is scored on.

So the worktree is copied out of the task's own image. Two things fall out of it for free:

  - the tree the agent edits is byte-identical to the tree the verifier compares against;
  - the history is one squashed commit, so there is no future history to leak. That is the
    exact contamination that reached the answer in an earlier run, where an agent ran
    `git log --all` and applied the fix commit it found.
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

# The same root run_benchmark.py uses (bench.fixtures.fixture_dir keys an atlas fixture by its
# task key), so `run` finds what this script prepared with no extra wiring.
FIXTURES = Path(os.environ.get(
    "ARACNE_BENCH_FIXTURES",
    Path.home() / ".cache" / "aracne-bench" / "fixtures",
))
ARAC = os.environ.get("ARAC_BIN", "arac")


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def have_image(image: str) -> bool:
    return run(["docker", "image", "inspect", image]).returncode == 0


def pull(image: str, quiet: bool = False) -> bool:
    if have_image(image):
        return True
    if not quiet:
        print(f"    pulling {image.split('@')[0].split(':')[-1][:60]} ...", flush=True)
    return run(["docker", "pull", image], timeout=3600).returncode == 0


def extract_workspace(image: str, mount: str, dest: Path) -> bool:
    """Copy the image's repo tree to dest. Uses `docker create` so nothing is executed."""
    cid = run(["docker", "create", image, "true"]).stdout.strip()
    if not cid:
        return False
    try:
        if dest.exists():
            shutil.rmtree(dest)
        dest.parent.mkdir(parents=True, exist_ok=True)
        # `docker cp <cid>:/workspace/.` copies the CONTENTS, so dest is the repo root rather
        # than a directory containing it.
        dest.mkdir()
        cp = run(["docker", "cp", f"{cid}:{mount.rstrip('/')}/.", str(dest)], timeout=1800)
        return cp.returncode == 0 and (dest / ".git").exists()
    finally:
        run(["docker", "rm", "-f", cid])


# What to ask a task image about its own runtime, and the environment variable that pins the
# host to the same thing. Only Go is here because Go is the only one of these whose toolchain
# can be pinned by an environment variable alone -- `GOTOOLCHAIN=go1.22.12` makes the host `go`
# fetch and use exactly that release. Python and Node would need a real interpreter switch.
_RUNTIME_PROBES = {
    "go": ("go env GOVERSION", "GOTOOLCHAIN"),
}


def probe_runtime(image: str) -> dict:
    """Ask the task image which toolchain it builds with.

    WHY THIS EXISTS. The agent works on a tree extracted from the image but runs on the HOST,
    while grading runs inside the image -- so the two can disagree about the compiler. On
    trufflehog they did: host Go 1.26.4 against the image's 1.22.12, and
    `golang.org/x/tools@v0.18.0` does not compile under 1.26 ("invalid array length
    -delta * delta"). The project therefore failed to build at its OWN base commit, before any
    agent touched it, and both arms spent turns discovering that a build they could not fix was
    not their fault. That is pure noise, and it lands unevenly across arms: on
    trufflehog-7be1e531 the control gave up after ~5 turns and the aracne arm chased it for
    ~10, which was the entire measured regression for that repository.

    Best-effort by design: a probe that fails leaves the fixture unpinned, which is exactly
    today's behaviour rather than a new failure mode.
    """
    env = {}
    for _lang, (probe, var) in _RUNTIME_PROBES.items():
        r = run(["docker", "run", "--rm", "--entrypoint", "sh", image, "-c", probe], timeout=300)
        value = (r.stdout or "").strip().splitlines()
        if r.returncode == 0 and value and value[0].strip():
            env[var] = value[0].strip()
    return env


def node_count(db: Path) -> tuple[int, int]:
    """(all resources, describable resources) — the cost driver for a fixture."""
    import sqlite3
    if not db.exists():
        return 0, 0
    c = sqlite3.connect(str(db))
    try:
        total = c.execute("SELECT COUNT(*) FROM resources").fetchone()[0]
        desc = c.execute(
            "SELECT COUNT(*) FROM resources WHERE kind IN "
            "('function','method','struct','interface')").fetchone()[0]
        return total, desc
    finally:
        c.close()


def prepare(task: dict, force: bool = False) -> dict:
    # Accept a bench manifest row (atlas fields nested under `raw`, so the row can be handed
    # straight to Task(**rec)) as readily as a flat record.
    if isinstance(task.get("raw"), dict):
        task = {**task["raw"], "key": task["key"], "language": task["language"]}
    key = task["key"]
    wt = FIXTURES / key / "worktree"
    db = wt / ".aracne" / "topology.db"
    out = {"key": key, "repo": task["repo"], "language": task["language"],
           "complexity": task["complexity"], "gold_decls": task["gold_decls"]}

    if db.exists() and not force:
        # Backfill for a fixture prepared before runtime pinning existed.
        rt = wt.parent / "runtime_env.json"
        if not rt.exists():
            rt.write_text(json.dumps(probe_runtime(task["image"]), indent=2), encoding="utf-8")
        out["runtime_env"] = json.loads(rt.read_text())
        out["total"], out["describable"] = node_count(db)
        out["status"] = "reused"
        return out

    if not pull(task["image"]):
        out["status"] = "pull failed"
        return out
    if not extract_workspace(task["image"], task["mount_path"], wt):
        out["status"] = "extract failed"
        return out

    # Both harness integrations, so an arm can select either without re-running init.
    for flag in ("--claude", "--opencode"):
        r = run([ARAC, "init", flag, "-y"], cwd=str(wt), timeout=300)
        if r.returncode != 0:
            out["status"] = f"init failed: {(r.stderr or r.stdout)[-160:]}"
            return out
    r = run([ARAC, "scan", "--all"], cwd=str(wt), timeout=3600)
    if r.returncode != 0:
        out["status"] = f"scan failed: {(r.stderr or r.stdout)[-160:]}"
        return out

    # The aracne artifacts must be COMMITTED, not left untracked: the verifier captures the
    # agent's work with `git add -A && git diff --cached HEAD`, and an untracked .aracne/ would
    # land in the graded patch as thousands of lines of database.
    run(["git", "config", "user.email", "bench@aracne.local"], cwd=str(wt))
    run(["git", "config", "user.name", "aracne bench"], cwd=str(wt))
    run(["git", "add", "-A"], cwd=str(wt))
    run(["git", "commit", "-q", "-m", "aracne: topology + harness integration"], cwd=str(wt))

    # Pin the host to the image's toolchain, so the agent builds what the verifier builds.
    env = probe_runtime(task["image"])
    (wt.parent / "runtime_env.json").write_text(json.dumps(env, indent=2), encoding="utf-8")
    out["runtime_env"] = env

    out["total"], out["describable"] = node_count(db)
    out["status"] = "prepared"
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--manifest", required=True, help="jsonl from atlas_tasks.py --out")
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()

    tasks = [json.loads(l) for l in Path(args.manifest).read_text().splitlines() if l.strip()]
    if args.limit:
        tasks = tasks[: args.limit]

    print(f"{'key':38} {'lang':11} {'nodes':>8} {'describable':>12}  status")
    print("-" * 88)
    rows = []
    for t in tasks:
        r = prepare(t, force=args.force)
        rows.append(r)
        print(f"{r['key'][:38]:38} {str(r['language'])[:11]:11} {r.get('total',0):8} "
              f"{r.get('describable',0):12}  {r['status']}", flush=True)
    print("-" * 88)
    ok = [r for r in rows if r["status"] in ("prepared", "reused")]
    print(f"{len(ok)} of {len(rows)} fixture(s) ready  ->  {FIXTURES}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

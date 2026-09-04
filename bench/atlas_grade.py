#!/usr/bin/env python3
"""Grade a SWE-Atlas refactoring patch in its own task container.

WHAT THE VERIFIER WANTS. Each task ships a `tests/` directory whose `test.sh` runs INSIDE the
task image: it captures the workspace diff itself, applies the task's test patch, runs the
suite before and after, compares the two, then has an LLM judge score the diff against a
rubric. `reward.txt` is 1.0 only when every relevant test passes AND every must-have rubric
item passes. So this does not reimplement grading -- it stages the agent's work into a
container and gets out of the way.

THREE THINGS IT HAS TO GET RIGHT.

  The patch must be SOURCE ONLY. `test.sh` grades whatever `git add -A` finds, and an aracne
  fixture carries a topology database; letting that reach the diff would put thousands of lines
  of SQLite into the graded patch and fail the rubric on presentation alone. The harness's
  own `_extract_patch` already excludes those, and this takes its output verbatim.

  The image must be BUILT, not just pulled. The task's Dockerfile installs the Go toolchain and
  sets GOPROXY=off so the suite builds with no network; the base image alone cannot run the
  tests it is graded by.

  The judge needs an OpenAI endpoint. There is no API key here, so `atlas_judge` bridges to the
  Claude CLI on the host and the container reaches it through host.docker.internal.
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

JUDGE_MODEL = os.environ.get("ATLAS_JUDGE_MODEL", "opus")


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def fields(task: dict) -> dict:
    """Accept either a raw SWE-Atlas record or a bench manifest row.

    read_task nests the atlas-specific values under `raw` so the row can be handed straight to
    `Task(**rec)`; this flattens either shape into the one dict the grader reads.
    """
    if "raw" in task and isinstance(task["raw"], dict):
        return {**task["raw"], "key": task["key"]}
    return task


def image_tag(task: dict) -> str:
    return f"aracne-atlas/{re.sub(r'[^a-z0-9_.-]', '-', task['key'].lower())}:latest"


def ensure_image(task: dict, rebuild: bool = False, log=print) -> str:
    """Build the task image from its own environment/Dockerfile."""
    task = fields(task)
    tag = image_tag(task)
    if not rebuild and run(["docker", "image", "inspect", tag]).returncode == 0:
        return tag
    env_dir = Path(task["task_dir"]) / "environment"
    log(f"    building {tag} (may take several minutes) ...")
    r = run(["docker", "build", "-t", tag, str(env_dir)], timeout=5400)
    if r.returncode != 0:
        raise RuntimeError(f"docker build failed: {(r.stderr or r.stdout)[-400:]}")
    return tag


def grade(task: dict, patch_text: str, judge_url: str, timeout_s: int = 7200,
          keep: bool = False, log=print) -> dict:
    task = fields(task)
    out = {"key": task["key"], "reward": None, "tests_reward": None,
           "must_have_pass": None, "error": None}
    if not patch_text.strip():
        out["error"] = "empty patch"
        return out

    tag = ensure_image(task, log=log)
    mount = task.get("mount_path", "/workspace").rstrip("/") or "/workspace"
    tests_dir = str(Path(task["task_dir"]) / "tests")

    cid = run([
        "docker", "run", "-d", "--rm",
        "--add-host", "host.docker.internal:host-gateway",
        "-v", f"{tests_dir}:/tests:ro",
        "-e", "EVAL_API_KEY=aracne-local-judge",
        "-e", f"EVAL_BASE_URL={judge_url}",
        "-e", f"EVAL_MODEL={JUDGE_MODEL}",
        "--entrypoint", "sleep", tag, str(timeout_s + 600),
    ]).stdout.strip()
    if not cid:
        out["error"] = "could not start container"
        return out

    try:
        # Streamed on stdin rather than written to a temp file and `docker cp`-ed: the
        # daemon resolves a source path in ITS OWN mount namespace, so any path this process
        # can write but the daemon cannot see (a sandboxed /tmp, a container-side bind) fails
        # with a bare "no such file or directory" that looks like a missing patch.
        wrote = subprocess.run(
            ["docker", "exec", "-i", cid, "sh", "-c", "cat > /tmp/agent.patch"],
            input=patch_text, text=True, capture_output=True)
        if wrote.returncode != 0:
            out["error"] = f"could not write patch into container: {(wrote.stderr or '')[-200:]}"
            return out

        # --3way first: a refactor moves whole blocks, and a strict apply fails on context
        # drift that git can reconcile from blob hashes.
        applied = run(["docker", "exec", "-w", mount, cid,
                       "sh", "-c", "git apply --3way --whitespace=nowarn /tmp/agent.patch"])
        if applied.returncode != 0:
            applied = run(["docker", "exec", "-w", mount, cid,
                           "sh", "-c", "git apply --whitespace=nowarn /tmp/agent.patch"])
        if applied.returncode != 0:
            out["error"] = f"patch did not apply: {(applied.stderr or '')[-300:]}"
            return out

        log("    running verifier (tests + rubric judge) ...")
        res = run(["docker", "exec", "-w", mount, cid, "bash", "/tests/test.sh"],
                  timeout=timeout_s)
        out["verifier_exit"] = res.returncode

        got = run(["docker", "exec", cid, "cat", "/logs/verifier/reward.json"])
        if got.returncode == 0 and got.stdout.strip():
            try:
                j = json.loads(got.stdout)
                out.update(reward=j.get("reward"), tests_reward=j.get("tests_reward"),
                           must_have_pass=j.get("must_have_pass"))
            except json.JSONDecodeError:
                out["error"] = "reward.json unparseable"
        else:
            txt = run(["docker", "exec", cid, "cat", "/logs/verifier/reward.txt"])
            if txt.returncode == 0 and txt.stdout.strip():
                out["reward"] = float(txt.stdout.strip())
            else:
                out["error"] = "verifier produced no reward"
                out["stdout_tail"] = (res.stdout or res.stderr or "")[-800:]
        return out
    except subprocess.TimeoutExpired:
        out["error"] = f"verifier timed out after {timeout_s}s"
        return out
    finally:
        if not keep:
            run(["docker", "rm", "-f", cid])
        else:
            log(f"    container kept: {cid[:12]}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--manifest", required=True)
    ap.add_argument("--key", required=True, help="task key to grade")
    ap.add_argument("--patch", required=True, help="path to the agent's source-only patch")
    ap.add_argument("--judge-port", type=int, default=8765)
    ap.add_argument("--timeout-s", type=int, default=7200)
    ap.add_argument("--keep", action="store_true", help="leave the container for inspection")
    args = ap.parse_args()

    tasks = {json.loads(l)["key"]: json.loads(l)
             for l in Path(args.manifest).read_text().splitlines() if l.strip()}
    if args.key not in tasks:
        print(f"no such task {args.key!r} in {args.manifest}", file=sys.stderr)
        return 2

    sys.path.insert(0, str(Path(__file__).parent))
    import atlas_judge
    httpd = atlas_judge.serve(args.judge_port, "0.0.0.0")
    port = httpd.server_address[1]
    judge_url = f"http://host.docker.internal:{port}/v1"
    print(f"judge shim: {judge_url}")
    try:
        r = grade(tasks[args.key], Path(args.patch).read_text(), judge_url,
                  timeout_s=args.timeout_s, keep=args.keep)
    finally:
        httpd.shutdown()
    print(json.dumps(r, indent=2))
    return 0 if r.get("error") is None else 1


if __name__ == "__main__":
    sys.exit(main())

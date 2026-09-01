"""Keep the web open to the agent, and the answer key shut.

WHY THIS EXISTS. Searching the web is legitimate engineering behaviour, and a benchmark that
forbids it is measuring an agent with one hand tied. But SWE-bench's answer sits at a URL
derived mechanically from the instance id -- `github.com/<org>/<repo>/pull/<n>.diff` -- and
across three runs of `scale40` the agent used the network for nothing else. Twelve fetch
events over six cells, one hundred percent of them the task's own repository: PR diffs, issue
threads, PR-title searches. Not one call to documentation, Stack Overflow, a package registry
or an API reference. It was not researching; it was looking up the answer, and three separate
"solves" (`sveltejs/svelte` twice, `iamkun/dayjs`, `anuraghazra/github-readme-stats` twice)
came from reading the diff rather than the code.

So the rule this module enforces is narrow on purpose: **the web is fair game, the graded diff
is not.** Everything except the repository under test stays reachable.

HOW. A scratch directory holding `curl` and `wget` wrappers is prepended to PATH. Each wrapper
refuses when any argument names the repository in `ARACNE_BENCH_DENY_REPO` and otherwise execs
the real binary, so a normal fetch is untouched. The deny target rides in the environment
rather than being baked into the script, which is what lets one shim directory serve every
cell in a run.

WHAT THIS DOES NOT COVER, deliberately. A hand-rolled `python3 -c "urllib..."`, or a harness
tool that makes its own HTTP request (`WebFetch`). Closing those needs an egress proxy, and on
this evidence -- every observed fetch was `curl` -- that is not yet worth a per-cell failure
mode. The gap is covered by measurement instead: `toolstats.count_answer_key_fetches` reads
the same intent out of the transcript afterwards, and `paired._graded` drops any repository
where it fired from BOTH arms. If the audit ever fires, the shim leaked and the proxy becomes
worth building.
"""
from __future__ import annotations

import os
import shutil
import tempfile

# The environment variable each wrapper reads. Empty or unset disables the wrappers entirely,
# which is what makes the shim directory safe to leave on PATH for a run that does not want it.
DENY_ENV = "ARACNE_BENCH_DENY_REPO"

# `curl` is what the agent actually reached for in every observed fetch; `wget` and `gh` are
# the obvious substitutes once it starts refusing. `gh` happens not to be installed on this
# host -- an agent tried `gh pr list --repo <task>` and got "No such file or directory" -- but
# relying on that is relying on luck, and shim_dir only writes a wrapper for a binary that
# actually exists, so naming it here costs nothing on a host without it.
_WRAPPED = ("curl", "wget", "gh")

# Built with str.replace rather than str.format: the script is mostly shell `${...}`
# expansions, and formatting it meant escaping every one of them as `$${...}`. The first
# version got that wrong -- `$$` expands to the shell PID, so the guard's `[ -n ... ]` test was
# always true, its case pattern never matched, and every fetch fell straight through to the
# real binary. It looked installed and did nothing. Hence the test that fetches a real URL.
_SHIM = """#!/usr/bin/env bash
# aracne-bench answer-key guard -- see bench/bench/netshim.py.
# Refuses to fetch the repository under test; everything else is passed straight through.
if [ -n "${__DENY__}" ]; then
  for arg in "$@"; do
    case "$arg" in
      *"${__DENY__}"*)
        echo "aracne-bench: refusing to fetch ${__DENY__} -- the graded diff for this task" >&2
        echo "lives there. Every other host is reachable; solve it from the code." >&2
        exit 3
        ;;
    esac
  done
fi
exec __REAL__ "$@"
"""

_SHIM_DIR: str | None = None


def shim_dir() -> str | None:
    """The scratch bin directory to prepend to PATH, or None when nothing could be wrapped.

    Cached for the process: one directory serves every cell, because the repository to refuse
    travels in the environment rather than in the script.
    """
    global _SHIM_DIR
    if _SHIM_DIR is not None:
        return _SHIM_DIR or None

    reals = {name: shutil.which(name) for name in _WRAPPED}
    reals = {name: path for name, path in reals.items() if path}
    if not reals:
        _SHIM_DIR = ""
        return None

    d = tempfile.mkdtemp(prefix="aracne-bench-netshim-")
    for name, real in reals.items():
        path = os.path.join(d, name)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(_SHIM.replace("__DENY__", DENY_ENV).replace("__REAL__", _quote(real)))
        os.chmod(path, 0o755)
    _SHIM_DIR = d
    return d


def _quote(path: str) -> str:
    return "'" + path.replace("'", "'\\''") + "'"


def deny_target(clone_url: str) -> str:
    """The `org/repo` a cell must not fetch, derived from its clone URL.

    `org/repo` rather than a full URL because it is the one substring every answer-key form
    shares: `github.com/o/r/pull/1.diff`, `patch-diff.githubusercontent.com/raw/o/r/pull/1`,
    and `api.github.com/search/issues?q=repo:o/r+...` all contain it, while an unrelated host
    does not.
    """
    tail = (clone_url or "").rstrip("/")
    if tail.endswith(".git"):
        tail = tail[: -len(".git")]
    parts = [p for p in tail.split("/") if p]
    if len(parts) < 2:
        return ""
    return f"{parts[-2]}/{parts[-1]}"


def apply(env: dict, deny_repo: str | None) -> dict:
    """Put the wrappers on PATH and name the repository they must refuse.

    A falsy `deny_repo` leaves the environment alone, so a caller that does not want the guard
    simply passes nothing.
    """
    if not deny_repo:
        return env
    d = shim_dir()
    if not d:
        return env
    env[DENY_ENV] = deny_repo
    env["PATH"] = d + os.pathsep + env.get("PATH", "")
    return env

#!/usr/bin/env python3
"""Regression tests for what the grading harnesses are HANDED (bench/bench/grade.py).

Two inputs, one rule each:

  Multi-SWE-bench needs the source repo already cloned; SWE-bench needs a dataset whose rows
  carry the eval columns the installed harness reads. Get either wrong and a batch dies before
  it judges anything — the verdicts are lost, not wrong, but lost verdicts still cost the run.

The repo rule: the harness must find `<repo_dir>/<org>/<repo>` already cloned.
When it does not it warns "Repository not found: ..." and clones from GitHub itself — inside
the timed harness call, into a per-run directory, so every run re-downloaded hundreds of
megabytes and an interrupted clone was thrown away.

No pytest, no network, no Docker: the "GitHub" side is a local git repo standing in for the
shared `_repos` cache.

    python3 bench/test_graderepos.py
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
import types
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import grade  # noqa: E402
from bench.sources import swe_dataset_name  # noqa: E402

_RESULTS: list[tuple[str, bool]] = []


def check(name: str, cond) -> None:
    _RESULTS.append((name, bool(cond)))
    print(("PASS  " if cond else "FAIL  ") + name)


class FakeTask:
    source = "multi_swe_bench"

    def __init__(self, org: str, repo: str, sha: str, number: int = 1):
        self.raw = {"org": org, "repo": repo, "number": number, "base": {"sha": sha}}
        self.key = f"{org}__{repo}-{number}"


def _make_cache(cache: Path) -> str:
    """A one-commit repo standing in for a `_repos` cache entry; returns its HEAD sha."""
    cache.mkdir(parents=True)
    subprocess.run(["git", "init", "-q", "-b", "main", str(cache)], check=True)
    (cache / "f.txt").write_text("hi", encoding="utf-8")
    subprocess.run(["git", "-C", str(cache), "add", "-A"], check=True)
    subprocess.run(["git", "-C", str(cache), "-c", "user.email=t@t", "-c", "user.name=t",
                    "commit", "-qm", "base"], check=True)
    return subprocess.run(["git", "-C", str(cache), "rev-parse", "HEAD"],
                          capture_output=True, text=True, check=True).stdout.strip()


def test_seed_from_cache(tmp: Path) -> None:
    cache_root = tmp / "fixtures" / "_repos"
    sha = _make_cache(cache_root / "acme__widget")
    repodir = tmp / "_mswe_repos"
    items = [({}, FakeTask("acme", "widget", sha), "baseline", "")]

    grade._ensure_repos(items, repodir, cache_root)
    dest = repodir / "acme" / "widget"
    check("seeds <repo_dir>/<org>/<repo> from the local cache", (dest / ".git").exists())
    check("seeded clone contains the instance base sha", grade._has_commit(dest, sha))
    origin = subprocess.run(["git", "-C", str(dest), "remote", "get-url", "origin"],
                            capture_output=True, text=True).stdout.strip()
    check("origin points at github, not the local cache",
          origin == "https://github.com/acme/widget.git")

    # The harness's own liveness check: a dir with a .git in it is 'found' and never re-cloned.
    check("harness would find the repo", (dest / ".git").exists() and dest.is_dir())


def test_base_commit_is_reachable_not_just_present(tmp: Path) -> None:
    """The harness walks REFS (`iter_commits("--all")`), not the object database.

    The `_repos` cache is itself a clone, so most branches live there as
    refs/remotes/origin/*: only ONE local head (e.g. tokio-rs/tracing has 1 head and 117
    remote branches). Cloning that cache copies every object but only its refs/heads, so a
    base commit carried by any other branch arrives orphaned — `cat-file -e` finds it,
    `rev-list --all` does not, and the harness aborts the batch with "Commit hash not found"."""
    root = tmp / "fixtures-reach"
    upstream = root / "upstream"
    _make_cache(upstream)                                # stands in for github
    subprocess.run(["git", "-C", str(upstream), "checkout", "-q", "-b", "side"], check=True)
    (upstream / "g.txt").write_text("side", encoding="utf-8")
    subprocess.run(["git", "-C", str(upstream), "add", "-A"], check=True)
    subprocess.run(["git", "-C", str(upstream), "-c", "user.email=t@t", "-c", "user.name=t",
                    "commit", "-qm", "side"], check=True)
    side = subprocess.run(["git", "-C", str(upstream), "rev-parse", "HEAD"],
                          capture_output=True, text=True, check=True).stdout.strip()
    subprocess.run(["git", "-C", str(upstream), "checkout", "-q", "main"], check=True)

    # The cache, as `ensure_repo_cache` builds it: a clone -> `side` is remote-tracking only.
    cache_root = root / "_repos"
    cache_root.mkdir(parents=True)
    subprocess.run(["git", "clone", "--quiet", str(upstream), str(cache_root / "acme__widget")],
                   check=True)
    heads = subprocess.run(["git", "-C", str(cache_root / "acme__widget"), "for-each-ref",
                            "--format=%(refname)", "refs/heads/"],
                           capture_output=True, text=True).stdout.split()
    check("the cache holds the base commit on a remote-tracking branch only", len(heads) == 1)

    repodir = root / "_mswe_repos"
    grade._ensure_repos([({}, FakeTask("acme", "widget", side), "baseline", "")],
                        repodir, cache_root)
    dest = repodir / "acme" / "widget"
    reachable = subprocess.run(["git", "-C", str(dest), "rev-list", "--all"],
                               capture_output=True, text=True).stdout.split()
    check("the base commit is reachable from a ref, as the harness requires",
          side in reachable)


def test_seed_is_idempotent(tmp: Path) -> None:
    cache_root = tmp / "fixtures2" / "_repos"
    sha = _make_cache(cache_root / "acme__widget")
    repodir = tmp / "_mswe_repos2"
    items = [({}, FakeTask("acme", "widget", sha), "baseline", "")]
    grade._ensure_repos(items, repodir, cache_root)
    marker = repodir / "acme" / "widget" / ".git" / "config"
    stamp = marker.stat().st_mtime_ns
    grade._ensure_repos(items, repodir, cache_root)     # second call: no work, no network
    check("re-seeding an existing clone is a no-op", marker.stat().st_mtime_ns == stamp)


def test_partial_clone_is_repaired(tmp: Path) -> None:
    cache_root = tmp / "fixtures3" / "_repos"
    sha = _make_cache(cache_root / "acme__widget")
    repodir = tmp / "_mswe_repos3"
    partial = repodir / "acme" / "widget"          # what a killed `git clone` leaves behind
    partial.mkdir(parents=True)
    (partial / "half-written").write_text("x", encoding="utf-8")
    grade._ensure_repos([({}, FakeTask("acme", "widget", sha), "baseline", "")],
                        repodir, cache_root)
    check("a partial clone is replaced, not left broken",
          (partial / ".git").exists() and not (partial / "half-written").exists())


def test_seed_failure_is_reported_not_raised(tmp: Path) -> None:
    # No cache entry and no reachable remote: seeding fails. It must not raise here (the
    # caller decides), and it must NAME the repo so the batch can be skipped cleanly.
    repodir = tmp / "_mswe_repos4"
    unreachable = tmp / "no-such-cache"                 # no cache entry -> falls back to github
    backoff = grade._NET_BACKOFF_S
    grade._NET_BACKOFF_S = 0                            # keep the test instant
    calls = _count_git_calls()
    try:
        failed = grade._ensure_repos(
            [({}, FakeTask("acme", "nope-does-not-exist", "0" * 40), "baseline", "")],
            repodir, unreachable)
        raised = False
    except Exception:  # noqa: BLE001
        failed, raised = [], True
    finally:
        calls.restore()
        grade._NET_BACKOFF_S = backoff
    check("a failed seed never raises out of _ensure_repos", not raised)
    check("a failed seed is reported by name", failed == ["acme/nope-does-not-exist"])


def _count_git_calls():
    """Patch grade.subprocess.run to count invocations; .restore() puts it back."""
    real = grade.subprocess.run
    box = types.SimpleNamespace(n=0)

    def counting(args, **kw):
        box.n += 1
        return real(args, **kw)

    grade.subprocess.run = counting
    box.restore = lambda: setattr(grade.subprocess, "run", real)
    return box


def test_network_failure_is_retried(tmp: Path) -> None:
    """A dropped connection mid-clone must not cost a repo its verdicts on the first blip."""
    backoff = grade._NET_BACKOFF_S
    grade._NET_BACKOFF_S = 0                            # keep the test instant
    calls = _count_git_calls()
    try:
        ok = grade._git_net(["clone", "--quiet", str(tmp / "does-not-exist"),
                             str(tmp / "dest-a")], "clone acme/x", check=False)
        attempts = calls.n
        raised = False
        try:
            grade._git_net(["clone", "--quiet", str(tmp / "does-not-exist"),
                            str(tmp / "dest-b")], "clone acme/x", check=True)
        except subprocess.CalledProcessError:
            raised = True
    finally:
        calls.restore()
        grade._NET_BACKOFF_S = backoff
    check("a failing network git command is retried", attempts == grade._NET_RETRIES)
    check("check=False reports failure instead of raising", ok is False)
    check("check=True raises once every attempt is spent", raised)


def test_unseeded_repo_leaves_rows_unknown(tmp: Path) -> None:
    """The batch is abandoned with a readable message — never a false verdict."""
    row = {"run_key": "k", "success": None}
    original = grade._grade_multi

    def boom(*_a, **_kw):
        raise RuntimeError("could not obtain the source repo(s) acme/widget")

    grade._grade_multi = boom
    try:
        grade.grade_all([(row, FakeTask("acme", "widget", "0" * 40), "aracne", "diff")],
                        {"grade_timeout_s": 1, "grade_stall_timeout_s": 1}, tmp)
    finally:
        grade._grade_multi = original
    check("an unseedable repo leaves success unknown, not failed", row.get("success") is None)
    check("the row records why it was never graded",
          "could not obtain the source repo" in str(row.get("grade_error")))
    check("and it is not counted as a fail", row.get("outcome") != "fail")


def test_repo_root_is_run_independent(tmp: Path) -> None:
    root = grade._mswe_repo_root({"fixtures_dir": str(tmp / "fixtures")})
    check("repo dir lives beside the fixtures, not under the run",
          root == tmp / "fixtures" / "_mswe_repos")
    check("repo dir is arm-independent",
          root == grade._mswe_repo_root({"fixtures_dir": str(tmp / "fixtures")}))


# --------------------------------------------------------------------------- #
# SWE-bench: the dataset name must carry the harness's eval columns
# --------------------------------------------------------------------------- #
def test_legacy_swe_dataset_is_remapped() -> None:
    """swebench>=5 reads `image`/`eval_script`/`log_parser`/`eval_type` off the dataset row.

    The `princeton-nlp/*` mirrors stop at the original 12 columns, so the harness raises
    `KeyError: 'image'` in make_test_spec before building anything. The SWE-bench org
    republishes the same instance ids WITH those columns."""
    check("the legacy Lite mirror is remapped",
          swe_dataset_name({"dataset": "princeton-nlp/SWE-bench_Lite"})
          == "SWE-bench/SWE-bench_Lite")
    check("so is Verified (any legacy name, not a hardcoded pair)",
          swe_dataset_name({"dataset": "princeton-nlp/SWE-bench_Verified"})
          == "SWE-bench/SWE-bench_Verified")
    check("an already-correct name is left alone",
          swe_dataset_name({"dataset": "SWE-bench/SWE-bench_Lite"})
          == "SWE-bench/SWE-bench_Lite")
    check("a local dataset path is never rewritten",
          swe_dataset_name({"dataset": "/data/ds.jsonl"}) == "/data/ds.jsonl")
    check("a missing name falls back to Lite", swe_dataset_name({}) == "SWE-bench/SWE-bench_Lite")


def test_swe_harness_gets_the_remapped_name(tmp: Path) -> None:
    """The remap must reach the actual command line, not just the loader."""
    seen: list[list[str]] = []
    original = grade._run_harness
    grade._run_harness = lambda cmd, *a, **kw: seen.append(cmd)
    cfg = {"sources": {"swe_bench": {"dataset": "princeton-nlp/SWE-bench_Lite",
                                     "model_name": "m", "max_workers": 1}}}
    try:
        grade._grade_swe([({}, FakeTask("x", "y", "0" * 40), "baseline", "diff")],
                         "baseline", cfg, tmp)
    except Exception:  # noqa: BLE001  — no report file to read; the cmd is what we assert on
        pass
    finally:
        grade._run_harness = original
    passed = seen[0][seen[0].index("--dataset_name") + 1] if seen else None
    check("the harness is invoked against the remapped dataset",
          passed == "SWE-bench/SWE-bench_Lite")


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="aracne-bench-repos-") as td:
        tmp = Path(td)
        test_seed_from_cache(tmp)
        test_base_commit_is_reachable_not_just_present(tmp)
        test_seed_is_idempotent(tmp)
        test_partial_clone_is_repaired(tmp)
        test_seed_failure_is_reported_not_raised(tmp)
        test_network_failure_is_retried(tmp)
        test_unseeded_repo_leaves_rows_unknown(tmp)
        test_repo_root_is_run_independent(tmp)
        test_legacy_swe_dataset_is_remapped()
        test_swe_harness_gets_the_remapped_name(tmp)

    failed = [name for name, passed in _RESULTS if not passed]
    print("\n" + "=" * 66)
    print(f"{len(_RESULTS) - len(failed)}/{len(_RESULTS)} passed")
    for name in failed:
        print(f"  FAILED: {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

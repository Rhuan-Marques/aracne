#!/usr/bin/env python3
"""Regression tests for the benchmark TIMEOUT rule (bench/bench/outcome.py).

The rule under test: a run that ran out of wall-clock is recorded as outcome="timeout" —
never as a fail, never as a pass, and never inside the success-rate denominator.

No pytest, no network, no Docker, no agent: the grading supervisor is exercised with
`sleep`/`bash` stand-ins, and the agent timeout with a stubbed backend, so this runs
anywhere in a couple of seconds.

    python3 bench/test_timeouts.py
"""
from __future__ import annotations

import contextlib
import io

import pytest
import subprocess
import sys
import tempfile
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from bench import grade, htmlreport, metrics, outcome, report, runner  # noqa: E402
import run_benchmark as rb  # noqa: E402

_RESULTS: list[tuple[str, bool]] = []


def check(name: str, cond) -> None:
    _RESULTS.append((name, bool(cond)))
    print(("PASS  " if cond else "FAIL  ") + name)


# --------------------------------------------------------------------------- #
# 1. classification
# --------------------------------------------------------------------------- #
def test_classify() -> None:
    check("graded True -> correct", outcome.classify({"success": True}) == outcome.CORRECT)
    check("graded False -> fail", outcome.classify({"success": False}) == outcome.FAIL)
    check("ungraded -> unknown", outcome.classify({"success": None}) == outcome.UNKNOWN)
    check("agent timeout -> timeout",
          outcome.classify({"success": None, "timeout_stage": outcome.AGENT}) == outcome.TIMEOUT)
    check("grading timeout -> timeout",
          outcome.classify({"success": None, "timeout_stage": outcome.GRADING}) == outcome.TIMEOUT)
    check("timeout wins over a stale success value",
          outcome.classify({"success": False, "timeout_stage": outcome.GRADING}) == outcome.TIMEOUT)
    check("legacy row (timeout only in `error`) still reads as a timeout",
          outcome.classify({"success": None, "error": "agent timeout"}) == outcome.TIMEOUT)
    check("tally covers every outcome name",
          set(outcome.tally([])) == set(outcome.ALL))


# --------------------------------------------------------------------------- #
# 2. metrics: timeouts stay out of the success rate
# --------------------------------------------------------------------------- #
def _row(lang: str, arm: str, **kw) -> dict:
    row = {"language": lang, "arm": arm, "success": None, "error": None, "timeout_stage": None,
           "input_tokens": 100, "output_tokens": 10, "num_turns": 3, "duration_ms": 1000,
           "cost_usd": 0.0, "scan_time_s": 0.0}
    row.update(kw)
    return row


def _sample_rows() -> list[dict]:
    return [
        _row("rust", "aracne", success=True),
        _row("rust", "aracne", success=False),
        _row("rust", "aracne", timeout_stage=outcome.AGENT, input_tokens=0, num_turns=0,
             error="agent timeout after 1800s"),
        _row("rust", "aracne", timeout_stage=outcome.GRADING, grade_error="harness stalled"),
        _row("rust", "baseline", success=True),
        _row("rust", "baseline", success=True),
    ]


def test_metrics() -> dict:
    rows = _sample_rows()
    st = metrics._arm_stats([r for r in rows if r["arm"] == "aracne"])
    check("timeouts are excluded from n_graded", st["n_graded"] == 2)
    check("success_rate is 1/2, not 1/4", st["success_rate"] == 0.5)
    check("n_timeout counts both", st["n_timeout"] == 2)
    check("timeouts split by stage",
          (st["n_timeout_agent"], st["n_timeout_grading"]) == (1, 1))
    check("outcome tally is exact",
          st["outcomes"] == {"correct": 1, "fail": 1, "timeout": 2, "unknown": 0})
    check("a GRADING timeout keeps the agent's own metrics", st["n_metric"] == 3)

    agg = metrics.aggregate(rows, {"arms": ["baseline", "aracne"], "languages": ["rust"],
                                   "samples": 1, "seeds": 1, "sample_seed": 0,
                                   "run_harness": "claude_code", "model": "opus",
                                   "max_turns": 30})
    check("arm delta reports the timeout gap", agg["overall"]["delta"]["timeout_abs"] == 2)
    return agg


# --------------------------------------------------------------------------- #
# 3. reporting surfaces timeouts separately
# --------------------------------------------------------------------------- #
@pytest.fixture
def agg() -> dict:
    """The aggregate `test_reporting` renders.

    `test_metrics` above RETURNS this value, which pytest does not chain into another
    test — so `test_reporting` errored at collection ("fixture 'agg' not found") and had
    never run. Build it here instead, from the same sample rows.
    """
    return metrics.aggregate(_sample_rows(),
                             {"arms": ["baseline", "aracne"], "languages": ["rust"],
                              "samples": 1, "seeds": 1, "sample_seed": 0,
                              "run_harness": "claude_code", "model": "opus",
                              "max_turns": 30})


def test_reporting(agg: dict, tmp: Path) -> None:
    buf = io.StringIO()
    with contextlib.redirect_stdout(buf):
        report.print_table(agg)
    console = buf.getvalue()
    check("console table has a t/o column", "t/o" in console)
    check("console prints the TIMEOUTS block", "TIMEOUTS (counted separately" in console)
    check("console attributes timeouts to the arm and stage",
          "2 of 4 run(s)" in console and "1 agent, 1 grading" in console)
    check("the PREPARE section still renders after the timeout block",
          "PREPARE" in console or agg.get("preparation") is None)

    html = htmlreport._render(agg, _sample_rows(), {"run_name": "t"}, "")
    check("html has a Timeouts row", "<th class='metric'>Timeouts</th>" in html)
    check("html shows the stage split", "1 agent, 1 grading" in html)

    report.write_results(agg, _sample_rows(), tmp)
    csv_text = (tmp / "summary.csv").read_text(encoding="utf-8")
    check("summary.csv carries the timeout columns",
          "n_timeout,n_timeout_agent,n_timeout_grading" in csv_text)


# --------------------------------------------------------------------------- #
# 4. the grading supervisor: stall cap, total cap, process-group kill
# --------------------------------------------------------------------------- #
def test_grade_supervisor(tmp: Path) -> None:
    grade._POLL_S = 1                      # poll fast so the test stays quick
    watch = tmp / "watch"
    watch.mkdir(exist_ok=True)

    try:
        grade._run_harness(["sleep", "120"], tmp,
                           {"grade_timeout_s": 0, "grade_stall_timeout_s": 3},
                           watch=[watch], label="fake/stall")
        check("a silent hang raises GradeTimeout", False)
    except grade.GradeTimeout as e:
        check("a silent hang raises GradeTimeout", True)
        check("the stall message names the cap and the diagnosis",
              "grade_stall_timeout_s=3" in str(e) and "wedged, not slow" in str(e))

    busy = f"while true; do date >> {watch}/live.log; sleep 1; done"
    started = time.monotonic()
    try:
        grade._run_harness(["bash", "-c", busy], tmp,
                           {"grade_timeout_s": 4, "grade_stall_timeout_s": 3},
                           watch=[watch], label="fake/total")
        check("a harness past its total cap raises GradeTimeout", False)
    except grade.GradeTimeout as e:
        elapsed = time.monotonic() - started
        check("a harness past its total cap raises GradeTimeout", True)
        check("the TOTAL cap fired, not the stall cap", "grade_timeout_s=4" in str(e))
        # The stall cap (3s) must not fire on a harness that is visibly working; the total
        # cap is 4s, so surviving past ~3.5s proves the liveness signal was honoured.
        check(f"steady output survives the stall cap ({elapsed:.1f}s)", elapsed > 3.5)

    marker = watch / "child.log"
    grandchild = f"bash -c 'while true; do echo x >> {marker}; sleep 1; done' & wait"
    with contextlib.suppress(grade.GradeTimeout):
        grade._run_harness(["bash", "-c", grandchild], tmp,
                           {"grade_timeout_s": 3, "grade_stall_timeout_s": 0},
                           watch=[tmp / "does-not-exist"], label="fake/kill")
    at_kill = marker.stat().st_size if marker.exists() else 0
    time.sleep(3)
    after = marker.stat().st_size if marker.exists() else 0
    check("the whole process group is killed (no orphaned grandchild)", after == at_kill)

    try:
        grade._run_harness(["bash", "-c", "exit 3"], tmp,
                           {"grade_timeout_s": 30, "grade_stall_timeout_s": 30},
                           watch=[watch], label="fake/fail")
        check("a failing harness raises CalledProcessError, not GradeTimeout", False)
    except grade.GradeTimeout:
        check("a failing harness raises CalledProcessError, not GradeTimeout", False)
    except subprocess.CalledProcessError:
        check("a failing harness raises CalledProcessError, not GradeTimeout", True)

    try:
        grade._run_harness(["bash", "-c", f"echo hi >> {watch}/done.log"], tmp,
                           {"grade_timeout_s": 30, "grade_stall_timeout_s": 30},
                           watch=[watch], label="fake/ok")
        check("a clean harness returns normally", True)
    except Exception as e:  # noqa: BLE001
        check(f"a clean harness returns normally ({e})", False)


def test_grade_all_marks_timeouts(tmp: Path) -> None:
    class FakeTask:
        key = "org__repo-1"
        source = "multi_swe_bench"
        raw = {"org": "org", "repo": "repo", "number": 1}

    row = {"run_key": "k1", "success": None, "error": None, "timeout_stage": None,
           "input_tokens": 500, "num_turns": 4}
    original = grade._grade_multi

    def boom(*_a, **_kw):
        raise grade.GradeTimeout("multi_swe_bench/aracne: no harness output for 900s")

    grade._grade_multi = boom
    try:
        with contextlib.redirect_stdout(io.StringIO()):
            grade.grade_all([(row, FakeTask(), "aracne", "diff")],
                            {"grade_timeout_s": 1, "grade_stall_timeout_s": 1}, tmp)
    finally:
        grade._grade_multi = original

    check("a grading timeout stamps outcome=timeout", row["outcome"] == outcome.TIMEOUT)
    check("a grading timeout does NOT set success=False", row["success"] is None)
    check("the stage is recorded as grading", row["timeout_stage"] == outcome.GRADING)
    check("`error` stays clear so agent metrics survive", row["error"] is None)
    check("the cause is kept in grade_error", "no harness output" in (row["grade_error"] or ""))


def test_grade_all_salvages_partial_verdicts(tmp: Path) -> None:
    """One wedged instance must not discard the verdicts the harness already wrote."""
    class FakeTask:
        def __init__(self, key, number):
            self.key, self.source = key, "multi_swe_bench"
            self.raw = {"org": "org", "repo": "repo", "number": number}

    def _row(key):
        return {"run_key": key, "success": None, "error": None, "timeout_stage": None,
                "input_tokens": 500, "num_turns": 4}

    graded_ok, graded_bad, wedged = _row("ok"), _row("bad"), _row("wedged")
    items = [(graded_ok, FakeTask("k-ok", 1), "aracne", "diff"),
             (graded_bad, FakeTask("k-bad", 2), "aracne", "diff"),
             (wedged, FakeTask("k-wedged", 3), "aracne", "diff")]
    original = grade._grade_multi

    def boom(*_a, **_kw):
        raise grade.GradeTimeout("multi_swe_bench/aracne: no harness output for 900s",
                                 {"k-ok": True, "k-bad": False})

    grade._grade_multi = boom
    try:
        with contextlib.redirect_stdout(io.StringIO()) as buf:
            grade.grade_all(items, {"grade_timeout_s": 1, "grade_stall_timeout_s": 1}, tmp)
    finally:
        grade._grade_multi = original

    check("a finished instance keeps its pass verdict through a batch timeout",
          graded_ok["success"] is True and graded_ok["outcome"] == outcome.CORRECT)
    check("a finished instance keeps its fail verdict through a batch timeout",
          graded_bad["success"] is False and graded_bad["outcome"] == outcome.FAIL)
    check("only the unjudged instance becomes a timeout",
          wedged["outcome"] == outcome.TIMEOUT and wedged["timeout_stage"] == outcome.GRADING)
    check("salvaged rows are not stamped with grade_error",
          graded_ok.get("grade_error") is None and graded_bad.get("grade_error") is None)
    check("the console reports 1 timeout, not the whole batch",
          "1 run(s) recorded as outcome=timeout" in buf.getvalue())


def test_grade_batches_are_independent(tmp: Path) -> None:
    """A wedged repo must not strand the repos that grade fine."""
    class FakeTask:
        def __init__(self, key, org, repo, number):
            self.key, self.source = key, "multi_swe_bench"
            self.raw = {"org": org, "repo": repo, "number": number}

    def _row(key):
        return {"run_key": key, "success": None, "error": None, "timeout_stage": None}

    good1, good2, bad = _row("g1"), _row("g2"), _row("b1")
    items = [(good1, FakeTask("k-svelte", "sveltejs", "svelte", 9497), "aracne", "diff"),
             (bad, FakeTask("k-vue", "vuejs", "core", 8470), "aracne", "diff"),
             (good2, FakeTask("k-clap", "clap-rs", "clap", 2093), "aracne", "diff")]

    seen_tags = []
    original = grade._grade_multi

    def fake(batch_items, arm, cfg, out_dir, tag=""):
        seen_tags.append(tag)
        if tag == "vuejs__core":
            raise grade.GradeTimeout("multi_swe_bench/aracne/vuejs__core: no harness output for 900s")
        return {t.key: True for _r, t, _a, _p in batch_items}

    grade._grade_multi = fake
    try:
        with contextlib.redirect_stdout(io.StringIO()):
            grade.grade_all(items, {"grade_timeout_s": 1, "grade_stall_timeout_s": 1}, tmp)
    finally:
        grade._grade_multi = original

    check("the group is split into one batch per repo",
          sorted(seen_tags) == ["clap-rs__clap", "sveltejs__svelte", "vuejs__core"])
    check("repos after the wedged one still get graded",
          good1["outcome"] == outcome.CORRECT and good2["outcome"] == outcome.CORRECT)
    check("only the wedged repo's run times out", bad["outcome"] == outcome.TIMEOUT)
    check("non-multi sources stay in a single batch",
          [t for t, _ in grade._batches("swe_bench", items)] == [""])


def test_swe_log_salvage(tmp: Path) -> None:
    """_parse_swe_logs reads per-instance reports when the final report was never written."""
    class FakeTask:
        def __init__(self, key):
            self.key = key

    run_id, model = "aracne-aracne", "aracne-bench-aracne"
    root = tmp / "logs" / "run_evaluation" / run_id / model
    (root / "inst-1").mkdir(parents=True)
    (root / "inst-2").mkdir(parents=True)
    (root / "inst-1" / "report.json").write_text('{"inst-1": {"resolved": true}}')
    (root / "inst-2" / "report.json").write_text('{"inst-2": {"resolved": false}}')
    items = [(None, FakeTask("inst-1"), "aracne", ""), (None, FakeTask("inst-2"), "aracne", ""),
             (None, FakeTask("inst-3"), "aracne", "")]

    got = grade._parse_swe_logs(tmp, run_id, model, items)
    check("swe per-instance logs yield the finished verdicts", got == {"inst-1": True, "inst-2": False})
    check("an instance with no report stays unjudged", "inst-3" not in got)


# --------------------------------------------------------------------------- #
# 5. the run loop: agent timeout, never graded, kept across --continue
# --------------------------------------------------------------------------- #
def _repo_and_task(tmp: Path, name: str = "repo"):
    """A one-commit git repo plus the FakeTask that points at it (shared by the run tests)."""
    repo = tmp / name
    repo.mkdir(exist_ok=True)
    for cmd in (["git", "init", "-q"], ["git", "config", "user.email", "t@t"],
                ["git", "config", "user.name", "t"]):
        subprocess.run(cmd, cwd=repo, check=True, capture_output=True)
    (repo / "a.txt").write_text("hello\n")
    subprocess.run(["git", "add", "-A"], cwd=repo, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-qm", "base"], cwd=repo, check=True, capture_output=True)
    base = subprocess.run(["git", "rev-parse", "HEAD"], cwd=repo,
                          capture_output=True, text=True).stdout.strip()

    class FakeTask:
        key = "org__repo-1"
        language = "rust"
        source = "multi_swe_bench"
        base_commit = base
        problem_statement = "fix it"
        raw = {"org": "org", "repo": "repo", "number": 1}

        def repo_slug(self):
            return "org__repo"

    return repo, FakeTask


def test_agent_timeout(tmp: Path) -> None:
    from bench import agents, arms

    repo, FakeTask = _repo_and_task(tmp)
    prev_prepare, prev_agent = arms.prepare_workdir, agents.run_agent

    def timed_out(*_a, **_kw):
        (repo / "a.txt").write_text("hello world\n")   # partial work, then the cap fires
        raise subprocess.TimeoutExpired(cmd="claude", timeout=1800)

    arms.prepare_workdir = lambda *_a, **_kw: repo
    agents.run_agent = timed_out
    try:
        cfg = {"run_harness": "claude_code", "model": "opus", "max_turns": 30,
               "timeout_s": 1800, "keep_workdir": True}
        row, _patch = runner.run_one(FakeTask(), "aracne", 0, cfg, tmp,
                                     tmp / "repos", tmp / "work", tmp / "fx")
    finally:
        arms.prepare_workdir, agents.run_agent = prev_prepare, prev_agent

    check("an agent timeout stamps outcome=timeout", row["outcome"] == outcome.TIMEOUT)
    check("the stage is recorded as agent", row["timeout_stage"] == outcome.AGENT)
    check("success stays None", row["success"] is None)
    check("the error names the cap that fired", row["error"] == "agent timeout after 1800s")
    check("the partial patch is still kept for debugging", row["has_patch"] is True)

    check("a timed-out run is never sent to the grader",
          rb._collect_to_grade([row], [FakeTask()], tmp) == [])
    healthy = dict(row, error=None, timeout_stage=None, outcome=outcome.UNKNOWN)
    check("a normal patched run IS sent to the grader",
          len(rb._collect_to_grade([healthy], [FakeTask()], tmp)) == 1)


def test_turn_limit(tmp: Path) -> None:
    """`--max-turns` exhaustion must behave like a timeout, not like a broken step.

    Claude Code reports the cap as a bare `is_error` with NO `result` text, so the only
    evidence is `num_turns` reaching the cap; the run loop must record it and keep going
    instead of parking the whole matrix as PENDING.
    """
    from bench import agents, arms
    from bench.claude_driver import RunResult

    repo, FakeTask = _repo_and_task(tmp, "repo-turns")
    prev_prepare, prev_agent = arms.prepare_workdir, agents.run_agent

    def capped(*_a, **_kw):
        (repo / "a.txt").write_text("hello world\n")   # partial work, then the cap fires
        return RunResult(result_text="", num_turns=41, duration_ms=292732, is_error=True,
                         raw={"is_error": True, "num_turns": 41, "stop_reason": "tool_use"})

    arms.prepare_workdir = lambda *_a, **_kw: repo
    agents.run_agent = capped
    cfg = {"run_harness": "claude_code", "model": "haiku", "max_turns": 40,
           "timeout_s": 1800, "keep_workdir": True}
    try:
        row, _patch = runner.run_one(FakeTask(), "aracne", 0, cfg, tmp,
                                     tmp / "repos", tmp / "work", tmp / "fx")

        check("hitting max_turns stamps outcome=timeout", row["outcome"] == outcome.TIMEOUT)
        check("the stage is recorded as turns", row["timeout_stage"] == outcome.TURNS)
        check("success stays None", row["success"] is None)
        check("the error names the cap, not a raw JSON blob",
              row["error"] == "agent turn limit: hit max_turns=40 after 41 turns")
        check("the turn count survives for the token/turn tables", row["num_turns"] == 41)
        check("a turn-capped run is never sent to the grader",
              rb._collect_to_grade([row], [FakeTask()], tmp) == [])
        check("nor is it re-graded from its half-written patch",
              rb._collect_to_regrade([row], [FakeTask()]) == [])
        check("--continue keeps it, --retry-timeouts re-runs it",
              rb._keep_on_continue(row, retry_timeouts=False)
              and not rb._keep_on_continue(row, retry_timeouts=True))

        # A genuine agent failure BELOW the cap stays a hard error (still fatal to the matrix).
        def broke(*_a, **_kw):
            return RunResult(result_text="Credit balance too low", num_turns=3, is_error=True)

        agents.run_agent = broke
        bad, _ = runner.run_one(FakeTask(), "aracne", 0, cfg, tmp,
                                tmp / "repos", tmp / "work", tmp / "fx")
        check("an error below the cap is still a hard error",
              bad["outcome"] == outcome.UNKNOWN and bad["timeout_stage"] is None
              and bad["error"].startswith("agent_error: Credit balance"))

        # OpenCode ignores --max-turns, so its turn count is not evidence of exhaustion.
        agents.run_agent = capped
        oc, _ = runner.run_one(FakeTask(), "aracne", 0, dict(cfg, run_harness="opencode"), tmp,
                               tmp / "repos", tmp / "work", tmp / "fx")
        check("a harness that ignores the cap is not misread as capped",
              oc["timeout_stage"] is None and oc["outcome"] == outcome.UNKNOWN)
    finally:
        arms.prepare_workdir, agents.run_agent = prev_prepare, prev_agent


def test_sigint_kills_harness(tmp: Path) -> None:
    """Ctrl-C must take the grading harness with it.

    `_run_harness` starts the harness in its own session so it can be signalled as a group;
    that also detaches it from the terminal, so without an explicit KeyboardInterrupt handler
    a Ctrl-C would leave the harness (and its docker builds) running invisibly.
    """
    import signal

    marker = tmp / "sigint-alive.log"
    child = (
        "import sys\n"
        "from pathlib import Path\n"
        f"sys.path.insert(0, {str(HERE)!r})\n"
        "from bench import grade\n"
        "grade._POLL_S = 1\n"
        f"grade._run_harness(['bash', '-c', 'while true; do echo x >> {marker}; sleep 1; done'],\n"
        "                   Path('.'), {'grade_timeout_s': 0, 'grade_stall_timeout_s': 0},\n"
        "                   watch=[], label='fake/sigint')\n"
    )
    proc = subprocess.Popen([sys.executable, "-c", child], cwd=str(HERE.parent),
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(4)
    running = marker.exists() and marker.stat().st_size > 0
    check("the stubbed harness is running before the interrupt", running)

    proc.send_signal(signal.SIGINT)
    try:
        proc.wait(timeout=30)
        exited = True
    except subprocess.TimeoutExpired:
        proc.kill()
        exited = False
    check("the benchmark process exits on SIGINT", exited)

    at_exit = marker.stat().st_size if marker.exists() else 0
    time.sleep(4)
    after = marker.stat().st_size if marker.exists() else 0
    check("the harness is killed too (no orphan left writing)", after == at_exit)


def test_continue_policy() -> None:
    done = {"run_key": "a", "success": True, "error": None}
    errored = {"run_key": "b", "success": None, "error": "runner crash: boom"}
    timed_out = {"run_key": "c", "success": None, "timeout_stage": outcome.AGENT,
                 "error": "agent timeout after 1800s"}
    rows = [done, errored, timed_out]

    kept = {r["run_key"] for r in rows if rb._keep_on_continue(r, retry_timeouts=False)}
    check("--continue keeps timeouts and re-runs errors", kept == {"a", "c"})
    retried = {r["run_key"] for r in rows if rb._keep_on_continue(r, retry_timeouts=True)}
    check("--retry-timeouts re-runs the timeouts too", retried == {"a"})


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="aracne-bench-timeout-") as td:
        tmp = Path(td)
        test_classify()
        agg = test_metrics()
        test_reporting(agg, tmp)
        test_grade_supervisor(tmp)
        test_grade_all_marks_timeouts(tmp)
        test_grade_all_salvages_partial_verdicts(tmp)
        test_swe_log_salvage(tmp / "swelogs")
        test_grade_batches_are_independent(tmp)
        test_agent_timeout(tmp)
        test_turn_limit(tmp)
        test_sigint_kills_harness(tmp)
        test_continue_policy()

    failed = [name for name, passed in _RESULTS if not passed]
    print("\n" + "=" * 66)
    print(f"{len(_RESULTS) - len(failed)}/{len(_RESULTS)} passed")
    for name in failed:
        print(f"  FAILED: {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

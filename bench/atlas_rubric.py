#!/usr/bin/env python3
"""Grade a SWE-Atlas refactor with the task's OWN rubric judge, on the host, without its tests.

WHAT IS OFFICIAL AND WHAT IS NOT. Each task ships `tests/evaluate_rubrics.py`, its system prompt,
its user template, `rubrics.json` and `prompt.txt` (the issue text WITHOUT the interface block --
the judge never saw the specification even upstream). This loads that module and calls its own
`evaluate_rubrics()`, so the prompts, the response parsing, the negative-rubric inversion and the
`must_have_pass` / `agg_score` aggregation are the benchmark's, byte for byte. Three things
differ, and each is deliberate:

  1. NO HIDDEN TESTS. The navigation variant withholds the interface specification, and the
     hidden tests call the new code by the names it gave away. See bench/atlas_prompt.py.
  2. NO CONTAINER. The rubric judge reads a diff and nothing else; the verifier's container
     exists for the tests. Skipping it skips a multi-gigabyte image build per task.
  3. A NAMING CLARIFICATION appended to the judge's system prompt (NAMING_CLARIFICATION, on by
     default, `naming_lenient=False` to grade strictly). Rubric items name exact identifiers
     ("Renames GetIngester to CreateIngester") that only the withheld specification told the
     agent to use. Without it, a correct refactor under another name fails the judge for the
     same reason it fails the tests. Behaviour, scope and counts are still required.

The judge is reached through atlas_judge.complete (the Claude CLI), and the per-item calls run in
parallel: they are independent, and evaluate_rubrics() makes them one at a time. The official
function then runs against a replay client holding those answers, so its own logic scores them.
"""
from __future__ import annotations

import argparse
import contextlib
import io
import hashlib
import importlib.util
import json
import os
import shutil
import sys
import tempfile
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import atlas_judge  # noqa: E402

JUDGE_MODEL = os.environ.get("ATLAS_JUDGE_MODEL", "opus")

NAMING_CLARIFICATION = """

### Clarification (Identifier Names in This Evaluation)

- The agent that produced this response was given ONLY the problem statement above. It was NOT given the interface specification (new file paths, type, function or field names, signatures) that the rubric criteria were written against.
- Therefore any identifier, file path or package name that a `rubric_statement` mentions for code the refactor INTRODUCES or RENAMES is illustrative, not a required spelling. If the response introduces or changes a construct that plays the described role with the described behaviour and scope, it meets the criterion even under a different, reasonable name or location.
- Names of code that already existed before the change (the code being refactored, its callers) are NOT illustrative: the criterion refers to that exact existing code.
- Everything else in the criterion is still required in full: the behaviour, which existing code is changed or removed, and any counts or lists of affected call sites, fields or handlers.
"""


class _Replay:
    """An OpenAI-shaped client whose answers were fetched in advance, keyed by prompt."""

    def __init__(self, answers: dict[str, str]):
        self.answers = answers
        self.chat = self
        self.completions = self

    def create(self, model, messages, **_kw):
        text = self.answers.get(_key(messages), "")
        msg = type("M", (), {"content": text})()
        choice = type("C", (), {"message": msg})()
        return type("R", (), {"choices": [choice]})()


class _Recorder:
    """An OpenAI-shaped client that records every request instead of answering it."""

    def __init__(self):
        self.requests: list[list[dict]] = []
        self.chat = self
        self.completions = self

    def create(self, model, messages, **_kw):
        self.requests.append(messages)
        msg = type("M", (), {"content": ""})()
        return type("R", (), {"choices": [type("C", (), {"message": msg})()]})()


def _key(messages: list[dict]) -> str:
    return hashlib.sha256(json.dumps(messages, sort_keys=True).encode()).hexdigest()


def _load_official(tests_dir: Path):
    spec = importlib.util.spec_from_file_location(f"evaluate_rubrics_{abs(hash(str(tests_dir)))}",
                                                  tests_dir / "evaluate_rubrics.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    # Unparseable replies retry with backoff upstream; a replay has nothing new to offer.
    mod.MAX_RETRIES = 1
    mod.time.sleep = lambda _s: None
    return mod


def grade(task_dir: str | Path, patch_text: str, *, naming_lenient: bool = True,
          tests_in_scope: bool = False, drop_rubrics=(),
          parallel: int = 4, model: str = JUDGE_MODEL, log=print) -> dict:
    """Rubric verdict for one patch: the official evaluate_rubrics() result, plus how it was run.

    tests_in_scope: the agent was asked to update the tests its change affects (bench/bench/
    atlas_tests.py). The judge's issue text loses its "tests are already done, do not touch them"
    claim and gains a note saying the opposite, its system prompt gets TESTS_CLARIFICATION, and the
    rubric items in drop_rubrics -- ones that penalise modifying tests -- are not asked."""
    from bench import atlas_tests
    from bench.atlas_prompt import neutralize_test_claim
    tests = Path(task_dir) / "tests"
    out = {"must_have_pass": None, "agg_score": None, "error": None,
           "grading": "rubric-only (no hidden tests)",
           "naming_lenient": naming_lenient, "tests_in_scope": tests_in_scope,
           "dropped_rubrics": sorted(drop_rubrics), "judge_model": model}
    if not patch_text.strip():
        out.update(must_have_pass=False, agg_score=0.0, error="empty patch")
        return out
    with tempfile.TemporaryDirectory(prefix="atlas-rubric-") as tmp:
        staged = Path(tmp)
        for name in ("rubrics_system_prompt.txt", "rubrics_user_prompt_template.txt",
                     "rubrics.json", "prompt.txt"):
            if (tests / name).exists():
                shutil.copy2(tests / name, staged / name)
        if naming_lenient:
            sysp = staged / "rubrics_system_prompt.txt"
            text = sysp.read_text()
            marker = "## Final Instructions"
            sysp.write_text(text.replace(marker, NAMING_CLARIFICATION.strip() + "\n\n\n" + marker, 1)
                            if marker in text else text + NAMING_CLARIFICATION)

        if tests_in_scope:
            sysp = staged / "rubrics_system_prompt.txt"
            text = sysp.read_text()
            marker = "## Final Instructions"
            sysp.write_text(text.replace(marker, atlas_tests.TESTS_CLARIFICATION.strip() + "\n\n\n" + marker, 1)
                            if marker in text else text + atlas_tests.TESTS_CLARIFICATION)
            prompt = staged / "prompt.txt"
            prompt.write_text(neutralize_test_claim(prompt.read_text())[0].rstrip("\n")
                              + atlas_tests.TESTS_PROBLEM_NOTE)
            if drop_rubrics:
                items = json.loads((staged / "rubrics.json").read_text() or "[]")
                (staged / "rubrics.json").write_text(json.dumps([r for r in items if r.get("id") not in set(drop_rubrics)]))

        mod = _load_official(tests)
        mod.TESTS_DIR = str(staged)
        rubrics = json.loads((staged / "rubrics.json").read_text() or "[]")
        problem = mod.read_file(str(staged / "prompt.txt"))

        # Pass 1: let the official code build every request.
        rec = _Recorder()
        with contextlib.redirect_stdout(io.StringIO()):
            mod.evaluate_rubrics(rec, model, problem, patch_text, rubrics)

        # Pass 2: answer them, in parallel.
        failures: list[str] = []

        def ask(messages):
            try:
                return _key(messages), atlas_judge.complete(model, messages, wants_json=True)
            except atlas_judge.JudgeError as e:
                log(f"      judge error: {e}")
                failures.append(str(e))
                return _key(messages), ""
        with ThreadPoolExecutor(max_workers=max(1, parallel)) as pool:
            answers = dict(pool.map(ask, rec.requests))

        # Pass 3: the official code scores the answers.
        with contextlib.redirect_stdout(io.StringIO()):
            result = mod.evaluate_rubrics(_Replay(answers), model, problem, patch_text, rubrics)
    out.update({k: result.get(k) for k in ("must_have_pass", "agg_score", "rubric_count",
                                           "evaluable_count", "must_have_count")})
    out["rubric_scores"] = result.get("rubric_scores")
    out["n_judge_calls"] = len(rec.requests)
    # The official parser scores an unparseable reply as neutral (it cannot fail a must-have).
    # That is right for a judge that answered badly, and wrong for one that never answered: an
    # infrastructure failure would read as a pass. Unknown is the honest verdict.
    if failures:
        out["error"] = f"{len(failures)} of {len(rec.requests)} judge calls failed: {failures[0][:200]}"
        out["must_have_pass"] = None
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--task-dir", required=True)
    ap.add_argument("--patch", required=True)
    ap.add_argument("--strict-naming", action="store_true", help="grade without the naming clarification")
    ap.add_argument("--parallel", type=int, default=4)
    args = ap.parse_args()
    r = grade(args.task_dir, Path(args.patch).read_text(), naming_lenient=not args.strict_naming,
              parallel=args.parallel)
    print(json.dumps({k: v for k, v in r.items() if k != "rubric_scores"}, indent=2))
    return 0 if r.get("error") is None else 1


if __name__ == "__main__":
    sys.exit(main())

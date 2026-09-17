"""SWE-Atlas "tests in scope": the agent updates the tests its refactor breaks, instead of being told
they are already done and off limits.

WHY. Upstream, a Refactoring task's container carries the updated tests (tests/test_patch.diff) and
the prompt says "I've already taken care of all changes to the test files. Do NOT modify any test
files". In this harness the workspace holds the tests at the BASE commit, so a correct refactor
leaves them broken, and the agent is forbidden to touch them -- a real change never ships like
that, and following the callers into the tests is exactly the navigation the benchmark measures.

WHAT CHANGES, for a task in TASKS and a run with `atlas_tests_in_scope: true`:
  - the agent prompt (claude_driver.ATLAS_TESTS_PROMPT_TEMPLATE) no longer forbids test changes, and
    does not ask for them either: tests go unmentioned, as in a real request;
  - the gold patch is solution/gold.patch PLUS tests/test_patch.diff, and localization scores test
    files like any other file (localization.score_row, include_tests);
  - the rubric judge is told the tests were in scope: the upstream prompt it reads has its
    tests-are-done claim removed, its system prompt gets TESTS_CLARIFICATION, and rubric items that
    penalise modifying tests are dropped (TASKS[key]["drop_rubrics"]).

ONLY THESE TASKS, deliberately: each was checked by hand -- the test patch applies on the fixture
base together with the gold patch, and every rubric item was read for a rule the change contradicts.
Gated by the run config as well, so regrading an older run never silently changes its gold.
"""
from __future__ import annotations

TASKS: dict[str, dict] = {
    "grafana_k6-10d4b451": {},
    "grafana_grafana-10d4b455": {},
    "grafana_k6-10d4b446": {},
    # "4.2: Modifies existing test files in the repository" (negative, must have) -- the opposite of
    # what this variant asks for.
    "grafana_k6-10d4b445": {"drop_rubrics": ["869c0961a76c7f3071391f29695643ef"]},
    # Its 4.2 ("Deletes any of the 4 test files ... without relocating them") stays: relocating the
    # tests along with the code is what updating them means here.
    "grafana_k6-10d4b447": {},
}

TESTS_CLARIFICATION = """

### Clarification (Test Files in This Evaluation)

- The test files were NOT already updated for the agent that produced this response, and it was not told to leave them alone: keeping the repository's tests building and passing after its change was part of the work.
- Changes to test files in the response are therefore expected and must not count against it. Judge each criterion on the whole response, test files included.
"""

TESTS_PROBLEM_NOTE = ("\n\nThe test files have NOT been updated: updating the tests affected by the change is part "
                      "of the task.\n")


def in_scope(key: str, cfg: dict | None) -> bool:
    """Whether this task's tests are the agent's to fix in this run."""
    return bool((cfg or {}).get("atlas_tests_in_scope")) and key in TASKS


def dropped_rubrics(key: str) -> set[str]:
    return set(TASKS.get(key, {}).get("drop_rubrics", ()))

"""Agent-backend dispatch: drive a prompt through Claude Code or OpenCode uniformly.

Both backends return a `claude_driver.RunResult`. Used for BOTH description generation
(the `/descriptions-generate` command) and the A/B run (a task-solving prompt), so a single
`--gen-harness`/`--run-harness` switch picks the engine and `--gen-model`/`--model` the model.

Model strings:
  - claude_code : an alias or id understood by `claude --model` (e.g. `haiku`, `sonnet`).
  - opencode    : `provider/model` (e.g. `deepseek/deepseek-v4-flash`).
"""
from __future__ import annotations

from . import claude_driver, opencode_driver

HARNESSES = ("claude_code", "opencode")


def run_agent(harness: str, prompt: str, cwd, model: str, max_turns: int, timeout_s: int, *,
              agent: str | None = None, extra_args: list[str] | None = None):
    """Run `prompt` (verbatim — already templated by the caller) in `cwd` and return a RunResult."""
    if harness == "claude_code":
        return claude_driver.run_raw(prompt, cwd, model, max_turns, timeout_s, extra_args)
    if harness == "opencode":
        return opencode_driver.run_raw(prompt, cwd, model, max_turns, timeout_s,
                                       agent=agent, extra_args=extra_args)
    raise ValueError(f"unknown harness {harness!r}; expected one of {HARNESSES}")


def task_prompt(problem: str) -> str:
    """The task-solving prompt for the A/B run (shared across harnesses)."""
    return claude_driver.PROMPT_TEMPLATE.format(problem=problem)

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
              agent: str | None = None, extra_args: list[str] | None = None,
              stream: bool = False, effort: str | None = None,
              isolate_operator_config: bool = False,
              allowed_tools: list[str] | None = None,
              builtin_tools: list[str] | None = None,
              deny_repo: str | None = None,
              guard_log: str | None = None,
              extra_env: dict | None = None):
    """Run `prompt` (verbatim — already templated by the caller) in `cwd` and return a RunResult.

    `stream` asks the backend for a per-event transcript on `RunResult.transcript`, which
    bench/toolstats.py folds into per-tool telemetry. Only claude_code honours it today;
    OpenCode already walks its own NDJSON but discards the bodies (opencode_driver:96-99),
    so it returns an empty transcript and toolstats records the zero row.

    `effort` is claude_code-only (the CLI's `--effort`). OpenCode exposes no equivalent, so
    it is dropped there rather than faked: a silently-ignored cost lever would make two runs
    look comparable when they are not.
    """
    if harness == "claude_code":
        return claude_driver.run_raw(prompt, cwd, model, max_turns, timeout_s, extra_args,
                                     stream=stream, effort=effort,
                                     isolate_operator_config=isolate_operator_config,
                                     allowed_tools=allowed_tools,
                                     builtin_tools=builtin_tools,
                                     deny_repo=deny_repo,
                                     guard_log=guard_log, extra_env=extra_env)
    if harness == "opencode":
        return opencode_driver.run_raw(prompt, cwd, model, max_turns, timeout_s,
                                       agent=agent, extra_args=extra_args)
    raise ValueError(f"unknown harness {harness!r}; expected one of {HARNESSES}")


def task_prompt(problem: str) -> str:
    """The task-solving prompt for the A/B run (shared across harnesses)."""
    return claude_driver.PROMPT_TEMPLATE.format(problem=problem)

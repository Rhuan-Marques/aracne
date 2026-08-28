"""Shared pytest fixtures for the bench suite.

`test_chunkgen.py`, `test_parallel.py`, `test_timeouts.py` and `test_graderepos.py` all
request a `tmp` fixture that nothing defined — there was no conftest.py in the tree — so
every test in them errored at collection and none had ever run. `tmp` is just `tmp_path`
under the name those files use.

NOTE on running them: this checkout's bench fixtures leaked an editable install of
pytest 4.6 (`bench/fixtures/pytest-dev__pytest@1aefb24b37c3`) into user site-packages, and
that version's assertion rewriter cannot parse Python 3.12 ASTs. Until that is removed,
run with `--assert=plain`.
"""
from __future__ import annotations

from pathlib import Path

import pytest


@pytest.fixture
def tmp(tmp_path: Path) -> Path:
    """A per-test temporary directory (alias for pytest's `tmp_path`)."""
    return tmp_path

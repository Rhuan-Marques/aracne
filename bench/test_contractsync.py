#!/usr/bin/env python3
"""Regression tests for fixtures.sync_agent_contract.

THE BUG. The per-cell contract sync ran `arac init --claude -y`. `arac init` lost that flag to
`arac setup` on 2026-09-06, the call exited 2, and the sync swallowed the error -- so every
aracne cell after it ran the CLAUDE.md frozen at prepare time instead of the one its arm config
renders. Nothing in a run's output said so.

These tests pin the spelling and the loud failure without needing `arac`.

    python3 -m pytest bench/test_contractsync.py
"""
from __future__ import annotations

import subprocess

import pytest

from bench import fixtures


def test_sync_runs_arac_setup_for_claude(monkeypatch, tmp_path):
    calls = []

    def fake_run(argv, **kw):
        calls.append((argv, kw.get("cwd")))
        return subprocess.CompletedProcess(argv, 0, "", "")

    monkeypatch.setattr(fixtures.subprocess, "run", fake_run)
    assert fixtures.sync_agent_contract(tmp_path, "arac") is True
    assert calls == [(["arac", "setup", "--claude", "-y"], str(tmp_path))]


def test_sync_failure_stops_the_cell(monkeypatch, tmp_path):
    def failing(argv, **kw):
        raise subprocess.CalledProcessError(2, argv, output="", stderr="flag provided but not defined: -claude")

    monkeypatch.setattr(fixtures.subprocess, "run", failing)
    with pytest.raises(RuntimeError, match="flag provided but not defined"):
        fixtures.sync_agent_contract(tmp_path, "arac")


def test_missing_binary_stops_the_cell(monkeypatch, tmp_path):
    def missing(argv, **kw):
        raise FileNotFoundError("arac")

    monkeypatch.setattr(fixtures.subprocess, "run", missing)
    with pytest.raises(RuntimeError, match="could not run"):
        fixtures.sync_agent_contract(tmp_path, "arac")


def test_real_arac_setup_accepts_the_flags(tmp_path):
    """The spelling against the installed binary, when there is one: `-h` parses the flag set
    without writing anything, and an unknown flag is exactly what exits non-zero."""
    import shutil
    arac = shutil.which("arac")
    if not arac:
        pytest.skip("arac not on PATH")
    r = subprocess.run([arac, "setup", "-h"], capture_output=True, text=True, timeout=30)
    usage = r.stdout + r.stderr
    assert "-claude" in usage and "-y" in usage, usage

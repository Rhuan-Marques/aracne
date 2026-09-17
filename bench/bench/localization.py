"""Localization scoring for SWE-Atlas refactors: did the agent change the code the reference
solution changes, and how much did it spend finding it?

WHY THIS AND NOT THE HIDDEN TESTS. The navigation variant (see atlas_prompt.py) strips the
interface specification from the task, and the hidden tests call the new code by exactly the
names that specification gave away. A correct refactor under different names fails them. What
the tests were never measuring anyway is the question aracne answers: WHERE does the change go.
That has a ground truth that no naming choice can move -- the files and declarations that exist
at the base commit and that the reference patch modifies or deletes.

EVERYTHING IS MEASURED ON THE BASE. Both patches are read as edits to the base tree: a changed
line's OLD-side position is mapped to the innermost declaration covering it, using the topology
database frozen at prepare time (the fixture snapshot), so the gold patch and every arm's patch
resolve through the same index. New files and new declarations are counted but not scored:
where a new thing lives, and what it is called, is a design choice, not a navigation result.

Deterministic and free: no model, no container, no network.
"""
from __future__ import annotations

import json
import re
import sqlite3
from dataclasses import dataclass, field
from pathlib import Path

# Files that are aracne's or the harness's own, never part of a solution.
_HARNESS = re.compile(r"(^|/)(\.aracne|\.claude|\.opencode)(/|$)|^(CLAUDE|AGENTS)\.md$|^\.mcp\.json$")
# Test files by the conventions of the languages SWE-Atlas covers. The task's own
# tests/test_files.json is consulted too; this catches the ones it does not list.
_TEST = re.compile(
    r"(^|/)(tests?|__tests__|testdata|fixtures?|spec)(/|$)|_test\.(go|py|rs|c|cc|cpp)$|(^|/)test_[^/]+\.py$"
    r"|\.(test|spec)\.[jt]sx?$|Test\.java$|(^|/)conftest\.py$", re.I)
# Declaration kinds worth locating. Files, packages and imports are containers, not fix sites.
_DECL_KINDS = {"function", "method", "struct", "interface", "named_type", "variable", "class",
               "enum", "trait", "impl", "constant", "type"}
_HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


@dataclass
class FileChange:
    path: str                      # path at base (old side); new path for an added file
    status: str = "modified"       # modified | added | deleted | renamed
    old_lines: set[int] = field(default_factory=set)


def parse_patch(text: str) -> dict[str, FileChange]:
    """Changed files of a unified diff, keyed by base path, with the base line numbers each
    change touches. A pure insertion is attributed to the base line it follows (or line 1)."""
    files: dict[str, FileChange] = {}
    cur: FileChange | None = None
    old_ln = 0
    old_path = new_path = None
    for raw in (text or "").splitlines():
        if raw.startswith("diff --git "):
            m = re.match(r"^diff --git a/(.+?) b/(.+)$", raw)
            old_path, new_path = (m.group(1), m.group(2)) if m else (None, None)
            cur = None
            continue
        if raw.startswith("--- "):
            p = raw[4:].strip()
            old_path = None if p == "/dev/null" else re.sub(r"^a/", "", p)
            continue
        if raw.startswith("+++ "):
            p = raw[4:].strip()
            new_path = None if p == "/dev/null" else re.sub(r"^b/", "", p)
            if old_path is None and new_path:
                cur = files.setdefault(new_path, FileChange(new_path, "added"))
            elif new_path is None and old_path:
                cur = files.setdefault(old_path, FileChange(old_path, "deleted"))
            elif old_path:
                status = "renamed" if new_path and new_path != old_path else "modified"
                cur = files.setdefault(old_path, FileChange(old_path, status))
            continue
        if raw.startswith("rename from "):
            # A pure `git mv` has no ---/+++ headers and no hunks: the rename line is the only
            # record that the base file was moved away.
            src = raw[len("rename from "):].strip()
            cur = files.setdefault(src, FileChange(src, "renamed"))
            continue
        m = _HUNK.match(raw)
        if m:
            old_ln = int(m.group(1))
            continue
        if cur is None:
            continue
        if raw.startswith("-"):
            cur.old_lines.add(old_ln)
            old_ln += 1
        elif raw.startswith("+"):
            cur.old_lines.add(max(old_ln - 1, 1))
        elif raw.startswith(" "):
            old_ln += 1
    return files


def is_source(path: str, test_files: set[str] = frozenset(), include_tests: bool = False) -> bool:
    """Whether a changed path counts toward localization. Harness files never do; test files do only
    when the run put the task's tests in scope (bench/bench/atlas_tests.py)."""
    if _HARNESS.search(path):
        return False
    return include_tests or not (_TEST.search(path) or path in test_files)


class BaseIndex:
    """The declarations at the base commit, from the fixture's frozen topology database."""

    def __init__(self, db_path: Path | None):
        self.by_file: dict[str, list[tuple[int, int, str]]] = {}
        self.languages: set[str] = set()
        if not db_path or not Path(db_path).exists():
            return
        con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        try:
            rows = con.execute("select id, kind, loc_path, starts_at, ends_at, language from resources").fetchall()
        finally:
            con.close()
        paths = [r[2] for r in rows if r[2]]
        root = _common_root(paths)
        for rid, kind, path, start, end, lang in rows:
            if kind not in _DECL_KINDS or not path or not start:
                continue
            rel = path[len(root):].lstrip("/") if root and path.startswith(root) else path
            self.by_file.setdefault(rel, []).append((int(start), int(end or start), rid))
            if lang:
                self.languages.add(lang)

    def indexed(self, path: str) -> bool:
        return path in self.by_file

    def decls_at(self, path: str, lines: set[int]) -> set[str]:
        """The innermost declaration covering each line; lines outside any declaration (imports,
        top-level statements) map to nothing."""
        spans = self.by_file.get(path) or []
        out = set()
        for ln in lines:
            best = None
            for start, end, rid in spans:
                if start <= ln <= end and (best is None or (end - start) < (best[1] - best[0])):
                    best = (start, end, rid)
            if best:
                out.add(best[2])
        return out


def _common_root(paths: list[str]) -> str:
    """The worktree prefix every indexed path shares, up to (and including) `/worktree`."""
    for p in paths:
        i = p.find("/worktree/")
        if i >= 0:
            return p[: i + len("/worktree")]
    return ""


def _ratio(num: int, den: int):
    return round(num / den, 4) if den else None


def _f1(p, r):
    if p is None or r is None or (p + r) == 0:
        return None if p is None or r is None else 0.0
    return round(2 * p * r / (p + r), 4)


def score(gold_patch: str, agent_patch: str, index: BaseIndex, test_files: set[str] = frozenset(),
          include_tests: bool = False) -> dict:
    """File- and declaration-level recall/precision of an agent patch against the gold patch,
    over code that exists at base."""
    gold = {p: c for p, c in parse_patch(gold_patch).items() if is_source(p, test_files, include_tests)}
    agent = {p: c for p, c in parse_patch(agent_patch).items() if is_source(p, test_files, include_tests)}

    gold_existing = {p for p, c in gold.items() if c.status != "added"}
    agent_existing = {p for p, c in agent.items() if c.status != "added"}
    hit_files = gold_existing & agent_existing
    file_recall = _ratio(len(hit_files), len(gold_existing))
    file_precision = _ratio(len(hit_files), len(agent_existing)) if agent_existing else (None if not agent else 0.0)

    # Declarations only where the index covers the file: a C file in a repo aracne does not scan
    # has no declarations to compare, and counting it as "found nothing" would be a lie.
    scorable = {p for p in gold_existing | agent_existing if index.indexed(p)}
    gold_decls = set().union(*(index.decls_at(p, gold[p].old_lines) for p in gold_existing & scorable)) \
        if gold_existing & scorable else set()
    agent_decls = set().union(*(index.decls_at(p, agent[p].old_lines) for p in agent_existing & scorable)) \
        if agent_existing & scorable else set()
    hit_decls = gold_decls & agent_decls
    decl_recall = _ratio(len(hit_decls), len(gold_decls))
    decl_precision = _ratio(len(hit_decls), len(agent_decls)) if gold_decls else None
    if gold_decls and not agent_decls:
        decl_precision = None if not agent_existing else 0.0

    return {
        "gold_files": sorted(gold_existing),
        "gold_new_files": sorted(p for p, c in gold.items() if c.status == "added"),
        "agent_files": sorted(agent_existing),
        "agent_new_files": sorted(p for p, c in agent.items() if c.status == "added"),
        "missed_files": sorted(gold_existing - agent_existing),
        "extra_files": sorted(agent_existing - gold_existing),
        "file_recall": file_recall,
        "file_precision": file_precision,
        "file_f1": _f1(file_precision, file_recall),
        "gold_decls": sorted(gold_decls),
        "agent_decls": sorted(agent_decls),
        "missed_decls": sorted(gold_decls - agent_decls),
        "decl_recall": decl_recall,
        "decl_precision": decl_precision,
        "decl_f1": _f1(decl_precision, decl_recall),
        "decl_scorable_files": len(gold_existing & scorable),
        "decl_unscorable_files": len(gold_existing - scorable),
    }


# --------------------------------------------------------------------------------------------
# Navigation cost, from the agent's stream-json transcript.
# --------------------------------------------------------------------------------------------

_EDIT_CMD = re.compile(
    r"sed\s+-i|perl\s+-[a-z]*i|\btee\b|(?<![0-9&>])>{1,2}\s*(?!&|/dev/null)[\w./-]|open\([^)]*['\"][wa]\+?['\"]|\.write\(|write_text|"
    r"\barac\s+(edit|write)\b|git\s+(apply|mv|rm)\b|\bmv\s|\brm\s|gofmt\s+-w|goimports\s+-w|patch\s+-p|"
    r"python3?\s+-\s*<<", re.I)


def _mentions(cmd: str, path: str) -> bool:
    if path in cmd:
        return True
    # A command run from a subdirectory names the file by its tail: `cd api/v1 && sed -i … routes.go`.
    base = path.rsplit("/", 1)[-1]
    parent = path.rsplit("/", 2)[-2] if path.count("/") >= 1 else ""
    return bool(re.search(r"(^|[\s/'\"=(])" + re.escape(base) + r"($|[\s'\")|;&:])", cmd)) and \
        (not parent or parent in cmd or base.count(".") and len(base) > 8)


def navigation(transcript_path: Path | None, gold_files: list[str]) -> dict:
    """Turns and tokens spent before the first edit of a gold file, and which gold files the
    agent's commands named before that edit.

    APPROXIMATE, and said so in the name of every field: a file can be found through a grep
    whose output names it without the command ever doing so. The edit side is sharper -- an edit
    has to name the file it writes -- so `turns_to_first_gold_edit` is the headline number.
    """
    out = {"nav_api_calls": None, "nav_turns_to_first_gold_touch": None, "nav_tokens_to_first_gold_touch": None,
           "nav_turns_to_first_gold_edit": None,
           "nav_tokens_to_first_gold_edit": None, "nav_gold_files_named_before_edit": None,
           "nav_gold_files_named": None}
    if not transcript_path or not Path(transcript_path).exists() or not gold_files:
        return out
    seen_msgs: set[str] = set()
    calls = 0
    tokens = 0
    named: set[str] = set()
    named_before: set[str] | None = None
    for line in Path(transcript_path).read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("type") != "assistant":
            continue
        msg = ev.get("message") or {}
        mid = msg.get("id")
        if mid and mid not in seen_msgs:
            seen_msgs.add(mid)
            calls += 1
            u = msg.get("usage") or {}
            tokens += sum(int(u.get(k) or 0) for k in ("input_tokens", "output_tokens",
                                                       "cache_read_input_tokens", "cache_creation_input_tokens"))
        for block in msg.get("content") or []:
            if not isinstance(block, dict) or block.get("type") != "tool_use":
                continue
            cmd = json.dumps(block.get("input") or {})
            cmd = (block.get("input") or {}).get("command") or cmd
            hits = {p for p in gold_files if _mentions(cmd, p)}
            # FOUND: the first command that names a gold file at all -- a read, a grep scoped to
            # it, or an edit. The earliest moment the agent demonstrably knows where the change goes.
            if hits and out["nav_turns_to_first_gold_touch"] is None:
                out["nav_turns_to_first_gold_touch"] = calls
                out["nav_tokens_to_first_gold_touch"] = tokens
            if hits and named_before is None and _EDIT_CMD.search(cmd):
                named_before = set(named)
                out["nav_turns_to_first_gold_edit"] = calls
                out["nav_tokens_to_first_gold_edit"] = tokens
            named |= hits
    out["nav_api_calls"] = calls
    out["nav_gold_files_named"] = _ratio(len(named), len(gold_files))
    if named_before is not None:
        out["nav_gold_files_named_before_edit"] = _ratio(len(named_before), len(gold_files))
    return out


# --------------------------------------------------------------------------------------------
# One call per graded row.
# --------------------------------------------------------------------------------------------

def gold_patch_text(task_dir: Path, include_tests: bool = False) -> str:
    """The task's gold patch; with include_tests, followed by its test patch."""
    parts = [task_dir / "solution" / "gold.patch"]
    if include_tests:
        parts.append(task_dir / "tests" / "test_patch.diff")
    texts = [p.read_text(errors="replace") for p in parts if p.exists()]
    return "\n".join(t if t.endswith("\n") else t + "\n" for t in texts)


ROW_FIELDS = ("file_recall", "file_precision", "file_f1", "decl_recall", "decl_precision", "decl_f1")


def score_row(task_raw: dict, agent_patch: str, snapshot_db: Path | None,
              transcript_path: Path | None, include_tests: bool = False) -> dict:
    """Everything localization knows about one run: the full detail, and flat `loc_*` fields for
    the aggregate tables.

    include_tests: the task's tests were the agent's to update, so the gold is the source patch plus
    tests/test_patch.diff and test files are scored like any other file."""
    task_dir = Path(task_raw.get("task_dir") or "")
    gold_patch = gold_patch_text(task_dir, include_tests)
    test_files: set[str] = set()
    tf = task_dir / "tests" / "test_files.json"
    if tf.exists():
        try:
            for item in json.loads(tf.read_text()).get("files", []):
                name = item.get("name") if isinstance(item, dict) else item
                if name:
                    test_files.add(name)
        except (json.JSONDecodeError, AttributeError):
            pass
    detail = score(gold_patch, agent_patch, BaseIndex(snapshot_db), test_files, include_tests)
    detail["tests_in_scope"] = include_tests
    detail.update(navigation(transcript_path, detail["gold_files"]))
    flat = {f"loc_{k}": detail[k] for k in ROW_FIELDS}
    flat.update({k: detail[k] for k in detail if k.startswith("nav_")})
    return {"detail": detail, "row": flat}

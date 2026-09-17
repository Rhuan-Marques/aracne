"""Strip the interface specification SWE-Atlas appends to a task's instruction.

WHY. Most SWE-Atlas refactor tasks end with a block like

    Use the below Interface:

    - Path: `apps/plugins/pkg/apis/plugins/v0alpha1/meta_object_gen.go`
    - Name: `NewMeta`
    - Type: function
    - Output: `*Meta`
    - Description: Creates a new Meta resource object ...

--- one entry for every declaration the reference solution adds, renames or re-signs, with its
file. That is the navigation problem, solved and handed over: where the code lives, what to call
it, what shape it has. A benchmark measuring whether a code-navigation tool helps an agent find
and change the right code cannot hand the agent the list of right code first -- the control arm
gets the answer to the question the tool exists to answer, and the comparison measures nothing.

The issue prose above the block stays. It is the task: what is wrong and what to change, in the
words a real issue would use. Only the appended specification goes.

THE SHAPES, all measured across the 284 instruction files of the rf, qa and tw splits:

  1. A lead-in line -- "Use the below Interface:", "Use the below interface for your solution:",
     "Use the interface below:" -- followed by entries to the end of the file. `---` lines
     SEPARATE entries there; they do not end the block.
  2. A heading -- "# Interface", "# Public Interfaces", "# Public Interface Specifications" --
     followed by `##` sub-sections and `---` separators, ending at the "I've already taken care
     of all changes to the test files" sentence, which is kept.
  3. No lead-in at all: numbered headings ("## 1. useIsPluginAvailableOnAllPlans") whose entries
     are `**Path:**` / `**File:**` / `**Signature:**` bullets.

A block runs from its start to that test-files sentence when one follows, otherwise to the end.
The `---` fence directly around it goes too.
"""
from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path

# The sentence every task uses to say the tests are already written. It is instruction, not
# specification, and it is the one thing that can follow an interface block.
_TESTS_LINE = re.compile(r"^\s*I['’]ve already taken care of all changes to the test files", re.I)

_LEAD_IN = re.compile(
    r"^\s*(?:\\?#{1,4}\s*)?use the (?:below )?interface(?: below)?(?: for your solution)?\s*:?\s*$", re.I)
_HEADING = re.compile(
    r"^\s*\\?#{1,4}\s*(?:public\s+)?interfaces?(?:\s+specifications?)?\s*:?\s*$", re.I)
_NUMBERED_HEADING = re.compile(r"^\s*\\?#{1,4}\s*\d+\.\s+\S")
_FIELD = re.compile(
    r"^\s*\\?[-*]\s*(?:\*\*)?(?:path|file|name|signature|export|type|input|output)(?:\*\*)?\s*:", re.I)
_RULE = re.compile(r"^\s*-{3,}\s*$")


def _starts_numbered_block(lines: list[str], i: int) -> bool:
    """A numbered heading whose next few non-blank lines are interface fields."""
    if not _NUMBERED_HEADING.match(lines[i]):
        return False
    seen = 0
    for ln in lines[i + 1:]:
        if not ln.strip():
            continue
        if _FIELD.match(ln):
            return True
        seen += 1
        if seen >= 2:
            return False
    return False


def strip_interface(text: str) -> tuple[str, int]:
    """Return (instruction without its interface specification, characters removed).

    Idempotent: a text with no block comes back unchanged with 0.
    """
    if not text:
        return text, 0
    lines = text.split("\n")
    start = next((i for i, ln in enumerate(lines)
                  if _LEAD_IN.match(ln) or _HEADING.match(ln) or _starts_numbered_block(lines, i)), None)
    if start is None:
        return text, 0
    end = next((j for j in range(start + 1, len(lines)) if _TESTS_LINE.match(lines[j])), len(lines))

    head = lines[:start]
    while head and (not head[-1].strip() or _RULE.match(head[-1])):
        head.pop()
    tail = lines[end:]

    kept = "\n".join(head).rstrip()
    if tail:
        kept = (kept + "\n\n" if kept else "") + "\n".join(tail).strip()
    kept += "\n"
    return kept, max(0, len(text) - len(kept))


# "I've already taken care of all changes to the test files." -- true upstream, where the task
# container carries the updated tests, and FALSE in this harness: the workspace an agent works in
# holds the tests at the base commit, and the updated ones exist only inside the grading
# container. Measured on k6-10d4b451: the agent's second message was "Let me look at the tests to
# infer the expected API", and it spent its next calls reading tests that could not contain it.
# The issue text now says nothing about tests: the harness template (claude_driver.ATLAS_PROMPT_TEMPLATE)
# states the one rule, once.
_TEST_CLAIM = re.compile(r"I(?:['’]ve| have) already taken care of all changes to the test files\.\s*", re.I)
# The instruction that follows the claim. The harness template states the one rule that matters (do
# not modify test files) once, so the issue text carries nothing about tests at all.
_TESTS_INSTRUCTION = re.compile(
    r"(?:Do not modify any test files(?: \(\\?\*\\?_test\.go\))?(?: or testing logic)? in any way[.!]?\s*)|"
    r"(?:Your task is to make the minimal changes to (?:non[- ]tests? )?(?:source )?(?:files )?(?:in the working "
    r"directory )?(?:to ensure the task is satisfied|only)\.?\s*)", re.I)
_THIS_MEANS = re.compile(r"This means you DON['’]T have to modify the testing logic or any of the tests in any way!",
                         re.I)


def neutralize_test_claim(text: str) -> tuple[str, bool]:
    """Drop everything the issue text says about tests: the claim that they are already updated (false
    in this workspace) and the instructions around it, which the harness template states once."""
    out = _TEST_CLAIM.sub("", text)
    out = _THIS_MEANS.sub("", out)
    out = _TESTS_INSTRUCTION.sub("", out)
    out = re.sub(r"(?im)^\s*[-*]\s*Do not modify any test files\.?\s*$\n?", "", out)
    out = re.sub(r"\n{3,}", "\n\n", out).strip() + "\n"
    return out, out != text


def strip_and_neutralize(text: str) -> str:
    """The deterministic half of `sanitize`: interface block, then the tests claim."""
    stripped, _ = strip_interface(text)
    return neutralize_test_claim(stripped)[0]


# -------------------------------------------------------------------------------------------
# File references. A prompt that names `api/v1/routes.go` has done the navigating for the agent.
# They are removed by a one-time model rewrite (atlas_rewrite_prompts.py) stored in REWRITES_PATH,
# because deleting them with a regex breaks the sentences they sit in. This module only DETECTS
# them, and refuses a prompt that still has them.
# -------------------------------------------------------------------------------------------

REWRITES_PATH = Path(__file__).resolve().parent.parent / "configs" / "atlas" / "rf_prompts_no_paths.jsonl"

_EXT = (r"(?:go|py|pyi|pyx|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|scala|c|h|cc|cpp|cxx|hpp|hh|m|mm|sql|cue|"
        r"json|jsonc|yaml|yml|toml|md|mdx|txt|sh|bash|proto|mod|sum|cmake|html|htm|css|scss|sass|less|xml|"
        r"gradle|lock|ini|cfg|conf|tmpl|tpl|svelte|vue|rb|php|swift|pb|snap|feature|graphql|gql|ipynb|csv|in)")
_FILENAME = re.compile(r"(?<![\w/.-])[\w.-]*\w\." + _EXT + r"(?![\w])")
_BUILD_FILES = re.compile(r"(?<![\w/.-])(?:CMakeLists\.txt|Makefile|Dockerfile|Jenkinsfile|go\.mod|go\.sum|"
                          r"package\.json|Cargo\.toml|pyproject\.toml|setup\.py|tsconfig\.json)(?![\w])")
_SLASHED = re.compile(r"(?<![\w@:/])/?(?:[\w.-]+/)+[\w.-]*")


def find_paths(text: str) -> list[str]:
    """File and directory references in prose. Measured against the 70 rf prompts; the slash
    forms that are NOT paths there (`GET/POST`, `I/O`, `Rust/C`, `SetMain/RunMain`, `cipher/AKM`)
    and URL routes written from the root (`/api/dashboards`) are let through."""
    out = []
    for m in _SLASHED.finditer(text):
        tok = m.group(0)
        if tok.startswith("/"):
            if _FILENAME.search(tok):
                out.append(tok)
            continue                       # a URL route or absolute URL path: behaviour, not layout
        segs = [s for s in tok.split("/") if s]
        dir_like = all(re.fullmatch(r"[a-z0-9_.-]+", s) for s in segs)
        if tok.endswith("/") or _FILENAME.search(tok) or tok.count("/") >= 2 or (dir_like and len(segs) >= 2):
            out.append(tok)
    for rx in (_FILENAME, _BUILD_FILES):
        for m in rx.finditer(text):
            if not any(m.group(0) in p for p in out):
                out.append(m.group(0))
    return out


_IDENT = re.compile(r"(?<![\w/.-])(?:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)(?![\w/-])")


_SLUG = re.compile(r"`([a-z0-9]+(?:-[a-z0-9]+)+)`")


def file_stems(paths: list[str], text: str = "") -> set[str]:
    """File names written without a directory or extension.

    From the references `find_paths` found: base names (extension dropped) and directory segments
    that are NOT valid identifiers -- `rrdset-slots`, `overview-card`. An identifier-shaped segment
    (`rvmanager`, `engine`) is left out: it is usually a package's own name, which is a symbol, or a
    plain word.

    From `text`: backticked hyphenated names in a sentence that is about FILES, MODULES or
    DIRECTORIES -- netdata-ed98813b lists `rrd-database-mode` and five more as "extracted ... into
    their own files", with no path or extension near them. The same shape names things that are not
    files, and those must stay: a library the task says to adopt (`react-hook-form`), a feature flag
    (`marketplace-redesign`). So a sentence that says flag, package, library or dependency wins."""
    out = set()
    for p in paths:
        for seg in [s for s in p.split("/") if s]:
            stem = re.sub(r"\.[A-Za-z0-9]+$", "", seg)
            if len(stem) >= 4 and not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", stem):
                out.add(stem)
    for sentence in re.split(r"(?<=[.!?])\s+|\n\s*\n", text):
        if not re.search(r"\b(files?|modules?|director(?:y|ies)|folders?)\b", sentence, re.I):
            continue
        if re.search(r"\b(flags?|packages?|librar(?:y|ies)|dependenc(?:y|ies)|npm)\b", sentence, re.I):
            continue
        out |= {m.group(1) for m in _SLUG.finditer(sentence) if len(m.group(1)) >= 4}
    return out


def identifiers(text: str) -> set[str]:
    """Code identifiers a rewrite must keep: backticked tokens and camel/snake/dotted names in
    prose, minus anything that is part of a file or directory reference."""
    paths = find_paths(text)
    stems = {s.lower() for s in file_stems(paths, text)}
    found: set[str] = set()
    for tick in re.findall(r"`([^`\n]+)`", text):
        # A file name without its extension is still a file, not a symbol.
        if tick.strip().lower() in stems:
            continue
        if not any(tick in p or p in tick for p in paths) and not _FILENAME.search(tick):
            found.add(tick.strip())
    for m in _IDENT.finditer(text):
        w = m.group(0)
        if any(w in p for p in paths):
            continue
        if re.search(r"[a-z][A-Z]|_[a-zA-Z]|[A-Za-z]\.[A-Za-z]", w) and not _FILENAME.fullmatch(w):
            found.add(w)
    return {f for f in found if f and not find_paths(f)}


def rewrites() -> dict[str, dict]:
    if not REWRITES_PATH.exists():
        return {}
    return {r["key"]: r for r in map(json.loads, REWRITES_PATH.read_text().splitlines()) if r}


class PromptNotRewritten(RuntimeError):
    pass


def sanitize(text: str, key: str | None = None) -> tuple[str, dict]:
    """Everything the navigation variant changes in a task prompt, in the one order that works:
    the interface block (its end is found by the test-files sentence), the tests claim, then file
    references -- replaced by the stored rewrite for `key`.

    RAISES when file references remain and no matching rewrite exists: a prompt that names files
    must never reach an agent in this variant, and a silent fallback to it is how it would.
    """
    stripped, removed = strip_interface(text)
    neutral, rewrote = neutralize_test_claim(stripped)
    changes = {"interface_removed_chars": removed, "test_claim_removed": rewrote, "paths_removed": []}
    paths = find_paths(neutral)
    if not paths:
        return neutral, changes
    rec = rewrites().get(key or "")
    sha = hashlib.sha256(neutral.encode()).hexdigest()
    if not rec or rec.get("source_sha256") != sha:
        raise PromptNotRewritten(
            f"SWE-Atlas task {key!r} names files ({', '.join(sorted(set(paths))[:5])}) and has no "
            f"{'up-to-date ' if rec else ''}path-free rewrite in {REWRITES_PATH}. "
            "Run: .venv/bin/python bench/atlas_rewrite_prompts.py")
    changes["paths_removed"] = rec.get("removed", sorted(set(paths)))
    return rec["prompt"], changes

"""Per-run tool telemetry, folded out of a Claude Code `stream-json` transcript.

WHY THIS EXISTS. The `scale40` run reported a +14% context-token regression for the
aracne arm and could not say *why*, because `--output-format json` returns one summary
object and nothing about tool calls. Every diagnosis had to be reconstructed by hand from
the engine. These counters make the next run self-explaining: they say whether the agent
used the aracne MCP tools at all, how many bytes each tool returned, and how often a
lookup missed or came back ambiguous.

The transcript is NDJSON, one event per line. The events we care about:

  {"type":"assistant","message":{"content":[{"type":"tool_use","id":…,"name":…,"input":…}]}}
  {"type":"user",     "message":{"content":[{"type":"tool_result","tool_use_id":…,
                                             "content":…,"is_error":…}]}}
  {"type":"result", …,"permission_denials":[…]}          <- terminal summary

`summarize` returns (stats, reduced) where `reduced` is the same NDJSON with tool_result
BODIES replaced by their byte count. We only ever need the sizes, and a full 80-turn
transcript on a large repo is tens of MB of duplicated source text.
"""
from __future__ import annotations

import json
import re

# Substrings that identify an aracne resolution failure in a tool result. These are the
# exact strings the engine emits — see internal/llm/languages/universaltools/universal_read.go
# and the per-language read_*.go tools.
_MISS_MARKERS = ("not found in topology", "does not exist")
_AMBIGUOUS_MARKER = "Multiple resources matching"
_GUARD_MARKER = "Blocked by aracne config"
# How an intercepted shell read is recognized in a transcript.
#
# MEASURED, NOT ASSUMED. A live Claude Code session (2.1.257) shows that a PreToolUse hook
# returning `updatedInput` rewrites what the tool RUNS but not what the transcript RECORDS: the
# tool_use block still carries the command the model typed. So the command form below almost
# never fires in practice and is kept only for the case where a model runs `arac cmd` itself.
# Everything real is caught on the RESULT.
#
# The result signatures, in the order they turned out to be needed:
#   - the fenced group header (```<path>) that opens every enriched read. This is the reliable
#     one: the same smoke run returned an answer with NO context block at all, because the
#     resource it framed had no documented neighbours -- so keying on "# CONTEXT:" alone would
#     have counted zero interceptions on a run that intercepted every read.
#   - the context block, for the answers that carry one.
#   - the elision marker, present whenever a window is framed by its enclosing declaration.
#   - topogrep's per-resource annotation header, which is what an intercepted SEARCH returns
#     instead of a fence.
_INTERCEPT_RE = re.compile(r"\barac\s+cmd\s+--")
# Discovery commands run through the shell, matched on the command WORD so the classification
# holds whether or not the guard rewrote the call. `arac cmd --` is skipped over first, since
# what follows it is the command the model actually asked for.
_SHELL_PREFIX_RE = re.compile(r"^\s*(?:\S*\barac\s+cmd\s+--\s+)?(?:[\w./\\-]*/)?([\w.-]+)")
_SHELL_READ_WORDS = frozenset((
    "cat", "head", "tail", "less", "more", "bat", "nl", "tac",
    "strings", "xxd", "od", "hexdump", "sed", "awk", "gawk", "mawk",
))
_SHELL_GREP_WORDS = frozenset(("grep", "egrep", "fgrep", "zgrep", "rg", "ack", "ag", "ug"))
_ENRICHED_RES = (
    re.compile(r"\A```[^\s`]"),      # ```path/to/file.go
    re.compile(r"\n# CONTEXT:"),
    re.compile(r"for its source ⋯"),
    re.compile(r"(?m)^# \S+ — "),     # topogrep: "# pkg.Fn — description"
)


def _looks_enriched(text: str) -> bool:
    """Did this tool result come from aracne rather than from coreutils?"""
    return any(r.search(text) for r in _ENRICHED_RES)

# A fetch is an ANSWER-KEY fetch when the thing being fetched names the repository under test.
# SWE-bench's answer is a public URL derived mechanically from the instance id, and across
# three `scale40` runs every single network call the agent made was one of these -- PR diffs,
# issue threads, PR-title searches -- and none was documentation or a package registry. So the
# rule is narrow: the web is fair game, the repository holding the graded diff is not.
#
# This counter is the AUDIT half of that rule. bench/bench/netshim.py is the prevention half,
# and it only wraps `curl`/`wget`; anything that reaches the network another way (a hand-rolled
# `python3 -c urllib`, a harness tool making its own request) shows up here instead. A non-zero
# count means the shim leaked, and paired._graded drops that repository from BOTH arms so a
# contaminated pass never counts as a solve for either.
_NET_MARKERS = ("http://", "https://")

# What a BLOCKED answer-key fetch looks like coming back. netshim prints the first; a shell
# reports the second for a binary that is not installed (`gh` is not, on the benchmark host).
# Either way the model learned nothing, which is the whole point of the distinction below.
_BLOCKED_FETCH = ("aracne-bench: refusing to fetch", "No such file or directory",
                  "command not found")

# A fetch that returned NOTHING taught the model nothing, so it is not contamination however
# it failed. This catches the case the marker list cannot: `gh issue view … 2>/dev/null` sends
# its "not installed" error to the void and comes back as a clean, empty result.
#
# The redirect check is the exception to the exception: `curl -o file` legitimately prints
# nothing on success, so an empty result there may still have delivered the diff.
# An fd redirect (`2>/dev/null`) and a discard are not "writing the result somewhere":
# `gh issue view … 2>/dev/null` swallowing its own not-installed error is exactly the case
# this whole rule exists to classify as "returned nothing".
_WRITES_TO_FILE = re.compile(r"(?:^|\s)(?:-o|--output)(?:\s|=)|(?<![\d&])>>?\s*(?!&|/dev/null)[^\s|;&]+")


def _event_result_text(event: dict) -> str:
    """The tool result payload carried on the EVENT rather than the content block.

    Claude Code puts the structured result (for Bash: {stdout, stderr, interrupted, ...}) here,
    while the block's `content` is the rendering the model saw. Only the contamination verdict
    reads this; every byte counter deliberately stays on what the model was shown.
    """
    raw = event.get("tool_use_result")
    if isinstance(raw, list):
        return "".join(p.get("text", "") for p in raw if isinstance(p, dict))
    if isinstance(raw, str):
        return raw
    if raw is None:
        return ""
    try:
        return json.dumps(raw)
    except (TypeError, ValueError):
        return ""


def _fetch_returned_nothing(command, text: str) -> bool:
    if any(m in text for m in _BLOCKED_FETCH):
        return True
    body = text.strip()
    if body in ("", "{}"):
        return True
    try:
        payload = json.loads(body)
    except (ValueError, TypeError):
        return False
    if not isinstance(payload, dict) or "stdout" not in payload:
        return False
    if (payload.get("stdout") or "").strip() or (payload.get("stderr") or "").strip():
        return False
    return not (isinstance(command, str) and _WRITES_TO_FILE.search(command))


# Commands that reach the network. Not forbidden -- a run legitimately installs a dependency --
# but a task solved by fetching its own upstream PR diff is not measuring what the benchmark
# thinks it is. In compact-blocked-20260830c, `sveltejs/svelte` curl'd the PR and `git apply`ed
# it; that single cell carried the entire headline efficiency win for its arm, and the same
# thing happened once in the baseline. Counting it is what lets the analysis exclude it.
# Anchored on a word boundary rather than on a command separator. The separator form
# missed `timeout 60 curl ...`, which is exactly how the agent wrote every fetch it made
# in the scale40 runs -- so the counter read zero while the transcript showed five.
# A stray "curl" inside a quoted string now counts too; for an audit counter that is the
# safe direction, and n_answer_key_fetches additionally requires a URL and the repo name.
# Commands that can pull bytes from outside the worktree.
#
# `pip download` / `npm pack` and friends were MISSING, and the omission cost a real result:
# in linerange-20260902a the seaborn cell ran `pip download seaborn==0.13.2`, unzipped the
# wheel and diffed the released axisgrid.py against the checkout, then patched the region the
# diff pointed at. A later release of the package under test contains the fix, so that is the
# answer key arriving through the package registry instead of through the PR URL -- and the
# cell recorded n_network_calls=0 because this pattern only knew about curl/wget/gh/git.
#
# The package managers are matched only on their FETCHING subcommands: a bare `pip install -r
# requirements.txt` or `npm ci` is ordinary environment setup that nearly every cell performs,
# and counting those as network reach would make the signal meaningless.
_NETWORK_RE = re.compile(
    r"(?<![\w./-])(?:curl|wget|gh)\s"
    r"|git\s+(?:fetch|clone|pull)\b"
    r"|(?<![\w./-])pip3?\s+download\b"
    r"|(?<![\w./-])npm\s+pack\b"
    r"|(?<![\w./-])(?:pip3?\s+install|npm\s+install|yarn\s+add|cargo\s+add)\s+\S+==|"
    r"(?<![\w./-])(?:pip3?\s+install)\s+\S+@"
)

# UPSTREAM_DIFF_RE catches the SHAPE of the contamination above rather than the fetch itself:
# comparing a downloaded copy of the project against the checkout. A diff between an extracted
# package and the worktree is not debugging, it is reading the answer.
UPSTREAM_DIFF_RE = re.compile(r"\bdiff\b[^|;]*\b(?:site-packages|dist-packages|node_modules/\.?[a-z]|/tmp/\w+/(?:ext|pkg|upstream))")

MCP_PREFIX = "mcp__aracne__"


def _result_text(block: dict) -> str:
    """Flatten a tool_result `content` (str, or a list of {type:text,text:…} parts)."""
    content = block.get("content")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for part in content:
            if isinstance(part, dict) and isinstance(part.get("text"), str):
                parts.append(part["text"])
            elif isinstance(part, str):
                parts.append(part)
        return "".join(parts)
    return "" if content is None else json.dumps(content)


def _blocks(event: dict) -> list:
    content = (event.get("message") or {}).get("content")
    return content if isinstance(content, list) else []


def empty_stats() -> dict:
    """The zero row — used for non-Claude backends and for runs with no transcript, so
    every row carries the same keys and a missing transcript never looks like a zero."""
    return {
        "tool_calls": {},
        "tool_result_bytes": {},
        "tool_errors": {},
        "n_tool_calls": 0,
        "n_mcp_calls": 0,
        "n_native_calls": 0,
        "mcp_result_bytes": 0,
        "native_result_bytes": 0,
        "grep_result_bytes": 0,
        "read_result_bytes": 0,
        "n_id_misses": 0,
        "n_ambiguous": 0,
        "n_guard_denials": 0,
        "n_network_calls": 0,
        # Two counters, because "went looking" and "found it" are different facts and only the
        # second invalidates a pair. The first run with netshim in place made the distinction
        # matter immediately: a cell issued two answer-key calls, the shim refused the `curl`
        # and `gh` was not installed, so the model got nothing -- censoring that cell would
        # have thrown away a clean data point for a lookup that provably failed.
        "n_answer_key_attempts": 0,
        "n_answer_key_fetches": 0,
        # The terminal surface delivers aracne through Bash, so every counter keyed on a tool
        # NAME reads it as a plain baseline: n_mcp_calls is 0 by construction and the bytes
        # land in native_result_bytes beside build logs and test output. These two put the
        # surface back on the row, and are what makes "bytes spent on discovery" comparable
        # between an MCP arm, a terminal arm and the control.
        "n_intercepted": 0,
        "terminal_result_bytes": 0,
        # read_result_bytes / grep_result_bytes are keyed on the TOOL NAME, so they see a
        # native `Read` and an `mcp__aracne__read` and nothing else. Both arms of a modern
        # matrix also discover through the shell -- the baseline because it has Bash, the
        # terminal arm because Bash is the only surface it has -- and those bytes landed in no
        # bucket at all. These two count them by command WORD instead.
        #
        # Deliberately SEPARATE fields rather than folded into the originals: prior runs'
        # numbers were computed the old way, and quietly changing what a column means is how a
        # comparison across the change becomes wrong without looking wrong. Total discovery
        # cost is the sum of the pair.
        "shell_read_result_bytes": 0,
        "shell_grep_result_bytes": 0,
        "has_transcript": False,
    }


def _shell_discovery_kind(command) -> str:
    """Classify a shell command as reading or searching, by its command word.

    Only the FIRST command word is examined, so `head -20 f && go build` counts as a read once
    and a pipeline's producer decides. That is deliberately coarse: the number is a
    bytes-spent-on-discovery estimate, not a classifier, and over-thinking a compound command
    would make it agree with nothing.
    """
    if not isinstance(command, str):
        return ""
    m = _SHELL_PREFIX_RE.match(command)
    if not m:
        return ""
    word = m.group(1).lower().removesuffix(".exe")
    if word in _SHELL_READ_WORDS:
        return "read"
    if word in _SHELL_GREP_WORDS:
        return "grep"
    return ""


def _is_answer_key_call(name: str, block: dict, answer_key: str) -> bool:
    """Does this tool call try to reach the repository under test?

    Matched against the call's INPUT rather than its result: the question is what the agent
    asked for, and a refused fetch is as much a contamination signal as a successful one -- it
    says the model went looking for the answer, which is what invalidates the pair.

    Any network call naming the repository counts, with no requirement that a URL appear:
    `gh pr list --repo org/repo` is an answer-key lookup written without one. A LOCAL command
    naming the repository -- grepping the worktree, reading go.mod -- is not a network call and
    so never reaches this test.
    """
    if not answer_key:
        return False
    if name == "WebFetch":
        return answer_key in json.dumps(block.get("input") or {})
    command = (block.get("input") or {}).get("command")
    if not isinstance(command, str) or not _NETWORK_RE.search(command):
        return False
    return answer_key in command


def summarize(stdout: str, answer_key: str = "") -> tuple[dict, str]:
    """Fold a stream-json transcript into counters, and return a size-reduced transcript.

    Never raises: a malformed or truncated stream still yields whatever was parseable, so
    a crashed run does not also lose its telemetry.
    """
    stats = empty_stats()
    if not (stdout or "").strip():
        return stats, ""

    names: dict[str, str] = {}      # tool_use_id -> tool name
    answer_key_pending: set = set()  # tool_use_ids of answer-key ATTEMPTS awaiting a result
    commands: dict = {}             # tool_use_id -> the shell command it ran
    intercepted: set = set()        # tool_use_ids the terminal surface answered
    shell_kind: dict = {}           # tool_use_id -> "read" | "grep", for shell discovery
    reduced: list[str] = []
    saw_event = False

    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        saw_event = True
        etype = event.get("type")

        if etype == "assistant":
            for block in _blocks(event):
                if not isinstance(block, dict) or block.get("type") != "tool_use":
                    continue
                name = block.get("name") or "?"
                names[block.get("id")] = name
                stats["tool_calls"][name] = stats["tool_calls"].get(name, 0) + 1
                stats["n_tool_calls"] += 1
                if name.startswith(MCP_PREFIX):
                    stats["n_mcp_calls"] += 1
                else:
                    stats["n_native_calls"] += 1
                if _is_answer_key_call(name, block, answer_key):
                    stats["n_answer_key_attempts"] += 1
                    answer_key_pending.add(block.get("id"))
                    commands[block.get("id")] = (block.get("input") or {}).get("command")
                if name == "Bash":
                    command = (block.get("input") or {}).get("command", "")
                    if isinstance(command, str) and _NETWORK_RE.search(command):
                        stats["n_network_calls"] += 1
                    if isinstance(command, str) and _INTERCEPT_RE.search(command):
                        intercepted.add(block.get("id"))
                    kind = _shell_discovery_kind(command)
                    if kind:
                        shell_kind[block.get("id")] = kind

        elif etype == "user":
            for block in _blocks(event):
                if not isinstance(block, dict) or block.get("type") != "tool_result":
                    continue
                name = names.get(block.get("tool_use_id"), "?")
                text = _result_text(block)
                nbytes = len(text.encode("utf-8", "replace"))
                stats["tool_result_bytes"][name] = \
                    stats["tool_result_bytes"].get(name, 0) + nbytes
                if name.startswith(MCP_PREFIX):
                    stats["mcp_result_bytes"] += nbytes
                else:
                    stats["native_result_bytes"] += nbytes
                # These two span BOTH arms deliberately — native `Read`/`Grep` are
                # counted alongside `mcp__aracne__read_*`/`grep`, so "bytes spent on
                # discovery" is directly comparable between baseline and aracne.
                bare = name.rsplit("__", 1)[-1].lower()
                if "grep" in bare:
                    stats["grep_result_bytes"] += nbytes
                elif bare.startswith("read"):
                    stats["read_result_bytes"] += nbytes
                if block.get("is_error"):
                    stats["tool_errors"][name] = stats["tool_errors"].get(name, 0) + 1
                if any(m in text for m in _MISS_MARKERS):
                    stats["n_id_misses"] += 1
                if _AMBIGUOUS_MARKER in text:
                    stats["n_ambiguous"] += 1
                if _GUARD_MARKER in text:
                    stats["n_guard_denials"] += 1
                # Counted from the RESULT, because the transcript records the command the
                # model typed rather than the one the hook rewrote it into.
                if _looks_enriched(text):
                    intercepted.add(block.get("tool_use_id"))
                    stats["terminal_result_bytes"] += nbytes
                kind = shell_kind.get(block.get("tool_use_id"))
                if kind == "read":
                    stats["shell_read_result_bytes"] += nbytes
                elif kind == "grep":
                    stats["shell_grep_result_bytes"] += nbytes
                # An attempt only contaminates the cell if it came back with something. A
                # refusal leaves the model exactly where it was.
                tid = block.get("tool_use_id")
                if tid in answer_key_pending:
                    answer_key_pending.discard(tid)
                    # Judge contamination on the RICHER payload. `block["content"]` is what
                    # the model was shown, which is the right basis for the byte counters
                    # above but not for "did anything come back": for a Bash result the
                    # structured {stdout, stderr, ...} lives on the event's `tool_use_result`,
                    # and the block can carry a short rendering that parses as neither empty
                    # nor a stdout payload. That divergence made `gh issue view … 2>/dev/null`
                    # -- an empty result from a binary that is not installed -- score as a
                    # FETCH in fair-20260901a and censor an otherwise clean pair, while the
                    # archival reader (bench/contamination.py) scored the same exchange
                    # correctly. One question, one answer: use the payload both can see.
                    verdict_text = _event_result_text(event) or text
                    if not _fetch_returned_nothing(commands.get(tid), verdict_text):
                        stats["n_answer_key_fetches"] += 1
                # Drop the body: we needed only its size.
                block["content"] = f"<{nbytes} bytes elided by toolstats>"

        elif etype == "result":
            # The CLI reports permission-hook denials directly on the terminal event; it
            # is a superset of the in-band "Blocked by aracne config" text (a PreToolUse
            # deny never reaches the model as a tool_result at all), so prefer it.
            denials = event.get("permission_denials")
            if isinstance(denials, list) and denials:
                stats["n_guard_denials"] = max(stats["n_guard_denials"], len(denials))

        reduced.append(json.dumps(event, separators=(",", ":")))

    stats["n_intercepted"] = len(intercepted)
    stats["has_transcript"] = saw_event
    return stats, "\n".join(reduced)


# The subset of counters that is flat enough to sit on a result row and be aggregated.
ROW_FIELDS = (
    "n_tool_calls", "n_mcp_calls", "n_native_calls",
    "mcp_result_bytes", "native_result_bytes",
    "grep_result_bytes", "read_result_bytes",
    "n_id_misses", "n_ambiguous", "n_guard_denials", "n_network_calls",
    "n_answer_key_attempts", "n_answer_key_fetches",
    "n_intercepted", "terminal_result_bytes",
    "shell_read_result_bytes", "shell_grep_result_bytes",
)


def row_fields(stats: dict) -> dict:
    """Project `stats` onto the flat scalar fields stored in runs.jsonl.

    The per-tool dicts stay out of the row: they are variable-width, they would bloat
    every line, and the transcript on disk still has them.
    """
    return {k: stats.get(k, 0) for k in ROW_FIELDS}

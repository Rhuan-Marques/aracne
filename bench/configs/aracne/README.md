# bench/configs/aracne

A library of **aracne configs** (`.aracne/config.json` variants) to benchmark the aracne arm
under. Select one from a run YAML with the `aracne_config` field, or from the CLI with
`--aracne-config`:

```bash
python bench/run_benchmark.py run --config gosmoke --aracne-config strict-tools
```

`aracne_config` accepts a **bare name** (resolved to `bench/configs/aracne/<name>.json`) or a
**path** to any JSON file. When set, the file is **deep-merged onto** the aracne arm's fixture
`.aracne/config.json` before each aracne run (baseline is untouched), and a copy is saved to
`results/<run_name>/aracne_config.used.json`. When unset, the aracne arm uses the fixture's
default config (unchanged behavior).

## Why deep-merge (not replace)

`arac` rejects a config that lacks a "sentinel" field and silently overwrites it with defaults.
Merging the overlay onto the fixture's already-valid config keeps the sentinels, so the result
stays valid while changing only the keys the overlay names. Each example here includes a sentinel
(`read.max_file_size` or `scan.mode`). Lists (e.g. `mcp_tools`, `blocked_tools`) are replaced
wholesale by the overlay; nested objects are merged.

## What takes effect at run time vs. is frozen at prepare

`arac serve` and the `arac guard` hook read `.aracne/config.json` **live** against the pre-built
`topology.db`, so these overlay keys change the aracne arm's behavior for the run:

- `llm.claude_code.main_agent.mcp_tools` — which MCP tools the agent is given
- `llm.claude_code.main_agent.blocked_tools` — which native tools the guard blocks
  (only `read, grep, edit, write, bash` are valid here)
- `read.context_filter.*` — verbosity of the `# CONTEXT:` block
- `read.max_file_size`, `read.pipe_passthrough`
- `scan.pre_tool` — the scan the guard runs before each tool call. Note the split: `scan.mode`
  is frozen into `topology.db` at prepare time, but `pre_tool` is read live by the hook, so an
  overlay does change it
- `terminal.*` — whether the guard rewrites shell reads/searches, and the over-serve ceiling
- `integration.mode` — which surface the arm runs on. This one takes effect through
  `fixtures.sync_agent_contract`, which re-runs `arac init --claude -y` after the overlay is
  applied: that is what writes (or removes) `.mcp.json` and regenerates CLAUDE.md for the mode.
  Without that step an overlay could set the mode and still ship the other surface's contract.

**Frozen at prepare (no effect via a run-time overlay):** `scan.mode`, `scanner.*`, the *content*
of generated descriptions (`descriptions.kinds`), and `paths` — these are baked into the topology
DB when fixtures are prepared/frozen. To benchmark those, re-prepare fixtures with the config.

Tool names are validated against the toolspec catalog when `arac serve`/`arac init` load the
config, so a typo makes that arm's runs error out (surfaced in the results).

## Performance configs (maximize task success, not token cost)

Three **complete** configs tuned to give the model the richest useful context and fullest capability.
Meant to be A/B'd against `baseline` and each other:

```bash
python bench/run_benchmark.py run --config gosmoke --aracne-config perf-balanced
```

| config | context | MCP tools | native tools | intent |
|--------|---------|-----------|--------------|--------|
| `perf-max.json` | everything `full` — incoming callers, full external-var source, all small fns inlined (`threshold 100000`), undocumented neighbors shown | all 19 | allowed | literal "as full as possible" |
| `perf-balanced.json` **(recommended)** | incoming callers, small fns inlined (`threshold 40`), undocumented shown, but external vars `normal` (avoids token-wall dilution) | 12-tool code core | native `read/edit/write` **blocked**; `grep` allowed | maximum useful recall without diluting/truncating the signal |
| `perf-mcp-only.json` | same as `perf-max` | 12-tool code core | native `read/grep/edit/write` **blocked** | force topology-guided navigation (bash + piped reads still work) |
| `terminal-first.json` | same as `perf-guarded` | **none** | none blocked | the terminal surface: no MCP server at all, and the guard *rewrites* `cat`/`head`/`tail`/`sed -n`/`grep` on indexed targets into `arac cmd`, which answers them from the topology |

### `terminal-first.json`

The one config where `mcp_tools` is empty on purpose. `integration.mode: "terminal"` means
`arac init` writes no `.mcp.json` at all, so there is no server to serve tools from; the model
gets the short terminal contract instead of the MCP one and reaches the topology through the
shell commands it already runs. `blocked_tools` is empty for the same reason: a denial would
name an MCP tool that is not served, and interception has already handled everything aracne can
answer.

Pair it with `arm_builtin_tools: {<arm>: [Bash, Glob, TodoWrite]}` — with no MCP tools in the
list there is nothing for Claude Code to defer behind a `ToolSearch`, which was the original
reason for trimming the built-in surface.

Its `read.*` values are copied verbatim from `perf-guarded.json` so a paired run against that
config isolates the delivery mechanism and nothing else.

### `terminal-noprefer.json`

`terminal-first.json` with one key flipped: `terminal.prefer_resource_ids: false`. That removes
exactly one sentence from the generated contract -- the line telling the model to prefer a
resource ID over a path -- and changes nothing else. Diffing the two JSON files should show a
single key, and diffing the two generated CLAUDE.md files a single line (2,212 vs 2,030 bytes).

It exists for `bench/configs/run/ab-prefer-ids.yaml`, which runs both as arms. The sentence is
worth measuring rather than arguing about: a resource read costs 3,159 bytes against 1,663 for a
40-line window on the clap fixture, so on bytes it steers toward the more expensive shape --
but 66% of the control arm's reads in fair-20260901a followed a search within three calls and
19% re-read a file already read, and a resource read has neither cost.

**Why these values** (all read live at run time):
- `read.context_filter`: `include_incoming: true` adds a `# USED BY:` section; `small_functions_visibility: "full"` inlines small neighbor bodies; a high `small_function_threshold` makes more neighbors qualify; `hide_no_description: false` keeps undocumented neighbors. Never use `"hidden"` — it *drops* information. The requested resource's own code is always rendered first and is never truncated; only the trailing context can be cut by the harness, which is why `perf-balanced` keeps external vars `normal` (their full source is a large "token wall" that dilutes the more valuable relationship info).
- `scan.pre_tool` — `"default"` (the shipped default) re-indexes before every tool call the guard sees, so freshness is paid for per call on the measured path. `perf-balanced` sets `"none"` instead and lets the harness run `arac scanner run` beside the aracne arm (`bench/arms.py:background_scanner`), which polls `scanner.update_frequency` ms and re-scans only what changed. Runs record `bg_scanner` in `runs.jsonl`; **if that is false, `scan.pre_tool: "none"` means nothing is keeping the topology fresh** — treat the run as void. **Avoid `"hard"`** (it *wipes descriptions*, the payload of every context line) and **`"full"`** (re-scans the whole repo on every call → per-tool timeout risk on large repos).
- The watcher also covers what `blocked_tools` cannot: the guard classifies a shell command by its command WORD, so `sed -i` is an edit but `python3 - <<EOF` writing a file is just `python3`. Heredoc patching is common, and only the scanner catches it.
- `read.max_file_size: 33554432` (32 MiB) — files over the limit are *rejected*, not truncated, so this effectively removes the cap for any real source file.
- `mcp_tools` set explicitly on `llm.claude_code.main_agent` (an empty list would serve zero tools). `perf-mcp-only` blocks native code tools but leaves `bash` and `pipe_passthrough: true` so the agent can still build/test/git and inspect piped output — only reads/greps/edits are forced through aracne.

**Caveats:** these assume the well-described fixtures the bench produces — description *content*,
`scan.mode`, and `paths` are frozen into `topology.db` at prepare time and a run overlay can't
change them (re-prepare fixtures to benchmark those). Knobs you can push further if your repos are
small: `scan.pre_tool: "full"` (more accuracy, slower) and `external_vars_visibility: "full"` in
`perf-balanced` (more detail, dilution risk).

## Smaller examples

- `verbose-context.json` — a partial overlay: verbose `# CONTEXT:` (incoming edges, full external
  vars) but hides small functions and drops undocumented (a narrower profile than the perf configs).
- `strict-tools.json` — a partial overlay: a trimmed MCP tool set, native `Read/Grep/Edit/Write`
  blocked by the guard, and piped reads gated too.

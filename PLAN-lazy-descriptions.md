# `descriptions.lazy` — descriptions generated on the read path

## The problem

A description is only useful once it exists, and today the only way to make one exist is
`arac descriptions generate`: a whole-repo sweep that describes every undocumented target in
one go. On a large repo that is a long, expensive, all-or-nothing run before the first read
returns anything better than a bare id — and `read.context_filter.hide_no_description`
defaults to **true**, so until the sweep finishes, an undescribed neighbour is not merely
undescribed in the `# CONTEXT:` block, it is *absent from it*.

That is the wrong shape for adoption. Most of a large repo is never read, and the descriptions
that matter are the ones attached to whatever the model happens to be looking at right now.

## The feature

`descriptions.lazy` (default **true**). When a surface is about to return content that would
carry descriptions, and some of the nodes that content will name have none, aracne generates
those descriptions first — in the background, with the configured description-generation
model — waits for them to land in the database, and then renders the answer from the refreshed
topology.

Reads get slower on a cold repo and converge on the old speed as the repo warms up. That is
the trade: on-the-fly instead of one big sweep.

## Surfaces covered

| Surface | Entry point | Seeds |
|---|---|---|
| MCP `read` / `read_resource` | `universaltools.Read.ReadIDs` | resolved units' neighbourhood |
| `arac read` | same | same |
| `arac cmd -- cat/head/tail/sed` (whole file or resource) | same | same |
| `arac cmd -- head -20 f.go` (window) | `universaltools.Read.ReadSlice` | the covering declarations' neighbourhood |
| MCP `grep` | `tools.Grep.Run` | the matched nodes themselves |
| `arac grep` | `cli.RunGrep` | same |
| `arac cmd -- grep …` | `cli.serveGrep` | same |

## What counts as "a description that will be shown"

**Reads.** The body of a read is code, so the resource the caller *asked for* does not show its
own description — its neighbours do. The seed set is therefore the one-hop neighbourhood of the
resolved units:

* outgoing edges (`calls`, `uses_*`) — the `# CONTEXT:` entries;
* containment edges (`has_*`, `methods`, `constructor`) — what a file or package read lists;
* incoming edges (the reverse of both) — the `# USED BY:` / `## Used By` / `## Implemented By`
  sections.

A neighbour is a target only if `helper.ShouldDescribe` says so: no description yet, a kind in
`descriptions.kinds`, and a read-context visibility of Normal (a `full` neighbour shows code,
a `hidden` one shows nothing — neither needs prose).

**Grep.** A description that does not exist cannot match a pattern, so lazy generation cannot
widen the *description* tier of a search. What it can do is describe the nodes the search
already found by **name** or by **content**, because those are exactly the nodes whose
`# <id> — <description>` header the result is about to print. Seeds are therefore the matched
resources themselves, and only those whose header will actually be rendered.

## Design

### New package `internal/lazydesc`

```
Filler ─ plan.go      which nodes need a description for this answer
       ─ generator.go how one batch of them becomes text in the database
       ─ filler.go    dedupe, cap, timeout, write, report "something changed"
```

`Filler` is nil-safe: `(*Filler)(nil).FillForRead(...)` returns false, which is what every
caller gets when the feature is off. A disabled feature must cost nothing but a nil check.

### The generator is tool-less, on purpose

`arac descriptions generate` runs a full sub-agent per batch: the executor reads each resource
and calls `update_description` itself. Reusing that here would drag `internal/llm/tools` into
`internal/lazydesc`, and `internal/llm/tools/grep.go` is one of the call sites — an import
cycle. It would also be slow: an agent loop is several round trips where the read path can
afford one.

So the lazy generator does a **single completion per batch**, with the source already cut and
inlined (`mgr.Cut`, exactly what `makeResourceReader` does today), and parses back
`<id> :: <description>` lines. Descriptions are validated with `domain.ValidateDescription` and
written through `TopologyManager.UpdateDescription`, so the budget and the unknown-id check are
the same ones the tool path enforces. House style, per-kind budgets and exemplars come from
`internal/prompts`, shared with the sweep.

### Model / effort

`descriptions.lazy.model` wins; otherwise the model configured for the
`descriptions-generation-executor` agent (`llm.<harness>.agents`, which defaults to `haiku`);
otherwise the first provider with an API key in the environment. Bare aliases (`haiku`,
`sonnet`, `opus`, `fable`, `gpt-5.5`, `deepseek-v4-flash`) map to a provider and a full model
id. **No API key ⇒ no generator ⇒ the read renders exactly as it does with the feature off.**

## Failure modes mapped out in advance

| Risk | Mitigation |
|---|---|
| **A read can never fail because of this.** | Every step is best-effort. No generator, no API key, a provider error, a malformed reply, a rejected write, a timeout — all return "nothing changed" and the answer renders from the topology as it stands. |
| **Unbounded fan-out.** A whole-file or package read can name hundreds of undescribed nodes. | `max_nodes` (default 24, ≈ `renderstate.DefaultMaxEntries`) caps one fill, with a deterministic priority: direct neighbours before incoming ones, then by id. The rest stay undescribed and are picked up by the next read that names them. |
| **Unbounded latency.** | `timeout_seconds` (default 120) bounds the whole fill. On expiry whatever landed is used. Batches run in parallel (`parallel`, default 4) in batches of `batch_size` (default 5). |
| **Re-attempting the same hopeless node on every read.** A node the model refuses to describe would otherwise be retried forever, once per read. | An in-process attempted-set: an id tried in this process is not tried again, success or failure. Process-scoped on purpose — `arac` is mostly one-shot, and a fresh process is a fresh chance. |
| **Two concurrent reads describing the same node.** | Targets are *claimed* under a mutex that is **not** held across the generation — a lock spanning a minute-long fill would block a read that needs nothing, turning a feature that makes one read slower into one that makes every read slower. Two overlapping reads claim each node exactly once; the loser renders without it, which is the output it would have had with the feature off one read earlier. Cross-process races are harmless: `UpdateDescription` is a single UPDATE and the loser overwrites with an equally valid description. |
| **The description sweep triggering itself.** The `descriptions-generation-executor` reads each resource in order to describe it; with a filler on that read, a description run would spawn a second, uninvited run over the neighbours of everything it was assigned — on a second model, racing the first for the writes. | `lazyFillerFor` returns nil for that agent, so its tool profile is built without one. Every other agent gets a filler, and one filler is shared by a profile's `read` and `grep` so they share an attempted-set and a lock. |
| **`arac cmd` over-serve budget.** A fill makes an intercepted read's answer BIGGER (context entries appear that were hidden), which can push it past `terminal.max_overserve` and make aracne pass through to the real command instead. | Correct as it stands, and not wasted: the guard exists to make exactly that call, and the descriptions are in the database for the next read that can afford them. |
| **Infinite recursion.** The generator reads source to describe a node. | It does not go through the read tool: it cuts source directly with `mgr.Cut`, so no read path is re-entered and no fill can trigger a fill. |
| **A grep that describes nodes whose header is then dropped.** `FormatResult` drops annotation entirely when headers grow past `AnnotateOverheadBudget` of the matched content — and descriptions make headers longer. | `topogrep.LazyTargets` picks targets *under a projection of that same budget*: node rows (always annotated) always qualify; line-match resources qualify only while the projected header bytes keep annotation alive. |
| **Descriptions written to a stale topology.** The units were built from a snapshot taken before the fill. | After a fill the topology is re-read and the units are rebuilt against a fresh `renderstate`. Rebuilding is cheap next to the LLM call that just happened, and it is the only way the render can see the new prose. |
| **Silent behaviour change for existing projects.** | The default is on, as specified, but every fill is a no-op unless a described-nothing node is about to be shown *and* a provider key is present. A project with no key, or a warm repo, is byte-identical. |
| **Tests must not call an LLM.** | `lazydesc.GeneratorFactory` is a package-level hook; tests swap in a deterministic fake. The production factory is the only thing behind it. |
| **`--regen_oversized` interaction.** | Out of scope: lazy fills only *missing* descriptions, never rewrites an existing one. |

## Config surface

```jsonc
"descriptions": {
  "kinds": [...],
  "lazy": true            // or false, or the object form:
  "lazy": {
    "enabled": true,
    "max_nodes": 24,
    "timeout_seconds": 120,
    "batch_size": 5,
    "parallel": 4,
    "model": "haiku",     // optional override
    "provider": "anthropic",
    "base_url": ""        // a gateway or self-hosted endpoint
  }
}
```

The bool and the object are the same key: `true` is shorthand for "enabled, everything else
default", and that is how it round-trips back out.

## Tests

* `internal/helper` — default is on; `false` is respected; both JSON forms parse and
  round-trip; an unknown provider is a validation error.
* `internal/lazydesc` — target planning (neighbours in, described nodes out, non-target kinds
  out, hidden-visibility nodes out, cap and priority honoured); the fill writes to the
  database; the attempted-set stops a retry; a failing generator changes nothing; a disabled
  filler never calls the generator.
* `internal/topogrep` — `LazyTargets` returns node rows, respects the annotation projection,
  and `ApplyDescriptions` puts new prose into a rendered header.
* `internal/lazydesc` (HTTP) — the production generator against an `httptest` server: the
  request carries the ids and the source and no tool definitions, the SSE reply parses, and a
  transport error surfaces as an error rather than a panic.
* `internal/cli` — the description executor's tool profile is built without a filler.
* `tests/lazy_descriptions_test.go` — end-to-end over a real scanned Go project: with lazy
  **off**, an undescribed neighbour is absent from `# CONTEXT:` and a grep header carries no
  description; with lazy **on**, the same read, the same window and the same grep come back
  with generated prose, the descriptions are in the database afterwards, and a second read on
  the warm repo generates nothing and returns byte-identical output. One test runs the whole
  chain with nothing faked but the model — the production factory, a real HTTP round trip, the
  real parser and a real database write.

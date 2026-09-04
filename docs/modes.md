# The four modes

`mode` in `.aracne/config.json` is **the one dial**. It answers three questions at once —
which tools exist, which shell commands aracne answers, and what vocabulary the generated
contract teaches — and every artifact `arac init` writes derives from it.

| mode | MCP tools | shell reads | shell grep | `blocked_tools` | addressing |
|------|-----------|-------------|------------|-----------------|------------|
| `mcp` | one `read` (+ `warnings_list`, `bug_*`) | run as themselves | intercepted | **active** | resource IDs |
| `cli` *(default)* | none | run as themselves | intercepted | **active** | resource IDs, via `arac read` |
| `intercept_id` | none | **intercepted** | intercepted | inert | resource IDs, as command operands |
| `intercept_line_ranges` | none | **intercepted** | intercepted | inert | `path:start-end` |

Three things hold in every mode: shell `grep` is answered by the annotated grep (it is the one
capability with no competing surface — no read tool answers "which node is DESCRIBED as X"),
edits re-sync the topology through the `arac update-file` hook, and `arac read`/`arac grep`
work from the CLI.

`grep`, `edit` and `write` are **not** MCP tools in any mode (`toolspec.IsShellServedTool`):
their shell forms are intercepted everywhere, so a tool for them offered a second way to ask
one question and charged a schema block per request for it. `Config.ServableMCPTools` is the
one filter every consumer goes through — the server registry, the generated agent markdown,
both harnesses' permission blocks — so a name in one that the server does not register cannot
drift in unnoticed.

**WHY FOUR MODES AND NOT A CROSS-PRODUCT.** This was once `integration.mode`
(terminal/mcp/both) × `identification_mode` (id/line_range) × five `terminal` booleans, with
nothing folding them together — so combinations existed that made no sense. `mcp` with
line-range addressing printed `path:start-end` in every grep header while the only reader in
the project was an MCP `read` that takes ids and has no line-range argument, and that
project's contract never mentioned a range at all. The surfaces are not independent axes.
Those keys are gone: aracne has never had a release that wrote them, so there is nothing to
migrate. A config with no `mode` resolves to `cli`.

`intercept_line_ranges` carries a promise the slice reader enforces: reading an advertised span returns
byte-for-byte what the resource read would have, imports and context included
(`whollyContained` in `universaltools/slice.go`). It is the default addressing for the
intercepting pair on evidence — over `ab-prefer-ids-20260902a` the model used a resource ID as
a command operand **0 times in 408 shell commands** while addressing code as file+line in all
408.

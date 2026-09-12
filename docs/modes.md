# The four modes

`mode` in `.aracne/config.json` is **the one dial**. It answers three questions at once —
which tools exist, which shell commands aracne answers, and what vocabulary the generated
contract teaches — and every artifact `arac setup` writes derives from it.

It is question two of `arac init`, which shows each mode with what it means and an example of
what it looks like. `arac setup` only ever *reads* it: the command that re-renders your
integration must not also be able to change which integration you have. Editing the key by
hand and re-running `arac setup` is the other way.

It decides *which* capabilities the contract may name. How much the contract says about them is
the other dial, `contract_verbosity`, documented in
[configuration.md](configuration.md); the two are orthogonal, and both settings of the second
render from this same four-way switch.

| mode | MCP tools | shell reads | shell grep | `blocked_tools` | addressing |
|------|-----------|-------------|------------|-----------------|------------|
| `mcp` | one `read` + `warnings_list` | run as themselves | intercepted | **active** | resource IDs |
| `cli` *(default)* | none | run as themselves | intercepted | **active** | resource IDs, via `arac read` |
| `intercept_id` | none | **intercepted** | intercepted | inert | resource IDs, as command operands |
| `intercept_line_ranges` | none | **intercepted** | intercepted | inert | `path:start-end` |

That is the main agent's default set (`helper.DefaultAgentMCPTools`). The bug tools are not in
it even with `features.bug_management` on — their schemas are a per-request cost for a workflow
most projects never run — so a project using the bug agents adds them back through
`llm.<harness>.main_agent.mcp_tools`. Generated sub-agents carry their own sets.

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

**WHY FOUR MODES AND NOT A CROSS-PRODUCT.** The alternative is independent axes — a
tool-surface key, an addressing key, and a boolean per interceptable command — and it admits
combinations that make no sense. `mcp` with line-range addressing would print `path:start-end`
in every grep header while the only reader in the project is an MCP `read` that takes ids and
has no line-range argument, and that project's contract would never mention a range at all.
The surfaces are not independent axes; they are four coherent products, and `mode` names the
product. A config with no `mode` resolves to `cli`.

`intercept_line_ranges` carries a promise the slice reader enforces: reading an advertised span
returns byte-for-byte what the resource read would have, imports and context included — for a
method inside a class too, whose resource read carries the enclosing class
(`promotable` in `universaltools/slice.go`). It is the default addressing for the
intercepting pair because file+line is vocabulary the model already has: benchmarking found
models address code as file+line essentially always and as a resource ID essentially never,
whatever the contract asks for. `intercept_id` is there for the properties an ID has that a
span does not — it cannot land mid-declaration, and it does not go stale when the file
shifts.

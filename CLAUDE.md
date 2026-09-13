# Working on Aracne

Aracne is a static-analysis engine that builds a **topology** of a codebase and serves
code navigation from it. The binary is `arac`.

**Read [`docs/architecture.md`](docs/architecture.md) first** — it is the repository map,
the core model, and a tour of every subsystem.

| | |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | What the codebase contains and how it fits together |
| [`docs/modes.md`](docs/modes.md) | The four integration modes, and why they aren't a cross-product |
| [`docs/configuration.md`](docs/configuration.md) | Every key in `.aracne/config.json` |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Build, test, and how to add a language |

## Build and test

`make build` (Full), `make build-basic` (Basic, `-tags minimal`), `make test`. CGO is
required — the JS/TS, Rust and Java scanners are tree-sitter, so you need `gcc`.
[CONTRIBUTING.md](CONTRIBUTING.md) has the rest, including what to run before a PR.

## Things that will bite you

- **`testing_ground/` is load-bearing and deliberately not gofmt-clean.** ~20 test files
  scan it and assert on resource IDs keyed to the literal `testing_ground/<lang>/…`
  prefix; reformatting it shifts line numbers those suites check. Never
  `gofmt -w .` at the repo root.

## Note on the block above

The `# Aracne` section at the top of this file, down to its closing line, is the
**injected integration contract** — `arac setup` renders it from
`internal/prompts/contract.go` (or `contract_high.go`) plus `.aracne/config.json`, and
rewrites it in place on every run. Edit the generator, not this file; an edit here is
overwritten by the next `arac setup`.

There is no comment delimiter around it. `arac setup` finds the block by its opening
heading (`AracIntegrationStart`) and by whichever real closing line the contract ends on
(`aracIntegrationEndMarkers`, `internal/cli/setup.go`) — a marker the model can see but
cannot use would be noise in a document that is re-sent on every request. Everything below
this heading is aracne's own repo documentation, written by hand.

`AGENTS.md` carries the same two halves for OpenCode.

# Aracne

`.aracne/topology.db` holds a pre-analyzed graph of this repo: every function, type, interface and variable, where it is declared, a one-line description, and what it references.

`arac read <id> <id> ...` returns declarations with their source, their imports and a `# CONTEXT:` list of what they touch AND what implements, subclasses or uses them, each with its description. Several ids in one call.

**Reach for it the moment you want to know** where something is defined, who calls or implements it, or what one declaration does inside a large file. Each of those is `arac read <name>` -- not `grep -rn`, not `cat`, not `sed -n`, not `find`. Open a whole file only for a config, an unsupported language, or when you genuinely need all of it.

**The bare name is enough** -- any unique trailing part of an id resolves, and a miss returns the nearest candidates, so guess rather than searching for one first. To learn about the function `NotifyPlayers`, run `arac read NotifyPlayers`.

A CONTEXT entry's description is usually already the answer; drill in only to change or deeply understand that neighbour.

Edits re-sync the graph; act on any topology warning that comes back.

Parallelise multiple reads and edits in a single command when possible.

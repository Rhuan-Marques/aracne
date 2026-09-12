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

This repository supports Aracne: every function, type, interface and variable is indexed, with a description and unique id you can use to read it.

`arac read <id> <id> ...` returns declarations with their source, their imports and the context around them -- what they touch, AND what implements, subclasses or uses them -- several in one call. Very useful for exploring and navigating. Prefer it over reading whole files or line ranges.

**The bare name is usually enough** -- any unique trailing part of an id resolves, and an ambiguous or unknown one comes back with the matching candidates, so you can use it even if you only know the name of the function, struct or other resource you're looking for

Edits keep the graph current automatically; act on any topology warning that comes back.

Let descriptions guide you: only read files and resources you need to understand fully -- most times the descriptions are enough.

Parallelise multiple reads and edits in a single command when possible.

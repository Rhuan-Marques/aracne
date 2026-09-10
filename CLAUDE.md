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

## Two things that will bite you

- **`testing_ground/` is load-bearing and deliberately not gofmt-clean.** ~20 test files
  scan it and assert on resource IDs keyed to the literal `testing_ground/<lang>/…`
  prefix; reformatting it shifts line numbers those suites check. Never
  `gofmt -w .` at the repo root.
- **Go resource IDs are module-path-prefixed.** They begin
  `github.com/Rhuan-Marques/aracne/…`, pinned by `corpusModulePath` in
  `tests/atscale_harness_test.go`, which must stay equal to `go.mod`'s module line.

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

## How it reaches you

Aracne's capabilities arrive as MCP tools; each tool's own description says how to call it. **Prefer a symbol over a file:** reading a declaration returns its source, its imports, and a `# CONTEXT:` list of the neighbours it touches with their descriptions -- usually the answer, for a fraction of a file's tokens.

`grep` is answered from the topology however you run it, and additionally searches node names and stored descriptions -- so a plain-English query finds code that never says the word.

## Other

- Your edits keep the graph current automatically; act on any topology warning that comes back.

## Behavioral Rules

1. **Be concise** -- report what you found and what you changed, not how you did it.
2. **Trust the topology** -- it is the source of truth and re-syncs after every edit. Never parse
   code by hand, and never ask for a re-scan.
3. **Do not guess** -- report an empty result or an error as what it is; never invent code or
   relationships.
4. **Do not re-read** -- if it is already in your context, use it.

Good Luck in your task.

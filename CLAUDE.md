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

```sh
make build        # Full  (engine + web visualizer)
make build-basic  # Basic (engine only, -tags minimal)
make test         # go test ./...
```

CGO is required — the JS/TS, Rust and Java scanners are tree-sitter, so you need `gcc`.

## Two things that will bite you

- **`testing_ground/` is load-bearing.** ~20 test files scan it and assert on resource IDs
  keyed to the literal `testing_ground/<lang>/…` prefix. It is deliberately full of edge
  cases and deliberately *not* gofmt-clean; reformatting it shifts line numbers the
  at-scale suites check.
- **Go resource IDs are module-path-prefixed.** They begin
  `github.com/Rhuan-Marques/aracne/…`. The at-scale harness pins them with
  `corpusModulePath` in `tests/atscale_harness_test.go`, which must stay equal to the
  module line in `go.mod`.

## Note for agents

Everything above the `---` in a *generated* CLAUDE.md is the injected integration
contract, written by `arac init` from `.aracne/config.json`. This file is aracne's own
repo documentation and carries no contract — the contract lives in
`internal/prompts/contract.go`.

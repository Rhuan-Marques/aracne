# Contributing to Aracne

## Setup

You need **Go 1.25+** and **gcc**. CGO is not optional: the JavaScript, TypeScript, Rust
and Java scanners are tree-sitter. SQLite is pure Go, so there is no C SQLite to install.

```sh
git clone https://github.com/Rhuan-Marques/aracne && cd aracne
make build     # Full build  -> bin/arac
make test      # the whole suite, ~1-2 minutes
make help      # every target
```

There is no front-end build step. The visualizer is hand-written HTML/JS/CSS in
`internal/viz/static/`, embedded with `go:embed` — rebuild the Go binary to pick up a
change.

## Before you open a PR

```sh
make fmt
make vet          # go vet under BOTH tag sets
make test
make test-minimal # the suite under -tags minimal
```

`make test-minimal` matters: the Basic build is a real shipped artifact, and it is the one
a default `go test ./...` does not exercise.

## Two things that will bite you

**`testing_ground/` is load-bearing, and deliberately not gofmt-clean.** It is a
hand-built multi-language corpus of edge cases, and about twenty test files scan it and
assert on resource IDs keyed to the literal `testing_ground/<lang>/…` prefix. Reformatting
it shifts line numbers the at-scale suites check. `make fmt` skips it on purpose — please
don't run `gofmt -w .` at the repo root.

**Go resource IDs carry the module path.** They begin `github.com/Rhuan-Marques/aracne/…`.
The at-scale harness pins this with `corpusModulePath` in `tests/atscale_harness_test.go`,
which must stay equal to the module line in `go.mod`. If you ever change one, change both.

## Layout

```
cmd/arac/          main, the subcommand dispatcher
internal/
  cli/             every `arac <cmd>` entry point + tool-registry wiring
  topology/        the engine: domain model, scanners, incremental scan, warnings
  llm/languages/   how a resource is rendered back (per language)
  shellcmd/        argv -> request parser for the terminal surface
  helper/          config, SQLite, manifest, incremental/partial scan
  viz/, chat/      the web visualizer and its chat harness (Full build only)
  prompts/         every generated agent/contract/command markdown
tests/             cross-package integration + the at-scale suites
testing_ground/    the multi-language corpus those suites scan
bench/             the A/B benchmark harness (Python). Not in any build.
docs/              architecture, modes, configuration, and design history
```

Start at the `case` in `cmd/arac/main.go` and jump to `internal/cli/<name>.go`; the CLI is
the index. [docs/architecture.md](docs/architecture.md) is the full tour.

## Adding a language

Roughly, in this order — every existing language followed the same path:

1. **A scanner** in `internal/topology/scanner/<lang>scanner/` (parser + resolver). The
   tree-sitter ones (`jsscanner`, `rustscanner`, `javascanner`) are the closest templates.
2. **A typed topology** in `internal/topology/<lang>/`, and the language's rules in
   `internal/topology/contract/<lang>.go` — how a call site is recorded and when a
   signature change still fits.
3. **Rendering** in `internal/llm/languages/<lang>tools/`, then register it in
   `universaltools/read.go` and `internal/cli/tool_profiles.go`.
4. **Corpus + tests**: a `testing_ground/<lang>family/` subtree of edge cases, an
   `tests/atscale_<lang>_test.go` running all three scan modes against it, and a
   `tests/<lang>family_edgecases_test.go`.

The at-scale suite is the bar: a language is supported when its topology is identical
across full, incremental and hard scans.

## Style

- Commit messages: `type(scope): what changed`, lowercase, imperative. The body should say
  **why** — this codebase's comments and commits explain reasoning, not mechanics, and
  that is the convention worth keeping.
- Comments earn their place by saying why something is the way it is, especially where the
  obvious alternative is wrong. Don't restate the code.
- Every new config key needs a line in [docs/configuration.md](docs/configuration.md), and
  every new feature flag needs adding to `validConfig` — see the comment there for the
  silent failure that omission causes.

## Reporting bugs

Open an issue with the language, the `arac --version` output, and ideally the smallest
source file that reproduces it. A failing case added to `testing_ground/` is the most
useful bug report there is.

<p align="center">
  <img src="assets/logo.png" alt="Aracne" width="180">
</p>

<h1 align="center">Aracne</h1>

<p align="center">
  <em>Give a coding agent a map of your codebase instead of a filesystem.</em>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <img alt="Go 1.25" src="https://img.shields.io/badge/go-1.25-00ADD8.svg">
  <img alt="Languages" src="https://img.shields.io/badge/languages-Go%20%7C%20Python%20%7C%20JS%20%7C%20TS%20%7C%20Rust%20%7C%20Java-informational.svg">
</p>

---

Aracne scans a source tree and builds a **topology**: a directed graph of every package,
file, function, method, class/struct, interface, named type, variable and dependency, plus
the edges between them — who calls whom, which type implements which interface, what
imports what. It stores that graph in SQLite next to your code and keeps it current as
files change.

Then it answers questions from the graph instead of from raw text.

When an agent runs `head -40 server.go`, Aracne returns those forty lines *framed by the
declaration they sit inside* and followed by a `# CONTEXT:` block naming only the
resources those lines actually mention — each with a one-line description. The agent gets
the code plus the map around it, and stops paying for the three re-reads it would have
needed to work out where it landed.

## Install

```sh
go install github.com/Rhuan-Marques/aracne/cmd/arac@latest
```

Or grab a binary from [Releases](https://github.com/Rhuan-Marques/aracne/releases). Two
builds are published:

| Build | Contains |
|---|---|
| **Full** | Engine, all six languages, and the web visualizer (`arac viz serve`) |
| **Basic** | The same engine and languages, no front-end — a smaller binary for CI and servers |

Building from source needs **gcc** — the JavaScript, TypeScript, Rust and Java scanners are
tree-sitter, so CGO is required. SQLite is pure Go. See
[CONTRIBUTING.md](CONTRIBUTING.md#setup).

Scanning **Python** additionally needs a `python3` (or `python`) on `PATH` at run time: that
scanner drives the interpreter's own parser rather than a grammar, so without one a Python
project simply yields no topology. Every other language is self-contained in the binary.

## Quickstart

```sh
cd your-project
arac init
```

That is the whole setup. `arac init` is an interactive, full-screen flow that asks the six
questions a project actually has to answer — which harness, how the agent should reach your
code, who writes the descriptions and with which model, whether to describe the repository now
or lazily as you read, and how much the generated contract should say. It then saves them to
`.aracne/config.json`, scans the project, and writes the integration for your harness: the
contract block in `CLAUDE.md`/`AGENTS.md`, the hooks, and the config. From then on your
agent's ordinary `grep` and `cat` are answered from the topology.

Answer with the arrow keys and Enter; Escape cancels without writing anything. It needs a
terminal — in a pipe or in CI, use `arac setup`, which writes the same files from whatever
`.aracne/config.json` already says and asks nothing:

```sh
arac setup --claude      # or --opencode, or neither for both
```

Re-run `arac setup` after editing the config by hand. It reads `mode` and never writes it.

You can drive it yourself too:

```sh
arac read internal/server.Handler       # a resource, with its context
arac grep "retry"                       # searches names, descriptions AND contents
arac resource list --kind interface
arac warnings list                      # what drifted since the last scan
arac scanner run                        # keep the topology current in the background
arac viz serve                          # the graph, in a browser (Full build)
```

**Description generation** is the one part that needs a model, and nothing is assumed about
which. `arac init` asks — an API key or a CLI you are already logged into, and which model —
and writes the answer into `.aracne/config.json`, so it is asked once:

```jsonc
// .aracne/config.json, after answering
"descriptions": { "provider": "openai", "api_key_env": "OPENAI_API_KEY" }
"descriptions": { "provider": "cli", "cli_provider_command": "claude -p" }
```

The CLI answer needs no API key at all — it spends the subscription behind a tool you already
use. Descriptions are also generated lazily, as a read or a search is about to show a node, so
a repo warms up as you work in it.

Everything else — scanning, reading, grepping, the visualizer — needs no key.
[configuration.md](docs/configuration.md#who-writes-the-descriptions) has the whole story.

## The four modes

Aracne's whole shape is one config key, `mode`, because how the agent reaches the topology
should be one decision rather than five independent switches:

| mode | How the agent reaches it |
|---|---|
| `cli` *(default)* | No MCP tools. Shell reads run as themselves; the contract points at `arac read`. |
| `intercept_id` | `cat`/`head`/`tail`/`sed -n` are answered from the topology and take a resource ID. |
| `intercept_line_ranges` | The same, with every declaration named by the exact lines it spans. |
| `mcp` | A single `read` MCP tool; shell reads run as themselves. |

Shell `grep` is answered by Aracne in **all four**, and edits re-sync the graph in all four.
See [docs/modes.md](docs/modes.md).

## Does it work?

There is a real benchmark, not a demo: [`bench/`](bench/) runs the **same model twice** on
real, test-graded GitHub issues — once with native file tools, once with Aracne — and
compares them **within task**, so difficulty cancels. It grades with each repo's own test
suite, bootstraps over repositories rather than tasks, and reports token cost, solve rate
and turns separately.

Results are reported per run, with their sample sizes and confidence intervals, in
[`bench/README.md`](bench/README.md). Read those numbers rather than a headline: the
harness is built to make the comparison honest, including when it is unflattering.

## Documentation

| | |
|---|---|
| [docs/architecture.md](docs/architecture.md) | What the codebase contains and how it fits together |
| [docs/modes.md](docs/modes.md) | The four modes, and why they aren't a cross-product |
| [docs/configuration.md](docs/configuration.md) | Every key in `.aracne/config.json` |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Build, test, and how to add a language |
| [bench/README.md](bench/README.md) | The benchmark harness and its statistics |

## Next up

Uniting nodes into **functionalities** an agent can generate, maintain, and grep for
directly.

## License

[Apache-2.0](LICENSE). See [NOTICE](NOTICE) for third-party attribution.

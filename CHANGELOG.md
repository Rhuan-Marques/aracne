# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] — unreleased

First public release.

### Added

- **Topology engine** for six languages — Go, Python, JavaScript, TypeScript, Rust, Java —
  with full, incremental and hard scan modes, verified identical by the at-scale suites.
- **Four integration modes** (`cli`, `intercept_id`, `intercept_line_ranges`, `mcp`) behind a
  single `mode` key, replacing a cross-product of independent switches that allowed
  combinations which made no sense.
- **Terminal surface**: `arac cmd` answers shell reads and searches from the topology, and
  passes through byte-identically for anything it does not model.
- **Topology-annotated grep** in every mode, searching node names and descriptions as well
  as file contents.
- **Description generation**, both as a sweep (`arac descriptions generate`) and lazily on
  the read path, against Anthropic, OpenAI or DeepSeek.
- **Web visualizer** (`arac viz serve`) with graph, neighborhood and search views.
- **Harness integration** (`arac init`) for Claude Code and OpenCode: contract, hooks,
  agents, commands, MCP config.
- **Two published builds** — Full (with the visualizer) and Basic (`-tags minimal`).
- `arac warnings list`, `arac check-updates`, `arac disable`, `arac scanner run`, and
  `arac descriptions export|import`.

[Unreleased]: https://github.com/Rhuan-Marques/aracne/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Rhuan-Marques/aracne/releases/tag/v1.0.0

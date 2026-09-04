# Security Policy

## Supported versions

The latest released version receives security fixes.

## Reporting a vulnerability

Please **do not** open a public issue for a security problem.

Report it through GitHub's private advisory form:
<https://github.com/Rhuan-Marques/aracne/security/advisories/new>

Include what you can reproduce and the version (`arac --version`). You should get an
acknowledgement within a few days, and an assessment once it has been reproduced.

## Scope notes

A few things are worth knowing about Aracne's threat model:

- **`arac serve`, `arac agent` and `arac viz serve` are local-only tools.** The visualizer
  binds `127.0.0.1` by default and rejects cross-origin requests to `/api/`. Exposing any
  of them on a network interface is not a supported configuration.
- **Aracne executes nothing from the code it scans.** Scanners parse; they do not evaluate.
- **Provider API keys** are read from the environment and never written to
  `.aracne/topology.db` or to any generated file.
- **`.aracne/` holds a database of your source code.** It is gitignored by default. Treat
  it with the same care as the repository it describes.

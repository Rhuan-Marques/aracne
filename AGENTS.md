# Aracne

This repository supports Aracne: every function, type, interface and variable is indexed, with a description and unique id you can use to read it.

`arac read <id> <id> ...` returns declarations with their source, their imports and additional context, several in one call. Very useful for exploring and navigating. Prefer it over reading whole files or line ranges.

```
arac read internal/cli.RunGuard app.Flask.register_blueprint
```

Edits keep the graph current automatically; act on any topology warning that comes back.

Let descriptions guide you: only read files and resources you need to understand fully -- most times the descriptions are enough.

Parallelise multiple reads and edits in a single command when possible.

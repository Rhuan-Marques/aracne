# Aracne Testing Ground

A hand-built corpus for exercising the aracne topology engine end-to-end with
**real-time edits and scans** (via `mcp__aracne__write` / `edit`). Every
supported language is represented with **many interconnected resources** and a
deliberate emphasis on **edge cases**: every resource kind and as many weird
cross-resource interactions as the parsers track.

> This folder exists to be scanned, edited, and re-scanned. It is intentionally
> separate from the real aracne source. The Go packages compile cleanly into the
> `aracne` module (`go build ./testing_ground/go/...`), the Python files import
> with relative imports, and the JS/TS files are valid ESM/CJS/TSX.

## Layout

```
testing_ground/
├── go/            module-rooted import paths: aracne/testing_ground/go/<pkg>
│   ├── shapes/      interfaces (multi/single impl, embedded, empty), structs, methods, iota, consts
│   ├── geometry/    cross-package return types, variadic, multiple returns, fan-out
│   ├── consumer/    FLAGSHIP: imports a package, receives a function's result as a var, calls methods
│   ├── generics/    generic types/functions, constraints, instantiation
│   └── edge/        defined type vs alias, func-typed fields, anon structs, closures, blank idents
├── python/        package aracne.testing_ground.python (relative imports)
│   ├── shapes.py    ABC + @abstractmethod, Protocol, @dataclass, single/multiple inheritance, property/static/classmethod
│   ├── factory.py   cross-module return types, async, *args/posonly/kwonly/defaults/**kwargs
│   └── consumer.py  module-var via cross-module call, param/local/factory method resolution, nested + hoisted defs
├── jsfamily/      ONE JS topology (.js + .jsx + .cjs)
│   ├── shapes.js    classes + extends, getter/setter, static, default + named exports
│   ├── factory.js   default+named imports, arrow/async/generator, new-based resolution
│   ├── consumer.js  default/named/aliased/namespace/side-effect imports
│   ├── commonjs.cjs require (destructured + external), exports.x = ...
│   ├── bundle.cjs   module.exports = { ... }, whole-module require + member call
│   └── components.jsx React function + class components, hook, mixin extends Mixin(Base)
└── tsfamily/      ONE TS topology (.ts + .tsx + .d.ts), separate from jsfamily
    ├── models.ts    interfaces (single/multi impl), interface extends, type aliases (union/intersection/generic), enums
    ├── shapes.ts    abstract class implements interface, access modifiers, param property, generic class
    ├── factory.ts   cross-module return types, annotation-driven method resolution (param/local)
    ├── consumer.ts  namespace block, module-var via cross-module call, aliased export, default export
    ├── ambient.d.ts declare function/interface/const, ambient namespace
    └── components.tsx typed function/class components, function-typed prop, enum usage
```

## How the scanners pick this up

- **Go** is detected via the repo `go.mod`. Each subdirectory is its own package;
  import paths are real (`aracne/testing_ground/go/...`), which is what lets
  cross-package edges resolve.
- **JavaScript** is detected via the repo `package.json`.
- **Python** and **TypeScript** have no root detection file, but the registry's
  `scannerHasFiles` walks recursively, so the presence of `.py` / `.ts` files
  here activates both scanners. `mcp__aracne__write` also routes each file to the
  right scanner by extension.
- **JS and TS are independent topologies** — cross-language imports do not
  resolve. That is why `.js/.jsx/.cjs` live together and `.ts/.tsx/.d.ts` live
  together, each importing only within its own family.

### File-name exclusions (do not rename fixtures to these)

The scanners silently skip: Go `*_test.go`; Python `test_*.py`; JS/TS
`*.test.*`, `*.spec.*`, `*.min.*`; and any dot-directory or `vendor`,
`node_modules`, `__pycache__`, `venv`, etc.

## Edge cases covered

### Go (`go/`)
- Interface with **multiple** implementers (`shapes.Shape` ← Circle/Rectangle/Triangle) and **single** implementer (`shapes.Drawable` ← Canvas).
- **Embedded interface** (`Solid` embeds `Shape`) and **empty interface** field (`Container.Any`) — both are known parser corner cases (embedded interfaces are not flattened; empty interfaces get no `implemented_by`).
- **Value vs pointer** receivers; **embedded struct** (`Square` embeds `Rectangle`).
- Constructors: pointer (`NewCircle`), value (`NewRectangle`), multi-return (`NewTriangle`).
- **Cross-package return-type inference** (the user's flagship case): `consumer.Report` does `c := geometry.MakeCircle(3)` then `c.Area()` → resolves to `shapes.(Circle).Area`.
- Constructor-return inference, multi-value cross-package assignment, **blank identifiers**, **named/multiple returns**, **variadic**, interface-method **fan-out**.
- **Generics**: type params, constraint type sets, generic constructor, function-typed parameter, instantiation.
- **Defined type vs type alias**, named slice/func types, **anonymous struct** field, **closure**, iota const block, external-import call (`fmt`/`math`/`strings`) producing **no internal edge**.

### Python (`python/`)
- **ABC** + `@abstractmethod` (`Shape`), **Protocol** (`Drawable`), **@dataclass** (`Point`).
- A subclass that fully implements the ABC (`Circle`) and one that **does not** (`IncompleteShape`); **single** (`Square`) and **multiple** inheritance (`LabeledCircle`).
- `@property`, `@staticmethod`, `@classmethod`.
- **async def**, `*args`, positional-only `/`, keyword-only `*`, defaults, `**kwargs`.
- **Cross-module return types**; module-level var initialized by a **cross-module call** (`DEFAULT_CIRCLE = make_circle(1.0)`).
- Method resolution via **param annotation** (`render`), **local instantiation** (`build_and_measure`), and **factory-return chaining** (`from_factory`).
- **Nested** function (not attributed to parent) and functions **hoisted** out of `if` / `try` blocks.

### JS family (`jsfamily/`)
- ES classes with `extends`, getter + setter, static method, default + named exports.
- Import flavours: default, named, **aliased** (`Circle as Disc`), **namespace** (`* as factory`), **side-effect**.
- Function / **arrow** / **async** / **generator**.
- **CommonJS**: destructured `require`, external `require`, `exports.x = ...`, `module.exports = { ... }`.
- `new X()`-based method resolution (resolves) vs plain return-value chaining (does not — documented JS limitation).
- React **function** and **class** components, a hook (`useState`), and a **mixin** (`extends Serializable(Circle)`). JSX element usage is not modeled as edges.

### TS family (`tsfamily/`)
- Interfaces with **multiple** implementers (`Shape`) and **single** implementer (`Drawable`); **interface extends interface** (`Solid`).
- **Type aliases**: union, object, intersection, generic function type. **Enums**: bare and assigned members.
- **Abstract class implements interface**, access modifiers (`public`/`private`/`protected`), **parameter property**, **generic class**.
- **Annotation-driven method resolution** — the headline TS feature — via param type (`render`) and local type (`areaOf`), plus cross-module return types.
- **Namespace** block, **aliased export**, **default export**, **ambient `.d.ts`** declarations.
- TSX typed function/class components, **function-typed prop**, enum usage.

## Bugs surfaced while building this

Building the corpus reproduced two real aracne defects (both filed via
`mcp__aracne__bug_report`). Both share one failure mode: a **duplicate resource
ID → duplicate connection → `UNIQUE(source_id, conn_type, target_id)` violation
that aborts the entire file's topology write** (the file still lands on disk, so
disk and topology silently diverge, and later full rescans of that file also
fail).

1. **Python — name bound in two sibling control-flow branches.** Minimal repro:
   ```python
   try:
       FLAG = True
   except Exception:
       FLAG = False
   ```
   `extract_body` recurses into every branch (try **and** except / if **and**
   else), so the name is emitted twice. Common in import fallbacks and config
   shims. `consumer.py` therefore keeps its `except` branch empty (`pass`) and
   never reassigns a module-level name.

2. **JS/TS — getter and setter with the same name.** Minimal repro:
   ```js
   class Box { get value() {} set value(x) {} }
   ```
   `parseMethod` gives the accessor pair the same ID `<Class>.value`, and
   `populateClassMethods` appends it twice without de-duping. `shapes.js`
   therefore uses a getter and a setter on **different** property names
   (`get diameter` / `set scale`); `shapes.ts` uses a getter only.

## How to verify

Topology (preferred — these were used to validate every family):

```
mcp__aracne__read_interface  aracne/testing_ground/go/shapes.Shape          # multi-impl
mcp__aracne__read_function   aracne/testing_ground/go/consumer.Report       # cross-pkg inference
mcp__aracne__read_function   aracne.testing_ground.python.from_factory      # factory chaining
mcp__aracne__read_function   aracne/testing_ground/jsfamily/consumer.report # aliased/namespace imports
mcp__aracne__read_interface  aracne/testing_ground/tsfamily/models.Drawable # single-impl
mcp__aracne__read_function   aracne/testing_ground/tsfamily/factory.areaOf  # annotation-driven
mcp__aracne__grep            "<pattern>"   testing_ground
mcp__aracne__warnings_list
```

Compilers / syntax (host toolchain):

```
go build ./testing_ground/go/...          # compiles clean
python3 -m py_compile testing_ground/python/*.py
# node/tsc not required; aracne parses JS/TS via tree-sitter
```

### Resource-ID formats

| Language | Resource | Function | Method |
|---|---|---|---|
| Go | `aracne/testing_ground/go/<pkg>.<Name>` | same | `aracne/.../<pkg>.(<Recv>).<Method>` |
| Python | `aracne.testing_ground.python.<Name>` | same | `aracne.testing_ground.python.<Class>.<method>` |
| JS | `aracne/testing_ground/jsfamily/<file>.<Name>` | same | `<file>.<Class>.<method>` |
| TS | `aracne/testing_ground/tsfamily/<file>.<Name>` | same | `<file>.<Class>.<method>` |

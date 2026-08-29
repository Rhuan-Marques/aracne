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
│   ├── generics/    generic types/functions, constraints, instantiation, GENERIC INTERFACE (Box/Container)
│   ├── edge/        defined type vs alias, func-typed fields, anon structs, closures, blank idents; iota EXPRESSIONS, typed consts, struct tags (more.go)
│   ├── recursive/   self-referential + mutually-recursive structs, self-recursive method, mutual-recursion funcs
│   ├── failure/     error interface (Error()), sentinel error var, local interface returning error, %w wrap
│   ├── embedding/   value/pointer/cross-package struct embedding, interface-in-struct, method promotion
│   ├── dispatch/    method value/expression, type assertion, type switch (call-resolution probes)
│   ├── concurrency/ defer, goroutine on named fn + literal, channel fields, select
│   ├── visibility/  unexported interface/struct/ctor/var, unexported interface matching, cross-visibility calls
│   ├── inits/       init() function, package-level var initialized by a local call
│   └── dotimport/   dot import (import . "strings") name-resolution probe
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
├── tsfamily/      ONE TS topology (.ts + .tsx + .d.ts), separate from jsfamily
│   ├── models.ts    interfaces (single/multi impl), interface extends, type aliases (union/intersection/generic), enums
│   ├── shapes.ts    abstract class implements interface, access modifiers, param property, generic class
│   ├── factory.ts   cross-module return types, annotation-driven method resolution (param/local)
│   ├── consumer.ts  namespace block, module-var via cross-module call, aliased export, default export
│   ├── ambient.d.ts declare function/interface/const, ambient namespace
│   └── components.tsx typed function/class components, function-typed prop, enum usage
├── rustfamily/    ONE Rust crate (Cargo.toml + src/**.rs), package name `rustfamily`
    ├── Cargo.toml    [package] name = "rustfamily"; optional `serde` dep (feature-gated) for the uses_dependency edge
    ├── src/lib.rs    crate root: `pub mod ...` declarations + `pub use shapes::Circle` re-export (imports_module to shapes)
    ├── src/shapes.rs  trait Shape (default method + assoc const); named/tuple/unit struct + newtype; inherent + trait impl; ctor; type alias
    ├── src/geometry.rs enum Geometry (tuple + struct variants) + `match`; same-file `impl Shape for Geometry`
    ├── src/factory.rs  free fns make_circle (value ctor) / make_shape (Box<dyn Shape>) / make_geometry; fn-pointer type alias
    ├── src/consumer.rs FLAGSHIP: import sibling module, receive a ctor's return in a local, call methods (cross-module resolution)
    ├── src/util/mod.rs nested module via mod.rs; pub struct + pub(crate) const + private fn; import cycle with math
    ├── src/util/math.rs leaf module: pub(crate) const PI, private const, pub static, `use super::`; uses PI + calls double
    ├── src/traits.rs  supertrait Drawable: Shape (inherits), generic trait + where, &dyn / impl Trait params, #[derive(Tagged)]
    ├── src/errors.rs  error enum, `?` propagation, sentinel const, `use serde::Serialize` + #[derive(Serialize)] (feature-gated)
    └── src/redprobes.rs RED PROBES (each `// GAP:`): trait-object, generic, `?`-unwrap, closure/iterator method dispatch
└── javafamily/    ONE Maven project (pom.xml + src/main/java/com/aracne/**), package `com.aracne.*`
    ├── pom.xml         Maven build file (the Detect anchor) + the sole external (guava) dependency
    ├── shapes/         interface Shape + Circle/Rectangle impls; OVERLOAD area(int); constructor; @Override
    ├── factory/        static factory methods returning the Shape interface, constructing concrete types
    ├── consumer/       FLAGSHIP cross-file: factory return into a Shape-typed local, then `.area()`
    ├── inheritance/    abstract base + subclass (class<->class inherits); sealed Expr + permits; multi-iface extends/implements
    ├── enums/          enum implements interface; CONSTANT BODY (Op$PLUS inherits Op); enum-level + overridden methods
    ├── records/        record Point(x,y) implements Located; compact constructor; synthesized accessors x()/y()
    ├── nested/         static-nested, inner, local ($Helper) and anonymous ($anon1) classes; field/class name disjointness
    ├── config/         static + instance initializer blocks -> <clinit>() / <instance-init>()
    ├── annotations/    @interface Marker (IsAnnotation) + a class that USES @Marker (uses_interface)
    ├── generics/       Box<T extends Shape>: bounded type param, generic STATIC method, wildcard List param
    ├── lambdas/        lambdas + method/constructor references; calls attribute to the ENCLOSING method
    ├── exceptions/     throws clause, try-with-resources, catch-wrap-rethrow; external supertype (extends Exception)
    ├── arrays/         varargs int... -> int[] and explicit String[] keep array dims in the method ID
    ├── external/       JDK (java.util) + the guava dependency; external imports -> NO false internal edge
    └── redprobes/      RED PROBES (each `// bug _N`): generic-return propagation + erased-overload collision
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
- **Rust** is detected via `rustfamily/Cargo.toml` (and the registry's recursive
  walk also activates the scanner on any `.rs` file). The whole `rustfamily/`
  tree is one crate = **one topology**; symbol IDs are `::`-rooted at the Cargo
  `[package].name` (`rustfamily`), so module paths come straight from the file
  layout (`src/lib.rs`/`src/main.rs` → crate root, `src/util/mod.rs` →
  `rustfamily::util`, `src/util/math.rs` → `rustfamily::util::math`).
- **Java** is detected via `javafamily/pom.xml` (any of `pom.xml` /
  `build.gradle` / `build.gradle.kts` / `settings.gradle`), and the registry's
  recursive walk also activates the scanner on any `.java` file. Java is
  **module-first** (like Python/JS/Rust): there is **no package node** — each
  `.java` file is a module keyed by absolute path, and symbol IDs are **FQNs
  rooted at the in-file `package …;` declaration** (`com.aracne.shapes.Circle`),
  independent of the file layout. Method IDs always carry a parenthesized
  signature (`area(int)`, even no-arg `area()`), so overloads stay distinct.

### File-name exclusions (do not rename fixtures to these)

The scanners silently skip: Go `*_test.go`; Python `test_*.py`; JS/TS
`*.test.*`, `*.spec.*`, `*.min.*`; Rust `target/`, `tests/`, `benches/` dirs;
Java `*Test.java` / `*Tests.java` / `*IT.java` files and
`target/`/`build/`/`.gradle/`/`out/`/`bin/`/`test/`/`tests/` dirs;
and any dot-directory or `vendor`, `node_modules`, `__pycache__`, `venv`, etc.

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
- **Recursion** (`recursive/`): self-referential struct (`Node.Next *Node`), mutually-referential structs (`Folder`/`File`), and **mutually-recursive functions** (`ping`↔`pong`, resolved as `calls`). A self-recursive method call *through a struct field* (`n.Next.Length()`) is NOT resolved (field-access method resolution is out of scope).
- **Errors** (`failure/`): a struct implementing the **builtin `error`** via `Error()` (no edge to the builtin), a **sentinel error var** (`errors.New`), a local `Validator` interface whose method returns `error` (matched), and `fmt.Errorf("%w")` wrapping.
- **Embedding** (`embedding/`): value/pointer/**cross-package** struct embedding (cross-pkg embed records `uses_package`, not `uses_struct`) and an **interface-typed embedded field**. **Method promotion is not modeled** — a call to a promoted method does not resolve.
- **Dispatch probes** (`dispatch/`): **method value** (`f := d.Sound`), **method expression** (`Dog.Sound`), **type assertion** (`a.(Dog)`), and **type switch** — none currently resolve the subsequent method call to a `calls` edge (left as red probes / filed bugs).
- **Concurrency** (`concurrency/`): a **deferred** call, a **goroutine** on a named function and on a function literal, and a `select` case all produce ordinary `calls` edges; channel-typed fields/params carry no spurious edges.
- **Visibility** (`visibility/`): **unexported** interface/struct/constructor/var, unexported interface↔struct matching, and exported→unexported `calls` across the visibility boundary.
- **Init & package vars** (`inits/`): an `init()` function (id `…inits.init`) and a package-level var initialized by a **local call** (`var Config = loadConfig()`).
- **Generic interface** (`generics/`): `Box[T]` structurally satisfies `Container[T]` — generic-interface satisfaction matching is NOT yet implemented (red probe / filed bug).
- **Dot import** (`dotimport/`): `import . "strings"` — the unqualified `ToUpper` produces **no false internal edge** (only a `use_missing_node` warning).

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

### Rust (`rustfamily/`)
- **Trait** with **multiple** implementers (`shapes.Shape` ← `Circle` / `geometry.Geometry`) and **single** implementer (`traits.Tagged` ← `Label`).
- **Supertrait** `trait Drawable: Shape` resolves to an **`inherits`** edge (cross-module: `traits.Drawable → shapes.Shape`), with the reverse `inherited_by` on `Shape`.
- **Generic trait** `Container<T>` with a `where T: Clone` bound; functions taking **`&dyn Shape`** and **`impl Shape`** (trait-object / static-dispatch parameters).
- Struct kinds: **named** (`Circle`), **tuple** (`Meters(f64)`), **unit** (`Origin`), and a **newtype** over an internal type (`Disk(Circle)`). **Named types** (type aliases): a primitive alias (`shapes.Radius`) and a **function-pointer** alias (`factory.ShapeFactory`).
- **Enums** model variants as a **property list** (no per-variant node): `geometry.Geometry { Round(Circle), Rect { w, h } }` (tuple + struct variants) and `errors.MyError { Missing, BadRadius(f64) }`. A method `match`es over the variants.
- **Inherent + trait method coexistence**: `Circle` has both an inherent `area` and a `Shape::area`; they collapse to a single `Circle::area` node (deduped by ID, no write-abort).
- **Constructors** auto-detected (assoc fn named `new`/`default`/`with_*`/`from_*` returning `Self`/the owning type): `Circle::new`, `Unit::new` get a `constructor` link.
- **Cross-module return-type inference** (the flagship): `consumer.report` does `let c = Circle::new(2.0); c.area()` and `let s = make_circle(1.0); s.diameter()` → resolves `calls` to `shapes.Circle::{new,area,diameter}` and `factory.make_circle`. Cross-file impl methods attach to the type's ID regardless of which file the `impl` lives in.
- **`use` resolution**: `use crate::...` → `imports_module`; grouped uses (`use crate::shapes::{Circle, Shape}`); a crate-root **re-export** (`pub use shapes::Circle`) still records `imports_module`; `use super::` / `use self::` (the `util`↔`util::math` **import cycle** resolves both ways).
- **External dependency** edge: `errors.rs`'s `use serde::Serialize;` (declared optional in `Cargo.toml`, feature-gated in source) yields a `serde` **dependency** node + an `imports_dependency` edge.
- **`#[derive(Tagged)]`** on an *internal* trait produces an **`implements`** edge (`Label → Tagged`); external derives (`Serialize`, `Clone`) resolve to nothing.
- **Module-level vars**: `pub(crate) const PI`, a private `const`, a `pub static`, and a sentinel `const NOT_FOUND`; a function-body reference to `PI` records a `uses_variable` edge (`math.circumference → math.PI`).
- **Visibility** variants exercised throughout: `pub`, `pub(crate)`, and private.
- **Red probes** (`redprobes.rs`, each `// GAP:`-annotated) — the *free-function / constructor* `calls` still resolve, but the **chained method call drops**: (1) trait-object `let s: Box<dyn Shape> = make_shape(); s.area()` (boxed type unknown); (2) generic `fn render<T: Shape>(t: T) { t.area() }` (type-param method unresolved); (3) `?`-unwrap `let c = load()?; c.area()` (the `?` result type is not tracked, though `load` resolves); (4) closure/iterator `vec![Circle::new(1.0)].iter().map(|x| x.area())` — `x.area()` drops, and the inline `Circle::new` *also* drops because it sits inside a `vec!` macro token-tree (unparsed by tree-sitter).

### Java (`javafamily/`)
- **Module-first FQN IDs**: no package node; each file is a module keyed by absolute path, every symbol ID is an FQN rooted at the in-file `package …;` (`com.aracne.shapes.Circle`). Method IDs **always** carry a parenthesized signature (`area()`), so overloads are distinct nodes (`Circle.area()` vs `Circle.area(int)`, the overload `calls` the no-arg sibling).
- **Interface** with **multiple** implementers (`shapes.Shape` ← `Circle` / `Rectangle`) and a class **implementing multiple** interfaces (`Widget` → `Named`, `Sized`), each interface gaining `implemented_by`.
- **Class extends class** is a **struct↔struct `inherits`** edge (`Derived → Base`, reverse `inherited_by`) — new vs the trait-only Rust model. **Interface extends interfaces** is iface↔iface `inherits` (`Describable → Named` + `Sized`).
- **Abstract** class + abstract method (`Base.rank`); a **sealed** interface with an explicit `permits` clause (`Expr permits Lit, Neg`); `final` classes (`Factory`, `Neg`).
- **Enum** (`enums.Op`): `IsEnum` + `Variants=[PLUS,MINUS]`, `implements Operation`, and a **constant body** `PLUS` that becomes its own struct `Op$PLUS` which **`inherits`** the enum and owns the overriding `apply`.
- **Record** (`records.Point`): `IsRecord` + `Components=[x,y]`, **synthesized accessor** methods `x()` / `y()` (Loc = record header line), a **compact constructor** (`Point.<init>(int,int)`), and `implements Located`.
- **Nested types**, arbitrarily deep + dotted: **static nested** (`Outer.Nested`), **inner** (`Outer.Inner`), **local** class in a method (`Outer$Helper`), **anonymous** class (`Outer$anon1`; `$anonN` numbered by deterministic source order within the top-level type). The `int` field `Inner` and the inner class `Inner` stay **distinct nodes** (expression vs type context).
- **Initializer blocks**: a static block → `Settings.<clinit>()` and all instance blocks merged into `Settings.<instance-init>()`, both `IsSynthetic`, each `calls` its helper.
- **`@interface`** annotation type (`annotations.Marker`) → a `ResourceInterface` with `IsAnnotation`; a class that **uses** `@Marker` (type/field/method) records a `uses_interface` edge (`Marked → Marker`).
- **Constructors** carry the `<init>(sig)` name; `this(…)` / `super(…)` chaining produces `calls` edges (`Derived.<init>() → Derived.<init>(int) → Base.<init>(String)`); a `new T(…)` records a `calls` edge to the ctor + `uses_struct`.
- **Signature normalization** (pure parse-time; return type never in the ID): type arguments stripped (`List<String>` → `List`), array dims kept (`int[]`), varargs normalized (`String…` → `String[]`). The erased-overload **collision** `pick(List<String>)` / `pick(List<Integer>)` → both `pick(List)` is a filed **red probe** (`redprobes.Overloaded`).
- **Cross-module return-type inference** (the flagship): `consumer.Consumer.total()` receives `Factory.makeCircle(…)` into a `Shape`-typed local and calls `.area()` → resolves `calls` to `factory.Factory.makeCircle(double)` + `shapes.Shape.area()`, with `uses_struct Factory` + `uses_interface Shape`. Cross-file `implements` / `extends` resolve regardless of which file owns the relation.
- **Lambdas + method/constructor references** (`lambdas.Events`): calls inside lambda bodies attribute to the **enclosing** method; `Circle::area` and `Circle::new` (→ `Circle.<init>(double)`) resolve as `calls`.
- **Exceptions** (`exceptions/`): a `throws` clause on an internal type, **try-with-resources** over a static-nested `Closeable` (`Handle`), and catch-wrap-rethrow → a `calls`/constructor edge to `CustomException.<init>(String,int)`; `extends Exception` is an **external** supertype (no internal node).
- **Imports**: an `import` of an internal type → **`imports_module`** (file→file); a JDK / guava import → **`imports_dependency`** (`java.util`, `com.google.common`) + a dependency node, with **no false internal edge** (`external.ExternalUser`).
- **Generics** (`generics.Box<T extends Shape>`): a bounded type parameter (`uses_interface Shape` via the bound), a generic **static** method, and a **wildcard** `List<? extends Shape>` parameter.
- **Red probes** (`redprobes/`, each `// bug _N`): generic **return-type propagation** drops the chained call (`Box<Rectangle>.get().area()` does not resolve `Rectangle.area()`); the **erased-overload collision** above collapses two methods into one ID. The free-function / constructor `calls` still resolve.
- **Determinism**: IDs are pure parse-time (no resolver dependency) and structural edges (`inherits`/`implements`) are rebuilt whole-graph from module-owned records, so full / incremental / hard scans yield identical resource sets and edges. Two same-named **local classes** in sibling method scopes are kept distinct (the second is suffixed `$Name#2`) rather than colliding.
- **Known coverage gaps** (deterministic, no DB-abort): anonymous classes declared in a **field initializer** (rather than a method / initializer-block body) are not extracted; bounded-type-parameter and generic-return chained-call resolution are filed red probes (`bug_1`/`bug_2`); cross-package same-simple-name overloads collapse to one ID (`bug_3`, by the documented signature contract).

## Edge-case probe suite (JS/TS deep dive)

A second, larger wave of fixtures stress-tests *interactions the topology should
arguably handle but frequently does not*. Each probe file is self-documenting —
its header comment states the EXPECTED behavior and the suspected gap. The
findings below were captured by writing each file (which auto-scans) and
inspecting the resulting graph with `read`. Legend: ✅ resolves, ❌ gap/bug, ⚠️ partial.

### The one rule that explains most method-call gaps
A method-call edge resolves **only when the receiver is a bare identifier (a
param or local `const`) carrying a direct class/interface type** — either a TS
annotation or a `const x = new C()` in the same body. Every other receiver shape
drops the chained method:

- ✅ `const c: Circle = makeCircle(r); c.area()` (`resolution.localChain`)
- ✅ `const c = new Circle(r); c.area()` (`factory.circleArea`)
- ✅ explicit `this: Circle` param → `this.area()` (`resolution.thisArea`)
- ❌ inline `new Circle(r).area()` — class+ctor resolve, `.area()` dropped (`calls.inlineArea`)
- ❌ inline `getCircle().area()` / `b.get().area()` — call resolves, chained method dropped (`resolution.viaReturn`, `generics_adv.unwrap`)
- ❌ untyped `this.method()` / `this.#priv()` (`calls.Helper.run`, `classfeatures.Featured.reveal`) — *filed*
- ❌ `super()` / `super.area()` (`calls.LoudCircle`, `classexpr.Decorated.area`)
- ❌ `(x as Circle).area()` cast (`resolution.castArea`)
- ❌ `c!.area()` non-null assertion — class resolves via the param type, method dropped (`resolution.bangArea`)
- ❌ union param `Circle | Rectangle` (`resolution.unionArea`); inconsistently, `Circle | number` *does* register the class (`overloads.area`)
- ❌ intersection / type-alias param (`type C = Circle`) resolves only to the alias, not through to Circle (`resolution.intersectionArea`, `resolution.aliasArea`)
- ❌ object-literal-method receiver (`objects.useOps`)

### Imports / exports
- ❌ **Re-export barrels** — `export {x} from`, `export * from`, `export * as ns from` dangle/drop; consumers through a barrel resolve nothing (`barrel.js`, `barrel_consumer.js`).
- ❌ **Dynamic `import()`** — not tracked as a dependency; member calls on the namespace don't resolve (`dynamic.js`).
- ❌ **`import x = require()`** (TS interop) — not handled (`importeq.viaEquals`).
- ❌ **`exports.x = function/arrow/class`** (CJS) — produces NO resource at all (`cjs_assign.cjs`).
- ❌ **`module.exports = require(...)`** whole-module re-export — dropped (`cjs_reexport.cjs`).
- ❌ **Anonymous `export default function/class`** — dropped entirely; no resource, no inherits (`anon_default_fn.js`, `anon_default_class.js`).
- ✅ **type-only imports** (`import type {Shape}`, inline `{type Box}`) still drive annotation resolution (`typeonly.describe`).
- ✅ **circular ES imports** resolve in both directions (`circular_a` ↔ `circular_b`).
- ⚠️ **export renaming** incl. `Widget as default` (`renames.js`).

### Classes
- ❌ **class expression to const** `const X = class …` → extracted as a **var**; methods + inheritance lost (`classexpr.Anon/Named`) — *filed*. (A function expression to const is handled correctly.)
- ❌ **mixin/HOF heritage** `extends Tagged(Timestamped(Circle))` → class kept, inherits edge dropped (`classexpr.Decorated`).
- ⚠️ **computed method name** `["dynamic"]()` is extracted but the ID keeps the raw bracket text (`classfeatures.Featured.["dynamic"]`).
- ✅ **modern fields** (instance/static/`#private`/static block) don't break extraction; `extends Circle` still resolves; `#private` methods are extracted (`classfeatures`).
- ✅ getter+setter on the same name now collapses to one merged accessor — the README's documented dup-ID abort no longer reproduces (`getset_bug.js`).
- ⚠️ nested function declarations are not their own resources; their body edges bubble up to the enclosing top-level function (`nested.outer`).

### TypeScript declaration merging (a new corruption class)
- ❌ **interface + interface** is last-writer-wins; earlier members are lost (`merge_iface.Box2` keeps only `height()`) — *filed*.
- ❌ **class + interface** drops the class and leaves an **orphaned method** (`merge_iface.Combo` becomes the interface; `Combo.run` survives parentless) — *filed*.
- ❌ **enum + namespace** drops the enum (`merge_ns.Mode`).
- ⚠️ **function + namespace** is benign — the function survives with its body (`merge_ns.widget`).
- ✅ **overload sets** (N signatures + 1 impl, same ID) collapse to one resource; the implementation body is retained, no abort (`overloads.area`, `overloads.Calc.add`).

### TypeScript types & namespaces
- ❌ **namespace-qualified types** `p: Geo.Point` don't resolve (`namespaces_adv.useQualified`); nested-namespace qualified calls `B.deep()` don't either.
- ❌ **generic return substitution** `Box<Circle>.get()` → `.area()` not modeled (`generics_adv.unwrap`).
- ✅ **return-type annotations are uses-edges** (`namespaces_adv.A.viaNested` shows a Circle use from `: Circle`).
- ✅ **multi-interface `implements`** (`Impl implements A, B`) creates `implemented_by` on each (`interfaces_adv`).
- ✅ **decorators** (class/method/property/parameter + factories) parse; a param decorator doesn't break param resolution; decorated method bodies keep their edges (`decorators.Service`).
- ✅ **enums** (const / string / heterogeneous) extract and are usable as types; member access is a use of the enum (`enums_adv`).
- ⚠️ **ambient**: `declare module "spec" { … }` members are extracted but with messy quoted IDs (`ambient_adv.d."virtual:shapes".VirtualCircle`); **`declare global { … }` contents are dropped**.
- ⚠️ keyof / indexed-access / mapped / conditional / `infer`, `satisfies`, `as const`, type predicates & assertion functions all parse without crashing (`generics_adv`, `predicates`).
- ⚠️ a TS module's whole-file `read` covers owned functions/classes but **not** its interfaces / enums / type-aliases (possible missing `has_interface` / `has_named_type` ownership edges).

### Probe files added
`jsfamily/`: `barrel.js`, `barrel_consumer.js`, `nested.js`, `objects.js`,
`calls.js`, `dynamic.js`, `cjs_assign.cjs`, `cjs_reexport.cjs`,
`anon_default_fn.js`, `anon_default_class.js`, `renames.js`, `classfeatures.js`,
`getset_bug.js`, `circular_a.js`, `circular_b.js`, `classexpr.js`.
`tsfamily/`: `overloads.ts`, `merge_iface.ts`, `merge_ns.ts`, `resolution.ts`,
`generics_adv.ts`, `typeonly.ts`, `enums_adv.ts`, `namespaces_adv.ts`,
`decorators.ts`, `interfaces_adv.ts`, `predicates.ts`, `ambient_adv.d.ts`,
`importeq.ts`.

## Bugs surfaced while building this

Building the corpus reproduced several real aracne defects (all filed via
`mcp__aracne__bug_report`). The first three share one failure mode: a **duplicate
resource ID → duplicate connection → `UNIQUE(source_id, conn_type, target_id)`
violation that aborts the entire file's topology write** (the file still lands on
disk, so disk and topology silently diverge, and later full rescans of that file
also fail).

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

3. **Go — two `init()` functions in the same file.** Minimal repro:
   ```go
   func init() {}
   func init() {}
   ```
   Both functions key to `<pkg>.init`, so the file emits a duplicate
   `has_function` connection and the scan write aborts. Go legally allows
   multiple `init()` per file/package, so `inits/inits.go` keeps a **single**
   `init()`; the duplicate case is exercised only as a filed-bug note (adding it
   to the corpus would abort every scan).

Beyond the write-abort class, the Go `dispatch/` and `generics/` packages also
surface **call-resolution gaps** (left as red probes with filed bugs): method
values/expressions, type-assertion/type-switch-bound method calls, and
generic-interface satisfaction matching are not yet resolved.

   > Update: the isolated probe `getset_bug.js` no longer reproduces this abort.
   > The accessor pair now collapses to a single merged `temp` resource (no
   > duplicate `has_method`, no `UNIQUE` violation). The other fixtures still
   > avoid the pattern for safety, but the abort itself appears fixed.

The edge-case probe suite surfaced four more (filed via `mcp__aracne__bug_report`):

3. **TS `class` + `interface` declaration merging → orphaned method.**
   `class Combo { run() }` followed by `interface Combo { extra() }` (same ID):
   the class is dropped (replaced by the interface) and `Combo.run` survives with
   no parent class. Filed on `merge_iface.Combo.run`. (`merge_iface.ts`)
4. **TS `interface` + `interface` merging is last-writer-wins.** Two
   `interface Box2` declarations: only the last's members survive. Filed on
   `merge_iface.Box2`. (`merge_iface.ts`)
5. **Class expression bound to a `const` is mis-kinded as a variable** — its
   methods and inheritance are dropped, whereas a function expression bound to a
   const is handled. Filed on `classexpr.Anon`. (`classexpr.js`)
6. **Untyped intra-class `this.method()` calls are not resolved**, leaving the OO
   call graph incomplete (an explicit `this:` parameter type *does* resolve).
   Filed on `calls.Helper.run`. (`calls.js`)

## Edge-case round 2 — JS/TS deep gaps

A second, larger sweep added ~26 edge-case fixtures to `jsfamily/` and
`tsfamily/` that probe what the scanner **should** do (not just what it can), and
locked the behavior in with two corpus-scan Go suites:

- `tests/jsfamily_edgecases_test.go` → `TestJSFamilyEdgecases`
- `tests/tsfamily_edgecases_test.go` → `TestTSFamilyEdgecases`

Both scan their family **in-memory** (`jsscanner.New{JavaScript,TypeScript}Scanner().Scan`)
so no SQLite write happens and the pre-existing multi-language scan-write abort
cannot interfere. Following the Python suite convention
(`tests/python_edgecases_test.go`), subtests are **mixed and deliberately left
red**: `// OK` subtests lock in working behavior (green); `// GAP (bug …_NN)`
subtests assert the ideal and **stay red** until the scanner is fixed (each maps
to a filed `bug_report`, referenced by the `_NN` suffix of its ID). Tally: JS
**12 green / 15 red**, TS **15 green / 11 red**.

> Needs `CGO_ENABLED=1` + gcc (tree-sitter). Run a single family with
> `CGO_ENABLED=1 go test ./tests/ -run TestJSFamilyEdgecases -v`.

### New fixtures

**`jsfamily/`** — `barrel.js`+`barrel_consumer.js` (re-export chains:
`export {X} from`, `{X as Y} from`, `{default} from`, `export *`, `export * as`),
`nested.js` (nested fns, closures, IIFE, top-level `new`), `objects.js`
(object-literal methods, revealing module), `dynamic.js` (`import()`,
`import.meta`), `calls.js` (`this`/`super`/optional-chain/inline-new-chain/
by-reference/default-param calls), `anon_default_fn.js`, `anon_default_class.js`
(anonymous default exports), `renames.js` (`export { a as b, c as default }`),
`cjs_assign.cjs` (`exports.x = arrow|fn|class`), `cjs_reexport.cjs`
(`module.exports = require(...)`), `classfeatures.js` (fields, `#private`,
`static {}`, computed names, `async *`), `getset_bug.js` (same-name accessor
abort probe), `circular_a.js`+`circular_b.js` (import cycle), `classexpr.js`
(class expressions + mixin chain).

**`tsfamily/`** — `overloads.ts` (fn/method overloads), `merge_iface.ts`
(interface+interface, class+interface merging), `merge_ns.ts` (fn/enum/class +
namespace merging), `generics_adv.ts` (constraints, `Box<Circle>.get().area()`,
keyof/mapped/conditional/infer), `typeonly.ts` (`import type`, inline `type`,
`export type … from`), `resolution.ts` (return-chain, union, intersection, `as`,
`!`, type-alias, `this`-param), `enums_adv.ts` (const/string/heterogeneous),
`namespaces_adv.ts` (nested + qualified type), `decorators.ts` (class/method/
property/parameter decorators), `interfaces_adv.ts` (index/call/construct
signatures, multi-implements/extends), `ambient_adv.d.ts` (`declare module` +
`declare global`), `predicates.ts` (type guard, `asserts`, `satisfies`,
`as const`), `importeq.ts` (`import x = require(...)`).

### Confirmed gaps (red tests → filed bugs)

| # | Gap (SHOULD) | Fixture |
|---|---|---|
| _5  | re-export chain forwards the binding | barrel.js |
| _6  | nested function is its own resource | nested.js |
| _7  | object-literal method is a resource | objects.js |
| _8  | dynamic `import()` registers dep + resolves member | dynamic.js |
| _10 | `super.method()` / `super()` resolves to parent | calls.js |
| _11 | inline `new X().m()` resolves the chained method | calls.js |
| _12 | default-param initializer call is tracked | calls.js |
| _13 | anonymous `export default fn/class` extracted | anon_default_*.js |
| _14 | `exports.x = arrow\|fn\|class` extracted | cjs_assign.cjs |
| _15 | class **expression** extracted as a class (now: a `variable`) | classexpr.js |
| _16 | mixin/call-expr base resolves inheritance | classexpr.js |
| _17 | computed method name → clean ID (now `Class.["x"]`) | classfeatures.js |
| _18 | `module.exports = require(...)` re-exports the module | cjs_reexport.cjs |
| _19 | interface+interface merge unions members | merge_iface.ts |
| _20 | class+interface merge keeps the class (interface overwrites it) | merge_iface.ts |
| _21 | inline `f().m()` follows the return type | resolution.ts |
| _22 | type-alias of a class is followed | resolution.ts |
| _23 | union-typed param resolves a member (also after narrowing) | resolution.ts / predicates.ts |
| _24 | `(x as T).m()` resolves | resolution.ts |
| _25 | generic return type (`Box<Circle>.get()`) propagated | generics_adv.ts |
| _26 | constrained generic type-param method resolves | generics_adv.ts (no test) |
| _27 | namespace-qualified type annotation resolves | namespaces_adv.ts |
| _28 | nested-namespace qualified call resolves | namespaces_adv.ts |
| _29 | `import x = require(...)` binding tracked | importeq.ts |
| _30 | `declare global` members extracted | ambient_adv.d.ts |

> **bug `_9` is a FALSE POSITIVE** (intra-class `this.method()`): it was filed
> from an MCP `read` whose CONTEXT render simply omits same-class
> sibling-method edges. A full scan *does* resolve `Helper.run -> Helper.compute`
> (green lock-in `this_call_resolves_sibling_method`). It should be dismissed.

### Behavior that already works (green lock-ins)

JS: re-named exports extract their decls; **circular imports** resolve both ways;
function-expression-as-const; nested body's `new` folds into the enclosing fn;
`new X()` + intermediate variable resolves the method; private `#methods` and
`async *` generators are extracted; **a same-name getter+setter now yields one
accessor with no write-abort** (the round-1 abort bug is fixed).

TS: function/method **overloads collapse** to one resource (no abort);
function/class **survive namespace merges**; **type-only imports drive
resolution**; class/method/**parameter** decorators don't block extraction or
param-type resolution; **class implements multiple** interfaces; **interface
extends multiple**; string enums and `declare module` exports are extracted;
generic instantiation resolves the class + the annotated `.get()`.

## How to verify

Topology (preferred — these were used to validate every family):

```
mcp__aracne__read            aracne/testing_ground/go/shapes.Shape          # multi-impl
mcp__aracne__read            aracne/testing_ground/go/consumer.Report       # cross-pkg inference
mcp__aracne__read            aracne.testing_ground.python.from_factory      # factory chaining
mcp__aracne__read            aracne/testing_ground/jsfamily/consumer.report # aliased/namespace imports
mcp__aracne__read            aracne/testing_ground/tsfamily/models.Drawable # single-impl
mcp__aracne__read            aracne/testing_ground/tsfamily/factory.areaOf  # annotation-driven
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
| Rust | `rustfamily::<mod>::<Name>` | same | `rustfamily::<mod>::<Type>::<method>` |
| Java | `com.aracne.<pkg>.<Class>` (nested `Outer.Inner`; synthetic `$anonN`/`$Local`/`Enum$CONST`) | — (no free functions) | `com.aracne.<pkg>.<Owner>.<name>(<sig>)` (ctor `<Owner>.<init>(<sig>)`; init `<Owner>.<clinit>()`/`<instance-init>()`; record accessor `<Rec>.<comp>()`) |

package prompts

import "strings"

// The per-language halves of the contract.
//
// WHERE THIS TEXT CAME FROM. Until this file existed there were two contracts. One lived in
// contract.go and was written into CLAUDE.md and AGENTS.md; the other was five
// `<lang>SpecificPrompt` constants under internal/llm/languages, sent by aracne's own harness
// instead. They described the same graph, the same read output and the same discipline, so
// every change to what a read RETURNS had to be made twice -- and the copy nobody was reading
// was the one that went stale. There is one contract now, and this is the part of it that
// genuinely differs between languages.
//
// WHAT BELONGS HERE, AND WHAT DOES NOT. Only what changes when the topology's language changes:
// what the graph models, how an ID is spelled, what a read's code block and `# CONTEXT:` look
// like, and the semantics an agent has to know to read the edges correctly. Anything true in
// every language belongs in contract.go, where it is written once.
//
// WHY THE FIELDS ARE SEPARATE AND NOT ONE BLOB. Because the mode decides which of them may be
// rendered at all. ModeInterceptLineRanges deliberately stopped teaching resource IDs -- the
// model used one 0 times in 408 commands -- so its contract may carry Graph and Semantics but
// never IDs, and its `# CONTEXT:` entries are keyed by span rather than by ID, which makes the
// ID-keyed Output example wrong there too. One blob would have had no way to leave those out.

// languageProfile is the language-varying half of a high-verbosity contract.
type languageProfile struct {
	// Display is the language's name as prose uses it ("Go", "TypeScript").
	Display string
	// Graph completes "a graph model of ..." -- what this language's scanner actually
	// indexes. Naming the kinds is what stops an agent asking for something the graph has
	// never held (a Java package node, a Go generic instantiation).
	Graph string
	// IDs teaches how a declaration is addressed. Rendered only where the mode advertises
	// IDs at all; see modeAdvertisesIDs.
	IDs string
	// Output is the shape a read comes back in: one fenced block per file, then the shared
	// `# CONTEXT:` section. Rendered only where those context entries really are keyed by
	// ID -- in ModeInterceptLineRanges they are keyed by `path:start-end` and the shared
	// span-keyed example is used instead.
	Output string
	// Examples are two IDs in this language, spelled exactly as one would be typed. They
	// stand in a command line in the contract, so a project never sees another language's
	// ID form demonstrated as if it were its own.
	Examples [2]string
	// Semantics is what an agent must know to read this language's edges correctly:
	// what implements what, how visibility works, and how call/usage edges were resolved.
	// It is the one field that is never mode-gated, because none of it is vocabulary the
	// model has to type.
	Semantics string
}

// languageProfiles is keyed by the topology's language string, as the scanners spell it.
var languageProfiles = map[string]languageProfile{
	"go": {
		Display: "Go",
		Graph:   "packages, files, functions, methods, structs, interfaces and package-level variables",
		IDs: "Go IDs are module-path-prefixed and dotted by package: " +
			"`github.com/org/proj/internal/topology.Manager` for a type, " +
			"`github.com/org/proj/internal/cli.RunGuard` for a function, " +
			"`pkg.(Type).Method` for a method. A unique trailing part is enough " +
			"(`RunGuard`, `TopologyManager`), and a miss returns the nearest candidates " +
			"rather than an error.",
		Output: "```go\n" +
			"import (...)\n" +
			"type ParentStruct struct {...}\n" +
			"func (p *ParentStruct) Method(...) (...) {...}\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## pkg.InterfaceName: Description\n" +
			"    pkg.ImplStruct: Description\n" +
			"        pkg.(ImplStruct).Method: Description\n" +
			"## pkg.StructName: Description\n" +
			"    pkg.(StructName).Method: Description\n" +
			"## pkg.CalledFunc: Description\n" +
			"## pkg.ExtVarName = value\n",
		Examples: [2]string{"internal/cli.RunGuard", "internal/topology.Manager"},
		Semantics: "A method comes back with its receiver type when that type is small enough " +
			"to inline. An interface lists the structs that satisfy it and their methods; a " +
			"struct lists its own. Visibility follows Go's rule -- an exported name starts " +
			"with a capital. Call and usage edges are resolved from direct calls, imports, " +
			"type declarations and composite literals.",
	},
	"python": {
		Display: "Python",
		Graph:   "packages, modules, functions, classes, methods and module-level variables",
		IDs: "Function, method and class IDs all resolve: `parse_file`, " +
			"`TopologyManager.__init__`, `flask.app.Flask`, " +
			"`src/flask/app.Flask.register_blueprint`. A unique trailing part is enough " +
			"(`Flask.register_blueprint`), and a miss returns the nearest candidates rather " +
			"than an error.",
		Output: "```python\n" +
			"import pkg  # internal\n" +
			"import dep  # external\n\n" +
			"class ParentClass: ...\n" +
			"    def method(self, ...): ...\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## pkg.BaseClass (base class): Description [NEED TO IMPLEMENT: method_name]\n" +
			"## pkg.ClassName: Description\n" +
			"    pkg.ClassName.method: Description\n" +
			"## pkg.CalledFunc: Description\n" +
			"## pkg.VarName = value\n",
		Examples: [2]string{"src/flask/app.Flask.register_blueprint", "parse_file"},
		Semantics: "Classes are the primary unit of organization. ABC and Protocol classes " +
			"define contracts, and a class read lists what it still has to implement. " +
			"`__init__` is the constructor; methods carry `self`/`cls`. Decorators modify " +
			"behaviour and are shown with the declaration they wrap.",
	},
	"javascript": {
		Display: "JavaScript",
		Graph:   "packages, modules (files), functions, classes, methods and module-level variables",
		IDs: "Top-level functions, arrow functions assigned to a binding, classes and class " +
			"methods all resolve from their ID (`parseFile`, `Component`, " +
			"`Component.render`). IDs are file-scoped, so the same name can legitimately " +
			"appear in several modules; a miss returns the nearest candidates rather than an " +
			"error.",
		Output: "```javascript\n" +
			"// import from ./helpers (internal)\n" +
			"// import from react (external)\n\n" +
			"class ParentClass { ... }\n\n" +
			"function example(...) { ... }\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## BaseClass (base class): Description\n" +
			"## ClassName: Description\n" +
			"    ClassName.method: Description\n" +
			"## calledFunc: Description\n" +
			"## VarName = value\n",
		Examples: [2]string{"parseFile", "Component.render"},
		Semantics: "Each file is its own module scope, and modules expose symbols via ESM " +
			"(import/export) or CommonJS (require/module.exports). Classes use `extends` for " +
			"inheritance and `constructor` for the constructor. JavaScript is untyped, so " +
			"call and usage edges are best-effort: they come from direct calls, imported " +
			"bindings and `new ClassName()` instantiations, never from type annotations.",
	},
	"typescript": {
		Display: "TypeScript",
		Graph: "packages, modules (files), functions, classes, interfaces, type aliases, enums " +
			"and module-level variables",
		IDs: "Functions (top-level, arrow and class methods), classes, interfaces, type " +
			"aliases and enums all resolve from their ID. IDs are file-scoped, so the same " +
			"name can legitimately appear in several modules; a miss returns the nearest " +
			"candidates rather than an error.",
		Output: "```typescript\n" +
			"import { Service } from './service';\n\n" +
			"export class Handler implements Contract {\n" +
			"    handle(req: Request): Response { ... }\n" +
			"}\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## Contract (interface): Description\n" +
			"    Handler: Description\n" +
			"## Service: Description\n" +
			"    Service.method: Description\n" +
			"## calledFunc: Description\n" +
			"## VarName = value\n",
		Examples: [2]string{"Handler.handle", "Service"},
		Semantics: "Each file is its own module scope, and modules expose symbols via ESM " +
			"(import/export) or CommonJS (require/module.exports). Interfaces define " +
			"contracts -- a class `implements` them, an interface can `extend` others -- and " +
			"a read of an interface lists its implementing classes, while a read of a type " +
			"alias lists its usages. Because TypeScript is typed, call and usage edges are " +
			"also resolved from type annotations: a parameter `s: Service` is what lets " +
			"`s.method()` resolve.",
	},
	"rust": {
		Display: "Rust",
		Graph: "modules (files), functions, methods, structs, enums, unions, traits, type " +
			"aliases and module-level consts/statics",
		IDs: "Rust IDs are `::`-qualified paths rooted at the crate name: " +
			"`mycrate::shapes::Circle` for a type, `mycrate::shapes::Circle::area` for a " +
			"method, `mycrate::factory::make_circle` for a free function. Free functions, " +
			"impl methods, associated functions, macros, structs/enums/unions, traits and " +
			"type aliases all resolve; a unique trailing part is enough (`Circle::area`), " +
			"and a miss returns the nearest candidates rather than an error.",
		Output: "```rust\n" +
			"// use serde (external crate)\n\n" +
			"impl Circle {\n" +
			"    fn area(&self) -> f64 { ... }\n" +
			"}\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## mycrate::shapes::Circle: Description\n" +
			"    mycrate::shapes::Circle::new (constructor): Description\n" +
			"## mycrate::shapes::Shape (trait): Description\n" +
			"## mycrate::factory::make_circle: Description\n",
		Examples: [2]string{"mycrate::shapes::Circle::area", "mycrate::factory::make_circle"},
		Semantics: "Each file is a module. Structs, enums and unions are all modeled as the " +
			"struct kind, and methods and associated functions attach to a type through its " +
			"impl blocks. Traits are the interface kind: a struct `implements` a trait, and a " +
			"trait inherits supertraits through its bounds (`trait A: B`). `type` aliases are " +
			"named types. Visibility follows Rust's rules (pub, pub(crate), pub(super), " +
			"private). Call and usage edges are resolved from direct calls, `use` imports, " +
			"type annotations and constructor/return-type inference.",
	},
	"java": {
		Display: "Java",
		Graph: "files, classes, enums, records, abstract classes, interfaces, annotation types, " +
			"methods, constructors, accessors and initializer blocks",
		IDs: "Java IDs are fully-qualified names rooted at the file's `package ...;` " +
			"declaration and dotted by nesting (`com.aracne.shapes.Circle`, " +
			"`com.aracne.nested.Outer.Inner`). A method or constructor ID ALWAYS carries a " +
			"parenthesized parameter signature so overloads stay distinct: " +
			"`com.aracne.shapes.Circle.area(int)`, `com.aracne.shapes.Circle.<init>(double)`, " +
			"and even a no-arg method ends in `()`. The signature comma-joins each " +
			"parameter's type with type arguments stripped (`List<String>` -> `List`), array " +
			"dimensions kept (`int[]`) and varargs normalized (`String...` -> `String[]`); " +
			"the return type is never part of the ID.",
		Output: "```java\n" +
			"class Circle implements Shape {\n" +
			"    public double area(int scale) { ... }\n" +
			"}\n" +
			"```\n\n" +
			"# CONTEXT:\n" +
			"## com.aracne.shapes.Circle: Description\n" +
			"    com.aracne.shapes.Circle.<init>(double) (constructor): Description\n" +
			"## com.aracne.shapes.Shape (interface): Description\n" +
			"## com.aracne.factory.Factory.makeCircle(double): Description\n",
		Examples: [2]string{"com.aracne.shapes.Circle", "com.aracne.shapes.Circle.area(int)"},
		Semantics: "Classes, enums, records and abstract/anonymous/local classes are all " +
			"modeled as the struct kind (enums carry Variants, records carry Components); " +
			"interfaces and annotation types (`@interface`) are the interface kind. A class " +
			"`implements` an interface, a class `extends` another class and an interface " +
			"`extends` other interfaces, both through `inherits` edges. Synthetic names use " +
			"`< > $` characters that never appear in a real FQN: `<init>` for constructors, " +
			"`<clinit>` for static initializers, `<instance-init>` for merged instance " +
			"initializers, `Enclosing$anon1` for anonymous classes and `Outer$Helper` for " +
			"local ones. Fields are stored inline on their owning class, so there are NO " +
			"separate variable resources and no type-alias or package nodes. Visibility " +
			"follows Java's rules (public, protected, package-private, private).",
	},
}

// profilesFor resolves the topology's languages to their profiles, in the order given, keeping
// only the ones aracne has a scanner (and therefore a profile) for and dropping duplicates.
//
// An unknown or empty list is not an error and must not be: `arac setup` can legitimately run
// before the first scan, and a project may be written in something no scanner reads yet. The
// caller renders the language-free contract in that case -- everything in it that is true of
// every language is still true.
func profilesFor(languages []string) []languageProfile {
	seen := map[string]bool{}
	out := make([]languageProfile, 0, len(languages))
	for _, lang := range languages {
		key := strings.ToLower(strings.TrimSpace(lang))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if p, ok := languageProfiles[key]; ok {
			out = append(out, p)
		}
	}
	return out
}

// languageNames joins the profiles' display names the way prose does: "Go", "Go and Python",
// "Go, Python and Rust".
func languageNames(profiles []languageProfile) string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Display)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

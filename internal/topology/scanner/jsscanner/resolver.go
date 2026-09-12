package jsscanner

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// exportRef points an imported name at the topology resource it resolves to.
type exportRef struct {
	Kind js.ConnectionKind // ConnCalls (function), ConnUsesClass (class), ConnUsesExtVar (variable)
	ID   string
}

// Checks whether an ID exists in the JavaScript topology's functions map.
func isFunc(gt *js.JavaScriptTopology, id string) bool {
	_, ok := gt.Functions[js.FunctionID(id)]
	return ok
}

// Checks whether an ID exists in the JavaScript topology's classes map.
func isClass(gt *js.JavaScriptTopology, id string) bool {
	_, ok := gt.Classes[js.ClassID(id)]
	return ok
}

// Checks if an identifier is registered as an external variable
func isVar(gt *js.JavaScriptTopology, id string) bool {
	_, ok := gt.ExternalVars[js.ExternalVarID(id)]
	return ok
}

// tsSourceExts maps the JavaScript extension a TypeScript ESM import is written
// with to the TypeScript source extensions it names. Under NodeNext/Node16
// resolution `./shape.js` is the required spelling for the file `shape.ts`.
var tsSourceExts = map[string][]string{
	".js":  {".ts", ".tsx"},
	".jsx": {".tsx", ".ts"},
	".mjs": {".mts"},
	".cjs": {".cts"},
}

// resolveSpecifier resolves a relative import specifier to the absolute path of
// a module already present in the topology, honouring implicit extensions and
// directory index files.
func resolveSpecifier(importerFile, source string, gt *js.JavaScriptTopology) (string, bool) {
	dir := filepath.Dir(importerFile)
	base := filepath.Clean(filepath.Join(dir, source))
	if filepath.IsAbs(source) { // a tsconfig/jsconfig alias, already mapped; see internalSpecifier
		base = filepath.Clean(source)
	}
	if _, ok := gt.Modules[base]; ok {
		return base, true
	}
	// A `.js`-family specifier with no such file names its TypeScript source
	// (`./shape.js` -> shape.ts, `./dir/index.js` -> dir/index.ts). Only a TS
	// topology holds those modules, so a JavaScript project is unaffected.
	ext := filepath.Ext(base)
	for _, e := range tsSourceExts[ext] {
		cand := strings.TrimSuffix(base, ext) + e
		if _, ok := gt.Modules[cand]; ok {
			return cand, true
		}
	}
	// TypeScript extensions first (a `.ts` importer resolving `./x` prefers x.ts), then
	// JavaScript. A given import resolves to whichever module actually exists.
	exts := []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}
	for _, e := range exts {
		if _, ok := gt.Modules[base+e]; ok {
			return base + e, true
		}
	}
	for _, e := range exts {
		cand := filepath.Join(base, "index"+e)
		if _, ok := gt.Modules[cand]; ok {
			return cand, true
		}
	}
	return "", false
}

// resolveExport resolves an imported symbol from a target module to a topology
// resource. Named symbols are resolved by existence (any importable top-level
// symbol shares the module's ID namespace); default imports use the module's
// recorded DefaultExport. This is topology-driven so Scan and UpdateFile resolve
// identically without needing per-file export tables.
func resolveExport(gt *js.JavaScriptTopology, abs, importedName string) (exportRef, bool) {
	return resolveExportWith(gt, abs, importedName, classifyValue, nil)
}

// classifyValue answers what a value reference (a call, a `new`, a read) finds at id.
func classifyValue(gt *js.JavaScriptTopology, id string) (exportRef, bool) {
	switch {
	case isFunc(gt, id):
		return exportRef{Kind: js.ConnCalls, ID: id}, true
	case isClass(gt, id):
		return exportRef{Kind: js.ConnUsesClass, ID: id}, true
	case isVar(gt, id):
		return exportRef{Kind: js.ConnUsesExtVar, ID: id}, true
	}
	return exportRef{}, false
}

// classifyType answers what a type reference (an annotation) finds at id.
func classifyType(gt *js.JavaScriptTopology, id string) (exportRef, bool) {
	if isClass(gt, id) {
		return exportRef{Kind: js.ConnUsesClass, ID: id}, true
	}
	if _, ok := gt.Interfaces[js.InterfaceID(id)]; ok {
		return exportRef{Kind: js.ConnUsesInterface, ID: id}, true
	}
	if _, ok := gt.NamedTypes[js.NamedTypeID(id)]; ok {
		return exportRef{Kind: js.ConnUsesNamedType, ID: id}, true
	}
	return exportRef{}, false
}

// resolveExportWith resolves importedName exported by the module at abs, following
// re-exports, and asks classify what the symbol it lands on is. The visited set lets a
// whole-module re-export chain (`module.exports = require('./x')`) be followed without
// looping on circular re-exports. A name not defined locally falls through to its named
// re-export, then to each re_exports_module target's namespace.
func resolveExportWith(gt *js.JavaScriptTopology, abs, importedName string, classify func(*js.JavaScriptTopology, string) (exportRef, bool), visited map[string]bool) (exportRef, bool) {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[abs] {
		return exportRef{}, false
	}
	visited[abs] = true

	modulePath := moduleKey(gt, abs)
	name := importedName
	if name == "default" {
		if mod, ok := gt.Modules[abs]; ok && mod.DefaultExport != "" {
			name = mod.DefaultExport
		}
	}
	if ref, ok := classify(gt, modulePath+"."+name); ok {
		return ref, true
	}
	mod, ok := gt.Modules[abs]
	if !ok {
		return exportRef{}, false
	}
	// Named re-export: `export {Orig as importedName} from "./src"` forwards the
	// requested name to a (possibly differently named) symbol in another module --
	// or, for `export {impl as importedName}`, in this one.
	if t, ok := mod.ReExportsNamed[importedName]; ok {
		target := string(t.Module)
		if target == abs {
			if ref, ok := classify(gt, modulePath+"."+t.Name); ok {
				return ref, true
			}
		} else if ref, ok := resolveExportWith(gt, target, t.Name, classify, visited); ok {
			return ref, true
		}
	}
	// A member reached through a binding arrives here as a dotted name ("Geo.dist", "ns.f"):
	// only the HEAD is exported by this module, and the rest is a name inside whatever the
	// head stands for. A namespace re-export (`export * as ns from "./lib"`) stands for the
	// module, so the tail is resolved there directly; an ordinary named re-export stands for
	// a symbol, so the tail hangs off that symbol's own name.
	if head, tail, dotted := strings.Cut(importedName, "."); dotted {
		if t, ok := mod.ReExportsNamed[head]; ok {
			name := tail
			if t.Name != wholeModuleReExport {
				name = t.Name + "." + tail
			}
			if ref, ok := resolveExportWith(gt, string(t.Module), name, classify, visited); ok {
				return ref, true
			}
		}
	}
	// Whole-module re-export fallback: resolve the (still original) imported name
	// against each re-exported module's namespace.
	for _, target := range mod.Connections[js.ConnReExportsModule] {
		if ref, ok := resolveExportWith(gt, target, importedName, classify, visited); ok {
			return ref, true
		}
	}
	return exportRef{}, false
}

// bindingExportNames lists the export names an imported binding may stand for
// when it is called or constructed, in the order to try them. A whole-module
// binding (`const W = require('./widget')`) is the module's value itself, so
// `W()` / `new W()` target its default export (`module.exports = class Widget`)
// first; the binding's own name is kept as the fallback it has always been.
func bindingExportNames(info importInfo, local string) []string {
	if info.ImportedName != "" {
		return []string{info.ImportedName}
	}
	if info.Namespace {
		return []string{"default", local}
	}
	return []string{local}
}

// resolveModuleReExports resolves a file's whole-module re-export specifiers
// (CommonJS `module.exports = require('./x')`) to the module (file) IDs they
// re-export, deduped. These become re_exports_module edges that let a consumer
// resolve a name through the re-exporting module to the source module.
func resolveModuleReExports(pr *ParseResult, gt *js.JavaScriptTopology) []js.ModuleID {
	var mods []js.ModuleID
	seen := make(map[js.ModuleID]bool)
	for _, source := range pr.ReExportAll {
		abs, ok := resolveSpecifier(pr.FileID, source, gt)
		if !ok {
			continue
		}
		mid := js.ModuleID(abs)
		if mid != "" && !seen[mid] {
			seen[mid] = true
			mods = append(mods, mid)
		}
	}
	return mods
}

// resolveNamedReExports resolves a file's named ESM re-exports (`export {Orig as
// Exported} from "./src"`) to a table mapping each exported name to the source
// module ID + original symbol name. Stored on the module so a consumer importing
// the exported name resolves through to the source symbol (with name translation
// the whole-module re-export edge can't express).
func resolveNamedReExports(pr *ParseResult, gt *js.JavaScriptTopology) map[string]js.ReExportTarget {
	if len(pr.ReExportNamed) == 0 {
		return nil
	}
	out := make(map[string]js.ReExportTarget)
	for _, re := range pr.ReExportNamed {
		abs := pr.FileID // no source: a local export alias (`export {impl as renamed}`)
		if re.Source != "" {
			var ok bool
			if abs, ok = resolveSpecifier(pr.FileID, re.Source, gt); !ok {
				continue
			}
		}
		out[re.Exported] = js.ReExportTarget{Module: js.ModuleID(abs), Name: re.Original}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveModuleImports resolves a file's internal import specifiers to the
// specific module (file) IDs they pull in, deduped. These become module->module
// import edges surfaced in the "Packages & Modules" viz.
func resolveModuleImports(pr *ParseResult, gt *js.JavaScriptTopology) []js.ModuleID {
	var mods []js.ModuleID
	seen := make(map[js.ModuleID]bool)
	for _, source := range pr.InternalImports {
		abs, ok := resolveSpecifier(pr.FileID, source, gt)
		if !ok {
			continue
		}
		mid := js.ModuleID(abs)
		if mid != "" && !seen[mid] {
			seen[mid] = true
			mods = append(mods, mid)
		}
	}
	return mods
}

// Extracts type annotations, instance creations, and method/function calls from a function body to build connection edges.
func analyzeFunctionBody(body *jsFunc, pr *ParseResult, gt *js.JavaScriptTopology, receiverClass *js.ClassID, fnScope string) map[js.ConnectionKind][]string {
	conn := make(map[js.ConnectionKind][]string)
	if body == nil {
		return conn
	}

	// current is the call being resolved, or nil outside the call loop.
	var current *jsBodyCall
	var callSites []string
	add := func(kind js.ConnectionKind, id string) {
		if id == "" {
			return
		}
		// Recorded BEFORE the edge dedupe below returns: two calls to the same callee are
		// one edge but two call sites, and a function called once correctly and once
		// wrongly must still report the wrong one.
		if kind == js.ConnCalls && current != nil {
			rec := contract.EncodeCallSite(jsCallSite(id, current))
			if rec != "" && !containsJSRec(callSites, rec) {
				callSites = append(callSites, rec)
			}
		}
		for _, existing := range conn[kind] {
			if existing == id {
				return
			}
		}
		conn[kind] = append(conn[kind], id)
	}

	varTypeMap := make(map[string]js.ClassID)
	// varTypeArg records a variable's generic type argument (the resolved class
	// inside `Box<Circle>`), so a method whose return type is the class's type
	// parameter (`Box.get(): T`) can be propagated to that argument.
	varTypeArg := make(map[string]js.ClassID)

	// TypeScript type annotations (params, declared local vars, return type). Each resolves
	// to a class (→ enables method-call resolution), an interface, or a named type, and the
	// corresponding usage edge is recorded. This is what JS couldn't do (it has no types).
	registerType := func(varName, typeName string) {
		kind, id, isClass := resolveTypeName(typeName, fnScope, pr, gt)
		if id == "" {
			return
		}
		add(kind, id)
		if varName == "" {
			return
		}
		if isClass {
			varTypeMap[varName] = js.ClassID(id)
		} else if kind == js.ConnUsesNamedType {
			// Type-alias indirection: a param typed as `type C = Circle` records a
			// uses_named_type edge to C, but method calls still need the underlying
			// class. Follow the alias through to Circle so `c.area()` resolves.
			if cid, ok := resolveAliasedClass(id, pr, gt); ok {
				varTypeMap[varName] = cid
			}
		}
	}
	for _, p := range body.Params {
		registerType(p.Name, p.Typing)
	}
	for _, o := range body.Output {
		registerType("", o.Typing)
	}
	for _, a := range body.BodyAssigns {
		if a.DeclaredType != "" {
			registerType(a.Name, a.DeclaredType)
		}
		// Generic instantiation: record the type argument of `const b: Box<Circle>`
		// so a chained call into a method returning the type parameter resolves.
		if a.Name != "" && a.DeclaredTypeArg != "" {
			if _, id, isClass := resolveTypeName(a.DeclaredTypeArg, fnScope, pr, gt); isClass && id != "" {
				varTypeArg[a.Name] = js.ClassID(id)
			}
		}
	}

	// Instances learned from `const x = new Foo()`.
	for _, a := range body.BodyAssigns {
		if a.NewClass == "" || a.Name == "" {
			continue
		}
		if cid, ok := resolveClassName(a.NewClass, fnScope, pr, gt); ok {
			varTypeMap[a.Name] = cid
		}
	}

	// Instances learned by following a function's return type: `const x = f()` types x as the
	// class f returns (its Output[0].TypingID, resolved in f's defining file), and `const x = y`
	// aliases an already-typed variable. Both then drive method-call resolution via varTypeMap.
	for _, a := range body.BodyAssigns {
		if a.Name == "" {
			continue
		}
		switch {
		case a.CallFunc != "":
			if fid, ok := resolveFunctionID(a.CallFunc, fnScope, pr, gt); ok {
				if f, ok := gt.Functions[fid]; ok && len(f.Output) > 0 {
					if cid := js.ClassID(f.Output[0].TypingID); cid != "" {
						if _, ok := gt.Classes[cid]; ok {
							varTypeMap[a.Name] = cid
						}
					}
				}
			}
		case a.AliasOf != "":
			if cid, ok := varTypeMap[a.AliasOf]; ok {
				varTypeMap[a.Name] = cid
			}
		}
	}

	for ci := range body.BodyCalls {
		call := body.BodyCalls[ci]
		current = &body.BodyCalls[ci]
		switch {
		case call.IsNew:
			cid, ok := resolveClassName(call.Func, fnScope, pr, gt)
			if !ok && call.ObjectName != "" {
				cid, ok = resolveNamespaceClass(call.ObjectName, call.Func, pr, gt)
			}
			if ok {
				add(js.ConnUsesClass, string(cid))
				if c, ok := gt.Classes[cid]; ok && c.Constructor != nil {
					add(js.ConnCalls, string(*c.Constructor))
				}
			} else if dep, ok := externalBinding(pr, call.Func, call.ObjectName); ok {
				// `new Client()` / `new pkg.Client()` of a package import.
				add(js.ConnUsesDep, dep)
			}

		case call.ObjectName == "" || call.MethodName == "":
			resolveDirectCall(call.Func, fnScope, pr, gt, add)

		default:
			resolveMethodCall(call, pr, gt, varTypeMap, varTypeArg, receiverClass, fnScope, add)
		}
	}
	current = nil

	// Sorted: the at-scale suite compares connection sets across scan modes byte-for-byte.
	if len(callSites) > 0 {
		sort.Strings(callSites)
		conn[js.ConnectionKind(contract.CallSitesConn)] = callSites
	}

	return conn
}

// jsCallSite reads the argument shape of one JavaScript or TypeScript call.
//
// Recorded for both, judged only for TypeScript. JavaScript arity is not binding --
// f(1) against function f(a, b) is legal and leaves b undefined -- so a mismatch there
// would be a style opinion dressed as a correctness warning, which is the class of false
// positive this whole mechanism exists to remove. The records are still written for JS
// because they cost nothing and become meaningful the moment the file gains annotations.
func jsCallSite(calleeID string, c *jsBodyCall) contract.CallSite {
	site := contract.CallSite{CalleeID: calleeID, N: c.ArgC, Variadic: c.ArgC < 0}
	anyKnown := false
	types := make([]*string, 0, len(c.Args))
	for _, tok := range c.Args {
		if tok == "" {
			types = append(types, nil)
			continue
		}
		anyKnown = true
		t := tok
		types = append(types, &t)
	}
	if anyKnown {
		site.Types = types
	}
	return site
}

func containsJSRec(recs []string, rec string) bool {
	for _, r := range recs {
		if r == rec {
			return true
		}
	}
	return false
}

// resolveFunctionTypingIDs resolves each parsed function's parameter and return type names
// to canonical resource IDs and stores them on the function's Input/Output VariableDefinitions
// (TypingID). Resolution happens here, where the topology store is available, so an imported
// type resolves against the DEFINING file's context. Storing the canonical id lets a caller in
// another file follow a return type (const x = f(); x.method()) without f's import context.
// This must run BEFORE body analysis so callees' TypingIDs are set when callers are analysed.
func resolveFunctionTypingIDs(gt *js.JavaScriptTopology, results []*ParseResult) {
	for _, pr := range results {
		for _, fp := range pr.Functions {
			f, ok := gt.Functions[fp.Function.ID]
			if !ok {
				continue
			}
			changed := false
			scope := functionScope(fp.Function, pr)
			for i := range f.Output {
				if id := typingID(f.Output[i].Typing, scope, pr, gt); id != "" && f.Output[i].TypingID != id {
					f.Output[i].TypingID = id
					changed = true
				}
			}
			for i := range f.Input {
				if id := typingID(f.Input[i].Typing, scope, pr, gt); id != "" && f.Input[i].TypingID != id {
					f.Input[i].TypingID = id
					changed = true
				}
			}
			if changed {
				gt.Functions[f.ID] = f
			}
		}
	}
}

// resolveInterfaceTypingIDs does for an interface's inline method summaries what
// resolveFunctionTypingIDs does for a function's own signature.
//
// An interface member is not a function resource -- it is a signature stored on the
// interface -- so nothing resolved the types it names, and a check comparing it with an
// implementation had only the text. The conformance check needs the id: `handle(e: MouseEv)`
// implementing `handle(e: BaseEv)` is legal TypeScript, and only the graph can say that the
// two are related rather than merely spelled differently.
func resolveInterfaceTypingIDs(gt *js.JavaScriptTopology, results []*ParseResult) {
	for _, pr := range results {
		for _, parsed := range pr.Interfaces {
			iface, ok := gt.Interfaces[parsed.ID]
			if !ok {
				continue
			}
			scope := declarationScope(string(iface.ID), pr)
			changed := false
			for m := range iface.Methods {
				for _, params := range [][]js.VariableDefinition{iface.Methods[m].Input, iface.Methods[m].Output} {
					for i := range params {
						if id := typingID(params[i].Typing, scope, pr, gt); id != "" && params[i].TypingID != id {
							params[i].TypingID = id
							changed = true
						}
					}
				}
			}
			if changed {
				gt.Interfaces[iface.ID] = iface
			}
		}
	}
}

// declarationScope is the name scope a top-level or nested declaration resolves names in:
// its own id minus the last segment, falling back to the file's module path.
func declarationScope(id string, pr *ParseResult) string {
	if i := strings.LastIndex(id, "."); i >= len(pr.ModulePath) {
		return id[:i]
	}
	return pr.ModulePath
}

// typingID resolves a type-annotation name to its canonical resource ID (or "" when the type
// is a built-in/external/unresolved), discarding the usage kind. It is the id half of
// resolveTypeName.
func typingID(typeName, scope string, pr *ParseResult, gt *js.JavaScriptTopology) string {
	_, id, _ := resolveTypeName(typeName, scope, pr, gt)
	return id
}

// functionScope is the namespace scope enclosing a function: for a method it is the class's
// container; for a plain function it is the parent of its own ID. For a top-level symbol this
// equals pr.ModulePath. It lets a name resolve relative to the caller's namespace (e.g.
// A.viaNested -> A.B.deep), which pr.ModulePath alone (reset to the file scope after parsing)
// cannot express.
func functionScope(f js.JavaScriptFunction, pr *ParseResult) string {
	scopeOf := string(f.ID)
	if f.MethodFrom != nil {
		scopeOf = string(*f.MethodFrom)
	}
	return declarationScope(scopeOf, pr)
}

// Resolves a direct function call by name, checking local functions and imported bindings.
func resolveDirectCall(name, scope string, pr *ParseResult, gt *js.JavaScriptTopology, add func(js.ConnectionKind, string)) {
	if name == "" {
		return
	}
	// local function, innermost enclosing namespace first
	for _, localID := range localIDs(pr, scope, name) {
		if isFunc(gt, localID) {
			add(js.ConnCalls, localID)
			return
		}
	}
	// imported binding
	info, ok := pr.ImportMap[name]
	if !ok {
		return
	}
	if !info.Internal {
		add(js.ConnUsesDep, info.Source)
		return
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return
	}
	for _, imported := range bindingExportNames(info, name) {
		if ref, ok := resolveExport(gt, abs, imported); ok {
			add(ref.Kind, ref.ID)
			return
		}
	}
}

// resolveFunctionID resolves a called name to the topology FunctionID it refers to: a local
// function in the current file, or an imported function resolved through the import map and the
// target module's ID namespace. It mirrors the function branch of resolveDirectCall but returns
// the id (so callers can read the function's return type) instead of adding edges.
func resolveFunctionID(name, scope string, pr *ParseResult, gt *js.JavaScriptTopology) (js.FunctionID, bool) {
	if name == "" {
		return "", false
	}
	for _, localID := range localIDs(pr, scope, name) {
		if isFunc(gt, localID) {
			return js.FunctionID(localID), true
		}
	}
	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", false
	}
	for _, imported := range bindingExportNames(info, name) {
		if ref, ok := resolveExport(gt, abs, imported); ok && ref.Kind == js.ConnCalls {
			return js.FunctionID(ref.ID), true
		}
	}
	return "", false
}

// Resolves a method call on an object, handling namespace imports, this-references, and variable type tracking.
func resolveMethodCall(call jsBodyCall, pr *ParseResult, gt *js.JavaScriptTopology, varTypeMap map[string]js.ClassID, varTypeArg map[string]js.ClassID, receiverClass *js.ClassID, fnScope string, add func(js.ConnectionKind, string)) {
	// A member of a package import -- `import axios from 'axios'; axios.get()` as much as
	// `import * as fs from 'fs'; fs.read()` -- is a use of that dependency.
	if dep, ok := externalBinding(pr, call.ObjectName, ""); ok {
		add(js.ConnUsesDep, dep)
		return
	}

	// namespace import: ns.member()
	if info, ok := pr.ImportMap[call.ObjectName]; ok && info.Namespace {
		if abs, ok := resolveSpecifier(pr.FileID, info.Source, gt); ok {
			if ref, ok := resolveExport(gt, abs, call.MethodName); ok {
				add(ref.Kind, ref.ID)
			} else if cid, ok := resolveClassName(call.ObjectName, fnScope, pr, gt); ok {
				// `const Circle = require('./circle'); Circle.unit()`: the binding is the
				// module's value, a class, and the member is its static method.
				addStaticCall(cid, call.MethodName, gt, add)
			}
		}
		return
	}

	// this.method() resolves against the enclosing class
	if call.ObjectName == "this" && receiverClass != nil {
		addMethodOf(*receiverClass, call.MethodName, gt, add)
		return
	}

	// super.method() / super(...) resolve against the enclosing class's base
	// class: `super.area()` -> Base.area, `super(...)` -> Base.constructor.
	if call.ObjectName == "super" {
		if receiverClass == nil {
			return
		}
		rc, ok := gt.Classes[*receiverClass]
		if !ok || len(rc.Bases) == 0 {
			return
		}
		bid, ok := resolveClassName(rc.Bases[0], fnScope, pr, gt)
		if !ok {
			return
		}
		if call.MethodName == "constructor" {
			if bc, ok := gt.Classes[bid]; ok && bc.Constructor != nil {
				add(js.ConnCalls, string(*bc.Constructor))
			}
		} else {
			addMethodOf(bid, call.MethodName, gt, add)
		}
		return
	}

	// method chained directly on a `new` expression: new Foo().bar()
	if call.ObjectNewClass != "" {
		if cid, ok := resolveClassName(call.ObjectNewClass, fnScope, pr, gt); ok {
			add(js.ConnUsesClass, string(cid))
			addMethodOf(cid, call.MethodName, gt, add)
		}
		return
	}

	// method chained directly on a call expression: getCircle().bar() follows
	// the callee's return type, mirroring the intermediate-variable form
	// (`const x = getCircle(); x.bar()`).
	if call.ObjectCallFunc != "" {
		if fid, ok := resolveFunctionID(call.ObjectCallFunc, fnScope, pr, gt); ok {
			if f, ok := gt.Functions[fid]; ok && len(f.Output) > 0 {
				if cid := js.ClassID(f.Output[0].TypingID); cid != "" {
					if _, ok := gt.Classes[cid]; ok {
						add(js.ConnUsesClass, string(cid))
						addMethodOf(cid, call.MethodName, gt, add)
					}
				}
			}
		}
		return
	}

	// method called on a TS cast: `(x as Circle).bar()` resolves against the
	// cast's target type, regardless of the operand's own static type.
	if call.ObjectCastClass != "" {
		if cid, ok := resolveClassName(call.ObjectCastClass, fnScope, pr, gt); ok {
			add(js.ConnUsesClass, string(cid))
			addMethodOf(cid, call.MethodName, gt, add)
		}
		return
	}

	// method chained on another method call: `b.get().area()`. Resolve the inner
	// receiver's class, follow the inner method's return type (propagating the
	// receiver's generic type argument when the return type is a type parameter,
	// e.g. `Box<Circle>.get() -> Circle`), then resolve the outer method.
	if call.ObjectMethodName != "" {
		if cid, ok := varTypeMap[call.ObjectMethodObject]; ok {
			if rcid, ok := methodReturnClass(cid, call.ObjectMethodName, varTypeArg[call.ObjectMethodObject], gt); ok {
				add(js.ConnUsesClass, string(rcid))
				addMethodOf(rcid, call.MethodName, gt, add)
			}
		}
		return
	}

	// member call on a local object-literal variable (`geometryOps.makeCircle()`)
	// or a namespace-qualified call (`B.deep()` inside namespace A). Resolution
	// walks the enclosing namespace scopes from the caller's own scope out to the
	// module root, so a nested-namespace member resolves (A.viaNested -> A.B.deep).
	if call.ObjectName != "" {
		for scope := fnScope; ; {
			if id := scope + "." + call.ObjectName + "." + call.MethodName; isFunc(gt, id) {
				// A static method called on its class (`Circle.unit()`) uses the class too.
				if owner := gt.Functions[id].MethodFrom; owner != nil {
					add(js.ConnUsesClass, string(*owner))
				}
				add(js.ConnCalls, id)
				return
			}
			if scope == pr.ModulePath {
				break
			}
			idx := strings.LastIndex(scope, ".")
			if idx < len(pr.ModulePath) {
				break
			}
			scope = scope[:idx]
		}
	}

	// variable known to hold a class instance
	if cid, ok := varTypeMap[call.ObjectName]; ok {
		add(js.ConnUsesClass, string(cid))
		addMethodOf(cid, call.MethodName, gt, add)
		return
	}

	// a static method called on an imported class (`import {Circle}; Circle.unit()`); a
	// local class was already reached by the scope walk above.
	if cid, ok := resolveClassName(call.ObjectName, fnScope, pr, gt); ok {
		addStaticCall(cid, call.MethodName, gt, add)
		return
	}

	// a member of an imported NAMESPACE (`import {Geo} from './ns'; Geo.dist()`).
	//
	// The branch at the top of this function only catches `import * as ns`, which is the
	// one form that sets the Namespace marker; a namespace imported by name looks like any
	// other binding. And the scope walk above searches the CALLER's namespaces, so a
	// namespace declared in another file was reachable from nowhere: the callee had no
	// incoming reference at all, which also cost it every signature_changed warning.
	//
	// Last, so an imported class's static method keeps resolving as one -- that branch also
	// records the class as used, which this one cannot know to do.
	if info, ok := pr.ImportMap[call.ObjectName]; ok && info.Internal {
		if abs, ok := resolveSpecifier(pr.FileID, info.Source, gt); ok {
			for _, imported := range bindingExportNames(info, call.ObjectName) {
				if ref, ok := resolveExport(gt, abs, imported+"."+call.MethodName); ok {
					add(ref.Kind, ref.ID)
					return
				}
			}
		}
	}
}

// addStaticCall records a member call made on a class itself rather than an instance: the
// class is used, and the member called when the class declares it.
func addStaticCall(cid js.ClassID, methodName string, gt *js.JavaScriptTopology, add func(js.ConnectionKind, string)) {
	add(js.ConnUsesClass, string(cid))
	addMethodOf(cid, methodName, gt, add)
}

// externalBinding reports the package behind a name bound by an import of a package (not a
// relative file): the binding itself (`axios`, `Client`), or the namespace object qualifying
// it (`pkg` in `new pkg.Client()`). A local declaration of the name shadows nothing here --
// JavaScript forbids a declaration and an import sharing a name.
func externalBinding(pr *ParseResult, name, qualifier string) (string, bool) {
	for _, n := range []string{name, qualifier} {
		if n == "" {
			continue
		}
		if info, ok := pr.ImportMap[n]; ok && !info.Internal {
			return info.Source, true
		}
	}
	return "", false
}

// methodReturnClass resolves the class returned by cid.<methodName>(). When the
// method's declared return type resolves to a concrete class, that class is
// returned. When the return type is declared but unresolved (a generic type
// parameter) and the receiver was instantiated with a type argument, the type
// argument is propagated as the result class (`Box<Circle>.get() -> Circle`).
func methodReturnClass(cid js.ClassID, methodName string, typeArg js.ClassID, gt *js.JavaScriptTopology) (js.ClassID, bool) {
	c, ok := gt.Classes[cid]
	if !ok {
		return "", false
	}
	for _, mid := range c.Methods() {
		mfn, ok := gt.Functions[mid]
		if !ok || mfn.Name != methodName || len(mfn.Output) == 0 {
			continue
		}
		out := mfn.Output[0]
		if rcid := js.ClassID(out.TypingID); rcid != "" {
			if _, ok := gt.Classes[rcid]; ok {
				return rcid, true
			}
		}
		// Return type declared but unresolved → treat it as the class's generic
		// type parameter and propagate the receiver's instantiation argument.
		if out.Typing != "" && typeArg != "" {
			return typeArg, true
		}
		return "", false
	}
	return "", false
}

// Finds and records a connection to a named method of a given class.
func addMethodOf(cid js.ClassID, methodName string, gt *js.JavaScriptTopology, add func(js.ConnectionKind, string)) {
	c, ok := gt.Classes[cid]
	if !ok {
		return
	}
	for _, mid := range c.Methods() {
		if mfn, ok := gt.Functions[mid]; ok && mfn.Name == methodName {
			add(js.ConnCalls, string(mid))
			return
		}
	}
}

// Resolves a class name to its ID, checking local scope and imported modules with full export resolution.
// resolveNamespaceClass resolves `new ns.Class()` where ns is an internal
// namespace import (static `import * as ns`, a require namespace binding, or a
// dynamic `await import(...)`), returning the imported class's ID.
func resolveNamespaceClass(nsObject, member string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ClassID, bool) {
	info, ok := pr.ImportMap[nsObject]
	if !ok || !info.Namespace || !info.Internal {
		return "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", false
	}
	if ref, ok := resolveExport(gt, abs, member); ok && ref.Kind == js.ConnUsesClass {
		return js.ClassID(ref.ID), true
	}
	return "", false
}

// resolveClassName resolves a class written by name in scope: a class of an enclosing
// namespace or the module, else the import bound to the name.
func resolveClassName(name, scope string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ClassID, bool) {
	for _, localID := range localIDs(pr, scope, name) {
		if _, ok := gt.Classes[localID]; ok {
			return js.ClassID(localID), true
		}
	}
	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", false
	}
	for _, imported := range bindingExportNames(info, name) {
		if ref, ok := resolveExport(gt, abs, imported); ok && ref.Kind == js.ConnUsesClass {
			return js.ClassID(ref.ID), true
		}
	}
	return "", false
}

// resolveTypeName resolves a TypeScript type-annotation name to a topology resource: a class
// (isClass=true → can drive method-call resolution), an interface, or a named type. Returns
// the usage ConnectionKind + resource ID, or empty if it resolves to a built-in/external type.
func resolveTypeName(name, scope string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ConnectionKind, string, bool) {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "[]")
	if i := strings.IndexByte(name, '<'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", "", false
	}

	for _, id := range localIDs(pr, scope, name) {
		if ref, ok := classifyType(gt, id); ok {
			return ref.Kind, ref.ID, ref.Kind == js.ConnUsesClass
		}
	}

	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", "", false
	}
	// Through re-exports too: a type imported from a barrel (`export * from './circle'`,
	// `export {Shape} from './shape'`) is declared somewhere else.
	for _, imported := range bindingExportNames(info, name) {
		if ref, ok := resolveExportWith(gt, abs, imported, classifyType, nil); ok {
			return ref.Kind, ref.ID, ref.Kind == js.ConnUsesClass
		}
	}
	return "", "", false
}

// resolveAliasedClass follows a type-alias named_type to the class it ultimately
// aliases (`type C = Circle` -> Circle), chasing chained aliases (`type C = D;
// type D = Circle`) until a class is reached. Returns the class ID, or false when
// the alias does not bottom out in a class. The visited set guards circular aliases.
func resolveAliasedClass(namedTypeID string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ClassID, bool) {
	visited := make(map[string]bool)
	for cur := namedTypeID; cur != "" && !visited[cur]; {
		visited[cur] = true
		nt, ok := gt.NamedTypes[js.NamedTypeID(cur)]
		if !ok || nt.Underlying == "" {
			return "", false
		}
		// The alias's own scope: `type C = Inner` inside namespace A names A.Inner.
		kind, id, isClass := resolveTypeName(nt.Underlying, extractModulePath(cur), pr, gt)
		if isClass {
			return js.ClassID(id), true
		}
		if kind == js.ConnUsesNamedType {
			cur = id
			continue
		}
		return "", false
	}
	return "", false
}

// moduleKey returns the ID namespace of the module at the given absolute path: the one
// it was registered under when it records one (a stem collision, see jsModulePathFor),
// else the extension-stripped path.
func moduleKey(gt *js.JavaScriptTopology, abs string) string {
	if mod, ok := gt.Modules[abs]; ok && mod.ModulePath != "" {
		return mod.ModulePath
	}
	return jsModulePath(gt.Root, abs)
}

// localIDs lists the IDs a bare name written in scope can denote within its own file,
// innermost first: each enclosing namespace out to the module (`ns.A.helper`, then
// `ns.helper`). Only namespace blocks are scopes here -- an object literal's `obj.m`
// prefix is not one a bare name can see into. An empty scope is the module itself.
func localIDs(pr *ParseResult, scope, name string) []string {
	var ids []string
	for strings.HasPrefix(scope, pr.ModulePath+".") {
		if pr.namespaces[scope] {
			ids = append(ids, scope+"."+name)
		}
		scope = scope[:strings.LastIndex(scope, ".")]
	}
	return append(ids, pr.ModulePath+"."+name)
}

// Links constructor methods to their parent classes in the JavaScript topology.
func detectConstructors(gt *js.JavaScriptTopology) {
	for _, fn := range gt.Functions {
		if fn.MethodFrom == nil || fn.Name != "constructor" {
			continue
		}
		cls := gt.Classes[*fn.MethodFrom]
		id := fn.ID
		cls.Constructor = &id
		gt.Classes[*fn.MethodFrom] = cls
	}
}

// Rebuilds has_method edges from functions to their containing classes, clearing stale connections on incremental updates.
func populateClassMethods(gt *js.JavaScriptTopology) {
	// Clear has_method edges first so an incremental UpdateFile that re-runs over
	// the whole topology rebuilds them instead of appending duplicates.
	for id, cls := range gt.Classes {
		delete(cls.Connections, js.ConnHasMethod)
		gt.Classes[id] = cls
	}
	for _, f := range gt.Functions {
		if f.MethodFrom == nil {
			continue
		}
		cls := gt.Classes[*f.MethodFrom]
		if cls.Connections == nil {
			cls.Connections = make(map[js.ConnectionKind][]string)
		}
		cls.Connections[js.ConnHasMethod] = append(cls.Connections[js.ConnHasMethod], string(f.ID))
		gt.Classes[*f.MethodFrom] = cls
	}
}

// Aggregates unique imported dependencies across all modules into the topology's dependency list.
func collectDependencies(gt *js.JavaScriptTopology) {
	seen := make(map[js.DependancyPath]bool)
	gt.Dependencies = nil
	for _, mod := range gt.Modules {
		for _, dep := range mod.DependenciesImported() {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				gt.Dependencies = append(gt.Dependencies, dep)
			}
		}
	}
}

// Deduplicates connection IDs within each connection kind, removing empty categories.
func uniqueConns(conns map[js.ConnectionKind][]string) map[js.ConnectionKind][]string {
	result := make(map[js.ConnectionKind][]string, len(conns))
	for k, v := range conns {
		seen := make(map[string]bool)
		var deduped []string
		for _, id := range v {
			if !seen[id] {
				seen[id] = true
				deduped = append(deduped, id)
			}
		}
		if len(deduped) > 0 {
			result[k] = deduped
		}
	}
	return result
}

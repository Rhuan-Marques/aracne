package jsscanner

import (
	"path/filepath"
	"strings"

	js "aracne/internal/topology/javascript"
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

// resolveSpecifier resolves a relative import specifier to the absolute path of
// a module already present in the topology, honouring implicit extensions and
// directory index files.
func resolveSpecifier(importerFile, source string, gt *js.JavaScriptTopology) (string, bool) {
	dir := filepath.Dir(importerFile)
	base := filepath.Clean(filepath.Join(dir, source))
	if _, ok := gt.Modules[base]; ok {
		return base, true
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
func resolveExport(gt *js.JavaScriptTopology, abs, modulePath, importedName string) (exportRef, bool) {
	name := importedName
	if name == "default" {
		mod, ok := gt.Modules[abs]
		if !ok || mod.DefaultExport == "" {
			return exportRef{}, false
		}
		name = mod.DefaultExport
	}
	id := modulePath + "." + name
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
func analyzeFunctionBody(body *jsFunc, pr *ParseResult, gt *js.JavaScriptTopology, receiverClass *js.ClassID) map[js.ConnectionKind][]string {
	conn := make(map[js.ConnectionKind][]string)
	if body == nil {
		return conn
	}

	add := func(kind js.ConnectionKind, id string) {
		if id == "" {
			return
		}
		for _, existing := range conn[kind] {
			if existing == id {
				return
			}
		}
		conn[kind] = append(conn[kind], id)
	}

	varTypeMap := make(map[string]js.ClassID)

	// TypeScript type annotations (params, declared local vars, return type). Each resolves
	// to a class (→ enables method-call resolution), an interface, or a named type, and the
	// corresponding usage edge is recorded. This is what JS couldn't do (it has no types).
	registerType := func(varName, typeName string) {
		kind, id, isClass := resolveTypeName(typeName, pr, gt)
		if id == "" {
			return
		}
		add(kind, id)
		if isClass && varName != "" {
			varTypeMap[varName] = js.ClassID(id)
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
	}

	// Instances learned from `const x = new Foo()`.
	for _, a := range body.BodyAssigns {
		if a.NewClass == "" || a.Name == "" {
			continue
		}
		if cid, ok := resolveClassName(a.NewClass, pr, gt); ok {
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
			if fid, ok := resolveFunctionID(a.CallFunc, pr, gt); ok {
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

	for _, call := range body.BodyCalls {
		switch {
		case call.IsNew:
			if cid, ok := resolveClassName(call.Func, pr, gt); ok {
				add(js.ConnUsesClass, string(cid))
				if c, ok := gt.Classes[cid]; ok && c.Constructor != nil {
					add(js.ConnCalls, string(*c.Constructor))
				}
			}

		case call.ObjectName == "" || call.MethodName == "":
			resolveDirectCall(call.Func, pr, gt, add)

		default:
			resolveMethodCall(call, pr, gt, varTypeMap, receiverClass, add)
		}
	}

	return conn
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
			for i := range f.Output {
				if id := typingID(f.Output[i].Typing, pr, gt); id != "" && f.Output[i].TypingID != id {
					f.Output[i].TypingID = id
					changed = true
				}
			}
			for i := range f.Input {
				if id := typingID(f.Input[i].Typing, pr, gt); id != "" && f.Input[i].TypingID != id {
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

// typingID resolves a type-annotation name to its canonical resource ID (or "" when the type
// is a built-in/external/unresolved), discarding the usage kind. It is the id half of
// resolveTypeName.
func typingID(typeName string, pr *ParseResult, gt *js.JavaScriptTopology) string {
	_, id, _ := resolveTypeName(typeName, pr, gt)
	return id
}

// Resolves a direct function call by name, checking local functions and imported bindings.
func resolveDirectCall(name string, pr *ParseResult, gt *js.JavaScriptTopology, add func(js.ConnectionKind, string)) {
	if name == "" {
		return
	}
	// local function
	localID := pr.ModulePath + "." + name
	if isFunc(gt, localID) {
		add(js.ConnCalls, localID)
		return
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
	imported := info.ImportedName
	if imported == "" {
		imported = name
	}
	if ref, ok := resolveExport(gt, abs, moduleKey(abs, pr), imported); ok {
		add(ref.Kind, ref.ID)
	}
}

// resolveFunctionID resolves a called name to the topology FunctionID it refers to: a local
// function in the current file, or an imported function resolved through the import map and the
// target module's ID namespace. It mirrors the function branch of resolveDirectCall but returns
// the id (so callers can read the function's return type) instead of adding edges.
func resolveFunctionID(name string, pr *ParseResult, gt *js.JavaScriptTopology) (js.FunctionID, bool) {
	if name == "" {
		return "", false
	}
	localID := pr.ModulePath + "." + name
	if isFunc(gt, localID) {
		return js.FunctionID(localID), true
	}
	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", false
	}
	imported := info.ImportedName
	if imported == "" {
		imported = name
	}
	if ref, ok := resolveExport(gt, abs, moduleKey(abs, pr), imported); ok && ref.Kind == js.ConnCalls {
		return js.FunctionID(ref.ID), true
	}
	return "", false
}

// Resolves a method call on an object, handling namespace imports, this-references, and variable type tracking.
func resolveMethodCall(call jsBodyCall, pr *ParseResult, gt *js.JavaScriptTopology, varTypeMap map[string]js.ClassID, receiverClass *js.ClassID, add func(js.ConnectionKind, string)) {
	// namespace import: ns.member()
	if info, ok := pr.ImportMap[call.ObjectName]; ok && info.Namespace {
		if !info.Internal {
			add(js.ConnUsesDep, info.Source)
			return
		}
		if abs, ok := resolveSpecifier(pr.FileID, info.Source, gt); ok {
			if ref, ok := resolveExport(gt, abs, moduleKey(abs, pr), call.MethodName); ok {
				add(ref.Kind, ref.ID)
			}
		}
		return
	}

	// this.method() resolves against the enclosing class
	if call.ObjectName == "this" && receiverClass != nil {
		addMethodOf(*receiverClass, call.MethodName, gt, add)
		return
	}

	// variable known to hold a class instance
	if cid, ok := varTypeMap[call.ObjectName]; ok {
		add(js.ConnUsesClass, string(cid))
		addMethodOf(cid, call.MethodName, gt, add)
	}
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
func resolveClassName(name string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ClassID, bool) {
	localID := js.ClassID(pr.ModulePath + "." + name)
	if _, ok := gt.Classes[localID]; ok {
		return localID, true
	}
	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", false
	}
	imported := info.ImportedName
	if imported == "" {
		imported = name
	}
	if ref, ok := resolveExport(gt, abs, moduleKey(abs, pr), imported); ok && ref.Kind == js.ConnUsesClass {
		return js.ClassID(ref.ID), true
	}
	return "", false
}

// resolveTypeName resolves a TypeScript type-annotation name to a topology resource: a class
// (isClass=true → can drive method-call resolution), an interface, or a named type. Returns
// the usage ConnectionKind + resource ID, or empty if it resolves to a built-in/external type.
func resolveTypeName(name string, pr *ParseResult, gt *js.JavaScriptTopology) (js.ConnectionKind, string, bool) {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "[]")
	if i := strings.IndexByte(name, '<'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", "", false
	}

	classify := func(id string) (js.ConnectionKind, string, bool) {
		if isClass(gt, id) {
			return js.ConnUsesClass, id, true
		}
		if _, ok := gt.Interfaces[js.InterfaceID(id)]; ok {
			return js.ConnUsesInterface, id, false
		}
		if _, ok := gt.NamedTypes[js.NamedTypeID(id)]; ok {
			return js.ConnUsesNamedType, id, false
		}
		return "", "", false
	}

	if k, id, isCls := classify(pr.ModulePath + "." + name); id != "" {
		return k, id, isCls
	}

	info, ok := pr.ImportMap[name]
	if !ok || !info.Internal {
		return "", "", false
	}
	abs, ok := resolveSpecifier(pr.FileID, info.Source, gt)
	if !ok {
		return "", "", false
	}
	imported := info.ImportedName
	if imported == "" {
		imported = name
	}
	return classify(moduleKey(abs, pr) + "." + imported)
}

// moduleKey returns the modulePath (exportIndex/ID namespace) for the module at
// the given absolute path, computed relative to the same project root as pr.
func moduleKey(abs string, pr *ParseResult) string {
	return jsModulePath(pr.ModuleRoot, abs)
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

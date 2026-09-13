package jsscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// JavaScriptScanner is the shared ECMAScript scanner; it drives both JavaScript and
// TypeScript via configuration (the grammar is chosen per file by grammarForFile). The name
// and extensions differ per language so resources are tagged distinctly and incremental
// diffing stays per-language.
type JavaScriptScanner struct {
	name        string
	extensions  []string
	detectFiles []string
}

// Creates a new JavaScriptScanner configured for .js, .mjs, .cjs, .jsx files.
func NewJavaScriptScanner() *JavaScriptScanner {
	return &JavaScriptScanner{
		name:        "javascript",
		extensions:  []string{".js", ".mjs", ".cjs", ".jsx"},
		detectFiles: []string{"package.json", "jsconfig.json"},
	}
}

// Creates a new TypeScript scanner configured for .ts, .tsx, .mts, .cts files.
func NewTypeScriptScanner() *JavaScriptScanner {
	return &JavaScriptScanner{
		name:        "typescript",
		extensions:  []string{".ts", ".tsx", ".mts", ".cts"},
		detectFiles: []string{"tsconfig.json"},
	}
}

// Returns the scanner's name identifier.
func (s *JavaScriptScanner) Name() string { return s.name }

// Returns the list of file extensions this scanner handles.
func (s *JavaScriptScanner) Extensions() []string { return s.extensions }

// Checks if JavaScript/TypeScript code exists in a directory by looking for signature files or glob patterns.
func (s *JavaScriptScanner) Detect(root string) bool {
	for _, name := range s.detectFiles {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	for _, ext := range s.extensions {
		if m, _ := filepath.Glob(filepath.Join(root, "*"+ext)); len(m) > 0 {
			return true
		}
	}
	return false
}

// Scans a directory tree for JavaScript/TypeScript files, parses them, and builds a complete topology with resolved references.
func (s *JavaScriptScanner) Scan(root string) (*domain.Topology, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to access root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory")
	}

	gt := newTopology(absRoot)

	files, err := helper.CollectSourceFiles(absRoot, s.Name())
	if err != nil {
		return nil, err
	}

	// Parse files concurrently (bounded by scanner.Workers()); each ParseFile owns
	// its tree-sitter parser and frees the tree before returning. Registration
	// stays sequential and in input order so the graph is deterministic.
	type parseRec struct {
		file string
		pr   *ParseResult
		err  error
	}
	recs := scanner.ParallelParse(files, "parsing "+s.Name(), func(f string) parseRec {
		pkgPath := getJSPackagePath(absRoot, filepath.Dir(f))
		pr, err := ParseFile(f, pkgPath, absRoot)
		return parseRec{file: f, pr: pr, err: err}
	})
	var parseResults []*ParseResult
	for _, rec := range recs {
		if rec.err != nil || rec.pr == nil {
			if rec.err != nil {
				gt.Errors[rec.file] = rec.err.Error()
			}
			continue
		}
		parseResults = append(parseResults, rec.pr)
		applyParsedFile(gt, rec.pr)
		recordSyntaxError(gt, rec.pr)
	}

	resolveTopology(gt, parseResults)

	return js.ToGeneric(gt, s.Name()), nil
}

// Scans a JavaScript/TypeScript file and updates the topology, preserving descriptions and generating warnings for function changes.
func (s *JavaScriptScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	gt := js.FromGeneric(topo)
	if gt == nil {
		return nil, fmt.Errorf("failed to convert topology from generic")
	}
	var warnings []domain.TopologyWarning

	root := gt.Root
	if root == "" {
		return warnings, nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings, nil
	}

	oldMod, had := gt.Modules[js.ModuleID(absPath)]
	var oldFuncs map[js.FunctionID]js.JavaScriptFunction
	var oldClasses map[js.ClassID]js.JavaScriptClass
	var oldVars map[js.ExternalVarID]js.JavaScriptExternalVar
	var oldIfaces map[js.InterfaceID]js.JavaScriptInterface
	var oldNamedTypes map[js.NamedTypeID]js.JavaScriptNamedType
	if had {
		oldFuncs, oldClasses, oldVars, oldIfaces, oldNamedTypes = snapshotModule(gt, oldMod)
		removeModule(gt, oldMod)
	}
	// Before this file's declarations are applied: a sibling that still holds the
	// namespace this file is about to take would otherwise delete them on removal.
	siblings := reparseStemSiblings(gt, root, absPath)

	pkgPath := getJSPackagePath(root, filepath.Dir(absPath))
	pr, err := ParseFile(absPath, pkgPath, root)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		if len(siblings) > 0 {
			resolveTopology(gt, siblings)
		}
		topo.Resources = js.ToGeneric(gt, s.Name()).Resources
		topo.Errors = gt.Errors
		return warnings, nil
	}

	if had {
		preserveDescriptions(pr, oldFuncs, oldClasses, oldVars, oldIfaces, oldNamedTypes, oldMod)
		warnings = diffFuncWarnings(oldFuncs, pr, s.name == "typescript")
	}

	applyParsedFile(gt, pr)
	resolveTopology(gt, append([]*ParseResult{pr}, siblings...))
	recordSyntaxError(gt, pr)

	topo.Resources = js.ToGeneric(gt, s.Name()).Resources
	topo.Errors = gt.Errors
	return warnings, nil
}

// recordSyntaxError keeps a file's scan error in step with its latest parse: set when the
// parser had to recover from part of the file, cleared once it parses cleanly.
func recordSyntaxError(gt *js.JavaScriptTopology, pr *ParseResult) {
	if pr.SyntaxError != "" {
		gt.Errors[pr.FileID] = pr.SyntaxError
	} else {
		delete(gt.Errors, pr.FileID)
	}
}

// reparseStemSiblings re-registers the files that share absPath's directory and stem
// (`util.js` / `util.cjs`) and whose ID namespace this update changed: jsModulePathFor ranks
// them against each other, so adding `util.js` beside a lone `util.cjs` moves util.cjs to
// `util.cjs.*`. Each is removed and parsed again under its current namespace, and its parse
// result returned for the relationship passes. A sibling no longer on disk is left to the
// caller's deletion.
func reparseStemSiblings(gt *js.JavaScriptTopology, root, absPath string) []*ParseResult {
	ext := filepath.Ext(absPath)
	stem := strings.TrimSuffix(absPath, ext)
	var results []*ParseResult
	for _, e := range moduleExtOrder {
		sib := stem + e
		mod, ok := gt.Modules[js.ModuleID(sib)]
		if sib == absPath || !ok {
			continue
		}
		if _, err := os.Stat(sib); err != nil {
			continue
		}
		current := mod.ModulePath
		if current == "" {
			current = jsModulePath(root, sib)
		}
		next := jsModulePathFor(root, sib)
		if next == current {
			continue
		}
		oldFuncs, oldClasses, oldVars, oldIfaces, oldNamedTypes := snapshotModule(gt, mod)
		removeModule(gt, mod)
		pr, err := ParseFile(sib, getJSPackagePath(root, filepath.Dir(sib)), root)
		if err != nil {
			gt.Errors[sib] = err.Error()
			continue
		}
		// The declarations are the same ones under a new prefix; their descriptions follow.
		preserveDescriptions(pr, rekey(oldFuncs, current, next), rekey(oldClasses, current, next),
			rekey(oldVars, current, next), rekey(oldIfaces, current, next), rekey(oldNamedTypes, current, next), mod)
		applyParsedFile(gt, pr)
		recordSyntaxError(gt, pr)
		results = append(results, pr)
	}
	return results
}

// rekey moves a snapshot's IDs from one module namespace to another.
func rekey[V any](m map[string]V, from, to string) map[string]V {
	out := make(map[string]V, len(m))
	for id, v := range m {
		if rest, ok := strings.CutPrefix(id, from+"."); ok {
			id = to + "." + rest
		}
		out[id] = v
	}
	return out
}

// Initializes an empty JavaScriptTopology with empty maps for functions, classes, interfaces, types, variables, modules, and errors.
func newTopology(absRoot string) *js.JavaScriptTopology {
	return &js.JavaScriptTopology{
		Root:         absRoot,
		Functions:    make(map[js.FunctionID]js.JavaScriptFunction),
		Classes:      make(map[js.ClassID]js.JavaScriptClass),
		Interfaces:   make(map[js.InterfaceID]js.JavaScriptInterface),
		NamedTypes:   make(map[js.NamedTypeID]js.JavaScriptNamedType),
		ExternalVars: make(map[js.ExternalVarID]js.JavaScriptExternalVar),
		Modules:      make(map[js.ModuleID]js.JavaScriptModule),
		Errors:       make(map[string]string),
	}
}

// mergedFunctions folds a parse result's function declarations into one resource per id,
// in first-declaration order.
//
// Both the graph and the signature diff go through here, and they have to: comparing the
// raw declarations would compare one overload set's implementation against the other's and
// never see an overload deleted between them.
func mergedFunctions(pr *ParseResult) (map[js.FunctionID]js.JavaScriptFunction, []js.FunctionID) {
	out := make(map[js.FunctionID]js.JavaScriptFunction, len(pr.Functions))
	order := make([]js.FunctionID, 0, len(pr.Functions))
	for _, fp := range pr.Functions {
		id := fp.Function.ID
		if prev, ok := out[id]; ok {
			out[id] = foldOverload(prev, fp)
			continue
		}
		fn := fp.Function
		if fp.Overload {
			// An overload set may have no implementation at all (`declare function`, a
			// .d.ts). Seeding the list from the first declaration means the set is
			// complete whether or not one follows.
			fn.Overloads = []js.FunctionDefinition{signatureOf(fn)}
		}
		out[id] = fn
		order = append(order, id)
	}
	return out, order
}

// foldOverload merges a second declaration of an id already in the graph.
//
// A TypeScript overload set writes the same function several times -- N bodiless signatures
// and one implementation -- and every declaration mints the same id, so the later one used
// to simply overwrite the earlier. What survived was whatever came last: the implementation
// (the one signature nobody may call) or, in a bodiless set, the final overload standing in
// for all of them. Both leave the callable API nowhere in the graph.
//
// The implementation stays the primary resource -- it is the one with a body, so it is the
// one worth reading -- while the signatures are collected beside it and the span is widened
// to cover the whole set, so a read shows the declarations a caller actually writes against.
// Two declarations that are not an overload set at all keep the old last-one-wins rule.
func foldOverload(prev js.JavaScriptFunction, fp FunctionParse) js.JavaScriptFunction {
	next := fp.Function
	switch {
	case fp.Overload:
		if len(prev.Overloads) == 0 {
			// A declaration that was not itself an overload now has one: keep its own
			// signature callable rather than letting the new one displace it.
			prev.Overloads = []js.FunctionDefinition{signatureOf(prev)}
		}
		prev.Overloads = append(prev.Overloads, signatureOf(next))
		prev.Loc = spanning(prev.Loc, next.Loc)
		return prev
	case len(prev.Overloads) > 0:
		// The implementation, arriving after its signatures: it takes over as the
		// resource and inherits the set.
		next.Overloads = prev.Overloads
		next.Loc = spanning(prev.Loc, next.Loc)
		return next
	}
	return next // not an overload set: an ordinary collision, last one wins as before
}

func signatureOf(fn js.JavaScriptFunction) js.FunctionDefinition {
	return js.FunctionDefinition{Name: fn.Name, Input: fn.Input, Output: fn.Output}
}

// spanning widens a location to cover both declarations, which are in the same file.
func spanning(a, b domain.Location) domain.Location {
	if b.StartsAt > 0 && (a.StartsAt == 0 || b.StartsAt < a.StartsAt) {
		a.StartsAt = b.StartsAt
	}
	if b.EndsAt > a.EndsAt {
		a.EndsAt = b.EndsAt
	}
	return a
}

// applyParsedFile inserts a parsed file's module, resources, and ownership edges
// into the topology. Relationship resolution happens later in resolveTopology.
func applyParsedFile(gt *js.JavaScriptTopology, pr *ParseResult) {
	modConns := make(map[js.ConnectionKind][]string)
	for _, dep := range pr.ExternalImports {
		modConns[js.ConnImportsDep] = append(modConns[js.ConnImportsDep], string(dep.PackagePath))
	}
	mod := js.JavaScriptModule{
		ID:            js.ModuleID(pr.FileID),
		Name:          filepath.Base(pr.FileID),
		FromPackage:   pr.PkgPath,
		DefaultExport: pr.Exports["default"],
		Connections:   modConns,
	}
	if pr.ModulePath != jsModulePath(pr.ModuleRoot, pr.FileID) {
		mod.ModulePath = pr.ModulePath // a stem collision; see jsModulePathFor
	}

	for _, c := range pr.Classes {
		gt.Classes[c.ID] = c
		mod.Connections[js.ConnHasClass] = append(mod.Connections[js.ConnHasClass], string(c.ID))
	}
	merged, order := mergedFunctions(pr)
	for _, id := range order {
		gt.Functions[id] = merged[id]
		mod.Connections[js.ConnHasFunc] = append(mod.Connections[js.ConnHasFunc], string(id))
	}
	for _, v := range pr.ExternalVars {
		gt.ExternalVars[v.ID] = v
		mod.Connections[js.ConnHasVar] = append(mod.Connections[js.ConnHasVar], string(v.ID))
	}
	for _, iface := range pr.Interfaces {
		if cls, ok := gt.Classes[iface.ID]; ok {
			// TypeScript class + interface declaration merging: a class and a
			// same-name interface share one resource ID. Keep the class as the
			// primary resource and fold the interface's members into it, rather
			// than letting the interface overwrite the class. Classes are applied
			// before interfaces above, so the class is already present here.
			cls.MergedInterfaceMethods = append(cls.MergedInterfaceMethods, iface.Methods...)
			cls.MergedInterfaceProperties = append(cls.MergedInterfaceProperties, iface.Properties...)
			if cls.Description == "" {
				cls.Description = iface.Description
			}
			gt.Classes[iface.ID] = cls
			continue
		}
		if existing, ok := gt.Interfaces[iface.ID]; ok {
			// TypeScript declaration merging: multiple same-name interface
			// declarations merge their members rather than the later one
			// overwriting the earlier. IDs are module-qualified, so this only
			// collides for merged declarations within the same file.
			existing.Methods = append(existing.Methods, iface.Methods...)
			existing.Properties = append(existing.Properties, iface.Properties...)
			existing.Bases = append(existing.Bases, iface.Bases...)
			for name, imp := range iface.HeritageImports {
				if existing.HeritageImports == nil {
					existing.HeritageImports = make(map[string]js.HeritageImport)
				}
				existing.HeritageImports[name] = imp
			}
			if existing.Description == "" {
				existing.Description = iface.Description
			}
			gt.Interfaces[iface.ID] = existing
			continue
		}
		gt.Interfaces[iface.ID] = iface
		mod.Connections[js.ConnHasInterface] = append(mod.Connections[js.ConnHasInterface], string(iface.ID))
	}
	for _, nt := range pr.NamedTypes {
		gt.NamedTypes[nt.ID] = nt
		mod.Connections[js.ConnHasNamedType] = append(mod.Connections[js.ConnHasNamedType], string(nt.ID))
	}

	gt.Modules[js.ModuleID(pr.FileID)] = mod
}

// resolveTopology runs the relationship passes. The structural passes run over
// the whole topology; body analysis runs only for the supplied parse results
// (all files for a full scan, the single changed file for an incremental one).
func resolveTopology(gt *js.JavaScriptTopology, results []*ParseResult) {
	populateClassMethods(gt)
	detectConstructors(gt)
	matchClassInheritance(gt)
	matchImplementsAndInterfaceExtends(gt)

	for _, pr := range results {
		imports := resolveModuleImports(pr, gt)
		reexports := resolveModuleReExports(pr, gt)
		named := resolveNamedReExports(pr, gt)
		if len(imports) > 0 || len(reexports) > 0 || len(named) > 0 {
			mod := gt.Modules[js.ModuleID(pr.FileID)]
			for _, target := range imports {
				mod.Connections[js.ConnImportsModule] = append(mod.Connections[js.ConnImportsModule], string(target))
			}
			for _, target := range reexports {
				mod.Connections[js.ConnReExportsModule] = append(mod.Connections[js.ConnReExportsModule], string(target))
			}
			if len(named) > 0 {
				mod.ReExportsNamed = named
			}
			mod.Connections = uniqueConns(mod.Connections)
			gt.Modules[js.ModuleID(pr.FileID)] = mod
		}
	}

	// Resolve return/param type IDs before body analysis so a caller can follow a callee's
	// return type (const x = f(); x.method()) even across files.
	resolveFunctionTypingIDs(gt, results)
	resolveInterfaceTypingIDs(gt, results)

	for _, pr := range results {
		for _, fp := range pr.Functions {
			if fp.Body == nil {
				continue
			}
			conns := analyzeFunctionBody(fp.Body, pr, gt, fp.Function.MethodFrom, functionScope(fp.Function, pr))
			f := gt.Functions[fp.Function.ID]
			if f.Connections == nil {
				f.Connections = make(map[js.ConnectionKind][]string)
			}
			for k, v := range conns {
				f.Connections[k] = append(f.Connections[k], v...)
			}
			f.Connections = uniqueConns(f.Connections)
			gt.Functions[f.ID] = f
		}
	}

	collectDependencies(gt)
}

// Extracts all functions, classes, variables, interfaces, and named types from a module into separate maps.
func snapshotModule(gt *js.JavaScriptTopology, mod js.JavaScriptModule) (
	map[js.FunctionID]js.JavaScriptFunction,
	map[js.ClassID]js.JavaScriptClass,
	map[js.ExternalVarID]js.JavaScriptExternalVar,
	map[js.InterfaceID]js.JavaScriptInterface,
	map[js.NamedTypeID]js.JavaScriptNamedType,
) {
	funcs := make(map[js.FunctionID]js.JavaScriptFunction)
	for _, id := range mod.Functions() {
		if f, ok := gt.Functions[id]; ok {
			funcs[id] = f
		}
	}
	classes := make(map[js.ClassID]js.JavaScriptClass)
	for _, id := range mod.Classes() {
		if c, ok := gt.Classes[id]; ok {
			classes[id] = c
		}
	}
	vars := make(map[js.ExternalVarID]js.JavaScriptExternalVar)
	for _, id := range mod.ExternalVars() {
		if v, ok := gt.ExternalVars[id]; ok {
			vars[id] = v
		}
	}
	ifaces := make(map[js.InterfaceID]js.JavaScriptInterface)
	for _, id := range mod.Interfaces() {
		if i, ok := gt.Interfaces[id]; ok {
			ifaces[id] = i
		}
	}
	namedTypes := make(map[js.NamedTypeID]js.JavaScriptNamedType)
	for _, id := range mod.NamedTypes() {
		if n, ok := gt.NamedTypes[id]; ok {
			namedTypes[id] = n
		}
	}
	return funcs, classes, vars, ifaces, namedTypes
}

// Removes a module and all its functions, classes, variables, interfaces, and named types from the topology.
func removeModule(gt *js.JavaScriptTopology, mod js.JavaScriptModule) {
	funcIDs := mod.Functions()
	classIDs := mod.Classes()
	varIDs := mod.ExternalVars()
	ifaceIDs := mod.Interfaces()
	namedTypeIDs := mod.NamedTypes()

	for _, id := range funcIDs {
		delete(gt.Functions, id)
	}
	for _, id := range classIDs {
		delete(gt.Classes, id)
	}
	for _, id := range varIDs {
		delete(gt.ExternalVars, id)
	}
	for _, id := range ifaceIDs {
		delete(gt.Interfaces, id)
	}
	for _, id := range namedTypeIDs {
		delete(gt.NamedTypes, id)
	}

	delete(gt.Modules, mod.ID)
}

// Carries forward user-provided descriptions from prior parse results when rescanning a file.
func preserveDescriptions(pr *ParseResult, oldFuncs map[js.FunctionID]js.JavaScriptFunction, oldClasses map[js.ClassID]js.JavaScriptClass, oldVars map[js.ExternalVarID]js.JavaScriptExternalVar, oldIfaces map[js.InterfaceID]js.JavaScriptInterface, oldNamedTypes map[js.NamedTypeID]js.JavaScriptNamedType, oldMod js.JavaScriptModule) {
	for i, fp := range pr.Functions {
		if old, ok := oldFuncs[fp.Function.ID]; ok && fp.Function.Description == "" && old.Description != "" {
			pr.Functions[i].Function.Description = old.Description
		}
	}
	for i, c := range pr.Classes {
		if old, ok := oldClasses[c.ID]; ok && c.Description == "" && old.Description != "" {
			pr.Classes[i].Description = old.Description
		}
	}
	for i, v := range pr.ExternalVars {
		if old, ok := oldVars[v.ID]; ok && v.Description == "" && old.Description != "" {
			pr.ExternalVars[i].Description = old.Description
		}
	}
	for i, iface := range pr.Interfaces {
		if old, ok := oldIfaces[iface.ID]; ok && iface.Description == "" && old.Description != "" {
			pr.Interfaces[i].Description = old.Description
		}
	}
	for i, nt := range pr.NamedTypes {
		if old, ok := oldNamedTypes[nt.ID]; ok && nt.Description == "" && old.Description != "" {
			pr.NamedTypes[i].Description = old.Description
		}
	}
	if pr.FileDescription == "" {
		pr.FileDescription = oldMod.Description
	}
}

// Compares old and new function signatures to detect removals and signature changes, returning topology warnings.
// typed is true for TypeScript, whose annotations and optional markers are binding.
func diffFuncWarnings(oldFuncs map[js.FunctionID]js.JavaScriptFunction, pr *ParseResult, typed bool) []domain.TopologyWarning {
	newFuncs, _ := mergedFunctions(pr)

	var warnings []domain.TopologyWarning
	for id, old := range oldFuncs {
		nf, ok := newFuncs[id]
		if !ok {
			// No node_removed warning here. Attributing it to the symbol that
			// just disappeared made CleanupOrphanedWarnings delete it before it
			// could reach the database, and told the agent nothing actionable.
			// The manager's referrer pass emits it against the surviving
			// callers instead, for every language.
			continue
		}
		if !signaturesEqualJS(old, nf, typed) {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       string(id) + "@sig_change@",
				SourceID: string(id),
				Kind:     domain.WarnSignatureChanged,
				Message:  fmt.Sprintf("function %s changed signature, verify callers", old.Name),
			})
		}
	}
	return warnings
}

// Compares two function signatures by parameter names and async/generator flags and, for
// TypeScript (typed), by everything a caller is checked against: each parameter's annotation,
// whether it is optional or a rest parameter, and the return type. The contract matcher then
// retires the warning for every call that still fits, so a change no caller can notice costs
// nothing -- while `b?: number` becoming `b: number` under `helper(1)` now warns.
//
// JavaScript stops at names on purpose. Its arity and types are not binding -- `f(1)` against
// `function f(a, b)` is legal -- so its matcher can never retire a warning, and gating on a
// default being added would raise one about a change no caller can notice that only a revert
// could clear.
func signaturesEqualJS(a, b js.JavaScriptFunction, typed bool) bool {
	if len(a.Input) != len(b.Input) {
		return false
	}
	for i := range a.Input {
		x, y := a.Input[i], b.Input[i]
		if x.Name != y.Name {
			return false
		}
		if typed && (x.Typing != y.Typing || x.Optional != y.Optional || x.Variadic != y.Variadic) {
			return false
		}
		if typed && annotationChanged(x, y) {
			return false
		}
	}
	if typed {
		if len(a.Output) != len(b.Output) {
			return false
		}
		for i := range a.Output {
			if a.Output[i].Typing != b.Output[i].Typing || annotationChanged(a.Output[i], b.Output[i]) {
				return false
			}
		}
	}
	if typed && !overloadsEqual(a.Overloads, b.Overloads) {
		return false
	}
	return a.IsAsync == b.IsAsync && a.IsGenerator == b.IsGenerator
}

// overloadsEqual compares the declared signatures of an overload set.
//
// Deleting an overload breaks every call that fitted only it, and nothing else in the
// comparison can see that: the implementation signature is deliberately the widest of the
// set, so it is unchanged by the deletion.
func overloadsEqual(a, b []js.FunctionDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i].Input) != len(b[i].Input) || len(a[i].Output) != len(b[i].Output) {
			return false
		}
		for j := range a[i].Input {
			x, y := a[i].Input[j], b[i].Input[j]
			if x.Name != y.Name || x.Typing != y.Typing || x.Optional != y.Optional ||
				x.Variadic != y.Variadic || annotationChanged(x, y) {
				return false
			}
		}
		for j := range a[i].Output {
			if a[i].Output[j].Typing != b[i].Output[j].Typing ||
				annotationChanged(a[i].Output[j], b[i].Output[j]) {
				return false
			}
		}
	}
	return true
}

// annotationChanged compares the full declared types of one parameter or return position.
//
// Typing alone cannot see this: it is reduced to the base name that resolves to a resource,
// so `Item` and `Item[]` are the same string and the edit between them -- which breaks every
// caller -- read as no change.
//
// An ABSENT annotation on either side is not a difference. A row stored before this field
// existed carries none, and calling that a change would make the first rescan after an
// upgrade warn about every annotated function in the repository. A real annotation being
// removed still changes Typing, which is compared beside this.
func annotationChanged(x, y js.VariableDefinition) bool {
	return x.Annotation != "" && y.Annotation != "" && x.Annotation != y.Annotation
}

// Derives a dot-separated package path from a directory relative to the project root.
// The project-root base name is deliberately absent (id-scheme 2); the root package
// reads ".". See getPythonPackagePath.
func getJSPackagePath(root, dir string) js.PackagePath {
	if dir == root {
		return js.PackagePath(".")
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return js.PackagePath(filepath.Base(dir))
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.ReplaceAll(rel, "/", ".")
	return js.PackagePath(rel)
}

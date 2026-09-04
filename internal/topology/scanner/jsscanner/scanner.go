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

	pkgPath := getJSPackagePath(root, filepath.Dir(absPath))
	pr, err := ParseFile(absPath, pkgPath, root)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		topo.Resources = js.ToGeneric(gt, s.Name()).Resources
		topo.Errors = gt.Errors
		return warnings, nil
	}

	if had {
		preserveDescriptions(pr, oldFuncs, oldClasses, oldVars, oldIfaces, oldNamedTypes, oldMod)
		warnings = diffFuncWarnings(oldFuncs, pr)
	}

	applyParsedFile(gt, pr)
	resolveTopology(gt, []*ParseResult{pr})
	delete(gt.Errors, absPath)

	topo.Resources = js.ToGeneric(gt, s.Name()).Resources
	topo.Errors = gt.Errors
	return warnings, nil
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

	for _, c := range pr.Classes {
		gt.Classes[c.ID] = c
		mod.Connections[js.ConnHasClass] = append(mod.Connections[js.ConnHasClass], string(c.ID))
	}
	for _, fp := range pr.Functions {
		gt.Functions[fp.Function.ID] = fp.Function
		mod.Connections[js.ConnHasFunc] = append(mod.Connections[js.ConnHasFunc], string(fp.Function.ID))
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

	for _, pr := range results {
		for _, fp := range pr.Functions {
			if fp.Body == nil {
				continue
			}
			// fnScope is the namespace scope enclosing this function: for a method
			// it is the class's container; for a plain function it is the parent of
			// its own ID. For a top-level symbol this equals pr.ModulePath. It lets a
			// namespace-qualified call resolve relative to the caller's namespace
			// (e.g. A.viaNested -> A.B.deep), which pr.ModulePath alone (reset to the
			// file scope after parsing) cannot express.
			scopeOf := string(fp.Function.ID)
			if fp.Function.MethodFrom != nil {
				scopeOf = string(*fp.Function.MethodFrom)
			}
			fnScope := pr.ModulePath
			if i := strings.LastIndex(scopeOf, "."); i >= len(pr.ModulePath) {
				fnScope = scopeOf[:i]
			}
			conns := analyzeFunctionBody(fp.Body, pr, gt, fp.Function.MethodFrom, fnScope)
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
func diffFuncWarnings(oldFuncs map[js.FunctionID]js.JavaScriptFunction, pr *ParseResult) []domain.TopologyWarning {
	newFuncs := make(map[js.FunctionID]js.JavaScriptFunction, len(pr.Functions))
	for _, fp := range pr.Functions {
		newFuncs[fp.Function.ID] = fp.Function
	}

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
		if !signaturesEqualJS(old, nf) {
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

// Compares two JavaScript function signatures for equality based on parameter names and async/generator flags.
func signaturesEqualJS(a, b js.JavaScriptFunction) bool {
	if len(a.Input) != len(b.Input) {
		return false
	}
	for i := range a.Input {
		if a.Input[i].Name != b.Input[i].Name {
			return false
		}
	}
	return a.IsAsync == b.IsAsync && a.IsGenerator == b.IsGenerator
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

// Generic helper that converts a slice of string-like types to a string slice.
func toStrings[T ~string](ids []T) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

// Filters a string slice by removing specified items, returning a new slice without matches.
func removeStrings(slice []string, items ...string) []string {
	if len(items) == 0 {
		return slice
	}
	removeSet := make(map[string]bool, len(items))
	for _, item := range items {
		removeSet[item] = true
	}
	var result []string
	for _, s := range slice {
		if !removeSet[s] {
			result = append(result, s)
		}
	}
	return result
}

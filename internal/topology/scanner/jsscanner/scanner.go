package jsscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
	js "aracne/internal/topology/javascript"
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

func NewJavaScriptScanner() *JavaScriptScanner {
	return &JavaScriptScanner{
		name:        "javascript",
		extensions:  []string{".js", ".mjs", ".cjs", ".jsx"},
		detectFiles: []string{"package.json", "jsconfig.json"},
	}
}

func NewTypeScriptScanner() *JavaScriptScanner {
	return &JavaScriptScanner{
		name:        "typescript",
		extensions:  []string{".ts", ".tsx", ".mts", ".cts"},
		detectFiles: []string{"tsconfig.json"},
	}
}

func (s *JavaScriptScanner) Name() string { return s.name }

func (s *JavaScriptScanner) Extensions() []string { return s.extensions }

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

	var parseResults []*ParseResult
	for _, f := range files {
		pkgPath := getJSPackagePath(absRoot, filepath.Dir(f))
		pr, err := ParseFile(f, pkgPath, absRoot)
		if err != nil {
			gt.Errors[f] = err.Error()
			continue
		}
		parseResults = append(parseResults, pr)
		applyParsedFile(gt, pr)
	}

	resolveTopology(gt, parseResults)

	return js.ToGeneric(gt, s.Name()), nil
}

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

func newTopology(absRoot string) *js.JavaScriptTopology {
	return &js.JavaScriptTopology{
		Root:         absRoot,
		Functions:    make(map[js.FunctionID]js.JavaScriptFunction),
		Classes:      make(map[js.ClassID]js.JavaScriptClass),
		Interfaces:   make(map[js.InterfaceID]js.JavaScriptInterface),
		NamedTypes:   make(map[js.NamedTypeID]js.JavaScriptNamedType),
		ExternalVars: make(map[js.ExternalVarID]js.JavaScriptExternalVar),
		Modules:      make(map[js.ModuleID]js.JavaScriptModule),
		Packages:     make(map[js.PackagePath]js.JavaScriptPackage),
		Errors:       make(map[string]string),
	}
}

// applyParsedFile inserts a parsed file's module, resources, and ownership edges
// into the topology. Relationship resolution happens later in resolveTopology.
func applyParsedFile(gt *js.JavaScriptTopology, pr *ParseResult) {
	pkgPath := pr.PkgPath
	pkg, ok := gt.Packages[pkgPath]
	if !ok {
		pkg = js.JavaScriptPackage{Path: pkgPath, Connections: make(map[js.ConnectionKind][]string)}
	}
	if pkg.Connections == nil {
		pkg.Connections = make(map[js.ConnectionKind][]string)
	}

	modConns := make(map[js.ConnectionKind][]string)
	for _, dep := range pr.ExternalImports {
		modConns[js.ConnImportsDep] = append(modConns[js.ConnImportsDep], string(dep.PackagePath))
	}
	mod := js.JavaScriptModule{
		ID:            js.ModuleID(pr.FileID),
		Name:          filepath.Base(pr.FileID),
		FromPackage:   pkgPath,
		DefaultExport: pr.Exports["default"],
		Connections:   modConns,
	}

	for _, c := range pr.Classes {
		gt.Classes[c.ID] = c
		mod.Connections[js.ConnHasClass] = append(mod.Connections[js.ConnHasClass], string(c.ID))
		pkg.Connections[js.ConnHasClass] = append(pkg.Connections[js.ConnHasClass], string(c.ID))
	}
	for _, fp := range pr.Functions {
		gt.Functions[fp.Function.ID] = fp.Function
		mod.Connections[js.ConnHasFunc] = append(mod.Connections[js.ConnHasFunc], string(fp.Function.ID))
		pkg.Connections[js.ConnHasFunc] = append(pkg.Connections[js.ConnHasFunc], string(fp.Function.ID))
	}
	for _, v := range pr.ExternalVars {
		gt.ExternalVars[v.ID] = v
		mod.Connections[js.ConnHasVar] = append(mod.Connections[js.ConnHasVar], string(v.ID))
		pkg.Connections[js.ConnHasVar] = append(pkg.Connections[js.ConnHasVar], string(v.ID))
	}
	for _, iface := range pr.Interfaces {
		gt.Interfaces[iface.ID] = iface
		mod.Connections[js.ConnHasInterface] = append(mod.Connections[js.ConnHasInterface], string(iface.ID))
		pkg.Connections[js.ConnHasInterface] = append(pkg.Connections[js.ConnHasInterface], string(iface.ID))
	}
	for _, nt := range pr.NamedTypes {
		gt.NamedTypes[nt.ID] = nt
		mod.Connections[js.ConnHasNamedType] = append(mod.Connections[js.ConnHasNamedType], string(nt.ID))
		pkg.Connections[js.ConnHasNamedType] = append(pkg.Connections[js.ConnHasNamedType], string(nt.ID))
	}

	pkg.Connections[js.ConnHasFile] = append(pkg.Connections[js.ConnHasFile], pr.FileID)
	gt.Modules[js.ModuleID(pr.FileID)] = mod
	gt.Packages[pkgPath] = pkg
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
		pkgs := resolveModuleImports(pr, gt)
		if len(pkgs) > 0 {
			mod := gt.Modules[js.ModuleID(pr.FileID)]
			for _, p := range pkgs {
				mod.Connections[js.ConnImportsPkg] = append(mod.Connections[js.ConnImportsPkg], string(p))
			}
			mod.Connections = uniqueConns(mod.Connections)
			gt.Modules[js.ModuleID(pr.FileID)] = mod
		}
	}

	for _, pr := range results {
		for _, fp := range pr.Functions {
			if fp.Body == nil {
				continue
			}
			conns := analyzeFunctionBody(fp.Body, pr, gt, fp.Function.MethodFrom)
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

	pkg, ok := gt.Packages[mod.FromPackage]
	if ok {
		pkg.Connections[js.ConnHasFile] = removeStrings(pkg.Connections[js.ConnHasFile], string(mod.ID))
		pkg.Connections[js.ConnHasFunc] = removeStrings(pkg.Connections[js.ConnHasFunc], toStrings(funcIDs)...)
		pkg.Connections[js.ConnHasClass] = removeStrings(pkg.Connections[js.ConnHasClass], toStrings(classIDs)...)
		pkg.Connections[js.ConnHasVar] = removeStrings(pkg.Connections[js.ConnHasVar], toStrings(varIDs)...)
		pkg.Connections[js.ConnHasInterface] = removeStrings(pkg.Connections[js.ConnHasInterface], toStrings(ifaceIDs)...)
		pkg.Connections[js.ConnHasNamedType] = removeStrings(pkg.Connections[js.ConnHasNamedType], toStrings(namedTypeIDs)...)
		gt.Packages[mod.FromPackage] = pkg
	}

	delete(gt.Modules, mod.ID)
}

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

func diffFuncWarnings(oldFuncs map[js.FunctionID]js.JavaScriptFunction, pr *ParseResult) []domain.TopologyWarning {
	newFuncs := make(map[js.FunctionID]js.JavaScriptFunction, len(pr.Functions))
	for _, fp := range pr.Functions {
		newFuncs[fp.Function.ID] = fp.Function
	}

	var warnings []domain.TopologyWarning
	for id, old := range oldFuncs {
		nf, ok := newFuncs[id]
		if !ok {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       string(id) + "@node_removed@",
				SourceID: string(id),
				Kind:     domain.WarnNodeRemoved,
				Message:  fmt.Sprintf("function %s was removed", old.Name),
			})
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

func getJSPackagePath(root, dir string) js.PackagePath {
	if dir == root {
		return js.PackagePath(filepath.Base(root))
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return js.PackagePath(filepath.Base(dir))
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.ReplaceAll(rel, "/", ".")
	return js.PackagePath(filepath.Base(root) + "." + rel)
}

func toStrings[T ~string](ids []T) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

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

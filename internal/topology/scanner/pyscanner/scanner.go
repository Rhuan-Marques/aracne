package pyscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Empty scanner marker struct for Python topology scanning.
type PythonScanner struct{}

// Creates and returns a new PythonScanner instance
func NewPythonScanner() *PythonScanner {
	return &PythonScanner{}
}

// Returns the scanner identifier: "python".
func (s *PythonScanner) Name() string { return "python" }

// Returns the file extensions this scanner handles: [".py"].
func (s *PythonScanner) Extensions() []string { return []string{".py"} }

// Detects Python projects by checking for standard config files or .py files in the root directory.
func (s *PythonScanner) Detect(root string) bool {
	indicators := []string{
		"setup.py", "pyproject.toml", "setup.cfg",
		"requirements.txt", "Pipfile", "Pipfile.lock",
	}
	for _, name := range indicators {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	pyFiles, _ := filepath.Glob(filepath.Join(root, "*.py"))
	return len(pyFiles) > 0
}

// Parses all Python files in a directory tree and builds a topology of modules, classes, functions, and their dependencies through multi-pass resolution.
func (s *PythonScanner) Scan(root string) (*domain.Topology, error) {
	// Re-discover import roots for this scan. The cache exists to avoid walking the tree
	// once per parsed FILE, not to persist across scans: `arac serve` is long-lived, so a
	// package added after startup would otherwise be classified against a stale sys.path
	// for the life of the process.
	ResetImportRootsCache()
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

	gt := &python.PythonTopology{
		Root:         absRoot,
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
		Modules:      make(map[python.ModuleID]python.PythonModule),
		Errors:       make(map[string]string),
	}

	pyFiles := collectPythonFiles(absRoot)

	type fileParseRecord struct {
		filePath string
		result   *ParseResult
	}

	var parseResults []fileParseRecord

	// Pass 1: parse every file concurrently (bounded by scanner.Workers()), then
	// register its module sequentially in input order so cross-file import and
	// reference resolution in later passes can see all modules (and the graph
	// stays deterministic). Parsing dominates Python's cost — ParseFile shells out
	// to an interpreter subprocess per file — so bounded parallelism is a large
	// speedup here, and the Workers() cap keeps the number of concurrent
	// subprocesses in check.
	type parseRec struct {
		filePath string
		result   *ParseResult
		err      error
	}
	recs := scanner.ParallelParse(pyFiles, "parsing python", func(filePath string) parseRec {
		pkgPath := getPythonPackagePath(absRoot, filepath.Dir(filePath))
		pr, err := ParseFile(filePath, pkgPath, absRoot)
		return parseRec{filePath: filePath, result: pr, err: err}
	})
	for _, rec := range recs {
		if rec.err != nil || rec.result == nil {
			if rec.err != nil {
				gt.Errors[rec.filePath] = rec.err.Error()
			}
			continue
		}
		pr := rec.result
		parseResults = append(parseResults, fileParseRecord{filePath: rec.filePath, result: pr})

		pkgPath := getPythonPackagePath(absRoot, filepath.Dir(rec.filePath))
		modConns := make(map[python.ConnectionKind][]string)
		for _, dep := range pr.ExternalImports {
			modConns[python.ConnImportsDep] = append(modConns[python.ConnImportsDep], string(dep.PackagePath))
		}
		gt.Modules[python.ModuleID(rec.filePath)] = python.PythonModule{
			ID:          python.ModuleID(rec.filePath),
			Name:        filepath.Base(rec.filePath),
			Description: pr.FileDescription,
			FromPackage: pkgPath,
			Connections: modConns,
		}
	}

	// Pass 2: register resources and wire each module's has_* containment edges.
	for _, fp := range parseResults {
		moduleID := python.ModuleID(fp.filePath)
		mod := gt.Modules[moduleID]

		for _, c := range fp.result.Classes {
			gt.Classes[c.ID] = c
			mod.Connections[python.ConnHasClass] = append(mod.Connections[python.ConnHasClass], string(c.ID))
		}
		for _, f := range fp.result.Functions {
			gt.Functions[f.Function.ID] = f.Function
			mod.Connections[python.ConnHasFunc] = append(mod.Connections[python.ConnHasFunc], string(f.Function.ID))
		}
		for _, v := range fp.result.ExternalVars {
			gt.ExternalVars[v.ID] = v
			mod.Connections[python.ConnHasVar] = append(mod.Connections[python.ConnHasVar], string(v.ID))
		}

		gt.Modules[moduleID] = mod
	}

	populateClassMethods(gt)
	detectConstructors(gt)

	// Resolve module->module import edges now that every module is present.
	for _, fp := range parseResults {
		moduleID := python.ModuleID(fp.filePath)
		mod := gt.Modules[moduleID]
		for _, target := range resolvePyModuleImports(fp.result, gt) {
			mod.Connections[python.ConnImportsModule] = append(mod.Connections[python.ConnImportsModule], string(target))
		}
		// Dedup containment/import edges before they reach the write layer:
		// constructs that bind one resource ID multiple times (@overload
		// signatures, property getter/setter/deleter, or a name bound in sibling
		// control-flow branches) otherwise emit duplicate has_* edges that abort
		// the whole topology write on the connections UNIQUE constraint. Mirrors
		// the dedup the incremental UpdateFile path already applies.
		mod.Connections = uniqueConns(mod.Connections)
		gt.Modules[moduleID] = mod
	}
	// After the import edges: a base class imported through a package re-export is found
	// by following them.
	matchClassInheritance(gt)
	matchProtocolImplementations(gt)

	for _, fp := range parseResults {
		for _, fi := range fp.result.Functions {
			if fi.Body != nil {
				conns := analyzeFunctionBody(fi.Body, fp.result, gt, fi.Function.Input, fi.Function.MethodFrom)
				f := gt.Functions[fi.Function.ID]
				if f.Connections == nil {
					f.Connections = make(map[python.ConnectionKind][]string)
				}
				for k, v := range conns {
					f.Connections[k] = append(f.Connections[k], v...)
				}
				f.Connections = uniqueConns(f.Connections)
				gt.Functions[f.ID] = f
			}
		}
		resolveClassVarRefs(fp.result, gt)
		resolveMetaclassRefs(fp.result, gt)
	}

	collectDependencies(gt)

	return python.ToGeneric(gt), nil
}

// Parses a Python file and updates the topology, preserving descriptions and emitting warnings for signature changes and removed resources
func (s *PythonScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	gt := python.FromGeneric(topo)
	if gt == nil {
		return nil, fmt.Errorf("failed to convert topology from generic")
	}

	var warnings []domain.TopologyWarning

	rootPath := gt.Root
	if rootPath == "" {
		return warnings, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings, nil
	}

	oldMod, hasMod := gt.Modules[python.ModuleID(absPath)]

	// Only an __init__.py can change which directories are packages, and therefore which
	// import roots exist -- and only a file the discovery has never seen can add a bare
	// top-level module to them. Invalidating on every edit would re-walk the tree per
	// keystroke; invalidating on neither left a long-lived process classifying `from later
	// import fn` as a third-party import after later.py was written, so the importer that the
	// incremental scan re-resolved for it resolved to nothing again.
	if filepath.Base(path) == "__init__.py" || !hasMod {
		ResetImportRootsCache()
	}

	dir := filepath.Dir(absPath)
	pkgPath := getPythonPackagePath(rootPath, dir)

	pr, err := ParseFile(absPath, pkgPath, rootPath)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		topo.Resources = python.ToGeneric(gt).Resources
		topo.Errors = gt.Errors
		return warnings, nil
	}

	if hasMod {
		// Carry over descriptions and emit signature/removal warnings before we
		// replace the old module's resources.
		oldFunctions := make(map[python.FunctionID]python.PythonFunction)
		for _, fid := range oldMod.Functions() {
			if f, ok := gt.Functions[fid]; ok {
				oldFunctions[fid] = f
			}
		}
		oldClasses := make(map[python.ClassID]python.PythonClass)
		for _, cid := range oldMod.Classes() {
			if c, ok := gt.Classes[cid]; ok {
				oldClasses[cid] = c
			}
		}
		oldExtVars := make(map[python.ExternalVarID]python.PythonExternalVar)
		for _, vid := range oldMod.ExternalVars() {
			if v, ok := gt.ExternalVars[vid]; ok {
				oldExtVars[vid] = v
			}
		}

		for i, fi := range pr.Functions {
			if oldFunc, ok := oldFunctions[fi.Function.ID]; ok && fi.Function.Description == "" && oldFunc.Description != "" {
				pr.Functions[i].Function.Description = oldFunc.Description
			}
		}
		for i, ci := range pr.Classes {
			if oldClass, ok := oldClasses[ci.ID]; ok && ci.Description == "" && oldClass.Description != "" {
				pr.Classes[i].Description = oldClass.Description
			}
		}
		for i, v := range pr.ExternalVars {
			if oldVar, ok := oldExtVars[v.ID]; ok && v.Description == "" && oldVar.Description != "" {
				pr.ExternalVars[i].Description = oldVar.Description
			}
		}
		if pr.FileDescription == "" && oldMod.Description != "" {
			pr.FileDescription = oldMod.Description
		}

		// A function that disappeared produces no warning here: see the note in
		// jsscanner.diffFuncWarnings. The manager's referrer pass attributes it
		// to the surviving callers, which is what persists and what an agent can
		// act on. Only a surviving function whose signature changed is reported.
		for fid, oldFunc := range oldFunctions {
			for _, fi := range pr.Functions {
				if fi.Function.ID != fid {
					continue
				}
				if !signaturesEqualPy(oldFunc, fi.Function) {
					warnings = append(warnings, domain.TopologyWarning{
						ID: string(fid) + "@sig_change@", SourceID: string(fid), Kind: domain.WarnSignatureChanged, TargetID: "", Message: fmt.Sprintf("function %s changed signature, verify callers", oldFunc.Name),
					})
				}
				break
			}
		}

		for _, fid := range oldMod.Functions() {
			delete(gt.Functions, fid)
		}
		for _, cid := range oldMod.Classes() {
			delete(gt.Classes, cid)
		}
		for _, vid := range oldMod.ExternalVars() {
			delete(gt.ExternalVars, vid)
		}
		delete(gt.Modules, oldMod.ID)
	}

	modConns := make(map[python.ConnectionKind][]string)
	for _, dep := range pr.ExternalImports {
		modConns[python.ConnImportsDep] = append(modConns[python.ConnImportsDep], string(dep.PackagePath))
	}
	newMod := python.PythonModule{
		ID:          python.ModuleID(absPath),
		Name:        filepath.Base(absPath),
		Description: pr.FileDescription,
		FromPackage: pkgPath,
		Connections: modConns,
	}
	for _, ci := range pr.Classes {
		gt.Classes[ci.ID] = ci
		newMod.Connections[python.ConnHasClass] = append(newMod.Connections[python.ConnHasClass], string(ci.ID))
	}
	for _, fi := range pr.Functions {
		gt.Functions[fi.Function.ID] = fi.Function
		newMod.Connections[python.ConnHasFunc] = append(newMod.Connections[python.ConnHasFunc], string(fi.Function.ID))
	}
	for _, v := range pr.ExternalVars {
		gt.ExternalVars[v.ID] = v
		newMod.Connections[python.ConnHasVar] = append(newMod.Connections[python.ConnHasVar], string(v.ID))
	}
	gt.Modules[python.ModuleID(absPath)] = newMod

	populateClassMethods(gt)
	detectConstructors(gt)

	// Re-resolve module->module imports for the updated file now that all
	// modules are present.
	updated := gt.Modules[python.ModuleID(absPath)]
	for _, target := range resolvePyModuleImports(pr, gt) {
		updated.Connections[python.ConnImportsModule] = append(updated.Connections[python.ConnImportsModule], string(target))
	}
	updated.Connections = uniqueConns(updated.Connections)
	gt.Modules[python.ModuleID(absPath)] = updated
	// After the import edges, as in Scan.
	matchClassInheritance(gt)
	matchProtocolImplementations(gt)

	for _, fi := range pr.Functions {
		if fi.Body != nil {
			conns := analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.Function.MethodFrom)
			f := gt.Functions[fi.Function.ID]
			if f.Connections == nil {
				f.Connections = make(map[python.ConnectionKind][]string)
			}
			for k, v := range conns {
				f.Connections[k] = append(f.Connections[k], v...)
			}
			f.Connections = uniqueConns(f.Connections)
			gt.Functions[f.ID] = f
		}
	}
	resolveClassVarRefs(pr, gt)
	resolveMetaclassRefs(pr, gt)

	collectDependencies(gt)
	delete(gt.Errors, absPath)

	topo.Resources = python.ToGeneric(gt).Resources
	topo.Errors = gt.Errors

	return warnings, nil
}

// Converts a directory path to a dotted Python package path relative to the project root.
// The project-root base name is deliberately absent (id-scheme 2): it names the directory
// a checkout happens to sit in, not anything in the source. The root package reads ".".
func getPythonPackagePath(root, dir string) python.PackagePath {
	if dir == root {
		return python.PackagePath(".")
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return python.PackagePath(filepath.Base(dir))
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.ReplaceAll(rel, "/", ".")
	return python.PackagePath(rel)
}

// Recursively collects all .py files from a directory, excluding common cache and test directories.
func collectPythonFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// path != root so a dot- or vendor-named ROOT is not pruned by its own
			// basename; WalkDir never visits the root's ancestors, so only the root
			// itself can match on a name it did not choose.
			if path == root {
				return nil
			}
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" ||
				name == "__pycache__" || name == ".pytest_cache" ||
				name == "venv" || name == ".venv" || name == "env" ||
				strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") && !strings.HasPrefix(d.Name(), "test_") {
			if domain.PathHidden(path) {
				return nil
			}
			files = append(files, path)
		}
		return nil
	})
	return files
}

// Compares two Python function signatures: each parameter's name, annotation and the way it
// may be passed (default, *args/**kwargs, keyword-only), and the return annotations.
//
// This decides whether a warning EXISTS; the contract matcher then retires it for every call
// that still fits, so it errs toward reporting. Comparing only the count and the annotations
// missed a default removed from under `opt(3)`, a keyword renamed from under `kw(1, b=2)` and a
// parameter made keyword-only under `g(1, 2)` -- each a TypeError at the call.
func signaturesEqualPy(a, b python.PythonFunction) bool {
	if len(a.Input) != len(b.Input) {
		return false
	}
	if len(a.Output) != len(b.Output) {
		return false
	}
	for i := range a.Input {
		x, y := a.Input[i], b.Input[i]
		if x.Name != y.Name || x.Typing != y.Typing || x.Optional != y.Optional ||
			x.Variadic != y.Variadic || x.KeyOnly != y.KeyOnly {
			return false
		}
	}
	for i := range a.Output {
		if a.Output[i].Typing != b.Output[i].Typing {
			return false
		}
	}
	return true
}

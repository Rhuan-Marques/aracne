package pyscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/python"
)

type PythonScanner struct{}

func NewPythonScanner() *PythonScanner {
	return &PythonScanner{}
}

func (s *PythonScanner) Name() string { return "python" }

func (s *PythonScanner) Extensions() []string { return []string{".py"} }

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

func (s *PythonScanner) Scan(root string) (*domain.Topology, error) {
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

	// Pass 1: parse every file and register its module (so cross-file import and
	// reference resolution in later passes can see all modules).
	for _, filePath := range pyFiles {
		pkgPath := getPythonPackagePath(absRoot, filepath.Dir(filePath))

		pr, err := ParseFile(filePath, pkgPath, absRoot)
		if err != nil {
			gt.Errors[filePath] = err.Error()
			continue
		}

		parseResults = append(parseResults, fileParseRecord{filePath: filePath, result: pr})

		modConns := make(map[python.ConnectionKind][]string)
		for _, dep := range pr.ExternalImports {
			modConns[python.ConnImportsDep] = append(modConns[python.ConnImportsDep], string(dep.PackagePath))
		}
		gt.Modules[python.ModuleID(filePath)] = python.PythonModule{
			ID:          python.ModuleID(filePath),
			Name:        filepath.Base(filePath),
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
	matchClassInheritance(gt)

	// Resolve module->module import edges now that every module is present.
	for _, fp := range parseResults {
		moduleID := python.ModuleID(fp.filePath)
		mod := gt.Modules[moduleID]
		for _, target := range resolvePyModuleImports(fp.result, gt) {
			mod.Connections[python.ConnImportsModule] = append(mod.Connections[python.ConnImportsModule], string(target))
		}
		gt.Modules[moduleID] = mod
	}

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
	}

	collectDependencies(gt)

	return python.ToGeneric(gt), nil
}

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

		for fid, oldFunc := range oldFunctions {
			found := false
			for _, fi := range pr.Functions {
				if fi.Function.ID == fid {
					found = true
					if !signaturesEqualPy(oldFunc, fi.Function) {
						warnings = append(warnings, domain.TopologyWarning{
							ID: string(fid) + "@sig_change@", SourceID: string(fid), Kind: domain.WarnSignatureChanged, TargetID: "", Message: fmt.Sprintf("function %s changed signature, verify callers", oldFunc.Name),
						})
					}
					break
				}
			}
			if !found {
				warnings = append(warnings, domain.TopologyWarning{
					ID: string(fid) + "@node_removed@", SourceID: string(fid), Kind: domain.WarnNodeRemoved, TargetID: "", Message: fmt.Sprintf("function %s was removed", oldFunc.Name),
				})
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
	matchClassInheritance(gt)

	// Re-resolve module->module imports for the updated file now that all
	// modules are present.
	updated := gt.Modules[python.ModuleID(absPath)]
	for _, target := range resolvePyModuleImports(pr, gt) {
		updated.Connections[python.ConnImportsModule] = append(updated.Connections[python.ConnImportsModule], string(target))
	}
	updated.Connections = uniqueConns(updated.Connections)
	gt.Modules[python.ModuleID(absPath)] = updated

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

	collectDependencies(gt)
	delete(gt.Errors, absPath)

	topo.Resources = python.ToGeneric(gt).Resources
	topo.Errors = gt.Errors

	return warnings, nil
}

func getPythonPackagePath(root, dir string) python.PackagePath {
	if dir == root {
		return python.PackagePath(filepath.Base(root))
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return python.PackagePath(filepath.Base(dir))
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.ReplaceAll(rel, "/", ".")
	return python.PackagePath(filepath.Base(root) + "." + rel)
}

func collectPythonFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" ||
				name == "__pycache__" || name == ".pytest_cache" ||
				name == "venv" || name == ".venv" || name == "env" ||
				strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") && !strings.HasPrefix(d.Name(), "test_") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func signaturesEqualPy(a, b python.PythonFunction) bool {
	if len(a.Input) != len(b.Input) {
		return false
	}
	if len(a.Output) != len(b.Output) {
		return false
	}
	for i := range a.Input {
		if a.Input[i].Typing != b.Input[i].Typing {
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

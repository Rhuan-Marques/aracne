package pyscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/python"
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
		Packages:     make(map[python.PackagePath]python.PythonPackage),
		Errors:       make(map[string]string),
	}

	pyFiles := collectPythonFiles(absRoot)

	dirFiles := make(map[string][]string)
	for _, f := range pyFiles {
		dir := filepath.Dir(f)
		dirFiles[dir] = append(dirFiles[dir], f)
	}

	type fileParseRecord struct {
		filePath string
		result   *ParseResult
		err      error
	}

	var parseResults []fileParseRecord

	for dir, files := range dirFiles {
		pkgPath := getPythonPackagePath(absRoot, dir)

		pkg, ok := gt.Packages[pkgPath]
		if !ok {
			pkg = python.PythonPackage{Path: pkgPath, Connections: make(map[python.ConnectionKind][]string)}
		}

		for _, filePath := range files {
			pr, err := ParseFile(filePath, pkgPath, absRoot)
			if err != nil {
				gt.Errors[filePath] = err.Error()
				continue
			}

			parseResults = append(parseResults, fileParseRecord{filePath: filePath, result: pr})

			modConns := make(map[python.ConnectionKind][]string)
			for _, ip := range pr.InternalImports {
				modConns[python.ConnImportsPkg] = append(modConns[python.ConnImportsPkg], string(ip))
			}
			for _, dep := range pr.ExternalImports {
				modConns[python.ConnImportsDep] = append(modConns[python.ConnImportsDep], string(dep.PackagePath))
			}
			mod := python.PythonModule{
				ID:          python.ModuleID(filePath),
				Name:        filepath.Base(filePath),
				Description: pr.FileDescription,
				FromPackage: pkgPath,
				Connections: modConns,
			}
			gt.Modules[python.ModuleID(filePath)] = mod
			pkg.Connections[python.ConnHasFile] = append(pkg.Connections[python.ConnHasFile], filePath)
		}

		gt.Packages[pkgPath] = pkg
	}

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
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

		pkg := gt.Packages[mod.FromPackage]
		pkgConns := pkg.Connections
		if pkgConns == nil {
			pkgConns = make(map[python.ConnectionKind][]string)
		}
		pkgConns[python.ConnHasFunc] = append(pkgConns[python.ConnHasFunc], mod.Connections[python.ConnHasFunc]...)
		pkgConns[python.ConnHasClass] = append(pkgConns[python.ConnHasClass], mod.Connections[python.ConnHasClass]...)
		pkgConns[python.ConnHasVar] = append(pkgConns[python.ConnHasVar], mod.Connections[python.ConnHasVar]...)
		pkg.Connections = pkgConns
		gt.Packages[mod.FromPackage] = pkg
	}

	populateClassMethods(gt)
	detectConstructors(gt)
	matchClassInheritance(gt)

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
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
	if !hasMod {
		return warnings, nil
	}

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

	dir := filepath.Dir(absPath)
	pkgPath := getPythonPackagePath(rootPath, dir)

	pr, err := ParseFile(absPath, pkgPath, rootPath)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		return warnings, nil
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

	pkg := gt.Packages[oldMod.FromPackage]
	pkg.Connections[python.ConnHasFile] = removeString(pkg.Connections[python.ConnHasFile], string(oldMod.ID))
	pkg.Connections[python.ConnHasFunc] = removeStrings(pkg.Connections[python.ConnHasFunc], castFuncIDs(oldMod.Functions())...)
	pkg.Connections[python.ConnHasClass] = removeStrings(pkg.Connections[python.ConnHasClass], castClassIDs(oldMod.Classes())...)
	pkg.Connections[python.ConnHasVar] = removeStrings(pkg.Connections[python.ConnHasVar], castExtVarIDs(oldMod.ExternalVars())...)
	gt.Packages[oldMod.FromPackage] = pkg

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

	modConns := make(map[python.ConnectionKind][]string)
	for _, ip := range pr.InternalImports {
		modConns[python.ConnImportsPkg] = append(modConns[python.ConnImportsPkg], string(ip))
	}
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

	pkg = gt.Packages[pkgPath]
	if pkg.Connections == nil {
		pkg.Connections = make(map[python.ConnectionKind][]string)
	}
	pkg.Connections[python.ConnHasFile] = append(pkg.Connections[python.ConnHasFile], absPath)
	pkg.Connections[python.ConnHasFunc] = append(pkg.Connections[python.ConnHasFunc], newMod.Connections[python.ConnHasFunc]...)
	pkg.Connections[python.ConnHasClass] = append(pkg.Connections[python.ConnHasClass], newMod.Connections[python.ConnHasClass]...)
	pkg.Connections[python.ConnHasVar] = append(pkg.Connections[python.ConnHasVar], newMod.Connections[python.ConnHasVar]...)
	gt.Packages[pkgPath] = pkg

	populateClassMethods(gt)
	detectConstructors(gt)
	matchClassInheritance(gt)

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

func removeString(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
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

func castFuncIDs(ids []python.FunctionID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

func castClassIDs(ids []python.ClassID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

func castExtVarIDs(ids []python.ExternalVarID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

package goscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
)

type GoScanner struct{}

func NewGoScanner() *GoScanner {
	return &GoScanner{}
}

func (s *GoScanner) Name() string { return "go" }

func (s *GoScanner) Extensions() []string { return []string{".go"} }

func (s *GoScanner) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}

func (s *GoScanner) Scan(root string) (*domain.Topology, error) {
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

	modulePath, err := readModulePath(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read module path: %w", err)
	}

	gt := &golang.GolangTopology{
		Root:         absRoot,
		Functions:    make(map[golang.FunctionID]golang.GolangFunction),
		Structs:      make(map[golang.StructID]golang.GolangStruct),
		Interfaces:   make(map[golang.InterfaceID]golang.GolangInterface),
		ExternalVars: make(map[golang.ExternalVarID]golang.GolangExternalVar),
		Files:        make(map[golang.FileID]golang.GolangFile),
		Packages:     make(map[golang.PackagePath]golang.GolangPackage),
		Warnings:     make(map[string]domain.TopologyWarning),
		Errors:       make(map[string]string),
	}

	goFiles := collectGoFiles(absRoot)

	dirFiles := make(map[string][]string)
	for _, f := range goFiles {
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
		pkgPath := getPackagePath(absRoot, dir, modulePath)

		pkg, ok := gt.Packages[pkgPath]
		if !ok {
			pkg = golang.GolangPackage{Path: pkgPath, Connections: make(map[golang.ConnectionKind][]string)}
		}

		for _, filePath := range files {
			pr, err := ParseFile(filePath, pkgPath, modulePath, absRoot)
			if err != nil {
				gt.Errors[filePath] = err.Error()
				continue
			}

			parseResults = append(parseResults, fileParseRecord{filePath: filePath, result: pr})

			fileConns := make(map[golang.ConnectionKind][]string)
			for _, ip := range pr.InternalImports {
				fileConns[golang.ConnImportsPkg] = append(fileConns[golang.ConnImportsPkg], string(ip))
			}
			for _, dep := range pr.ExternalImports {
				fileConns[golang.ConnImportsDep] = append(fileConns[golang.ConnImportsDep], string(dep.PackagePath))
			}
			file := golang.GolangFile{
				ID:          golang.FileID(filePath),
				Name:        filepath.Base(filePath),
				Description: pr.FileDescription,
				FromPackage: pkgPath,
				Connections: fileConns,
			}
			gt.Files[golang.FileID(filePath)] = file
			pkg.Connections[golang.ConnHasFile] = append(pkg.Connections[golang.ConnHasFile], filePath)
		}

		gt.Packages[pkgPath] = pkg
	}

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
		filePath := golang.FileID(fp.filePath)
		file := gt.Files[filePath]

		for _, s := range fp.result.Structs {
			gt.Structs[s.ID] = s
			file.Connections[golang.ConnHasStruct] = append(file.Connections[golang.ConnHasStruct], string(s.ID))
		}
		for _, iface := range fp.result.Interfaces {
			gt.Interfaces[iface.ID] = iface
			file.Connections[golang.ConnHasIface] = append(file.Connections[golang.ConnHasIface], string(iface.ID))
		}
		for _, f := range fp.result.Functions {
			gt.Functions[f.Function.ID] = f.Function
			file.Connections[golang.ConnHasFunc] = append(file.Connections[golang.ConnHasFunc], string(f.Function.ID))
		}
		for _, v := range fp.result.ExternalVars {
			gt.ExternalVars[v.ID] = v
			file.Connections[golang.ConnHasVar] = append(file.Connections[golang.ConnHasVar], string(v.ID))
		}

		gt.Files[filePath] = file

		pkg := gt.Packages[file.FromPackage]
		pkgConns := pkg.Connections
		if pkgConns == nil {
			pkgConns = make(map[golang.ConnectionKind][]string)
		}
		pkgConns[golang.ConnHasFunc] = append(pkgConns[golang.ConnHasFunc], file.Connections[golang.ConnHasFunc]...)
		pkgConns[golang.ConnHasStruct] = append(pkgConns[golang.ConnHasStruct], file.Connections[golang.ConnHasStruct]...)
		pkgConns[golang.ConnHasIface] = append(pkgConns[golang.ConnHasIface], file.Connections[golang.ConnHasIface]...)
		pkgConns[golang.ConnHasVar] = append(pkgConns[golang.ConnHasVar], file.Connections[golang.ConnHasVar]...)
		pkg.Connections = pkgConns
		gt.Packages[file.FromPackage] = pkg
	}

	populateStructMethods(gt)
	detectConstructors(gt)

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
		for _, fi := range fp.result.Functions {
			if fi.Body != nil {
				conns := analyzeFunctionBody(fi.Body, fp.result, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID)
				f := gt.Functions[fi.Function.ID]
				if f.Connections == nil {
					f.Connections = make(map[golang.ConnectionKind][]string)
				}
				for k, v := range conns {
					f.Connections[k] = append(f.Connections[k], v...)
				}
				f.Connections = uniqueConns(f.Connections)
				gt.Functions[f.ID] = f
			}
		}
	}

	matchStructsToInterfaces(gt)
	collectDependencies(gt)

	return golang.ToGeneric(gt), nil
}

func (s *GoScanner) UpdateFile(topo *domain.Topology, path string) []domain.TopologyWarning {
	gt := golang.FromGeneric(topo)
	if gt == nil {
		return nil
	}

	if gt.Warnings == nil {
		gt.Warnings = make(map[string]domain.TopologyWarning)
	}

	var warnings []domain.TopologyWarning

	rootPath := gt.Root
	if rootPath == "" {
		return warnings
	}

	modulePath, err := readModulePath(rootPath)
	if err != nil {
		return warnings
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings
	}

	oldFile, hasFile := gt.Files[golang.FileID(absPath)]
	if !hasFile {
		return warnings
	}

	oldFunctions := make(map[golang.FunctionID]golang.GolangFunction)
	for _, fid := range oldFile.Functions() {
		if f, ok := gt.Functions[fid]; ok {
			oldFunctions[fid] = f
		}
	}
	oldStructs := make(map[golang.StructID]golang.GolangStruct)
	for _, sid := range oldFile.Structs() {
		if s, ok := gt.Structs[sid]; ok {
			oldStructs[sid] = s
		}
	}
	oldInterfaces := make(map[golang.InterfaceID]golang.GolangInterface)
	for _, iid := range oldFile.Interfaces() {
		if iface, ok := gt.Interfaces[iid]; ok {
			oldInterfaces[iid] = iface
		}
	}
	oldExtVars := make(map[golang.ExternalVarID]golang.GolangExternalVar)
	for _, vid := range oldFile.ExternalVars() {
		if v, ok := gt.ExternalVars[vid]; ok {
			oldExtVars[vid] = v
		}
	}

	dir := filepath.Dir(absPath)
	pkgPath := getPackagePath(rootPath, dir, modulePath)

	pr, err := ParseFile(absPath, pkgPath, modulePath, rootPath)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		topo.Resources = golang.ToGeneric(gt).Resources
		topo.Errors = gt.Errors
		topo.Warnings = gt.Warnings
		return warnings
	}

	for i, fi := range pr.Functions {
		if oldFunc, ok := oldFunctions[fi.Function.ID]; ok && fi.Function.Description == "" && oldFunc.Description != "" {
			pr.Functions[i].Function.Description = oldFunc.Description
		}
	}
	for i, si := range pr.Structs {
		if oldStruct, ok := oldStructs[si.ID]; ok && si.Description == "" && oldStruct.Description != "" {
			pr.Structs[i].Description = oldStruct.Description
		}
	}
	for i, ii := range pr.Interfaces {
		if oldIface, ok := oldInterfaces[ii.ID]; ok && ii.Description == "" && oldIface.Description != "" {
			pr.Interfaces[i].Description = oldIface.Description
		}
	}
	for i, v := range pr.ExternalVars {
		if oldVar, ok := oldExtVars[v.ID]; ok && v.Description == "" && oldVar.Description != "" {
			pr.ExternalVars[i].Description = oldVar.Description
		}
	}
	if pr.FileDescription == "" && oldFile.Description != "" {
		pr.FileDescription = oldFile.Description
	}

	removedFuncs := make(map[golang.FunctionID]golang.GolangFunction)
	for fid, oldFunc := range oldFunctions {
		found := false
		for _, fi := range pr.Functions {
			if fi.Function.ID == fid {
				found = true
				if !signaturesEqualFn(oldFunc, fi.Function) {
					callers := s.getCallers(gt, string(fid), string(golang.ConnCalls))
					for _, callerID := range callers {
						if _, isOld := oldFunctions[golang.FunctionID(callerID)]; isOld {
							continue
						}
						warnID := callerID + "@" + string(domain.WarnSignatureChanged) + "@" + string(fid)
						gt.Warnings[warnID] = domain.TopologyWarning{
							ID:       warnID,
							SourceID: string(fid),
							Kind:     domain.WarnSignatureChanged,
							TargetID: callerID,
							Message:  fmt.Sprintf("function %s changed input/output format, verify caller %s", oldFunc.Name, callerID),
						}
					}
				}
				break
			}
		}
		if !found {
			removedFuncs[fid] = oldFunc
		}
	}

	removedStructs := make(map[golang.StructID]golang.GolangStruct)
	for sid := range oldStructs {
		found := false
		for _, si := range pr.Structs {
			if si.ID == sid {
				found = true
				break
			}
		}
		if !found {
			removedStructs[sid] = oldStructs[sid]
		}
	}

	for fid, oldFunc := range removedFuncs {
		callers := s.getCallers(gt, string(fid), string(golang.ConnCalls))
		for _, callerID := range callers {
			if _, isOld := oldFunctions[golang.FunctionID(callerID)]; isOld {
				continue
			}
			warnID := callerID + "@" + string(domain.WarnNodeRemoved) + "@" + string(fid)
			gt.Warnings[warnID] = domain.TopologyWarning{
				ID:       warnID,
				SourceID: callerID,
				Kind:     domain.WarnNodeRemoved,
				TargetID: string(fid),
				Message:  fmt.Sprintf("function %s calls %s which was removed from %s", callerID, oldFunc.Name, absPath),
			}
			if callerFn, ok := gt.Functions[golang.FunctionID(callerID)]; ok {
				callerFn.Connections[golang.ConnCalls] = removeString(callerFn.Connections[golang.ConnCalls], string(fid))
				gt.Functions[golang.FunctionID(callerID)] = callerFn
			}
		}

		structUsers := s.getCallers(gt, string(fid), string(golang.ConnUsesStruct))
		for _, sourceID := range structUsers {
			if _, isOld := oldFunctions[golang.FunctionID(sourceID)]; isOld {
				continue
			}
			if sourceFn, ok := gt.Functions[golang.FunctionID(sourceID)]; ok {
				sourceFn.Connections[golang.ConnUsesStruct] = removeString(sourceFn.Connections[golang.ConnUsesStruct], string(fid))
				gt.Functions[golang.FunctionID(sourceID)] = sourceFn
			}
		}
	}

	pkg := gt.Packages[oldFile.FromPackage]
	pkg.Connections[golang.ConnHasFile] = removeString(pkg.Connections[golang.ConnHasFile], string(oldFile.ID))
	pkg.Connections[golang.ConnHasFunc] = removeStrings(pkg.Connections[golang.ConnHasFunc], castFuncIDs(oldFile.Functions())...)
	pkg.Connections[golang.ConnHasStruct] = removeStrings(pkg.Connections[golang.ConnHasStruct], castStructIDs(oldFile.Structs())...)
	pkg.Connections[golang.ConnHasIface] = removeStrings(pkg.Connections[golang.ConnHasIface], castInterfaceIDs(oldFile.Interfaces())...)
	pkg.Connections[golang.ConnHasVar] = removeStrings(pkg.Connections[golang.ConnHasVar], castExtVarIDs(oldFile.ExternalVars())...)
	gt.Packages[oldFile.FromPackage] = pkg

	for _, fid := range oldFile.Functions() {
		delete(gt.Functions, fid)
	}
	for _, sid := range oldFile.Structs() {
		delete(gt.Structs, sid)
	}
	for _, iid := range oldFile.Interfaces() {
		delete(gt.Interfaces, iid)
	}
	for _, vid := range oldFile.ExternalVars() {
		delete(gt.ExternalVars, vid)
	}
	delete(gt.Files, oldFile.ID)

	fileConns := make(map[golang.ConnectionKind][]string)
	for _, ip := range pr.InternalImports {
		fileConns[golang.ConnImportsPkg] = append(fileConns[golang.ConnImportsPkg], string(ip))
	}
	for _, dep := range pr.ExternalImports {
		fileConns[golang.ConnImportsDep] = append(fileConns[golang.ConnImportsDep], string(dep.PackagePath))
	}
	newFile := golang.GolangFile{
		ID:          golang.FileID(absPath),
		Name:        filepath.Base(absPath),
		Description: pr.FileDescription,
		FromPackage: pkgPath,
		Connections: fileConns,
	}
	for _, si := range pr.Structs {
		gt.Structs[si.ID] = si
		newFile.Connections[golang.ConnHasStruct] = append(newFile.Connections[golang.ConnHasStruct], string(si.ID))
	}
	for _, ii := range pr.Interfaces {
		gt.Interfaces[ii.ID] = ii
		newFile.Connections[golang.ConnHasIface] = append(newFile.Connections[golang.ConnHasIface], string(ii.ID))
	}
	for _, fi := range pr.Functions {
		gt.Functions[fi.Function.ID] = fi.Function
		newFile.Connections[golang.ConnHasFunc] = append(newFile.Connections[golang.ConnHasFunc], string(fi.Function.ID))
	}
	for _, v := range pr.ExternalVars {
		gt.ExternalVars[v.ID] = v
		newFile.Connections[golang.ConnHasVar] = append(newFile.Connections[golang.ConnHasVar], string(v.ID))
	}
	gt.Files[golang.FileID(absPath)] = newFile

	pkg = gt.Packages[pkgPath]
	if pkg.Connections == nil {
		pkg.Connections = make(map[golang.ConnectionKind][]string)
	}
	pkg.Connections[golang.ConnHasFile] = append(pkg.Connections[golang.ConnHasFile], absPath)
	pkg.Connections[golang.ConnHasFunc] = append(pkg.Connections[golang.ConnHasFunc], newFile.Connections[golang.ConnHasFunc]...)
	pkg.Connections[golang.ConnHasStruct] = append(pkg.Connections[golang.ConnHasStruct], newFile.Connections[golang.ConnHasStruct]...)
	pkg.Connections[golang.ConnHasIface] = append(pkg.Connections[golang.ConnHasIface], newFile.Connections[golang.ConnHasIface]...)
	pkg.Connections[golang.ConnHasVar] = append(pkg.Connections[golang.ConnHasVar], newFile.Connections[golang.ConnHasVar]...)
	gt.Packages[pkgPath] = pkg

	populateStructMethods(gt)
	detectConstructors(gt)

	for _, fi := range pr.Functions {
		if fi.Body != nil {
			conns := analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID)
			f := gt.Functions[fi.Function.ID]
			if f.Connections == nil {
				f.Connections = make(map[golang.ConnectionKind][]string)
			}
			for k, v := range conns {
				f.Connections[k] = append(f.Connections[k], v...)
			}
			f.Connections = uniqueConns(f.Connections)
			gt.Functions[f.ID] = f
		}
	}

	s.resolveWarnings(gt, pr, removedFuncs, removedStructs)

	matchStructsToInterfaces(gt)
	collectDependencies(gt)
	delete(gt.Errors, absPath)

	topo.Resources = golang.ToGeneric(gt).Resources
	topo.Errors = gt.Errors
	topo.Warnings = gt.Warnings

	return warnings
}

func (s *GoScanner) resolveWarnings(gt *golang.GolangTopology, pr *ParseResult, removedFuncs map[golang.FunctionID]golang.GolangFunction, removedStructs map[golang.StructID]golang.GolangStruct) {
	newIDs := make(map[string]bool)
	for _, fi := range pr.Functions {
		newIDs[string(fi.Function.ID)] = true
	}
	for _, si := range pr.Structs {
		newIDs[string(si.ID)] = true
	}
	for _, ii := range pr.Interfaces {
		newIDs[string(ii.ID)] = true
	}
	for _, v := range pr.ExternalVars {
		newIDs[string(v.ID)] = true
	}

	for warnID, w := range gt.Warnings {
		if !newIDs[w.TargetID] {
			continue
		}

		switch w.Kind {
		case domain.WarnUseMissingNode:
			s.resolveUseMissingWarning(gt, w)
			delete(gt.Warnings, warnID)
		case domain.WarnNodeRemoved:
			s.resolveUseMissingWarning(gt, w)
			delete(gt.Warnings, warnID)
		case domain.WarnSignatureChanged:
			if _, removed := removedFuncs[golang.FunctionID(w.TargetID)]; removed {
				delete(gt.Warnings, warnID)
			}
		}
	}

	for warnID, w := range gt.Warnings {
		if w.Kind == domain.WarnUseMissingNode || w.Kind == domain.WarnNodeRemoved {
			if _, removed := removedFuncs[golang.FunctionID(w.TargetID)]; removed {
				continue
			}
			if _, removed := removedStructs[golang.StructID(w.TargetID)]; removed {
				continue
			}
		}
		if w.Kind == domain.WarnNodeRemoved {
			if _, removed := removedFuncs[golang.FunctionID(w.SourceID)]; removed {
				delete(gt.Warnings, warnID)
			}
			if _, removed := removedStructs[golang.StructID(w.SourceID)]; removed {
				delete(gt.Warnings, warnID)
			}
		}
	}
}

func (s *GoScanner) resolveUseMissingWarning(gt *golang.GolangTopology, w domain.TopologyWarning) {
	sourceFn, ok := gt.Functions[golang.FunctionID(w.SourceID)]
	if !ok {
		return
	}

	switch {
	case s.existsInFunctions(gt, w.TargetID):
		sourceFn.Connections[golang.ConnCalls] = append(sourceFn.Connections[golang.ConnCalls], w.TargetID)
	case s.existsInStructs(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesStruct] = append(sourceFn.Connections[golang.ConnUsesStruct], w.TargetID)
	case s.existsInInterfaces(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesIface] = append(sourceFn.Connections[golang.ConnUsesIface], w.TargetID)
	case s.existsInExtVars(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesExtVar] = append(sourceFn.Connections[golang.ConnUsesExtVar], w.TargetID)
	}

	sourceFn.Connections = uniqueConns(sourceFn.Connections)
	gt.Functions[golang.FunctionID(w.SourceID)] = sourceFn
}

func (s *GoScanner) existsInFunctions(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Functions[golang.FunctionID(id)]
	return ok
}

func (s *GoScanner) existsInStructs(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Structs[golang.StructID(id)]
	return ok
}

func (s *GoScanner) existsInInterfaces(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Interfaces[golang.InterfaceID(id)]
	return ok
}

func (s *GoScanner) existsInExtVars(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.ExternalVars[golang.ExternalVarID(id)]
	return ok
}

func (s *GoScanner) getCallers(gt *golang.GolangTopology, targetID string, connType string) []string {
	var callers []string
	for id, fn := range gt.Functions {
		for _, callID := range fn.Connections[golang.ConnectionKind(connType)] {
			if callID == targetID {
				callers = append(callers, string(id))
				break
			}
		}
	}
	return callers
}

func readModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("go.mod not found at %s: %w", root, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(line[7:]), nil
		}
	}
	return "", fmt.Errorf("no module declaration in go.mod")
}

func getPackagePath(root, dir, modulePath string) golang.PackagePath {
	if dir == root {
		return golang.PackagePath(modulePath)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return golang.PackagePath(modulePath + "/" + filepath.Base(dir))
	}
	return golang.PackagePath(modulePath + "/" + strings.ReplaceAll(rel, "\\", "/"))
}

func collectGoFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func populateStructMethods(gt *golang.GolangTopology) {
	for _, f := range gt.Functions {
		if f.MethodFrom != nil {
			str := gt.Structs[*f.MethodFrom]
			if str.Connections == nil {
				str.Connections = make(map[golang.ConnectionKind][]string)
			}
			str.Connections[golang.ConnHasMethod] = append(str.Connections[golang.ConnHasMethod], string(f.ID))
			gt.Structs[*f.MethodFrom] = str
		}
	}
}

func detectConstructors(gt *golang.GolangTopology) {
	for pkgPath, pkg := range gt.Packages {
		for _, funcID := range pkg.HasFunctions() {
			f := gt.Functions[funcID]
			if f.MethodFrom != nil {
				continue
			}
			if !strings.HasPrefix(f.Name, "New") {
				continue
			}
			if len(f.Output) == 0 {
				continue
			}
			returnType := f.Output[0].Typing
			if returnType == "" {
				continue
			}
			structID := golang.StructID(string(pkgPath) + "." + returnType)
			if _, exists := gt.Structs[structID]; exists {
				str := gt.Structs[structID]
				str.Constructor = &f.ID
				gt.Structs[structID] = str
				continue
			}
			if strings.HasPrefix(returnType, "*") {
				structID = golang.StructID(string(pkgPath) + "." + returnType[1:])
				if _, exists := gt.Structs[structID]; exists {
					str := gt.Structs[structID]
					str.Constructor = &f.ID
					gt.Structs[structID] = str
				}
			}
		}
	}
}

func collectDependencies(gt *golang.GolangTopology) {
	seen := make(map[golang.DependancyPath]bool)
	for _, file := range gt.Files {
		for _, dep := range file.DependenciesImported() {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				gt.Dependencies = append(gt.Dependencies, dep)
			}
		}
	}
}

func uniqueConns(conns map[golang.ConnectionKind][]string) map[golang.ConnectionKind][]string {
	result := make(map[golang.ConnectionKind][]string, len(conns))
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

func signaturesEqualFn(a, b golang.GolangFunction) bool {
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

func castFuncIDs(ids []golang.FunctionID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

func castStructIDs(ids []golang.StructID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

func castInterfaceIDs(ids []golang.InterfaceID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

func castExtVarIDs(ids []golang.ExternalVarID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

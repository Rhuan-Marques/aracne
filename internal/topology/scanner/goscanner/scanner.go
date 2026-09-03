package goscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/scanner"
)

// Go AST scanner that parses Go source files to extract function, struct, interface, and variable definitions plus their call relationships.
type GoScanner struct{}

// Creates and returns a new GoScanner instance.
func NewGoScanner() *GoScanner {
	return &GoScanner{}
}

// Returns the scanner identifier for Go: "go"
func (s *GoScanner) Name() string { return "go" }

// Returns the file extensions supported by the Go scanner: .go
func (s *GoScanner) Extensions() []string { return []string{".go"} }

// Detects a Go project by checking for the presence of a go.mod file in the root directory.
func (s *GoScanner) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}

// Parses Go source files from root directory and builds complete topology with functions, structs, interfaces, and dependencies.
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
		NamedTypes:   make(map[golang.NamedTypeID]golang.GolangNamedType),
		ExternalVars: make(map[golang.ExternalVarID]golang.GolangExternalVar),
		Files:        make(map[golang.FileID]golang.GolangFile),
		Packages:     make(map[golang.PackagePath]golang.GolangPackage),
		Warnings:     make(map[string]domain.TopologyWarning),
		Errors:       make(map[string]string),
	}

	goFiles := collectGoFiles(absRoot)

	type fileParseRecord struct {
		filePath string
		result   *ParseResult
		err      error
	}

	// Pass 1: parse every file concurrently (bounded by scanner.Workers()). Each
	// worker drops the AST bodies before returning — they are recovered by a
	// re-parse in pass 2 — so at most ~Workers() files' ASTs are ever live at
	// once. That bound is what keeps peak RAM from ballooning to the whole tree
	// (the previous behavior, which pinned every function body for the entire
	// project simultaneously).
	records := scanner.ParallelParse(goFiles, "parsing go", func(filePath string) fileParseRecord {
		pkgPath := getPackagePath(absRoot, filepath.Dir(filePath), modulePath)
		pr, err := ParseFile(filePath, pkgPath, modulePath, absRoot)
		if err == nil && pr != nil {
			for i := range pr.Functions {
				pr.Functions[i].Body = nil
			}
		}
		return fileParseRecord{filePath: filePath, result: pr, err: err}
	})

	// Register each file's package/file nodes sequentially, in input order, so the
	// resulting graph is deterministic and identical to the old single-threaded
	// loop. parseResults keeps the body-less results for the structural passes
	// below (populateNamedTypeUsage reads signatures/imports, never bodies).
	var parseResults []fileParseRecord
	for _, rec := range records {
		if rec.err != nil || rec.result == nil {
			if rec.err != nil {
				gt.Errors[rec.filePath] = rec.err.Error()
			}
			continue
		}
		pr := rec.result
		filePath := rec.filePath
		pkgPath := getPackagePath(absRoot, filepath.Dir(filePath), modulePath)

		pkg, ok := gt.Packages[pkgPath]
		if !ok {
			pkg = golang.GolangPackage{Path: pkgPath, Connections: make(map[golang.ConnectionKind][]string)}
		}

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
		gt.Packages[pkgPath] = pkg

		parseResults = append(parseResults, rec)
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
		for _, nt := range fp.result.NamedTypes {
			gt.NamedTypes[nt.ID] = nt
			file.Connections[golang.ConnHasNamedType] = append(file.Connections[golang.ConnHasNamedType], string(nt.ID))
		}
		for _, f := range fp.result.Functions {
			gt.Functions[f.Function.ID] = f.Function
			file.Connections[golang.ConnHasFunc] = append(file.Connections[golang.ConnHasFunc], string(f.Function.ID))
		}
		for _, v := range fp.result.ExternalVars {
			gt.ExternalVars[v.ID] = v
			file.Connections[golang.ConnHasVar] = append(file.Connections[golang.ConnHasVar], string(v.ID))
		}

		file.Connections = uniqueConns(file.Connections)
		gt.Files[filePath] = file

		pkg := gt.Packages[file.FromPackage]
		pkgConns := pkg.Connections
		if pkgConns == nil {
			pkgConns = make(map[golang.ConnectionKind][]string)
		}
		pkgConns[golang.ConnHasFunc] = append(pkgConns[golang.ConnHasFunc], file.Connections[golang.ConnHasFunc]...)
		pkgConns[golang.ConnHasStruct] = append(pkgConns[golang.ConnHasStruct], file.Connections[golang.ConnHasStruct]...)
		pkgConns[golang.ConnHasIface] = append(pkgConns[golang.ConnHasIface], file.Connections[golang.ConnHasIface]...)
		pkgConns[golang.ConnHasNamedType] = append(pkgConns[golang.ConnHasNamedType], file.Connections[golang.ConnHasNamedType]...)
		pkgConns[golang.ConnHasVar] = append(pkgConns[golang.ConnHasVar], file.Connections[golang.ConnHasVar]...)
		pkg.Connections = pkgConns
		gt.Packages[file.FromPackage] = pkg
	}

	for pkgPath, pkg := range gt.Packages {
		pkg.Connections = uniqueConns(pkg.Connections)
		gt.Packages[pkgPath] = pkg
	}

	populateStructMethods(gt)
	detectConstructors(gt)
	var namedTypeParseResults []*ParseResult
	for _, fp := range parseResults {
		if fp.err == nil && fp.result != nil {
			namedTypeParseResults = append(namedTypeParseResults, fp.result)
		}
	}
	populateNamedTypeUsage(gt, namedTypeParseResults)

	// Pass 2: recover each file's bodies (dropped in pass 1) by re-parsing, then
	// resolve every function's body edges against the now-complete gt. Parse +
	// analysis run concurrently; gt is only READ here (existence checks). The one
	// thing analyzeFunctionBody writes — gt.Warnings, via a captured pointer — is
	// redirected to a per-worker private map by handing it a shallow gt copy, so
	// there is no concurrent map write. The per-function deltas are merged
	// sequentially in input order afterward, making the result identical to the
	// old single-threaded body loop.
	type funcConns struct {
		id    golang.FunctionID
		conns map[golang.ConnectionKind][]string
	}
	type bodyDelta struct {
		conns    []funcConns
		warnings map[string]domain.TopologyWarning
	}
	deltas := scanner.ParallelParse(goFiles, "analyzing go", func(filePath string) bodyDelta {
		d := bodyDelta{warnings: make(map[string]domain.TopologyWarning)}
		pkgPath := getPackagePath(absRoot, filepath.Dir(filePath), modulePath)
		pr, err := ParseFile(filePath, pkgPath, modulePath, absRoot)
		if err != nil || pr == nil {
			return d
		}
		gtLocal := *gt
		gtLocal.Warnings = d.warnings
		for _, fi := range pr.Functions {
			if fi.Body != nil {
				conns := analyzeFunctionBody(fi.Body, pr, &gtLocal, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID, fi.TypeParamNames)
				d.conns = append(d.conns, funcConns{id: fi.Function.ID, conns: conns})
			}
		}
		return d
	})

	for _, d := range deltas {
		for _, fc := range d.conns {
			f := gt.Functions[fc.id]
			if f.Connections == nil {
				f.Connections = make(map[golang.ConnectionKind][]string)
			}
			for k, v := range fc.conns {
				f.Connections[k] = append(f.Connections[k], v...)
			}
			f.Connections = uniqueConns(f.Connections)
			gt.Functions[f.ID] = f
		}
		for id, w := range d.warnings {
			if _, ok := gt.Warnings[id]; !ok {
				gt.Warnings[id] = w
			}
		}
	}

	matchStructsToInterfaces(gt)
	collectDependencies(gt)

	return golang.ToGeneric(gt), nil
}

// Reanalyzes a single Go file and updates topology with new definitions and connections.
func (s *GoScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	gt := golang.FromGeneric(topo)
	if gt == nil {
		return nil, fmt.Errorf("failed to convert topology from generic")
	}

	if gt.Warnings == nil {
		gt.Warnings = make(map[string]domain.TopologyWarning)
	}

	var warnings []domain.TopologyWarning

	rootPath := gt.Root
	if rootPath == "" {
		return warnings, nil
	}

	modulePath, err := readModulePath(rootPath)
	if err != nil {
		return warnings, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings, nil
	}

	dir := filepath.Dir(absPath)
	pkgPath := getPackagePath(rootPath, dir, modulePath)

	pr, err := ParseFile(absPath, pkgPath, modulePath, rootPath)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		topo.Resources = golang.ToGeneric(gt).Resources
		topo.Errors = gt.Errors
		topo.Warnings = gt.Warnings
		return warnings, nil
	}

	warnings, err = s.applyFileUpdate(gt, pr, absPath, pkgPath, s.fullPasses())
	if err != nil {
		return nil, err
	}

	topo.Resources = golang.ToGeneric(gt).Resources
	topo.Errors = gt.Errors
	topo.Warnings = gt.Warnings
	return warnings, nil
}

// fullPasses returns the whole-graph relationship passes used by the full
// UpdateFile path (unchanged behavior).
func (s *GoScanner) fullPasses() gtPasses {
	return gtPasses{
		getCallers:    s.getCallers,
		structMethods: func(gt *golang.GolangTopology, _ *ParseResult) { populateStructMethods(gt) },
		constructors:  func(gt *golang.GolangTopology, _ *ParseResult) { detectConstructors(gt) },
		matchInterfaces: func(gt *golang.GolangTopology, _ *ParseResult, _ map[golang.StructID]golang.GolangStruct, _ map[golang.FunctionID]golang.GolangFunction) {
			matchStructsToInterfaces(gt)
		},
		collectDeps: collectDependencies,
	}
}

// applyFileUpdate mutates gt to reflect the (re)parsed file pr at absPath, using
// the supplied relationship passes. It unifies the new-file and existing-file
// cases: when the file already exists in gt it carries over descriptions,
// computes removed/signature-changed warnings, cleans up caller edges, and
// removes the old members; otherwise those steps are skipped. It returns the
// caller-facing warnings (signature-changed + node-removed) but does NOT write
// back to any domain.Topology (callers convert gt as needed).
func (s *GoScanner) applyFileUpdate(gt *golang.GolangTopology, pr *ParseResult, absPath string, pkgPath golang.PackagePath, passes gtPasses) ([]domain.TopologyWarning, error) {
	var warnings []domain.TopologyWarning
	oldFile, hasFile := gt.Files[golang.FileID(absPath)]

	if !hasFile {
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
		for _, nt := range pr.NamedTypes {
			gt.NamedTypes[nt.ID] = nt
			newFile.Connections[golang.ConnHasNamedType] = append(newFile.Connections[golang.ConnHasNamedType], string(nt.ID))
		}
		for _, fi := range pr.Functions {
			gt.Functions[fi.Function.ID] = fi.Function
			newFile.Connections[golang.ConnHasFunc] = append(newFile.Connections[golang.ConnHasFunc], string(fi.Function.ID))
		}
		for _, v := range pr.ExternalVars {
			gt.ExternalVars[v.ID] = v
			newFile.Connections[golang.ConnHasVar] = append(newFile.Connections[golang.ConnHasVar], string(v.ID))
		}
		newFile.Connections = uniqueConns(newFile.Connections)
		gt.Files[golang.FileID(absPath)] = newFile

		pkg, exists := gt.Packages[pkgPath]
		if !exists {
			pkg = golang.GolangPackage{Path: pkgPath, Connections: make(map[golang.ConnectionKind][]string)}
		}
		if pkg.Connections == nil {
			pkg.Connections = make(map[golang.ConnectionKind][]string)
		}
		pkg.Connections[golang.ConnHasFile] = append(pkg.Connections[golang.ConnHasFile], absPath)
		pkg.Connections[golang.ConnHasFunc] = append(pkg.Connections[golang.ConnHasFunc], newFile.Connections[golang.ConnHasFunc]...)
		pkg.Connections[golang.ConnHasStruct] = append(pkg.Connections[golang.ConnHasStruct], newFile.Connections[golang.ConnHasStruct]...)
		pkg.Connections[golang.ConnHasIface] = append(pkg.Connections[golang.ConnHasIface], newFile.Connections[golang.ConnHasIface]...)
		pkg.Connections[golang.ConnHasNamedType] = append(pkg.Connections[golang.ConnHasNamedType], newFile.Connections[golang.ConnHasNamedType]...)
		pkg.Connections[golang.ConnHasVar] = append(pkg.Connections[golang.ConnHasVar], newFile.Connections[golang.ConnHasVar]...)
		pkg.Connections = uniqueConns(pkg.Connections)
		gt.Packages[pkgPath] = pkg

		for _, fi := range pr.Functions {
			s.clearReanalyzedFunctionWarnings(gt, fi.Function.ID)
			if fi.Body != nil {
				conns := analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID, fi.TypeParamNames)
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

		passes.structMethods(gt, pr)
		passes.constructors(gt, pr)
		populateNamedTypeUsage(gt, []*ParseResult{pr})
		s.resolveWarnings(gt, pr, nil, nil, nil)
		passes.matchInterfaces(gt, pr, nil, nil)
		passes.collectDeps(gt)
		delete(gt.Errors, absPath)
		return warnings, nil
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
	oldNamedTypes := make(map[golang.NamedTypeID]golang.GolangNamedType)
	for _, nid := range oldFile.NamedTypes() {
		if nt, ok := gt.NamedTypes[nid]; ok {
			oldNamedTypes[nid] = nt
		}
	}
	oldExtVars := make(map[golang.ExternalVarID]golang.GolangExternalVar)
	for _, vid := range oldFile.ExternalVars() {
		if v, ok := gt.ExternalVars[vid]; ok {
			oldExtVars[vid] = v
		}
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
	for i, nt := range pr.NamedTypes {
		if oldNT, ok := oldNamedTypes[nt.ID]; ok && nt.Description == "" && oldNT.Description != "" {
			pr.NamedTypes[i].Description = oldNT.Description
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
					callers := passes.getCallers(gt, string(fid), string(golang.ConnCalls))
					for _, callerID := range callers {
						if _, isOld := oldFunctions[golang.FunctionID(callerID)]; isOld {
							continue
						}
						warnID := callerID + "@" + string(domain.WarnSignatureChanged) + "@" + string(fid)
						warning := domain.TopologyWarning{
							ID:       warnID,
							SourceID: string(fid),
							Kind:     domain.WarnSignatureChanged,
							TargetID: callerID,
							Message:  fmt.Sprintf("function %s changed input/output format, verify caller %s", oldFunc.Name, callerID),
						}
						gt.Warnings[warnID] = warning
						warnings = append(warnings, warning)
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

	removedNamedTypes := make(map[golang.NamedTypeID]golang.GolangNamedType)
	for nid := range oldNamedTypes {
		found := false
		for _, nt := range pr.NamedTypes {
			if nt.ID == nid {
				found = true
				break
			}
		}
		if !found {
			removedNamedTypes[nid] = oldNamedTypes[nid]
		}
	}

	for fid, oldFunc := range removedFuncs {
		callers := passes.getCallers(gt, string(fid), string(golang.ConnCalls))
		for _, callerID := range callers {
			if _, isOld := oldFunctions[golang.FunctionID(callerID)]; isOld {
				continue
			}
			warnID := callerID + "@" + string(domain.WarnNodeRemoved) + "@" + string(fid)
			warning := domain.TopologyWarning{
				ID:       warnID,
				SourceID: callerID,
				Kind:     domain.WarnNodeRemoved,
				TargetID: string(fid),
				Message:  fmt.Sprintf("function %s calls %s which was removed from %s", callerID, oldFunc.Name, absPath),
			}
			gt.Warnings[warnID] = warning
			warnings = append(warnings, warning)
			if callerFn, ok := gt.Functions[golang.FunctionID(callerID)]; ok {
				callerFn.Connections[golang.ConnCalls] = removeString(callerFn.Connections[golang.ConnCalls], string(fid))
				gt.Functions[golang.FunctionID(callerID)] = callerFn
			}
		}

		structUsers := passes.getCallers(gt, string(fid), string(golang.ConnUsesStruct))
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

	for sid, oldStruct := range removedStructs {
		structUsers := passes.getCallers(gt, string(sid), string(golang.ConnUsesStruct))
		for _, sourceID := range structUsers {
			if sourceFn, ok := gt.Functions[golang.FunctionID(sourceID)]; ok {
				sourceFn.Connections[golang.ConnUsesStruct] = removeString(sourceFn.Connections[golang.ConnUsesStruct], string(sid))
				gt.Functions[golang.FunctionID(sourceID)] = sourceFn
			}
			warnID := sourceID + "@" + string(domain.WarnNodeRemoved) + "@" + string(sid)
			warning := domain.TopologyWarning{
				ID:       warnID,
				SourceID: sourceID,
				Kind:     domain.WarnNodeRemoved,
				TargetID: string(sid),
				Message:  fmt.Sprintf("function %s uses struct %s which was removed from %s", sourceID, oldStruct.Name, absPath),
			}
			gt.Warnings[warnID] = warning
			warnings = append(warnings, warning)
		}
	}

	for nid, oldNT := range removedNamedTypes {
		ntUsers := passes.getCallers(gt, string(nid), string(golang.ConnUsesNamedType))
		for _, sourceID := range ntUsers {
			if sourceFn, ok := gt.Functions[golang.FunctionID(sourceID)]; ok {
				sourceFn.Connections[golang.ConnUsesNamedType] = removeString(sourceFn.Connections[golang.ConnUsesNamedType], string(nid))
				gt.Functions[golang.FunctionID(sourceID)] = sourceFn
			}
			warnID := sourceID + "@" + string(domain.WarnNodeRemoved) + "@" + string(nid)
			warning := domain.TopologyWarning{
				ID:       warnID,
				SourceID: sourceID,
				Kind:     domain.WarnNodeRemoved,
				TargetID: string(nid),
				Message:  fmt.Sprintf("function %s uses named type %s which was removed from %s", sourceID, oldNT.Name, absPath),
			}
			gt.Warnings[warnID] = warning
			warnings = append(warnings, warning)
		}
	}

	pkg := gt.Packages[oldFile.FromPackage]
	pkg.Connections[golang.ConnHasFile] = removeString(pkg.Connections[golang.ConnHasFile], string(oldFile.ID))
	pkg.Connections[golang.ConnHasFunc] = removeStrings(pkg.Connections[golang.ConnHasFunc], castFuncIDs(oldFile.Functions())...)
	pkg.Connections[golang.ConnHasStruct] = removeStrings(pkg.Connections[golang.ConnHasStruct], castStructIDs(oldFile.Structs())...)
	pkg.Connections[golang.ConnHasIface] = removeStrings(pkg.Connections[golang.ConnHasIface], castInterfaceIDs(oldFile.Interfaces())...)
	pkg.Connections[golang.ConnHasNamedType] = removeStrings(pkg.Connections[golang.ConnHasNamedType], castNamedTypeIDs(oldFile.NamedTypes())...)
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
	for _, nid := range oldFile.NamedTypes() {
		delete(gt.NamedTypes, nid)
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
	for _, nt := range pr.NamedTypes {
		gt.NamedTypes[nt.ID] = nt
		newFile.Connections[golang.ConnHasNamedType] = append(newFile.Connections[golang.ConnHasNamedType], string(nt.ID))
	}
	for _, fi := range pr.Functions {
		gt.Functions[fi.Function.ID] = fi.Function
		newFile.Connections[golang.ConnHasFunc] = append(newFile.Connections[golang.ConnHasFunc], string(fi.Function.ID))
	}
	for _, v := range pr.ExternalVars {
		gt.ExternalVars[v.ID] = v
		newFile.Connections[golang.ConnHasVar] = append(newFile.Connections[golang.ConnHasVar], string(v.ID))
	}
	newFile.Connections = uniqueConns(newFile.Connections)
	gt.Files[golang.FileID(absPath)] = newFile

	pkg = gt.Packages[pkgPath]
	if pkg.Connections == nil {
		pkg.Connections = make(map[golang.ConnectionKind][]string)
	}
	pkg.Connections[golang.ConnHasFile] = append(pkg.Connections[golang.ConnHasFile], absPath)
	pkg.Connections[golang.ConnHasFunc] = append(pkg.Connections[golang.ConnHasFunc], newFile.Connections[golang.ConnHasFunc]...)
	pkg.Connections[golang.ConnHasStruct] = append(pkg.Connections[golang.ConnHasStruct], newFile.Connections[golang.ConnHasStruct]...)
	pkg.Connections[golang.ConnHasIface] = append(pkg.Connections[golang.ConnHasIface], newFile.Connections[golang.ConnHasIface]...)
	pkg.Connections[golang.ConnHasNamedType] = append(pkg.Connections[golang.ConnHasNamedType], newFile.Connections[golang.ConnHasNamedType]...)
	pkg.Connections[golang.ConnHasVar] = append(pkg.Connections[golang.ConnHasVar], newFile.Connections[golang.ConnHasVar]...)
	pkg.Connections = uniqueConns(pkg.Connections)
	gt.Packages[pkgPath] = pkg

	passes.structMethods(gt, pr)
	passes.constructors(gt, pr)
	populateNamedTypeUsage(gt, []*ParseResult{pr})

	for _, fi := range pr.Functions {
		s.clearReanalyzedFunctionWarnings(gt, fi.Function.ID)
		if fi.Body != nil {
			conns := analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID, fi.TypeParamNames)
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

	s.resolveWarnings(gt, pr, removedFuncs, removedStructs, removedNamedTypes)

	passes.matchInterfaces(gt, pr, removedStructs, removedFuncs)
	passes.collectDeps(gt)
	delete(gt.Errors, absPath)

	return warnings, nil
}

// Removes topology warnings related to a reanalyzed function.
//
// signature_changed is deliberately NOT handled here any more. Dropping it because the
// caller was re-parsed cannot tell a fix from a comment -- touching a caller silenced a
// warning that was still true -- and the check that can tell them apart needs the recorded
// call sites, which live on the domain resource rather than in this typed topology. The
// manager's ClearReferrerWarningsForFile now owns that case for every language, and runs on
// this path too (manager.go's UpdateFile and the batch resolve set), so removing it here
// loses no coverage and removes a second, looser copy of the same rule.
func (s *GoScanner) clearReanalyzedFunctionWarnings(gt *golang.GolangTopology, functionID golang.FunctionID) {
	id := string(functionID)
	for warnID, w := range gt.Warnings {
		switch w.Kind {
		case domain.WarnUseMissingNode, domain.WarnNodeRemoved:
			if w.SourceID == id {
				delete(gt.Warnings, warnID)
			}
		}
	}
}

// Clears topology warnings when their target nodes are re-added or removed from the codebase.
func (s *GoScanner) resolveWarnings(gt *golang.GolangTopology, pr *ParseResult, removedFuncs map[golang.FunctionID]golang.GolangFunction, removedStructs map[golang.StructID]golang.GolangStruct, removedNamedTypes map[golang.NamedTypeID]golang.GolangNamedType) {
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
	for _, nt := range pr.NamedTypes {
		newIDs[string(nt.ID)] = true
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
			if _, removed := removedNamedTypes[golang.NamedTypeID(w.TargetID)]; removed {
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
		if w.Kind == domain.WarnSignatureChanged {
			if _, removed := removedFuncs[golang.FunctionID(w.TargetID)]; removed {
				delete(gt.Warnings, warnID)
			}
		}
	}
}

// Resolves a use-missing warning by adding the appropriate connection type and cross-package dependency
func (s *GoScanner) resolveUseMissingWarning(gt *golang.GolangTopology, w domain.TopologyWarning) {
	sourceFn, ok := gt.Functions[golang.FunctionID(w.SourceID)]
	if !ok {
		return
	}

	// When a use-missing warning resolves to a type in another package, the cold
	// path always pairs the type edge (uses_struct/uses_named_type/uses_interface
	// or the call edge) with the sibling uses_package edge for that type's
	// package (see resolveCompositeLit / resolveQualifiedCall). Reproduce that
	// rollup here so the incremental path does not drop uses_package when a
	// previously-missing cross-package type later appears.
	srcPkg := s.sourceFunctionPackage(sourceFn)
	addCrossPkg := func() {
		tgtPkg := trimLastDotSegment(w.TargetID)
		if tgtPkg != "" && tgtPkg != srcPkg {
			sourceFn.Connections[golang.ConnUsesPkg] = append(sourceFn.Connections[golang.ConnUsesPkg], tgtPkg)
		}
	}

	switch {
	case s.existsInFunctions(gt, w.TargetID):
		sourceFn.Connections[golang.ConnCalls] = append(sourceFn.Connections[golang.ConnCalls], w.TargetID)
		addCrossPkg()
	case s.existsInStructs(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesStruct] = append(sourceFn.Connections[golang.ConnUsesStruct], w.TargetID)
		addCrossPkg()
	case s.existsInNamedTypes(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesNamedType] = append(sourceFn.Connections[golang.ConnUsesNamedType], w.TargetID)
		addCrossPkg()
	case s.existsInInterfaces(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesIface] = append(sourceFn.Connections[golang.ConnUsesIface], w.TargetID)
		addCrossPkg()
	case s.existsInExtVars(gt, w.TargetID):
		sourceFn.Connections[golang.ConnUsesExtVar] = append(sourceFn.Connections[golang.ConnUsesExtVar], w.TargetID)
	}

	sourceFn.Connections = uniqueConns(sourceFn.Connections)
	gt.Functions[golang.FunctionID(w.SourceID)] = sourceFn
}

// sourceFunctionPackage returns the package path of fn. Methods carry their
// owning package in MethodFrom (a "pkg.Recv" struct id); plain funcs carry it
// in their own "pkg.Name" id. Both reduce to the package by dropping the final
// ".Name"/".Recv" segment.
func (s *GoScanner) sourceFunctionPackage(fn golang.GolangFunction) string {
	if fn.MethodFrom != nil {
		return trimLastDotSegment(string(*fn.MethodFrom))
	}
	return trimLastDotSegment(string(fn.ID))
}

// Checks if function ID exists in topology.
func (s *GoScanner) existsInFunctions(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Functions[golang.FunctionID(id)]
	return ok
}

// Checks whether an ID exists in the structs map
func (s *GoScanner) existsInStructs(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Structs[golang.StructID(id)]
	return ok
}

// Checks whether an ID exists in the named types map
func (s *GoScanner) existsInNamedTypes(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.NamedTypes[golang.NamedTypeID(id)]
	return ok
}

// Checks whether an ID exists in the interfaces map
func (s *GoScanner) existsInInterfaces(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.Interfaces[golang.InterfaceID(id)]
	return ok
}

// Checks if external variable ID exists in topology.
func (s *GoScanner) existsInExtVars(gt *golang.GolangTopology, id string) bool {
	_, ok := gt.ExternalVars[golang.ExternalVarID(id)]
	return ok
}

// Finds all functions and structs that call or reference a target ID by a given connection type
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
	for id, str := range gt.Structs {
		for _, callID := range str.Connections[golang.ConnectionKind(connType)] {
			if callID == targetID {
				callers = append(callers, string(id))
				break
			}
		}
	}
	return callers
}

// Reads the module declaration from go.mod in the given root directory and returns the module path.
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

// Computes the full package path for a directory relative to the module root.
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

// Recursively walks a directory tree and returns all non-test .go files, skipping vendor, .git, node_modules, and hidden directories.
func collectGoFiles(root string) []string {
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
			if name == "vendor" || name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			if domain.PathHidden(path) {
				return nil
			}
			files = append(files, path)
		}
		return nil
	})
	return files
}

// Rebuilds the HasMethod connections for structs by iterating through all functions and linking methods to their receiver types.
func populateStructMethods(gt *golang.GolangTopology) {
	for id, str := range gt.Structs {
		delete(str.Connections, golang.ConnHasMethod)
		gt.Structs[id] = str
	}
	for _, f := range gt.Functions {
		if f.MethodFrom != nil {
			str, exists := gt.Structs[*f.MethodFrom]
			if !exists {
				continue
			}
			if str.Connections == nil {
				str.Connections = make(map[golang.ConnectionKind][]string)
			}
			str.Connections[golang.ConnHasMethod] = append(str.Connections[golang.ConnHasMethod], string(f.ID))
			gt.Structs[*f.MethodFrom] = str
		}
	}
}

// Links New-prefixed functions to their struct constructors by matching return types.
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

// Collects all unique imported dependencies from Go files in the topology, deduplicating by package path.
func collectDependencies(gt *golang.GolangTopology) {
	seen := make(map[golang.DependancyPath]bool)
	gt.Dependencies = nil
	for _, file := range gt.Files {
		for _, dep := range file.DependenciesImported() {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				gt.Dependencies = append(gt.Dependencies, dep)
			}
		}
	}
}

var goTypeTokenPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?`)

// Extracts and deduplicates named type references from function/struct/interface parameter and result types across parse results.
func populateNamedTypeUsage(gt *golang.GolangTopology, parseResults []*ParseResult) {
	for _, pr := range parseResults {
		if pr == nil {
			continue
		}
		for _, fnParse := range pr.Functions {
			fn, ok := gt.Functions[fnParse.Function.ID]
			if !ok {
				continue
			}
			for _, param := range fn.Input {
				addNamedTypeRefs(fn.Connections, namedTypeRefsFromType(param.Typing, pr, gt), string(fn.ID))
			}
			for _, result := range fn.Output {
				addNamedTypeRefs(fn.Connections, namedTypeRefsFromType(result.Typing, pr, gt), string(fn.ID))
			}
			fn.Connections = uniqueConns(fn.Connections)
			gt.Functions[fn.ID] = fn
		}
		for _, parsedStruct := range pr.Structs {
			str, ok := gt.Structs[parsedStruct.ID]
			if !ok {
				continue
			}
			for _, param := range str.Params {
				addNamedTypeRefs(str.Connections, namedTypeRefsFromType(param.Typing, pr, gt), string(str.ID))
			}
			str.Connections = uniqueConns(str.Connections)
			gt.Structs[str.ID] = str
		}
		for _, parsedIface := range pr.Interfaces {
			iface, ok := gt.Interfaces[parsedIface.ID]
			if !ok {
				continue
			}
			for _, method := range iface.Methods {
				for _, param := range method.Input {
					addNamedTypeRefs(iface.Connections, namedTypeRefsFromType(param.Typing, pr, gt), string(iface.ID))
				}
				for _, result := range method.Output {
					addNamedTypeRefs(iface.Connections, namedTypeRefsFromType(result.Typing, pr, gt), string(iface.ID))
				}
			}
			iface.Connections = uniqueConns(iface.Connections)
			gt.Interfaces[iface.ID] = iface
		}
		for _, parsedNamedType := range pr.NamedTypes {
			nt, ok := gt.NamedTypes[parsedNamedType.ID]
			if !ok {
				continue
			}
			addNamedTypeRefs(nt.Connections, namedTypeRefsFromType(nt.Underlying, pr, gt), string(nt.ID))
			nt.Connections = uniqueConns(nt.Connections)
			gt.NamedTypes[nt.ID] = nt
		}
	}
}

// Extracts all named type references (structs/interfaces) from a type string, filtering out builtins and external imports.
func namedTypeRefsFromType(typing string, pr *ParseResult, gt *golang.GolangTopology) []golang.NamedTypeID {
	var refs []golang.NamedTypeID
	for _, token := range goTypeTokenPattern.FindAllString(typing, -1) {
		if token == "" || goBuiltins[token] {
			continue
		}

		var id golang.NamedTypeID
		if strings.Contains(token, ".") {
			parts := strings.SplitN(token, ".", 2)
			importPath, ok := pr.ImportMap[parts[0]]
			if !ok || !isInternalImport(importPath, pr.ModulePath) {
				continue
			}
			id = golang.NamedTypeID(importPath + "." + parts[1])
		} else {
			id = golang.NamedTypeID(string(pr.PkgPath) + "." + token)
		}

		if _, ok := gt.NamedTypes[id]; ok && !containsNamedTypeID(refs, id) {
			refs = append(refs, id)
		}
	}
	return refs
}

// Records named type references as connection edges, skipping self-references and duplicates.
func addNamedTypeRefs(conns map[golang.ConnectionKind][]string, refs []golang.NamedTypeID, ownerID string) {
	if conns == nil || len(refs) == 0 {
		return
	}
	for _, ref := range refs {
		if string(ref) == ownerID {
			continue
		}
		if !containsString(conns[golang.ConnUsesNamedType], string(ref)) {
			conns[golang.ConnUsesNamedType] = append(conns[golang.ConnUsesNamedType], string(ref))
		}
	}
}

// Checks whether a NamedTypeID exists in a slice.
func containsNamedTypeID(ids []golang.NamedTypeID, target golang.NamedTypeID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// Deduplicates connection lists by kind, removing duplicate IDs while preserving order and omitting empty lists.
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

// Compares two function signatures for equality based on input and output types.
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

// Filters a string slice to exclude a specific item and returns the result.
func removeString(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// Filters a string slice to exclude specified items.
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

// Converts a slice of FunctionID to a string slice.
func castFuncIDs(ids []golang.FunctionID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

// Converts a slice of StructIDs to []string by casting each element.
func castStructIDs(ids []golang.StructID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

// Converts a slice of InterfaceID to a string slice.
func castInterfaceIDs(ids []golang.InterfaceID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

// Converts a slice of ExternalVarID to a string slice.
func castExtVarIDs(ids []golang.ExternalVarID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

// Converts a slice of NamedTypeID to a string slice.
func castNamedTypeIDs(ids []golang.NamedTypeID) []string {
	var result []string
	for _, id := range ids {
		result = append(result, string(id))
	}
	return result
}

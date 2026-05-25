// Package analyzer implements the core topology extraction pipeline for Go
// projects. It walks the filesystem, parses every .go file with the standard
// library's go/ast, extracts declarations into the domain types, resolves
// inter-element references by analyzing function bodies, and matches structs
// to interfaces via method signature comparison.
package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llm-topology/internal/topology/domain"
)

// Scan is the top-level entry point for topology extraction. It performs
// a multi-phase analysis: (1) walk the directory tree and collect all .go
// files grouped by package, (2) parse every file to extract declarations
// (structs, interfaces, functions, variables), (3) populate reverse indexes
// such as struct methods and constructor detection, (4) analyze each function
// body to resolve call graph and type usage references, (5) match structs to
// interfaces by comparing method signatures, and (6) collect the unique set
// of external dependencies across all files.
func Scan(root string) (*domain.Topology, error) {
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

	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read module path: %w", err)
	}

	topo := &domain.Topology{
		Root:         absRoot,
		Packages:     make(map[domain.PackagePath]domain.Package),
		Files:        make(map[domain.FilePath]domain.File),
		Struct:       make(map[domain.StructID]domain.Struct),
		Interfaces:   make(map[domain.InterfaceID]domain.Interface),
		Functions:    make(map[domain.FunctionID]domain.Function),
		ExternalVars: make(map[domain.ExternalVarID]domain.ExternalVar),
		Errors:       make(map[domain.FilePath]string),
	}

	goFiles := collectGoFiles(absRoot)

	dirFiles := make(map[string][]string)
	for _, f := range goFiles {
		dir := filepath.Dir(f)
		dirFiles[dir] = append(dirFiles[dir], f)
	}

	type fileParse struct {
		filePath string
		result   *ParseResult
		err      error
	}

	var parseResults []fileParse

	for dir, files := range dirFiles {
		pkgPath := GetPackagePath(absRoot, dir, modulePath)

		pkg, ok := topo.Packages[pkgPath]
		if !ok {
			pkg = domain.Package{Path: pkgPath}
		}

		for _, filePath := range files {
			pr, err := ParseFile(filePath, pkgPath, modulePath, absRoot)
			if err != nil {
				topo.Errors[domain.FilePath(filePath)] = err.Error()
				continue
			}

			parseResults = append(parseResults, fileParse{filePath: filePath, result: pr})

			file := domain.File{
				Path:                 domain.FilePath(filePath),
				Name:                 filepath.Base(filePath),
				Description:          pr.FileDescription,
				PackagesImported:     pr.InternalImports,
				DependanciesImported: pr.ExternalImports,
			}
			topo.Files[domain.FilePath(filePath)] = file
			pkg.Files = append(pkg.Files, domain.FilePath(filePath))
		}

		topo.Packages[pkgPath] = pkg
	}

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
		filePath := domain.FilePath(fp.filePath)
		file := topo.Files[filePath]

		for _, s := range fp.result.Structs {
			topo.Struct[s.Struct.ID] = s.Struct
			file.Structs = append(file.Structs, s.Struct.ID)
		}
		for _, iface := range fp.result.Interfaces {
			topo.Interfaces[iface.Interface.ID] = iface.Interface
			file.Interfaces = append(file.Interfaces, iface.Interface.ID)
		}
		for _, f := range fp.result.Functions {
			topo.Functions[f.Function.ID] = f.Function
			file.Functions = append(file.Functions, f.Function.ID)
		}
		for _, v := range fp.result.ExternalVars {
			topo.ExternalVars[v.ID] = v
			file.ExternalVars = append(file.ExternalVars, v.ID)
		}

		topo.Files[filePath] = file

		pkg := topo.Packages[file.FromPackage]
		pkg.Functions = append(pkg.Functions, file.Functions...)
		pkg.Structs = append(pkg.Structs, file.Structs...)
		pkg.Interfaces = append(pkg.Interfaces, file.Interfaces...)
		pkg.ExternalVars = append(pkg.ExternalVars, file.ExternalVars...)
		topo.Packages[file.FromPackage] = pkg
	}

	populateStructMethods(topo)

	detectConstructors(topo)

	for _, fp := range parseResults {
		if fp.err != nil || fp.result == nil {
			continue
		}
		for _, fi := range fp.result.Functions {
			if fi.Body != nil {
				rel := analyzeFunctionBody(fi.Body, fp.result, topo)
				f := topo.Functions[fi.Function.ID]
				f.FunctionsUsed = append(f.FunctionsUsed, rel.functionsUsed...)
				f.StructsUsed = append(f.StructsUsed, rel.structsUsed...)
				f.InterfacesUsed = append(f.InterfacesUsed, rel.interfacesUsed...)
				f.ExternalVarsUsed = append(f.ExternalVarsUsed, rel.externalVarsUsed...)
				f.PackagesUsed = append(f.PackagesUsed, rel.packagesUsed...)
				f.DependanciesUsed = append(f.DependanciesUsed, rel.dependenciesUsed...)
				deduplicateSlices(&f)
				topo.Functions[f.ID] = f
			}
		}
	}

	matchStructsToInterfaces(topo)

	collectDependencies(topo)

	return topo, nil
}

// ReadModulePath reads the go.mod file at the project root and extracts the
// module declaration. This module path is used as the base for constructing
// PackagePath values and for distinguishing internal imports (same module)
// from external third-party dependencies during import processing.
func ReadModulePath(root string) (string, error) {
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

// GetPackagePath derives the Go import path for a directory relative to the
// module root. Directories at the root level map to the bare module path, while
// subdirectories produce the module path joined with the relative filesystem
// path, using forward slashes for consistency with Go import conventions.
func GetPackagePath(root, dir, modulePath string) domain.PackagePath {
	if dir == root {
		return domain.PackagePath(modulePath)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return domain.PackagePath(modulePath + "/" + filepath.Base(dir))
	}
	return domain.PackagePath(modulePath + "/" + strings.ReplaceAll(rel, "\\", "/"))
}

// collectGoFiles recursively walks the given root directory to discover all
// .go source files, skipping vendor directories, .git folders, hidden
// directories, and _test.go files to focus on production code only. The
// returned file list is the raw input for the subsequent parsing phase.
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

// populateStructMethods performs a reverse-index pass over all functions to
// attach each method to its parent struct. After the initial parse phase,
// functions with a non-nil MethodFrom pointer need to be added to the
// corresponding struct's Methods slice so that struct-to-interface matching
// and method lookup have a complete picture of each struct's capabilities.
func populateStructMethods(topo *domain.Topology) {
	for _, f := range topo.Functions {
		if f.MethodFrom != nil {
			str := topo.Struct[*f.MethodFrom]
			str.Methods = append(str.Methods, f.ID)
			topo.Struct[*f.MethodFrom] = str
		}
	}
}

// detectConstructors identifies constructor functions following the common Go
// convention of functions named "New" or "New<TypeName>" that return a struct
// type or a pointer to it. When a matching constructor is found, the struct's
// Constructor field is set to point back to the function, providing a useful
// shortcut for consumers of the topology to locate initialization patterns.
func detectConstructors(topo *domain.Topology) {
	for pkgPath, pkg := range topo.Packages {
		for _, funcID := range pkg.Functions {
			f := topo.Functions[funcID]
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
			structID := domain.StructID(string(pkgPath) + "." + returnType)
			if _, exists := topo.Struct[structID]; exists {
				topo.Functions[funcID] = f
				str := topo.Struct[structID]
				str.Constructor = &f.ID
				topo.Struct[structID] = str
				continue
			}
			if strings.HasPrefix(returnType, "*") {
				structID = domain.StructID(string(pkgPath) + "." + returnType[1:])
				if _, exists := topo.Struct[structID]; exists {
					str := topo.Struct[structID]
					str.Constructor = &f.ID
					topo.Struct[structID] = str
				}
			}
		}
	}
}

// collectDependencies aggregates all unique external package paths across
// every file in the topology into a single deduplicated list. This produces
// a complete inventory of third-party packages that the analyzed project
// depends on, stored as a top-level field in the Topology output.
func collectDependencies(topo *domain.Topology) {
	seen := make(map[domain.DependancyPath]bool)
	for _, file := range topo.Files {
		for _, dep := range file.DependanciesImported {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				topo.Dependancies = append(topo.Dependancies, dep)
			}
		}
	}
}

// deduplicateSlices removes duplicate IDs from all relationship slices on a
// Function. During body analysis, the same function or type may be referenced
// multiple times, but the topology contract requires each ID to appear at most
// once per relationship category to keep the output clean and predictable.
func deduplicateSlices(f *domain.Function) {
	f.FunctionsUsed = unique(f.FunctionsUsed)
	f.StructsUsed = unique(f.StructsUsed)
	f.InterfacesUsed = unique(f.InterfacesUsed)
	f.ExternalVarsUsed = unique(f.ExternalVarsUsed)
	f.PackagesUsed = unique(f.PackagesUsed)
	f.DependanciesUsed = unique(f.DependanciesUsed)
}

// unique is a generic helper that deduplicates any comparable-type slice by
// tracking seen elements in a map. It preserves insertion order, returning
// a new slice containing only the first occurrence of each element. Used by
// deduplicateSlices across all six relationship categories.
func unique[T comparable](slice []T) []T {
	seen := make(map[T]bool)
	var result []T
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// UpdateFileInTopology re-parses a single .go file and updates the topology
// in-place. It compares the old file data (loaded from topo) with the newly
// parsed result, returns warnings for removed or signature-changed functions,
// preserves existing descriptions when the new source omits doc comments,
// and re-runs the structural post-processing phases (struct methods,
// constructors, function body analysis, interface matching, dependency
// collection). Callers must persist the topology afterward.
func UpdateFileInTopology(topo *domain.Topology, filePath, rootPath string) []domain.TopologyWarning {
	var warnings []domain.TopologyWarning

	modulePath, err := ReadModulePath(rootPath)
	if err != nil {
		return warnings
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return warnings
	}

	oldFile, hasFile := topo.Files[domain.FilePath(absPath)]
	if !hasFile {
		return warnings
	}

	oldFunctions := make(map[domain.FunctionID]domain.Function)
	for _, fid := range oldFile.Functions {
		if f, ok := topo.Functions[fid]; ok {
			oldFunctions[fid] = f
		}
	}
	oldStructs := make(map[domain.StructID]domain.Struct)
	for _, sid := range oldFile.Structs {
		if s, ok := topo.Struct[sid]; ok {
			oldStructs[sid] = s
		}
	}
	oldInterfaces := make(map[domain.InterfaceID]domain.Interface)
	for _, iid := range oldFile.Interfaces {
		if iface, ok := topo.Interfaces[iid]; ok {
			oldInterfaces[iid] = iface
		}
	}
	oldExtVars := make(map[domain.ExternalVarID]domain.ExternalVar)
	for _, vid := range oldFile.ExternalVars {
		if v, ok := topo.ExternalVars[vid]; ok {
			oldExtVars[vid] = v
		}
	}

	dir := filepath.Dir(absPath)
	pkgPath := GetPackagePath(rootPath, dir, modulePath)

	pr, err := ParseFile(absPath, pkgPath, modulePath, rootPath)
	if err != nil {
		topo.Errors[domain.FilePath(absPath)] = err.Error()
		return warnings
	}

	for i, fi := range pr.Functions {
		if oldFunc, ok := oldFunctions[fi.Function.ID]; ok && fi.Function.Description == "" && oldFunc.Description != "" {
			pr.Functions[i].Function.Description = oldFunc.Description
		}
	}
	for i, si := range pr.Structs {
		if oldStruct, ok := oldStructs[si.Struct.ID]; ok && si.Struct.Description == "" && oldStruct.Description != "" {
			pr.Structs[i].Struct.Description = oldStruct.Description
		}
	}
	for i, ii := range pr.Interfaces {
		if oldIface, ok := oldInterfaces[ii.Interface.ID]; ok && ii.Interface.Description == "" && oldIface.Description != "" {
			pr.Interfaces[i].Interface.Description = oldIface.Description
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

	for fid, oldFunc := range oldFunctions {
		found := false
		for _, fi := range pr.Functions {
			if fi.Function.ID == fid {
				found = true
				if !signaturesEqualFn(oldFunc, fi.Function) {
					warnings = append(warnings, domain.TopologyWarning{
						Resource:          domain.FUNCTION_RESOURCE,
						AffectedFunctions: []domain.FunctionID{fid},
						Message:           fmt.Sprintf("function %s changed input/output format, verify callers", oldFunc.Name),
					})
				}
				break
			}
		}
		if !found {
			warnings = append(warnings, domain.TopologyWarning{
				Resource:          domain.FUNCTION_RESOURCE,
				AffectedFunctions: []domain.FunctionID{fid},
				Message:           fmt.Sprintf("function %s was removed, update or remove all references", oldFunc.Name),
			})
		}
	}

	pkg := topo.Packages[oldFile.FromPackage]
	pkg.Files = removeFromSlice(pkg.Files, oldFile.Path)
	pkg.Functions = removeFromSlice(pkg.Functions, oldFile.Functions...)
	pkg.Structs = removeFromSlice(pkg.Structs, oldFile.Structs...)
	pkg.Interfaces = removeFromSlice(pkg.Interfaces, oldFile.Interfaces...)
	pkg.ExternalVars = removeFromSlice(pkg.ExternalVars, oldFile.ExternalVars...)
	topo.Packages[oldFile.FromPackage] = pkg

	for _, fid := range oldFile.Functions {
		delete(topo.Functions, fid)
	}
	for _, sid := range oldFile.Structs {
		delete(topo.Struct, sid)
	}
	for _, iid := range oldFile.Interfaces {
		delete(topo.Interfaces, iid)
	}
	for _, vid := range oldFile.ExternalVars {
		delete(topo.ExternalVars, vid)
	}
	delete(topo.Files, oldFile.Path)

	newFile := domain.File{
		Path:                 domain.FilePath(absPath),
		Name:                 filepath.Base(absPath),
		Description:          pr.FileDescription,
		FromPackage:          pkgPath,
		PackagesImported:     pr.InternalImports,
		DependanciesImported: pr.ExternalImports,
	}
	for _, si := range pr.Structs {
		topo.Struct[si.Struct.ID] = si.Struct
		newFile.Structs = append(newFile.Structs, si.Struct.ID)
	}
	for _, ii := range pr.Interfaces {
		topo.Interfaces[ii.Interface.ID] = ii.Interface
		newFile.Interfaces = append(newFile.Interfaces, ii.Interface.ID)
	}
	for _, fi := range pr.Functions {
		topo.Functions[fi.Function.ID] = fi.Function
		newFile.Functions = append(newFile.Functions, fi.Function.ID)
	}
	for _, v := range pr.ExternalVars {
		topo.ExternalVars[v.ID] = v
		newFile.ExternalVars = append(newFile.ExternalVars, v.ID)
	}
	topo.Files[domain.FilePath(absPath)] = newFile

	pkg = topo.Packages[pkgPath]
	pkg.Files = append(pkg.Files, domain.FilePath(absPath))
	pkg.Functions = append(pkg.Functions, newFile.Functions...)
	pkg.Structs = append(pkg.Structs, newFile.Structs...)
	pkg.Interfaces = append(pkg.Interfaces, newFile.Interfaces...)
	pkg.ExternalVars = append(pkg.ExternalVars, newFile.ExternalVars...)
	topo.Packages[pkgPath] = pkg

	populateStructMethods(topo)
	detectConstructors(topo)

	for _, fi := range pr.Functions {
		if fi.Body != nil {
			rel := analyzeFunctionBody(fi.Body, pr, topo)
			f := topo.Functions[fi.Function.ID]
			f.FunctionsUsed = append(f.FunctionsUsed, rel.functionsUsed...)
			f.StructsUsed = append(f.StructsUsed, rel.structsUsed...)
			f.InterfacesUsed = append(f.InterfacesUsed, rel.interfacesUsed...)
			f.ExternalVarsUsed = append(f.ExternalVarsUsed, rel.externalVarsUsed...)
			f.PackagesUsed = append(f.PackagesUsed, rel.packagesUsed...)
			f.DependanciesUsed = append(f.DependanciesUsed, rel.dependenciesUsed...)
			deduplicateSlices(&f)
			topo.Functions[f.ID] = f
		}
	}

	matchStructsToInterfaces(topo)
	collectDependencies(topo)
	delete(topo.Errors, domain.FilePath(absPath))

	return warnings
}

// signaturesEqualFn checks whether two Function entries have identical
// input parameter and output return type signatures.
func signaturesEqualFn(a, b domain.Function) bool {
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

// removeFromSlice returns a new slice with all occurrences of the given items
// removed. It preserves the original order of remaining elements.
func removeFromSlice[T comparable](slice []T, items ...T) []T {
	var result []T
	for _, s := range slice {
		keep := true
		for _, item := range items {
			if s == item {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, s)
		}
	}
	return result
}

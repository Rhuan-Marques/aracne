package goscanner

import (
	"bytes"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
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

// Detects a Go project: a go.mod or go.work at the root, or any Go source file Scan would index.
// Checking the root go.mod alone disagreed with the registry, which detects Go by its files, and
// with Scan, which indexes a module wherever it sits (see moduleLayout).
func (s *GoScanner) Detect(root string) bool {
	for _, marker := range []string{"go.mod", "go.work"} {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return hasGoSource(absRoot)
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

	layout, err := loadModuleLayout(absRoot)
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
		mod := layout.forDir(filepath.Dir(filePath))
		pr, err := ParseFile(filePath, mod.pkgPath, mod.modulePath, absRoot, mod.siblings...)
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
		pkgPath := pr.PkgPath

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

	// Give every repeated function declaration its own id before anything is keyed on it; see
	// assignDuplicateOrdinals. Pass 2 re-parses, so it re-applies the same ids from finalIDs.
	var parsed []*ParseResult
	for _, rec := range parseResults {
		parsed = append(parsed, rec.result)
	}
	finalIDs := assignScanFunctionIDs(parsed)

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
		mod := layout.forDir(filepath.Dir(filePath))
		pr, err := ParseFile(filePath, mod.pkgPath, mod.modulePath, absRoot, mod.siblings...)
		if err != nil || pr == nil {
			return d
		}
		if ids, ok := finalIDs[filePath]; ok && len(ids) == len(pr.Functions) {
			for i := range pr.Functions {
				pr.Functions[i].Function.ID = ids[i]
			}
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

	layout, err := loadModuleLayout(rootPath)
	if err != nil {
		return warnings, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings, nil
	}

	mod := layout.forDir(filepath.Dir(absPath))
	pkgPath := mod.pkgPath

	pr, err := ParseFile(absPath, pkgPath, mod.modulePath, rootPath, mod.siblings...)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		topo.Resources = golang.ToGeneric(gt).Resources
		topo.Errors = gt.Errors
		topo.Warnings = gt.Warnings
		return warnings, nil
	}

	warnings, err = s.applyFileUpdate(gt, pr, absPath, pkgPath, s.fullPasses(layout))
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
func (s *GoScanner) fullPasses(layout *moduleLayout) gtPasses {
	return gtPasses{
		getCallers:    s.getCallers,
		structMethods: func(gt *golang.GolangTopology, _ *ParseResult) { populateStructMethods(gt) },
		constructors:  func(gt *golang.GolangTopology, _ *ParseResult) { detectConstructors(gt) },
		matchInterfaces: func(gt *golang.GolangTopology, _ *ParseResult, _ map[golang.StructID]golang.GolangStruct, _ map[golang.FunctionID]golang.GolangFunction, _ map[golang.NamedTypeID]golang.GolangNamedType) {
			matchStructsToInterfaces(gt)
		},
		collectDeps: collectDependencies,
		refresh: func(gt *golang.GolangTopology, pr *ParseResult, fns []golang.FunctionID, added addedDecls) error {
			s.refreshOutsideFile(gt, layout, pr, fns, added)
			return nil
		},
	}
}

// refreshOutsideFile re-derives the edges of resources in OTHER files that this update changed
// the meaning of, which is what a cold scan gets for free by resolving everything against the
// final graph:
//
//   - fns are functions whose body named something that did not exist and now does (the target
//     of a use_missing_node or node_removed warning just reappeared). Their bodies are resolved
//     again, so the edge AND its call-site record land exactly as a cold scan writes them;
//     patching a single edge in by the target's kind, as this used to, missed both the call
//     site and anything the fixed call made resolvable further along the same expression.
//   - addedNames are the names of every declaration this update INTRODUCED -- functions,
//     methods, structs, interfaces, named types, package vars. A body elsewhere that already
//     spelled one of them resolved it to nothing and recorded no warning to key a later fix on
//     (`x.NewMethod()` on a typed variable, a bare `Count`, `var b Box`), and a signature that
//     spelled a new named type got its uses_named_type edge only from populateNamedTypeUsage,
//     which ran for that file while the type did not exist. So every file of the package, and
//     every importer of it, whose TEXT mentions one of the names is resolved again in full.
func (s *GoScanner) refreshOutsideFile(gt *golang.GolangTopology, layout *moduleLayout, pr *ParseResult, fns []golang.FunctionID, added addedDecls) {
	files := make(map[string]map[golang.FunctionID]bool)
	for _, id := range fns {
		f, ok := gt.Functions[id]
		if !ok || f.Loc.Path == "" || f.Loc.Path == pr.FileID {
			continue
		}
		if files[f.Loc.Path] == nil {
			files[f.Loc.Path] = make(map[golang.FunctionID]bool)
		}
		files[f.Loc.Path][id] = true
	}
	// A file that only MENTIONS a new name carries no record of where it mentions it, so
	// every function in it is resolved again rather than a named subset.
	whole := make(map[string]bool)
	for _, path := range addedNameReferrerFiles(gt, pr, added) {
		if files[path] == nil {
			files[path] = make(map[golang.FunctionID]bool)
		}
		whole[path] = true
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		s.reresolveFile(gt, layout, path, files[path], whole[path])
	}
}

// addedNameReferrerFiles returns the files, other than the one being updated, that can spell
// one of the added names: the files of its own package, which name it bare, and the files
// importing that package. Only files whose text mentions a name are returned -- a cheap
// prefilter, generous by design.
func addedNameReferrerFiles(gt *golang.GolangTopology, pr *ParseResult, added addedDecls) []string {
	if added.empty() {
		return nil
	}
	var out []string
	for fid, file := range gt.Files {
		if fid == pr.FileID {
			continue
		}
		if file.FromPackage != pr.PkgPath && !containsString(file.Connections[golang.ConnImportsPkg], string(pr.PkgPath)) {
			continue
		}
		if fileMentionsAnyName(fid, added) {
			out = append(out, fid)
		}
	}
	sort.Strings(out)
	return out
}

// fileMentionsAnyName reports whether the file's text spells a reference to one of the added
// names: a plain name as a whole identifier, a method name as a whole identifier directly after
// a dot. Unreadable files report true: re-resolving costs a parse, missing one costs an edge.
func fileMentionsAnyName(path string, added addedDecls) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	isWord := func(c byte) bool {
		return c == '_' || c >= 0x80 || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
	}
	mentions := func(name string, afterDot bool) bool {
		for from := 0; from < len(data); {
			i := bytes.Index(data[from:], []byte(name))
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(name)
			from = start + 1
			if end != len(data) && isWord(data[end]) {
				continue
			}
			if afterDot {
				if start > 0 && data[start-1] == '.' {
					return true
				}
				continue
			}
			if start == 0 || !isWord(data[start-1]) {
				return true
			}
		}
		return false
	}
	for name := range added.plain {
		if mentions(name, false) {
			return true
		}
	}
	for name := range added.methods {
		if mentions(name, true) {
			return true
		}
	}
	return false
}

// reresolveFile re-parses an already-registered file and brings it up to date with the graph
// around it: every signature-level named-type reference is re-derived, and the functions in
// fns have their bodies resolved again from scratch (their parse-time edges, then signature
// references, then body edges -- the order Scan builds them in). Nothing is added or removed:
// the file did not change, the graph it resolves against did.
func (s *GoScanner) reresolveFile(gt *golang.GolangTopology, layout *moduleLayout, path string, fns map[golang.FunctionID]bool, allFns bool) {
	file, ok := gt.Files[golang.FileID(path)]
	if !ok {
		return
	}
	mod := layout.forDir(filepath.Dir(path))
	pr, err := ParseFile(path, file.FromPackage, mod.modulePath, layout.root, mod.siblings...)
	if err != nil {
		return
	}
	planFunctionIDs(gt, pr, path)
	for _, fi := range pr.Functions {
		if !allFns && !fns[fi.Function.ID] {
			continue
		}
		f, ok := gt.Functions[fi.Function.ID]
		if !ok {
			continue
		}
		conns := make(map[golang.ConnectionKind][]string, len(fi.Function.Connections))
		for k, v := range fi.Function.Connections {
			conns[k] = append([]string(nil), v...)
		}
		f.Connections = conns
		gt.Functions[f.ID] = f
	}
	populateNamedTypeUsage(gt, []*ParseResult{pr})
	for _, fi := range pr.Functions {
		if (!allFns && !fns[fi.Function.ID]) || fi.Body == nil {
			continue
		}
		f, ok := gt.Functions[fi.Function.ID]
		if !ok {
			continue
		}
		s.clearReanalyzedFunctionWarnings(gt, f.ID)
		for k, v := range analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID, fi.TypeParamNames) {
			f.Connections[k] = append(f.Connections[k], v...)
		}
		f.Connections = uniqueConns(f.Connections)
		gt.Functions[f.ID] = f
	}
}

// addedDecls names the declarations an update introduces, split by how another file can spell
// a REFERENCE to one. A method is only ever reached through a selector (`c.Diameter()`), so its
// name counts only after a dot -- a sibling that merely declares a method of the same name
// (`func (q Sq) Area()`, an interface's `Area() float64`) is not a reference and must not cost
// the partial path a fallback. Everything else is spelled as a plain identifier, bare in its own
// package or after the package qualifier (`Count`, `shapes.Meter`), which the word test finds
// either way.
type addedDecls struct {
	plain   map[string]bool
	methods map[string]bool
}

func (a addedDecls) empty() bool { return len(a.plain) == 0 && len(a.methods) == 0 }

// addedDeclNames collects the names of every declaration pr introduces that the graph does not
// hold yet. Computed before the file's old members are removed, so a declaration the file
// already had is not reported as new.
func addedDeclNames(gt *golang.GolangTopology, pr *ParseResult) addedDecls {
	out := addedDecls{plain: map[string]bool{}, methods: map[string]bool{}}
	add := func(set map[string]bool, name string) {
		if name != "" && name != "_" {
			set[name] = true
		}
	}
	for _, fi := range pr.Functions {
		if _, existed := gt.Functions[fi.Function.ID]; existed {
			continue
		}
		if fi.Function.MethodFrom != nil {
			add(out.methods, fi.Function.Name)
		} else {
			add(out.plain, fi.Function.Name)
		}
	}
	for _, si := range pr.Structs {
		if _, existed := gt.Structs[si.ID]; !existed {
			add(out.plain, si.Name)
		}
	}
	for _, ii := range pr.Interfaces {
		if _, existed := gt.Interfaces[ii.ID]; !existed {
			add(out.plain, ii.Name)
		}
	}
	for _, nt := range pr.NamedTypes {
		if _, existed := gt.NamedTypes[nt.ID]; !existed {
			add(out.plain, nt.Name)
		}
	}
	for _, v := range pr.ExternalVars {
		if _, existed := gt.ExternalVars[v.ID]; !existed {
			add(out.plain, v.Name)
		}
	}
	return out
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

	// The names this update brings into existence. A body or signature in another file may
	// already spell one -- a call to a method that did not exist yet, a bare package var, a
	// type in a var declaration -- and resolved it to nothing without leaving a warning to key
	// a later fix on. Only passes.refresh can reach those files.
	added := addedDeclNames(gt, pr)
	// Final ids for this file's functions, and the renames its sibling declarations need; see
	// assignDuplicateOrdinals. Applied once this file's old members are gone.
	renames := planFunctionIDs(gt, pr, absPath)
	// An id this file stops declaring is not gone when a sibling declaration takes it over
	// (the second `init`, the other build-tag variant): its callers still resolve to it.
	takenOver := make(map[golang.FunctionID]bool, len(renames))
	for _, to := range renames {
		takenOver[to] = true
	}

	if !hasFile {
		applyFunctionRenames(gt, renames)
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

		// The structural passes BEFORE the bodies, as the existing-file branch below and Scan
		// both do: a body calling a method of a struct declared in this same new file resolves
		// through that struct's method set, which is empty until structMethods has run.
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

		reresolve := s.resolveWarnings(gt, pr, nil, nil, nil, takenOver)
		if err := passes.refresh(gt, pr, reresolve, added); err != nil {
			return nil, err
		}
		passes.matchInterfaces(gt, pr, nil, nil, nil)
		passes.collectDeps(gt)
		delete(gt.Errors, absPath)
		return warnings, nil
	}

	oldFunctions := make(map[golang.FunctionID]golang.GolangFunction)
	for _, fid := range oldFile.Functions() {
		// Only a function this file DECLARED. A database written before repeated declarations
		// got their own ids lists one shared id under every file that declared it, located at
		// whichever of them won; that one belongs to its location, not to this file.
		if f, ok := gt.Functions[fid]; ok && (f.Loc.Path == "" || f.Loc.Path == absPath) {
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
					// EVERY caller, including one declared in the file this update re-parsed.
					//
					// The removal loop below skips those deliberately: a symbol that is GONE
					// leaves a dangling reference the re-parse itself sees, and reports as
					// use_missing_node against the same caller. Nothing of the sort happens to a
					// signature change -- the callee still resolves, so the re-parse has nothing
					// to complain about -- and skipping the caller here left the single most
					// common breaking edit reporting nothing at all: widen a helper's parameter
					// list and `go build` fails while `arac warnings list` says "No warnings
					// found". settleSignatureWarnings retires the warning as soon as the call
					// site fits again, so a caller fixed in the same edit costs nothing.
					callers := passes.getCallers(gt, string(fid), string(golang.ConnCalls))
					for _, callerID := range callers {
						warnID := callerID + "@" + string(domain.WarnSignatureChanged) + "@" + string(fid)
						warning := domain.TopologyWarning{
							ID:       warnID,
							SourceID: string(fid),
							Kind:     domain.WarnSignatureChanged,
							TargetID: callerID,
							Message:  domain.SignatureChangedMessage(oldFunc.Name, callerID),
						}
						gt.Warnings[warnID] = warning
						warnings = append(warnings, warning)
					}
				}
				break
			}
		}
		if !found && !takenOver[fid] {
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

	removedExtVars := make(map[golang.ExternalVarID]golang.GolangExternalVar)
	for vid := range oldExtVars {
		found := false
		for _, v := range pr.ExternalVars {
			if v.ID == vid {
				found = true
				break
			}
		}
		if !found {
			removedExtVars[vid] = oldExtVars[vid]
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
			// A user in this same file was re-parsed by this very update, so the OLD edge says what
			// it used to reference, not what it references now. Judge it on the new version: gone,
			// or no longer naming the removed node, is not broken -- a const moved to another
			// package along with its only use used to leave a warning nothing could clear.
			if _, isOld := oldFunctions[golang.FunctionID(sourceID)]; isOld && !newFunctionNames(pr, sourceID, oldStruct.Name, true) {
				continue
			}
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
			// A user in this same file was re-parsed by this very update, so the OLD edge says what
			// it used to reference, not what it references now. Judge it on the new version: gone,
			// or no longer naming the removed node, is not broken -- a const moved to another
			// package along with its only use used to leave a warning nothing could clear.
			if _, isOld := oldFunctions[golang.FunctionID(sourceID)]; isOld && !newFunctionNames(pr, sourceID, oldNT.Name, true) {
				continue
			}
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

	// A deleted package-level var, warned and unhooked exactly like a deleted function or
	// named type. Without this the var's users kept a uses_extvar edge to a node the same
	// update deletes -- a dangling edge and no warning -- while the full path, which
	// re-resolves those users' files, raised node_removed for the same edit.
	for vid, oldVar := range removedExtVars {
		varUsers := passes.getCallers(gt, string(vid), string(golang.ConnUsesExtVar))
		for _, sourceID := range varUsers {
			// A user in this same file was re-parsed by this very update, so the OLD edge says what
			// it used to reference, not what it references now. Judge it on the new version: gone,
			// or no longer naming the removed node, is not broken -- a const moved to another
			// package along with its only use used to leave a warning nothing could clear.
			if _, isOld := oldFunctions[golang.FunctionID(sourceID)]; isOld && !newFunctionNames(pr, sourceID, oldVar.Name, false) {
				continue
			}
			if sourceFn, ok := gt.Functions[golang.FunctionID(sourceID)]; ok {
				sourceFn.Connections[golang.ConnUsesExtVar] = removeString(sourceFn.Connections[golang.ConnUsesExtVar], string(vid))
				gt.Functions[golang.FunctionID(sourceID)] = sourceFn
			}
			warnID := sourceID + "@" + string(domain.WarnNodeRemoved) + "@" + string(vid)
			warning := domain.TopologyWarning{
				ID:       warnID,
				SourceID: sourceID,
				Kind:     domain.WarnNodeRemoved,
				TargetID: string(vid),
				Message:  fmt.Sprintf("function %s uses variable %s which was removed from %s", sourceID, oldVar.Name, absPath),
			}
			gt.Warnings[warnID] = warning
			warnings = append(warnings, warning)
		}
	}

	pkg := gt.Packages[oldFile.FromPackage]
	pkg.Connections[golang.ConnHasFile] = removeString(pkg.Connections[golang.ConnHasFile], string(oldFile.ID))
	var ownFuncs []string
	for _, fid := range oldFile.Functions() {
		if f, ok := gt.Functions[fid]; !ok || f.Loc.Path == "" || f.Loc.Path == absPath {
			ownFuncs = append(ownFuncs, string(fid))
		}
	}
	pkg.Connections[golang.ConnHasFunc] = removeStrings(pkg.Connections[golang.ConnHasFunc], ownFuncs...)
	pkg.Connections[golang.ConnHasStruct] = removeStrings(pkg.Connections[golang.ConnHasStruct], castStructIDs(oldFile.Structs())...)
	pkg.Connections[golang.ConnHasIface] = removeStrings(pkg.Connections[golang.ConnHasIface], castInterfaceIDs(oldFile.Interfaces())...)
	pkg.Connections[golang.ConnHasNamedType] = removeStrings(pkg.Connections[golang.ConnHasNamedType], castNamedTypeIDs(oldFile.NamedTypes())...)
	pkg.Connections[golang.ConnHasVar] = removeStrings(pkg.Connections[golang.ConnHasVar], castExtVarIDs(oldFile.ExternalVars())...)
	gt.Packages[oldFile.FromPackage] = pkg

	for fid := range oldFunctions {
		delete(gt.Functions, fid)
	}
	applyFunctionRenames(gt, renames)
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
	// The clear above is for warnings judged against a function's OLD body. A node_removed this
	// update raised on a same-file user was judged against its NEW one (newFunctionNames), so it
	// goes back in: the partial path reports from gt.Warnings, not from the returned list, and lost
	// it -- a user still naming a removed const was warned on one route and not the other.
	for _, w := range warnings {
		if w.Kind == domain.WarnNodeRemoved {
			gt.Warnings[w.ID] = w
		}
	}

	reresolve := s.resolveWarnings(gt, pr, removedFuncs, removedStructs, removedNamedTypes, takenOver)
	if err := passes.refresh(gt, pr, reresolve, added); err != nil {
		return nil, err
	}

	passes.matchInterfaces(gt, pr, removedStructs, removedFuncs, removedNamedTypes)
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
// appeared names ids that came back without being declared by pr: a sibling declaration that
// took over a repeated id (see planFunctionIDs).
// It returns the functions, declared outside pr, whose body named a target that has just
// reappeared: their bodies have to be resolved again (passes.refresh), because the edge patched
// in here is only the one the warning names.
func (s *GoScanner) resolveWarnings(gt *golang.GolangTopology, pr *ParseResult, removedFuncs map[golang.FunctionID]golang.GolangFunction, removedStructs map[golang.StructID]golang.GolangStruct, removedNamedTypes map[golang.NamedTypeID]golang.GolangNamedType, appeared map[golang.FunctionID]bool) []golang.FunctionID {
	var reresolve []golang.FunctionID
	ownFuncs := make(map[string]bool, len(pr.Functions))
	for _, fi := range pr.Functions {
		ownFuncs[string(fi.Function.ID)] = true
	}
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
	for id := range appeared {
		newIDs[string(id)] = true
	}

	for warnID, w := range gt.Warnings {
		if !newIDs[w.TargetID] {
			continue
		}

		switch w.Kind {
		case domain.WarnUseMissingNode, domain.WarnNodeRemoved:
			s.resolveUseMissingWarning(gt, w)
			delete(gt.Warnings, warnID)
			if !ownFuncs[w.SourceID] {
				reresolve = append(reresolve, golang.FunctionID(w.SourceID))
			}
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
	return reresolve
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
			if skipGoDir(root, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isGoSourceName(d.Name()) {
			if domain.PathHidden(path) {
				return nil
			}
			files = append(files, path)
		}
		return nil
	})
	return files
}

// Rebuilds the HasMethod connections for structs and non-struct named types by iterating through all functions and linking methods to their receiver types.
func populateStructMethods(gt *golang.GolangTopology) {
	for id, str := range gt.Structs {
		delete(str.Connections, golang.ConnHasMethod)
		gt.Structs[id] = str
	}
	for id, nt := range gt.NamedTypes {
		delete(nt.Connections, golang.ConnHasMethod)
		gt.NamedTypes[id] = nt
	}
	for _, f := range gt.Functions {
		if f.MethodFrom != nil {
			attachMethod(gt, f)
		}
	}
}

// attachMethod adds f to the method list of its receiver type. The receiver is usually a
// struct, but any defined type can carry methods -- `type HandlerFunc func(int) int` with
// Serve, `type Celsius float64` with String -- and those used to be dropped, leaving the
// method with no owner and the type unable to satisfy any interface.
func attachMethod(gt *golang.GolangTopology, f golang.GolangFunction) {
	if str, ok := gt.Structs[*f.MethodFrom]; ok {
		if str.Connections == nil {
			str.Connections = make(map[golang.ConnectionKind][]string)
		}
		str.Connections[golang.ConnHasMethod] = append(str.Connections[golang.ConnHasMethod], string(f.ID))
		gt.Structs[*f.MethodFrom] = str
		return
	}
	if nt, ok := gt.NamedTypes[golang.NamedTypeID(*f.MethodFrom)]; ok {
		if nt.Connections == nil {
			nt.Connections = make(map[golang.ConnectionKind][]string)
		}
		nt.Connections[golang.ConnHasMethod] = append(nt.Connections[golang.ConnHasMethod], string(f.ID))
		gt.NamedTypes[golang.NamedTypeID(*f.MethodFrom)] = nt
	}
}

// Links New-prefixed functions to their struct constructors by matching return types.
//
// A struct's constructor is re-derived from scratch: one that was removed is forgotten, as a
// cold scan never sees it. When several New* functions return the same struct, the LAST one in
// declaration order (file path, then line) wins -- the answer a cold scan has always given,
// now given by every path. The update paths used to walk the package's function list, whose
// order records which file was re-parsed last, or (partial path) a map.
func detectConstructors(gt *golang.GolangTopology) {
	for id, str := range gt.Structs {
		if str.Constructor != nil {
			str.Constructor = nil
			gt.Structs[id] = str
		}
	}
	for pkgPath, pkg := range gt.Packages {
		assignConstructors(gt, pkgPath, pkg.HasFunctions(), func(golang.StructID) bool { return true })
	}
}

// assignConstructors sets Constructor on the structs of pkgPath (those in scope) from the
// candidate functions, in declaration order so the result does not depend on how they were
// listed.
func assignConstructors(gt *golang.GolangTopology, pkgPath golang.PackagePath, funcIDs []golang.FunctionID, inScope func(golang.StructID) bool) {
	var candidates []golang.GolangFunction
	for _, funcID := range funcIDs {
		f, ok := gt.Functions[funcID]
		if !ok || f.MethodFrom != nil || !strings.HasPrefix(f.Name, "New") || len(f.Output) == 0 {
			continue
		}
		if funcPkg(f.ID, f) != pkgPath {
			continue
		}
		candidates = append(candidates, f)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Loc, candidates[j].Loc
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartsAt != b.StartsAt {
			return a.StartsAt < b.StartsAt
		}
		return candidates[i].ID < candidates[j].ID
	})
	for _, f := range candidates {
		name := constructedTypeName(f.Output[0].Typing)
		if name == "" {
			continue
		}
		structID := golang.StructID(string(pkgPath) + "." + name)
		str, exists := gt.Structs[structID]
		if !exists || !inScope(structID) {
			continue
		}
		fid := f.ID
		str.Constructor = &fid
		gt.Structs[structID] = str
	}
}

// constructedTypeName reduces a constructor's first result type to the bare name of the type
// it builds: `*Box[T]` and `Pair[K, V]` name Box and Pair, the ids their structs are
// registered under. A qualified or composite type names nothing in this package.
func constructedTypeName(returnType string) string {
	name := strings.TrimPrefix(returnType, "*")
	if idx := strings.IndexByte(name, '['); idx > 0 {
		name = name[:idx]
	}
	if name == "" || strings.ContainsAny(name, ".*[](){} ") {
		return ""
	}
	return name
}

// duplicateIDSep joins a repeated declaration's ordinal to the id it shares; see
// assignDuplicateOrdinals.
const duplicateIDSep = "#"

// baseFunctionID splits a function id into the id its declaration spells and its ordinal among
// the package's declarations of that id: "pkg.init#3" is ("pkg.init", 3), "pkg.init" is
// ("pkg.init", 1).
func baseFunctionID(id golang.FunctionID) (golang.FunctionID, int) {
	i := strings.LastIndex(string(id), duplicateIDSep)
	if i < 0 {
		return id, 1
	}
	n, err := strconv.Atoi(string(id)[i+1:])
	if err != nil || n < 2 {
		return id, 1
	}
	return id[:i], n
}

// funcDecl is one function declaration as assignDuplicateOrdinals orders it.
type funcDecl struct {
	path string
	line int
	hint int // tie-break within one file: source position, or the ordinal an id already has
	base golang.FunctionID
}

// assignDuplicateOrdinals gives every function declaration of ONE package its final id.
//
// WHY. Go lets a package declare `init` -- and the blank function `_` -- any number of times,
// and a function can be declared once per build-tag variant (open_linux.go, open_windows.go).
// All of them spell the same id, and keying the graph on it collapsed them into one node:
// three inits became one located wherever the last writer was, carrying the union of their
// edges on a cold scan and only the re-parsed file's on an incremental one.
//
// THE RULE. Order the package's declarations by file path, then line. The first declaration
// of an id keeps it unchanged -- so a package with one init still has `pkg.init`, the id every
// existing graph holds -- and each later one is `id#2`, `id#3`, ... in that order. Callers
// resolve to the plain id, which exists for as long as any file declares the name.
func assignDuplicateOrdinals(decls []funcDecl) []golang.FunctionID {
	order := make([]int, len(decls))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := decls[order[a]], decls[order[b]]
		if x.path != y.path {
			return x.path < y.path
		}
		if x.line != y.line {
			return x.line < y.line
		}
		return x.hint < y.hint
	})
	seen := make(map[golang.FunctionID]int, len(decls))
	out := make([]golang.FunctionID, len(decls))
	for _, i := range order {
		base := decls[i].base
		seen[base]++
		if n := seen[base]; n > 1 {
			out[i] = base + duplicateIDSep + golang.FunctionID(strconv.Itoa(n))
		} else {
			out[i] = base
		}
	}
	return out
}

// assignScanFunctionIDs applies assignDuplicateOrdinals to a cold scan's parse results, in
// place, and returns each file's final function ids in pr.Functions order.
func assignScanFunctionIDs(prs []*ParseResult) map[string][]golang.FunctionID {
	byPkg := make(map[golang.PackagePath][]*ParseResult)
	for _, pr := range prs {
		byPkg[pr.PkgPath] = append(byPkg[pr.PkgPath], pr)
	}
	out := make(map[string][]golang.FunctionID, len(prs))
	for _, group := range byPkg {
		var decls []funcDecl
		for _, pr := range group {
			for i, fi := range pr.Functions {
				decls = append(decls, funcDecl{path: pr.FileID, line: fi.Function.Loc.StartsAt, hint: i, base: fi.Function.ID})
			}
		}
		final := assignDuplicateOrdinals(decls)
		k := 0
		for _, pr := range group {
			ids := make([]golang.FunctionID, len(pr.Functions))
			for i := range pr.Functions {
				pr.Functions[i].Function.ID = final[k]
				ids[i] = final[k]
				k++
			}
			out[pr.FileID] = ids
		}
	}
	return out
}

// planFunctionIDs gives pr's functions their final ids against the rest of the package as gt
// holds it, and returns the renames the package's OTHER declarations need (old id -> new id)
// without applying them. A rename happens when this file gains or loses a declaration of an id
// another file also declares: adding a first `init` to a.go makes b.go's `pkg.init` the
// second one.
//
// A declaration whose file is gone from disk does not count: that file is about to be removed
// from the graph. This is the rename case -- a.go moved to b.go reaches here as b.go being
// registered while a.go's rows are still present -- and counting a.go would make every
// function of b.go the SECOND declaration of its own id.
func planFunctionIDs(gt *golang.GolangTopology, pr *ParseResult, path string) map[golang.FunctionID]golang.FunctionID {
	var decls []funcDecl
	var others []golang.FunctionID
	onDisk := make(map[string]bool)
	pkg := gt.Packages[pr.PkgPath]
	for _, id := range pkg.HasFunctions() {
		f, ok := gt.Functions[id]
		if !ok || f.Loc.Path == "" || f.Loc.Path == path {
			continue
		}
		exists, checked := onDisk[f.Loc.Path]
		if !checked {
			_, err := os.Stat(f.Loc.Path)
			exists = err == nil
			onDisk[f.Loc.Path] = exists
		}
		if !exists {
			continue
		}
		base, n := baseFunctionID(id)
		decls = append(decls, funcDecl{path: f.Loc.Path, line: f.Loc.StartsAt, hint: n, base: base})
		others = append(others, id)
	}
	for i, fi := range pr.Functions {
		base, _ := baseFunctionID(fi.Function.ID)
		decls = append(decls, funcDecl{path: path, line: fi.Function.Loc.StartsAt, hint: i, base: base})
	}
	final := assignDuplicateOrdinals(decls)
	renames := make(map[golang.FunctionID]golang.FunctionID)
	for j, id := range others {
		if final[j] != id {
			renames[id] = final[j]
		}
	}
	for i := range pr.Functions {
		pr.Functions[i].Function.ID = final[len(others)+i]
	}
	return renames
}

// applyFunctionRenames re-keys sibling declarations planFunctionIDs moved to a new ordinal:
// the function itself, its file's and package's has_function lists, its receiver's method
// list, and the warnings that name it. Nothing else can point at one -- a body never resolves
// to an ordinal id, and neither `init` nor `_` can be referenced at all.
func applyFunctionRenames(gt *golang.GolangTopology, renames map[golang.FunctionID]golang.FunctionID) {
	if len(renames) == 0 {
		return
	}
	moved := make(map[golang.FunctionID]golang.GolangFunction, len(renames))
	for from := range renames {
		if f, ok := gt.Functions[from]; ok {
			moved[from] = f
			delete(gt.Functions, from)
		}
	}
	rename := func(ids []string) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			if to, ok := renames[golang.FunctionID(id)]; ok {
				out[i] = string(to)
			} else {
				out[i] = id
			}
		}
		return out
	}
	// Each list is rewritten exactly once: renames chain (init -> init#2 while init#2 ->
	// init#3), so a second pass over the same list would move an id twice.
	files := make(map[golang.FileID]bool)
	for from, f := range moved {
		f.ID = renames[from]
		gt.Functions[f.ID] = f
		files[golang.FileID(f.Loc.Path)] = true
	}
	for fid := range files {
		if file, ok := gt.Files[fid]; ok {
			file.Connections[golang.ConnHasFunc] = rename(file.Connections[golang.ConnHasFunc])
			gt.Files[fid] = file
		}
	}
	for path, pkg := range gt.Packages {
		if len(pkg.Connections[golang.ConnHasFunc]) > 0 {
			pkg.Connections[golang.ConnHasFunc] = rename(pkg.Connections[golang.ConnHasFunc])
			gt.Packages[path] = pkg
		}
	}
	for id, str := range gt.Structs {
		if len(str.Connections[golang.ConnHasMethod]) > 0 {
			str.Connections[golang.ConnHasMethod] = rename(str.Connections[golang.ConnHasMethod])
			gt.Structs[id] = str
		}
	}
	for id, nt := range gt.NamedTypes {
		if len(nt.Connections[golang.ConnHasMethod]) > 0 {
			nt.Connections[golang.ConnHasMethod] = rename(nt.Connections[golang.ConnHasMethod])
			gt.NamedTypes[id] = nt
		}
	}
	// Collected first: re-keying while ranging could visit a re-keyed warning twice.
	var touched []string
	for warnID, w := range gt.Warnings {
		_, srcOK := renames[golang.FunctionID(w.SourceID)]
		_, tgtOK := renames[golang.FunctionID(w.TargetID)]
		if srcOK || tgtOK {
			touched = append(touched, warnID)
		}
	}
	renamed := make([]domain.TopologyWarning, 0, len(touched))
	for _, warnID := range touched {
		w := gt.Warnings[warnID]
		src, srcOK := renames[golang.FunctionID(w.SourceID)]
		tgt, tgtOK := renames[golang.FunctionID(w.TargetID)]
		parts := strings.Split(warnID, "@")
		if srcOK {
			for i, p := range parts {
				if p == w.SourceID {
					parts[i] = string(src)
				}
			}
			w.SourceID = string(src)
		}
		if tgtOK {
			for i, p := range parts {
				if p == w.TargetID {
					parts[i] = string(tgt)
				}
			}
			w.TargetID = string(tgt)
		}
		delete(gt.Warnings, warnID)
		w.ID = strings.Join(parts, "@")
		renamed = append(renamed, w)
	}
	for _, w := range renamed {
		gt.Warnings[w.ID] = w
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
			if !ok || !pr.internalImport(importPath) {
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
		if !contract.SameGoTypeText(a.Input[i].Typing, b.Input[i].Typing) {
			return false
		}
	}
	for i := range a.Output {
		if !contract.SameGoTypeText(a.Output[i].Typing, b.Output[i].Typing) {
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

// newFunctionNames reports whether the re-parsed version of function id still names a
// package-level identifier: as a bare identifier in its body (a selector's field or method name
// is not one), or, for a type, in its signature. False when the function no longer exists.
//
// Syntactic on purpose. Only an UNQUALIFIED name can refer to the removed node of this file's own
// package, and the resolver deliberately raises nothing for an unresolved bare identifier -- it
// may be a local -- so without this a user that still references the node would never be warned,
// and one that stopped would stay warned.
func newFunctionNames(pr *ParseResult, id, name string, inSignature bool) bool {
	for _, fi := range pr.Functions {
		if string(fi.Function.ID) != id {
			continue
		}
		if inSignature {
			for _, defs := range [][]golang.VariableDefinition{fi.Function.Input, fi.Function.Output} {
				for _, d := range defs {
					if typingNames(d.Typing, name) {
						return true
					}
				}
			}
		}
		if fi.Body == nil {
			return false
		}
		found := false
		selectors := map[*ast.Ident]bool{}
		ast.Inspect(fi.Body, func(n ast.Node) bool {
			if found {
				return false
			}
			switch x := n.(type) {
			case *ast.SelectorExpr:
				selectors[x.Sel] = true
			case *ast.Ident:
				if x.Name == name && !selectors[x] {
					found = true
				}
			}
			return true
		})
		return found
	}
	return false
}

// typingNames reports whether a rendered type ("[]*Rect", "map[string]Rect") mentions name as a
// whole, unqualified identifier.
func typingNames(typing, name string) bool {
	for i := strings.Index(typing, name); i >= 0; {
		end := i + len(name)
		before := i == 0 || !isGoIdentByte(typing[i-1]) && typing[i-1] != '.'
		after := end == len(typing) || !isGoIdentByte(typing[end])
		if before && after {
			return true
		}
		next := strings.Index(typing[i+1:], name)
		if next < 0 {
			break
		}
		i += next + 1
	}
	return false
}

func isGoIdentByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

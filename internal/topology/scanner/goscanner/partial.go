package goscanner

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// jsonRoundTrip marshals from then unmarshals into to, converting between the
// map[string]any decoded from properties_json and a typed slice/struct.
func jsonRoundTrip(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, to)
}

// errPartialFallback aliases scanner.ErrPartialFallback: it signals that the
// partial change-path cannot safely handle this file and the caller must use the
// full ReadDb path instead. Not a real error.
var errPartialFallback = scanner.ErrPartialFallback

// UpdateFilePartial implements scanner.PartialUpdater for Go. It updates a single
// changed file WITHOUT loading the whole topology graph: it loads only the
// working set it needs from the DB (the changed file's old resources, its
// package + internally-imported package members, all interfaces, the reverse
// callers of its old members, and all warnings), runs resolution plus
// INCREMENTAL global passes over that partial topology, then returns a scoped
// delta (upserts/deletes) plus the complete updated warnings map. The cost
// scales with the change, not the repo, and there is no full ReadDb on this path.
func (s *GoScanner) UpdateFilePartial(dbPath, root, absPath string) (upserts []domain.Resource, deletes []string, warnings map[string]domain.TopologyWarning, err error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, nil, err
	}
	modulePath, err := readModulePath(rootPath)
	if err != nil {
		return nil, nil, nil, err
	}

	dir := filepath.Dir(absPath)
	pkgPath := getPackagePath(rootPath, dir, modulePath)

	// Parse the changed file up front so we know which internal packages it
	// imports (their members are forward-resolution targets).
	pr, parseErr := ParseFile(absPath, pkgPath, modulePath, rootPath)

	// Build the working-set topology from the DB.
	gt, loadErr := s.loadWorkingSet(dbPath, rootPath, absPath, pkgPath, pr)
	if loadErr != nil {
		return nil, nil, nil, loadErr
	}

	// Fingerprint the loaded working set so we can derive a precise delta after
	// the update (any loaded neighbor whose row/edges change is upserted).
	beforeTopo := golang.ToGeneric(gt)
	beforeSigs := helper.ResourceSignatures(beforeTopo.Resources)
	beforeIDs := make(map[string]bool, len(beforeTopo.Resources))
	for id := range beforeTopo.Resources {
		beforeIDs[id] = true
	}

	if parseErr != nil {
		// Parse failure mirrors the full path: record the error, drop the file's
		// old members, keep everything else. We still must produce a correct
		// delta, so fall back to the full path by signalling fallback (the
		// manager routes to the full ReadDb path, which records gt.Errors).
		return nil, nil, nil, errPartialFallback
	}

	// Interface satisfaction is structural and cross-package: a struct anywhere
	// in the repo may implement an interface defined/changed in this file. The
	// partial working set does not load every struct, so any change touching an
	// interface (added, removed, or modified) is routed to the full path for
	// correctness. Interface edits are far rarer than function/struct/method
	// edits, so the common change-path stays on the fast partial path.
	if len(pr.Interfaces) > 0 {
		return nil, nil, nil, errPartialFallback
	}
	if oldFile, ok := gt.Files[golang.FileID(absPath)]; ok && len(oldFile.Interfaces()) > 0 {
		return nil, nil, nil, errPartialFallback
	}

	// Run the same per-file mutation the full UpdateFile runs, but with
	// INCREMENTAL global passes that never wipe-and-rebuild the whole graph.
	passes := s.incrementalPasses(dbPath)
	if _, applyErr := s.applyFileUpdate(gt, pr, absPath, pkgPath, passes); applyErr != nil {
		return nil, nil, nil, applyErr
	}

	afterTopo := golang.ToGeneric(gt)
	// Restrict the delta to the working set: a resource is an upsert if it is new
	// or its signature changed; a delete is an old working-set ID now gone.
	for id, res := range afterTopo.Resources {
		if beforeSigs[id] != helper.ResourceSignatureOf(res) {
			upserts = append(upserts, res)
		}
	}
	for id := range beforeIDs {
		if _, ok := afterTopo.Resources[id]; !ok {
			deletes = append(deletes, id)
		}
	}

	return upserts, deletes, gt.Warnings, nil
}

// loadWorkingSet assembles the partial GolangTopology the incremental update
// needs, reading only the resources required for correct resolution and
// incremental matching:
// - the changed file's OLD member resources (by loc_path) + the file node +
// the owning package node (both by ID),
// - the changed file's package members + each internally-imported package's
// members (forward-resolution targets),
// - ALL interfaces (struct<->interface matching is structural/cross-package),
// - the reverse callers/users of the file's old funcs/structs/named-types
// (so signature-change / node-removed warnings + caller edge cleanup work),
// - ALL warnings (the table is small; the existing add/clear logic runs on it
// and the complete map is returned for a wholesale warnings rewrite).
// A point-lookup MISS in the resulting gt is the correct "missing node" signal;
// nothing is fabricated.
func (s *GoScanner) loadWorkingSet(dbPath, rootPath, absPath string, pkgPath golang.PackagePath, pr *ParseResult) (*golang.GolangTopology, error) {
	idSet := make(map[string]bool)

	// 1. The file node + its old members (members carry loc_path; the file node
	// is stored with loc_path == "" so fetch it by ID).
	fileMembers, err := helper.ReadResourcesByFile(dbPath, absPath)
	if err != nil {
		return nil, err
	}
	idSet[absPath] = true
	for id := range fileMembers {
		idSet[id] = true
	}

	// 2. Load the forward-resolution package closure: the changed package, every
	// internally-imported package, and transitively the packages of any type
	// returned/used by a loaded function/struct/named-type (so cross-package
	// return inference — e.g. consumer calls geometry.MakeCircle() returning
	// shapes.Circle, then .Area() on it — resolves to the concrete struct + its
	// methods exactly like the full scan, instead of falling back to an
	// interface). Expansion follows VariableDefinition.TypingID (the parse-time
	// resolved canonical id) to its owning package, until a fixpoint.
	memberConnTypes := []string{
		string(golang.ConnHasFunc), string(golang.ConnHasStruct),
		string(golang.ConnHasIface), string(golang.ConnHasNamedType),
		string(golang.ConnHasVar), string(golang.ConnHasFile),
	}
	loadedPkgs := make(map[string]bool)
	frontier := map[string]bool{string(pkgPath): true}
	if pr != nil {
		for _, ip := range pr.InternalImports {
			frontier[string(ip)] = true
		}
	}
	const maxPkgExpansions = 8
	for round := 0; round < maxPkgExpansions && len(frontier) > 0; round++ {
		pkgIDs := make([]string, 0, len(frontier))
		for id := range frontier {
			pkgIDs = append(pkgIDs, id)
			loadedPkgs[id] = true
		}
		frontier = make(map[string]bool)

		pkgNodes, perr := helper.ReadResourcesByIDs(dbPath, pkgIDs)
		if perr != nil {
			return nil, perr
		}
		var memberIDs []string
		for id, pkg := range pkgNodes {
			idSet[id] = true
			for _, ct := range memberConnTypes {
				for _, target := range pkg.Connections[ct] {
					idSet[target] = true
					memberIDs = append(memberIDs, target)
				}
			}
		}
		// Read the package members so we can inspect their typing ids and
		// discover further packages to load.
		members, merr := helper.ReadResourcesByIDs(dbPath, memberIDs)
		if merr != nil {
			return nil, merr
		}
		for _, target := range typingPackagesOf(members, pr) {
			if !loadedPkgs[target] {
				frontier[target] = true
			}
		}
	}

	// 3. Reverse callers/users of the file's old members so warnings + caller
	// edge cleanup can run. Collect old member IDs (funcs/structs/named-types
	// are the referenced kinds).
	oldMemberIDs := make([]string, 0, len(fileMembers))
	for id := range fileMembers {
		oldMemberIDs = append(oldMemberIDs, id)
	}
	for _, ct := range []string{
		string(golang.ConnCalls), string(golang.ConnUsesStruct),
		string(golang.ConnUsesNamedType), string(golang.ConnUsesIface),
		string(golang.ConnUsesExtVar),
	} {
		rev, rerr := helper.ReadReverseConnections(dbPath, oldMemberIDs, ct)
		if rerr != nil {
			return nil, rerr
		}
		for _, sources := range rev {
			for _, src := range sources {
				idSet[src] = true
			}
		}
	}

	// Materialize everything collected so far by ID.
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	resources, err := helper.ReadResourcesByIDs(dbPath, ids)
	if err != nil {
		return nil, err
	}

	// 4. ALL interfaces (for struct<->interface matching). Merge in.
	ifaces, err := helper.ReadResourcesByKind(dbPath, domain.ResourceInterface)
	if err != nil {
		return nil, err
	}
	for id, res := range ifaces {
		resources[id] = res
	}

	topo := &domain.Topology{
		Root:      rootPath,
		Language:  "go",
		Languages: []string{"go"},
		Resources: resources,
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	gt := golang.FromGeneric(topo)
	if gt == nil {
		return nil, fmt.Errorf("failed to convert working set from generic")
	}
	gt.Root = rootPath

	// 5. ALL warnings (small table).
	allWarnings, err := helper.ReadAllWarnings(dbPath)
	if err != nil {
		return nil, err
	}
	gt.Warnings = allWarnings

	return gt, nil
}

// typingPackagesOf extracts the owning package paths of every type referenced
// by the given resources via the parse-time-resolved TypingID on function
// inputs/outputs, struct params, and interface method signatures. Only
// module-internal packages (those under pr.ModulePath) are returned, since
// external/builtin types have no topology resources to load. These packages must
// be loaded so cross-package return inference resolves to concrete types instead
// of falling back to interfaces.
func typingPackagesOf(resources map[string]domain.Resource, pr *ParseResult) []string {
	modulePath := ""
	if pr != nil {
		modulePath = pr.ModulePath
	}
	seen := make(map[string]bool)
	var out []string
	add := func(typingID string) {
		if typingID == "" {
			return
		}
		pkg := trimLastDotSegment(typingID)
		if pkg == "" || seen[pkg] {
			return
		}
		if modulePath != "" && !strings.HasPrefix(pkg, modulePath) {
			return
		}
		seen[pkg] = true
		out = append(out, pkg)
	}
	collectVarDefs := func(raw any) {
		for _, vd := range decodeVarDefs(raw) {
			add(vd.TypingID)
		}
	}
	for _, res := range resources {
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			collectVarDefs(res.Properties["input"])
			collectVarDefs(res.Properties["output"])
		case domain.ResourceStruct:
			collectVarDefs(res.Properties["params"])
		case domain.ResourceInterface:
			for _, m := range decodeMethodDefs(res.Properties["methods"]) {
				for _, vd := range m.Input {
					add(vd.TypingID)
				}
				for _, vd := range m.Output {
					add(vd.TypingID)
				}
			}
		}
	}
	return out
}

// Decodes a JSON-serialized variable definition into a VariableDefinition slice.
func decodeVarDefs(raw any) []golang.VariableDefinition {
	if raw == nil {
		return nil
	}
	var out []golang.VariableDefinition
	jsonRoundTrip(raw, &out)
	return out
}

// Decodes raw data into a slice of FunctionDefinition via JSON round-trip.
func decodeMethodDefs(raw any) []golang.FunctionDefinition {
	if raw == nil {
		return nil
	}
	var out []golang.FunctionDefinition
	jsonRoundTrip(raw, &out)
	return out
}

// gtPasses bundles the global relationship passes UpdateFile runs after the
// per-file mutation. The full path uses the whole-graph implementations; the
// partial path swaps in incremental variants that only touch the changed file's
// resources and their neighbors, preserving everyone else's edges.
type gtPasses struct {
	getCallers      func(gt *golang.GolangTopology, targetID, connType string) []string
	structMethods   func(gt *golang.GolangTopology, pr *ParseResult)
	constructors    func(gt *golang.GolangTopology, pr *ParseResult)
	matchInterfaces func(gt *golang.GolangTopology, pr *ParseResult, removedStructs map[golang.StructID]golang.GolangStruct, removedFuncs map[golang.FunctionID]golang.GolangFunction)
	collectDeps     func(gt *golang.GolangTopology)
}

// incrementalPasses returns passes scoped to the changed file. getCallers uses
// the idx_conn_target index instead of scanning the whole graph.
func (s *GoScanner) incrementalPasses(dbPath string) gtPasses {
	return gtPasses{
		getCallers: func(gt *golang.GolangTopology, targetID, connType string) []string {
			rev, err := helper.ReadReverseConnections(dbPath, []string{targetID}, connType)
			if err != nil {
				return nil
			}
			return rev[targetID]
		},
		structMethods:   populateStructMethodsIncremental,
		constructors:    detectConstructorsIncremental,
		matchInterfaces: matchStructsToInterfacesIncremental,
		collectDeps:     collectDependenciesIncremental,
	}
}

// populateStructMethodsIncremental rebuilds ConnHasMethod only for structs in the
// changed file's package: it clears the method list on those structs, then
// re-derives from every method whose receiver is one of them. Structs in other
// packages keep their stored method edges untouched. (Methods always live in the
// same package as their receiver struct.)
func populateStructMethodsIncremental(gt *golang.GolangTopology, pr *ParseResult) {
	pkg := pr.PkgPath
	// Identify the structs of the changed package present in the working set.
	scoped := make(map[golang.StructID]bool)
	for sid, str := range gt.Structs {
		if structPkg(sid, str) == pkg {
			scoped[sid] = true
			delete(str.Connections, golang.ConnHasMethod)
			gt.Structs[sid] = str
		}
	}
	for _, f := range gt.Functions {
		if f.MethodFrom == nil {
			continue
		}
		if !scoped[*f.MethodFrom] {
			continue
		}
		str := gt.Structs[*f.MethodFrom]
		if str.Connections == nil {
			str.Connections = make(map[golang.ConnectionKind][]string)
		}
		str.Connections[golang.ConnHasMethod] = append(str.Connections[golang.ConnHasMethod], string(f.ID))
		gt.Structs[*f.MethodFrom] = str
	}
	// Dedup (a method could appear twice if the working set overlaps).
	for sid := range scoped {
		str := gt.Structs[sid]
		str.Connections = uniqueConns(str.Connections)
		gt.Structs[sid] = str
	}
}

// detectConstructorsIncremental scopes constructor detection to the changed
// file's package. It first clears any stale Constructor on the package's structs
// (so a removed constructor is forgotten), then re-detects from the package's
// New* functions.
func detectConstructorsIncremental(gt *golang.GolangTopology, pr *ParseResult) {
	pkgPath := pr.PkgPath
	scoped := make(map[golang.StructID]bool)
	for sid, str := range gt.Structs {
		if structPkg(sid, str) == pkgPath {
			scoped[sid] = true
			if str.Constructor != nil {
				str.Constructor = nil
				gt.Structs[sid] = str
			}
		}
	}
	for fid, f := range gt.Functions {
		if f.MethodFrom != nil {
			continue
		}
		if funcPkg(fid, f) != pkgPath {
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
		if scoped[structID] {
			str := gt.Structs[structID]
			fid := f.ID
			str.Constructor = &fid
			gt.Structs[structID] = str
			continue
		}
		if strings.HasPrefix(returnType, "*") {
			structID = golang.StructID(string(pkgPath) + "." + returnType[1:])
			if scoped[structID] {
				str := gt.Structs[structID]
				fid := f.ID
				str.Constructor = &fid
				gt.Structs[structID] = str
			}
		}
	}
}

// matchStructsToInterfacesIncremental updates struct<->interface edges for only
// the structs affected by the change, preserving every other implementer's
// edges. For each affected struct it clears that struct's Implements list,
// removes it from every interface's ImplementedBy, then re-matches it against
// all (loaded) interfaces. Affected structs = structs defined in the changed
// file plus structs in the changed package whose method set changed (receivers
// of added or removed methods). Interface changes are routed to the full path
// (see canPartial), so the "changed interface" branch is unnecessary here.
func matchStructsToInterfacesIncremental(gt *golang.GolangTopology, pr *ParseResult, removedStructs map[golang.StructID]golang.GolangStruct, removedFuncs map[golang.FunctionID]golang.GolangFunction) {
	affected := make(map[golang.StructID]bool)
	for _, si := range pr.Structs {
		affected[si.ID] = true
	}
	for _, fi := range pr.Functions {
		if fi.Function.MethodFrom != nil {
			affected[*fi.Function.MethodFrom] = true
		}
	}
	for _, f := range removedFuncs {
		if f.MethodFrom != nil {
			affected[*f.MethodFrom] = true
		}
	}
	// Removed structs are deleted from gt already, but clear them from any
	// interface ImplementedBy lists in the working set.
	for sid := range removedStructs {
		for iid, iface := range gt.Interfaces {
			if removeIfPresent(&iface.Connections, golang.ConnImplBy, string(sid)) {
				gt.Interfaces[iid] = iface
			}
		}
	}

	for sid := range affected {
		str, exists := gt.Structs[sid]
		if !exists {
			continue
		}
		// Clear this struct's Implements and remove it from every interface.
		delete(str.Connections, golang.ConnImplements)
		gt.Structs[sid] = str
		for iid, iface := range gt.Interfaces {
			if removeIfPresent(&iface.Connections, golang.ConnImplBy, string(sid)) {
				gt.Interfaces[iid] = iface
			}
		}
		// Re-match against all loaded interfaces.
		for iid, iface := range gt.Interfaces {
			if implements(str, iface, gt) {
				if iface.Connections == nil {
					iface.Connections = make(map[golang.ConnectionKind][]string)
				}
				iface.Connections[golang.ConnImplBy] = append(iface.Connections[golang.ConnImplBy], string(sid))
				gt.Interfaces[iid] = iface

				if str.Connections == nil {
					str.Connections = make(map[golang.ConnectionKind][]string)
				}
				str.Connections[golang.ConnImplements] = append(str.Connections[golang.ConnImplements], string(iid))
				gt.Structs[sid] = str
			}
		}
	}
}

// collectDependenciesIncremental derives the dependency set from the working
// set's files. On the partial path Dependencies are never persisted as their own
// rows by anything that depends on this list being globally complete (the file's
// imports_dependency edges carry the real data), so deriving from the loaded
// files is sufficient and avoids a whole-graph scan.
func collectDependenciesIncremental(gt *golang.GolangTopology) {
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

// removeIfPresent removes item from conns[kind]; returns true if it mutated.
func removeIfPresent(conns *map[golang.ConnectionKind][]string, kind golang.ConnectionKind, item string) bool {
	if *conns == nil {
		return false
	}
	targets := (*conns)[kind]
	out := targets[:0]
	removed := false
	for _, t := range targets {
		if t == item {
			removed = true
			continue
		}
		out = append(out, t)
	}
	if !removed {
		return false
	}
	if len(out) == 0 {
		delete(*conns, kind)
	} else {
		(*conns)[kind] = out
	}
	return true
}

// structPkg returns the package path that owns a struct, derived from its ID
// (pkgPath.Name). Methods/constructors are co-located, so this is used to scope
// the incremental passes to the changed package.
func structPkg(sid golang.StructID, _ golang.GolangStruct) golang.PackagePath {
	return golang.PackagePath(trimLastDotSegment(string(sid)))
}

// Extracts the package path from a function or method ID.
func funcPkg(fid golang.FunctionID, f golang.GolangFunction) golang.PackagePath {
	id := string(fid)
	// Methods have IDs like pkg.(Recv).Method — strip the receiver+method.
	if f.MethodFrom != nil {
		return golang.PackagePath(trimLastDotSegment(string(*f.MethodFrom)))
	}
	return golang.PackagePath(trimLastDotSegment(id))
}

// trimLastDotSegment drops the final ".Name" of a resource ID to yield its
// package path. Package paths may contain '/', never the trailing '.Name', so
// trimming after the last '.' is correct for the pkg.Name id scheme.
func trimLastDotSegment(id string) string {
	idx := strings.LastIndex(id, ".")
	if idx < 0 {
		return id
	}
	return id[:idx]
}

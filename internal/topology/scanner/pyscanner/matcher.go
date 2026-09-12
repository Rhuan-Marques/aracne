package pyscanner

import (
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// Matches and rebuilds inheritance relationships between Python classes by resolving base classes and creating bidirectional ConnInherits/ConnInheritedBy edges.
func matchClassInheritance(gt *python.PythonTopology) {
	// Clear existing inheritance edges first so re-running over the full
	// topology during an incremental UpdateFile rebuilds them from scratch
	// instead of appending duplicates (mirrors goscanner.matchStructsToInterfaces).
	for id, cls := range gt.Classes {
		delete(cls.Connections, python.ConnInherits)
		delete(cls.Connections, python.ConnInheritedBy)
		gt.Classes[id] = cls
	}
	for classID, cls := range gt.Classes {
		for i, baseName := range cls.Bases {
			var cands []string
			if i < len(cls.BaseCandidates) {
				cands = cls.BaseCandidates[i]
			}
			parentID := resolveBaseClassID(baseName, cands, cls, gt)
			if parentID == nil {
				continue
			}

			clsConns := cls.Connections
			if clsConns == nil {
				clsConns = make(map[python.ConnectionKind][]string)
			}
			clsConns[python.ConnInherits] = append(clsConns[python.ConnInherits], string(*parentID))
			cls.Connections = clsConns

			parent := gt.Classes[*parentID]
			parentConns := parent.Connections
			if parentConns == nil {
				parentConns = make(map[python.ConnectionKind][]string)
			}
			parentConns[python.ConnInheritedBy] = append(parentConns[python.ConnInheritedBy], string(classID))
			parent.Connections = parentConns
			gt.Classes[*parentID] = parent

			gt.Classes[classID] = cls
		}
	}
}

// Emits structural Protocol-conformance edges: when a non-Protocol class defines
// every method of a typing.Protocol it does NOT nominally inherit, it gets an
// implements edge to the Protocol and the Protocol gets an implemented_by edge
// back (mirroring goscanner.matchStructsToInterfaces). Must run AFTER
// matchClassInheritance, since it consults the rebuilt inherits edges to avoid
// double-emitting for nominal subclasses.
func matchProtocolImplementations(gt *python.PythonTopology) {
	// Clear existing structural edges first so re-running over the full topology
	// during an incremental UpdateFile rebuilds them from scratch instead of
	// appending duplicates (mirrors matchClassInheritance).
	for id, cls := range gt.Classes {
		delete(cls.Connections, python.ConnImplements)
		delete(cls.Connections, python.ConnImplementedBy)
		gt.Classes[id] = cls
	}

	// Collect each Protocol's required method-name set. Skip empty Protocols so
	// every class doesn't trivially "implement" them.
	protocolMembers := make(map[python.ClassID]map[string]bool)
	for protoID, proto := range gt.Classes {
		if !proto.IsProtocol {
			continue
		}
		members := classMethodNames(proto, gt)
		if len(members) == 0 {
			continue
		}
		protocolMembers[protoID] = members
	}
	if len(protocolMembers) == 0 {
		return
	}

	// Deterministic iteration order so the emitted edge lists are stable.
	classIDs := make([]python.ClassID, 0, len(gt.Classes))
	for id := range gt.Classes {
		classIDs = append(classIDs, id)
	}
	sort.Slice(classIDs, func(i, j int) bool { return classIDs[i] < classIDs[j] })

	protoIDs := make([]python.ClassID, 0, len(protocolMembers))
	for id := range protocolMembers {
		protoIDs = append(protoIDs, id)
	}
	sort.Slice(protoIDs, func(i, j int) bool { return protoIDs[i] < protoIDs[j] })

	for _, classID := range classIDs {
		cls := gt.Classes[classID]
		if cls.IsProtocol {
			continue
		}
		clsMethods := classMethodNames(cls, gt)
		if len(clsMethods) == 0 {
			continue
		}
		for _, protoID := range protoIDs {
			if protoID == classID || nominallyInherits(cls, protoID) {
				continue
			}
			if !methodSetCovers(clsMethods, protocolMembers[protoID]) {
				continue
			}

			clsConns := cls.Connections
			if clsConns == nil {
				clsConns = make(map[python.ConnectionKind][]string)
			}
			clsConns[python.ConnImplements] = append(clsConns[python.ConnImplements], string(protoID))
			cls.Connections = clsConns

			proto := gt.Classes[protoID]
			protoConns := proto.Connections
			if protoConns == nil {
				protoConns = make(map[python.ConnectionKind][]string)
			}
			protoConns[python.ConnImplementedBy] = append(protoConns[python.ConnImplementedBy], string(classID))
			proto.Connections = protoConns
			gt.Classes[protoID] = proto
		}
		gt.Classes[classID] = cls
	}
}

// classMethodNames returns the set of method names directly defined on a class.
func classMethodNames(cls python.PythonClass, gt *python.PythonTopology) map[string]bool {
	names := make(map[string]bool)
	for _, mid := range cls.Methods() {
		m, ok := gt.Functions[mid]
		if !ok || m.Name == "" {
			continue
		}
		names[m.Name] = true
	}
	return names
}

// methodSetCovers reports whether every name in need is present in have.
func methodSetCovers(have, need map[string]bool) bool {
	for name := range need {
		if !have[name] {
			return false
		}
	}
	return true
}

// nominallyInherits reports whether cls already has an inherits edge to protoID.
func nominallyInherits(cls python.PythonClass, protoID python.ClassID) bool {
	for _, id := range cls.Connections[python.ConnInherits] {
		if id == string(protoID) {
			return true
		}
	}
	return false
}

// baseCandidates computes PythonClass.BaseCandidates for a class's bases from its file's
// imports. A base bound by an internal import gets the IDs importRefs derives for it; one
// bound by a third-party import gets that import's dotted path, which names no project class
// and so keeps the base from being matched by name to an unrelated one. Returns nil when no
// base is import-bound, leaving every base to the by-name rules.
func baseCandidates(bases []string, pr *ParseResult) [][]string {
	var out [][]string
	bound := false
	for _, base := range bases {
		refs, members := importRefs(base, pr)
		var cands []string
		for _, r := range refs {
			cands = append(cands, r.Module+"."+r.Name)
		}
		cands = append(cands, members...)
		if len(cands) == 0 {
			head, rest, _ := strings.Cut(base, ".")
			if imp, ok := pr.ImportMap[head]; ok && imp != "" {
				if rest != "" {
					imp += "." + rest
				}
				cands = []string{imp}
			}
		}
		if len(cands) > 0 {
			bound = true
		}
		out = append(out, cands)
	}
	if !bound {
		return nil
	}
	return out
}

// Resolves a base class name to its ClassID. An import-bound base (cands non-empty) resolves
// only to what its import names -- directly, or through that module's own re-exports -- since
// the file said which class it means. Otherwise: same module first, then a unique class of
// that name anywhere.
func resolveBaseClassID(baseName string, cands []string, cls python.PythonClass, gt *python.PythonTopology) *python.ClassID {
	if len(cands) > 0 {
		for _, c := range cands {
			if cid := python.ClassID(c); classExists(cid, gt) {
				return &cid
			}
		}
		root := &ParseResult{ModuleRoot: gt.Root}
		isClass := func(id string) bool { return classExists(python.ClassID(id), gt) }
		for _, c := range cands {
			i := strings.LastIndex(c, ".")
			if i <= 0 {
				continue
			}
			if id := followReexport(pySymbolRef{Module: c[:i], Name: c[i+1:]}, root, gt, isClass); id != "" {
				cid := python.ClassID(id)
				return &cid
			}
		}
		return nil
	}

	samePkgID := python.ClassID(extractPkgFromID(string(cls.ID)) + "." + baseName)
	if _, exists := gt.Classes[samePkgID]; exists {
		return &samePkgID
	}

	// Deterministic global fallback: only resolve a bare base name when exactly
	// one class in the whole topology carries it. The previous implementation
	// returned the first match from a (randomly ordered) map range, producing
	// non-deterministic and often wrong inheritance edges.
	var matches []python.ClassID
	for id := range gt.Classes {
		if extractClassName(id) == baseName {
			matches = append(matches, python.ClassID(id))
		}
	}
	if len(matches) == 1 {
		return &matches[0]
	}

	return nil
}

// Extracts the package part from a dotted Python identifier by returning everything before the last dot.
func extractPkgFromID(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[:i]
		}
	}
	return id
}

// Extracts the class name from a dotted Python identifier by returning everything after the last dot.
func extractClassName(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}

package pyscanner

import "aracne/internal/topology/python"

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
		for _, baseName := range cls.Bases {
			parentID := resolveBaseClassID(baseName, cls, gt)
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

// Resolves a base class name to its ClassID, checking same-package first, then deterministically matching a unique global class by name.
func resolveBaseClassID(baseName string, cls python.PythonClass, gt *python.PythonTopology) *python.ClassID {
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

package jsscanner

import (
	"path/filepath"
	"strings"

	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// matchClassInheritance wires up `class X extends Y` relationships. A base class is
// resolved the way the declaring file sees the name (see resolveHeritage): its own
// declarations, then its imports, then -- for a name it neither declares nor imports -- the
// one class of that name in the topology. This is best-effort (JavaScript inheritance can be
// dynamic), mirroring the Python scanner's by-name resolution.
func matchClassInheritance(gt *js.JavaScriptTopology) {
	byName := make(map[string][]string)
	for id, cls := range gt.Classes {
		delete(cls.Connections, js.ConnInherits)
		delete(cls.Connections, js.ConnInheritedBy)
		gt.Classes[id] = cls
		byName[extractClassName(id)] = append(byName[extractClassName(id)], id)
	}
	for classID, cls := range gt.Classes {
		for _, baseName := range cls.Bases {
			parentID := resolveBaseClassID(baseName, cls, gt, byName[baseName])
			if parentID == nil {
				continue
			}

			clsConns := cls.Connections
			if clsConns == nil {
				clsConns = make(map[js.ConnectionKind][]string)
			}
			clsConns[js.ConnInherits] = append(clsConns[js.ConnInherits], string(*parentID))
			cls.Connections = clsConns

			parent := gt.Classes[*parentID]
			parentConns := parent.Connections
			if parentConns == nil {
				parentConns = make(map[js.ConnectionKind][]string)
			}
			parentConns[js.ConnInheritedBy] = append(parentConns[js.ConnInheritedBy], string(classID))
			parent.Connections = parentConns
			gt.Classes[*parentID] = parent

			gt.Classes[classID] = cls
		}
	}
}

// resolveBaseClassID resolves a class's `extends` name to a class ID (see resolveHeritage).
func resolveBaseClassID(baseName string, cls js.JavaScriptClass, gt *js.JavaScriptTopology, sameName []string) *js.ClassID {
	id, ok := resolveHeritage(baseName, string(cls.ID), cls.Loc.Path, cls.HeritageImports, gt, sameName,
		func(gt *js.JavaScriptTopology, id string) (exportRef, bool) {
			if isClass(gt, id) {
				return exportRef{Kind: js.ConnUsesClass, ID: id}, true
			}
			return exportRef{}, false
		})
	if !ok {
		return nil
	}
	cid := js.ClassID(id)
	return &cid
}

// resolveHeritage resolves a name written in an `extends`/`implements` clause of the
// declaration fromID, which lives in file, the way that file sees the name:
//
//  1. the file's import of the name (`import {Base as B}` + `extends B` is shapes' Base;
//     `extends ns.Base` is ns's), followed through re-exports. An imported name never falls
//     back: it names one module's export, and a package import (`extends React.Component`)
//     must not be pinned on a project class that happens to share the name -- not even the
//     declaring class itself;
//  2. else a declaration of the file's own, innermost enclosing namespace first;
//  3. else, for a name the file neither imports nor declares (a global, a script-style
//     project), the one declaration of that name in the topology -- and nothing when there
//     are several, since picking one would be a guess.
//
// It used to take the first same-named declaration Go's map iteration produced, so with two
// `Base` classes in a project the edge changed from one scan to the next.
func resolveHeritage(name, fromID, file string, imports map[string]js.HeritageImport, gt *js.JavaScriptTopology, sameName []string, classify func(*js.JavaScriptTopology, string) (exportRef, bool)) (string, bool) {
	if imp, ok := imports[name]; ok {
		if !(isRelativeSpecifier(imp.Source) || filepath.IsAbs(imp.Source)) || file == "" {
			return "", false
		}
		abs, ok := resolveSpecifier(file, imp.Source, gt)
		if !ok {
			return "", false
		}
		for _, exported := range bindingExportNames(importInfo{Source: imp.Source, Internal: true, ImportedName: imp.Name, Namespace: imp.Name == ""}, name) {
			if ref, ok := resolveExportWith(gt, abs, exported, classify, nil); ok {
				return ref.ID, true
			}
		}
		return "", false
	}
	modulePath := extractModulePath(fromID)
	if file != "" {
		modulePath = moduleKey(gt, file)
	}
	for scope := extractModulePath(fromID); ; scope = extractModulePath(scope) {
		if ref, ok := classify(gt, scope+"."+name); ok {
			return ref.ID, true
		}
		if len(scope) <= len(modulePath) || !strings.HasPrefix(scope, modulePath+".") {
			break
		}
	}
	var found string
	for _, id := range sameName {
		if _, ok := classify(gt, id); !ok {
			continue
		}
		if found != "" {
			return "", false // ambiguous
		}
		found = id
	}
	return found, found != ""
}

// matchImplementsAndInterfaceExtends wires TypeScript `class C implements I` edges
// (implements/implemented_by) and interface `extends` edges (inherits/inherited_by between
// interfaces). Unlike Go, these are explicitly declared, so resolution is just by name
// (same module first, then any interface in the topology).
func matchImplementsAndInterfaceExtends(gt *js.JavaScriptTopology) {
	for id, c := range gt.Classes {
		delete(c.Connections, js.ConnImplements)
		gt.Classes[id] = c
	}
	for id, iface := range gt.Interfaces {
		delete(iface.Connections, js.ConnImplementedBy)
		delete(iface.Connections, js.ConnInherits)
		delete(iface.Connections, js.ConnInheritedBy)
		gt.Interfaces[id] = iface
	}

	byName := make(map[string][]string)
	for id := range gt.Interfaces {
		byName[extractClassName(id)] = append(byName[extractClassName(id)], id)
	}
	for classID, c := range gt.Classes {
		for _, name := range c.ImplementsRaw {
			ifaceID := resolveInterfaceID(name, string(classID), c.Loc.Path, c.HeritageImports, gt, byName[name])
			if ifaceID == nil {
				continue
			}
			if c.Connections == nil {
				c.Connections = make(map[js.ConnectionKind][]string)
			}
			c.Connections[js.ConnImplements] = append(c.Connections[js.ConnImplements], string(*ifaceID))
			gt.Classes[classID] = c

			iface := gt.Interfaces[*ifaceID]
			if iface.Connections == nil {
				iface.Connections = make(map[js.ConnectionKind][]string)
			}
			iface.Connections[js.ConnImplementedBy] = append(iface.Connections[js.ConnImplementedBy], string(classID))
			gt.Interfaces[*ifaceID] = iface
		}
	}

	for ifaceID, iface := range gt.Interfaces {
		for _, name := range iface.Bases {
			parentID := resolveInterfaceID(name, string(ifaceID), iface.Loc.Path, iface.HeritageImports, gt, byName[name])
			if parentID == nil {
				continue
			}
			if iface.Connections == nil {
				iface.Connections = make(map[js.ConnectionKind][]string)
			}
			iface.Connections[js.ConnInherits] = append(iface.Connections[js.ConnInherits], string(*parentID))
			gt.Interfaces[ifaceID] = iface

			parent := gt.Interfaces[*parentID]
			if parent.Connections == nil {
				parent.Connections = make(map[js.ConnectionKind][]string)
			}
			parent.Connections[js.ConnInheritedBy] = append(parent.Connections[js.ConnInheritedBy], string(ifaceID))
			gt.Interfaces[*parentID] = parent
		}
	}
}

// resolveInterfaceID resolves an `implements`/interface `extends` name to an interface ID
// (see resolveHeritage).
func resolveInterfaceID(name, fromID, file string, imports map[string]js.HeritageImport, gt *js.JavaScriptTopology, sameName []string) *js.InterfaceID {
	id, ok := resolveHeritage(name, fromID, file, imports, gt, sameName,
		func(gt *js.JavaScriptTopology, id string) (exportRef, bool) {
			if _, ok := gt.Interfaces[js.InterfaceID(id)]; ok {
				return exportRef{Kind: js.ConnImplements, ID: id}, true
			}
			return exportRef{}, false
		})
	if !ok {
		return nil
	}
	iid := js.InterfaceID(id)
	return &iid
}

// Extracts the module path from a fully-qualified identifier by returning the text before the last dot.
func extractModulePath(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[:i]
		}
	}
	return id
}

// Extracts the class name from a fully-qualified identifier by returning the text after the last dot.
func extractClassName(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}

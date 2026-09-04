package jsscanner

import js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"

// matchClassInheritance wires up `class X extends Y` relationships. Base classes
// are resolved by name: same module first, then any class in the topology. This
// is best-effort (JavaScript inheritance can be dynamic), mirroring the Python
// scanner's by-name resolution.
func matchClassInheritance(gt *js.JavaScriptTopology) {
	for id, cls := range gt.Classes {
		delete(cls.Connections, js.ConnInherits)
		delete(cls.Connections, js.ConnInheritedBy)
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

// Resolves a base class ID by searching in the same module first, then across all classes in the topology.
func resolveBaseClassID(baseName string, cls js.JavaScriptClass, gt *js.JavaScriptTopology) *js.ClassID {
	sameModuleID := js.ClassID(extractModulePath(string(cls.ID)) + "." + baseName)
	if _, exists := gt.Classes[sameModuleID]; exists {
		return &sameModuleID
	}
	for id := range gt.Classes {
		if extractClassName(id) == baseName {
			cid := js.ClassID(id)
			return &cid
		}
	}
	return nil
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

	for classID, c := range gt.Classes {
		for _, name := range c.ImplementsRaw {
			ifaceID := resolveInterfaceID(name, string(classID), gt)
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
			parentID := resolveInterfaceID(name, string(ifaceID), gt)
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

// Looks up an interface by name within the same module or by class name across all interfaces.
func resolveInterfaceID(name, fromID string, gt *js.JavaScriptTopology) *js.InterfaceID {
	sameModuleID := js.InterfaceID(extractModulePath(fromID) + "." + name)
	if _, ok := gt.Interfaces[sameModuleID]; ok {
		return &sameModuleID
	}
	for id := range gt.Interfaces {
		if extractClassName(id) == name {
			iid := js.InterfaceID(id)
			return &iid
		}
	}
	return nil
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

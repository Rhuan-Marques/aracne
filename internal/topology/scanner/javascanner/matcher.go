package javascanner

import (
	"sort"
	"strings"

	java "aracne/internal/topology/java"
)

// connExtendsRecords / connImplRecords are private module-node edge kinds that
// persist each resolved hierarchy relation as one "<childID>=>><parentFQN>[:kind]"
// entry, so inherits/implements can be rebuilt whole-graph (across files) on every
// scan — full or incremental. The relation is owned by the CHILD's file (a record
// disappears the moment that file is re-parsed), and resolution to a parent FQN
// happens at record creation (deterministic given the global declared index), so
// two parents with the same simple name in different packages never collide.
//
// These survive FromGeneric/ToGeneric (arbitrary Connection kinds round-trip) and
// the DB connections table (keyed only by source/conn/target, no FK on target).
const (
	connExtendsRecords java.ConnectionKind = "__extends_records"
	connImplRecords    java.ConnectionKind = "__impl_records"
)

// recSep separates the child ID and parent FQN inside a hierarchy record. Java
// FQNs use '.'/'$'/'(' and never contain "=>>".
const recSep = "=>>"

// populateMethods rebuilds each class's/interface's has_method edges from every
// JavaMethod's MethodFrom (owner may be a class OR an interface), and builds the
// overload-aware method index. Whole-graph and idempotent.
func populateMethods(gt *java.JavaTopology, ctx *javaCtx) {
	for id, c := range gt.Classes {
		delete(c.Connections, java.ConnHasMethod)
		gt.Classes[id] = c
	}
	for id, t := range gt.Interfaces {
		delete(t.Connections, java.ConnHasMethod)
		gt.Interfaces[id] = t
	}
	ctx.methodIndex = map[string]map[string][]string{}

	for id, m := range gt.Methods {
		if m.MethodFrom == nil {
			continue
		}
		owner := *m.MethodFrom
		if c, ok := gt.Classes[owner]; ok {
			c.Connections = ensureAppend(c.Connections, java.ConnHasMethod, id)
			gt.Classes[owner] = c
		} else if t, ok := gt.Interfaces[owner]; ok {
			t.Connections = ensureAppend(t.Connections, java.ConnHasMethod, id)
			gt.Interfaces[owner] = t
		}
		if ctx.methodIndex[owner] == nil {
			ctx.methodIndex[owner] = map[string][]string{}
		}
		ctx.methodIndex[owner][m.Name] = append(ctx.methodIndex[owner][m.Name], id)
	}
	// Deterministic ordering of overloads in the index.
	for _, byName := range ctx.methodIndex {
		for name := range byName {
			sort.Strings(byName[name])
		}
	}
}

// rebuildHierarchy clears and rebuilds inherits/inherited_by (struct<->struct for
// class-extends AND iface<->iface for iface-extends) and implements/implemented_by
// (struct->iface) across the WHOLE graph from module-owned hierarchy records.
func rebuildHierarchy(gt *java.JavaTopology) {
	for id, c := range gt.Classes {
		delete(c.Connections, java.ConnInherits)
		delete(c.Connections, java.ConnInheritedBy)
		delete(c.Connections, java.ConnImplements)
		gt.Classes[id] = c
	}
	for id, t := range gt.Interfaces {
		delete(t.Connections, java.ConnInherits)
		delete(t.Connections, java.ConnInheritedBy)
		delete(t.Connections, java.ConnImplementedBy)
		gt.Interfaces[id] = t
	}

	addClassInherits := func(child, parent string) {
		c, ok := gt.Classes[child]
		if !ok {
			return
		}
		p, ok := gt.Classes[parent]
		if !ok {
			return
		}
		c.Connections = ensureAppend(c.Connections, java.ConnInherits, parent)
		gt.Classes[child] = c
		p.Connections = ensureAppend(p.Connections, java.ConnInheritedBy, child)
		gt.Classes[parent] = p
	}
	addIfaceInherits := func(child, parent string) {
		c, ok := gt.Interfaces[child]
		if !ok {
			return
		}
		p, ok := gt.Interfaces[parent]
		if !ok {
			return
		}
		c.Connections = ensureAppend(c.Connections, java.ConnInherits, parent)
		gt.Interfaces[child] = c
		p.Connections = ensureAppend(p.Connections, java.ConnInheritedBy, child)
		gt.Interfaces[parent] = p
	}
	addImplements := func(child, parent string) {
		c, ok := gt.Classes[child]
		if !ok {
			return
		}
		p, ok := gt.Interfaces[parent]
		if !ok {
			return
		}
		c.Connections = ensureAppend(c.Connections, java.ConnImplements, parent)
		gt.Classes[child] = c
		p.Connections = ensureAppend(p.Connections, java.ConnImplementedBy, child)
		gt.Interfaces[parent] = p
	}

	for _, mod := range gt.Modules {
		for _, rec := range mod.Connections[connExtendsRecords] {
			child, parent, kind := decodeExtends(rec)
			if child == "" {
				continue
			}
			if kind == "iface" {
				addIfaceInherits(child, parent)
			} else {
				addClassInherits(child, parent)
			}
		}
		for _, rec := range mod.Connections[connImplRecords] {
			child, parent := decodeImpl(rec)
			if child == "" {
				continue
			}
			addImplements(child, parent)
		}
	}
}

// decodeExtends splits a "<child>=>><parent>:kind" extends record.
func decodeExtends(rec string) (child, parent, kind string) {
	i := strings.Index(rec, recSep)
	if i < 0 {
		return "", "", ""
	}
	child = rec[:i]
	rest := rec[i+len(recSep):]
	if j := strings.LastIndex(rest, ":"); j >= 0 {
		return child, rest[:j], rest[j+1:]
	}
	return child, rest, "class"
}

// decodeImpl splits a "<child>=>><iface>" implements record.
func decodeImpl(rec string) (child, parent string) {
	i := strings.Index(rec, recSep)
	if i < 0 {
		return "", ""
	}
	return rec[:i], rec[i+len(recSep):]
}

// markConstructors sets each class's Constructor to its no-arg constructor if
// present, else the lexicographically-first declared constructor (deterministic).
func markConstructors(gt *java.JavaTopology) {
	for id, c := range gt.Classes {
		c.Constructor = nil
		var ctors []string
		for _, mid := range c.Methods() {
			if m, ok := gt.Methods[mid]; ok && m.IsConstructor {
				ctors = append(ctors, mid)
			}
		}
		sort.Strings(ctors)
		pick := ""
		for _, mid := range ctors {
			if len(gt.Methods[mid].Input) == 0 {
				pick = mid
				break
			}
		}
		if pick == "" && len(ctors) > 0 {
			pick = ctors[0]
		}
		if pick != "" {
			cid := java.FunctionID(pick)
			c.Constructor = &cid
		}
		gt.Classes[id] = c
	}
}

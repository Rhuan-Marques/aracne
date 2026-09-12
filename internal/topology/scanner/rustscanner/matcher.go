package rustscanner

import (
	"sort"
	"strings"

	rust "github.com/Rhuan-Marques/aracne/internal/topology/rust"
)

// connImplRecords is a private module-node edge kind that persists each resolved
// `impl Trait for Type` as one "<typeID>=>><traitID>" entry, so that
// implements/implemented_by can be rebuilt whole-graph (across files) on every
// scan — full or incremental. It is the source of truth a cross-file impl needs:
// the impl's struct may live in another file, so the relationship must be owned
// by the impl's own file (its module), not by the struct, and re-parsing that
// file replaces these records (a removed impl's record disappears).
//
// This survives FromGeneric/ToGeneric (arbitrary Connections kinds round-trip)
// and the DB connections table, which is keyed only by (source_id, conn_type,
// target_id) with no foreign key on target_id, so the encoded non-resource
// target persists fine.
const connImplRecords rust.ConnectionKind = "__impl_records"

// implRecordSep separates the type and trait IDs inside an impl record. Resource
// IDs are "::"-separated module paths, so "=>>" can never collide with one.
const implRecordSep = "=>>"

// attachImpls resolves a file's impl blocks: each impl method becomes a method
// function (ID = <typeID>::<name>, MethodFrom set) owned by the impl's file, and
// each `impl Trait for Type` is persisted as a module impl-record (the
// implements/implemented_by edges themselves are rebuilt whole-graph in
// rebuildImplements). Methods are deduped by ID (last write wins) and edge lists
// are uniqued, since a duplicate edge would abort the file's DB write. The target
// type and trait resolve in the impl's own scope -- its module (an inline `mod` when
// the impl sits in one) and that module's `use` bindings -- so `use crate::b::Thing;
// impl Thing {..}` attaches to b::Thing even when another module also declares a Thing.
func attachImpls(gt *rust.RustTopology, pr *ParseResult, ix *rustIndex) {
	pr.MethodFns = nil
	for _, impl := range pr.Impls {
		sc := ix.scope(pr.FileID, declModule(impl.Module, pr))
		sid := sc.typeID(impl.TypeName)
		if sid == "" {
			continue // impl on an external/unresolved type
		}
		var traitID string
		if impl.TraitName != "" {
			traitID = sc.traitID(impl.TraitName)
		}
		mod := gt.Modules[pr.FileID]
		if mod.Connections == nil {
			mod.Connections = map[rust.ConnectionKind][]string{}
		}
		for _, m := range impl.Methods {
			mid := sid + "::" + m.Name
			owner := sid
			fn := rust.RustFunction{
				ID:           mid,
				Name:         m.Name,
				Input:        m.Input,
				Output:       m.Output,
				Loc:          m.Loc,
				Connections:  map[rust.ConnectionKind][]string{},
				MethodFrom:   &owner,
				IsAsync:      m.IsAsync,
				IsUnsafe:     m.IsUnsafe,
				IsConst:      m.IsConst,
				IsAssociated: m.IsAssociated,
				Receiver:     m.Receiver,
				Visibility:   m.Visibility,
				Exported:     m.Exported,
			}
			gt.Functions[mid] = fn
			mod.Connections[rust.ConnHasFunc] = append(mod.Connections[rust.ConnHasFunc], mid)
			pr.MethodFns = append(pr.MethodFns, resolvedMethod{ID: mid, Body: m.Body, Recv: sid, Module: impl.Module})
		}
		if traitID != "" {
			mod.Connections[connImplRecords] = append(mod.Connections[connImplRecords], sid+implRecordSep+traitID)
		}
		mod.Connections = uniqueConns(mod.Connections)
		gt.Modules[pr.FileID] = mod
	}
}

// decodeImplRecord splits a "<typeID>=>><traitID>" module impl-record.
func decodeImplRecord(rec string) (string, string, bool) {
	i := strings.Index(rec, implRecordSep)
	if i < 0 {
		return "", "", false
	}
	return rec[:i], rec[i+len(implRecordSep):], true
}

// rebuildImplements rebuilds implements/implemented_by across the WHOLE graph
// (clear-then-rebuild), so the relationship is never stale on an incremental
// update. Sources, in priority order:
//  1. every module's persisted impl-records (`impl Trait for Type`), which a
//     cross-file impl owns regardless of where its struct lives, and
//  2. `#[derive(...)]` entries that name an internal trait (external derives like
//     Debug/Clone resolve to nothing and are skipped).
//
// Because it reads only round-tripping data (module connections + struct
// derives), a full scan and an incremental update produce identical edges, and a
// removed cross-file impl drops out the moment its file is re-parsed. A derive
// resolves in the struct's declaring scope (its module and its file's persisted
// `use` bindings), never by picking one of several same-named traits.
func rebuildImplements(gt *rust.RustTopology, ix *rustIndex) {
	for id, st := range gt.Structs {
		delete(st.Connections, rust.ConnImplements)
		gt.Structs[id] = st
	}
	for id, t := range gt.Traits {
		delete(t.Connections, rust.ConnImplementedBy)
		gt.Traits[id] = t
	}

	addEdge := func(sid, tid string) {
		st, ok := gt.Structs[sid]
		if !ok {
			return
		}
		t, ok := gt.Traits[tid]
		if !ok {
			return
		}
		if st.Connections == nil {
			st.Connections = map[rust.ConnectionKind][]string{}
		}
		st.Connections[rust.ConnImplements] = appendUnique(st.Connections[rust.ConnImplements], tid)
		gt.Structs[sid] = st
		if t.Connections == nil {
			t.Connections = map[rust.ConnectionKind][]string{}
		}
		t.Connections[rust.ConnImplementedBy] = appendUnique(t.Connections[rust.ConnImplementedBy], sid)
		gt.Traits[tid] = t
	}

	for _, fid := range sortedKeys(gt.Modules) {
		for _, rec := range gt.Modules[fid].Connections[connImplRecords] {
			if sid, tid, ok := decodeImplRecord(rec); ok {
				addEdge(sid, tid)
			}
		}
	}
	for _, sid := range sortedKeys(gt.Structs) {
		sc := ix.declScope(sid)
		for _, d := range gt.Structs[sid].Derives {
			if tid := sc.traitID(d); tid != "" {
				addEdge(sid, tid)
			}
		}
	}
}

// sortedKeys returns a map's keys in sorted order, for passes whose output order must
// not follow map iteration.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// appendUnique appends v to list unless already present.
func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// populateStructMethods rebuilds each struct's has_method edges from all
// functions' MethodFrom, clearing stale edges first (whole-graph, idempotent).
func populateStructMethods(gt *rust.RustTopology) {
	for id, st := range gt.Structs {
		delete(st.Connections, rust.ConnHasMethod)
		gt.Structs[id] = st
	}
	for _, f := range gt.Functions {
		if f.MethodFrom == nil {
			continue
		}
		st, ok := gt.Structs[*f.MethodFrom]
		if !ok {
			continue
		}
		if st.Connections == nil {
			st.Connections = map[rust.ConnectionKind][]string{}
		}
		st.Connections[rust.ConnHasMethod] = append(st.Connections[rust.ConnHasMethod], string(f.ID))
		st.Connections = uniqueConns(st.Connections)
		gt.Structs[*f.MethodFrom] = st
	}
}

// matchSupertraits rebuilds trait inherits/inherited_by edges from each trait's
// Bounds (`trait A: B`), each resolved in the trait's declaring scope (whole-graph,
// idempotent).
func matchSupertraits(gt *rust.RustTopology, ix *rustIndex) {
	for id, t := range gt.Traits {
		delete(t.Connections, rust.ConnInherits)
		delete(t.Connections, rust.ConnInheritedBy)
		gt.Traits[id] = t
	}
	for _, tid := range sortedKeys(gt.Traits) {
		sc := ix.declScope(tid)
		for _, b := range gt.Traits[tid].Bounds {
			pid := sc.traitID(b)
			if pid == "" || pid == tid {
				continue
			}
			t := gt.Traits[tid]
			if t.Connections == nil {
				t.Connections = map[rust.ConnectionKind][]string{}
			}
			t.Connections[rust.ConnInherits] = append(t.Connections[rust.ConnInherits], pid)
			t.Connections = uniqueConns(t.Connections)
			gt.Traits[tid] = t

			parent := gt.Traits[pid]
			if parent.Connections == nil {
				parent.Connections = map[rust.ConnectionKind][]string{}
			}
			parent.Connections[rust.ConnInheritedBy] = append(parent.Connections[rust.ConnInheritedBy], tid)
			parent.Connections = uniqueConns(parent.Connections)
			gt.Traits[pid] = parent
		}
	}
}

// detectConstructors sets each struct's Constructor to an associated function
// named new/default/with_*/from_* whose return type is Self or the owning type
// (whole-graph, idempotent). Cleared first so incremental rescans don't keep
// stale links.
func detectConstructors(gt *rust.RustTopology) {
	for id, st := range gt.Structs {
		st.Constructor = nil
		gt.Structs[id] = st
	}
	for fid, fn := range gt.Functions {
		if fn.MethodFrom == nil || !fn.IsAssociated || !isCtorName(fn.Name) || len(fn.Output) == 0 {
			continue
		}
		sid := *fn.MethodFrom
		st, ok := gt.Structs[sid]
		if !ok {
			continue
		}
		out := fn.Output[0]
		if out.Typing == "Self" || normType(out.Typing) == "Self" ||
			out.TypingID == sid || normType(out.Typing) == st.Name {
			cid := fid
			st.Constructor = &cid
			gt.Structs[sid] = st
		}
	}
}

// isCtorName reports whether a name follows a constructor convention.
func isCtorName(name string) bool {
	return name == "new" || name == "default" ||
		strings.HasPrefix(name, "with_") || strings.HasPrefix(name, "from_")
}

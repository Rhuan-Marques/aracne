package rustscanner

import (
	"sort"
	"strings"

	rust "github.com/Rhuan-Marques/aracne/internal/topology/rust"
)

// connUseBindings is a private module-node edge kind that persists each `use` leaf of a
// file as one "<local>=>><path>" entry, the path exactly as written (`crate::b::Thing`),
// with globLocal as the local name of a glob import (`use crate::factory::*`).
//
// It exists for the same reason connImplRecords does. The whole-graph passes -- derives
// and supertraits, rebuilt on every scan -- must resolve a name through the imports of the
// file that declared it, and on an incremental update that file was never re-parsed. The
// record is owned by the file's module, so re-parsing the file replaces it wholesale, and
// it round-trips through FromGeneric/ToGeneric and the DB with no code aware of it. The
// path is stored unresolved and resolved at lookup time, so a binding whose target file
// is added later starts resolving without its own file being re-parsed.
const connUseBindings rust.ConnectionKind = "__use_bindings"

// globLocal is the local name a glob import is recorded under; no identifier can be "*".
const globLocal = "*"

// maxUseDepth bounds how many `use` hops a lookup follows (an alias of an alias, a
// re-export of a re-export), so a cyclic re-export cannot recurse forever.
const maxUseDepth = 4

// useBindingRecords encodes a file's use leaves as connUseBindings entries, sorted and
// deduplicated so the record never depends on anything but the file's text.
//
// A `use` inside an inline `mod inner { .. }` is in scope only there, so its local name is
// recorded qualified by the inline path relative to the file's module (`inner::Thing`,
// `inner::*`); an identifier never contains "::", so a file-level binding is unambiguous.
func useBindingRecords(uses []useLeaf, fileModule string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range uses {
		if len(u.Path) == 0 {
			continue
		}
		local := u.LocalName
		switch {
		case u.IsGlob:
			local = globLocal
		case local == "" || local == "_" || local == "self":
			continue // `use x as _` brings a trait into scope but binds no name
		case len(u.Path) == 1 && u.Path[0] == local:
			continue // `use serde;` names the crate it already names
		}
		if rel := inlineRel(u.Module, fileModule); rel != "" {
			local = rel + "::" + local
		}
		rec := local + implRecordSep + strings.Join(u.Path, "::")
		if !seen[rec] {
			seen[rec] = true
			out = append(out, rec)
		}
	}
	sort.Strings(out)
	return out
}

// inlineRel returns module's path below fileModule (`inner` for `p::a::inner` in file
// module `p::a`), or "" when module is the file's own module.
func inlineRel(module, fileModule string) string {
	if module == "" || module == fileModule || !strings.HasPrefix(module, fileModule+"::") {
		return ""
	}
	return module[len(fileModule)+2:]
}

// fileImports is one file's decoded `use` bindings.
type fileImports struct {
	binds map[string][][]string // local name -> the paths it is bound to, sorted
	globs [][]string            // glob-imported paths, sorted
	// The bindings of each inline `mod` in the file, by its path below the file's module.
	inner map[string]*fileImports
}

// rustIndex is what name resolution consults. It is built once per resolveTopology, after
// every file of the pass is applied (so the struct and trait tables are final), and every
// listing it keeps is sorted: no lookup depends on map iteration order.
type rustIndex struct {
	gt          *rust.RustTopology
	files       map[string]*fileImports // file ID -> its use bindings
	moduleFiles map[string][]string     // module path -> the files declaring it, sorted
	ownerFile   map[string]string       // struct/trait ID -> its declaring file
	crates      map[string]bool         // crate names: every module path's root segment
	modules     map[string]bool         // every known module path, file-backed or inline
	structNames map[string][]string     // simple name -> struct IDs, sorted
	traitNames  map[string][]string     // simple name -> trait IDs, sorted
}

// newRustIndex indexes the graph's modules, use bindings and type/trait names.
func newRustIndex(gt *rust.RustTopology) *rustIndex {
	ix := &rustIndex{
		gt:          gt,
		files:       map[string]*fileImports{},
		moduleFiles: map[string][]string{},
		ownerFile:   map[string]string{},
		crates:      map[string]bool{},
		modules:     map[string]bool{},
		structNames: map[string][]string{},
		traitNames:  map[string][]string{},
	}
	fileIDs := make([]string, 0, len(gt.Modules))
	for id := range gt.Modules {
		fileIDs = append(fileIDs, id)
	}
	sort.Strings(fileIDs)
	for _, fid := range fileIDs {
		mod := gt.Modules[fid]
		ix.files[fid] = decodeUseBindings(mod.Connections[connUseBindings])
		if mod.ModulePath != "" {
			ix.moduleFiles[mod.ModulePath] = append(ix.moduleFiles[mod.ModulePath], fid)
			ix.addModule(mod.ModulePath)
			// An inline module with `use` bindings can re-export through them too.
			for _, rel := range sortedKeys(ix.files[fid].inner) {
				ix.moduleFiles[mod.ModulePath+"::"+rel] = append(ix.moduleFiles[mod.ModulePath+"::"+rel], fid)
			}
		}
		// First declaring file wins, in file-ID order: two files mapped to one module path
		// (examples/foo.rs beside src/foo.rs) may declare the same ID.
		for _, kind := range []rust.ConnectionKind{rust.ConnHasStruct, rust.ConnHasTrait} {
			for _, id := range mod.Connections[kind] {
				if _, ok := ix.ownerFile[id]; !ok {
					ix.ownerFile[id] = fid
				}
			}
		}
	}
	for id := range gt.Structs {
		ix.structNames[lastSeg(id)] = append(ix.structNames[lastSeg(id)], id)
		ix.addModule(modulePrefixOf(id))
	}
	for id := range gt.Traits {
		ix.traitNames[lastSeg(id)] = append(ix.traitNames[lastSeg(id)], id)
		ix.addModule(modulePrefixOf(id))
	}
	for id, fn := range gt.Functions {
		if fn.MethodFrom == nil {
			ix.addModule(modulePrefixOf(id))
		}
	}
	for id := range gt.Variables {
		ix.addModule(modulePrefixOf(id))
	}
	for id := range gt.NamedTypes {
		ix.addModule(modulePrefixOf(id))
	}
	for _, ids := range ix.structNames {
		sort.Strings(ids)
	}
	for _, ids := range ix.traitNames {
		sort.Strings(ids)
	}
	return ix
}

// addModule records a module path, its ancestors, and its crate root.
func (ix *rustIndex) addModule(mp string) {
	segs := strings.Split(mp, "::")
	ix.crates[segs[0]] = true
	for i := len(segs); i > 0; i-- {
		p := strings.Join(segs[:i], "::")
		if ix.modules[p] {
			return
		}
		ix.modules[p] = true
	}
}

// decodeUseBindings parses a module's connUseBindings entries.
func decodeUseBindings(recs []string) *fileImports {
	root := &fileImports{binds: map[string][][]string{}, inner: map[string]*fileImports{}}
	for _, rec := range recs {
		local, path, ok := decodeImplRecord(rec)
		if !ok || local == "" || path == "" {
			continue
		}
		fi := root
		if i := strings.LastIndex(local, "::"); i >= 0 {
			// An inline module's binding (see useBindingRecords).
			rel := local[:i]
			local = local[i+2:]
			if fi = root.inner[rel]; fi == nil {
				fi = &fileImports{binds: map[string][][]string{}}
				root.inner[rel] = fi
			}
		}
		segs := strings.Split(path, "::")
		if local == globLocal {
			fi.globs = append(fi.globs, segs)
		} else {
			fi.binds[local] = append(fi.binds[local], segs)
		}
	}
	sortPaths := func(ps [][]string) {
		sort.Slice(ps, func(i, j int) bool { return strings.Join(ps[i], "::") < strings.Join(ps[j], "::") })
	}
	sortAll := func(fi *fileImports) {
		for _, ps := range fi.binds {
			sortPaths(ps)
		}
		sortPaths(fi.globs)
	}
	sortAll(root)
	for _, fi := range root.inner {
		sortAll(fi)
	}
	return root
}

// rustScope is one lookup site: the module whose items are in scope, and the file whose
// `use` bindings apply there.
type rustScope struct {
	ix     *rustIndex
	module []string
	imp    *fileImports
}

// scope returns the lookup scope of a file at the given module path (the file's own, or
// an inline `mod` inside it). An inline module sees its own `use` bindings, not the
// file's: in Rust a `mod` block starts a fresh scope.
func (ix *rustIndex) scope(fileID, modulePath string) rustScope {
	imp := ix.files[fileID]
	if rel := inlineRel(modulePath, ix.gt.Modules[fileID].ModulePath); rel != "" && imp != nil {
		imp = imp.inner[rel]
	}
	if imp == nil {
		imp = &fileImports{}
	}
	var module []string
	if modulePath != "" {
		module = strings.Split(modulePath, "::")
	}
	return rustScope{ix: ix, module: module, imp: imp}
}

// declScope returns the scope a struct or trait was declared in: its own module, with
// the imports of the file that declares it.
func (ix *rustIndex) declScope(id string) rustScope {
	return ix.scope(ix.ownerFile[id], modulePrefixOf(id))
}

// typeID resolves a written type to a struct/enum/union ID ("" for Self, built-in,
// external or unresolved types).
func (sc rustScope) typeID(name string) string {
	return sc.resolve(normTypePath(name), func(id string) bool { return isStruct(sc.ix.gt, id) }, sc.ix.structNames)
}

// traitID resolves a written trait name to a trait ID.
func (sc rustScope) traitID(name string) string {
	return sc.resolve(normTypePath(name), func(id string) bool { return isTrait(sc.ix.gt, id) }, sc.ix.traitNames)
}

// freeFnID resolves a written function path (`make`, `factory::make`,
// `crate::factory::make`) to a free-function ID. Never by bare name: two modules'
// same-named helpers are far too common for a guess to be safe.
func (sc rustScope) freeFnID(path string) string {
	return sc.resolve(path, func(id string) bool { return isFreeFunc(sc.ix.gt, id) }, nil)
}

// varID resolves a written value path to a module-level const/static ID.
func (sc rustScope) varID(path string) string {
	return sc.resolve(path, func(id string) bool { return isVar(sc.ix.gt, id) }, nil)
}

// resolve resolves a written, possibly qualified name to an ID for which has holds.
//
// A qualified path resolves only through its head (see expand): `fmt::Display` names
// no internal trait, however many traits are called Display. A bare name tries, in
// order: an item of this module, an explicit `use` binding, the glob imports, and last
// the by-name index -- taken only when exactly one resource has that name, and never
// when the name is bound by a `use` to something else. Anything else stays unresolved.
func (sc rustScope) resolve(name string, has func(string) bool, byName map[string][]string) string {
	segs := splitPath(name)
	if len(segs) == 0 {
		return ""
	}
	if len(segs) > 1 {
		for _, abs := range sc.expand(segs, 0) {
			if id := sc.ix.follow(abs, has, 0); id != "" {
				return id
			}
		}
		return ""
	}
	n := segs[0]
	switch n {
	case "Self", "self", "super", "crate":
		return ""
	}
	if id := joinPath(sc.module, n); has(id) {
		return id
	}
	bound := sc.imp.binds[n]
	if id := sc.resolveBound(bound, has); id != "" {
		return id
	}
	if len(bound) > 0 {
		// The name is taken: by another crate's item, or by an internal item of another
		// kind. Either way a same-named resource elsewhere is not what it means.
		if !sc.internalBinding(bound) || sc.resolveBound(bound, func(id string) bool { return isInternalSymbol(sc.ix.gt, id) }) != "" {
			return ""
		}
	}
	var hit string
	for _, g := range sc.imp.globs {
		for _, abs := range sc.expand(g, 1) {
			if id := sc.ix.follow(append(abs, n), has, 0); id != "" {
				if hit != "" && hit != id {
					return "" // two glob imports offer the name: ambiguous in Rust too
				}
				hit = id
				break
			}
		}
	}
	if hit != "" {
		return hit
	}
	if ids := byName[n]; len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// resolveBound returns the first of a name's bound paths that resolves.
func (sc rustScope) resolveBound(bound [][]string, has func(string) bool) string {
	for _, p := range bound {
		for _, abs := range sc.expand(p, 1) {
			if id := sc.ix.follow(abs, has, 0); id != "" {
				return id
			}
		}
	}
	return ""
}

// internalBinding reports whether any bound path points into a known module of this
// workspace (as opposed to `use serde::Serialize`).
func (sc rustScope) internalBinding(bound [][]string) bool {
	for _, p := range bound {
		for _, abs := range sc.expand(p, 1) {
			if len(abs) > 1 && sc.ix.modules[strings.Join(abs[:len(abs)-1], "::")] {
				return true
			}
		}
	}
	return false
}

// expand turns a written path into the absolute paths it may denote, most likely first.
// `crate::`, `self::` and `super::` heads are exact. Any other head is tried as a name
// bound by `use` (`use crate::factory;` then `factory::make()`), an item or child module
// of this module, a crate of the workspace by name, a crate-root module (the 2015 form),
// and finally a member of a glob-imported module.
func (sc rustScope) expand(segs []string, depth int) [][]string {
	if len(segs) == 0 || depth > maxUseDepth {
		return nil
	}
	head, rest := segs[0], segs[1:]
	switch head {
	case "crate":
		if len(sc.module) == 0 {
			return nil
		}
		return [][]string{concatPath(sc.module[:1], rest)}
	case "self":
		return [][]string{concatPath(sc.module, rest)}
	case "super":
		up := sc.module
		for len(segs) > 0 && segs[0] == "super" {
			if len(up) <= 1 {
				return nil
			}
			up, segs = up[:len(up)-1], segs[1:]
		}
		return [][]string{concatPath(up, segs)}
	case "Self":
		return nil
	}
	var out [][]string
	if len(rest) > 0 {
		for _, p := range sc.imp.binds[head] {
			for _, e := range sc.expand(p, depth+1) {
				out = append(out, concatPath(e, rest))
			}
		}
	}
	out = append(out, concatPath(sc.module, segs))
	if sc.ix.crates[head] {
		out = append(out, append([]string{}, segs...))
	}
	if len(sc.module) > 0 {
		out = append(out, concatPath(sc.module[:1], segs))
	}
	if len(rest) > 0 {
		for _, g := range sc.imp.globs {
			for _, e := range sc.expand(g, depth+1) {
				out = append(out, concatPath(e, segs))
			}
		}
	}
	seen := map[string]bool{}
	uniq := out[:0]
	for _, p := range out {
		if k := strings.Join(p, "::"); !seen[k] {
			seen[k] = true
			uniq = append(uniq, p)
		}
	}
	return uniq
}

// follow returns abs's ID if it names a resource, else chases a re-export: when abs is
// `M::n` and a file of module M binds n (`pub use shapes::Circle;` in lib.rs) or glob-
// imports a module holding n, the lookup continues from that binding, in M's scope.
func (ix *rustIndex) follow(abs []string, has func(string) bool, depth int) string {
	id := strings.Join(abs, "::")
	if has(id) {
		return id
	}
	if depth >= maxUseDepth || len(abs) < 2 {
		return ""
	}
	mod, n := abs[:len(abs)-1], abs[len(abs)-1]
	for _, f := range ix.moduleFiles[strings.Join(mod, "::")] {
		sc := ix.scope(f, strings.Join(mod, "::"))
		for _, p := range sc.imp.binds[n] {
			for _, e := range sc.expand(p, depth+1) {
				if r := ix.follow(e, has, depth+1); r != "" {
					return r
				}
			}
		}
		for _, g := range sc.imp.globs {
			for _, e := range sc.expand(g, depth+1) {
				if r := ix.follow(append(e, n), has, depth+1); r != "" {
					return r
				}
			}
		}
	}
	return ""
}

// splitPath splits a written "::" path into segments, dropping empty ones (a leading
// `::std::x` global path yields [std x]).
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.TrimSpace(p), "::") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// concatPath returns a fresh slice holding a then b.
func concatPath(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// joinPath renders a module path plus one name as an ID.
func joinPath(module []string, name string) string {
	if len(module) == 0 {
		return name
	}
	return strings.Join(module, "::") + "::" + name
}

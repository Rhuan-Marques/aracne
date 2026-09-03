package rustscanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/topology/contract"
	"aracne/internal/topology/domain"
	rust "aracne/internal/topology/rust"
)

// crateInfo describes one crate root: its directory and Cargo package name.
type crateInfo struct {
	Dir  string
	Name string
}

// crateCtx is the resolved crate model for a project root: the set of crates, the
// external-crate names, and the file<->module-path indexes.
type crateCtx struct {
	Root           string
	Crates         []crateInfo
	ExternalCrates map[string]bool
	moduleOfFile   map[string]string
	fileOfModule   map[string]string
	fileCrate      map[string]string
}

// buildCrateModel scans Cargo.toml (single-crate or a `[workspace]`) to determine
// the crate name(s) and external dependency set, seeding std/core/alloc/proc_macro.
func buildCrateModel(absRoot string) *crateCtx {
	ctx := &crateCtx{
		Root:           absRoot,
		ExternalCrates: map[string]bool{"std": true, "core": true, "alloc": true, "proc_macro": true},
		moduleOfFile:   map[string]string{},
		fileOfModule:   map[string]string{},
		fileCrate:      map[string]string{},
	}

	rootToml := filepath.Join(absRoot, "Cargo.toml")
	name, deps, members, hasPkg := parseCargoToml(rootToml)
	for _, d := range deps {
		ctx.ExternalCrates[d] = true
	}
	if hasPkg && name != "" {
		ctx.Crates = append(ctx.Crates, crateInfo{Dir: absRoot, Name: name})
	}
	for _, m := range members {
		memberDir := filepath.Join(absRoot, filepath.FromSlash(m))
		mn, mdeps, _, mHasPkg := parseCargoToml(filepath.Join(memberDir, "Cargo.toml"))
		for _, d := range mdeps {
			ctx.ExternalCrates[d] = true
		}
		if mHasPkg && mn != "" {
			ctx.Crates = append(ctx.Crates, crateInfo{Dir: memberDir, Name: mn})
		}
	}
	if len(ctx.Crates) == 0 {
		// No Cargo.toml with a [package] section (a bare .rs tree, or a pure [workspace]
		// root with no member package). The crate has no declared name, so use Rust's own
		// word for "this crate" rather than the directory the checkout happens to live in:
		// `crate::shapes::Circle` is what `use crate::shapes::Circle;` in the source says,
		// while `myproj::shapes::Circle` appears nowhere and is unguessable (id-scheme 2).
		ctx.Crates = append(ctx.Crates, crateInfo{Dir: absRoot, Name: "crate"})
	}
	return ctx
}

// parseCargoToml hand-scans a Cargo.toml (no TOML library) for the package name,
// dependency crate names, and workspace members.
func parseCargoToml(path string) (name string, deps []string, members []string, hasPackage bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, false
	}
	section := ""
	inMembers := false
	addDep := func(key string) {
		key = strings.TrimSpace(key)
		if i := strings.IndexByte(key, '.'); i >= 0 {
			key = key[:i]
		}
		key = strings.Trim(strings.TrimSpace(key), "\"'")
		if key != "" {
			deps = append(deps, key)
		}
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if inMembers {
			members = append(members, extractQuoted(line)...)
			if strings.Contains(line, "]") {
				inMembers = false
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			header := strings.TrimSpace(strings.Trim(line, "[]"))
			switch {
			case header == "package":
				section = "package"
				hasPackage = true
			case header == "dependencies" || header == "dev-dependencies" || header == "build-dependencies":
				section = "deps"
			case strings.HasPrefix(header, "dependencies.") ||
				strings.HasPrefix(header, "dev-dependencies.") ||
				strings.HasPrefix(header, "build-dependencies."):
				if i := strings.IndexByte(header, '.'); i >= 0 {
					addDep(header[i+1:])
				}
				section = "depsub"
			case header == "workspace":
				section = "workspace"
			default:
				section = "other"
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch section {
		case "package":
			if key == "name" {
				name = strings.Trim(val, "\"'")
			}
		case "deps":
			addDep(key)
		case "workspace":
			if key == "members" {
				members = append(members, extractQuoted(val)...)
				if !strings.Contains(val, "]") {
					inMembers = true
				}
			}
		}
	}
	return name, deps, members, hasPackage
}

// extractQuoted returns all single/double-quoted substrings on a line.
func extractQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	for _, r := range s {
		if r == '"' || r == '\'' {
			if in {
				if cur.Len() > 0 {
					out = append(out, cur.String())
				}
				cur.Reset()
				in = false
			} else {
				in = true
			}
			continue
		}
		if in {
			cur.WriteRune(r)
		}
	}
	return out
}

// crateOf returns the crate that owns a file (longest matching directory prefix).
func (c *crateCtx) crateOf(abs string) crateInfo {
	best := c.Crates[0]
	bestLen := -1
	for _, cr := range c.Crates {
		if abs == cr.Dir || strings.HasPrefix(abs, cr.Dir+string(filepath.Separator)) {
			if len(cr.Dir) > bestLen {
				best = cr
				bestLen = len(cr.Dir)
			}
		}
	}
	return best
}

// moduleForFile maps a source file to its "::"-qualified module path and the
// owning crate name.
func (c *crateCtx) moduleForFile(abs string) (string, string) {
	cr := c.crateOf(abs)
	rel, err := filepath.Rel(cr.Dir, abs)
	if err != nil {
		rel = filepath.Base(abs)
	}
	rel = filepath.ToSlash(rel)
	rel = strings.TrimSuffix(rel, ".rs")
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return cr.Name, cr.Name
	}
	switch parts[0] {
	case "src":
		inner := parts[1:]
		if len(inner) == 0 {
			return cr.Name, cr.Name
		}
		if inner[len(inner)-1] == "mod" {
			inner = inner[:len(inner)-1]
		}
		if len(inner) == 1 && (inner[0] == "lib" || inner[0] == "main") {
			return cr.Name, cr.Name
		}
		if len(inner) == 0 {
			return cr.Name, cr.Name
		}
		return cr.Name + "::" + strings.Join(inner, "::"), cr.Name
	case "examples":
		return cr.Name + "::" + parts[len(parts)-1], cr.Name
	default:
		stem := parts[len(parts)-1]
		if stem == "mod" && len(parts) >= 2 {
			stem = parts[len(parts)-2]
		}
		return cr.Name + "::" + stem, cr.Name
	}
}

// buildModuleIndex fills moduleOfFile/fileOfModule/fileCrate for the given files.
func (c *crateCtx) buildModuleIndex(files []string) {
	for _, f := range files {
		mp, cn := c.moduleForFile(f)
		c.moduleOfFile[f] = mp
		c.fileCrate[f] = cn
		if _, exists := c.fileOfModule[mp]; !exists {
			c.fileOfModule[mp] = f
		}
	}
}

// longestModulePrefix returns the longest prefix of segs that names a known
// module path.
func (c *crateCtx) longestModulePrefix(segs []string) (string, bool) {
	for i := len(segs); i > 0; i-- {
		cand := strings.Join(segs[:i], "::")
		if _, ok := c.fileOfModule[cand]; ok {
			return cand, true
		}
	}
	return "", false
}

// collectRustFiles walks a project root collecting .rs files, skipping build/test
// and vendor directories.
func collectRustFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// path != root so a dot- or vendor-named ROOT is not pruned by its own
			// basename; WalkDir never visits the root's ancestors, so only the root
			// itself can match on a name it did not choose.
			if path == root {
				return nil
			}
			name := d.Name()
			switch name {
			case "target", "tests", "benches", "node_modules", "vendor", ".git":
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".rs") {
			if domain.PathHidden(path) {
				return nil
			}
			files = append(files, path)
		}
		return nil
	})
	return files
}

// rewriteHead normalizes a use/path's head segment: crate->crateName, self->the
// current module, super->the parent module; other heads are left intact.
func rewriteHead(path []string, crateName string, curSegs []string) []string {
	if len(path) == 0 {
		return path
	}
	switch path[0] {
	case "crate":
		return append([]string{crateName}, path[1:]...)
	case "self":
		return append(append([]string{}, curSegs...), path[1:]...)
	case "super":
		parent := curSegs
		if len(parent) > 0 {
			parent = parent[:len(parent)-1]
		}
		return append(append([]string{}, parent...), path[1:]...)
	default:
		return path
	}
}

// resolveUses builds a file's import map and resolves its internal use targets to
// imported-module files and external uses to dependency crate names.
func (s *RustScanner) resolveUses(pr *ParseResult, ctx *crateCtx, gt *rust.RustTopology) (map[string]rustImport, []string, []string) {
	imap := map[string]rustImport{}
	modFiles := map[string]bool{}
	deps := map[string]bool{}

	crateName := ctx.fileCrate[pr.FileID]
	if crateName == "" {
		crateName = ctx.crateOf(pr.FileID).Name
	}
	curSegs := strings.Split(pr.ModulePath, "::")

	for _, u := range pr.Uses {
		full := rewriteHead(u.Path, crateName, curSegs)
		if len(full) == 0 {
			continue
		}
		head := full[0]
		cand := strings.Join(full, "::")

		// Internal symbol binding.
		if u.LocalName != "" && isInternalSymbol(gt, cand) {
			imap[u.LocalName] = rustImport{Internal: true, ID: cand}
		}
		// Imported-module edge: the longest prefix that names a known module.
		if mp, ok := ctx.longestModulePrefix(full); ok {
			if f, ok := ctx.fileOfModule[mp]; ok && f != pr.FileID {
				modFiles[f] = true
			}
		}
		// External dependency.
		if head != crateName && ctx.ExternalCrates[head] {
			deps[head] = true
			if u.LocalName != "" {
				if _, exists := imap[u.LocalName]; !exists {
					imap[u.LocalName] = rustImport{Internal: false, Dep: head}
				}
			}
			continue
		}
		// Ambiguous head (2015-style crate-relative): try crate-rooting it.
		if head != crateName && !ctx.ExternalCrates[head] {
			crel := append([]string{crateName}, u.Path...)
			ccand := strings.Join(crel, "::")
			if u.LocalName != "" && isInternalSymbol(gt, ccand) {
				imap[u.LocalName] = rustImport{Internal: true, ID: ccand}
			}
			if mp, ok := ctx.longestModulePrefix(crel); ok {
				if f, ok := ctx.fileOfModule[mp]; ok && f != pr.FileID {
					modFiles[f] = true
				}
			}
		}
	}
	return imap, keys(modFiles), keys(deps)
}

// keys returns the keys of a set in arbitrary order.
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// resolveFunctionTypingIDs resolves each result function's param/return type
// names to canonical resource IDs, mapping `Self` to the method's owner. Runs
// before body analysis so callees' TypingIDs are set when callers resolve.
func resolveFunctionTypingIDs(gt *rust.RustTopology, results []*ParseResult) {
	for _, pr := range results {
		for _, fp := range pr.Functions {
			resolveFnTypings(gt, fp.Function.ID, pr, "")
		}
		for _, m := range pr.MethodFns {
			resolveFnTypings(gt, m.ID, pr, m.Recv)
		}
	}
}

// resolveFnTypings resolves and stores TypingIDs for one function's I/O defs.
func resolveFnTypings(gt *rust.RustTopology, fid string, pr *ParseResult, ownerSID string) {
	fn, ok := gt.Functions[fid]
	if !ok {
		return
	}
	changed := false
	for i := range fn.Output {
		if id := typingID(fn.Output[i].Typing, pr, gt, ownerSID); id != "" && fn.Output[i].TypingID != id {
			fn.Output[i].TypingID = id
			changed = true
		}
	}
	for i := range fn.Input {
		if id := typingID(fn.Input[i].Typing, pr, gt, ownerSID); id != "" && fn.Input[i].TypingID != id {
			fn.Input[i].TypingID = id
			changed = true
		}
	}
	if changed {
		gt.Functions[fid] = fn
	}
}

// typingID resolves a type annotation to a canonical resource ID, mapping `Self`
// to the owning type. Returns "" for built-in/external/unresolved types.
func typingID(typ string, pr *ParseResult, gt *rust.RustTopology, ownerSID string) string {
	n := normType(typ)
	if n == "Self" && ownerSID != "" {
		return ownerSID
	}
	return resolveTypeID(n, pr.ModulePath, pr.ImportMap, gt)
}

// analyzeBodies runs body analysis for a file's free functions and impl methods,
// merging the resulting edges onto each function.
func analyzeBodies(gt *rust.RustTopology, pr *ParseResult, ctx *crateCtx) {
	apply := func(id string, body *rustBody, ownerSID string) {
		if body == nil {
			return
		}
		conns := analyzeFunctionBody(body, pr, gt, ctx, ownerSID)
		fn, ok := gt.Functions[id]
		if !ok {
			return
		}
		if fn.Connections == nil {
			fn.Connections = map[rust.ConnectionKind][]string{}
		}
		for k, v := range conns {
			fn.Connections[k] = append(fn.Connections[k], v...)
		}
		fn.Connections = uniqueConns(fn.Connections)
		gt.Functions[id] = fn
	}
	for _, fp := range pr.Functions {
		apply(fp.Function.ID, fp.Body, "")
	}
	for _, m := range pr.MethodFns {
		apply(m.ID, m.Body, m.Recv)
	}
}

// analyzeFunctionBody resolves a body's records into call/uses edges. ownerSID is
// the receiver type ID for `self`/`Self` resolution (empty for free functions).
func analyzeFunctionBody(body *rustBody, pr *ParseResult, gt *rust.RustTopology, ctx *crateCtx, ownerSID string) map[rust.ConnectionKind][]string {
	conn := map[rust.ConnectionKind][]string{}
	// current is the call being resolved. Every ConnCalls edge below is emitted from inside
	// the body.Calls loop, so reading it in add() records the argument shape at all three
	// emission sites without threading it through each.
	var current *rustCall
	var callSites []string
	add := func(kind rust.ConnectionKind, id string) {
		if id == "" {
			return
		}
		// Recorded BEFORE the edge dedupe below returns: two calls to the same callee are
		// one edge but two call sites, and a function called once correctly and once
		// wrongly must still report the wrong one.
		if kind == rust.ConnCalls && current != nil {
			rec := contract.EncodeCallSite(rustCallSite(id, current))
			if rec != "" && !containsRec(callSites, rec) {
				callSites = append(callSites, rec)
			}
		}
		for _, e := range conn[kind] {
			if e == id {
				return
			}
		}
		conn[kind] = append(conn[kind], id)
	}

	varType := map[string]string{}
	for _, l := range body.Lets {
		switch {
		case l.StructLit != "":
			if sid := resolveTypeID(l.StructLit, pr.ModulePath, pr.ImportMap, gt); sid != "" {
				varType[l.Name] = sid
				add(rust.ConnUsesStruct, sid)
			}
		case l.CallType != "":
			tsid := ownerSID
			if l.CallType != "Self" {
				tsid = resolveTypeID(l.CallType, pr.ModulePath, pr.ImportMap, gt)
			}
			if tsid != "" {
				add(rust.ConnUsesStruct, tsid)
				mid := tsid + "::" + l.CallName
				if mfn, ok := gt.Functions[mid]; ok {
					add(rust.ConnCalls, mid)
					if rid := returnStructID(mfn, tsid); rid != "" {
						varType[l.Name] = rid
					}
				}
			}
		case l.CallFunc != "":
			if fid := resolveFreeFnID(l.CallFunc, pr.ModulePath, pr.ImportMap, gt); fid != "" {
				add(rust.ConnCalls, fid)
				if f, ok := gt.Functions[fid]; ok && len(f.Output) > 0 && f.Output[0].TypingID != "" {
					varType[l.Name] = f.Output[0].TypingID
				}
			}
		case l.AliasOf != "":
			if t, ok := varType[l.AliasOf]; ok {
				varType[l.Name] = t
			}
		}
	}

	for i := range body.Calls {
		c := body.Calls[i]
		current = &body.Calls[i]
		switch {
		case c.Method != "":
			sid := ""
			if c.IsSelf || c.ObjName == "self" {
				sid = ownerSID
			} else if t, ok := varType[c.ObjName]; ok {
				sid = t
			}
			if sid != "" {
				if mid := sid + "::" + c.Method; isFunc(gt, mid) {
					add(rust.ConnCalls, mid)
				}
			}
		case c.PathName != "":
			sid := ownerSID
			if !c.IsSelf {
				sid = resolveTypeID(c.PathType, pr.ModulePath, pr.ImportMap, gt)
			}
			if sid != "" {
				add(rust.ConnUsesStruct, sid)
				if mid := sid + "::" + c.PathName; isFunc(gt, mid) {
					add(rust.ConnCalls, mid)
				}
			} else if imp, ok := pr.ImportMap[c.PathType]; ok && !imp.Internal {
				add(rust.ConnUsesDep, imp.Dep)
			}
		case c.Func != "":
			if fid := resolveFreeFnID(c.Func, pr.ModulePath, pr.ImportMap, gt); fid != "" {
				add(rust.ConnCalls, fid)
			} else if imp, ok := pr.ImportMap[c.Func]; ok && !imp.Internal {
				add(rust.ConnUsesDep, imp.Dep)
			}
		}
	}
	current = nil

	for _, name := range body.Structs {
		if sid := resolveTypeID(name, pr.ModulePath, pr.ImportMap, gt); sid != "" {
			add(rust.ConnUsesStruct, sid)
		}
	}

	crateName := ctx.fileCrate[pr.FileID]
	curSegs := strings.Split(pr.ModulePath, "::")
	for _, r := range body.Refs {
		if len(r.Segs) == 0 {
			continue
		}
		if id, ok := resolveRefVar(r.Segs, pr, gt, crateName, curSegs); ok {
			add(rust.ConnUsesVar, id)
			continue
		}
		full := rewriteHead(r.Segs, crateName, curSegs)
		if len(full) > 1 && ctx.ExternalCrates[full[0]] {
			add(rust.ConnUsesDep, full[0])
		}
	}

	// Sorted: the at-scale suite compares connection sets across scan modes byte-for-byte,
	// and resolution order is not guaranteed to agree between a cold and an incremental pass.
	if len(callSites) > 0 {
		sort.Strings(callSites)
		conn[rust.ConnectionKind(contract.CallSitesConn)] = callSites
	}

	return conn
}

// resolveRefVar resolves a value-position reference to a module-level variable ID.
func resolveRefVar(segs []string, pr *ParseResult, gt *rust.RustTopology, crateName string, curSegs []string) (string, bool) {
	if len(segs) == 1 {
		local := pr.ModulePath + "::" + segs[0]
		if isVar(gt, local) {
			return local, true
		}
		if imp, ok := pr.ImportMap[segs[0]]; ok && imp.Internal && isVar(gt, imp.ID) {
			return imp.ID, true
		}
		return "", false
	}
	full := rewriteHead(segs, crateName, curSegs)
	cand := strings.Join(full, "::")
	if isVar(gt, cand) {
		return cand, true
	}
	return "", false
}

// returnStructID resolves a function's return type to a struct ID, mapping `Self`
// (or a bare owning-type return) to ownerSID.
func returnStructID(fn rust.RustFunction, ownerSID string) string {
	if len(fn.Output) == 0 {
		return ""
	}
	out := fn.Output[0]
	if out.TypingID != "" {
		return out.TypingID
	}
	if normType(out.Typing) == "Self" {
		return ownerSID
	}
	return ""
}

// resolveTypeID resolves a type name to a struct/enum ID: local module first,
// then the file's import map, then by-name across the graph. Returns "" when the
// type is external/unresolved.
func resolveTypeID(name, modulePath string, imap map[string]rustImport, gt *rust.RustTopology) string {
	n := normType(name)
	if n == "" || n == "Self" {
		return ""
	}
	local := modulePath + "::" + n
	if isStruct(gt, local) {
		return local
	}
	if imap != nil {
		if imp, ok := imap[n]; ok && imp.Internal && isStruct(gt, imp.ID) {
			return imp.ID
		}
	}
	for id := range gt.Structs {
		if lastSeg(id) == n {
			return id
		}
	}
	return ""
}

// resolveFreeFnID resolves a called name to a free-function ID via the local
// module or the import map (never by-name, to avoid cross-module false edges).
func resolveFreeFnID(name, modulePath string, imap map[string]rustImport, gt *rust.RustTopology) string {
	local := modulePath + "::" + name
	if fn, ok := gt.Functions[local]; ok && fn.MethodFrom == nil {
		return local
	}
	if imap != nil {
		if imp, ok := imap[name]; ok && imp.Internal {
			if fn, ok := gt.Functions[imp.ID]; ok && fn.MethodFrom == nil {
				return imp.ID
			}
		}
	}
	return ""
}

// normType strips references, mutability, generics, and path qualifiers from a
// type string down to its base type name.
func normType(raw string) string {
	s := strings.TrimSpace(raw)
	for {
		switch {
		case strings.HasPrefix(s, "&"):
			s = strings.TrimSpace(s[1:])
		case strings.HasPrefix(s, "mut "):
			s = strings.TrimSpace(s[4:])
		case strings.HasPrefix(s, "dyn "):
			s = strings.TrimSpace(s[4:])
		case strings.HasPrefix(s, "impl "):
			s = strings.TrimSpace(s[5:])
		default:
			goto stripped
		}
	}
stripped:
	// Drop a leading lifetime (e.g. "'a Foo").
	if strings.HasPrefix(s, "'") {
		if i := strings.IndexByte(s, ' '); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	if i := strings.IndexByte(s, '<'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	return strings.TrimSpace(s)
}

// lastSeg returns the final "::"-segment of a resource ID.
func lastSeg(id string) string {
	if i := strings.LastIndex(id, "::"); i >= 0 {
		return id[i+2:]
	}
	return id
}

// modulePrefixOf returns a resource ID's enclosing module path (everything before
// the final "::"-segment).
func modulePrefixOf(id string) string {
	if i := strings.LastIndex(id, "::"); i >= 0 {
		return id[:i]
	}
	return id
}

// isInternalSymbol reports whether an ID names any internal resource.
func isInternalSymbol(gt *rust.RustTopology, id string) bool {
	if _, ok := gt.Functions[id]; ok {
		return true
	}
	if _, ok := gt.Structs[id]; ok {
		return true
	}
	if _, ok := gt.Traits[id]; ok {
		return true
	}
	if _, ok := gt.NamedTypes[id]; ok {
		return true
	}
	if _, ok := gt.Variables[id]; ok {
		return true
	}
	return false
}

// isStruct reports whether an ID names a struct/enum/union.
func isStruct(gt *rust.RustTopology, id string) bool {
	_, ok := gt.Structs[id]
	return ok
}

// isFunc reports whether an ID names a function/method.
func isFunc(gt *rust.RustTopology, id string) bool {
	_, ok := gt.Functions[id]
	return ok
}

// isVar reports whether an ID names a module-level variable.
func isVar(gt *rust.RustTopology, id string) bool {
	_, ok := gt.Variables[id]
	return ok
}

// collectDependencies rebuilds the topology dependency list from modules' imported
// dependencies, deduped.
func collectDependencies(gt *rust.RustTopology) {
	seen := map[string]bool{}
	gt.Dependencies = nil
	for _, mod := range gt.Modules {
		for _, dep := range mod.Connections[rust.ConnImportsDep] {
			if !seen[dep] {
				seen[dep] = true
				gt.Dependencies = append(gt.Dependencies, rust.RustDependency{CratePath: rust.DependencyPath(dep)})
			}
		}
	}
}

// uniqueConns dedups each connection list, dropping empty kinds.
func uniqueConns(conns map[rust.ConnectionKind][]string) map[rust.ConnectionKind][]string {
	result := make(map[rust.ConnectionKind][]string, len(conns))
	for k, v := range conns {
		seen := map[string]bool{}
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

// rustCallSite reads the argument shape of one Rust call. Rust has no default arguments,
// no overloads and no varargs, so arity alone is a sound check -- which is why the token
// list only sharpens the verdict and never weakens it.
func rustCallSite(calleeID string, c *rustCall) contract.CallSite {
	site := contract.CallSite{CalleeID: calleeID, N: len(c.Args)}
	anyKnown := false
	types := make([]*string, 0, len(c.Args))
	for _, tok := range c.Args {
		if tok == "" {
			types = append(types, nil)
			continue
		}
		anyKnown = true
		t := tok
		types = append(types, &t)
	}
	if anyKnown {
		site.Types = types
	}
	return site
}

// containsRec reports whether a record is already recorded.
func containsRec(recs []string, rec string) bool {
	for _, r := range recs {
		if r == rec {
			return true
		}
	}
	return false
}

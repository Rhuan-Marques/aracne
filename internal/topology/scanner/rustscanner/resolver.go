package rustscanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	rust "github.com/Rhuan-Marques/aracne/internal/topology/rust"
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
	name, deps, members, excludes, hasPkg := parseCargoToml(rootToml)
	for _, d := range deps {
		ctx.ExternalCrates[crateIdent(d)] = true
	}
	if hasPkg && name != "" {
		ctx.Crates = append(ctx.Crates, crateInfo{Dir: absRoot, Name: crateIdent(name)})
	}
	for _, memberDir := range expandMembers(absRoot, members, excludes) {
		if memberDir == absRoot {
			continue // `members = ["."]`: the root package, registered above
		}
		mn, mdeps, _, _, mHasPkg := parseCargoToml(filepath.Join(memberDir, "Cargo.toml"))
		for _, d := range mdeps {
			ctx.ExternalCrates[crateIdent(d)] = true
		}
		if mHasPkg && mn != "" {
			ctx.Crates = append(ctx.Crates, crateInfo{Dir: memberDir, Name: crateIdent(mn)})
		}
	}
	// A crate does not have to sit at the scan root, and does not have to be listed by a
	// workspace: a repository can keep its Rust under a subdirectory (`src-tauri/` beside a
	// package.json, `rust/` in a polyglot monorepo) with no root Cargo.toml at all. The walk
	// finds those .rs files either way, so before they were indexed under the nameless
	// `crate` root with only the file stem for a module path -- src/net/util.rs and
	// src/db/util.rs both became `crate::util`, and one silently replaced the other. Every
	// Cargo.toml with a [package] under the root is therefore a crate root. One the
	// workspace explicitly excludes is not: Cargo does not build it as part of this
	// workspace, and expandMembers already leaves it out.
	registered := map[string]bool{}
	for _, cr := range ctx.Crates {
		registered[cr.Dir] = true
	}
	var excludedDirs []string
	for _, e := range excludes {
		excludedDirs = append(excludedDirs, globDirs(absRoot, e)...)
	}
	for _, pkgDir := range discoverPackageDirs(absRoot) {
		if registered[pkgDir] || underAny(pkgDir, excludedDirs) {
			continue
		}
		pn, pdeps, _, _, pHasPkg := parseCargoToml(filepath.Join(pkgDir, "Cargo.toml"))
		if !pHasPkg || pn == "" {
			continue
		}
		for _, d := range pdeps {
			ctx.ExternalCrates[crateIdent(d)] = true
		}
		registered[pkgDir] = true
		ctx.Crates = append(ctx.Crates, crateInfo{Dir: pkgDir, Name: crateIdent(pn)})
	}
	// A path dependency on another member of the workspace names an internal crate, whose
	// items are nodes of this topology: it is not an external dependency.
	for _, cr := range ctx.Crates {
		delete(ctx.ExternalCrates, cr.Name)
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

// crateIdent is the name source code uses for a Cargo package: Cargo turns `-` into `_`
// (package `beta-core` is `beta_core::` in a path), so that is the form IDs are rooted at
// and the form a `use` head is matched against.
func crateIdent(pkg string) string {
	return strings.ReplaceAll(pkg, "-", "_")
}

// expandMembers resolves `[workspace].members` to member directories the way Cargo does:
// each entry is a path or a glob, a directory an `exclude` entry names (or sits under) is
// dropped, and so is one with no Cargo.toml -- Cargo would reject it, and `crates/*`
// routinely matches a stray non-crate directory. The result is sorted and deduplicated, so
// the crate order never depends on the order entries were written or the filesystem lists.
func expandMembers(root string, members, excludes []string) []string {
	var excluded []string
	for _, e := range excludes {
		excluded = append(excluded, globDirs(root, e)...)
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range members {
		for _, dir := range globDirs(root, m) {
			if seen[dir] || underAny(dir, excluded) {
				continue
			}
			seen[dir] = true
			if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err != nil {
				continue
			}
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

// globDirs expands one workspace path pattern relative to root. A pattern with no glob
// metacharacter is returned as the path it names; otherwise each "/"-segment is matched
// against directory entries with filepath.Match (`*`, `?`, `[...]`), and a `**` segment
// matches zero or more directories, as in the glob crate Cargo uses.
func globDirs(root, pattern string) []string {
	pattern = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(pattern)), "/")
	if pattern == "" {
		return nil
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{filepath.Join(root, filepath.FromSlash(pattern))}
	}
	var out []string
	var walk func(dir string, segs []string)
	walk = func(dir string, segs []string) {
		if len(segs) == 0 {
			out = append(out, dir)
			return
		}
		seg, rest := segs[0], segs[1:]
		switch {
		case seg == "**":
			walk(dir, rest)
			for _, sub := range subdirs(dir) {
				// A recursive glob never descends into build output or dot-directories:
				// neither holds a workspace member, and target/ can be enormous.
				if base := filepath.Base(sub); base == "target" || base == "node_modules" || strings.HasPrefix(base, ".") {
					continue
				}
				walk(sub, segs)
			}
		case !strings.ContainsAny(seg, "*?["):
			if next := filepath.Join(dir, seg); isDir(next) {
				walk(next, rest)
			}
		default:
			for _, sub := range subdirs(dir) {
				if ok, _ := filepath.Match(seg, filepath.Base(sub)); ok {
					walk(sub, rest)
				}
			}
		}
	}
	walk(root, strings.Split(pattern, "/"))
	return out
}

// subdirs lists a directory's subdirectories in name order.
func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if p := filepath.Join(dir, e.Name()); e.IsDir() || (e.Type()&os.ModeSymlink != 0 && isDir(p)) {
			out = append(out, p)
		}
	}
	return out
}

// isDir reports whether path is a directory (following symlinks).
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// underAny reports whether dir is, or sits under, any of the given directories.
func underAny(dir string, parents []string) bool {
	for _, p := range parents {
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// parseCargoToml hand-scans a Cargo.toml (no TOML library) for the package name,
// dependency crate names, and workspace members and excludes.
func parseCargoToml(path string) (name string, deps []string, members []string, excludes []string, hasPackage bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, nil, false
	}
	section := ""
	// inArray is the list a multi-line `key = [` array is still being read into.
	var inArray *[]string
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
		line := strings.TrimSpace(stripTomlComment(raw))
		if line == "" {
			continue
		}
		if inArray != nil {
			*inArray = append(*inArray, extractQuoted(line)...)
			if strings.Contains(line, "]") {
				inArray = nil
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			// `[target.'cfg(unix)'.dependencies]` declares dependencies exactly as
			// `[dependencies]` does, only for some targets.
			header := stripTargetSpec(strings.TrimSpace(strings.Trim(line, "[]")))
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
				if q := extractQuoted(val); len(q) > 0 {
					name = q[0]
				} else {
					name = strings.Trim(val, "\"'")
				}
			}
		case "deps":
			addDep(key)
		case "workspace":
			var dst *[]string
			switch key {
			case "members":
				dst = &members
			case "exclude":
				dst = &excludes
			}
			if dst != nil {
				*dst = append(*dst, extractQuoted(val)...)
				if !strings.Contains(val, "]") {
					inArray = dst
				}
			}
		}
	}
	return name, deps, members, excludes, hasPackage
}

// stripTomlComment drops a `#` comment from a TOML line, leaving a `#` inside a quoted
// string alone (`name = "p8" # the crate` is the name `p8`).
func stripTomlComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}

// stripTargetSpec reduces a platform-specific table header to the table it scopes:
// `target.'cfg(unix)'.dependencies` and `target.x86_64-pc-windows-gnu.dev-dependencies.foo`
// become `dependencies` and `dev-dependencies.foo`. Any other header is returned as is.
func stripTargetSpec(header string) string {
	rest, ok := strings.CutPrefix(header, "target.")
	if !ok || rest == "" {
		return header
	}
	if q := rest[0]; q == '\'' || q == '"' {
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return header
		}
		rest = rest[end+2:]
	} else if i := strings.IndexByte(rest, '.'); i >= 0 {
		rest = rest[i:]
	} else {
		return header
	}
	return strings.TrimPrefix(rest, ".")
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

// isCrate reports whether name is a crate of this project (the workspace's own crates).
func (c *crateCtx) isCrate(name string) bool {
	for _, cr := range c.Crates {
		if cr.Name == name {
			return true
		}
	}
	return false
}

// crateOf returns the crate that owns a file (longest matching directory prefix). A file
// under no crate -- only possible below a pure `[workspace]` root, e.g. inside an excluded
// directory -- belongs to no declared crate, so it gets the same nameless `crate` root a
// bare .rs tree does rather than being filed under whichever member happens to be first.
func (c *crateCtx) crateOf(abs string) crateInfo {
	best := crateInfo{Dir: c.Root, Name: "crate"}
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
		if len(inner) == 1 && inner[0] == "lib" {
			return cr.Name, cr.Name
		}
		if len(inner) == 1 && inner[0] == "main" {
			// src/main.rs is the root of the package's binary crate. Alone it is the only
			// root and keeps the crate name; beside src/lib.rs it is a second crate whose
			// items would collide with the library's, so it is rooted at `<crate>::main`.
			if _, err := os.Stat(filepath.Join(cr.Dir, "src", "lib.rs")); err == nil {
				return cr.Name + "::main", cr.Name
			}
			return cr.Name, cr.Name
		}
		if len(inner) == 0 {
			return cr.Name, cr.Name
		}
		return cr.Name + "::" + strings.Join(inner, "::"), cr.Name
	case "examples":
		// examples/foo.rs and examples/foo/main.rs are both the example `foo`: a
		// multi-file example is named by its directory, not by its `main.rs`.
		if len(parts) == 3 && parts[2] == "main" {
			return cr.Name + "::" + parts[1], cr.Name
		}
		return cr.Name + "::" + parts[len(parts)-1], cr.Name
	default:
		// Anything not under the crate's src/ or examples/ -- in practice a bare .rs tree
		// with no Cargo.toml anywhere, where there is no src/ to key off. The whole
		// relative path is the module path: keeping only the file stem made a/util.rs and
		// b/util.rs the same module, and whichever was indexed second replaced the first.
		segs := parts
		if segs[len(segs)-1] == "mod" && len(segs) >= 2 {
			segs = segs[:len(segs)-1]
		}
		return cr.Name + "::" + strings.Join(segs, "::"), cr.Name
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

// discoverPackageDirs walks a project root collecting every directory that holds a
// Cargo.toml, pruning the same build/test/vendor directories collectRustFiles does -- a
// crate whose sources the scan would never read is no crate of this topology. The walk is
// lexical, so the order (and therefore the crate order) is stable.
func discoverPackageDirs(root string) []string {
	var dirs []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if prunedScanDir(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Cargo.toml" && !domain.PathHidden(path) {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	return dirs
}

// prunedScanDir reports whether a directory is one neither the file walk nor crate
// discovery descends into: a build/test/vendor output, a dot-directory, or one the
// project's scan.ignore rules exclude.
func prunedScanDir(path, name string) bool {
	switch name {
	case "target", "tests", "benches", "node_modules", "vendor", ".git":
		return true
	}
	if strings.HasPrefix(name, ".") && name != "." {
		return true
	}
	return domain.PathPruneDir(path)
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
			if prunedScanDir(path, d.Name()) {
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
	for _, u := range pr.Uses {
		// `self::`/`super::` are relative to the module the `use` is written in, which is
		// an inline `mod` when the `use` sits inside one.
		curSegs := strings.Split(declModule(u.Module, pr), "::")
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
		// A crate of this workspace named by its crate name is an absolute path, resolved
		// above: neither an external dependency nor a crate-relative (2015) path.
		if ctx.isCrate(head) {
			continue
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
func resolveFunctionTypingIDs(gt *rust.RustTopology, results []*ParseResult, ix *rustIndex) {
	for _, pr := range results {
		for _, fp := range pr.Functions {
			resolveFnTypings(gt, fp.Function.ID, ix.scope(pr.FileID, declModule(fp.Module, pr)), "")
		}
		for _, m := range pr.MethodFns {
			resolveFnTypings(gt, m.ID, ix.scope(pr.FileID, declModule(m.Module, pr)), m.Recv)
		}
	}
}

// declModule is the module a declaration of pr sits in: its recorded enclosing module (an
// inline `mod` inside the file), or else the file's own module.
func declModule(module string, pr *ParseResult) string {
	if module != "" {
		return module
	}
	return pr.ModulePath
}

// resolveFnTypings resolves and stores TypingIDs for one function's I/O defs.
func resolveFnTypings(gt *rust.RustTopology, fid string, sc rustScope, ownerSID string) {
	fn, ok := gt.Functions[fid]
	if !ok {
		return
	}
	changed := false
	for i := range fn.Output {
		if id := typingID(fn.Output[i].Typing, sc, ownerSID); id != "" && fn.Output[i].TypingID != id {
			fn.Output[i].TypingID = id
			changed = true
		}
	}
	for i := range fn.Input {
		if id := typingID(fn.Input[i].Typing, sc, ownerSID); id != "" && fn.Input[i].TypingID != id {
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
func typingID(typ string, sc rustScope, ownerSID string) string {
	if normType(typ) == "Self" && ownerSID != "" {
		return ownerSID
	}
	return sc.typeID(typ)
}

// analyzeBodies runs body analysis for a file's free functions and impl methods,
// merging the resulting edges onto each function.
func analyzeBodies(gt *rust.RustTopology, pr *ParseResult, ctx *crateCtx, ix *rustIndex) {
	apply := func(id string, body *rustBody, ownerSID, module string) {
		if body == nil {
			return
		}
		fn, ok := gt.Functions[id]
		if !ok {
			return
		}
		conns := analyzeFunctionBody(body, pr, gt, ctx, ix.scope(pr.FileID, declModule(module, pr)), ownerSID, fn.Input)
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
		apply(fp.Function.ID, fp.Body, "", fp.Module)
	}
	for _, m := range pr.MethodFns {
		apply(m.ID, m.Body, m.Recv, m.Module)
	}
}

// analyzeFunctionBody resolves a body's records into call/uses edges. ownerSID is
// the receiver type ID for `self`/`Self` resolution (empty for free functions); params
// are the function's declared parameters, whose resolved types type their bindings.
func analyzeFunctionBody(body *rustBody, pr *ParseResult, gt *rust.RustTopology, ctx *crateCtx, sc rustScope, ownerSID string, params []rust.VariableDefinition) map[rust.ConnectionKind][]string {
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
			site := *current
			if site.PathName != "" && len(site.Args) > 0 && hasReceiver(gt, id) {
				// `Circle::area(&c, 2.0)` passes the receiver as its first argument, and
				// the receiver is not among the declared parameters.
				site.Args = site.Args[1:]
			}
			rec := contract.EncodeCallSite(rustCallSite(id, &site))
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
	// A parameter's annotation types its binding: `fn f(c: &Circle) { c.area() }`.
	for _, p := range params {
		if name := bindingName(p.Name); name != "" && p.TypingID != "" {
			varType[name] = p.TypingID
		}
	}
	for _, l := range body.Lets {
		// An annotation (`let c: Circle = ..`) names the type outright, so it wins over
		// whatever the value lets us infer; the value is still walked for its edges.
		annot := ""
		if l.TypeAnn != "" {
			if normType(l.TypeAnn) == "Self" {
				annot = ownerSID
			} else {
				annot = sc.typeID(l.TypeAnn)
			}
			add(rust.ConnUsesStruct, annot)
		}
		switch {
		case l.StructLit != "":
			if sid := sc.typeID(l.StructLit); sid != "" {
				varType[l.Name] = sid
				add(rust.ConnUsesStruct, sid)
			}
		case l.CallType != "":
			// The path is a module first (`factory::make_circle()`), then a type.
			if fid := freeFnOfPath(sc, l.CallType, l.CallPath, l.CallName); fid != "" {
				add(rust.ConnCalls, fid)
				if f, ok := gt.Functions[fid]; ok && len(f.Output) > 0 && f.Output[0].TypingID != "" {
					varType[l.Name] = f.Output[0].TypingID
				}
				break
			}
			tsid := ownerSID
			if l.CallType != "Self" {
				tsid = sc.typeID(strings.Join(l.CallPath, "::"))
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
			if fid := sc.freeFnID(l.CallFunc); fid != "" {
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
		if annot != "" {
			varType[l.Name] = annot
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
			// The path is a module first (`factory::make_circle()`), then a type.
			if fid := freeFnOfPath(sc, c.PathType, c.PathSegs, c.PathName); fid != "" {
				add(rust.ConnCalls, fid)
				continue
			}
			sid := ownerSID
			if !c.IsSelf {
				sid = sc.typeID(strings.Join(c.PathSegs, "::"))
			}
			if sid != "" {
				add(rust.ConnUsesStruct, sid)
				if mid := sid + "::" + c.PathName; isFunc(gt, mid) {
					add(rust.ConnCalls, mid)
				}
			} else if imp, ok := pr.ImportMap[c.PathType]; ok && !imp.Internal {
				add(rust.ConnUsesDep, imp.Dep)
			} else if missing := sc.missingInModule(c.PathSegs, c.PathName); missing != "" {
				add(rust.ConnectionKind(domain.MissingRefsConn), missing)
			}
		case c.Func != "":
			if fid := sc.freeFnID(c.Func); fid != "" {
				add(rust.ConnCalls, fid)
			} else if imp, ok := pr.ImportMap[c.Func]; ok && !imp.Internal {
				add(rust.ConnUsesDep, imp.Dep)
			} else if missing := sc.missingBinding(c.Func); missing != "" {
				add(rust.ConnectionKind(domain.MissingRefsConn), missing)
			}
		}
	}
	current = nil

	for _, name := range body.Structs {
		if sid := sc.typeID(name); sid != "" {
			add(rust.ConnUsesStruct, sid)
		}
	}

	crateName := ctx.fileCrate[pr.FileID]
	curSegs := strings.Split(pr.ModulePath, "::")
	for _, r := range body.Refs {
		if len(r.Segs) == 0 {
			continue
		}
		if id := sc.varID(strings.Join(r.Segs, "::")); id != "" {
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

// bindingName returns the variable a simple parameter pattern binds (`c`, `mut c`), or ""
// for a destructuring or wildcard pattern.
func bindingName(pat string) string {
	pat = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(pat), "mut "))
	if pat == "" || pat == "_" || pat == "self" {
		return ""
	}
	for _, r := range pat {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return pat
}

// freeFnOfPath resolves a `path::name()` call as a free function in a module --
// `factory::make()`, `crate::factory::make()`, `super::make()` -- before the caller tries
// the path as a type. A `Self::` path is always the owning type.
func freeFnOfPath(sc rustScope, pathType string, path []string, name string) string {
	if pathType == "Self" || len(path) == 0 || name == "" {
		return ""
	}
	return sc.freeFnID(strings.Join(path, "::") + "::" + name)
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

// normType strips references, mutability, generics, and path qualifiers from a
// type string down to its base type name.
func normType(raw string) string {
	return strings.TrimSpace(lastSeg(normTypePath(raw)))
}

// normTypePath strips references, mutability, generics and lifetimes from a type
// string but keeps its path (`&mut crate::b::Thing<T>` -> `crate::b::Thing`).
func normTypePath(raw string) string {
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
	return strings.TrimSuffix(strings.TrimSpace(s), "::")
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

// isTrait reports whether an ID names a trait.
func isTrait(gt *rust.RustTopology, id string) bool {
	_, ok := gt.Traits[id]
	return ok
}

// isFunc reports whether an ID names a function/method.
func isFunc(gt *rust.RustTopology, id string) bool {
	_, ok := gt.Functions[id]
	return ok
}

// isFreeFunc reports whether an ID names a free (non-method) function.
func isFreeFunc(gt *rust.RustTopology, id string) bool {
	fn, ok := gt.Functions[id]
	return ok && fn.MethodFrom == nil
}

// hasReceiver reports whether an ID names a method that takes `self`, which a path
// (UFCS) call passes explicitly.
func hasReceiver(gt *rust.RustTopology, id string) bool {
	fn, ok := gt.Functions[id]
	return ok && fn.MethodFrom != nil && !fn.IsAssociated
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

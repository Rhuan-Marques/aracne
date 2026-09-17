package javascanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	java "github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// javaCtx is the resolved project model for a root: external dependency roots and
// coordinate map (from pom/gradle), the whole-graph declared-type index, the
// per-file package map, and the overload-aware method index.
type javaCtx struct {
	Root          string
	externalRoots map[string]bool
	depCoord      map[string]string // dep group prefix -> "group:artifact"
	declared      map[string]string // type FQN -> abs file
	packageOfFile map[string]string
	packages      map[string]bool     // every package that declares at least one type
	byTail        map[string][]string // typeTail -> declared type FQNs (last-resort lookup)
	typeByFQN     map[string]bool
	methodIndex   map[string]map[string][]string // owner -> simple name -> []method ID
}

// buildJavaModel seeds the external roots (java/javax/jakarta) and scans pom.xml /
// build.gradle for declared dependency coordinates.
func buildJavaModel(absRoot string) *javaCtx {
	ctx := &javaCtx{
		Root:          absRoot,
		externalRoots: map[string]bool{"java": true, "javax": true, "jakarta": true},
		depCoord:      map[string]string{},
		declared:      map[string]string{},
		packageOfFile: map[string]string{},
		packages:      map[string]bool{},
		byTail:        map[string][]string{},
		typeByFQN:     map[string]bool{},
		methodIndex:   map[string]map[string][]string{},
	}
	parseBuildDeps(absRoot, ctx)
	return ctx
}

// parseBuildDeps hand-scans pom.xml and gradle build files under root for
// dependency group/artifact coordinates (no XML/Groovy library).
func parseBuildDeps(root string, ctx *javaCtx) {
	pom := filepath.Join(root, "pom.xml")
	if data, err := os.ReadFile(pom); err == nil {
		parsePomDeps(string(data), ctx)
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			parseGradleDeps(string(data), ctx)
		}
	}
}

// parsePomDeps extracts the <groupId>/<artifactId> pair of every <dependency> in a
// pom.xml body. The project's own coordinates, its <parent>, plugins and a
// dependency's <exclusion>s also carry such pairs but are not dependencies: the
// project's own group, taken as one, turned every unresolved reference under the
// project's packages into a dependency on the project itself.
func parsePomDeps(data string, ctx *javaCtx) {
	group, artifact := "", ""
	inDep, inExcl := false, false
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "<dependency>") {
			inDep, group, artifact = true, "", ""
		}
		if strings.Contains(line, "<exclusion>") {
			inExcl = true
		}
		if inDep && !inExcl {
			if g, ok := xmlTag(line, "groupId"); ok {
				group = g
			}
			if a, ok := xmlTag(line, "artifactId"); ok {
				artifact = a
			}
		}
		if strings.Contains(line, "</exclusion>") {
			inExcl = false
		}
		if strings.Contains(line, "</dependency>") {
			if group != "" && artifact != "" {
				ctx.depCoord[group] = group + ":" + artifact
				ctx.externalRoots[firstSeg(group)] = true
			}
			inDep = false
		}
	}
}

// parseGradleDeps extracts "group:artifact:version" coordinates from gradle files.
func parseGradleDeps(data string, ctx *javaCtx) {
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		for _, q := range []byte{'\'', '"'} {
			if c := between(line, q); c != "" && strings.Count(c, ":") >= 1 {
				parts := strings.Split(c, ":")
				if len(parts) >= 2 {
					group := parts[0]
					ctx.depCoord[group] = group + ":" + parts[1]
					ctx.externalRoots[firstSeg(group)] = true
				}
			}
		}
	}
}

// xmlTag extracts the inner text of a one-line <tag>value</tag>.
func xmlTag(line, tag string) (string, bool) {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(line, open)
	j := strings.Index(line, close)
	if i >= 0 && j > i {
		return strings.TrimSpace(line[i+len(open) : j]), true
	}
	return "", false
}

// between returns the substring between the first two occurrences of q.
func between(s string, q byte) string {
	i := strings.IndexByte(s, q)
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], q)
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

// firstSeg returns the first dotted segment of a string.
func firstSeg(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}

// javaBuildFiles are the files that mark a directory as a Maven/Gradle module
// root -- the only place besides the project root where target/build/out/bin is
// build output rather than a package directory.
var javaBuildFiles = []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}

// isBuildModuleDir reports whether dir is the project root or a build module
// root. Only stat'd for the handful of directories whose basename collides with
// a build-output name, so the walk stays cheap.
func isBuildModuleDir(dir, root string) bool {
	if dir == root {
		return true
	}
	for _, n := range javaBuildFiles {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// collectJavaFiles walks a project root collecting .java files, skipping build
// output, test directories, and *Test/*Tests/*IT test files.
func collectJavaFiles(root string) []string {
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
			case ".gradle", "node_modules", ".git":
				return filepath.SkipDir
			case "target", "build", "out", "bin":
				// Build output only where a build actually writes it: directly under
				// the project root or under a module root. Deeper down these names are
				// ordinary package segments -- a hexagonal `...port.out`, a
				// `com.google.devtools.build...` -- and pruning them by basename alone
				// deleted whole packages from the graph with no error, after which the
				// project's own package prefix surfaced as an external dependency.
				if isBuildModuleDir(filepath.Dir(path), root) {
					return filepath.SkipDir
				}
			case "test", "tests":
				// `src/test`(/java) is the Maven/Gradle test root and a root-level
				// `tests/` its hand-rolled equivalent; anywhere else `test` is a
				// package name (`com.acme.test.support`).
				if parent := filepath.Dir(path); parent == root || filepath.Base(parent) == "src" {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".java") {
			return nil
		}
		if strings.HasSuffix(name, "Test.java") || strings.HasSuffix(name, "Tests.java") || strings.HasSuffix(name, "IT.java") {
			return nil
		}
		if domain.PathHidden(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sortByPortablePath(files)
	return files
}

// indexDeclarations (re)builds the whole-graph declared-type index, per-file
// package map, and type set from the topology (so cross-file and incremental
// resolution see every type in the tree).
func (ctx *javaCtx) indexDeclarations(gt *java.JavaTopology) {
	ctx.declared = map[string]string{}
	ctx.packageOfFile = map[string]string{}
	ctx.packages = map[string]bool{}
	ctx.byTail = map[string][]string{}
	ctx.typeByFQN = map[string]bool{}
	owners := map[string][]string{}
	for id, mod := range gt.Modules {
		ctx.packageOfFile[id] = mod.Package
		ctx.packages[mod.Package] = true
		for _, sid := range mod.Structs() {
			owners[sid] = append(owners[sid], id)
		}
		for _, iid := range mod.Interfaces() {
			owners[iid] = append(owners[iid], id)
		}
	}
	for fqn, files := range owners {
		ctx.declared[fqn] = declaringFile(gt, fqn, files)
		ctx.typeByFQN[fqn] = true
	}
	for fqn := range ctx.declared {
		tail := typeTail(fqn)
		ctx.byTail[tail] = append(ctx.byTail[tail], fqn)
	}
}

// declaringFile picks the file a type declared by several files is attributed to,
// not whichever module the map yields last: the file holding the copy the graph
// keeps (the last in path order, see coOwners), unless that file is gone -- an
// incremental scan resolves importers before it removes a deleted file, and the
// surviving copy is the one they must import -- and then the last surviving one.
func declaringFile(gt *java.JavaTopology, fqn string, files []string) string {
	if len(files) == 1 {
		return files[0]
	}
	kept := ""
	if c, ok := gt.Classes[fqn]; ok {
		kept = c.Loc.Path
	} else if t, ok := gt.Interfaces[fqn]; ok {
		kept = t.Loc.Path
	}
	sortByPortablePath(files)
	var live []string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			live = append(live, f)
		}
	}
	if len(live) == 0 {
		live = files
	}
	for _, f := range live {
		if f == kept {
			return f
		}
	}
	return live[len(live)-1]
}

// typeFQN resolves a raw type name to an internal type FQN, or "" for an
// external/unresolved type. scope is the type the reference occurs in (its
// member types and local classes are visible), or "" when there is none.
//
// A dotted name resolves head-first: `Map.Entry` resolves `Map` and then looks up
// its member type, so the qualifier decides the owner rather than the last
// segment. A dotted name whose head is not a type is a fully qualified name,
// and every internal FQN is in ctx.declared, so a miss there is external.
func (ctx *javaCtx) typeFQN(rawName string, pr *ParseResult, scope string) string {
	qualified := stripTypeText(rawName)
	if qualified == "" {
		return ""
	}
	if _, ok := ctx.declared[qualified]; ok {
		return qualified
	}
	head, rest, dotted := strings.Cut(qualified, ".")
	owner := ctx.simpleTypeFQN(head, pr, scope)
	if !dotted {
		return owner
	}
	if owner == "" {
		return ""
	}
	if cand := owner + "." + rest; ctx.declared[cand] != "" {
		return cand
	}
	return ""
}

// simpleTypeFQN resolves an unqualified type name in Java's order: the enclosing
// types (innermost first, with their member and local classes), single-type
// imports, the same package, on-demand imports, then implicit java.lang. A name
// any of those binds to an external type resolves to "" — never to an internal
// type that merely shares the simple name. Only a name nothing binds falls back
// to the by-name index, and only when exactly one internal type has that name.
func (ctx *javaCtx) simpleTypeFQN(n string, pr *ParseResult, scope string) string {
	for s := scope; ctx.declared[s] != ""; s = enclosingType(s) {
		if typeTail(s) == n {
			return s
		}
		if cand := s + "." + n; ctx.declared[cand] != "" {
			return cand
		}
		if cand := s + "$" + n; ctx.declared[cand] != "" {
			return cand
		}
	}
	if imp, ok := pr.ImportMap[n]; ok {
		if imp.Internal && ctx.declared[imp.FQN] != "" {
			return imp.FQN
		}
		return ""
	}
	if cand := qualify(pr.Package, n); ctx.declared[cand] != "" {
		return cand
	}
	found, externalWildcard := "", false
	for _, wp := range pr.Wildcards {
		if cand := wp + "." + n; ctx.declared[cand] != "" {
			if found != "" && found != cand {
				return "" // ambiguous between two on-demand imports
			}
			found = cand
		} else if !ctx.packages[wp] && ctx.declared[wp] == "" {
			externalWildcard = true
		}
	}
	if found != "" {
		return found
	}
	// An external on-demand import (java.util.*) or java.lang may be what binds
	// the name; its contents are unknown, so there is nothing safe to fall back to.
	if javaLangTypes[n] || externalWildcard {
		return ""
	}
	if cands := ctx.byTail[n]; len(cands) == 1 {
		return cands[0]
	}
	return ""
}

// enclosingType drops the final '.'- or '$'-separated segment of a type FQN (the
// declaring type of a member, local or anonymous class; a package for a
// top-level type).
func enclosingType(fqn string) string {
	if i := strings.LastIndexAny(fqn, ".$"); i >= 0 {
		return fqn[:i]
	}
	return ""
}

// holderScope returns the type a holder's references are resolved in: a
// method's declaring type, or the holder type itself.
func holderScope(gt *java.JavaTopology, holderID string) string {
	if m, ok := gt.Methods[holderID]; ok && m.MethodFrom != nil {
		return *m.MethodFrom
	}
	return holderID
}

// javaLangTypes are the public top-level types of java.lang, which every
// compilation unit imports on demand.
var javaLangTypes = map[string]bool{
	"Appendable": true, "AutoCloseable": true, "Boolean": true, "Byte": true,
	"CharSequence": true, "Character": true, "Class": true, "ClassLoader": true,
	"ClassValue": true, "Cloneable": true, "Comparable": true, "Deprecated": true,
	"Double": true, "Enum": true, "Float": true, "FunctionalInterface": true,
	"InheritableThreadLocal": true, "Integer": true, "Iterable": true, "Long": true,
	"Math": true, "Module": true, "ModuleLayer": true, "Number": true, "Object": true,
	"Override": true, "Package": true, "Process": true, "ProcessBuilder": true,
	"ProcessHandle": true, "Readable": true, "Record": true, "Runnable": true,
	"Runtime": true, "RuntimePermission": true, "SafeVarargs": true,
	"SecurityManager": true, "Short": true, "StackTraceElement": true,
	"StackWalker": true, "StrictMath": true, "String": true, "StringBuffer": true,
	"StringBuilder": true, "SuppressWarnings": true, "System": true, "Thread": true,
	"ThreadGroup": true, "ThreadLocal": true, "Throwable": true, "Void": true,

	"ArithmeticException": true, "ArrayIndexOutOfBoundsException": true,
	"ArrayStoreException": true, "ClassCastException": true,
	"ClassNotFoundException": true, "CloneNotSupportedException": true,
	"EnumConstantNotPresentException": true, "Exception": true,
	"IllegalAccessException": true, "IllegalArgumentException": true,
	"IllegalCallerException": true, "IllegalMonitorStateException": true,
	"IllegalStateException": true, "IllegalThreadStateException": true,
	"IndexOutOfBoundsException": true, "InstantiationException": true,
	"InterruptedException": true, "LayerInstantiationException": true,
	"MatchException": true, "NegativeArraySizeException": true,
	"NoSuchFieldException": true, "NoSuchMethodException": true,
	"NullPointerException": true, "NumberFormatException": true,
	"ReflectiveOperationException": true, "RuntimeException": true,
	"SecurityException": true, "StringIndexOutOfBoundsException": true,
	"TypeNotPresentException": true, "UnsupportedOperationException": true,
	"WrongThreadException": true,

	"AbstractMethodError": true, "AssertionError": true, "BootstrapMethodError": true,
	"ClassCircularityError": true, "ClassFormatError": true, "Error": true,
	"ExceptionInInitializerError": true, "IllegalAccessError": true,
	"IncompatibleClassChangeError": true, "InstantiationError": true,
	"InternalError": true, "LinkageError": true, "NoClassDefFoundError": true,
	"NoSuchFieldError": true, "NoSuchMethodError": true, "OutOfMemoryError": true,
	"StackOverflowError": true, "ThreadDeath": true, "UnknownError": true,
	"UnsatisfiedLinkError": true, "UnsupportedClassVersionError": true,
	"VerifyError": true, "VirtualMachineError": true,
}

// depFor maps an external type/package FQN to a stable dependency coordinate,
// preferring a pom/gradle group match, then a JDK root, then a package root.
func (ctx *javaCtx) depFor(fqn string) string {
	// Pick the most specific (longest) matching group, tie-broken
	// lexicographically, so the result never depends on map iteration order
	// when two declared groups are dotted-prefixes of each other (e.g.
	// "org.springframework" and "org.springframework.boot").
	bestGroup, bestCoord := "", ""
	for group, coord := range ctx.depCoord {
		if fqn != group && !strings.HasPrefix(fqn, group+".") {
			continue
		}
		if bestGroup == "" || len(group) > len(bestGroup) ||
			(len(group) == len(bestGroup) && group < bestGroup) {
			bestGroup, bestCoord = group, coord
		}
	}
	if bestGroup != "" {
		return bestCoord
	}
	segs := strings.Split(fqn, ".")
	if len(segs) == 0 {
		return ""
	}
	switch segs[0] {
	case "java", "javax", "jakarta":
		if len(segs) >= 2 {
			return segs[0] + "." + segs[1]
		}
		return segs[0]
	}
	n := 3
	if len(segs) < n {
		n = len(segs)
	}
	return strings.Join(segs[:n], ".")
}

// resolveImports resolves a file's imports into module import edges, fills its
// import map / wildcards / static members, resolves its hierarchy records onto
// the module, and applies type-use / annotation-use edges.
func resolveImports(pr *ParseResult, ctx *javaCtx, gt *java.JavaTopology) {
	imap := map[string]javaImport{}
	var wildcards, staticWildcards []string
	static := map[string]string{}
	modFiles := map[string]bool{}
	deps := map[string]bool{}

	for _, imp := range pr.Imports {
		switch {
		case imp.Static && imp.Wildcard:
			// `import static a.Util.*` names a TYPE, not a package: it imports Util's
			// static members, and its member types, which is why it also joins the
			// on-demand list type names are resolved through.
			wildcards = append(wildcards, imp.FQN)
			if file, ok := ctx.declared[imp.FQN]; ok {
				if file != pr.FileID {
					modFiles[file] = true
				}
				staticWildcards = append(staticWildcards, imp.FQN)
			} else if d := ctx.depFor(imp.FQN); d != "" {
				deps[d] = true
			}
		case imp.Wildcard:
			wildcards = append(wildcards, imp.FQN)
			matched := false
			for typeFQN, file := range ctx.declared {
				if ctx.packageOfFile[file] == imp.FQN && file != pr.FileID {
					modFiles[file] = true
					matched = true
				}
				_ = typeFQN
			}
			if !matched {
				if d := ctx.depFor(imp.FQN); d != "" {
					deps[d] = true
				}
			}
		case imp.Static:
			// Recorded even when the owner is external: a single-static import
			// shadows every static import on demand, so an external one must still
			// stop the name from resolving through an internal `import static T.*`.
			static[imp.Member] = imp.FQN
			if file, ok := ctx.declared[imp.FQN]; ok {
				if file != pr.FileID {
					modFiles[file] = true
				}
			} else if d := ctx.depFor(imp.FQN); d != "" {
				deps[d] = true
			}
		default:
			if file, ok := ctx.declared[imp.FQN]; ok {
				if file != pr.FileID {
					modFiles[file] = true
				}
				imap[imp.LocalName] = javaImport{FQN: imp.FQN, Internal: true, LocalName: imp.LocalName}
			} else {
				d := ctx.depFor(imp.FQN)
				if d != "" {
					deps[d] = true
				}
				imap[imp.LocalName] = javaImport{FQN: imp.FQN, Internal: false, Dep: d, LocalName: imp.LocalName}
			}
		}
	}

	pr.ImportMap = imap
	pr.Wildcards = wildcards
	pr.StaticMembers = static
	pr.StaticWildcards = staticWildcards

	mod := gt.Modules[pr.FileID]
	if mod.Connections == nil {
		mod.Connections = map[java.ConnectionKind][]string{}
	}
	delete(mod.Connections, java.ConnImportsModule)
	delete(mod.Connections, java.ConnImportsDep)
	delete(mod.Connections, connExtendsRecords)
	delete(mod.Connections, connImplRecords)
	for f := range modFiles {
		mod.Connections[java.ConnImportsModule] = append(mod.Connections[java.ConnImportsModule], f)
	}
	for d := range deps {
		mod.Connections[java.ConnImportsDep] = append(mod.Connections[java.ConnImportsDep], d)
	}

	// Resolve deferred hierarchy records to parent FQNs and persist them on the
	// module (whole-graph rebuild reads these every scan).
	for _, hr := range pr.HierRecords {
		// A supertype clause is outside the child's body: resolve it in the
		// declaring scope, where the child's own member types are not visible.
		parent := ctx.typeFQN(hr.ParentName, pr, enclosingType(hr.ChildID))
		if parent == "" {
			continue
		}
		switch hr.Kind {
		case hkExtendsClass, hkEnumConst:
			mod.Connections[connExtendsRecords] = append(mod.Connections[connExtendsRecords], hr.ChildID+recSep+parent+":class")
		case hkExtendsIface:
			mod.Connections[connExtendsRecords] = append(mod.Connections[connExtendsRecords], hr.ChildID+recSep+parent+":iface")
		case hkImplements:
			mod.Connections[connImplRecords] = append(mod.Connections[connImplRecords], hr.ChildID+recSep+parent)
		case hkAnonSuper:
			if _, ok := gt.Interfaces[parent]; ok {
				mod.Connections[connImplRecords] = append(mod.Connections[connImplRecords], hr.ChildID+recSep+parent)
			} else if _, ok := gt.Classes[parent]; ok {
				mod.Connections[connExtendsRecords] = append(mod.Connections[connExtendsRecords], hr.ChildID+recSep+parent+":class")
			}
		}
	}
	mod.Connections = uniqueConns(mod.Connections)
	gt.Modules[pr.FileID] = mod

	// Type-use edges (field types, generic bounds, record components).
	for _, tu := range pr.TypeUses {
		if id := ctx.typeFQN(tu.TypeName, pr, holderScope(gt, tu.HolderID)); id != "" {
			addUsesType(gt, tu.HolderID, id)
		}
	}
	// Annotation-use edges (always uses_interface to an internal annotation type).
	for _, au := range pr.AnnoUses {
		if id := ctx.typeFQN(au.TypeName, pr, holderScope(gt, au.HolderID)); id != "" {
			if _, ok := gt.Interfaces[id]; ok {
				addConn(gt, au.HolderID, java.ConnUsesTrait, id)
			}
		}
	}
}

// addUsesType adds a uses_struct or uses_interface edge from a holder to a
// resolved type, choosing the edge kind by the target's kind.
func addUsesType(gt *java.JavaTopology, holderID, targetID string) {
	if _, ok := gt.Interfaces[targetID]; ok {
		addConn(gt, holderID, java.ConnUsesTrait, targetID)
		return
	}
	if _, ok := gt.Classes[targetID]; ok {
		addConn(gt, holderID, java.ConnUsesStruct, targetID)
	}
}

// addConn appends a connection to a holder (class, interface, or method),
// de-duplicating, and writes it back.
func addConn(gt *java.JavaTopology, holderID string, kind java.ConnectionKind, target string) {
	if holderID == target {
		return
	}
	if c, ok := gt.Classes[holderID]; ok {
		c.Connections = ensureAppend(c.Connections, kind, target)
		gt.Classes[holderID] = c
		return
	}
	if i, ok := gt.Interfaces[holderID]; ok {
		i.Connections = ensureAppend(i.Connections, kind, target)
		gt.Interfaces[holderID] = i
		return
	}
	if m, ok := gt.Methods[holderID]; ok {
		m.Connections = ensureAppend(m.Connections, kind, target)
		gt.Methods[holderID] = m
	}
}

// ensureAppend appends target to conns[kind] unless already present.
func ensureAppend(conns map[java.ConnectionKind][]string, kind java.ConnectionKind, target string) map[java.ConnectionKind][]string {
	if conns == nil {
		conns = map[java.ConnectionKind][]string{}
	}
	for _, e := range conns[kind] {
		if e == target {
			return conns
		}
	}
	conns[kind] = append(conns[kind], target)
	return conns
}

// resolveMethodTypingIDs resolves each result method's I/O type names to internal
// FQNs (in the method's own file context), so callers can follow return types.
func resolveMethodTypingIDs(gt *java.JavaTopology, results []*ParseResult, ctx *javaCtx) {
	for _, pr := range results {
		for _, mp := range pr.Methods {
			m, ok := gt.Methods[mp.Method.ID]
			if !ok {
				continue
			}
			changed := false
			scope := holderScope(gt, mp.Method.ID)
			for i := range m.Output {
				if id := ctx.typeFQN(m.Output[i].Typing, pr, scope); id != "" && m.Output[i].TypingID != id {
					m.Output[i].TypingID = id
					changed = true
				}
			}
			for i := range m.Input {
				if id := ctx.typeFQN(m.Input[i].Typing, pr, scope); id != "" && m.Input[i].TypingID != id {
					m.Input[i].TypingID = id
					changed = true
				}
			}
			if changed {
				gt.Methods[mp.Method.ID] = m
			}
		}
	}
}

// analyzeBodies resolves each method's body record into calls/uses edges.
func analyzeBodies(gt *java.JavaTopology, pr *ParseResult, ctx *javaCtx) {
	for _, mp := range pr.Methods {
		if mp.Body == nil {
			continue
		}
		m, ok := gt.Methods[mp.Method.ID]
		if !ok || m.MethodFrom == nil {
			continue
		}
		conns := analyzeFunctionBody(gt, ctx, pr, mp.Body, *m.MethodFrom, m.Input)
		if m.Connections == nil {
			m.Connections = map[java.ConnectionKind][]string{}
		}
		for k, v := range conns {
			for _, id := range v {
				m.Connections = ensureAppend(m.Connections, k, id)
			}
		}
		gt.Methods[mp.Method.ID] = m
	}
}

// analyzeFunctionBody resolves a body's lets/news/calls into edges, using the
// owner's fields, the method's parameters and the owner's inheritance for
// receiver typing and the overload-aware method index for call targets.
func analyzeFunctionBody(gt *java.JavaTopology, ctx *javaCtx, pr *ParseResult, body *javaBody, ownerSID string, params []java.VariableDefinition) map[java.ConnectionKind][]string {
	conn := map[java.ConnectionKind][]string{}
	add := func(kind java.ConnectionKind, id string) {
		if id == "" {
			return
		}
		for _, e := range conn[kind] {
			if e == id {
				return
			}
		}
		conn[kind] = append(conn[kind], id)
	}
	var callSites []string
	// addCalls records the edges for one call site along with what it passes.
	//
	// matchMethods returns EVERY same-named overload when none has the right arity, so
	// those edges are a guess about which method is meant. Since the fallback fires
	// precisely when no arity fits, checking arity against them would report a mismatch on
	// all of them; the Dyn flag marks the guess so the verdict is downgraded to Unknown.
	addCalls := func(ids []string, argCount int, argTypes []string) {
		exact := javaArityExact(ids, argCount)
		for _, mid := range ids {
			add(java.ConnCalls, mid)
			rec := contract.EncodeCallSite(javaCallSite(mid, argCount, argTypes, !exact))
			if rec != "" && !containsJavaRec(callSites, rec) {
				callSites = append(callSites, rec)
			}
		}
	}
	addUses := func(fqn string) {
		if fqn == "" {
			return
		}
		if _, ok := gt.Interfaces[fqn]; ok {
			add(java.ConnUsesTrait, fqn)
			return
		}
		if _, ok := gt.Classes[fqn]; ok {
			add(java.ConnUsesStruct, fqn)
		}
	}

	// Seed variable types from the owner's fields (and record components).
	varType := map[string]string{}
	if oc, ok := gt.Classes[ownerSID]; ok {
		for _, f := range oc.Fields {
			if id := ctx.typeFQN(f.Typing, pr, ownerSID); id != "" {
				varType[f.Name] = id
			}
		}
		for _, c := range oc.Components {
			if id := ctx.typeFQN(c.Typing, pr, ownerSID); id != "" {
				varType[c.Name] = id
			}
		}
	}
	fieldType := map[string]string{}
	for k, v := range varType {
		fieldType[k] = v
	}
	// A parameter shadows a field of the same name (`this.x` still reads fieldType);
	// one of an external type is recorded as "", known but not callable into.
	for _, p := range params {
		if p.Name != "" {
			varType[p.Name] = ctx.typeFQN(p.Typing, pr, ownerSID)
		}
	}

	parentSID := superclassOf(gt, ownerSID)

	for _, l := range body.Lets {
		if l.DeclType != "" {
			if id := ctx.typeFQN(l.DeclType, pr, ownerSID); id != "" {
				varType[l.Name] = id
				if !l.Param {
					addUses(id)
				}
				continue
			}
		}
		if l.Cast != "" {
			if id := ctx.typeFQN(l.Cast, pr, ownerSID); id != "" {
				varType[l.Name] = id
				continue
			}
		}
		if l.NewType != "" {
			if id := ctx.typeFQN(l.NewType, pr, ownerSID); id != "" {
				varType[l.Name] = id
				continue
			}
		}
		if l.CallMeth != "" {
			ownerOfCall := ""
			if t, ok := varType[l.CallObj]; ok {
				ownerOfCall = t
			} else if t := ctx.typeFQN(l.CallObj, pr, ownerSID); t != "" {
				ownerOfCall = t
			}
			if rid := returnTypeID(gt, ctx, ownerOfCall, l.CallMeth, l.CallArgs); rid != "" {
				varType[l.Name] = rid
			}
		}
	}

	for _, n := range body.News {
		fqn := ctx.typeFQN(n.Type, pr, ownerSID)
		if fqn != "" {
			addUses(fqn)
			addCalls(matchMethods(ctx, fqn, "<init>", n.ArgCount), n.ArgCount, nil)
		} else if imp, ok := pr.ImportMap[stripTypeText(n.Type)]; ok && !imp.Internal && imp.Dep != "" {
			add(java.ConnUsesDep, imp.Dep)
		}
	}

	for _, c := range body.Calls {
		if c.Implicit {
			addCalls(resolveImplicitCall(gt, ctx, pr, ownerSID, c.Method, c.ArgCount), c.ArgCount, nil)
			continue
		}
		sid := ""
		switch {
		case c.IsSelf:
			sid = ownerSID
		case c.IsSuper:
			sid = parentSID
		case c.CastType != "":
			sid = ctx.typeFQN(c.CastType, pr, ownerSID)
			addUses(sid)
		case c.FieldRecv != "":
			sid = fieldType[c.FieldRecv]
		case c.Object != "":
			if t, ok := varType[c.Object]; ok {
				sid = t
			} else if t, ok := fieldType[c.Object]; ok {
				sid = t
			} else if t := ctx.typeFQN(c.Object, pr, ownerSID); t != "" {
				sid = t
				addUses(t)
			}
		}
		if sid != "" {
			ids := matchInHierarchy(gt, ctx, sid, c.Method, c.ArgCount)
			addCalls(ids, c.ArgCount, nil)
			if len(ids) == 0 && closedHierarchy(gt, sid) && !objectMethods[c.Method] {
				// `new A().fun2()` where A and everything it extends are project classes
				// and none of them declares fun2. See domain.MissingRefsConn.
				add(java.ConnectionKind(domain.MissingRefsConn), sid+"."+c.Method+"(")
			}
		} else if c.Object != "" {
			// An unresolved receiver says nothing about which method is meant; only an
			// external type it names contributes its dependency.
			if imp, ok := pr.ImportMap[c.Object]; ok && !imp.Internal && imp.Dep != "" {
				add(java.ConnUsesDep, imp.Dep)
			}
		}
	}

	// Sorted: the at-scale suite compares connection sets across scan modes byte-for-byte.
	if len(callSites) > 0 {
		sort.Strings(callSites)
		conn[java.ConnectionKind(contract.CallSitesConn)] = callSites
	}

	return conn
}

// matchMethods returns the method IDs for owner.name filtered by arity (emitting
// ALL matching-arity overloads); falls back to all overloads when arity is
// unknown (-1) or no arity matches.
func matchMethods(ctx *javaCtx, owner, name string, argCount int) []string {
	byName := ctx.methodIndex[owner]
	if byName == nil {
		return nil
	}
	cands := byName[name]
	if len(cands) == 0 {
		return nil
	}
	if argCount < 0 {
		return cands
	}
	var matched []string
	for _, id := range cands {
		if methodArity(id) == argCount {
			matched = append(matched, id)
		}
	}
	if len(matched) == 0 {
		return cands
	}
	return matched
}

// matchInHierarchy resolves owner.name against owner and every internal type it
// inherits from, nearest first (see hierarchyOrder). The nearest type declaring an
// overload of the right arity wins, so an override shadows the method it overrides;
// when no type has that arity, it falls back to every overload of the nearest type
// declaring the name, as matchMethods does. Constructors are not inherited and
// resolve against owner alone.
func matchInHierarchy(gt *java.JavaTopology, ctx *javaCtx, owner, name string, argCount int) []string {
	if name == "<init>" {
		return matchMethods(ctx, owner, name, argCount)
	}
	var fallback []string
	for _, t := range hierarchyOrder(gt, owner) {
		ids := matchMethods(ctx, t, name, argCount)
		if len(ids) == 0 {
			continue
		}
		if javaArityExact(ids, argCount) {
			return ids
		}
		if fallback == nil {
			fallback = ids
		}
	}
	return fallback
}

// hierarchyOrder lists start, its superclass chain, then the interfaces those
// classes implement and their superinterfaces (breadth-first, each level sorted so
// the order does not depend on how the edges were stored). A class method always
// precedes an interface default, as in Java. Only internal types are known; the
// chain ends at the first external supertype.
func hierarchyOrder(gt *java.JavaTopology, start string) []string {
	seen := map[string]bool{}
	var order, ifaces []string
	for t := start; t != "" && !seen[t]; t = superclassOf(gt, t) {
		seen[t] = true
		order = append(order, t)
		if c, ok := gt.Classes[t]; ok {
			ifaces = append(ifaces, sortedCopy(c.Implements())...)
		} else if i, ok := gt.Interfaces[t]; ok {
			ifaces = append(ifaces, sortedCopy(i.Inherits())...)
		}
	}
	for n := 0; n < len(ifaces); n++ {
		t := ifaces[n]
		if seen[t] {
			continue
		}
		seen[t] = true
		order = append(order, t)
		if i, ok := gt.Interfaces[t]; ok {
			ifaces = append(ifaces, sortedCopy(i.Inherits())...)
		}
	}
	return order
}

// superclassOf returns a class's internal superclass, or "" (an interface, a class
// extending nothing internal, or an unknown ID).
func superclassOf(gt *java.JavaTopology, id string) string {
	if c, ok := gt.Classes[id]; ok {
		if inh := sortedCopy(c.Inherits()); len(inh) > 0 {
			return inh[0]
		}
	}
	return ""
}

func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// resolveImplicitCall binds a call written with no receiver (`helper(1)`) in
// Java's order: the innermost lexically enclosing type whose members -- its own or
// inherited -- include a method of that name decides; only when none does, a
// single-static import of the name, and then the static imports on demand.
func resolveImplicitCall(gt *java.JavaTopology, ctx *javaCtx, pr *ParseResult, ownerSID, name string, argCount int) []string {
	for t := ownerSID; isJavaType(gt, t); t = lexicalParent(pr, t) {
		if ids := matchInHierarchy(gt, ctx, t, name, argCount); len(ids) > 0 {
			return ids
		}
	}
	if owner, ok := pr.StaticMembers[name]; ok {
		// An external owner resolves to nothing, and still shadows the on-demand imports.
		return matchInHierarchy(gt, ctx, owner, name, argCount)
	}
	var found [][]string
	for _, owner := range pr.StaticWildcards {
		if ids := matchInHierarchy(gt, ctx, owner, name, argCount); len(ids) > 0 {
			found = append(found, ids)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	// Several on-demand imports offer the name: keep only the overloads that fit.
	var exact []string
	for _, ids := range found {
		if javaArityExact(ids, argCount) {
			exact = append(exact, ids...)
		}
	}
	return exact
}

// isJavaType reports whether id is an internal class or interface.
func isJavaType(gt *java.JavaTopology, id string) bool {
	if _, ok := gt.Classes[id]; ok {
		return true
	}
	_, ok := gt.Interfaces[id]
	return ok
}

// lexicalParent returns the type whose code encloses t: recorded for an anonymous
// class, and otherwise its ID minus the last segment (a member or local class).
func lexicalParent(pr *ParseResult, t string) string {
	if p, ok := pr.AnonEnclosing[t]; ok {
		return p
	}
	return enclosingType(t)
}

// methodArity counts the parameters encoded in a method ID's "(...)" signature.
func methodArity(id string) int {
	i := strings.LastIndex(id, "(")
	j := strings.LastIndex(id, ")")
	if i < 0 || j <= i+1 {
		return 0
	}
	inner := id[i+1 : j]
	if strings.TrimSpace(inner) == "" {
		return 0
	}
	return strings.Count(inner, ",") + 1
}

// returnTypeID resolves owner.name (by arity, inherited methods included) to its
// first output's TypingID.
func returnTypeID(gt *java.JavaTopology, ctx *javaCtx, owner, name string, argCount int) string {
	for _, mid := range matchInHierarchy(gt, ctx, owner, name, argCount) {
		if m, ok := gt.Methods[mid]; ok && len(m.Output) > 0 && m.Output[0].TypingID != "" {
			return m.Output[0].TypingID
		}
	}
	return ""
}

// collectDependencies rebuilds the topology dependency list from modules' imported
// dependencies, deduped.
func collectDependencies(gt *java.JavaTopology) {
	seen := map[string]bool{}
	gt.Dependencies = nil
	var coords []string
	for _, mod := range gt.Modules {
		for _, dep := range mod.Connections[java.ConnImportsDep] {
			if !seen[dep] {
				seen[dep] = true
				coords = append(coords, dep)
			}
		}
	}
	sort.Strings(coords)
	for _, dep := range coords {
		gt.Dependencies = append(gt.Dependencies, java.JavaDependency{Coordinate: java.DependencyPath(dep)})
	}
}

// dedupAll de-duplicates every Connections slice on every resource (a duplicate
// edge aborts the file's DB write).
func dedupAll(gt *java.JavaTopology) {
	for id, c := range gt.Classes {
		c.Connections = uniqueConns(c.Connections)
		gt.Classes[id] = c
	}
	for id, t := range gt.Interfaces {
		t.Connections = uniqueConns(t.Connections)
		gt.Interfaces[id] = t
	}
	for id, m := range gt.Methods {
		m.Connections = uniqueConns(m.Connections)
		gt.Methods[id] = m
	}
	for id, mod := range gt.Modules {
		mod.Connections = uniqueConns(mod.Connections)
		gt.Modules[id] = mod
	}
}

// uniqueConns dedups each connection list, dropping empty kinds.
func uniqueConns(conns map[java.ConnectionKind][]string) map[java.ConnectionKind][]string {
	result := make(map[java.ConnectionKind][]string, len(conns))
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

// javaCallSite reads the argument shape of one Java call.
func javaCallSite(calleeID string, argCount int, argTypes []string, dyn bool) contract.CallSite {
	site := contract.CallSite{CalleeID: calleeID, N: argCount, Dyn: dyn}
	anyKnown := false
	types := make([]*string, 0, len(argTypes))
	for _, tok := range argTypes {
		if tok == "" {
			types = append(types, nil)
			continue
		}
		anyKnown = true
		t := tok
		types = append(types, &t)
	}
	if anyKnown && len(argTypes) == argCount {
		site.Types = types
	}
	return site
}

// javaArityExact reports whether matchMethods actually found an overload of the wanted
// arity, rather than falling back to returning every same-named one.
func javaArityExact(ids []string, argCount int) bool {
	if argCount < 0 {
		return false
	}
	for _, id := range ids {
		if methodArity(id) == argCount {
			return true
		}
	}
	return false
}

func containsJavaRec(recs []string, rec string) bool {
	for _, r := range recs {
		if r == rec {
			return true
		}
	}
	return false
}

// objectMethods are the methods every Java class has from java.lang.Object, which no project type
// declares and every receiver can call.
var objectMethods = map[string]bool{
	"toString": true, "equals": true, "hashCode": true, "getClass": true, "clone": true,
	"finalize": true, "notify": true, "notifyAll": true, "wait": true,
}

// closedHierarchy reports whether every method a receiver of type sid can have is declared in this
// project: sid is a plain class, and it and every class it extends name only supertypes that
// resolved to project classes. Anything else -- an interface, an enum or record (compiler-generated
// members), a supertype from the JDK or a dependency, an interface in the chain whose own
// supertypes this model does not keep -- may supply a method this scan cannot see, so a call that
// finds nothing is not evidence of a missing one.
func closedHierarchy(gt *java.JavaTopology, sid string) bool {
	for t := sid; t != ""; t = superclassOf(gt, t) {
		c, ok := gt.Classes[t]
		if !ok || c.IsEnum || c.IsRecord || c.IsAnonymous {
			return false
		}
		if len(c.Bases) > len(c.Inherits()) {
			return false
		}
		if len(c.Interfaces) > 0 {
			return false
		}
	}
	return true
}

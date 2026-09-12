// Package javascanner parses a Java source tree with tree-sitter and builds a
// generic topology graph via the internal/topology/java model. It mirrors the
// rustscanner package structure (manual cursor walks, a parse/resolve/match
// split, and a snapshot+diff incremental UpdateFile) with the Java adaptations:
// FQN-based IDs rooted at the in-file package declaration, signature-bearing
// method IDs, whole-graph structural rebuild from module-owned hierarchy records,
// and overload-aware call resolution. There is no PartialUpdater.
package javascanner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	java "github.com/Rhuan-Marques/aracne/internal/topology/java"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// JavaScanner implements scanner.LanguageScanner for Java source trees.
type JavaScanner struct {
	name        string
	extensions  []string
	detectFiles []string
}

// NewJavaScanner constructs a JavaScanner.
func NewJavaScanner() *JavaScanner {
	return &JavaScanner{
		name:        "java",
		extensions:  []string{".java"},
		detectFiles: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"},
	}
}

// Name returns the scanner's language identifier.
func (s *JavaScanner) Name() string { return s.name }

// Extensions returns the file extensions this scanner handles.
func (s *JavaScanner) Extensions() []string { return s.extensions }

// Detect reports whether a Java project lives at root (a build file present, or
// any .java file under the root / src tree).
func (s *JavaScanner) Detect(root string) bool {
	for _, name := range s.detectFiles {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	for _, ext := range s.extensions {
		if m, _ := filepath.Glob(filepath.Join(root, "*"+ext)); len(m) > 0 {
			return true
		}
		if m, _ := filepath.Glob(filepath.Join(root, "src", "*"+ext)); len(m) > 0 {
			return true
		}
	}
	// Fall back to a recursive probe for a .java file (Maven/Gradle layouts).
	if len(collectJavaFiles(root)) > 0 {
		return true
	}
	return false
}

// Scan parses the whole Java tree and returns a resolved generic topology.
func (s *JavaScanner) Scan(root string) (*domain.Topology, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to access root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory")
	}

	ctx := buildJavaModel(absRoot)
	files := collectJavaFiles(absRoot)

	gt := newTopology(absRoot)

	// Parse files concurrently (bounded by scanner.Workers()); each ParseFile owns
	// its tree-sitter parser and frees the tree before returning, so only the
	// compact digested results are retained. Registration stays sequential and in
	// input order so the graph is deterministic.
	type parseRec struct {
		file string
		pr   *ParseResult
		err  error
	}
	recs := scanner.ParallelParse(files, "parsing "+s.Name(), func(f string) parseRec {
		pr, err := ParseFile(f, "")
		return parseRec{file: f, pr: pr, err: err}
	})
	var results []*ParseResult
	for _, rec := range recs {
		if rec.err != nil || rec.pr == nil {
			if rec.err != nil {
				gt.Errors[rec.file] = rec.err.Error()
			}
			continue
		}
		results = append(results, rec.pr)
		applyParsedFile(gt, rec.pr)
	}

	ctx.indexDeclarations(gt)
	resolveTopology(gt, results, ctx)
	return java.ToGeneric(gt, s.Name()), nil
}

// UpdateFile re-parses a single changed file, preserving descriptions and
// emitting signature/removal warnings, then re-resolves it into the topology.
func (s *JavaScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	gt := java.FromGeneric(topo)
	if gt == nil {
		return nil, fmt.Errorf("failed to convert topology from generic")
	}
	if gt.Errors == nil {
		gt.Errors = map[string]string{}
	}
	var warnings []domain.TopologyWarning

	root := gt.Root
	if root == "" {
		return warnings, nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return warnings, nil
	}

	ctx := buildJavaModel(root)

	oldMod, had := gt.Modules[absPath]
	var oldMethods map[string]java.JavaMethod
	var oldClasses map[string]java.JavaClass
	var oldIfaces map[string]java.JavaInterface
	if had {
		oldMethods, oldClasses, oldIfaces = snapshotModule(gt, oldMod)
		removeModule(gt, oldMod)
	}

	pr, err := ParseFile(absPath, "")
	if err != nil {
		gt.Errors[absPath] = err.Error()
		out := java.ToGeneric(gt, s.Name())
		topo.Resources = out.Resources
		topo.Errors = out.Errors
		return warnings, nil
	}

	if had {
		preserveDescriptions(pr, oldClasses, oldIfaces, oldMod)
		warnings = diffMethodWarnings(oldMethods, pr)
	}

	// Every other file declaring one of the same IDs is re-parsed along with this one,
	// and all are applied in path order, as Scan applies them; see coOwners.
	results := []*ParseResult{pr}
	var coOldMethods []map[string]java.JavaMethod
	for _, f := range coOwners(gt, absPath, oldMod, pr) {
		coMod := gt.Modules[f]
		coMethods, coClasses, coIfaces := snapshotModule(gt, coMod)
		removeModule(gt, coMod)
		coPR, coErr := ParseFile(f, "")
		if coErr != nil {
			gt.Errors[f] = coErr.Error()
			continue
		}
		preserveDescriptions(coPR, coClasses, coIfaces, coMod)
		coOldMethods = append(coOldMethods, coMethods)
		results = append(results, coPR)
		delete(gt.Errors, f)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].FileID < results[j].FileID })
	for _, r := range results {
		applyParsedFile(gt, r)
	}
	ctx.indexDeclarations(gt)
	resolveTopology(gt, results, ctx)
	if had {
		preserveMethodDescriptions(gt, oldMethods)
	}
	for _, m := range coOldMethods {
		preserveMethodDescriptions(gt, m)
	}
	delete(gt.Errors, absPath)

	out := java.ToGeneric(gt, s.Name())
	topo.Resources = out.Resources
	topo.Errors = out.Errors
	return warnings, nil
}

// newTopology allocates an empty JavaTopology rooted at absRoot.
func newTopology(absRoot string) *java.JavaTopology {
	return &java.JavaTopology{
		Root:       absRoot,
		Classes:    map[string]java.JavaClass{},
		Interfaces: map[string]java.JavaInterface{},
		Methods:    map[string]java.JavaMethod{},
		Modules:    map[string]java.JavaModule{},
		Errors:     map[string]string{},
	}
}

// applyParsedFile inserts a parsed file's module, types, and ownership edges. The
// module owns every method via has_function (so snapshot/remove can find them);
// classes get has_method later, in populateMethods.
func applyParsedFile(gt *java.JavaTopology, pr *ParseResult) {
	mod := java.JavaModule{
		ID:          pr.FileID,
		Name:        filepath.Base(pr.FileID),
		Description: pr.FileDescription,
		Package:     pr.Package,
		Connections: map[java.ConnectionKind][]string{},
	}
	for _, st := range pr.Classes {
		gt.Classes[st.ID] = st
		mod.Connections[java.ConnHasStruct] = append(mod.Connections[java.ConnHasStruct], st.ID)
	}
	for _, t := range pr.Interfaces {
		gt.Interfaces[t.ID] = t
		mod.Connections[java.ConnHasTrait] = append(mod.Connections[java.ConnHasTrait], t.ID)
	}
	for _, mp := range pr.Methods {
		gt.Methods[mp.Method.ID] = mp.Method
		mod.Connections[java.ConnHasFunc] = append(mod.Connections[java.ConnHasFunc], mp.Method.ID)
	}
	gt.Modules[pr.FileID] = mod
}

// resolveTopology runs the resolution passes. Per-file passes (imports, hierarchy
// records, type/annotation uses, body analysis) run for the supplied parse
// results; structural passes (methods, hierarchy, constructors) run whole-graph
// from round-tripping module records so full == incremental == hard.
func resolveTopology(gt *java.JavaTopology, results []*ParseResult, ctx *javaCtx) {
	for _, pr := range results {
		resolveImports(pr, ctx, gt)
	}
	populateMethods(gt, ctx)
	rebuildHierarchy(gt)
	markConstructors(gt)
	resolveMethodTypingIDs(gt, results, ctx)
	for _, pr := range results {
		analyzeBodies(gt, pr, ctx)
	}
	collectDependencies(gt)
	dedupAll(gt)
}

// snapshotModule extracts a module's owned methods/classes/interfaces into maps,
// used to preserve descriptions and diff signatures across an update.
func snapshotModule(gt *java.JavaTopology, mod java.JavaModule) (
	map[string]java.JavaMethod,
	map[string]java.JavaClass,
	map[string]java.JavaInterface,
) {
	methods := map[string]java.JavaMethod{}
	for _, id := range mod.Functions() {
		if m, ok := gt.Methods[id]; ok {
			methods[id] = m
		}
	}
	classes := map[string]java.JavaClass{}
	for _, id := range mod.Structs() {
		if c, ok := gt.Classes[id]; ok {
			classes[id] = c
		}
	}
	ifaces := map[string]java.JavaInterface{}
	for _, id := range mod.Interfaces() {
		if t, ok := gt.Interfaces[id]; ok {
			ifaces[id] = t
		}
	}
	return methods, classes, ifaces
}

// removeModule deletes a module and all types/methods it owns from the topology.
func removeModule(gt *java.JavaTopology, mod java.JavaModule) {
	for _, id := range mod.Functions() {
		delete(gt.Methods, id)
	}
	for _, id := range mod.Structs() {
		delete(gt.Classes, id)
	}
	for _, id := range mod.Interfaces() {
		delete(gt.Interfaces, id)
	}
	delete(gt.Modules, mod.ID)
}

// coOwners returns, sorted, the other files that declare one of the type or method
// IDs path declares -- before its edit or after it -- closed transitively.
//
// One FQN can be declared by several files (src/main/java and src/main/java11 of a
// multi-release jar, two modules of one build). The graph keeps a single copy of the
// ID: the last one in path order, which is the one Scan applies last. Re-parsing only
// the edited file let IT win instead, so an edit to the src/main/java copy replaced
// the copy a full scan keeps. Re-parsing the co-owners with it restores Scan's order.
//
// A co-owner whose file no longer exists is left out: it is about to be removed, and
// the survivors must take its IDs over first, or the removal takes them along.
func coOwners(gt *java.JavaTopology, path string, oldMod java.JavaModule, pr *ParseResult) []string {
	owners := map[string][]string{} // ID -> the other modules declaring it
	for id, mod := range gt.Modules {
		if id == path {
			continue
		}
		for _, x := range ownedIDs(mod) {
			owners[x] = append(owners[x], id)
		}
	}
	pending := ownedIDs(oldMod)
	for _, c := range pr.Classes {
		pending = append(pending, c.ID)
	}
	for _, t := range pr.Interfaces {
		pending = append(pending, t.ID)
	}
	for _, mp := range pr.Methods {
		pending = append(pending, mp.Method.ID)
	}
	group := map[string]bool{}
	for len(pending) > 0 {
		x := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, f := range owners[x] {
			if group[f] {
				continue
			}
			if _, err := os.Stat(f); err != nil {
				continue
			}
			group[f] = true
			pending = append(pending, ownedIDs(gt.Modules[f])...)
		}
	}
	out := make([]string, 0, len(group))
	for f := range group {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// ownedIDs lists every type and method ID a module declares.
func ownedIDs(mod java.JavaModule) []string {
	ids := append([]string(nil), mod.Functions()...)
	ids = append(ids, mod.Structs()...)
	return append(ids, mod.Interfaces()...)
}

// preserveDescriptions carries forward stored class/interface and module
// descriptions when the re-parsed resource has none. Methods are handled
// separately (after resolution recreates them).
func preserveDescriptions(pr *ParseResult, oldClasses map[string]java.JavaClass, oldIfaces map[string]java.JavaInterface, oldMod java.JavaModule) {
	for i, c := range pr.Classes {
		if old, ok := oldClasses[c.ID]; ok && c.Description == "" && old.Description != "" {
			pr.Classes[i].Description = old.Description
		}
	}
	for i, t := range pr.Interfaces {
		if old, ok := oldIfaces[t.ID]; ok && t.Description == "" && old.Description != "" {
			pr.Interfaces[i].Description = old.Description
		}
	}
	if pr.FileDescription == "" {
		pr.FileDescription = oldMod.Description
	}
}

// preserveMethodDescriptions carries forward stored method descriptions after
// resolution recreates them.
func preserveMethodDescriptions(gt *java.JavaTopology, oldMethods map[string]java.JavaMethod) {
	for id, old := range oldMethods {
		if old.Description == "" {
			continue
		}
		if m, ok := gt.Methods[id]; ok && m.Description == "" {
			m.Description = old.Description
			gt.Methods[id] = m
		}
	}
}

// diffMethodWarnings compares old module methods against the re-parsed file,
// emitting node-removed and signature-changed warnings. Because method IDs encode
// their signature, a signature change appears as a removed ID whose owner+name
// still exists.
func diffMethodWarnings(oldMethods map[string]java.JavaMethod, pr *ParseResult) []domain.TopologyWarning {
	newByID := map[string]java.JavaMethod{}
	// owner+"."+name -> the method's NEW id. A Java method id encodes its
	// signature, so a signature change shows up as a removed id whose owner and
	// name still exist under a different id. The warning has to be attributed to
	// that NEW id: attributing it to the old one made CleanupOrphanedWarnings
	// drop every Java sig_change warning before it reached the database.
	newIDByOwnerName := map[string]string{}
	for _, mp := range pr.Methods {
		newByID[mp.Method.ID] = mp.Method
		if mp.Method.MethodFrom != nil {
			newIDByOwnerName[*mp.Method.MethodFrom+"."+mp.Method.Name] = mp.Method.ID
		}
	}

	var warnings []domain.TopologyWarning
	for id, old := range oldMethods {
		if nm, ok := newByID[id]; ok {
			// The id encodes only the parameter list, so a surviving id can still have
			// changed what it RETURNS -- invisible to every call site, and a compile error at
			// each one that uses the result.
			if !sameJavaTypes(old.Output, nm.Output) {
				warnings = append(warnings, domain.TopologyWarning{
					ID:       id + "@sig_change@",
					SourceID: id,
					Kind:     domain.WarnSignatureChanged,
					Message:  fmt.Sprintf("method %s changed its return type, verify callers", old.Name),
				})
			}
			continue
		}
		owner := ""
		if old.MethodFrom != nil {
			owner = *old.MethodFrom
		}
		if newID, ok := newIDByOwnerName[owner+"."+old.Name]; ok {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       newID + "@sig_change@",
				SourceID: newID,
				Kind:     domain.WarnSignatureChanged,
				Message:  fmt.Sprintf("method %s changed signature, verify callers", old.Name),
			})
			continue
		}
		// A removed method is reported by the manager's referrer pass against
		// its surviving callers; see the note in jsscanner.diffFuncWarnings.
	}
	return warnings
}

// sameJavaTypes compares two declared type lists by their written text.
func sameJavaTypes(a, b []java.VariableDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Typing != b[i].Typing {
			return false
		}
	}
	return true
}

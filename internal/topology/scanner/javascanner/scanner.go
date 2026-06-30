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

	"aracne/internal/topology/domain"
	java "aracne/internal/topology/java"
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
	var results []*ParseResult
	for _, f := range files {
		pr, err := ParseFile(f, "")
		if err != nil {
			gt.Errors[f] = err.Error()
			continue
		}
		results = append(results, pr)
		applyParsedFile(gt, pr)
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

	applyParsedFile(gt, pr)
	ctx.indexDeclarations(gt)
	resolveTopology(gt, []*ParseResult{pr}, ctx)
	if had {
		preserveMethodDescriptions(gt, oldMethods)
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
	newByID := map[string]bool{}
	newByOwnerName := map[string]bool{}
	for _, mp := range pr.Methods {
		newByID[mp.Method.ID] = true
		if mp.Method.MethodFrom != nil {
			newByOwnerName[*mp.Method.MethodFrom+"."+mp.Method.Name] = true
		}
	}

	var warnings []domain.TopologyWarning
	for id, old := range oldMethods {
		if newByID[id] {
			continue
		}
		owner := ""
		if old.MethodFrom != nil {
			owner = *old.MethodFrom
		}
		if newByOwnerName[owner+"."+old.Name] {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       id + "@sig_change@",
				SourceID: id,
				Kind:     domain.WarnSignatureChanged,
				Message:  fmt.Sprintf("method %s changed signature, verify callers", old.Name),
			})
			continue
		}
		warnings = append(warnings, domain.TopologyWarning{
			ID:       id + "@node_removed@",
			SourceID: id,
			Kind:     domain.WarnNodeRemoved,
			Message:  fmt.Sprintf("method %s was removed", old.Name),
		})
	}
	return warnings
}

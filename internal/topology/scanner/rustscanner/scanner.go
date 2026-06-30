// Package rustscanner parses a Rust source tree with tree-sitter and builds a
// generic topology graph via the internal/topology/rust model. It mirrors the
// jsscanner package structure (manual cursor walks, a parse/resolve split, and a
// snapshot+diff incremental UpdateFile).
//
// Where the task spec's node-type/field names differed from the bundled
// tree-sitter-rust grammar, the grammar was used (see corrections noted in the
// implementation): trait supertraits live under a `bounds` field as a
// `trait_bounds` node (not a declaration_list); a trait's associated types are
// `associated_type` nodes (impl-side aliases are `type_item`); `#[derive(...)]`
// and `#[cfg(test)]`/`#[test]` attributes are preceding sibling `attribute_item`
// nodes (not children of the item); function async/unsafe/const flags live in a
// `function_modifiers` child; and `macro_rules!` definitions are
// `macro_definition` nodes.
package rustscanner

import (
	"fmt"
	"os"
	"path/filepath"

	"aracne/internal/topology/domain"
	rust "aracne/internal/topology/rust"
)

// RustScanner implements scanner.LanguageScanner for Rust source trees.
type RustScanner struct {
	name        string
	extensions  []string
	detectFiles []string
}

// NewRustScanner constructs a RustScanner.
func NewRustScanner() *RustScanner {
	return &RustScanner{
		name:        "rust",
		extensions:  []string{".rs"},
		detectFiles: []string{"Cargo.toml"},
	}
}

// Name returns the scanner's language identifier.
func (s *RustScanner) Name() string { return s.name }

// Extensions returns the file extensions this scanner handles.
func (s *RustScanner) Extensions() []string { return s.extensions }

// Detect reports whether a Rust project lives at root (Cargo.toml present, or any
// .rs file).
func (s *RustScanner) Detect(root string) bool {
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
	return false
}

// Scan parses the whole Rust tree and returns a resolved generic topology.
func (s *RustScanner) Scan(root string) (*domain.Topology, error) {
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

	ctx := buildCrateModel(absRoot)
	files := collectRustFiles(absRoot)
	ctx.buildModuleIndex(files)

	gt := newTopology(absRoot)
	var results []*ParseResult
	for _, f := range files {
		pr, err := ParseFile(f, ctx.moduleOfFile[f])
		if err != nil {
			gt.Errors[f] = err.Error()
			continue
		}
		results = append(results, pr)
		applyParsedFile(gt, pr)
	}

	resolveTopology(gt, results, ctx)
	return rust.ToGeneric(gt, s.Name()), nil
}

// UpdateFile re-parses a single changed file, preserving descriptions and
// emitting signature/removal warnings, then re-resolves it into the topology.
func (s *RustScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	gt := rust.FromGeneric(topo)
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

	ctx := buildCrateModel(root)
	files := collectRustFiles(root)
	ctx.buildModuleIndex(files)
	modulePath := ctx.moduleOfFile[absPath]
	if modulePath == "" {
		modulePath, _ = ctx.moduleForFile(absPath)
	}

	oldMod, had := gt.Modules[absPath]
	var oldFuncs map[string]rust.RustFunction
	var oldStructs map[string]rust.RustStruct
	var oldTraits map[string]rust.RustTrait
	var oldNamed map[string]rust.RustNamedType
	var oldVars map[string]rust.RustVariable
	preStructNames := structNameMap(gt)
	if had {
		oldFuncs, oldStructs, oldTraits, oldNamed, oldVars = snapshotModule(gt, oldMod)
		removeModule(gt, oldMod)
	}

	pr, err := ParseFile(absPath, modulePath)
	if err != nil {
		gt.Errors[absPath] = err.Error()
		out := rust.ToGeneric(gt, s.Name())
		topo.Resources = out.Resources
		topo.Errors = out.Errors
		return warnings, nil
	}

	if had {
		preserveDescriptions(pr, oldStructs, oldTraits, oldNamed, oldVars, oldMod)
		warnings = diffFuncWarnings(oldFuncs, pr, preStructNames)
	}

	applyParsedFile(gt, pr)
	resolveTopology(gt, []*ParseResult{pr}, ctx)
	if had {
		preserveFunctionDescriptions(gt, oldFuncs)
	}
	delete(gt.Errors, absPath)

	out := rust.ToGeneric(gt, s.Name())
	topo.Resources = out.Resources
	topo.Errors = out.Errors
	return warnings, nil
}

// newTopology allocates an empty RustTopology rooted at absRoot.
func newTopology(absRoot string) *rust.RustTopology {
	return &rust.RustTopology{
		Root:       absRoot,
		Functions:  map[string]rust.RustFunction{},
		Structs:    map[string]rust.RustStruct{},
		Traits:     map[string]rust.RustTrait{},
		NamedTypes: map[string]rust.RustNamedType{},
		Variables:  map[string]rust.RustVariable{},
		Modules:    map[string]rust.RustModule{},
		Errors:     map[string]string{},
	}
}

// applyParsedFile inserts a parsed file's module, symbols, and ownership edges.
// Impl methods are added later, in attachImpls, since their IDs need the resolved
// target type.
func applyParsedFile(gt *rust.RustTopology, pr *ParseResult) {
	mod := rust.RustModule{
		ID:          pr.FileID,
		Name:        filepath.Base(pr.FileID),
		Description: pr.FileDescription,
		ModulePath:  pr.ModulePath,
		Connections: map[rust.ConnectionKind][]string{},
	}
	for _, st := range pr.Structs {
		gt.Structs[st.ID] = st
		mod.Connections[rust.ConnHasStruct] = append(mod.Connections[rust.ConnHasStruct], st.ID)
	}
	for _, t := range pr.Traits {
		gt.Traits[t.ID] = t
		mod.Connections[rust.ConnHasTrait] = append(mod.Connections[rust.ConnHasTrait], t.ID)
	}
	for _, fp := range pr.Functions {
		gt.Functions[fp.Function.ID] = fp.Function
		mod.Connections[rust.ConnHasFunc] = append(mod.Connections[rust.ConnHasFunc], fp.Function.ID)
	}
	for _, nt := range pr.NamedTypes {
		gt.NamedTypes[nt.ID] = nt
		mod.Connections[rust.ConnHasNamedType] = append(mod.Connections[rust.ConnHasNamedType], nt.ID)
	}
	for _, v := range pr.Variables {
		gt.Variables[v.ID] = v
		mod.Connections[rust.ConnHasVar] = append(mod.Connections[rust.ConnHasVar], v.ID)
	}
	gt.Modules[pr.FileID] = mod
}

// resolveTopology runs the resolution passes. Structural passes (impl attach,
// derives, methods, implemented_by, supertraits, constructors) run whole-graph;
// use resolution, type-ID resolution, and body analysis run for the supplied
// parse results (all files on a full scan, the changed file on an update).
func resolveTopology(gt *rust.RustTopology, results []*ParseResult, ctx *crateCtx) {
	for _, pr := range results {
		attachImpls(gt, pr)
	}
	for _, pr := range results {
		imap, modFiles, deps := scannerSingleton.resolveUses(pr, ctx, gt)
		pr.ImportMap = imap
		mod := gt.Modules[pr.FileID]
		if mod.Connections == nil {
			mod.Connections = map[rust.ConnectionKind][]string{}
		}
		delete(mod.Connections, rust.ConnImportsModule)
		delete(mod.Connections, rust.ConnImportsDep)
		for _, f := range modFiles {
			mod.Connections[rust.ConnImportsModule] = append(mod.Connections[rust.ConnImportsModule], f)
		}
		for _, d := range deps {
			mod.Connections[rust.ConnImportsDep] = append(mod.Connections[rust.ConnImportsDep], d)
		}
		mod.Connections = uniqueConns(mod.Connections)
		gt.Modules[pr.FileID] = mod
	}

	populateStructMethods(gt)
	rebuildImplements(gt)
	matchSupertraits(gt)

	resolveFunctionTypingIDs(gt, results)
	detectConstructors(gt)

	for _, pr := range results {
		analyzeBodies(gt, pr, ctx)
	}

	collectDependencies(gt)
}

// scannerSingleton lets resolveTopology reuse the stateless use-resolution method
// without threading a receiver through every pass.
var scannerSingleton = &RustScanner{name: "rust"}

// snapshotModule extracts a module's symbols (by its ownership edges) into maps,
// used to preserve descriptions and diff signatures across an update.
func snapshotModule(gt *rust.RustTopology, mod rust.RustModule) (
	map[string]rust.RustFunction,
	map[string]rust.RustStruct,
	map[string]rust.RustTrait,
	map[string]rust.RustNamedType,
	map[string]rust.RustVariable,
) {
	funcs := map[string]rust.RustFunction{}
	for _, id := range mod.Functions() {
		if f, ok := gt.Functions[id]; ok {
			funcs[id] = f
		}
	}
	structs := map[string]rust.RustStruct{}
	for _, id := range mod.Structs() {
		if st, ok := gt.Structs[id]; ok {
			structs[id] = st
		}
	}
	traits := map[string]rust.RustTrait{}
	for _, id := range mod.Traits() {
		if t, ok := gt.Traits[id]; ok {
			traits[id] = t
		}
	}
	named := map[string]rust.RustNamedType{}
	for _, id := range mod.NamedTypes() {
		if n, ok := gt.NamedTypes[id]; ok {
			named[id] = n
		}
	}
	vars := map[string]rust.RustVariable{}
	for _, id := range mod.Variables() {
		if v, ok := gt.Variables[id]; ok {
			vars[id] = v
		}
	}
	return funcs, structs, traits, named, vars
}

// removeModule deletes a module and all symbols it owns from the topology.
func removeModule(gt *rust.RustTopology, mod rust.RustModule) {
	for _, id := range mod.Functions() {
		delete(gt.Functions, id)
	}
	for _, id := range mod.Structs() {
		delete(gt.Structs, id)
	}
	for _, id := range mod.Traits() {
		delete(gt.Traits, id)
	}
	for _, id := range mod.NamedTypes() {
		delete(gt.NamedTypes, id)
	}
	for _, id := range mod.Variables() {
		delete(gt.Variables, id)
	}
	delete(gt.Modules, mod.ID)
}

// preserveDescriptions carries forward stored descriptions for structs, traits,
// named types, variables, and the module when the re-parsed resource has none.
// Functions are handled separately (after resolution) since impl methods are
// created during resolveTopology.
func preserveDescriptions(pr *ParseResult, oldStructs map[string]rust.RustStruct, oldTraits map[string]rust.RustTrait, oldNamed map[string]rust.RustNamedType, oldVars map[string]rust.RustVariable, oldMod rust.RustModule) {
	for i, st := range pr.Structs {
		if old, ok := oldStructs[st.ID]; ok && st.Description == "" && old.Description != "" {
			pr.Structs[i].Description = old.Description
		}
	}
	for i, t := range pr.Traits {
		if old, ok := oldTraits[t.ID]; ok && t.Description == "" && old.Description != "" {
			pr.Traits[i].Description = old.Description
		}
	}
	for i, n := range pr.NamedTypes {
		if old, ok := oldNamed[n.ID]; ok && n.Description == "" && old.Description != "" {
			pr.NamedTypes[i].Description = old.Description
		}
	}
	for i, v := range pr.Variables {
		if old, ok := oldVars[v.ID]; ok && v.Description == "" && old.Description != "" {
			pr.Variables[i].Description = old.Description
		}
	}
	if pr.FileDescription == "" {
		pr.FileDescription = oldMod.Description
	}
}

// preserveFunctionDescriptions carries forward stored function/method
// descriptions after resolution recreates them.
func preserveFunctionDescriptions(gt *rust.RustTopology, oldFuncs map[string]rust.RustFunction) {
	for id, old := range oldFuncs {
		if old.Description == "" {
			continue
		}
		if f, ok := gt.Functions[id]; ok && f.Description == "" {
			f.Description = old.Description
			gt.Functions[id] = f
		}
	}
}

// structNameMap maps each struct/enum's simple name to its ID (last write wins),
// used to compute impl-method IDs while diffing an update.
func structNameMap(gt *rust.RustTopology) map[string]string {
	out := map[string]string{}
	for id := range gt.Structs {
		out[lastSeg(id)] = id
	}
	return out
}

// diffFuncWarnings compares old module functions (including impl methods) against
// the re-parsed file, emitting node-removed and signature-changed warnings.
func diffFuncWarnings(oldFuncs map[string]rust.RustFunction, pr *ParseResult, structNames map[string]string) []domain.TopologyWarning {
	newFuncs := map[string]rust.RustFunction{}
	for _, fp := range pr.Functions {
		newFuncs[fp.Function.ID] = fp.Function
	}
	for _, impl := range pr.Impls {
		sid := structNames[normType(impl.TypeName)]
		if sid == "" {
			continue
		}
		for _, m := range impl.Methods {
			newFuncs[sid+"::"+m.Name] = rust.RustFunction{
				Name:     m.Name,
				Input:    m.Input,
				Output:   m.Output,
				IsAsync:  m.IsAsync,
				IsUnsafe: m.IsUnsafe,
				IsConst:  m.IsConst,
			}
		}
	}

	var warnings []domain.TopologyWarning
	for id, old := range oldFuncs {
		nf, ok := newFuncs[id]
		if !ok {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       id + "@node_removed@",
				SourceID: id,
				Kind:     domain.WarnNodeRemoved,
				Message:  fmt.Sprintf("function %s was removed", old.Name),
			})
			continue
		}
		if !signaturesEqual(old, nf) {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       id + "@sig_change@",
				SourceID: id,
				Kind:     domain.WarnSignatureChanged,
				Message:  fmt.Sprintf("function %s changed signature, verify callers", old.Name),
			})
		}
	}
	return warnings
}

// signaturesEqual compares two Rust functions by parameter names and
// async/unsafe/const flags.
func signaturesEqual(a, b rust.RustFunction) bool {
	if len(a.Input) != len(b.Input) {
		return false
	}
	for i := range a.Input {
		if a.Input[i].Name != b.Input[i].Name {
			return false
		}
	}
	return a.IsAsync == b.IsAsync && a.IsUnsafe == b.IsUnsafe && a.IsConst == b.IsConst
}

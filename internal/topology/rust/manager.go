package rust

import (
	"fmt"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// RustManager wraps a generic topology engine to handle Rust analysis and
// resource queries.
type RustManager struct {
	generic *topology.TopologyManager
}

// NewRustManager creates a new RustManager wrapping a generic topology manager.
func NewRustManager(mgr *topology.TopologyManager) *RustManager {
	return &RustManager{generic: mgr}
}

// Generic returns the underlying generic topology manager.
func (m *RustManager) Generic() *topology.TopologyManager {
	return m.generic
}

// ReadFunction retrieves a Rust function/method and its context: parent struct
// (for methods), called functions, used structs/traits/named-types/variables,
// and external crate dependencies.
func (m *RustManager) ReadFunction(id string, opts ...topology.TopologyOption) (*RustFunctionContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	fn, ok := gt.Functions[FunctionID(id)]
	if !ok {
		return nil, fmt.Errorf("function %s not found in topology", id)
	}

	ctx := &RustFunctionContext{}

	funcCut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &FunctionCut{RustFunction: fn, Cut: funcCut.Cut}

	if opt.HasResource(domain.ResourceStruct) && fn.MethodFrom != nil {
		if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			// Past the ceiling the enclosing type stops riding along with the member and
			// becomes an ordinary neighbour instead. For Python/JS/Java the cut IS the whole
			// class, so inlining it unconditionally meant reading one method returned the
			// entire class.
			if opt.ContextFilter().InlineParent(parent.Loc.EndsAt - parent.Loc.StartsAt + 1) {
				ctx.ParentStruct = &StructCut{RustStruct: parent, Cut: parentCut.Cut}
			} else {
				ctx.OversizedParent = &StructCut{RustStruct: parent, Cut: parentCut.Cut}
			}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, calledID := range fn.Calls() {
			if string(calledID) == id {
				continue
			}
			if called, ok := gt.Functions[calledID]; ok {
				ctx.CalledFunctions = append(ctx.CalledFunctions, simplifyFunction(called))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sID := range fn.UsesStruct() {
			if s, ok := gt.Structs[sID]; ok {
				ctx.StructsUsed = append(ctx.StructsUsed, structUsageWithMethods(gt, s))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tid := range fn.UsesTrait() {
			if t, ok := gt.Traits[tid]; ok {
				ctx.TraitsUsed = append(ctx.TraitsUsed, simplifyTrait(t))
			}
		}
	}

	if opt.HasResource(domain.ResourceNamedType) {
		for _, ntid := range fn.UsesNamedType() {
			if n, ok := gt.NamedTypes[ntid]; ok {
				ctx.NamedTypesUsed = append(ctx.NamedTypesUsed, simplifyNamedType(n))
			}
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, vid := range fn.UsesVar() {
			if v, ok := gt.Variables[vid]; ok {
				ctx.VarsUsed = append(ctx.VarsUsed, simplifyVariable(v))
			}
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		depSet := make(map[DependencyPath]bool)
		for _, d := range fn.UsesDep() {
			depSet[d] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
				for _, d := range parent.UsesDep() {
					depSet[d] = true
				}
			}
		}
		for d := range depSet {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	return ctx, nil
}

// ReadStruct retrieves a Rust struct/enum/union and its context: constructor,
// enum variants, impl methods, implemented traits, used structs/named-types, and
// external crate dependencies.
func (m *RustManager) ReadStruct(id string, opts ...topology.TopologyOption) (*RustStructContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	s, ok := gt.Structs[StructID(id)]
	if !ok {
		return nil, fmt.Errorf("struct %s not found in topology", id)
	}

	ctx := &RustStructContext{IsEnum: s.IsEnum, Variants: s.Variants}

	cut, err := m.generic.Cut(s.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Struct = &StructCut{RustStruct: s, Cut: cut.Cut}

	if opt.HasResource(domain.ResourceFunction) && s.Constructor != nil {
		if cfn, ok := gt.Functions[*s.Constructor]; ok {
			cCut, err := m.generic.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &FunctionCut{RustFunction: cfn, Cut: cCut.Cut}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, methodID := range collectMethodIDs(gt, StructID(id)) {
			if fn, ok := gt.Functions[methodID]; ok {
				ctx.Methods = append(ctx.Methods, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tid := range s.Implements() {
			if t, ok := gt.Traits[tid]; ok {
				ctx.Implements = append(ctx.Implements, simplifyTrait(t))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sid := range s.UsesStruct() {
			if string(sid) == id {
				continue
			}
			if us, ok := gt.Structs[sid]; ok {
				ctx.StructsUsed = append(ctx.StructsUsed, structUsageWithMethods(gt, us))
			}
		}
	}

	if opt.HasResource(domain.ResourceNamedType) {
		for _, ntid := range s.UsesNamedType() {
			if n, ok := gt.NamedTypes[ntid]; ok {
				ctx.NamedTypesUsed = append(ctx.NamedTypesUsed, simplifyNamedType(n))
			}
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		for _, d := range s.UsesDep() {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	return ctx, nil
}

// ReadInterface retrieves a Rust trait (the interface kind) and its context: the
// supertraits it inherits and the structs/enums that implement it.
func (m *RustManager) ReadInterface(id string, opts ...topology.TopologyOption) (*RustInterfaceContext, error) {
	// Options are accepted for API parity with the other Read* methods; a trait
	// read always surfaces its supertraits and implementors.
	for _, o := range opts {
		o(&topology.TopologyOptions{})
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	t, ok := gt.Traits[TraitID(id)]
	if !ok {
		return nil, fmt.Errorf("trait %s not found in topology", id)
	}

	ctx := &RustInterfaceContext{}
	cut, err := m.generic.Cut(t.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Trait = &TraitCut{RustTrait: t, Cut: cut.Cut}

	for _, superID := range t.Inherits() {
		if st, ok := gt.Traits[superID]; ok {
			ctx.Supertraits = append(ctx.Supertraits, simplifyTrait(st))
		}
	}
	for _, structID := range t.ImplementedBy() {
		if s, ok := gt.Structs[structID]; ok {
			ctx.Implementors = append(ctx.Implementors, simplifyStruct(s))
		}
	}

	return ctx, nil
}

// ReadNamedType retrieves a Rust type alias and the resources that use it.
func (m *RustManager) ReadNamedType(id string, opts ...topology.TopologyOption) (*RustNamedTypeContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	nt, ok := gt.NamedTypes[NamedTypeID(id)]
	if !ok {
		return nil, fmt.Errorf("named type %s not found in topology", id)
	}

	ctx := &RustNamedTypeContext{}
	cut, err := m.generic.Cut(nt.Loc)
	if err != nil {
		return nil, err
	}
	ctx.NamedType = &NamedTypeCut{RustNamedType: nt, Cut: cut.Cut}
	ctx.UsedBy = usersOfNamedType(gt, id)

	return ctx, nil
}

// ReadModule retrieves a Rust module (a file) and its contents: functions,
// structs, traits, named types, variables, imported modules, and external crate
// dependencies.
func (m *RustManager) ReadModule(id string, opts ...topology.TopologyOption) (*RustModuleContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	mod, ok := gt.Modules[ModuleID(id)]
	if !ok {
		return nil, fmt.Errorf("module %s not found in topology", id)
	}

	ctx := &RustModuleContext{}
	modCut, err := m.generic.Cut(domain.Location{Path: string(ModuleID(id))})
	if err != nil {
		return nil, err
	}
	ctx.Module = &ModuleCut{RustModule: mod, Cut: modCut.Cut}
	ctx.ModulePath = mod.ModulePath

	if opt.HasResource(domain.ResourceFunction) {
		for _, fnID := range mod.Functions() {
			if fn, ok := gt.Functions[fnID]; ok {
				ctx.Functions = append(ctx.Functions, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sID := range mod.Structs() {
			if s, ok := gt.Structs[sID]; ok {
				ctx.Structs = append(ctx.Structs, structUsageWithMethods(gt, s))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tID := range mod.Traits() {
			if t, ok := gt.Traits[tID]; ok {
				ctx.Traits = append(ctx.Traits, simplifyTrait(t))
			}
		}
	}

	if opt.HasResource(domain.ResourceNamedType) {
		for _, ntID := range mod.NamedTypes() {
			if n, ok := gt.NamedTypes[ntID]; ok {
				ctx.NamedTypes = append(ctx.NamedTypes, simplifyNamedType(n))
			}
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, vID := range mod.Variables() {
			if v, ok := gt.Variables[vID]; ok {
				ctx.Variables = append(ctx.Variables, simplifyVariable(v))
			}
		}
	}

	for _, target := range mod.ModulesImported() {
		ctx.Imports = append(ctx.Imports, target)
	}
	for _, d := range mod.DependenciesImported() {
		ctx.Dependencies = append(ctx.Dependencies, d.CratePath)
	}

	return ctx, nil
}

// ReadDependency retrieves an external crate dependency and lists which
// functions and structs use it.
func (m *RustManager) ReadDependency(id string, opts ...topology.TopologyOption) (*RustDependencyContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	depPath := DependencyPath(id)
	ctx := &RustDependencyContext{Dependency: depPath}

	found := false
	for _, d := range gt.Dependencies {
		if d.CratePath == depPath {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("dependency %s not found in topology", id)
	}

	ctx.UsedBy = usersOfDependency(gt, depPath)

	return ctx, nil
}

// FindFunctionsByName searches for all functions with a matching name in the
// Rust topology.
func (m *RustManager) FindFunctionsByName(name string) ([]FunctionID, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)
	var results []FunctionID
	for id, fn := range gt.Functions {
		if fn.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

// FindStructsByName searches for all structs/enums/unions with a matching name
// in the Rust topology.
func (m *RustManager) FindStructsByName(name string) ([]StructID, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)
	var results []StructID
	for id, s := range gt.Structs {
		if s.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

// UpdateDescription updates a resource's description in the topology database.
func (m *RustManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

// ReadResourceAndCut returns the code cut for a resource (function, method,
// struct, trait, named type, variable, file, or dependency) by kind and ID.
func (m *RustManager) ReadResourceAndCut(id string, kind domain.ResourceKind) (*domain.CodeEntry, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	var loc domain.Location
	found := false

	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		if fn, ok := gt.Functions[FunctionID(id)]; ok {
			loc = fn.Loc
			found = true
		}
	case domain.ResourceStruct:
		if s, ok := gt.Structs[StructID(id)]; ok {
			loc = s.Loc
			found = true
		}
	case domain.ResourceInterface:
		if t, ok := gt.Traits[TraitID(id)]; ok {
			loc = t.Loc
			found = true
		}
	case domain.ResourceNamedType:
		if n, ok := gt.NamedTypes[NamedTypeID(id)]; ok {
			loc = n.Loc
			found = true
		}
	case domain.ResourceVariable:
		if v, ok := gt.Variables[VariableID(id)]; ok {
			loc = v.Location
			found = true
		}
	case domain.ResourceDependency:
		return &domain.CodeEntry{Cut: string(kind)}, nil
	case domain.ResourceFile:
		loc = domain.Location{Path: id}
		found = true
	default:
		return nil, fmt.Errorf("unknown resource: %s", kind)
	}

	if !found {
		return nil, fmt.Errorf("resource not found: %s", id)
	}

	return m.generic.Cut(loc)
}

package java

import (
	"fmt"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// JavaManager wraps a generic topology engine to handle Java analysis and
// resource queries.
type JavaManager struct {
	generic *topology.TopologyManager
}

// NewJavaManager creates a new JavaManager wrapping a generic topology manager.
func NewJavaManager(mgr *topology.TopologyManager) *JavaManager {
	return &JavaManager{generic: mgr}
}

// Generic returns the underlying generic topology manager.
func (m *JavaManager) Generic() *topology.TopologyManager {
	return m.generic
}

// ReadFunction retrieves a Java method/constructor and its context: parent class,
// called methods, used classes/interfaces, and external dependencies.
func (m *JavaManager) ReadFunction(id string, opts ...topology.TopologyOption) (*JavaFunctionContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	fn, ok := gt.Methods[FunctionID(id)]
	if !ok {
		return nil, fmt.Errorf("function %s not found in topology", id)
	}

	ctx := &JavaFunctionContext{}

	funcCut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &FunctionCut{JavaMethod: fn, Cut: funcCut.Cut}

	if opt.HasResource(domain.ResourceStruct) && fn.MethodFrom != nil {
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			ctx.ParentStruct = &StructCut{JavaClass: parent, Cut: parentCut.Cut}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, calledID := range fn.Calls() {
			if string(calledID) == id {
				continue
			}
			if called, ok := gt.Methods[calledID]; ok {
				ctx.CalledFunctions = append(ctx.CalledFunctions, simplifyFunction(called))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sID := range fn.UsesStruct() {
			if s, ok := gt.Classes[sID]; ok {
				ctx.StructsUsed = append(ctx.StructsUsed, structUsageWithMethods(gt, s))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tid := range fn.UsesInterface() {
			if t, ok := gt.Interfaces[tid]; ok {
				ctx.InterfacesUsed = append(ctx.InterfacesUsed, simplifyInterface(t))
			}
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		depSet := make(map[DependencyPath]bool)
		for _, d := range fn.UsesDep() {
			depSet[d] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
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

// ReadStruct retrieves a Java class/enum/record and its context: constructor,
// enum variants, record components, methods, implemented interfaces,
// superclasses, used classes, and external dependencies.
func (m *JavaManager) ReadStruct(id string, opts ...topology.TopologyOption) (*JavaStructContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	s, ok := gt.Classes[StructID(id)]
	if !ok {
		return nil, fmt.Errorf("struct %s not found in topology", id)
	}

	ctx := &JavaStructContext{
		IsEnum:     s.IsEnum,
		Variants:   s.Variants,
		IsRecord:   s.IsRecord,
		Components: s.Components,
	}

	cut, err := m.generic.Cut(s.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Struct = &StructCut{JavaClass: s, Cut: cut.Cut}

	if opt.HasResource(domain.ResourceFunction) && s.Constructor != nil {
		if cfn, ok := gt.Methods[*s.Constructor]; ok {
			cCut, err := m.generic.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &FunctionCut{JavaMethod: cfn, Cut: cCut.Cut}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, methodID := range collectMethodIDs(gt, StructID(id)) {
			if fn, ok := gt.Methods[methodID]; ok {
				ctx.Methods = append(ctx.Methods, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tid := range s.Implements() {
			if t, ok := gt.Interfaces[tid]; ok {
				ctx.Implements = append(ctx.Implements, simplifyInterface(t))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sid := range s.Inherits() {
			if sup, ok := gt.Classes[sid]; ok {
				ctx.Inherits = append(ctx.Inherits, simplifyStruct(sup))
			}
		}
		for _, sid := range s.UsesStruct() {
			if string(sid) == id {
				continue
			}
			if us, ok := gt.Classes[sid]; ok {
				ctx.StructsUsed = append(ctx.StructsUsed, structUsageWithMethods(gt, us))
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

// ReadInterface retrieves a Java interface/annotation and its context: the
// supertypes it extends and the classes/enums/records that implement it.
// Default/static method summaries and the annotation flag are carried on the
// interface cut.
func (m *JavaManager) ReadInterface(id string, opts ...topology.TopologyOption) (*JavaInterfaceContext, error) {
	// Options are accepted for API parity with the other Read* methods; an
	// interface read always surfaces its supertypes and implementors.
	for _, o := range opts {
		o(&topology.TopologyOptions{})
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	t, ok := gt.Interfaces[InterfaceID(id)]
	if !ok {
		return nil, fmt.Errorf("interface %s not found in topology", id)
	}

	ctx := &JavaInterfaceContext{IsAnnotation: t.IsAnnotation}
	cut, err := m.generic.Cut(t.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Interface = &InterfaceCut{JavaInterface: t, Cut: cut.Cut}

	for _, superID := range t.Inherits() {
		if st, ok := gt.Interfaces[superID]; ok {
			ctx.Supertypes = append(ctx.Supertypes, simplifyInterface(st))
		}
	}
	for _, structID := range t.ImplementedBy() {
		if s, ok := gt.Classes[structID]; ok {
			ctx.ImplementedBy = append(ctx.ImplementedBy, simplifyStruct(s))
		}
	}

	return ctx, nil
}

// ReadModule retrieves a Java module (a file) and its contents: methods,
// classes, interfaces, imported modules, and external dependencies.
func (m *JavaManager) ReadModule(id string, opts ...topology.TopologyOption) (*JavaModuleContext, error) {
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

	ctx := &JavaModuleContext{}
	modCut, err := m.generic.Cut(domain.Location{Path: string(ModuleID(id))})
	if err != nil {
		return nil, err
	}
	ctx.Module = &ModuleCut{JavaModule: mod, Cut: modCut.Cut}
	ctx.Package = mod.Package

	if opt.HasResource(domain.ResourceFunction) {
		for _, fnID := range mod.Functions() {
			if fn, ok := gt.Methods[fnID]; ok {
				ctx.Functions = append(ctx.Functions, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceStruct) {
		for _, sID := range mod.Structs() {
			if s, ok := gt.Classes[sID]; ok {
				ctx.Structs = append(ctx.Structs, structUsageWithMethods(gt, s))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, tID := range mod.Interfaces() {
			if t, ok := gt.Interfaces[tID]; ok {
				ctx.Interfaces = append(ctx.Interfaces, simplifyInterface(t))
			}
		}
	}

	for _, target := range mod.ModulesImported() {
		ctx.Imports = append(ctx.Imports, target)
	}
	for _, d := range mod.DependenciesImported() {
		ctx.Dependencies = append(ctx.Dependencies, d.Coordinate)
	}

	return ctx, nil
}

// ReadDependency retrieves an external dependency and lists which methods and
// classes use it.
func (m *JavaManager) ReadDependency(id string, opts ...topology.TopologyOption) (*JavaDependencyContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	depPath := DependencyPath(id)
	ctx := &JavaDependencyContext{Dependency: depPath}

	found := false
	for _, d := range gt.Dependencies {
		if d.Coordinate == depPath {
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

// FindFunctionsByName searches for all methods/constructors with a matching name
// in the Java topology.
func (m *JavaManager) FindFunctionsByName(name string) ([]FunctionID, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)
	var results []FunctionID
	for id, fn := range gt.Methods {
		if fn.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

// FindStructsByName searches for all classes/enums/records with a matching name
// in the Java topology.
func (m *JavaManager) FindStructsByName(name string) ([]StructID, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)
	var results []StructID
	for id, s := range gt.Classes {
		if s.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

// UpdateDescription updates a resource's description in the topology database.
func (m *JavaManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

// ReadResourceAndCut returns the code cut for a resource (method, class,
// interface, file, or dependency) by kind and ID.
func (m *JavaManager) ReadResourceAndCut(id string, kind domain.ResourceKind) (*domain.CodeEntry, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	var loc domain.Location
	found := false

	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		if fn, ok := gt.Methods[FunctionID(id)]; ok {
			loc = fn.Loc
			found = true
		}
	case domain.ResourceStruct:
		if s, ok := gt.Classes[StructID(id)]; ok {
			loc = s.Loc
			found = true
		}
	case domain.ResourceInterface:
		if t, ok := gt.Interfaces[InterfaceID(id)]; ok {
			loc = t.Loc
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

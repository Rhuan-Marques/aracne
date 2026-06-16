package javascript

import (
	"encoding/json"
	"fmt"
	"sort"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

type JavaScriptManager struct {
	generic *topology.TopologyManager
}

func NewJavaScriptManager(mgr *topology.TopologyManager) *JavaScriptManager {
	return &JavaScriptManager{generic: mgr}
}

func (m *JavaScriptManager) Generic() *topology.TopologyManager {
	return m.generic
}

func (m *JavaScriptManager) ReadFunction(id string, opts ...topology.TopologyOption) (*JavaScriptFunctionContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	funcID := FunctionID(id)
	fn, ok := gt.Functions[funcID]
	if !ok {
		return nil, fmt.Errorf("function %s not found in topology", id)
	}

	ctx := &JavaScriptFunctionContext{}

	funcCut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &FunctionCut{JavaScriptFunction: fn, Cut: funcCut.Cut}

	if opt.HasResource(domain.ResourceType) && fn.MethodFrom != nil {
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			ctx.ParentClass = &ClassCut{JavaScriptClass: parent, Cut: parentCut.Cut}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, calledID := range fn.Calls() {
			called, ok := gt.Functions[calledID]
			if !ok || called.MethodFrom != nil {
				continue
			}
			ctx.CalledFunctions = append(ctx.CalledFunctions, simplifyFunction(called))
		}
	}

	if opt.HasResource(domain.ResourceType) {
		for _, classID := range fn.UsesClass() {
			c, ok := gt.Classes[classID]
			if !ok {
				continue
			}
			usage := ClassUsage{
				ID:          c.ID,
				Name:        c.Name,
				Description: c.Description,
				Location:    c.Loc,
			}
			for _, calledID := range fn.Calls() {
				called, ok := gt.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == classID {
					usage.Methods = append(usage.Methods, simplifyFunction(called))
				}
			}
			ctx.ClassesUsed = append(ctx.ClassesUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, varID := range fn.UsesExtVar() {
			if v, ok := gt.ExternalVars[varID]; ok {
				ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, simplifyExtVar(v))
			}
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, iid := range fn.UsesInterface() {
			if i, ok := gt.Interfaces[iid]; ok {
				ctx.InterfacesUsed = append(ctx.InterfacesUsed, SimplifiedInterface{
					ID: i.ID, Name: i.Name, Description: i.Description, Location: i.Loc,
				})
			}
		}
	}

	if opt.HasResource(domain.ResourceNamedType) {
		for _, ntid := range fn.UsesNamedType() {
			if n, ok := gt.NamedTypes[ntid]; ok {
				ctx.NamedTypesUsed = append(ctx.NamedTypesUsed, SimplifiedNamedType{
					ID: n.ID, Name: n.Name, Description: n.Description, Location: n.Loc,
				})
			}
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		depSet := make(map[DependancyPath]bool)
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

	if opt.HasResource(domain.ResourcePackage) {
		pkgSet := make(map[PackagePath]bool)
		for _, p := range fn.UsesPkg() {
			pkgSet[p] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
				for _, p := range parent.UsesPkg() {
					pkgSet[p] = true
				}
			}
		}
		for p := range pkgSet {
			ctx.ModulesUsed = append(ctx.ModulesUsed, p)
		}
	}

	var blocks []ContextBlock

	blocks = append(blocks, ContextBlock{
		Kind: "function", FileID: ModuleID(fn.Loc.Path), Line: fn.Loc.StartsAt,
		Title: fmt.Sprintf("function %s", fn.Name), Cut: funcCut.Cut,
	})

	if ctx.ParentClass != nil {
		blocks = append(blocks, ContextBlock{
			Kind: "parent_class", FileID: ModuleID(ctx.ParentClass.Loc.Path),
			Line:  ctx.ParentClass.Loc.StartsAt,
			Title: fmt.Sprintf("class %s", ctx.ParentClass.Name),
			Cut:   ctx.ParentClass.Cut,
		})
	}

	for _, cf := range ctx.CalledFunctions {
		blocks = append(blocks, ContextBlock{
			Kind: "called_func", FileID: ModuleID(cf.Location.Path),
			Line: cf.Location.StartsAt, Title: fmt.Sprintf("function %s", cf.Name),
		})
	}

	for _, cu := range ctx.ClassesUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "class", FileID: ModuleID(cu.Location.Path),
			Line: cu.Location.StartsAt, Title: fmt.Sprintf("class %s", cu.Name),
		})
		for _, sm := range cu.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "class_method", FileID: ModuleID(sm.Location.Path),
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", cu.Name, sm.Name),
			})
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: ModuleID(ev.Location.Path),
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}

	ctx.Blocks = sortBlocks(blocks)

	return ctx, nil
}

func (m *JavaScriptManager) ReadClass(id string, opts ...topology.TopologyOption) (*JavaScriptClassContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	classID := ClassID(id)
	c, ok := gt.Classes[classID]
	if !ok {
		return nil, fmt.Errorf("class %s not found in topology", id)
	}

	ctx := &JavaScriptClassContext{}

	classCut, err := m.generic.Cut(c.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Class = &ClassCut{JavaScriptClass: c, Cut: classCut.Cut}

	actualMethodIDs := collectMethodIDs(gt, classID)

	var constructorFunc *JavaScriptFunction
	if opt.HasResource(domain.ResourceFunction) && c.Constructor != nil {
		if cfn, ok := gt.Functions[*c.Constructor]; ok {
			constructorFunc = &cfn
			cCut, err := m.generic.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &FunctionCut{JavaScriptFunction: cfn, Cut: cCut.Cut}
		}
	}

	constructorClassRefs := make(map[ClassID]bool)
	constructorExtVarRefs := make(map[ExternalVarID]bool)
	constructorDepRefs := make(map[DependancyPath]bool)
	constructorPkgRefs := make(map[PackagePath]bool)
	if constructorFunc != nil {
		for _, cid := range constructorFunc.UsesClass() {
			constructorClassRefs[cid] = true
		}
		for _, evid := range constructorFunc.UsesExtVar() {
			constructorExtVarRefs[evid] = true
		}
		for _, d := range constructorFunc.UsesDep() {
			constructorDepRefs[d] = true
		}
		for _, p := range constructorFunc.UsesPkg() {
			constructorPkgRefs[p] = true
		}
	}

	for _, baseClassID := range c.Inherits() {
		base, ok := gt.Classes[baseClassID]
		if !ok {
			continue
		}
		ctx.BaseClasses = append(ctx.BaseClasses, SimplifiedClass{
			ID:          base.ID,
			Name:        base.Name,
			Description: base.Description,
			Location:    base.Loc,
		})
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, methodID := range actualMethodIDs {
			if fn, ok := gt.Functions[methodID]; ok {
				ctx.Methods = append(ctx.Methods, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceType) && constructorFunc != nil {
		for refClassID := range constructorClassRefs {
			cu, ok := gt.Classes[refClassID]
			if !ok {
				continue
			}
			usage := ClassUsage{
				ID:          cu.ID,
				Name:        cu.Name,
				Description: cu.Description,
				Location:    cu.Loc,
			}
			for _, calledID := range constructorFunc.Calls() {
				called, ok := gt.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == refClassID {
					usage.Methods = append(usage.Methods, simplifyFunction(called))
				}
			}
			ctx.ClassesUsed = append(ctx.ClassesUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for refExtVarID := range constructorExtVarRefs {
			if v, ok := gt.ExternalVars[refExtVarID]; ok {
				ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, simplifyExtVar(v))
			}
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		depSet := make(map[DependancyPath]bool)
		for _, d := range c.UsesDep() {
			depSet[d] = true
		}
		for d := range constructorDepRefs {
			depSet[d] = true
		}
		for d := range depSet {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	if opt.HasResource(domain.ResourcePackage) {
		pkgSet := make(map[PackagePath]bool)
		for _, p := range c.UsesPkg() {
			pkgSet[p] = true
		}
		for p := range constructorPkgRefs {
			pkgSet[p] = true
		}
		for p := range pkgSet {
			ctx.ModulesUsed = append(ctx.ModulesUsed, p)
		}
	}

	var blocks []ContextBlock

	blocks = append(blocks, ContextBlock{
		Kind: "class", FileID: ModuleID(c.Loc.Path), Line: c.Loc.StartsAt,
		Title: fmt.Sprintf("class %s", c.Name), Cut: classCut.Cut,
	})

	if ctx.Constructor != nil {
		blocks = append(blocks, ContextBlock{
			Kind: "constructor", FileID: ModuleID(ctx.Constructor.Loc.Path),
			Line:  ctx.Constructor.Loc.StartsAt,
			Title: fmt.Sprintf("%s.constructor", c.Name),
			Cut:   ctx.Constructor.Cut,
		})
	}

	for _, base := range ctx.BaseClasses {
		blocks = append(blocks, ContextBlock{
			Kind: "base_class", FileID: ModuleID(base.Location.Path),
			Line:  base.Location.StartsAt,
			Title: fmt.Sprintf("class %s", base.Name),
		})
	}

	for _, mm := range ctx.Methods {
		blocks = append(blocks, ContextBlock{
			Kind: "method", FileID: ModuleID(mm.Location.Path),
			Line: mm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", c.Name, mm.Name),
		})
	}

	for _, cu := range ctx.ClassesUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "class", FileID: ModuleID(cu.Location.Path),
			Line: cu.Location.StartsAt, Title: fmt.Sprintf("class %s", cu.Name),
		})
		for _, sm := range cu.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "class_method", FileID: ModuleID(sm.Location.Path),
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", cu.Name, sm.Name),
			})
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: ModuleID(ev.Location.Path),
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}

	ctx.Blocks = sortBlocks(blocks)

	return ctx, nil
}

func (m *JavaScriptManager) ReadModule(id string, opts ...topology.TopologyOption) (*JavaScriptModuleContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	modID := ModuleID(id)
	mod, ok := gt.Modules[modID]
	if !ok {
		return nil, fmt.Errorf("module %s not found in topology", id)
	}

	ctx := &JavaScriptModuleContext{}
	modCut, err := m.generic.Cut(domain.Location{Path: string(modID)})
	if err != nil {
		return nil, err
	}
	ctx.Module = &ModuleCut{JavaScriptModule: mod, Cut: modCut.Cut}
	ctx.FromPackage = mod.FromPackage

	if opt.HasResource(domain.ResourceFunction) {
		for _, fnID := range mod.Functions() {
			if fn, ok := gt.Functions[fnID]; ok {
				ctx.Functions = append(ctx.Functions, simplifyFunction(fn))
			}
		}
	}

	if opt.HasResource(domain.ResourceType) {
		for _, cID := range mod.Classes() {
			c, ok := gt.Classes[cID]
			if !ok {
				continue
			}
			ctx.Classes = append(ctx.Classes, classUsageWithMethods(gt, c))
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, vID := range mod.ExternalVars() {
			if v, ok := gt.ExternalVars[vID]; ok {
				ctx.ExtVars = append(ctx.ExtVars, simplifyExtVar(v))
			}
		}
	}

	for _, p := range mod.PackagesImported() {
		ctx.Imports = append(ctx.Imports, p)
	}

	var blocks []ContextBlock
	blocks = append(blocks, ContextBlock{
		Kind: "module", FileID: ModuleID(modID), Line: 1,
		Title: fmt.Sprintf("module %s", modID), Cut: modCut.Cut,
	})
	for _, fn := range ctx.Functions {
		blocks = append(blocks, ContextBlock{
			Kind: "function", FileID: ModuleID(fn.Location.Path),
			Line: fn.Location.StartsAt, Title: fmt.Sprintf("function %s", fn.Name),
		})
	}
	for _, c := range ctx.Classes {
		blocks = append(blocks, ContextBlock{
			Kind: "class", FileID: ModuleID(c.Location.Path),
			Line: c.Location.StartsAt, Title: fmt.Sprintf("class %s", c.Name),
		})
		for _, mm := range c.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "method", FileID: ModuleID(mm.Location.Path),
				Line: mm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", c.Name, mm.Name),
			})
		}
	}
	for _, ev := range ctx.ExtVars {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: ModuleID(ev.Location.Path),
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}
	ctx.Blocks = sortBlocks(blocks)

	return ctx, nil
}

func (m *JavaScriptManager) ReadPackage(id string, opts ...topology.TopologyOption) (*JavaScriptPackageContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	pkgPath := PackagePath(id)
	pkg, ok := gt.Packages[pkgPath]
	if !ok {
		return nil, fmt.Errorf("package %s not found in topology", id)
	}

	ctx := &JavaScriptPackageContext{}
	ctx.Package = &PackageCut{JavaScriptPackage: pkg}

	var blocks []ContextBlock

	if opt.HasResource(domain.ResourceFile) {
		for _, fID := range pkg.Files() {
			ctx.Files = append(ctx.Files, fID)
			blocks = append(blocks, ContextBlock{
				Kind: "module", FileID: ModuleID(fID), Line: 1,
				Title: fmt.Sprintf("module %s", fID),
			})
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, fnID := range pkg.HasFunctions() {
			fn, ok := gt.Functions[fnID]
			if !ok {
				continue
			}
			ctx.Functions = append(ctx.Functions, simplifyFunction(fn))
			blocks = append(blocks, ContextBlock{
				Kind: "function", FileID: ModuleID(fn.Loc.Path),
				Line: fn.Loc.StartsAt, Title: fmt.Sprintf("function %s", fn.Name),
			})
		}
	}

	if opt.HasResource(domain.ResourceType) {
		for _, cID := range pkg.HasClasses() {
			c, ok := gt.Classes[cID]
			if !ok {
				continue
			}
			usage := classUsageWithMethods(gt, c)
			ctx.Classes = append(ctx.Classes, usage)
			blocks = append(blocks, ContextBlock{
				Kind: "class", FileID: ModuleID(c.Loc.Path),
				Line: c.Loc.StartsAt, Title: fmt.Sprintf("class %s", c.Name),
			})
			for _, mm := range usage.Methods {
				blocks = append(blocks, ContextBlock{
					Kind: "method", FileID: ModuleID(mm.Location.Path),
					Line: mm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", c.Name, mm.Name),
				})
			}
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, vID := range pkg.HasExternalVars() {
			v, ok := gt.ExternalVars[vID]
			if !ok {
				continue
			}
			ctx.ExtVars = append(ctx.ExtVars, simplifyExtVar(v))
			blocks = append(blocks, ContextBlock{
				Kind: "extvar", FileID: ModuleID(v.Location.Path),
				Line: v.Location.StartsAt, Title: fmt.Sprintf("var %s", v.Name),
			})
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		depSet := make(map[DependancyPath]bool)
		for _, d := range pkg.Connections[ConnImportsDep] {
			depSet[DependancyPath(d)] = true
		}
		for d := range depSet {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	ctx.Blocks = sortBlocks(blocks)

	return ctx, nil
}

func (m *JavaScriptManager) ReadDependency(id string, opts ...topology.TopologyOption) (*JavaScriptDependencyContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	depPath := DependancyPath(id)
	ctx := &JavaScriptDependencyContext{Dependency: depPath}

	found := false
	for _, d := range gt.Dependencies {
		if d.PackagePath == depPath {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("dependency %s not found in topology", id)
	}

	var blocks []ContextBlock

	for fnID, fn := range gt.Functions {
		for _, d := range fn.UsesDep() {
			if d == depPath {
				kind := domain.ResourceFunction
				if fn.MethodFrom != nil {
					kind = domain.ResourceMethod
				}
				ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
					ID:          string(fnID),
					Kind:        kind,
					Name:        fn.Name,
					Description: fn.Description,
					Location:    fn.Loc,
				})
				blocks = append(blocks, ContextBlock{
					Kind: string(kind), FileID: ModuleID(fn.Loc.Path),
					Line: fn.Loc.StartsAt, Title: fn.Name,
				})
				break
			}
		}
	}

	for cID, c := range gt.Classes {
		for _, d := range c.UsesDep() {
			if d == depPath {
				ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
					ID:          string(cID),
					Kind:        domain.ResourceType,
					Name:        c.Name,
					Description: c.Description,
					Location:    c.Loc,
				})
				blocks = append(blocks, ContextBlock{
					Kind: "class", FileID: ModuleID(c.Loc.Path),
					Line: c.Loc.StartsAt, Title: c.Name,
				})
				break
			}
		}
	}

	ctx.Blocks = sortBlocks(blocks)

	return ctx, nil
}

func (m *JavaScriptManager) ReadInterface(id string, opts ...topology.TopologyOption) (*JavaScriptInterfaceContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	iface, ok := gt.Interfaces[InterfaceID(id)]
	if !ok {
		return nil, fmt.Errorf("interface %s not found in topology", id)
	}

	ctx := &JavaScriptInterfaceContext{}
	cut, err := m.generic.Cut(iface.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Interface = &InterfaceCut{JavaScriptInterface: iface, Cut: cut.Cut}

	for _, baseID := range iface.Inherits() {
		if b, ok := gt.Interfaces[baseID]; ok {
			ctx.BaseInterfaces = append(ctx.BaseInterfaces, SimplifiedInterface{
				ID: b.ID, Name: b.Name, Description: b.Description, Location: b.Loc,
			})
		}
	}
	for _, classID := range iface.ImplementedBy() {
		if c, ok := gt.Classes[classID]; ok {
			ctx.Implementations = append(ctx.Implementations, SimplifiedClass{
				ID: c.ID, Name: c.Name, Description: c.Description, Location: c.Loc,
			})
		}
	}

	var blocks []ContextBlock
	blocks = append(blocks, ContextBlock{
		Kind: "interface", FileID: ModuleID(iface.Loc.Path), Line: iface.Loc.StartsAt,
		Title: fmt.Sprintf("interface %s", iface.Name), Cut: cut.Cut,
	})
	for _, b := range ctx.BaseInterfaces {
		blocks = append(blocks, ContextBlock{
			Kind: "base_interface", FileID: ModuleID(b.Location.Path), Line: b.Location.StartsAt,
			Title: fmt.Sprintf("interface %s", b.Name),
		})
	}
	for _, c := range ctx.Implementations {
		blocks = append(blocks, ContextBlock{
			Kind: "implementor", FileID: ModuleID(c.Location.Path), Line: c.Location.StartsAt,
			Title: fmt.Sprintf("class %s", c.Name),
		})
	}
	ctx.Blocks = sortBlocks(blocks)
	return ctx, nil
}

func (m *JavaScriptManager) ReadNamedType(id string, opts ...topology.TopologyOption) (*JavaScriptNamedTypeContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	nt, ok := gt.NamedTypes[NamedTypeID(id)]
	if !ok {
		return nil, fmt.Errorf("named type %s not found in topology", id)
	}

	ctx := &JavaScriptNamedTypeContext{}
	cut, err := m.generic.Cut(nt.Loc)
	if err != nil {
		return nil, err
	}
	ctx.NamedType = &NamedTypeCut{JavaScriptNamedType: nt, Cut: cut.Cut}

	var blocks []ContextBlock
	title := fmt.Sprintf("type %s", nt.Name)
	if nt.Kind == "enum" {
		title = fmt.Sprintf("enum %s", nt.Name)
	}
	blocks = append(blocks, ContextBlock{
		Kind: "named_type", FileID: ModuleID(nt.Loc.Path), Line: nt.Loc.StartsAt,
		Title: title, Cut: cut.Cut,
	})

	addUsage := func(resID string, kind domain.ResourceKind, name, desc string, loc domain.Location) {
		ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
			ID: resID, Kind: kind, Name: name, Description: desc, Location: loc,
		})
		blocks = append(blocks, ContextBlock{
			Kind: string(kind), FileID: ModuleID(loc.Path), Line: loc.StartsAt, Title: name,
		})
	}
	for fid, fn := range gt.Functions {
		for _, ntid := range fn.UsesNamedType() {
			if ntid == id {
				kind := domain.ResourceFunction
				if fn.MethodFrom != nil {
					kind = domain.ResourceMethod
				}
				addUsage(string(fid), kind, fn.Name, fn.Description, fn.Loc)
				break
			}
		}
	}
	for cid, c := range gt.Classes {
		for _, ntid := range c.UsesNamedType() {
			if ntid == id {
				addUsage(string(cid), domain.ResourceType, c.Name, c.Description, c.Loc)
				break
			}
		}
	}
	ctx.Blocks = sortBlocks(blocks)
	return ctx, nil
}

func (m *JavaScriptManager) FindFunctionsByName(name string) ([]FunctionID, error) {
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

func (m *JavaScriptManager) FindClassesByName(name string) ([]ClassID, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)
	var results []ClassID
	for id, c := range gt.Classes {
		if c.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

func (m *JavaScriptManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

func (m *JavaScriptManager) ReadResourceAndCut(id string, kind domain.ResourceKind) (*domain.CodeEntry, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}

	var loc domain.Location
	found := false

	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		gt := FromGeneric(topo)
		for _, fn := range gt.Functions {
			if string(fn.ID) == id {
				loc = fn.Loc
				found = true
				break
			}
		}
	case domain.ResourceType:
		gt := FromGeneric(topo)
		for _, c := range gt.Classes {
			if string(c.ID) == id {
				loc = c.Loc
				found = true
				break
			}
		}
	case domain.ResourceVariable:
		gt := FromGeneric(topo)
		for _, v := range gt.ExternalVars {
			if string(v.ID) == id {
				loc = v.Location
				found = true
				break
			}
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

func simplifyFunction(fn JavaScriptFunction) SimplifiedFunction {
	return SimplifiedFunction{
		ID:          fn.ID,
		Name:        fn.Name,
		Description: fn.Description,
		Input:       fn.Input,
		Output:      fn.Output,
		Location:    fn.Loc,
	}
}

func simplifyExtVar(v JavaScriptExternalVar) SimplifiedExtVar {
	const maxValueLen = 500
	sv := SimplifiedExtVar{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		Location:    v.Location,
	}
	if v.Value != nil {
		b, _ := json.Marshal(*v.Value)
		valStr := string(b)
		if len(valStr) > maxValueLen {
			valStr = valStr[:maxValueLen] + "..."
		}
		sv.Value = valStr
	}
	return sv
}

func classUsageWithMethods(gt *JavaScriptTopology, c JavaScriptClass) ClassUsage {
	usage := ClassUsage{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Location:    c.Loc,
	}
	for _, mID := range c.Methods() {
		if method, ok := gt.Functions[mID]; ok {
			usage.Methods = append(usage.Methods, simplifyFunction(method))
		}
	}
	return usage
}

func sortBlocks(blocks []ContextBlock) []ContextBlock {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})
	return blocks
}

func collectMethodIDs(gt *JavaScriptTopology, classID ClassID) []FunctionID {
	var ids []FunctionID
	for id, f := range gt.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == classID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

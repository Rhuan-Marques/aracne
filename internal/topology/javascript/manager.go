package javascript

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Manager wrapping a generic topology engine to handle JavaScript/TypeScript analysis and resource queries.
type JavaScriptManager struct {
	generic *topology.TopologyManager
}

// Creates a new JavaScriptManager wrapping a generic topology manager.
func NewJavaScriptManager(mgr *topology.TopologyManager) *JavaScriptManager {
	return &JavaScriptManager{generic: mgr}
}

// Returns the underlying generic topology manager for the JavaScript domain.
func (m *JavaScriptManager) Generic() *topology.TopologyManager {
	return m.generic
}

// Retrieves a JavaScript function and its context including parent class, called functions, used classes, interfaces, and dependencies.
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

	if opt.HasResource(domain.ResourceStruct) && fn.MethodFrom != nil {
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			// Past the ceiling the enclosing type stops riding along with the member and
			// becomes an ordinary neighbour instead. For Python/JS/Java the cut IS the whole
			// class, so inlining it unconditionally meant reading one method returned the
			// entire class.
			if opt.ContextFilter().InlineParent(parent.Loc.EndsAt - parent.Loc.StartsAt + 1) {
				ctx.ParentClass = &ClassCut{JavaScriptClass: parent, Cut: parentCut.Cut}
			} else {
				ctx.OversizedParent = &ClassCut{JavaScriptClass: parent, Cut: parentCut.Cut}
			}
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

	if opt.HasResource(domain.ResourceStruct) {
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

	m.filterFunctionContext(gt, ctx, opt.ContextFilter(), fn.ID)

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

// Retrieves a JavaScript class and its full context including constructor, methods, base classes, and dependencies.
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

	if opt.HasResource(domain.ResourceStruct) && constructorFunc != nil {
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

	m.filterClassContext(gt, ctx, opt.ContextFilter(), c.ID)

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

// Retrieves a JavaScript module and its contents including functions, classes, variables, and imports.
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

	if opt.HasResource(domain.ResourceStruct) {
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

	for _, target := range mod.ModulesImported() {
		ctx.Imports = append(ctx.Imports, target)
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

// Retrieves a dependency and lists which functions and classes use it.
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
					Kind:        domain.ResourceStruct,
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

// Retrieves a JavaScript interface and its context including base interfaces and implementing classes.
func (m *JavaScriptManager) ReadInterface(id string, opts ...topology.TopologyOption) (*JavaScriptInterfaceContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

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

	m.filterInterfaceContext(gt, ctx, opt.ContextFilter(), iface.ID)

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

// Retrieves a named type (type or enum) and all functions/classes that use it, organized as context blocks.
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
				addUsage(string(cid), domain.ResourceStruct, c.Name, c.Description, c.Loc)
				break
			}
		}
	}
	ctx.Blocks = sortBlocks(blocks)
	return ctx, nil
}

// Searches for all functions with a matching name in the JavaScript topology.
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

// Searches for all classes with a matching name in the JavaScript topology.
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

// Updates a resource's description in the topology database.
func (m *JavaScriptManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

// Converts JavaScriptFunction to SimplifiedFunction for API output.
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

// Converts JavaScriptExternalVar to SimplifiedExtVar, truncating values over 500 characters.
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

// Builds a ClassUsage from a JavaScriptClass, including its name, location, and simplified method signatures.
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

// Sorts context blocks by file ID, then line number.
func sortBlocks(blocks []ContextBlock) []ContextBlock {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})
	return blocks
}

// Collects all method IDs belonging to a class, sorted alphabetically
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

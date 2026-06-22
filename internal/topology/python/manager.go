package python

import (
	"encoding/json"
	"fmt"
	"sort"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// Manages Python topology analysis by wrapping a generic topology manager.
type PythonManager struct {
	generic *topology.TopologyManager
}

// Creates and returns a new PythonManager wrapping a TopologyManager.
func NewPythonManager(mgr *topology.TopologyManager) *PythonManager {
	return &PythonManager{generic: mgr}
}

// Get the underlying generic TopologyManager instance.
func (m *PythonManager) Generic() *topology.TopologyManager {
	return m.generic
}

// Retrieves a Python function with its context including parent class, called functions, used classes, external variables, dependencies, and modules, organized into context blocks.
func (m *PythonManager) ReadFunction(id string, opts ...topology.TopologyOption) (*PythonFunctionContext, error) {
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

	ctx := &PythonFunctionContext{}

	funcCut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &FunctionCut{PythonFunction: fn, Cut: funcCut.Cut}

	if opt.HasResource(domain.ResourceType) && fn.MethodFrom != nil {
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			ctx.ParentClass = &ClassCut{PythonClass: parent, Cut: parentCut.Cut}
		}
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, calledID := range fn.Calls() {
			called, ok := gt.Functions[calledID]
			if !ok || called.MethodFrom != nil {
				continue
			}
			ctx.CalledFunctions = append(ctx.CalledFunctions, SimplifiedFunction{
				ID:          called.ID,
				Name:        called.Name,
				Description: called.Description,
				Input:       called.Input,
				Output:      called.Output,
				Location:    called.Loc,
			})
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
					usage.Methods = append(usage.Methods, SimplifiedFunction{
						ID:          called.ID,
						Name:        called.Name,
						Description: called.Description,
						Input:       called.Input,
						Output:      called.Output,
						Location:    called.Loc,
					})
				}
			}
			ctx.ClassesUsed = append(ctx.ClassesUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		const maxValueLen = 500
		for _, varID := range fn.UsesExtVar() {
			v, ok := gt.ExternalVars[varID]
			if !ok {
				continue
			}
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
			ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, sv)
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
		Title: fmt.Sprintf("def %s", fn.Name), Cut: funcCut.Cut,
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
			Line: cf.Location.StartsAt, Title: fmt.Sprintf("def %s", cf.Name),
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

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})

	ctx.Blocks = blocks

	return ctx, nil
}

// Read a Python class with its context including methods, base classes, dependencies, and constructor details.
func (m *PythonManager) ReadClass(id string, opts ...topology.TopologyOption) (*PythonClassContext, error) {
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

	ctx := &PythonClassContext{}

	classCut, err := m.generic.Cut(c.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Class = &ClassCut{PythonClass: c, Cut: classCut.Cut}

	actualMethodIDs := collectMethodIDs(gt, classID)

	var constructorFunc *PythonFunction
	if opt.HasResource(domain.ResourceFunction) && c.Constructor != nil {
		if cfn, ok := gt.Functions[*c.Constructor]; ok {
			constructorFunc = &cfn
			cCut, err := m.generic.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &FunctionCut{PythonFunction: cfn, Cut: cCut.Cut}
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

		needToImplement := false
		var missingMethods []string
		if base.IsABC && base.HasAbstractMethods {
			abstractMethodNames := make(map[string]bool)
			for _, mid := range base.Methods() {
				if bfn, ok := gt.Functions[mid]; ok {
					for _, d := range bfn.Decorators {
						if d == "abstractmethod" || d == "abc.abstractmethod" {
							abstractMethodNames[bfn.Name] = true
							break
						}
					}
				}
			}
			for amName := range abstractMethodNames {
				found := false
				for _, actualID := range actualMethodIDs {
					if actualFn, ok := gt.Functions[actualID]; ok && actualFn.Name == amName {
						found = true
						break
					}
				}
				if !found {
					needToImplement = true
					missingMethods = append(missingMethods, amName)
				}
			}
		}

		if base.IsProtocol && base.HasAbstractMethods {
			protoMethodNames := make(map[string]bool)
			for _, mid := range base.Methods() {
				if pfn, ok := gt.Functions[mid]; ok {
					protoMethodNames[pfn.Name] = true
				}
			}
			for pmName := range protoMethodNames {
				found := false
				for _, actualID := range actualMethodIDs {
					if actualFn, ok := gt.Functions[actualID]; ok && actualFn.Name == pmName {
						found = true
						break
					}
				}
				if !found {
					needToImplement = true
					missingMethods = append(missingMethods, pmName)
				}
			}
		}

		ctx.BaseClasses = append(ctx.BaseClasses, SimplifiedClass{
			ID:                     base.ID,
			Name:                   base.Name,
			Description:            base.Description,
			Location:               base.Loc,
			NeedToImplement:        needToImplement,
			NeedToImplementMethods: missingMethods,
		})
	}

	if opt.HasResource(domain.ResourceFunction) {
		for _, methodID := range actualMethodIDs {
			if fn, ok := gt.Functions[methodID]; ok {
				ctx.Methods = append(ctx.Methods, SimplifiedFunction{
					ID:          fn.ID,
					Name:        fn.Name,
					Description: fn.Description,
					Input:       fn.Input,
					Output:      fn.Output,
					Location:    fn.Loc,
				})
			}
		}
	}

	if opt.HasResource(domain.ResourceType) {
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
					usage.Methods = append(usage.Methods, SimplifiedFunction{
						ID:          called.ID,
						Name:        called.Name,
						Description: called.Description,
						Input:       called.Input,
						Output:      called.Output,
						Location:    called.Loc,
					})
				}
			}
			ctx.ClassesUsed = append(ctx.ClassesUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		const maxValueLen = 500
		for refExtVarID := range constructorExtVarRefs {
			v, ok := gt.ExternalVars[refExtVarID]
			if !ok {
				continue
			}
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
			ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, sv)
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
			Title: fmt.Sprintf("def %s", ctx.Constructor.Name),
			Cut:   ctx.Constructor.Cut,
		})
	}

	for _, base := range ctx.BaseClasses {
		needBadge := ""
		if base.NeedToImplement {
			needBadge = fmt.Sprintf(" [NEED TO IMPLEMENT: %v]", base.NeedToImplementMethods)
		}
		abcFlag := ""
		blocks = append(blocks, ContextBlock{
			Kind: "base_class", FileID: ModuleID(base.Location.Path),
			Line:  base.Location.StartsAt,
			Title: fmt.Sprintf("class %s%s%s", base.Name, abcFlag, needBadge),
		})
	}

	for _, m := range ctx.Methods {
		blocks = append(blocks, ContextBlock{
			Kind: "method", FileID: ModuleID(m.Location.Path),
			Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", c.Name, m.Name),
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

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})

	ctx.Blocks = blocks

	return ctx, nil
}

// Find all functions in the topology by name.
func (m *PythonManager) FindFunctionsByName(name string) ([]FunctionID, error) {
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

// Find all classes in the topology by name.
func (m *PythonManager) FindClassesByName(name string) ([]ClassID, error) {
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

// Updates the description of a Python resource in the topology database by kind and ID.
func (m *PythonManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

// Retrieves a Python module with its functions, classes, external variables, and imports, organized into context blocks sorted by file and line number.
func (m *PythonManager) ReadModule(id string, opts ...topology.TopologyOption) (*PythonModuleContext, error) {
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

	ctx := &PythonModuleContext{}
	modCut, err := m.generic.Cut(domain.Location{Path: string(modID)})
	if err != nil {
		return nil, err
	}
	ctx.Module = &ModuleCut{PythonModule: mod, Cut: modCut.Cut}
	ctx.FromPackage = mod.FromPackage

	if opt.HasResource(domain.ResourceFunction) {
		for _, fnID := range mod.Functions() {
			fn, ok := gt.Functions[fnID]
			if !ok {
				continue
			}
			ctx.Functions = append(ctx.Functions, SimplifiedFunction{
				ID:          fn.ID,
				Name:        fn.Name,
				Description: fn.Description,
				Input:       fn.Input,
				Output:      fn.Output,
				Location:    fn.Loc,
			})
		}
	}

	if opt.HasResource(domain.ResourceType) {
		for _, cID := range mod.Classes() {
			c, ok := gt.Classes[cID]
			if !ok {
				continue
			}
			usage := ClassUsage{
				ID:          c.ID,
				Name:        c.Name,
				Description: c.Description,
				Location:    c.Loc,
			}
			for _, mID := range c.Methods() {
				method, ok := gt.Functions[mID]
				if !ok {
					continue
				}
				usage.Methods = append(usage.Methods, SimplifiedFunction{
					ID:          method.ID,
					Name:        method.Name,
					Description: method.Description,
					Input:       method.Input,
					Output:      method.Output,
					Location:    method.Loc,
				})
			}
			ctx.Classes = append(ctx.Classes, usage)
		}
	}

	if opt.HasResource(domain.ResourceVariable) {
		for _, vID := range mod.ExternalVars() {
			v, ok := gt.ExternalVars[vID]
			if !ok {
				continue
			}
			ctx.ExtVars = append(ctx.ExtVars, SimplifiedExtVar{
				ID:          v.ID,
				Name:        v.Name,
				Description: v.Description,
				Location:    v.Location,
			})
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
			Line: fn.Location.StartsAt, Title: fmt.Sprintf("def %s", fn.Name),
		})
	}
	for _, c := range ctx.Classes {
		blocks = append(blocks, ContextBlock{
			Kind: "class", FileID: ModuleID(c.Location.Path),
			Line: c.Location.StartsAt, Title: fmt.Sprintf("class %s", c.Name),
		})
		for _, m := range c.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "method", FileID: ModuleID(m.Location.Path),
				Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", c.Name, m.Name),
			})
		}
	}
	for _, ev := range ctx.ExtVars {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: ModuleID(ev.Location.Path),
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})
	ctx.Blocks = blocks

	return ctx, nil
}

// Read a dependency with its usage locations across functions and classes.
func (m *PythonManager) ReadDependency(id string, opts ...topology.TopologyOption) (*PythonDependencyContext, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	depPath := DependancyPath(id)
	ctx := &PythonDependencyContext{Dependency: depPath}

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

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})
	ctx.Blocks = blocks

	return ctx, nil
}

// Retrieves a code entry by resource ID and kind, returning the source code cut for functions, types, variables, files, or a placeholder for dependencies.
func (m *PythonManager) ReadResourceAndCut(id string, kind domain.ResourceKind) (*domain.CodeEntry, error) {
	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}

	var loc domain.Location
	found := false

	switch kind {
	case domain.ResourceFunction:
		gt := FromGeneric(topo)
		for _, fn := range gt.Functions {
			if string(fn.ID) == id {
				loc = fn.Loc
				found = true
				break
			}
		}
	case domain.ResourceMethod:
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
		return &domain.CodeEntry{
			Cut: string(kind),
		}, nil
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

// Collects all function IDs that are methods belonging to a given Python class.
func collectMethodIDs(gt *PythonTopology, classID ClassID) []FunctionID {
	var ids []FunctionID
	for id, f := range gt.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == classID {
			ids = append(ids, id)
		}
	}
	return ids
}

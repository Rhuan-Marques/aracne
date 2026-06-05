package golang

import (
	"encoding/json"
	"fmt"
	"sort"

	"ltp/internal/helper"
	"ltp/internal/topology"
	"ltp/internal/topology/domain"
)

// GoManager wraps TopologyManager with Go-specific context enrichment. It exposes ReadFunction/ReadStruct for retrieving functions and structs with interconnected context (called funcs, implemented interfaces, constructor, etc.) and delegates UpdateDescription to the generic manager.
type GoManager struct {
	generic *topology.TopologyManager
}

// Creates a new GoManager wrapping a generic TopologyManager for Go-specific context enrichment operations.
func NewGoManager(mgr *topology.TopologyManager) *GoManager {
	return &GoManager{generic: mgr}
}

// Returns the underlying generic TopologyManager that this GoManager wraps for low-level topology operations.
func (m *GoManager) Generic() *topology.TopologyManager {
	return m.generic
}

// Retrieves a function's full context from the topology by ID, including source cut, parent struct, called functions, struct/interface usage, external variables, dependencies, and packages. Accepts optional TopologyOption filters to limit which resource categories are populated. Returns a GoFunctionContext or an error if the function is not found.
func (m *GoManager) ReadFunction(id string, opts ...topology.TopologyOption) (*GoFunctionContext, error) {
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

	ctx := &GoFunctionContext{}

	funcCut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &FunctionCut{GolangFunction: fn, Cut: funcCut.Cut}

	if opt.HasResource(domain.ResourceType) && fn.MethodFrom != nil {
		if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
			parentCut, err := m.generic.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			ctx.ParentStruct = &StructCut{GolangStruct: parent, Cut: parentCut.Cut}
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
		for _, structID := range fn.UsesStruct() {
			s, ok := gt.Structs[structID]
			if !ok {
				continue
			}
			usage := StructUsage{
				ID:          s.ID,
				Name:        s.Name,
				Description: s.Description,
				Location:    s.Loc,
			}
			for _, calledID := range fn.Calls() {
				called, ok := gt.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == structID {
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
			ctx.StructsUsed = append(ctx.StructsUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for _, ifaceID := range fn.UsesInterface() {
			iface, ok := gt.Interfaces[ifaceID]
			if !ok {
				continue
			}
			usage := InterfaceUsage{
				ID:          iface.ID,
				Name:        iface.Name,
				Description: iface.Description,
				Location:    iface.Loc,
			}
			for _, implStructID := range iface.ImplementedBy() {
				implStruct, ok := gt.Structs[implStructID]
				if !ok {
					continue
				}
				impl := InterfaceImplementation{
					StructID:    implStruct.ID,
					Name:        implStruct.Name,
					Description: implStruct.Description,
					Location:    implStruct.Loc,
				}
				for _, calledID := range fn.Calls() {
					called, ok := gt.Functions[calledID]
					if !ok {
						continue
					}
					if called.MethodFrom != nil && *called.MethodFrom == implStructID {
						impl.Methods = append(impl.Methods, SimplifiedFunction{
							ID:          called.ID,
							Name:        called.Name,
							Description: called.Description,
							Input:       called.Input,
							Output:      called.Output,
							Location:    called.Loc,
						})
					}
				}
				usage.Implementations = append(usage.Implementations, impl)
			}
			ctx.InterfacesUsed = append(ctx.InterfacesUsed, usage)
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

	if opt.HasResource(domain.ResourcePackage) {
		pkgSet := make(map[PackagePath]bool)
		for _, p := range fn.UsesPkg() {
			pkgSet[p] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
				for _, p := range parent.UsesPkg() {
					pkgSet[p] = true
				}
			}
		}
		for p := range pkgSet {
			ctx.PackagesUsed = append(ctx.PackagesUsed, p)
		}
	}

	var blocks []ContextBlock

	blocks = append(blocks, ContextBlock{
		Kind: "function", FileID: FileID(fn.Loc.Path), Line: fn.Loc.StartsAt,
		Title: fmt.Sprintf("func %s", fn.Name), Cut: funcCut.Cut,
	})

	if ctx.ParentStruct != nil {
		blocks = append(blocks, ContextBlock{
			Kind: "parent_struct", FileID: FileID(ctx.ParentStruct.Loc.Path),
			Line:  ctx.ParentStruct.Loc.StartsAt,
			Title: fmt.Sprintf("struct %s", ctx.ParentStruct.Name),
			Cut:   ctx.ParentStruct.Cut,
		})
	}

	for _, cf := range ctx.CalledFunctions {
		blocks = append(blocks, ContextBlock{
			Kind: "called_func", FileID: FileID(cf.Location.Path),
			Line: cf.Location.StartsAt, Title: fmt.Sprintf("func %s", cf.Name),
		})
	}

	for _, su := range ctx.StructsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "struct", FileID: FileID(su.Location.Path),
			Line: su.Location.StartsAt, Title: fmt.Sprintf("struct %s", su.Name),
		})
		for _, sm := range su.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "struct_method", FileID: FileID(sm.Location.Path),
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", su.Name, sm.Name),
			})
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "interface", FileID: FileID(iu.Location.Path),
			Line: iu.Location.StartsAt, Title: fmt.Sprintf("interface %s", iu.Name),
		})
		for _, impl := range iu.Implementations {
			blocks = append(blocks, ContextBlock{
				Kind: "interface_impl", FileID: FileID(impl.Location.Path),
				Line: impl.Location.StartsAt, Title: fmt.Sprintf("struct %s", impl.Name),
			})
			for _, m := range impl.Methods {
				blocks = append(blocks, ContextBlock{
					Kind: "impl_method", FileID: FileID(m.Location.Path),
					Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", impl.Name, m.Name),
				})
			}
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: FileID(ev.Location.Path),
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

// Reads a struct from the topology by ID, returning a GoStructContext with its source cut, constructor function, implemented interfaces, methods, and all referenced resources (structs, interfaces, vars, packages, deps) sorted into context blocks.
func (m *GoManager) ReadStruct(id string, opts ...topology.TopologyOption) (*GoStructContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	structID := StructID(id)
	s, ok := gt.Structs[structID]
	if !ok {
		return nil, fmt.Errorf("struct %s not found in topology", id)
	}

	ctx := &GoStructContext{}

	structCut, err := m.generic.Cut(s.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Struct = &StructCut{GolangStruct: s, Cut: structCut.Cut}

	actualMethodIDs := collectMethodIDs(gt, structID)

	var constructorFunc *GolangFunction
	if opt.HasResource(domain.ResourceFunction) && s.Constructor != nil {
		if cfn, ok := gt.Functions[*s.Constructor]; ok {
			constructorFunc = &cfn
			cCut, err := m.generic.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &FunctionCut{GolangFunction: cfn, Cut: cCut.Cut}
		}
	}

	constructorStructRefs := make(map[StructID]bool)
	constructorIfaceRefs := make(map[InterfaceID]bool)
	constructorExtVarRefs := make(map[ExternalVarID]bool)
	constructorDepRefs := make(map[DependancyPath]bool)
	constructorPkgRefs := make(map[PackagePath]bool)
	if constructorFunc != nil {
		for _, sid := range constructorFunc.UsesStruct() {
			constructorStructRefs[sid] = true
		}
		for _, iid := range constructorFunc.UsesInterface() {
			constructorIfaceRefs[iid] = true
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

	if opt.HasResource(domain.ResourceInterface) {
		for _, iface := range gt.Interfaces {
			implements := false
			for _, implStructID := range iface.ImplementedBy() {
				if implStructID == structID {
					implements = true
					break
				}
			}
			if !implements {
				continue
			}

			needToImplement := false
			for _, reqMethod := range iface.Methods {
				found := false
				for _, actualID := range actualMethodIDs {
					if actualFn, ok := gt.Functions[actualID]; ok && actualFn.Name == reqMethod.Name {
						found = true
						break
					}
				}
				if !found {
					needToImplement = true
					break
				}
			}

			ctx.Interfaces = append(ctx.Interfaces, SimplifiedInterface{
				ID:              iface.ID,
				Name:            iface.Name,
				Description:     iface.Description,
				Location:        iface.Loc,
				NeedToImplement: needToImplement,
			})
		}
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
		for refStructID := range constructorStructRefs {
			su, ok := gt.Structs[refStructID]
			if !ok {
				continue
			}
			usage := StructUsage{
				ID:          su.ID,
				Name:        su.Name,
				Description: su.Description,
				Location:    su.Loc,
			}
			for _, calledID := range constructorFunc.Calls() {
				called, ok := gt.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == refStructID {
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
			ctx.StructsUsed = append(ctx.StructsUsed, usage)
		}
	}

	if opt.HasResource(domain.ResourceInterface) {
		for refIfaceID := range constructorIfaceRefs {
			iface, ok := gt.Interfaces[refIfaceID]
			if !ok {
				continue
			}
			usage := InterfaceUsage{
				ID:          iface.ID,
				Name:        iface.Name,
				Description: iface.Description,
				Location:    iface.Loc,
			}
			for _, implStructID := range iface.ImplementedBy() {
				implStruct, ok := gt.Structs[implStructID]
				if !ok {
					continue
				}
				impl := InterfaceImplementation{
					StructID:    implStruct.ID,
					Name:        implStruct.Name,
					Description: implStruct.Description,
					Location:    implStruct.Loc,
				}
				for _, calledID := range constructorFunc.Calls() {
					called, ok := gt.Functions[calledID]
					if !ok {
						continue
					}
					if called.MethodFrom != nil && *called.MethodFrom == implStructID {
						impl.Methods = append(impl.Methods, SimplifiedFunction{
							ID:          called.ID,
							Name:        called.Name,
							Description: called.Description,
							Input:       called.Input,
							Output:      called.Output,
							Location:    called.Loc,
						})
					}
				}
				usage.Implementations = append(usage.Implementations, impl)
			}
			ctx.InterfacesUsed = append(ctx.InterfacesUsed, usage)
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
		for _, d := range s.UsesDep() {
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
		for _, p := range s.UsesPkg() {
			pkgSet[p] = true
		}
		for p := range constructorPkgRefs {
			pkgSet[p] = true
		}
		for p := range pkgSet {
			ctx.PackagesUsed = append(ctx.PackagesUsed, p)
		}
	}

	var blocks []ContextBlock

	blocks = append(blocks, ContextBlock{
		Kind: "struct", FileID: FileID(s.Loc.Path), Line: s.Loc.StartsAt,
		Title: fmt.Sprintf("struct %s", s.Name), Cut: structCut.Cut,
	})

	if ctx.Constructor != nil {
		blocks = append(blocks, ContextBlock{
			Kind: "constructor", FileID: FileID(ctx.Constructor.Loc.Path),
			Line:  ctx.Constructor.Loc.StartsAt,
			Title: fmt.Sprintf("func %s", ctx.Constructor.Name),
			Cut:   ctx.Constructor.Cut,
		})
	}

	for _, iface := range ctx.Interfaces {
		blocks = append(blocks, ContextBlock{
			Kind: "interface", FileID: FileID(iface.Location.Path),
			Line: iface.Location.StartsAt, Title: fmt.Sprintf("interface %s", iface.Name),
		})
	}

	for _, m := range ctx.Methods {
		blocks = append(blocks, ContextBlock{
			Kind: "method", FileID: FileID(m.Location.Path),
			Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", s.Name, m.Name),
		})
	}

	for _, su := range ctx.StructsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "struct", FileID: FileID(su.Location.Path),
			Line: su.Location.StartsAt, Title: fmt.Sprintf("struct %s", su.Name),
		})
		for _, sm := range su.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "struct_method", FileID: FileID(sm.Location.Path),
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", su.Name, sm.Name),
			})
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "interface", FileID: FileID(iu.Location.Path),
			Line: iu.Location.StartsAt, Title: fmt.Sprintf("interface %s", iu.Name),
		})
		for _, impl := range iu.Implementations {
			blocks = append(blocks, ContextBlock{
				Kind: "interface_impl", FileID: FileID(impl.Location.Path),
				Line: impl.Location.StartsAt, Title: fmt.Sprintf("struct %s", impl.Name),
			})
			for _, m := range impl.Methods {
				blocks = append(blocks, ContextBlock{
					Kind: "impl_method", FileID: FileID(m.Location.Path),
					Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", impl.Name, m.Name),
				})
			}
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, ContextBlock{
			Kind: "extvar", FileID: FileID(ev.Location.Path),
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

// Reads an interface from the topology by ID, returning a GoInterfaceContext with its source cut and implementing structs with matching methods.
func (m *GoManager) ReadInterface(id string, opts ...topology.TopologyOption) (*GoInterfaceContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	interfaceID := InterfaceID(id)
	iface, ok := gt.Interfaces[interfaceID]
	if !ok {
		return nil, fmt.Errorf("interface %s not found in topology", id)
	}

	ctx := &GoInterfaceContext{}

	interfaceCut, err := m.generic.Cut(iface.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Interface = &InterfaceCut{GolangInterface: iface, Cut: interfaceCut.Cut}

	if opt.HasResource(domain.ResourceType) || opt.HasResource(domain.ResourceFunction) {
		for _, implStructID := range iface.ImplementedBy() {
			implStruct, ok := gt.Structs[implStructID]
			if !ok {
				continue
			}
			impl := InterfaceImplementation{
				StructID:    implStruct.ID,
				Name:        implStruct.Name,
				Description: implStruct.Description,
				Location:    implStruct.Loc,
			}
			if opt.HasResource(domain.ResourceFunction) {
				for _, reqMethod := range iface.Methods {
					for _, methodID := range implStruct.Methods() {
						method, exists := gt.Functions[methodID]
						if !exists || !functionSignatureMatchesDefinition(method, reqMethod) {
							continue
						}
						impl.Methods = append(impl.Methods, SimplifiedFunction{
							ID:          method.ID,
							Name:        method.Name,
							Description: method.Description,
							Input:       method.Input,
							Output:      method.Output,
							Location:    method.Loc,
						})
						break
					}
				}
			}
			ctx.Implementations = append(ctx.Implementations, impl)
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		ctx.Dependencies = append(ctx.Dependencies, iface.UsesDep()...)
	}
	if opt.HasResource(domain.ResourcePackage) {
		ctx.PackagesUsed = append(ctx.PackagesUsed, iface.UsesPkg()...)
	}

	blocks := []ContextBlock{{
		Kind: "interface", FileID: FileID(iface.Loc.Path), Line: iface.Loc.StartsAt,
		Title: fmt.Sprintf("interface %s", iface.Name), Cut: interfaceCut.Cut,
	}}
	for _, impl := range ctx.Implementations {
		blocks = append(blocks, ContextBlock{
			Kind: "interface_impl", FileID: FileID(impl.Location.Path),
			Line: impl.Location.StartsAt, Title: fmt.Sprintf("struct %s", impl.Name),
		})
		for _, method := range impl.Methods {
			blocks = append(blocks, ContextBlock{
				Kind: "impl_method", FileID: FileID(method.Location.Path),
				Line: method.Location.StartsAt, Title: fmt.Sprintf("%s.%s", impl.Name, method.Name),
			})
		}
	}
	sortContextBlocks(blocks)
	ctx.Blocks = blocks

	return ctx, nil
}

// Reads a named type from the topology by ID, returning a GoNamedTypeContext with its source cut and resources that use it.
func (m *GoManager) ReadNamedType(id string, opts ...topology.TopologyOption) (*GoNamedTypeContext, error) {
	opt := &topology.TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := m.generic.ReadAll()
	if err != nil {
		return nil, err
	}
	gt := FromGeneric(topo)

	namedTypeID := NamedTypeID(id)
	namedType, ok := gt.NamedTypes[namedTypeID]
	if !ok {
		return nil, fmt.Errorf("named type %s not found in topology", id)
	}

	ctx := &GoNamedTypeContext{}

	namedTypeCut, err := m.generic.Cut(namedType.Loc)
	if err != nil {
		return nil, err
	}
	ctx.NamedType = &NamedTypeCut{GolangNamedType: namedType, Cut: namedTypeCut.Cut}

	if opt.HasResource(domain.ResourceFunction) {
		for _, fn := range gt.Functions {
			if usesNamedType(fn.UsesNamedType(), namedTypeID) {
				kind := domain.ResourceFunction
				if fn.MethodFrom != nil {
					kind = domain.ResourceMethod
				}
				ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
					ID:          string(fn.ID),
					Kind:        kind,
					Name:        fn.Name,
					Description: fn.Description,
					Location:    fn.Loc,
				})
			}
		}
	}
	if opt.HasResource(domain.ResourceType) {
		for _, s := range gt.Structs {
			if usesNamedType(s.UsesNamedType(), namedTypeID) {
				ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
					ID:          string(s.ID),
					Kind:        domain.ResourceType,
					Name:        s.Name,
					Description: s.Description,
					Location:    s.Loc,
				})
			}
		}
	}
	if opt.HasResource(domain.ResourceInterface) {
		for _, iface := range gt.Interfaces {
			if usesNamedType(iface.UsesNamedType(), namedTypeID) {
				ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
					ID:          string(iface.ID),
					Kind:        domain.ResourceInterface,
					Name:        iface.Name,
					Description: iface.Description,
					Location:    iface.Loc,
				})
			}
		}
	}
	if opt.HasResource(domain.ResourceNamedType) {
		for _, nt := range gt.NamedTypes {
			if nt.ID == namedTypeID || !usesNamedType(nt.UsesNamedType(), namedTypeID) {
				continue
			}
			ctx.UsedBy = append(ctx.UsedBy, ResourceUsage{
				ID:          string(nt.ID),
				Kind:        domain.ResourceNamedType,
				Name:        nt.Name,
				Description: nt.Description,
				Location:    nt.Loc,
			})
		}
	}

	if opt.HasResource(domain.ResourceDependency) {
		ctx.Dependencies = append(ctx.Dependencies, namedType.UsesDep()...)
	}
	if opt.HasResource(domain.ResourcePackage) {
		ctx.PackagesUsed = append(ctx.PackagesUsed, namedType.UsesPkg()...)
	}

	sort.SliceStable(ctx.UsedBy, func(i, j int) bool {
		if ctx.UsedBy[i].Location.Path != ctx.UsedBy[j].Location.Path {
			return ctx.UsedBy[i].Location.Path < ctx.UsedBy[j].Location.Path
		}
		if ctx.UsedBy[i].Location.StartsAt != ctx.UsedBy[j].Location.StartsAt {
			return ctx.UsedBy[i].Location.StartsAt < ctx.UsedBy[j].Location.StartsAt
		}
		return ctx.UsedBy[i].ID < ctx.UsedBy[j].ID
	})

	blocks := []ContextBlock{{
		Kind: "named_type", FileID: FileID(namedType.Loc.Path), Line: namedType.Loc.StartsAt,
		Title: fmt.Sprintf("type %s", namedType.Name), Cut: namedTypeCut.Cut,
	}}
	for _, usage := range ctx.UsedBy {
		blocks = append(blocks, ContextBlock{
			Kind: string(usage.Kind), FileID: FileID(usage.Location.Path),
			Line: usage.Location.StartsAt, Title: usage.ID,
		})
	}
	sortContextBlocks(blocks)
	ctx.Blocks = blocks

	return ctx, nil
}

// Searches all functions in the topology by name. Returns a slice of matching FunctionIDs, or an error if reading the topology fails.
func (m *GoManager) FindFunctionsByName(name string) ([]FunctionID, error) {
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

// Searches all structs in the topology by name. Returns a slice of matching StructIDs, or an error if reading the topology fails.
func (m *GoManager) FindStructsByName(name string) ([]StructID, error) {
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

// Updates the description of a resource identified by its ID and kind in the underlying topology database.
func (m *GoManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.generic.DbPath(), kind, id, description)
}

// Reads the source code cut for any resource by its ID and kind (function, method, type, interface, or variable). Returns a CodeEntry containing the source lines and location, or an error if the kind is not supported or the resource is not found.
func (m *GoManager) ReadResourceAndCut(id string, kind domain.ResourceKind) (*domain.CodeEntry, error) {
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
		for _, s := range gt.Structs {
			if string(s.ID) == id {
				loc = s.Loc
				found = true
				break
			}
		}
	case domain.ResourceNamedType:
		gt := FromGeneric(topo)
		for _, n := range gt.NamedTypes {
			if string(n.ID) == id {
				loc = n.Loc
				found = true
				break
			}
		}
	case domain.ResourceInterface:
		gt := FromGeneric(topo)
		for _, iface := range gt.Interfaces {
			if string(iface.ID) == id {
				loc = iface.Loc
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
		return nil, fmt.Errorf("ReadResourceAndCut not supported for File")
	default:
		return nil, fmt.Errorf("unknown resource: %s", kind)
	}

	if !found {
		return nil, fmt.Errorf("resource not found: %s", id)
	}

	return m.generic.Cut(loc)
}

// Collects all function IDs that are methods of the given struct by iterating the topology's function map. Takes a GolangTopology and a StructID, returns a slice of FunctionIDs belonging to that struct.
func collectMethodIDs(gt *GolangTopology, structID StructID) []FunctionID {
	var ids []FunctionID
	for id, f := range gt.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == structID {
			ids = append(ids, id)
		}
	}
	return ids
}

func functionSignatureMatchesDefinition(fn GolangFunction, def FunctionDefinition) bool {
	if fn.Name != def.Name {
		return false
	}
	if len(fn.Input) != len(def.Input) || len(fn.Output) != len(def.Output) {
		return false
	}
	for i := range fn.Input {
		if fn.Input[i].Typing != def.Input[i].Typing {
			return false
		}
	}
	for i := range fn.Output {
		if fn.Output[i].Typing != def.Output[i].Typing {
			return false
		}
	}
	return true
}

func usesNamedType(ids []NamedTypeID, target NamedTypeID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func sortContextBlocks(blocks []ContextBlock) {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})
}

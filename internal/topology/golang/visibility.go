package golang

import (
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// lineSpan returns the inclusive line count of a location, or 0 when unknown.
func lineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

func visHasDesc(s string) bool { return strings.TrimSpace(s) != "" }

// ---------------------------------------------------------------------------
// Full-block builders (imports + optional parent + own cut, no CONTEXT)
// ---------------------------------------------------------------------------

func (m *GoManager) fnFullBlock(gt *GolangTopology, fn GolangFunction) *FullBlock {
	cut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil
	}
	fb := &FullBlock{
		Path:    fn.Loc.Path,
		Cut:     cut.Cut,
		Imports: append([]PackagePath(nil), fn.UsesPkg()...),
		Deps:    append([]DependancyPath(nil), fn.UsesDep()...),
	}
	if fn.MethodFrom != nil {
		if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
			if pc, perr := m.generic.Cut(parent.Loc); perr == nil {
				fb.ParentCut = pc.Cut
			}
			fb.Imports = append(fb.Imports, parent.UsesPkg()...)
			fb.Deps = append(fb.Deps, parent.UsesDep()...)
		}
	}
	return fb
}

func (m *GoManager) structFullBlock(s GolangStruct) *FullBlock {
	cut, err := m.generic.Cut(s.Loc)
	if err != nil {
		return nil
	}
	return &FullBlock{
		Path:    s.Loc.Path,
		Cut:     cut.Cut,
		Imports: append([]PackagePath(nil), s.UsesPkg()...),
		Deps:    append([]DependancyPath(nil), s.UsesDep()...),
	}
}

func (m *GoManager) ifaceFullBlock(iface GolangInterface) *FullBlock {
	cut, err := m.generic.Cut(iface.Loc)
	if err != nil {
		return nil
	}
	return &FullBlock{
		Path:    iface.Loc.Path,
		Cut:     cut.Cut,
		Imports: append([]PackagePath(nil), iface.UsesPkg()...),
		Deps:    append([]DependancyPath(nil), iface.UsesDep()...),
	}
}

func (m *GoManager) extVarFullBlock(v GolangExternalVar) *FullBlock {
	cut, err := m.generic.Cut(v.Location)
	if err != nil {
		return nil
	}
	return &FullBlock{Path: v.Location.Path, Cut: cut.Cut}
}

// ---------------------------------------------------------------------------
// Per-list visibility application
// ---------------------------------------------------------------------------

func (m *GoManager) applyFuncList(gt *GolangTopology, filter domain.ContextFilter, fns []SimplifiedFunction) []SimplifiedFunction {
	var out []SimplifiedFunction
	for _, fn := range fns {
		kind := domain.ResourceFunction
		if gfn, ok := gt.Functions[fn.ID]; ok && gfn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		vis := filter.For(kind, lineSpan(fn.Location), visHasDesc(fn.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		fn.Visibility = vis
		if vis == domain.VisibilityFull {
			if gfn, ok := gt.Functions[fn.ID]; ok {
				fn.Full = m.fnFullBlock(gt, gfn)
			}
		}
		out = append(out, fn)
	}
	return out
}

func (m *GoManager) applyExtVars(gt *GolangTopology, filter domain.ContextFilter, vars []SimplifiedExtVar) []SimplifiedExtVar {
	var out []SimplifiedExtVar
	for _, v := range vars {
		vis := filter.For(domain.ResourceVariable, 0, visHasDesc(v.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		v.Visibility = vis
		if vis == domain.VisibilityFull {
			if gv, ok := gt.ExternalVars[v.ID]; ok {
				v.Full = m.extVarFullBlock(gv)
			}
		}
		out = append(out, v)
	}
	return out
}

func (m *GoManager) applyStructUsages(gt *GolangTopology, filter domain.ContextFilter, structs []StructUsage) []StructUsage {
	var out []StructUsage
	for _, su := range structs {
		su.Methods = m.applyFuncList(gt, filter, su.Methods)
		eff := filter.For(domain.ResourceType, 0, visHasDesc(su.Description))
		for _, mth := range su.Methods {
			eff = eff.Max(mth.Visibility)
		}
		if eff == domain.VisibilityHidden {
			continue
		}
		su.Visibility = eff
		if eff == domain.VisibilityFull {
			if gs, ok := gt.Structs[su.ID]; ok {
				su.Full = m.structFullBlock(gs)
			}
		}
		out = append(out, su)
	}
	return out
}

func (m *GoManager) applyImplList(gt *GolangTopology, filter domain.ContextFilter, impls []InterfaceImplementation) []InterfaceImplementation {
	var out []InterfaceImplementation
	for _, impl := range impls {
		impl.Methods = m.applyFuncList(gt, filter, impl.Methods)
		eff := filter.For(domain.ResourceType, 0, visHasDesc(impl.Description))
		for _, mth := range impl.Methods {
			eff = eff.Max(mth.Visibility)
		}
		if eff == domain.VisibilityHidden {
			continue
		}
		impl.Visibility = eff
		if eff == domain.VisibilityFull {
			if gs, ok := gt.Structs[impl.StructID]; ok {
				impl.Full = m.structFullBlock(gs)
			}
		}
		out = append(out, impl)
	}
	return out
}

func (m *GoManager) applyInterfaceUsages(gt *GolangTopology, filter domain.ContextFilter, ifaces []InterfaceUsage) []InterfaceUsage {
	var out []InterfaceUsage
	for _, iu := range ifaces {
		iu.Implementations = m.applyImplList(gt, filter, iu.Implementations)
		eff := filter.For(domain.ResourceInterface, 0, visHasDesc(iu.Description))
		for _, impl := range iu.Implementations {
			eff = eff.Max(impl.Visibility)
		}
		if eff == domain.VisibilityHidden {
			continue
		}
		iu.Visibility = eff
		if eff == domain.VisibilityFull {
			if gi, ok := gt.Interfaces[iu.ID]; ok {
				iu.Full = m.ifaceFullBlock(gi)
			}
		}
		out = append(out, iu)
	}
	return out
}

// ---------------------------------------------------------------------------
// Incoming (reverse) edges
// ---------------------------------------------------------------------------

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func functionUses(fn GolangFunction, target string) bool {
	return containsID(fn.Calls(), target) ||
		containsID(fn.UsesStruct(), target) ||
		containsID(fn.UsesInterface(), target) ||
		containsID(fn.UsesExtVar(), target) ||
		containsID(fn.UsesNamedType(), target)
}

// incomingRefs scans the topology for resources whose forward connections
// reference targetID (callers/users), for the "# USED BY:" section.
func (m *GoManager) incomingRefs(gt *GolangTopology, targetID string) []domain.ResourceRef {
	var refs []domain.ResourceRef
	for fnID, fn := range gt.Functions {
		if fnID == targetID || !functionUses(fn, targetID) {
			continue
		}
		kind := domain.ResourceFunction
		if fn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		refs = append(refs, domain.ResourceRef{
			ID: fnID, Kind: kind, Name: fn.Name, Description: fn.Description, Location: fn.Loc,
		})
	}
	for sID, s := range gt.Structs {
		if sID == targetID || !containsID(s.UsesNamedType(), targetID) {
			continue
		}
		refs = append(refs, domain.ResourceRef{
			ID: sID, Kind: domain.ResourceType, Name: s.Name, Description: s.Description, Location: s.Loc,
		})
	}
	for ifaceID, iface := range gt.Interfaces {
		if ifaceID == targetID || !containsID(iface.UsesNamedType(), targetID) {
			continue
		}
		refs = append(refs, domain.ResourceRef{
			ID: ifaceID, Kind: domain.ResourceInterface, Name: iface.Name, Description: iface.Description, Location: iface.Loc,
		})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Location.Path != refs[j].Location.Path {
			return refs[i].Location.Path < refs[j].Location.Path
		}
		return refs[i].Location.StartsAt < refs[j].Location.StartsAt
	})
	return refs
}

// ---------------------------------------------------------------------------
// Context entry points (no-op under the default all-Normal filter)
// ---------------------------------------------------------------------------

func (m *GoManager) filterFunctionContext(gt *GolangTopology, ctx *GoFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, ctx.CalledFunctions)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	ctx.InterfacesUsed = m.applyInterfaceUsages(gt, filter, ctx.InterfacesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

func (m *GoManager) filterStructContext(gt *GolangTopology, ctx *GoStructContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	ctx.InterfacesUsed = m.applyInterfaceUsages(gt, filter, ctx.InterfacesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

func (m *GoManager) filterInterfaceContext(gt *GolangTopology, ctx *GoInterfaceContext, filter domain.ContextFilter, targetID string) {
	ctx.Implementations = m.applyImplList(gt, filter, ctx.Implementations)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

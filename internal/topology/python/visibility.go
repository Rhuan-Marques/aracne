package python

import (
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

func lineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

func visHasDesc(s string) bool { return strings.TrimSpace(s) != "" }

// ---------------------------------------------------------------------------
// Full-block builders
// ---------------------------------------------------------------------------

func (m *PythonManager) fnFullBlock(gt *PythonTopology, fn PythonFunction) *FullBlock {
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
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			if pc, perr := m.generic.Cut(parent.Loc); perr == nil {
				fb.ParentCut = pc.Cut
			}
			fb.Imports = append(fb.Imports, parent.UsesPkg()...)
			fb.Deps = append(fb.Deps, parent.UsesDep()...)
		}
	}
	return fb
}

func (m *PythonManager) classFullBlock(c PythonClass) *FullBlock {
	cut, err := m.generic.Cut(c.Loc)
	if err != nil {
		return nil
	}
	return &FullBlock{
		Path:    c.Loc.Path,
		Cut:     cut.Cut,
		Imports: append([]PackagePath(nil), c.UsesPkg()...),
		Deps:    append([]DependancyPath(nil), c.UsesDep()...),
	}
}

func (m *PythonManager) extVarFullBlock(v PythonExternalVar) *FullBlock {
	cut, err := m.generic.Cut(v.Location)
	if err != nil {
		return nil
	}
	return &FullBlock{Path: v.Location.Path, Cut: cut.Cut}
}

// ---------------------------------------------------------------------------
// Per-list visibility application
// ---------------------------------------------------------------------------

func (m *PythonManager) applyFuncList(gt *PythonTopology, filter domain.ContextFilter, fns []SimplifiedFunction) []SimplifiedFunction {
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

func (m *PythonManager) applyExtVars(gt *PythonTopology, filter domain.ContextFilter, vars []SimplifiedExtVar) []SimplifiedExtVar {
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

func (m *PythonManager) applyClassUsages(gt *PythonTopology, filter domain.ContextFilter, classes []ClassUsage) []ClassUsage {
	var out []ClassUsage
	for _, cu := range classes {
		cu.Methods = m.applyFuncList(gt, filter, cu.Methods)
		eff := filter.For(domain.ResourceType, 0, visHasDesc(cu.Description))
		for _, mth := range cu.Methods {
			eff = eff.Max(mth.Visibility)
		}
		if eff == domain.VisibilityHidden {
			continue
		}
		cu.Visibility = eff
		if eff == domain.VisibilityFull {
			if gc, ok := gt.Classes[cu.ID]; ok {
				cu.Full = m.classFullBlock(gc)
			}
		}
		out = append(out, cu)
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

func functionUses(fn PythonFunction, target string) bool {
	return containsID(fn.Calls(), target) ||
		containsID(fn.UsesClass(), target) ||
		containsID(fn.UsesExtVar(), target)
}

func (m *PythonManager) incomingRefs(gt *PythonTopology, targetID string) []domain.ResourceRef {
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
	for cID, c := range gt.Classes {
		if cID == targetID || !containsID(c.UsesClass(), targetID) {
			continue
		}
		refs = append(refs, domain.ResourceRef{
			ID: cID, Kind: domain.ResourceType, Name: c.Name, Description: c.Description, Location: c.Loc,
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

func (m *PythonManager) filterFunctionContext(gt *PythonTopology, ctx *PythonFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, ctx.CalledFunctions)
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, ctx.ClassesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

func (m *PythonManager) filterClassContext(gt *PythonTopology, ctx *PythonClassContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, ctx.ClassesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

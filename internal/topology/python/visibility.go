package python

import (
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// Returns the number of lines spanned by a location, or 0 if the range is invalid.
func lineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

// Checks whether a string is a non-empty description after trimming whitespace.
func visHasDesc(s string) bool { return strings.TrimSpace(s) != "" }

// Constructs a FullBlock for a function with source code, imports, dependencies, and parent class context if applicable.

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

// Builds a FullBlock for a Python class with its source cut, imports, and dependencies.
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

// Builds a FullBlock for an external variable with its source cut and location.
func (m *PythonManager) extVarFullBlock(v PythonExternalVar) *FullBlock {
	cut, err := m.generic.Cut(v.Location)
	if err != nil {
		return nil
	}
	return &FullBlock{Path: v.Location.Path, Cut: cut.Cut}
}

// Filters functions/methods by visibility level and populates full block for visible ones.

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

// Filters external variables by visibility level and populates full block for visible ones.
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

// Filters class usages by visibility, applying method filtering and populating full class blocks for visible classes.
func (m *PythonManager) applyClassUsages(gt *PythonTopology, filter domain.ContextFilter, classes []ClassUsage) []ClassUsage {
	var out []ClassUsage
	for _, cu := range classes {
		cu.Methods = m.applyFuncList(gt, filter, cu.Methods)
		eff := filter.For(domain.ResourceStruct, 0, visHasDesc(cu.Description))
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

// Checks if a target ID exists in a slice of string IDs.

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// dropSelf removes any item whose ID equals self (the resource being read), so
// the resource never appears in its own forward "# CONTEXT:" lists (e.g. a
// recursive function calling itself, or a self-referential class).
func dropSelf[T any](items []T, self string, idOf func(T) string) []T {
	if self == "" {
		return items
	}
	out := items[:0:0]
	for _, it := range items {
		if idOf(it) == self {
			continue
		}
		out = append(out, it)
	}
	return out
}

// Determines if a Python function uses a target resource via calls, class usage, or external variable references.
func functionUses(fn PythonFunction, target string) bool {
	return containsID(fn.Calls(), target) ||
		containsID(fn.UsesClass(), target) ||
		containsID(fn.UsesExtVar(), target)
}

// Collects and sorts all functions and classes that reference a target resource, differentiating between functions and methods.
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
			ID: cID, Kind: domain.ResourceStruct, Name: c.Name, Description: c.Description, Location: c.Loc,
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

// Filters a function context's references (called functions, classes, external variables, and incoming references) based on visibility rules.

func (m *PythonManager) filterFunctionContext(gt *PythonTopology, ctx *PythonFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, dropSelf(ctx.CalledFunctions, targetID, func(f SimplifiedFunction) string { return f.ID }))
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, ctx.ClassesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

// Filters a class context's methods, usages, and external variables by visibility; optionally adds incoming references.
func (m *PythonManager) filterClassContext(gt *PythonTopology, ctx *PythonClassContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, dropSelf(ctx.ClassesUsed, targetID, func(c ClassUsage) string { return c.ID }))
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

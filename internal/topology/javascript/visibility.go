package javascript

import (
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Calculates the line span length of a location, returning 0 if end is before start
func lineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

// Checks whether a string contains non-whitespace characters.
func visHasDesc(s string) bool { return strings.TrimSpace(s) != "" }

// Builds a full source block for a function including parent class info and all dependencies

func (m *JavaScriptManager) fnFullBlock(gt *JavaScriptTopology, fn JavaScriptFunction) *FullBlock {
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

// Extracts a class's source code block with its imports and dependency paths.
func (m *JavaScriptManager) classFullBlock(c JavaScriptClass) *FullBlock {
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

// Extracts an external variable's source code block by location.
func (m *JavaScriptManager) extVarFullBlock(v JavaScriptExternalVar) *FullBlock {
	cut, err := m.generic.Cut(v.Location)
	if err != nil {
		return nil
	}
	return &FullBlock{Path: v.Location.Path, Cut: cut.Cut}
}

// Filters a list of simplified functions by visibility, resolves full blocks for visible-full items, and classifies each as function or method.

func (m *JavaScriptManager) applyFuncList(gt *JavaScriptTopology, filter domain.ContextFilter, fns []SimplifiedFunction) []SimplifiedFunction {
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

// Filters external variables by context visibility and populates full block details when visibility is full.
func (m *JavaScriptManager) applyExtVars(gt *JavaScriptTopology, filter domain.ContextFilter, vars []SimplifiedExtVar) []SimplifiedExtVar {
	var out []SimplifiedExtVar
	for _, v := range vars {
		// A constant whose value is shown IS described -- `MaxRetries = 3` needs no prose, and
		// hiding it under hide_no_description would drop the one thing it had to say.
		vis := filter.For(domain.ResourceVariable, 0, visHasDesc(v.Description) || v.Value != "")
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

// Filters class usages by context visibility, applying method filters and computing aggregate visibility.
func (m *JavaScriptManager) applyClassUsages(gt *JavaScriptTopology, filter domain.ContextFilter, classes []ClassUsage) []ClassUsage {
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

// Checks if a target ID exists in a slice of string IDs

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

// Determines if a function uses a target by checking calls, classes, interfaces, variables, and types
func functionUses(fn JavaScriptFunction, target string) bool {
	return containsID(fn.Calls(), target) ||
		containsID(fn.UsesClass(), target) ||
		containsID(fn.UsesInterface(), target) ||
		containsID(fn.UsesExtVar(), target) ||
		containsID(fn.UsesNamedType(), target)
}

// Checks if a class uses a target class, interface, or named type in its dependencies.
func classUses(c JavaScriptClass, target string) bool {
	return containsID(c.UsesClass(), target) ||
		containsID(c.UsesInterface(), target) ||
		containsID(c.UsesNamedType(), target)
}

// Collects all functions and classes that reference a target, sorted by location
func (m *JavaScriptManager) incomingRefs(gt *JavaScriptTopology, targetID string) []domain.ResourceRef {
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
		if cID == targetID || !classUses(c, targetID) {
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

// Filters a function's context by applying visibility rules to its called functions, used classes, and external variables, and optionally includes incoming references.

func (m *JavaScriptManager) filterFunctionContext(gt *JavaScriptTopology, ctx *JavaScriptFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, dropSelf(ctx.CalledFunctions, targetID, func(f SimplifiedFunction) string { return f.ID }))
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, ctx.ClassesUsed)
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	ctx.InterfacesUsed = keepVisible(filter, domain.ResourceInterface, ctx.InterfacesUsed,
		func(i SimplifiedInterface) string { return i.Description })
	ctx.NamedTypesUsed = keepVisible(filter, domain.ResourceNamedType, ctx.NamedTypesUsed,
		func(n SimplifiedNamedType) string { return n.Description })
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

// keepVisible drops the used interfaces or named types the filter hides. Neither kind ever
// renders as a source cut, so the only decision is shown-or-hidden -- the one Go's function
// context makes for its used interfaces. Without it a TS function's undescribed interface
// printed "## id: no description" under `normal`, which hides every other undescribed neighbour.
func keepVisible[T any](filter domain.ContextFilter, kind domain.ResourceKind, items []T, descOf func(T) string) []T {
	out := items[:0:0]
	for _, it := range items {
		if filter.For(kind, 0, visHasDesc(descOf(it))) != domain.VisibilityHidden {
			out = append(out, it)
		}
	}
	return out
}

// Filters a class's context by applying visibility rules to its methods, used classes, and external variables, and optionally includes incoming references.
func (m *JavaScriptManager) filterClassContext(gt *JavaScriptTopology, ctx *JavaScriptClassContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.ClassesUsed = m.applyClassUsages(gt, filter, dropSelf(ctx.ClassesUsed, targetID, func(c ClassUsage) string { return c.ID }))
	ctx.ExtVarsUsed = m.applyExtVars(gt, filter, ctx.ExtVarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

// Populates incoming references in a class context when the filter requests them
func (m *JavaScriptManager) filterInterfaceContext(gt *JavaScriptTopology, ctx *JavaScriptInterfaceContext, filter domain.ContextFilter, targetID string) {
	if filter.IncludeIncoming {
		ctx.Incoming = m.incomingRefs(gt, targetID)
	}
}

package java

import (
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// simplifyFunction converts a JavaMethod into a lightweight SimplifiedFunction
// for context lists.
func simplifyFunction(fn JavaMethod) SimplifiedFunction {
	return SimplifiedFunction{
		ID:          fn.ID,
		Name:        fn.Name,
		Description: fn.Description,
		Input:       fn.Input,
		Output:      fn.Output,
		Location:    fn.Loc,
	}
}

// simplifyStruct converts a JavaClass into a lightweight SimplifiedStruct.
func simplifyStruct(s JavaClass) SimplifiedStruct {
	return SimplifiedStruct{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Location:    s.Loc,
	}
}

// simplifyInterface converts a JavaInterface into a lightweight
// SimplifiedInterface.
func simplifyInterface(t JavaInterface) SimplifiedInterface {
	return SimplifiedInterface{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Location:    t.Loc,
	}
}

// structUsageWithMethods builds a StructUsage from a JavaClass, including its
// simplified method signatures.
func structUsageWithMethods(gt *JavaTopology, s JavaClass) StructUsage {
	usage := StructUsage{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Location:    s.Loc,
	}
	for _, mID := range s.Methods() {
		if method, ok := gt.Methods[mID]; ok {
			usage.Methods = append(usage.Methods, simplifyFunction(method))
		}
	}
	return usage
}

// collectMethodIDs collects all method/constructor IDs whose MethodFrom points
// at the given class, sorted alphabetically.
func collectMethodIDs(gt *JavaTopology, structID StructID) []FunctionID {
	var ids []FunctionID
	for id, f := range gt.Methods {
		if f.MethodFrom != nil && *f.MethodFrom == structID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// usersOfDependency collects all methods and classes that use the given external
// dependency, sorted by location.
func usersOfDependency(gt *JavaTopology, depPath DependencyPath) []ResourceUsage {
	var out []ResourceUsage
	for fid, fn := range gt.Methods {
		for _, d := range fn.UsesDep() {
			if d == depPath {
				kind := domain.ResourceFunction
				if fn.MethodFrom != nil {
					kind = domain.ResourceMethod
				}
				out = append(out, ResourceUsage{
					ID: string(fid), Kind: kind, Name: fn.Name, Description: fn.Description, Location: fn.Loc,
				})
				break
			}
		}
	}
	for sid, s := range gt.Classes {
		for _, d := range s.UsesDep() {
			if d == depPath {
				out = append(out, ResourceUsage{
					ID: string(sid), Kind: domain.ResourceStruct, Name: s.Name, Description: s.Description, Location: s.Loc,
				})
				break
			}
		}
	}
	sortUsages(out)
	return out
}

// sortUsages orders resource usages by file path, then start line, for stable
// output.
func sortUsages(refs []ResourceUsage) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Location.Path != refs[j].Location.Path {
			return refs[i].Location.Path < refs[j].Location.Path
		}
		return refs[i].Location.StartsAt < refs[j].Location.StartsAt
	})
}

// lineSpan returns the inclusive line count of a location, or 0 when unknown.
func lineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

// visHasDesc reports whether a string carries non-whitespace content.
func visHasDesc(s string) bool { return strings.TrimSpace(s) != "" }

// fnFullBlock builds the full source block for a method: its cut and dependencies.
//
// The enclosing class is attached as ParentCut only when it does not already contain the
// member. A Java class cut is the whole class, so attaching it would print the member twice
// and pull an entire class into the context for one small method -- the cost
// max_inline_parent_lines exists to cap.
func (m *JavaManager) fnFullBlock(gt *JavaTopology, fn JavaMethod) *FullBlock {
	cut, err := m.generic.Cut(fn.Loc)
	if err != nil {
		return nil
	}
	fb := &FullBlock{
		Path: fn.Loc.Path,
		Cut:  cut.Cut,
		Deps: append([]DependencyPath(nil), fn.UsesDep()...),
	}
	if fn.MethodFrom != nil {
		if parent, ok := gt.Classes[*fn.MethodFrom]; ok {
			if !parent.Loc.Contains(fn.Loc) {
				if pc, perr := m.generic.Cut(parent.Loc); perr == nil {
					fb.ParentCut = pc.Cut
				}
			}
			fb.Deps = append(fb.Deps, parent.UsesDep()...)
		}
	}
	return fb
}

// structFullBlock builds the full source block for a class: its cut and dependencies.
func (m *JavaManager) structFullBlock(s JavaClass) *FullBlock {
	cut, err := m.generic.Cut(s.Loc)
	if err != nil {
		return nil
	}
	return &FullBlock{
		Path: s.Loc.Path,
		Cut:  cut.Cut,
		Deps: append([]DependencyPath(nil), s.UsesDep()...),
	}
}

// applyFuncList applies read.context_filter to a method list: hidden entries are dropped,
// the rest carry their visibility, and full ones their source block.
func (m *JavaManager) applyFuncList(gt *JavaTopology, filter domain.ContextFilter, fns []SimplifiedFunction) []SimplifiedFunction {
	var out []SimplifiedFunction
	for _, fn := range fns {
		kind := domain.ResourceFunction
		if jfn, ok := gt.Methods[fn.ID]; ok && jfn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		vis := filter.For(kind, lineSpan(fn.Location), visHasDesc(fn.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		fn.Visibility = vis
		if vis == domain.VisibilityFull {
			if jfn, ok := gt.Methods[fn.ID]; ok {
				fn.Full = m.fnFullBlock(gt, jfn)
			}
		}
		out = append(out, fn)
	}
	return out
}

// applyStructUsages applies read.context_filter to used classes and their listed methods. A
// class stays when it or any of its methods would render, at the highest of their visibilities.
func (m *JavaManager) applyStructUsages(gt *JavaTopology, filter domain.ContextFilter, structs []StructUsage) []StructUsage {
	var out []StructUsage
	for _, su := range structs {
		su.Methods = m.applyFuncList(gt, filter, su.Methods)
		eff := filter.For(domain.ResourceStruct, 0, visHasDesc(su.Description))
		for _, mth := range su.Methods {
			eff = eff.Max(mth.Visibility)
		}
		if eff == domain.VisibilityHidden {
			continue
		}
		su.Visibility = eff
		if eff == domain.VisibilityFull {
			if s, ok := gt.Classes[su.ID]; ok {
				su.Full = m.structFullBlock(s)
			}
		}
		out = append(out, su)
	}
	return out
}

// applyInterfaceList applies read.context_filter to used interfaces.
func applyInterfaceList(filter domain.ContextFilter, ifaces []SimplifiedInterface) []SimplifiedInterface {
	var out []SimplifiedInterface
	for _, iu := range ifaces {
		vis := filter.For(domain.ResourceInterface, 0, visHasDesc(iu.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		iu.Visibility = vis
		out = append(out, iu)
	}
	return out
}

// containsID reports whether target is one of ids.
func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// incomingRefs scans the topology for the methods and classes whose forward connections
// reference targetID (callers/users), for the "# USED BY:" section.
func incomingRefs(gt *JavaTopology, targetID string) []domain.ResourceRef {
	var refs []domain.ResourceRef
	for fnID, fn := range gt.Methods {
		if fnID == targetID || !(containsID(fn.Calls(), targetID) || containsID(fn.UsesStruct(), targetID) ||
			containsID(fn.UsesInterface(), targetID)) {
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
	for sID, s := range gt.Classes {
		if sID == targetID || !(containsID(s.UsesStruct(), targetID) || containsID(s.UsesInterface(), targetID)) {
			continue
		}
		refs = append(refs, domain.ResourceRef{
			ID: sID, Kind: domain.ResourceStruct, Name: s.Name, Description: s.Description, Location: s.Loc,
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

// filterFunctionContext applies read.context_filter to a method's neighbours (called methods,
// used classes and interfaces) and, when asked, collects its incoming references.
func (m *JavaManager) filterFunctionContext(gt *JavaTopology, ctx *JavaFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, ctx.CalledFunctions)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	ctx.InterfacesUsed = applyInterfaceList(filter, ctx.InterfacesUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

// filterStructContext applies read.context_filter to a class's members and used classes and,
// when asked, collects its incoming references. The relationship lines -- constructor,
// extends, implements, enum variants, record components -- are structure the filter does not
// govern, as a Python base class or a Go struct's interfaces are not.
func (m *JavaManager) filterStructContext(gt *JavaTopology, ctx *JavaStructContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

// filterInterfaceContext collects an interface's incoming references when asked. Its
// supertypes and implementors are relationship lines and always render.
func (m *JavaManager) filterInterfaceContext(gt *JavaTopology, ctx *JavaInterfaceContext, filter domain.ContextFilter, targetID string) {
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

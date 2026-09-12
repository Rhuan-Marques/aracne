package rust

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// simplifyFunction converts a RustFunction into a lightweight SimplifiedFunction
// for context lists.
func simplifyFunction(fn RustFunction) SimplifiedFunction {
	return SimplifiedFunction{
		ID:          fn.ID,
		Name:        fn.Name,
		Description: fn.Description,
		Input:       fn.Input,
		Output:      fn.Output,
		Location:    fn.Loc,
	}
}

// simplifyStruct converts a RustStruct into a lightweight SimplifiedStruct.
func simplifyStruct(s RustStruct) SimplifiedStruct {
	return SimplifiedStruct{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Location:    s.Loc,
	}
}

// simplifyTrait converts a RustTrait into a lightweight SimplifiedTrait.
func simplifyTrait(t RustTrait) SimplifiedTrait {
	return SimplifiedTrait{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Location:    t.Loc,
	}
}

// simplifyNamedType converts a RustNamedType into a lightweight
// SimplifiedNamedType.
func simplifyNamedType(n RustNamedType) SimplifiedNamedType {
	return SimplifiedNamedType{
		ID:          n.ID,
		Name:        n.Name,
		Description: n.Description,
		Location:    n.Loc,
	}
}

// simplifyVariable converts a RustVariable into a lightweight SimplifiedVariable,
// truncating values over 500 characters.
func simplifyVariable(v RustVariable) SimplifiedVariable {
	const maxValueLen = 500
	sv := SimplifiedVariable{
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

// structUsageWithMethods builds a StructUsage from a RustStruct, including its
// simplified impl-method signatures.
func structUsageWithMethods(gt *RustTopology, s RustStruct) StructUsage {
	usage := StructUsage{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Location:    s.Loc,
	}
	for _, mID := range s.Methods() {
		if method, ok := gt.Functions[mID]; ok {
			usage.Methods = append(usage.Methods, simplifyFunction(method))
		}
	}
	return usage
}

// collectMethodIDs collects all method/associated-function IDs whose MethodFrom
// points at the given struct, sorted alphabetically.
func collectMethodIDs(gt *RustTopology, structID StructID) []FunctionID {
	var ids []FunctionID
	for id, f := range gt.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == structID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// usersOfNamedType collects all functions/methods and structs that reference the
// given named-type ID, sorted by location.
func usersOfNamedType(gt *RustTopology, id string) []ResourceUsage {
	var out []ResourceUsage
	for fid, fn := range gt.Functions {
		for _, ntid := range fn.UsesNamedType() {
			if ntid == id {
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
	for sid, s := range gt.Structs {
		for _, ntid := range s.UsesNamedType() {
			if ntid == id {
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

// usersOfDependency collects all functions/methods and structs that use the
// given external crate dependency, sorted by location.
func usersOfDependency(gt *RustTopology, depPath DependencyPath) []ResourceUsage {
	var out []ResourceUsage
	for fid, fn := range gt.Functions {
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
	for sid, s := range gt.Structs {
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

// fnFullBlock builds the full source block for a function: its cut, crate dependencies and,
// for a method, the declaration of the type it belongs to (the receiver's shape, as in Go).
func (m *RustManager) fnFullBlock(gt *RustTopology, fn RustFunction) *FullBlock {
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
		if parent, ok := gt.Structs[*fn.MethodFrom]; ok {
			if pc, perr := m.generic.Cut(parent.Loc); perr == nil {
				fb.ParentCut = pc.Cut
			}
			fb.Deps = append(fb.Deps, parent.UsesDep()...)
		}
	}
	return fb
}

// structFullBlock builds the full source block for a struct/enum/union declaration.
func (m *RustManager) structFullBlock(s RustStruct) *FullBlock {
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

// varFullBlock builds the full source block for a module-level const or static.
func (m *RustManager) varFullBlock(v RustVariable) *FullBlock {
	cut, err := m.generic.Cut(v.Location)
	if err != nil {
		return nil
	}
	return &FullBlock{Path: v.Location.Path, Cut: cut.Cut}
}

// applyFuncList applies read.context_filter to a function list: hidden entries are dropped,
// the rest carry their visibility, and full ones their source block.
func (m *RustManager) applyFuncList(gt *RustTopology, filter domain.ContextFilter, fns []SimplifiedFunction) []SimplifiedFunction {
	var out []SimplifiedFunction
	for _, fn := range fns {
		kind := domain.ResourceFunction
		if rfn, ok := gt.Functions[fn.ID]; ok && rfn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		vis := filter.For(kind, lineSpan(fn.Location), visHasDesc(fn.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		fn.Visibility = vis
		if vis == domain.VisibilityFull {
			if rfn, ok := gt.Functions[fn.ID]; ok {
				fn.Full = m.fnFullBlock(gt, rfn)
			}
		}
		out = append(out, fn)
	}
	return out
}

// applyStructUsages applies read.context_filter to used types and their listed methods. A type
// stays when it or any of its methods would render, at the highest of their visibilities.
func (m *RustManager) applyStructUsages(gt *RustTopology, filter domain.ContextFilter, structs []StructUsage) []StructUsage {
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
			if s, ok := gt.Structs[su.ID]; ok {
				su.Full = m.structFullBlock(s)
			}
		}
		out = append(out, su)
	}
	return out
}

// applyTraitList applies read.context_filter to used traits.
func applyTraitList(filter domain.ContextFilter, traits []SimplifiedTrait) []SimplifiedTrait {
	var out []SimplifiedTrait
	for _, t := range traits {
		vis := filter.For(domain.ResourceInterface, 0, visHasDesc(t.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		t.Visibility = vis
		out = append(out, t)
	}
	return out
}

// applyNamedTypeList applies read.context_filter to used type aliases.
func applyNamedTypeList(filter domain.ContextFilter, types []SimplifiedNamedType) []SimplifiedNamedType {
	var out []SimplifiedNamedType
	for _, n := range types {
		vis := filter.For(domain.ResourceNamedType, 0, visHasDesc(n.Description))
		if vis == domain.VisibilityHidden {
			continue
		}
		n.Visibility = vis
		out = append(out, n)
	}
	return out
}

// applyVars applies read.context_filter to used consts and statics.
func (m *RustManager) applyVars(gt *RustTopology, filter domain.ContextFilter, vars []SimplifiedVariable) []SimplifiedVariable {
	var out []SimplifiedVariable
	for _, v := range vars {
		// A constant whose value is shown IS described -- `MAX = 3` needs no prose, and hiding
		// it under hide_no_description would drop the one thing it had to say.
		vis := filter.For(domain.ResourceVariable, 0, visHasDesc(v.Description) || v.Value != "")
		if vis == domain.VisibilityHidden {
			continue
		}
		v.Visibility = vis
		if vis == domain.VisibilityFull {
			if rv, ok := gt.Variables[v.ID]; ok {
				v.Full = m.varFullBlock(rv)
			}
		}
		out = append(out, v)
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

// incomingRefs scans the topology for the functions and types whose forward connections
// reference targetID (callers/users), for the "# USED BY:" section.
func incomingRefs(gt *RustTopology, targetID string) []domain.ResourceRef {
	var refs []domain.ResourceRef
	for fnID, fn := range gt.Functions {
		if fnID == targetID || !(containsID(fn.Calls(), targetID) || containsID(fn.UsesStruct(), targetID) ||
			containsID(fn.UsesTrait(), targetID) || containsID(fn.UsesNamedType(), targetID) ||
			containsID(fn.UsesVar(), targetID)) {
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
		if sID == targetID || !(containsID(s.UsesStruct(), targetID) || containsID(s.UsesNamedType(), targetID)) {
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

// filterFunctionContext applies read.context_filter to a function's neighbours (called
// functions, used types, traits, aliases and variables) and, when asked, collects its incoming
// references.
func (m *RustManager) filterFunctionContext(gt *RustTopology, ctx *RustFunctionContext, filter domain.ContextFilter, targetID string) {
	ctx.CalledFunctions = m.applyFuncList(gt, filter, ctx.CalledFunctions)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	ctx.TraitsUsed = applyTraitList(filter, ctx.TraitsUsed)
	ctx.NamedTypesUsed = applyNamedTypeList(filter, ctx.NamedTypesUsed)
	ctx.VarsUsed = m.applyVars(gt, filter, ctx.VarsUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

// filterStructContext applies read.context_filter to a type's methods, used types and aliases
// and, when asked, collects its incoming references. The relationship lines -- constructor,
// implemented traits, enum variants -- are structure the filter does not govern, as a Python
// base class or a Go struct's interfaces are not.
func (m *RustManager) filterStructContext(gt *RustTopology, ctx *RustStructContext, filter domain.ContextFilter, targetID string) {
	ctx.Methods = m.applyFuncList(gt, filter, ctx.Methods)
	ctx.StructsUsed = m.applyStructUsages(gt, filter, ctx.StructsUsed)
	ctx.NamedTypesUsed = applyNamedTypeList(filter, ctx.NamedTypesUsed)
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

// filterInterfaceContext collects a trait's incoming references when asked. Its supertraits
// and implementors are relationship lines and always render.
func (m *RustManager) filterInterfaceContext(gt *RustTopology, ctx *RustInterfaceContext, filter domain.ContextFilter, targetID string) {
	if filter.IncludeIncoming {
		ctx.Incoming = incomingRefs(gt, targetID)
	}
}

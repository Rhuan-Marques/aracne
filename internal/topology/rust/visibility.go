package rust

import (
	"encoding/json"
	"sort"

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

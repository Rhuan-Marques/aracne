package java

import (
	"sort"

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

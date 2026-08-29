package domain

import "sort"

// Connection keys are shared vocabulary across every language scanner, so the graph walks
// below need no per-language code. Go says "uses_struct" where Python says "uses_class", but
// both mean "this resource references that type", and a walk that accepts either is correct
// for both.
const (
	connCalls        = "calls"
	connUsesStruct   = "uses_struct"
	connUsesClass    = "uses_class"
	connUsesIface    = "uses_interface"
	connUsesNamedTyp = "uses_named_type"
	connUsesExtVar   = "uses_extvar"

	connHasFunc      = "has_function"
	connHasStruct    = "has_struct"
	connHasClass     = "has_class"
	connHasInterface = "has_interface"
	connHasNamedType = "has_named_type"
	connHasVar       = "has_extvar"
	connMethods      = "methods"
	connConstructor  = "constructor"
)

// outgoingKeys are the edges that mean "this resource references that one". Containment
// ("has_function") and reverse edges ("implemented_by", "inherited_by") are deliberately
// excluded: the first is the body, the second is the USED BY section.
var outgoingKeys = []string{
	connCalls, connUsesStruct, connUsesClass, connUsesIface, connUsesNamedTyp, connUsesExtVar,
}

// containmentKeys are the edges that mean "that resource is declared inside this one".
var containmentKeys = []string{
	connHasFunc, connHasStruct, connHasClass, connHasInterface, connHasNamedType, connHasVar,
	connMethods, connConstructor,
}

// FileMembers returns every resource declared inside the given file, transitively: a file's
// classes plus those classes' methods and constructors.
//
// This is what makes a whole-file read honest about what it covers. The file's source already
// contains all of it, so none of it belongs in the context section -- listing it there was an
// index of the fence directly above, which is the duplication this rework removes.
func FileMembers(topo *Topology, fileID string) []string {
	if topo == nil {
		return nil
	}
	seen := map[string]bool{fileID: true}
	var out []string
	queue := []string{fileID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		res, ok := topo.Resources[cur]
		if !ok {
			continue
		}
		for _, key := range containmentKeys {
			for _, id := range res.Connections[key] {
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, id)
				queue = append(queue, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// OutgoingNeighbors returns the resources that `ids` reference, excluding anything in `ids`
// itself and anything in `exclude`.
//
// Result order is by ID so a read is reproducible; callers that want relevance ordering sort
// afterwards.
func OutgoingNeighbors(topo *Topology, ids []string, exclude map[string]bool) []Resource {
	if topo == nil {
		return nil
	}
	skip := map[string]bool{}
	for _, id := range ids {
		skip[id] = true
	}
	for id := range exclude {
		skip[id] = true
	}

	seen := map[string]bool{}
	var out []Resource
	for _, id := range ids {
		res, ok := topo.Resources[id]
		if !ok {
			continue
		}
		for _, key := range outgoingKeys {
			for _, target := range res.Connections[key] {
				if target == "" || skip[target] || seen[target] {
					continue
				}
				n, ok := topo.Resources[target]
				if !ok {
					continue
				}
				seen[target] = true
				out = append(out, n)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

package scanner

import (
	"llm-topology/internal/topology/domain"
)

// matchStructsToInterfaces is the third analysis phase that discovers struct-
// interface implementation relationships. After all struct methods have been
// populated, it iterates over every interface-struct pair and checks whether
// the struct satisfies the interface's method contract. When a match is found,
// the interface is added to the struct's Implements field and the struct is
// appended to the interface's ImplementedBy list. A struct may implement
// multiple interfaces; only the first match sets the Implements pointer while
// all matches populate the respective ImplementedBy slices.
func matchStructsToInterfaces(topo *domain.Topology) {
	for ifaceID, iface := range topo.Interfaces {
		currentIfaceID := ifaceID
		for structID, str := range topo.Struct {
			if implements(str, iface, topo) {
				if str.Implements == nil {
					str.Implements = &currentIfaceID
				}
				iface.ImplementedBy = append(iface.ImplementedBy, structID)
				topo.Struct[structID] = str
			}
		}
		topo.Interfaces[ifaceID] = iface
	}
}

// implements checks whether a struct satisfies a given interface by verifying
// that every interface method has a corresponding method on the struct with a
// matching name and compatible signature. Embedded interface methods without
// a name (from the original source's embedded interface references) are skipped.
// The comparison uses exact type string equality on both input parameters and
// output return types, which covers the common case while acknowledging that
// type alias and named type equivalences may produce false negatives.
func implements(str domain.Struct, iface domain.Interface, topo *domain.Topology) bool {
	if len(iface.Methods) == 0 {
		return false
	}

	for _, ifaceMethod := range iface.Methods {
		if ifaceMethod.Name == "" {
			continue
		}

		found := false
		for _, mid := range str.Methods {
			method, exists := topo.Functions[mid]
			if !exists || method.Name != ifaceMethod.Name {
				continue
			}
			if signaturesMatch(method, ifaceMethod) {
				found = true
				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}

// signaturesMatch performs an exact structural comparison between a concrete
// method and an interface method definition. It checks that the names match
// and that both the input parameter count/types and output return count/types
// are identical. This approach works correctly for the overwhelming majority
// of Go interface implementations while remaining efficient since it operates
// entirely on pre-parsed type strings without requiring type-checking.
func signaturesMatch(method domain.Function, ifaceMethod domain.FunctionDefinition) bool {
	if method.Name != ifaceMethod.Name {
		return false
	}
	if len(method.Input) != len(ifaceMethod.Input) {
		return false
	}
	if len(method.Output) != len(ifaceMethod.Output) {
		return false
	}

	for i := range method.Input {
		if method.Input[i].Typing != ifaceMethod.Input[i].Typing {
			return false
		}
	}
	for i := range method.Output {
		if method.Output[i].Typing != ifaceMethod.Output[i].Typing {
			return false
		}
	}

	return true
}

package goscanner

import (
	"llm-topology/internal/topology/golang"
)

// Iterates all structs and interfaces in the topology, checking if each struct implements each interface, and records the ConnImplements/ConnImplBy connections bidirectionally.
func matchStructsToInterfaces(gt *golang.GolangTopology) {
	for ifaceID, iface := range gt.Interfaces {
		for structID, str := range gt.Structs {
			if implements(str, iface, gt) {
				ifaceConns := iface.Connections
				if ifaceConns == nil {
					ifaceConns = make(map[golang.ConnectionKind][]string)
				}
				ifaceConns[golang.ConnImplBy] = append(ifaceConns[golang.ConnImplBy], string(structID))
				iface.Connections = ifaceConns

				strConns := str.Connections
				if strConns == nil {
					strConns = make(map[golang.ConnectionKind][]string)
				}
				strConns[golang.ConnImplements] = append(strConns[golang.ConnImplements], string(ifaceID))
				str.Connections = strConns

				gt.Interfaces[ifaceID] = iface
				gt.Structs[structID] = str
			}
		}
	}
}

// Checks whether a given GolangStruct satisfies all methods of a GolangInterface by comparing method signatures in the topology; returns true only if every interface method has a matching struct method.
func implements(str golang.GolangStruct, iface golang.GolangInterface, gt *golang.GolangTopology) bool {
	if len(iface.Methods) == 0 {
		return false
	}

	for _, ifaceMethod := range iface.Methods {
		if ifaceMethod.Name == "" {
			continue
		}

		found := false
		for _, mid := range str.Methods() {
			method, exists := gt.Functions[mid]
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

// Compares a struct method against an interface method definition by name, parameter count, and parameter types to determine if the method satisfies the interface contract.
func signaturesMatch(method golang.GolangFunction, ifaceMethod golang.FunctionDefinition) bool {
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

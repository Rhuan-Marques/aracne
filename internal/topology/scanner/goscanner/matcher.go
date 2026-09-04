package goscanner

import (
	"regexp"

	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

// Establishes bidirectional implementation relationships between all structs and interfaces in the topology.
func matchStructsToInterfaces(gt *golang.GolangTopology) {
	for ifaceID, iface := range gt.Interfaces {
		delete(iface.Connections, golang.ConnImplBy)
		gt.Interfaces[ifaceID] = iface
	}
	for structID, str := range gt.Structs {
		delete(str.Connections, golang.ConnImplements)
		gt.Structs[structID] = str
	}

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

// Checks whether a struct implements an interface by matching all interface method signatures.
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

var importPrefixRE = regexp.MustCompile(`\b\w+\.(\w+)\b`)

// Removes import package prefix from a type string using regex.
func stripImportPrefix(t string) string {
	return importPrefixRE.ReplaceAllString(t, "$1")
}

// Checks if a method matches an interface method by name and stripped type signatures.
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
		if stripImportPrefix(method.Input[i].Typing) != stripImportPrefix(ifaceMethod.Input[i].Typing) {
			return false
		}
	}
	for i := range method.Output {
		if stripImportPrefix(method.Output[i].Typing) != stripImportPrefix(ifaceMethod.Output[i].Typing) {
			return false
		}
	}

	return true
}

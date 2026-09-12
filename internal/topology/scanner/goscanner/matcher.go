package goscanner

import (
	"regexp"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
	"strings"
)

// Establishes bidirectional implementation relationships between all structs and interfaces in the topology.
// Non-struct named types take part too: `type HandlerFunc func(int) int` with a Serve method
// satisfies an interface exactly as a struct would.
func matchStructsToInterfaces(gt *golang.GolangTopology) {
	for ifaceID, iface := range gt.Interfaces {
		delete(iface.Connections, golang.ConnImplBy)
		gt.Interfaces[ifaceID] = iface
	}
	for structID, str := range gt.Structs {
		delete(str.Connections, golang.ConnImplements)
		gt.Structs[structID] = str
	}
	for ntID, nt := range gt.NamedTypes {
		delete(nt.Connections, golang.ConnImplements)
		gt.NamedTypes[ntID] = nt
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
		for ntID := range gt.NamedTypes {
			linkNamedTypeIfImplements(gt, ntID, ifaceID)
		}
	}
}

// linkNamedTypeIfImplements records ntID implements ifaceID (and the reverse edge) when the
// named type's method set satisfies the interface.
func linkNamedTypeIfImplements(gt *golang.GolangTopology, ntID golang.NamedTypeID, ifaceID golang.InterfaceID) {
	nt, ok := gt.NamedTypes[ntID]
	iface, ok2 := gt.Interfaces[ifaceID]
	if !ok || !ok2 || !methodSetImplements(nt.Connections[golang.ConnHasMethod], iface, gt) {
		return
	}
	if iface.Connections == nil {
		iface.Connections = make(map[golang.ConnectionKind][]string)
	}
	iface.Connections[golang.ConnImplBy] = append(iface.Connections[golang.ConnImplBy], string(ntID))
	gt.Interfaces[ifaceID] = iface
	if nt.Connections == nil {
		nt.Connections = make(map[golang.ConnectionKind][]string)
	}
	nt.Connections[golang.ConnImplements] = append(nt.Connections[golang.ConnImplements], string(ifaceID))
	gt.NamedTypes[ntID] = nt
}

// maxEmbedDepth bounds the embed walk. Go rejects an embedding cycle at compile time, but a
// half-written file on the way to compiling is exactly what this scanner reads.
const maxEmbedDepth = 8

// interfaceRequirements returns every method an interface demands, expanding the interfaces it
// EMBEDS. `type NamedShape interface { Shape; Named }` demands Shape's and Named's methods;
// recording an embed as a method named "Shape" meant no type could ever satisfy it, so every
// composed interface -- the io.ReadWriter pattern -- listed zero implementers.
//
// ok is false when an embed cannot be read: another package's interface this scan did not
// parse (`fmt.Stringer`), or a name that resolves to nothing. Its requirements are then
// unknown, and claiming satisfaction from the half that IS readable would put an implements
// edge on a type that does not implement it. The edge is simply not claimed, which is what
// happened before this function existed.
func interfaceRequirements(iface golang.GolangInterface, gt *golang.GolangTopology, depth int, seen map[golang.InterfaceID]bool) ([]golang.FunctionDefinition, bool) {
	if depth > maxEmbedDepth || seen[iface.ID] {
		return nil, false
	}
	seen[iface.ID] = true

	required := append([]golang.FunctionDefinition(nil), iface.Methods...)
	for _, embed := range iface.Embeds {
		embedded, ok := gt.Interfaces[embeddedInterfaceID(iface.ID, embed)]
		if !ok {
			return nil, false
		}
		inherited, ok := interfaceRequirements(embedded, gt, depth+1, seen)
		if !ok {
			return nil, false
		}
		required = append(required, inherited...)
	}
	return required, true
}

// embeddedInterfaceID resolves an embed as written against the embedding interface's own
// package: an unqualified `Shape` is a sibling declaration. A qualified `pkg.Shape` is left as
// written, so it only resolves if some scanned interface happens to carry that exact id --
// this scanner does not track the embedding file's import map here, and guessing one would
// invent requirements rather than read them.
func embeddedInterfaceID(owner golang.InterfaceID, embed string) golang.InterfaceID {
	if strings.Contains(embed, ".") {
		return golang.InterfaceID(embed)
	}
	id := string(owner)
	if cut := strings.LastIndex(id, "."); cut > 0 {
		return golang.InterfaceID(id[:cut] + "." + embed)
	}
	return golang.InterfaceID(embed)
}

// Checks whether a struct implements an interface by matching all interface method signatures.
func implements(str golang.GolangStruct, iface golang.GolangInterface, gt *golang.GolangTopology) bool {
	return methodSetImplements(promotedMethodSet(gt, str), iface, gt)
}

// promotedMethodSet returns the method set Go gives str: its own methods, plus the methods of
// the structs it embeds, and theirs in turn. `type Outer struct{ Base }` satisfies an
// interface Base's methods satisfy, and matching only str.Methods() left the idiomatic mixin
// out of every implementer list -- while call resolution (structMethodID) already followed the
// same chain, so the two disagreed about the same method.
//
// Promotion follows Go's depth rule, like structMethodID: the shallowest depth wins, and a
// name taken at a shallower depth -- by a method or by a field -- hides everything deeper.
// Two embedded types offering the same name at the SAME depth are ambiguous in Go and promote
// nothing; both are kept here, which can only make matching more generous, never less.
func promotedMethodSet(gt *golang.GolangTopology, str golang.GolangStruct) []golang.FunctionID {
	methods := str.Methods()
	level := []golang.StructID{str.ID}
	seen := map[golang.StructID]bool{str.ID: true}
	taken := make(map[string]bool)
	for _, mid := range methods {
		if m, ok := gt.Functions[mid]; ok {
			taken[m.Name] = true
		}
	}
	for _, p := range str.Params {
		taken[p.Name] = true
	}
	for depth := 0; depth < maxPromotionDepth && len(level) > 0; depth++ {
		var next []golang.StructID
		for _, sid := range level {
			owner, ok := gt.Structs[sid]
			if !ok {
				continue
			}
			for _, p := range owner.Params {
				if emb, ok := embeddedStructOf(gt, sid, p); ok && !seen[emb] {
					seen[emb] = true
					next = append(next, emb)
				}
			}
		}
		hidden := make(map[string]bool)
		for _, sid := range next {
			emb := gt.Structs[sid]
			for _, mid := range emb.Methods() {
				if m, ok := gt.Functions[mid]; ok && !taken[m.Name] {
					hidden[m.Name] = true
					methods = append(methods, mid)
				}
			}
			for _, p := range emb.Params {
				hidden[p.Name] = true
			}
		}
		for name := range hidden {
			taken[name] = true
		}
		level = next
	}
	return methods
}

// methodSetImplements checks whether a method set -- a struct's or a named type's -- provides
// every method the interface declares, with matching signatures.
func methodSetImplements(methods []golang.FunctionID, iface golang.GolangInterface, gt *golang.GolangTopology) bool {
	required, ok := interfaceRequirements(iface, gt, 0, map[golang.InterfaceID]bool{})
	if !ok || len(required) == 0 {
		return false
	}

	for _, ifaceMethod := range required {
		if ifaceMethod.Name == "" {
			continue
		}

		found := false
		for _, mid := range methods {
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

	// SameGoTypeText, not ==: on the incremental path the interface comes from the database
	// and the method from a fresh parse, so one side can still carry an older build's lossy
	// rendering of a func- or struct-typed parameter. Comparing those literally would drop
	// the implements edge until the interface's own file happened to be re-parsed.
	for i := range method.Input {
		if !contract.SameGoTypeText(stripImportPrefix(method.Input[i].Typing), stripImportPrefix(ifaceMethod.Input[i].Typing)) {
			return false
		}
	}
	for i := range method.Output {
		if !contract.SameGoTypeText(stripImportPrefix(method.Output[i].Typing), stripImportPrefix(ifaceMethod.Output[i].Typing)) {
			return false
		}
	}

	return true
}

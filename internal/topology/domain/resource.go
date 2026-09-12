package domain

import "strings"

type ResourceKind string

var (
	ResourcePackage    ResourceKind = "package"
	ResourceFile       ResourceKind = "file"
	ResourceFunction   ResourceKind = "function"
	ResourceMethod     ResourceKind = "method"
	ResourceStruct     ResourceKind = "struct"
	ResourceNamedType  ResourceKind = "named_type"
	ResourceInterface  ResourceKind = "interface"
	ResourceVariable   ResourceKind = "variable"
	ResourceDependency ResourceKind = "dependency"
)

// IsPrivateConnType reports whether a connection kind is aracne's own bookkeeping rather than
// an edge between two resources.
//
// The "__" kinds -- contract.CallSitesConn, Rust's __use_bindings -- record what a caller
// PASSES, encoded into the target string (`pkg.F=>>{"n":2,...}`). That target names no
// resource, so anything that counts, draws, ranks or offers edges has to skip them: counted as
// edges they inflate a node's degree (which drives node size and the degree-based graph
// collapse rules), advertise an "__call_sites" edge type nobody can ask for, and produce
// detail rows whose ids resolve to nothing.
func IsPrivateConnType(connType string) bool {
	return strings.HasPrefix(connType, "__")
}

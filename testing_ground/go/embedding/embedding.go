// Package embedding collects Go embedding edge cases: a struct that embeds
// another struct BY VALUE and BY POINTER, a struct that embeds a struct from
// ANOTHER package (recursive.Node), and a struct that embeds an INTERFACE.
// Method promotion is exercised on purpose: Derived embeds Base, but the
// topology models only explicitly declared methods, so Derived has no Hello of
// its own (promoted methods are not separate resources).
package embedding

import "aracne/testing_ground/go/recursive"

// Base is a plain struct with one method, used as an embedding target.
type Base struct {
	ID int
}

// Hello is Base's only method; embedders promote it in real Go but NOT in the
// topology.
func (b Base) Hello() string {
	return "base"
}

// Derived embeds Base BY VALUE — a uses_struct edge to Base. Base.Hello is
// promoted to Derived in Go, but no promoted-method resource is emitted.
type Derived struct {
	Base
	Label string
}

// Greet is Derived's OWN method; it calls the promoted Base.Hello through the
// embedded field (probes whether the promoted call resolves).
func (d Derived) Greet() string {
	return d.Hello() + ":" + d.Label
}

// PtrDerived embeds *Base BY POINTER.
type PtrDerived struct {
	*Base
	Note string
}

// Decorated embeds a struct from ANOTHER package (recursive.Node) — a
// CROSS-PACKAGE embedding (uses_struct -> recursive.Node, uses_package ->
// recursive).
type Decorated struct {
	recursive.Node
	Tag string
}

// Sink is an interface used as an embedded field below.
type Sink interface {
	Drain() int
}

// Wrapper embeds the Sink INTERFACE as an anonymous field — an interface field
// inside a struct.
type Wrapper struct {
	Sink
	Count int
}

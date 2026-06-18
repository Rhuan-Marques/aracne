// Package edge collects miscellaneous Go edge cases: a DEFINED type vs a TYPE
// ALIAS, a named slice type, a named function type, a struct with a
// function-typed field and an anonymous-struct field, a closure (function
// returning a function), blank-identifier declarations, and an external import
// (strings) used only inside a closure.
package edge

import "strings"

// Celsius is a DEFINED type (a new type whose underlying type is float64).
type Celsius float64

// Fahrenheit is a TYPE ALIAS (=) — identical to float64, not a new type.
type Fahrenheit = float64

// StringList is a named (defined) slice type.
type StringList []string

// Handler is a named function type.
type Handler func(string) (string, error)

// Registry has a FUNCTION-TYPED field, a bare func field, and an
// ANONYMOUS-STRUCT field.
type Registry struct {
	Transform Handler
	OnError   func(error)
	Meta      struct {
		Name    string
		Version int
	}
}

// A blank package-level declaration: the parser skips it (no resource emitted).
var _ = "ignored"

// pkgCounter is a package-level variable mutated by methods below.
var pkgCounter int

// MakeUpper returns a CLOSURE (a function that returns a function value).
func MakeUpper() Handler {
	return func(s string) (string, error) {
		return strings.ToUpper(s), nil // strings: external import, no internal edge
	}
}

// Run invokes the function-typed field and uses the blank identifier.
func (r Registry) Run(input string) string {
	if r.Transform == nil {
		return input
	}
	out, _ := r.Transform(input) // blank identifier discards the error
	pkgCounter++
	return out
}

// Convert converts a Celsius (defined type) into a Fahrenheit (alias).
func Convert(c Celsius) Fahrenheit {
	return Fahrenheit(c)*9/5 + 32
}

// Join uses the StringList defined type so it is referenced somewhere.
func Join(items StringList, sep string) string {
	return strings.Join(items, sep)
}

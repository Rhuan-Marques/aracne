// Package-level variables of FUNCTION type, called through the variable.
//
// This is the shape that produced 145 false `use_missing_node` warnings on the
// cli/cli fixture: `var Yellow = makeColorFunc("yellow")` / `var PrepareCmd =
// func(*exec.Cmd) Runnable`, called as `utils.Yellow(s)` / `run.PrepareCmd(c)`.
// The body analyzer resolved calls against functions, structs and named types
// but never against package-level vars, so a node that was sitting in the
// database was reported as nonexistent.
//
// EXPECTED: a call through one of these vars produces a `uses_extvar` edge to
// the var and NO warning — both same-package (Decorate below) and cross-package
// (consumer.Report, which calls edge.Colorize).
package edge

import "strings"

// Colorize is a func-typed package var assigned a function literal, called
// cross-package from consumer.Report.
var Colorize = func(s string) string {
	return "<" + s + ">"
}

// Shout is a func-typed package var assigned from a local factory call — the
// exact `var Yellow = makeColorFunc("yellow")` shape.
var Shout = makeTransform(strings.ToUpper)

// Handle is declared with the named function type Handler (see edge.go) rather
// than an inline func literal, so the var's typing is a named type.
var Handle Handler = func(s string) (string, error) {
	return s, nil
}

// makeTransform returns a closure; it is the factory behind Shout.
func makeTransform(f func(string) string) func(string) string {
	return func(s string) string {
		return f(s)
	}
}

// Decorate calls two package-level func vars from the SAME package. EXPECTED:
// uses_extvar to Colorize and to Shout, and no use_missing_node warning.
func Decorate(s string) string {
	return Colorize(Shout(s))
}

// DecorateChecked calls a func var declared with a named function type.
// EXPECTED: uses_extvar to Handle.
func DecorateChecked(s string) (string, error) {
	return Handle(s)
}

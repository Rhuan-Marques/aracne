// Package failure collects Go error-handling edge cases: a SENTINEL error
// variable created by errors.New, a struct that implements the BUILTIN error
// interface via an Error() method, a LOCAL interface whose method returns
// error, a struct that satisfies it, and an error-wrapping helper using
// fmt.Errorf with %w.
package failure

import (
	"errors"
	"fmt"
)

// ErrEmpty is a SENTINEL error: a package-level var initialized by a
// cross-dependency call (errors.New) — exercises a var whose value comes from
// an external dependency.
var ErrEmpty = errors.New("empty input")

// ParseError implements the BUILTIN error interface via its Error method. The
// builtin error is not a corpus resource, so no implemented_by edge to it is
// expected — this documents that boundary.
type ParseError struct {
	Line int
	Msg  string
}

// Error makes *ParseError satisfy the builtin error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// Validator is a LOCAL interface whose single method returns the builtin error
// type — exercises interface matching when a signature mentions error.
type Validator interface {
	Validate() error
}

// Form satisfies Validator.
type Form struct {
	Name string
}

// Validate makes Form implement Validator; it returns a *ParseError or the
// ErrEmpty sentinel as the builtin error.
func (f Form) Validate() error {
	if f.Name == "" {
		return ErrEmpty
	}
	if len(f.Name) > 64 {
		return &ParseError{Line: 1, Msg: "name too long"}
	}
	return nil
}

// wrap wraps an error with context using fmt.Errorf %w (external, no internal
// edge) and takes/returns the builtin error type.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("validate: %w", err)
}

// Check validates a form and wraps any failure — ties Form.Validate and wrap
// together so both are reachable.
func Check(f Form) error {
	return wrap(f.Validate())
}

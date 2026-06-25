// Package visibility collects UNEXPORTED (lowercase) Go resources and verifies
// they are still tracked: an unexported interface, an unexported struct that
// implements it, an unexported constructor that returns the interface, an
// unexported package var, and an exported function that wires them together
// (cross-visibility calls).
package visibility

import "errors"

// reader is an UNEXPORTED interface.
type reader interface {
	read() ([]byte, error)
}

// fileReader is an UNEXPORTED struct that implements the unexported reader
// interface (unexported interface <-> struct matching).
type fileReader struct {
	path string
}

// read makes fileReader satisfy reader.
func (f fileReader) read() ([]byte, error) {
	if f.path == "" {
		return nil, errors.New("no path")
	}
	return []byte(f.path), nil
}

// defaultPath is an UNEXPORTED package-level variable.
var defaultPath = "/tmp/default"

// newReader is an UNEXPORTED constructor that returns the INTERFACE type (not a
// concrete struct) — probes whether constructor detection fires for an
// interface return.
func newReader(p string) reader {
	if p == "" {
		p = defaultPath
	}
	return fileReader{path: p}
}

// Load is the EXPORTED entry point; it calls the unexported constructor and the
// unexported method (cross-visibility calls edges -> newReader, -> read).
func Load(p string) ([]byte, error) {
	r := newReader(p)
	return r.read()
}

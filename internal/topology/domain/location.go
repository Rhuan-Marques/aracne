package domain

// Represents a source code location with a file path and Start/End line numbers, used to identify and extract code regions for the topology.
type Location struct {
	StartsAt int
	EndsAt   int
	Path     string
}

// Contains reports whether other lies entirely within l, in the same file.
//
// It is what tells a read whether an inlined enclosing type ALREADY contains the member being
// read. In Go and Rust a struct's cut is just the type declaration, so a method sits outside
// it and has to be appended; in Python, JS and Java the class cut is the whole class, so
// appending the method would print it twice. Comparing ranges gets both right without the
// renderer knowing which language it is looking at.
func (l Location) Contains(other Location) bool {
	if l.Path == "" || l.Path != other.Path {
		return false
	}
	if l.StartsAt <= 0 || l.EndsAt <= 0 || other.StartsAt <= 0 {
		return false
	}
	return other.StartsAt >= l.StartsAt && other.EndsAt <= l.EndsAt
}

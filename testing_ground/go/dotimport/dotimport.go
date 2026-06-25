// Package dotimport probes a DOT IMPORT (import . "strings"): the imported
// package's names are usable unqualified, so ToUpper below has no package
// selector. The scanner must not crash on this and must not invent a false
// internal edge for the unqualified call (strings is an external dependency).
package dotimport

import . "strings"

// Shout upper-cases its argument using the DOT-IMPORTED ToUpper (no "strings."
// qualifier) — exercises name resolution under a dot import.
func Shout(s string) string {
	return ToUpper(s)
}

// Loud repeats Shout's result so the dot-imported name is referenced twice.
func Loud(s string) string {
	return ToUpper(Shout(s))
}

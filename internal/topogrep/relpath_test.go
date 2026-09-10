package topogrep

import (
	"testing"
)

// See domain.RelInside. displayPath relativizes a WALKED path -- one relative to the working
// directory -- and a `./..data/x` path from a walk rooted at `.` must shed its `./` like any other
// path inside the tree. The old `strings.HasPrefix(rel, "..")` test took `..data` for "outside"
// and kept the raw spelling. (An absolute path is left as it stands either way: displayPath does
// not relativize those.)
func TestSearchRowsKeepADotDotNamedDirectoryRelative(t *testing.T) {
	if got, want := displayPath("./..data/app.go", false), "..data/app.go"; got != want {
		t.Errorf("displayPath = %q, want %q", got, want)
	}
	if got, want := displayPath("../outside/app.go", false), "../outside/app.go"; got != want {
		t.Errorf("a path outside the tree keeps its spelling: %q", got)
	}
}

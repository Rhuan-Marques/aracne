package domain

import (
	"path/filepath"
	"testing"
)

// RelInside tests whole path SEGMENTS. The prefix test it replaces also matched a directory
// whose name merely begins with two dots -- `..data`, what Kubernetes ConfigMap and Secret volume
// mounts are called -- and every renderer using it echoed such a file's absolute path.
func TestRelInsideJudgesSegmentsNotPrefixes(t *testing.T) {
	for rel, want := range map[string]bool{
		".":              true,
		"a/b.go":         true,
		"..data":         true,
		"..data/app.go":  true,
		"...":            true,
		"a/../b":         true,
		"..":             false,
		"../x.go":        false,
		"../../x":        false,
		"/abs/elsewhere": false,
	} {
		if got := RelInside(filepath.FromSlash(rel)); got != want {
			t.Errorf("RelInside(%q) = %v, want %v", rel, got, want)
		}
	}
}

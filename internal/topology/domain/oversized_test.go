package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RD-8: read.max_file_size says a file above it is not indexed. PathHidden is the one gate
// every discovery walk consults, so the limit lives there -- but only for extensions a scanner
// parses: the manifest walk asks about every file in the tree, and a stat per image or lockfile
// nearly doubled a walk that runs before every tool call.
func TestPathOversizedHidesOnlyOverLimitSourceFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	big, small := write("big.go", 2000), write("small.go", 100)
	bigTS, image := write("bundle.ts", 2000), write("logo.png", 2000)

	SetActiveMaxFileSize(1000)
	defer SetActiveMaxFileSize(0)
	for p, want := range map[string]bool{big: true, bigTS: true, small: false, image: false} {
		if got := PathHidden(p); got != want {
			t.Errorf("PathHidden(%s) = %v, want %v", filepath.Base(p), got, want)
		}
	}
	if PathHidden(filepath.Join(dir, "missing.go")) {
		t.Error("a file that does not exist is not oversized")
	}

	SetActiveMaxFileSize(0)
	if PathHidden(big) {
		t.Error("with no limit installed nothing is oversized")
	}
}

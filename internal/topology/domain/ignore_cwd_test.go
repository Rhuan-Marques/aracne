package domain

import (
	"os"
	"path/filepath"
	"testing"
)

// A RELATIVE PATH IS RESOLVED AGAINST THE PROCESS, NOT AGAINST THE ROOT.
//
// Every caller hands the matcher the output of a real directory walk, so a relative path is
// relative to the working directory that walk started from -- the root only when the caller
// happens to be standing in it. Joining it onto the matcher's root instead was right in that one
// case and wrong in every other: an intercepted `grep -rn x ../../` run from a subdirectory
// produced `../../generated/gen.go`, which joined to `<root>/../../generated/gen.go`, rel'd back
// to a `../` chain, and returned "" -- read by every rule as "outside the root, nothing
// applies". scan.ignore silently stopped applying the moment the agent's shell was not at the
// project root.
func TestIgnoreMatchesAWalkPathFromAnotherWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m := BuildIgnoreMatcher(root, []string{"generated/"})

	// The walk's own spelling from three vantage points. All three name the same file, so all
	// three must be ignored.
	t.Chdir(root)
	if !m.Match("generated/gen.go") {
		t.Error("a walk rooted at the project must match")
	}
	if !m.Match(filepath.Join(root, "generated", "gen.go")) {
		t.Error("an absolute path must match")
	}

	t.Chdir(sub)
	if !m.Match(filepath.Join("..", "..", "generated", "gen.go")) {
		t.Error("a walk rooted at a subdirectory must match too: scan.ignore does not stop " +
			"applying because the shell moved")
	}
	if !m.MatchDir(filepath.Join("..", "..", "generated")) {
		t.Error("the directory itself must still be prunable from a subdirectory")
	}

	// And nothing outside the root is swept in by the new resolution.
	if m.Match(filepath.Join("..", "..", "pkg", "sub", "keep.go")) {
		t.Error("an unignored path must stay unignored")
	}
}

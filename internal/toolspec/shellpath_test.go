package toolspec

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestShellPathsReadPOSIXOnEveryPlatform pins the property the guard was missing.
//
// These assertions are about the POSIX spelling, which is how an agent writes a path in a
// command whatever it is running on. They read as tautologies on Unix -- that is the point:
// each one is a place where the path/filepath function these wrap answers DIFFERENTLY on
// Windows, and where that difference had the guard treating `/etc/passwd` as a relative path
// inside the project and `/tmp/other` as no path at all.
func TestShellPathsReadPOSIXOnEveryPlatform(t *testing.T) {
	for _, p := range []string{"/", "/etc/passwd", "/tmp/other", "/repo/worktree/src/a.go"} {
		if !ShellPathIsAbs(p) {
			t.Errorf("ShellPathIsAbs(%q) = false; a rooted path is absolute in the command that carries it", p)
		}
	}
	for _, p := range []string{"a.go", "src/a.go", "..", "./x"} {
		if ShellPathIsAbs(p) {
			t.Errorf("ShellPathIsAbs(%q) = true, want false", p)
		}
	}
	for _, p := range []string{"src/a.go", "/tmp/x", "a/b/c"} {
		if !ShellPathHasSeparator(p) {
			t.Errorf("ShellPathHasSeparator(%q) = false; `/` separates on every platform", p)
		}
	}
	if ShellPathHasSeparator("NR>=955") || ShellPathHasSeparator("a.go") {
		t.Error("a token with no separator must not read as a path")
	}

	if got := ShellPathJoin("/tmp/other", "shapes/shape.go"); got != "/tmp/other/shapes/shape.go" {
		t.Errorf("ShellPathJoin = %q, want /tmp/other/shapes/shape.go", got)
	}
	if got := ShellPathJoin("sub", "a.go"); got != "sub/a.go" {
		t.Errorf("ShellPathJoin = %q, want sub/a.go", got)
	}
	if got := ShellPathJoin("", "a.go"); got != "a.go" {
		t.Errorf("ShellPathJoin with no base = %q, want a.go", got)
	}

	// Whole segments, so a sibling sharing a prefix is not a child.
	for _, tc := range []struct {
		path, root string
		want       bool
	}{
		{"/repo/worktree", "/repo/worktree", true},
		{"/repo/worktree/src/a.go", "/repo/worktree", true},
		{"/repo/worktree-backup/src/a.go", "/repo/worktree", false},
		{"/tmp/scratch.py", "/repo/worktree", false},
		{"/repo/worktree/../other/a.go", "/repo/worktree", false},
	} {
		if got := ShellPathUnder(tc.path, tc.root); got != tc.want {
			t.Errorf("ShellPathUnder(%q, %q) = %v, want %v", tc.path, tc.root, got, tc.want)
		}
	}
}

// The host's own dialect keeps its meaning: these are the paths a Windows agent or a hook
// payload actually carries, and they must not be demoted to relative by the POSIX reading.
func TestShellPathsKeepTheHostDialect(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows spellings are only absolute on Windows")
	}
	for _, p := range []string{`C:\Users\x\repo\a.go`, `\\host\share\a.go`} {
		if !ShellPathIsAbs(p) {
			t.Errorf("ShellPathIsAbs(%q) = false, want true", p)
		}
		if !ShellPathHasSeparator(p) {
			t.Errorf("ShellPathHasSeparator(%q) = false, want true", p)
		}
	}
	if !ShellPathUnder(`C:\repo\src\a.go`, `C:\repo`) {
		t.Error("a Windows path under a Windows root must read as inside it")
	}
	// And the two dialects are comparable once cleaned, which is what lets a command's
	// operand be judged against a root that came from the filesystem.
	if ShellPathClean(`C:\repo\src`) != "C:/repo/src" {
		t.Errorf("ShellPathClean = %q, want C:/repo/src", ShellPathClean(`C:\repo\src`))
	}
	if filepath.IsAbs("/etc/passwd") {
		t.Error("fixture check: filepath.IsAbs is expected to be false here, which is the whole reason for ShellPathIsAbs")
	}
}

package toolspec

import (
	"path"
	"path/filepath"
	"strings"
)

// A path written inside a COMMAND or a tool argument is not a path in the host filesystem's
// spelling, and judging it with path/filepath is wrong on Windows.
//
// WHAT GOES WRONG THERE. `filepath.Separator` is `\`, so `/tmp/scratch.py` and `shapes/shape.go`
// carry no separator at all and were not recognised as paths; `filepath.IsAbs("/etc/passwd")` is
// false, because a rooted path with no volume is not absolute on Windows, so the guard read it as
// RELATIVE and rebased it onto the project -- `cat /tmp/pr.diff` looked like a read of the
// project, and an `ids` entry of "/etc/passwd" was not even path-shaped enough to be judged
// against the workspace. Every one of those is a command an agent writes in POSIX form whatever
// it is running on, because the shell it is written for takes it that way.
//
// So these five judge a path the way the command that carries it does: `/` is a separator and a
// leading `/` is absolute, on every platform, while a Windows spelling (`C:\x`, `\\host\share`)
// keeps its own meaning. On Unix each is exactly the path/filepath function it wraps, which is
// why nothing there can change.

// ShellPathIsAbs reports whether p is absolute as the command that carries it means it.
//
// The leading-separator test covers BOTH dialects, which matters for a path that has been
// through filepath.Join on Windows: joining onto "/repo" yields `\repo\...`, rooted but
// carrying no volume, and filepath.IsAbs calls that relative. Resolving it against the working
// directory then bolts the current drive onto a path the caller had already given in full.
func ShellPathIsAbs(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, string(filepath.Separator)) || filepath.IsAbs(p)
}

// ShellPathHasSeparator reports whether p carries a directory separator of either dialect.
func ShellPathHasSeparator(p string) bool {
	return strings.ContainsRune(p, '/') || strings.ContainsRune(p, filepath.Separator)
}

// ShellPathClean is filepath.Clean over one spelling: forward slashes, which both dialects read.
func ShellPathClean(p string) string {
	if p == "" {
		return p
	}
	return path.Clean(filepath.ToSlash(p))
}

// ShellPathJoin joins an operand onto the directory a `cd` moved the command to. The result is
// spelled with forward slashes for the same reason ShellPathClean is: the two sides can arrive
// in different dialects (`cd /tmp/other` and `C:\proj\a.go` in one command line), and only one
// spelling can be compared against the other.
func ShellPathJoin(base, p string) string {
	if base == "" {
		return p
	}
	return path.Join(filepath.ToSlash(base), filepath.ToSlash(p))
}

// ShellPathUnder reports whether p is root or lives beneath it, compared segment-wise so a
// sibling sharing a name prefix (`/repo-backup` beside `/repo`) is not mistaken for a child.
func ShellPathUnder(p, root string) bool {
	p, root = ShellPathClean(p), ShellPathClean(root)
	if root == "" {
		return false
	}
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

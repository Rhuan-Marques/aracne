package helper

import "path/filepath"

// CanonicalPath returns path as an absolute path with every symlinked component resolved.
//
// WHY THE SCAN CANNOT USE filepath.Abs ALONE. filepath.Abs does not resolve symlinks, and
// filepath.WalkDir Lstats its root -- so a root whose last component IS a symlink (`cd
// /work/link && arac scan`, a dotfiles checkout, a WSL or container bind mount) is visited as
// a link and never descended into. Detection still succeeds, because Stat follows links and
// finds the go.mod behind it, so the scan reported success and indexed nothing. Worse, the
// stored root then disagreed with the real path every later `arac update-file` passes, and the
// ids it minted from the relative path between them (`_/../real/pkg.One`) belonged to no
// project.
//
// A path that does not exist yet -- a database about to be created, a file being removed --
// still has existing ancestors, and it is the DIRECTORY components that carry the symlink, so
// the deepest resolvable prefix is used and the rest is rejoined. Everything is best-effort:
// where nothing resolves, the absolute path is the answer, which is what the callers used
// before.
func CanonicalPath(path string) string {
	if path == "" {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return canonicalAbs(abs)
}

// canonicalAbs is CanonicalPath's recursion over an already-absolute path: resolve it, or
// resolve its parent and re-attach the last component.
func canonicalAbs(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if dir == abs || base == "" {
		return abs
	}
	return filepath.Join(canonicalAbs(dir), base)
}

// PathCandidates returns the spellings a caller's path may have to be tried under before it is
// declared absent from the graph: the input itself, its absolute form, its form joined onto the
// topology root, and the canonical form of each.
//
// WHY THE CANONICAL FORMS COME LAST. The scan mints every id through CanonicalPath, so a
// project reached through a symlinked directory is stored under the REAL path while the caller
// -- an agent's cwd, a `cat` the guard rewrote, a tool argument -- names it through the link.
// On macOS every t.TempDir() is such a path (/var is a symlink to /private/var), and so is any
// checkout under a bind mount or a linked home. The lookup missed and the read answered "not in
// the topology" for a file the index held.
//
// Appending rather than substituting keeps this strictly additive: every spelling that resolves
// today still resolves first, in the order it always did, and the canonical forms are reached
// only where the answer used to be "not found". EvalSymlinks costs one lstat per component, so
// it is worth paying only on that miss -- callers that already hold a graph path never get here.
func PathCandidates(id, root string) []string {
	out := make([]string, 0, 6)
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(id)
	if abs, err := filepath.Abs(id); err == nil {
		add(abs)
	}
	if root != "" && !filepath.IsAbs(id) {
		joined := filepath.Join(root, id)
		add(joined)
		if abs, err := filepath.Abs(joined); err == nil {
			add(abs)
		}
		add(CanonicalPath(joined))
	}
	add(CanonicalPath(id))
	return out
}

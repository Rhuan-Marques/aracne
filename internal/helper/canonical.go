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

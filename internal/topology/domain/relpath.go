package domain

import (
	"path/filepath"
	"strings"
)

// RelInside reports whether rel -- a filepath.Rel result -- names a path inside the base it was
// computed against (the base itself included).
//
// The test is on whole path SEGMENTS. `strings.HasPrefix(rel, "..")` is the tempting spelling
// and it is wrong: it also matches a directory whose NAME begins with two dots, and `..data` is
// exactly what Kubernetes ConfigMap and Secret volume mounts are called. Every renderer that
// used it echoed such a file's absolute path into each row it printed.
//
// BOTH SEPARATORS ARE JUDGED, ON EVERY PLATFORM, and a rooted path is rejected even where
// filepath.IsAbs says otherwise. On Windows `filepath.IsAbs` is false for a path that is rooted
// but carries no volume (`\abs\elsewhere`, which is what a forward-slashed path out of a config
// file or a JSON payload becomes), and Separator is `\`, so `../x` -- legal on Windows and the
// spelling every slash-normalized path arrives in -- walked straight past the escape test and
// was reported as INSIDE the project. A drive-relative path (`C:foo`) is outside for the same
// reason: it names a directory this base knows nothing about.
//
// The cost on Unix is that a file literally named `\x` or `..\x` is now called outside. Such a
// name is legal there and nothing creates one; answering "outside" for it errs toward excluding
// a path, which is the direction this test is safe in.
func RelInside(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || isRooted(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`)
}

// isRooted reports the two shapes filepath.IsAbs does not call absolute but which are still not
// relative to the base: a leading separator of either kind, and a Windows drive-relative path.
func isRooted(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return filepath.VolumeName(p) != ""
}

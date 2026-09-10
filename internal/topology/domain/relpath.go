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
func RelInside(rel string) bool {
	if filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

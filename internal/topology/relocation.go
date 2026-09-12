package topology

import (
	"os"
	"path/filepath"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Relocation reports whether the project this database indexes has moved since it was scanned:
// the root recorded in the database is not the directory that holds its `.aracne`.
//
// WHY IT HAS TO BE ASKED. The database stores absolute paths -- the root, every file ID, every
// manifest key. After `mv p1 p2` all of them name a directory that is gone: reads opened files
// under p1, `check-updates` failed on the missing root, the guard's pre-tool scan scanned the
// stored root and failed silently on every call, and a plain `arac scan` diffed the manifest
// against p2, saw every file deleted and every file new, and left an empty graph with a
// node_removed warning per symbol and the descriptions gone. A copied project is the same case
// with the old directory still present: its graph describes the original, not the copy.
//
// The check is two path comparisons and one indexed query, so every entry point can afford it.
// It applies only to the standard `<root>/.aracne/<db>` layout -- a database kept anywhere else
// was placed there by a caller who chose the root, and nothing can be inferred from where it
// sits. A stored root that is relative, or that names the same directory under another spelling
// (a symlink, a bind mount), is not a move -- and every scan entry point now stores the root
// canonically (helper.CanonicalPath), so that spelling is the resolved one.
func (m *TopologyManager) Relocation() (storedRoot, projectRoot string, moved bool) {
	projectRoot = standardProjectRoot(m.dbPath)
	if projectRoot == "" {
		return "", "", false
	}
	storedRoot = helper.ReadStoredRoot(m.dbPath)
	if storedRoot == "" || !filepath.IsAbs(storedRoot) || filepath.Clean(storedRoot) == projectRoot {
		return storedRoot, projectRoot, false
	}
	if a, err := os.Stat(storedRoot); err == nil {
		if b, err := os.Stat(projectRoot); err == nil && os.SameFile(a, b) {
			return storedRoot, projectRoot, false
		}
	}
	return storedRoot, projectRoot, true
}

// SyncRelocation rebuilds the graph under the project's current root when Relocation reports a
// move, and reports whether it did.
//
// A full rescan rather than a rewrite of the stored paths: FullReScan already carries every
// description across by ID and by identity (relative path, kind, name, parent), remaps bugs,
// and records old -> new aliases, so the move costs one scan and loses nothing -- and it raises
// no warnings, because nothing about the code changed.
func (m *TopologyManager) SyncRelocation(reg *scanner.Registry) (bool, error) {
	if reg == nil {
		return false, nil
	}
	_, projectRoot, moved := m.Relocation()
	if !moved {
		return false, nil
	}
	_, err := m.FullReScan(projectRoot, reg)
	return true, err
}

// standardProjectRoot is the directory holding the `.aracne` a database sits in, or "" when the
// database lives anywhere else.
func standardProjectRoot(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) != ".aracne" {
		return ""
	}
	return filepath.Dir(dir)
}

// rebasedPathIDs maps every old resource whose ID is a path under the old root to the resource
// at the same relative path under the new root -- the file nodes, whose identity IS their
// absolute path. It is the exact half of carrying a relocated graph across; the identity remap
// only reaches resources that have a description.
func rebasedPathIDs(oldTopo, newTopo *domain.Topology) map[string]string {
	oldRoot, newRoot := oldTopo.Root, newTopo.Root
	if oldRoot == "" || newRoot == "" || filepath.Clean(oldRoot) == filepath.Clean(newRoot) {
		return nil
	}
	out := make(map[string]string)
	for id := range oldTopo.Resources {
		if !filepath.IsAbs(id) {
			continue
		}
		rel, err := filepath.Rel(oldRoot, id)
		if err != nil || !domain.RelInside(rel) {
			continue
		}
		newID := filepath.Join(newRoot, rel)
		if _, ok := newTopo.Resources[newID]; ok {
			out[id] = newID
		}
	}
	return out
}

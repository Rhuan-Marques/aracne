package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// STO-05. `arac scanner run` diffs only the languages Registry.DetectAll still finds on disk,
// so a language whose LAST file was deleted is no longer detected and its deletion is never
// seen: the nodes stay in the graph while `arac check-updates` calls the index stale.
// IncrementalScan was fixed for exactly this (TopologyManager.IncrementalLanguages); the
// watcher was not. It also returned on the first DiffScanFiles error, the veto over every
// other language that IncrementalScan deliberately removed.

// watcherFixture builds a mixed Go/Python project, scans it, and returns its root and db path.
func watcherFixture(t *testing.T) (root, db string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module watched\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Kept() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "py"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "py", "only.py"), []byte("def helper():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	RunScan([]string{"-root", root})
	db = filepath.Join(root, DefaultDBRelative)
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("fixture scan produced no database: %v", err)
	}
	return root, db
}

func TestSTO05_WatcherSeesTheLastFileOfALanguageDeleted(t *testing.T) {
	root, db := watcherFixture(t)
	if err := os.Remove(filepath.Join(root, "py", "only.py")); err != nil {
		t.Fatal(err)
	}

	mgr := topology.New()
	mgr.Load(db)
	_, _, deleted, diffErrs := watcherDiff(mgr, NewScannerRegistry(), helper.CanonicalPath(root), helper.ManifestPath(db))
	if len(diffErrs) > 0 {
		t.Fatalf("unexpected diff errors: %v", diffErrs)
	}
	found := false
	for _, d := range deleted {
		if strings.HasSuffix(d, "only.py") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the watcher never saw the last python file deleted, so its nodes stay in the graph forever: deleted=%v", deleted)
	}
}

func TestSTO05_WatcherSkipsAFailingLanguageInsteadOfAborting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this fixture relies on")
	}
	root, db := watcherFixture(t)

	// A directory the walk cannot LIST but can still traverse is the case DiffScanFiles'
	// mass-deletion guard exists for: python's walk comes back empty while its manifest file is
	// still on disk, so the diff refuses to report it deleted and errors. Go's own changes must
	// still come through.
	pyDir := filepath.Join(root, "py")
	if err := os.Chmod(pyDir, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pyDir, 0o755) })

	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package main\n\nfunc Added() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := topology.New()
	mgr.Load(db)
	added, _, _, diffErrs := watcherDiff(mgr, NewScannerRegistry(), helper.CanonicalPath(root), helper.ManifestPath(db))
	if len(diffErrs) == 0 {
		t.Fatal("fixture is wrong: the python diff was expected to fail")
	}
	found := false
	for _, a := range added {
		if strings.HasSuffix(a, "added.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("one language's diff error vetoed every other language: added=%v errors=%v", added, diffErrs)
	}
}

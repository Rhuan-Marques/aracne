package topology_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
)

// scannedMixedProject writes files under a fresh root, full-scans them with the Go and Python
// scanners, and returns the root, db path, manager and registry.
func scannedMixedProject(t *testing.T, files map[string]string) (string, string, *topology.TopologyManager, *scanner.Registry) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		domain.SetActiveIgnore(nil)
		domain.SetActivePathVisibility(nil)
	})
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return dir, dbPath, mgr, reg
}

// Deleting the last file of a language used to leave it in the graph for good: the language
// was no longer detected, so no incremental scan ever diffed it, while check-updates (which
// diffs stored and detected languages alike) reported the index stale on every run.
func TestIncrementalScanRemovesALanguagesLastFile(t *testing.T) {
	dir, dbPath, mgr, reg := scannedMixedProject(t, map[string]string{
		"go.mod":   "module example.com/p\n\ngo 1.21\n",
		"pkg/a.go": "package pkg\n\nfunc A() int { return 1 }\n",
		"app.py":   "def f():\n    return 1\n",
	})
	if !hasResource(t, dbPath, "app.f") {
		t.Fatal("setup: app.f was never indexed")
	}
	if err := os.Remove(filepath.Join(dir, "app.py")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := mgr.IncrementalScan(dir, reg); err != nil {
			t.Fatalf("incremental scan %d: %v", i+1, err)
		}
	}
	if hasResource(t, dbPath, "app.f") {
		t.Error("app.f is still in the graph after its file was deleted")
	}
	if hasResource(t, dbPath, filepath.Join(dir, "app.py")) {
		t.Error("the deleted app.py file node is still in the graph")
	}
	// The other language is untouched.
	if !hasResource(t, dbPath, "example.com/p/pkg.A") {
		t.Error("deleting the Python file took the Go graph with it")
	}
	health, err := mgr.IndexHealth(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if health.Stale() {
		t.Errorf("the index is still stale after the deletion was scanned: %s", health.Summary())
	}
}

// A project whose every source file was deleted: the incremental scan used to fail with "no
// language scanner detected" on every call (the guard runs one before each tool call) and keep
// the old graph. It now processes the deletions.
func TestIncrementalScanProcessesDeletingEveryFile(t *testing.T) {
	dir, dbPath, mgr, reg := scannedMixedProject(t, map[string]string{
		"app.py": "def f():\n    return 1\n",
	})
	if err := os.Remove(filepath.Join(dir, "app.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("incremental scan: %v", err)
	}
	if hasResource(t, dbPath, "app.f") {
		t.Error("app.f is still in the graph after every file was deleted")
	}
}

// Pins what must not change: a registered language that never had a file in this project is
// not diffed, so a Go-only project with the Python scanner registered scans exactly as before,
// and a language whose files are still there keeps its nodes.
func TestIncrementalScanLeavesUnusedLanguagesAlone(t *testing.T) {
	dir, dbPath, mgr, reg := scannedMixedProject(t, map[string]string{
		"go.mod":   "module example.com/p\n\ngo 1.21\n",
		"pkg/a.go": "package pkg\n\nfunc A() int { return 1 }\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "pkg", "b.go"),
		[]byte("package pkg\n\nfunc B() int { return A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("incremental scan: %v", err)
	}
	for _, id := range []string{"example.com/p/pkg.A", "example.com/p/pkg.B"} {
		if !hasResource(t, dbPath, id) {
			t.Errorf("%s missing after an ordinary incremental scan", id)
		}
	}
}

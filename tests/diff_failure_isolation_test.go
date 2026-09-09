package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// One language's diff failure must not veto the whole scan.
//
// IncrementalScan used to `return nil, diffErr` on the first failure, which handed one language
// a veto over every other. The reachable case is DiffScanFiles' mass-deletion guard, and the
// consequence was permanent: with one Python manifest entry the guard would not clear, every
// incremental scan from then on -- `arac scan`, the guard's pre-tool scan, the drift check --
// aborted before it reached Go, so the graph silently stopped tracking the whole project until
// someone ran `arac scan --all`, which nothing told them to do.
func TestOneLanguagesDiffFailureDoesNotVetoTheScan(t *testing.T) {
	reg := partialTestRegistry()

	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module diffisolation\n\ngo 1.21\n")
	writeFileMk(t, dir, "main.go", "package main\n\nfunc Alpha() {}\n")
	// pyproject.toml keeps Python DETECTED after its last source file is removed, which is
	// what makes the wedged diff reachable through IncrementalScan -- and is how nearly every
	// real Python project is laid out.
	writeFileMk(t, dir, "pyproject.toml", "[project]\nname = \"diffisolation\"\n")
	writeFileMk(t, dir, "app.py", "def alpha():\n    pass\n")

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	// Wedge the PYTHON diff, in the exact shape the mass-deletion guard refuses: its source
	// is gone from the tree, and its manifest entry names a real file the walk cannot reach.
	if err := os.Remove(filepath.Join(dir, "app.py")); err != nil {
		t.Fatal(err)
	}
	stranded := filepath.Join(t.TempDir(), "app.py")
	if err := os.WriteFile(stranded, []byte("def alpha():\n    pass\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manifestPath := helper.ManifestPath(dbPath)
	manifest := helper.ReadManifest(manifestPath)
	for path := range manifest {
		if strings.HasSuffix(path, ".py") {
			delete(manifest, path)
		}
	}
	manifest[stranded] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if err := helper.WriteManifest(manifest, manifestPath); err != nil {
		t.Fatal(err)
	}

	// A Go change the scan must still pick up.
	goSrc := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goSrc, []byte("package main\n\nfunc Alpha() {}\n\nfunc Beta() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bumpMTime(t, goSrc)

	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v (a wedged python diff must not fail the whole scan)", err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, res := range topo.Resources {
		if res.Name == "Beta" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the Go change was not indexed: one language's diff failure vetoed the scan")
	}
	// Recorded rather than swallowed, so a reader of the graph can see that a language
	// stopped being scanned.
	sawDiffError := false
	for key := range topo.Errors {
		if strings.HasPrefix(key, "diff:") {
			sawDiffError = true
		}
	}
	if !sawDiffError {
		t.Fatalf("the python diff failure was not recorded in topo.Errors: %v", topo.Errors)
	}
}

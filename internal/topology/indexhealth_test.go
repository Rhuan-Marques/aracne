package topology_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
)

// A LANGUAGE THE GRAPH HAS NEVER SEEN IS STILL DRIFT.
//
// IndexHealth took its language list from the STORED topology while IncrementalScan takes its
// from Registry.DetectAll against the tree, so the first .py file in a Go repository was
// invisible to one and obvious to the other. `arac check-updates` -- the command whose whole job
// is "should I believe this graph" -- printed "All files are up to date" and exited 0 with a
// whole unindexed language on disk, and the guard's post-tool drift check, which gates its scan
// on this, skipped the very call that created the file.
func TestIndexHealthSeesALanguageTheGraphDoesNotHoldYet(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/h\n\ngo 1.21\n")
	write("a.go", "package main\n\nfunc Main() int { return 1 }\n")

	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}

	health, err := mgr.IndexHealth(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if health.Stale() {
		t.Fatalf("a freshly scanned tree must be current, got %s", health.Summary())
	}

	// The first file of a language the topology has never held.
	write("new.py", "def brand_new(a):\n    return a + 1\n")

	health, err = mgr.IndexHealth(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !health.Stale() {
		t.Error("a new language's first file is unindexed source and must report as drift")
	}
	var found bool
	for _, f := range health.Files() {
		if f == "new.py" {
			found = true
		}
	}
	if !found {
		t.Errorf("new.py missing from the drift report: %v", health.Files())
	}

	// A caller with no registry keeps the old stored-languages answer rather than erroring.
	if _, err := mgr.IndexHealth(dir, nil); err != nil {
		t.Errorf("a nil registry must still be answerable: %v", err)
	}
}

// A MULTI-LANGUAGE PROJECT IS CURRENT AFTER A SCAN, test files and all.
//
// A project in two languages stores topo.Language as the aggregate "multi", and IndexHealth
// used to diff under that name too. No scanner owns it, so the diff fell through to a
// language-agnostic file test that had no rule for test files: every *_test.go and test_*.py
// was reported as new on every run, check-updates could never exit 0, and the guard's drift
// probe was permanently true. This repository reported 192 such files.
func TestIndexHealthIsCurrentAfterScanningAMultiLanguageProject(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("a.go", "package main\n\nfunc Alpha() int { return 1 }\n")
	write("a_test.go", "package main\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) { _ = Alpha() }\n")
	write("mod.py", "def beta():\n    return 2\n")
	write("test_mod.py", "def test_beta():\n    assert True\n")

	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}

	for _, r := range []*scanner.Registry{reg, nil} {
		health, err := mgr.IndexHealth(dir, r)
		if err != nil {
			t.Fatal(err)
		}
		if health.Stale() {
			t.Errorf("a freshly scanned two-language project must be current (registry=%v), got %s: %v",
				r != nil, health.Summary(), health.Files())
		}
	}
}

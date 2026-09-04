package tests_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
)

func partialTestRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	return reg
}

// bumpMTime makes a file look newer than the manifest so DiffScanFiles marks it
// modified (the test runs faster than mtime granularity otherwise).
func bumpMTime(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

// TestPartialPathExercisedOnGoModify proves that modifying a Go function body in
// a multi-file repo goes through the scoped partial change-path (UpdateFilePartial
// + WriteScopedResources), not the full ReadDb path. It asserts via the
// PartialIncrementalCount observability counter.
func TestPartialPathExercisedOnGoModify(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module partialeq\n\ngo 1.21\n")
	writeFileMk(t, dir, "lib/lib.go", `package lib

func Helper() {}

func Other() {}
`)
	writeFileMk(t, dir, "main.go", `package main

import "partialeq/lib"

func main() { lib.Helper() }
`)

	db := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(db), 0755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	mgr.Load(db)
	reg := partialTestRegistry()

	// Cold start -> full rescan (no partial path).
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before := topology.PartialIncrementalCount()

	// Modify a function body in main.go (no interfaces involved -> partial path).
	mainPath := filepath.Join(dir, "main.go")
	writeFile(t, mainPath, `package main

import "partialeq/lib"

func main() {
	lib.Helper()
	lib.Other()
}
`)
	bumpMTime(t, mainPath)

	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("incremental scan: %v", err)
	}
	after := topology.PartialIncrementalCount()
	if after <= before {
		t.Fatalf("expected partial change-path to be exercised (count %d -> %d)", before, after)
	}

	// Behavior check: the new call edge landed.
	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	mainFn, ok := topo.Resources["partialeq.main"]
	if !ok {
		t.Fatalf("partialeq.main missing")
	}
	found := false
	for _, c := range mainFn.Connections["calls"] {
		if c == "partialeq/lib.Other" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected main to call partialeq/lib.Other, got calls=%v", mainFn.Connections["calls"])
	}
}

// TestPartialFallbackOnInterfaceChange proves a change touching an interface
// routes to the full path (the partial path is NOT counted), preserving
// correctness for cross-package interface satisfaction.
func TestPartialFallbackOnInterfaceChange(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module ifacefb\n\ngo 1.21\n")
	writeFileMk(t, dir, "api.go", `package main

type Greeter interface{ Greet() string }
`)
	writeFileMk(t, dir, "main.go", `package main

func main() {}
`)

	db := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(db), 0755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	mgr.Load(db)
	reg := partialTestRegistry()

	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before := topology.PartialIncrementalCount()

	apiPath := filepath.Join(dir, "api.go")
	writeFile(t, apiPath, `package main

type Greeter interface {
	Greet() string
	Name() string
}
`)
	bumpMTime(t, apiPath)

	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("incremental scan: %v", err)
	}
	if topology.PartialIncrementalCount() != before {
		t.Errorf("interface change should fall back to full path, but partial count advanced from %d", before)
	}
}

package tests_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// resourceFingerprint canonicalizes everything that defines a resource's place
// in the graph: identity, row fields, and its connection set (conn types +
// targets sorted so ordering never matters). Used to compare two topologies for
// structural equality.
func resourceFingerprint(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(string(res.Kind))
	b.WriteByte('|')
	b.WriteString(res.Name)
	b.WriteByte('|')
	b.WriteString(res.Language)
	b.WriteByte('|')
	b.WriteString(res.Description)
	b.WriteByte('|')
	b.WriteString(res.Location.Path)
	b.WriteByte('|')
	propsJSON, _ := json.Marshal(res.Properties)
	b.Write(propsJSON)
	b.WriteByte('|')

	connTypes := make([]string, 0, len(res.Connections))
	for ct := range res.Connections {
		connTypes = append(connTypes, ct)
	}
	sort.Strings(connTypes)
	for _, ct := range connTypes {
		targets := append([]string(nil), res.Connections[ct]...)
		sort.Strings(targets)
		b.WriteString(ct)
		b.WriteByte('=')
		b.WriteString(strings.Join(targets, ","))
		b.WriteByte(';')
	}
	return b.String()
}

// assertSameGraph fails if the resource+connection graphs of a (incremental) and
// b (full rescan) differ. Warnings are intentionally excluded: the incremental
// path derives them differently (e.g. signature-changed) from a cold full scan.
func assertSameGraph(t *testing.T, incr, full *domain.Topology) {
	t.Helper()
	for id, r := range full.Resources {
		ir, ok := incr.Resources[id]
		if !ok {
			t.Errorf("resource %q present in full rescan but missing after incremental", id)
			continue
		}
		if fp, ifp := resourceFingerprint(r), resourceFingerprint(ir); fp != ifp {
			t.Errorf("resource %q differs:\n full: %s\n incr: %s", id, fp, ifp)
		}
	}
	for id := range incr.Resources {
		if _, ok := full.Resources[id]; !ok {
			t.Errorf("resource %q present after incremental but absent from full rescan (stale row)", id)
		}
	}
}

// TestIncrementalWriteEqualsFullRescan is the core safety net for the scoped
// write path: a full scan, then a file modify + a new file added, applied
// incrementally, must yield the exact same resource/connection graph as a
// from-scratch full scan of the final tree.
func TestIncrementalWriteEqualsFullRescan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module equiv\n\ngo 1.21\n")
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "lib", "lib.go"), `package lib

type T struct{}

func (t *T) M() {}

func Helper() {}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "equiv/lib"

func main() {
	lib.Helper()
	x := &lib.T{}
	x.M()
}
`)

	incrDB := filepath.Join(dir, "incr.db")

	// 1. Initial full scan (no db yet -> FullReScan) into incrDB.
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)

	// 2. Mutate the tree: change main.go's body AND add a new file.
	writeFile(t, filepath.Join(dir, "helper2.go"), `package main

func Helper2() {}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "equiv/lib"

func main() {
	lib.Helper()
	Helper2()
	x := &lib.T{}
	x.M()
}
`)

	// 3. Incremental scan into incrDB (exercises WriteIncremental + scoped diff).
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)

	// 4. Authoritative: full scan of the final tree into a fresh db.
	fullDB := filepath.Join(dir, "full.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", fullDB)

	incr, err := helper.ReadDb(incrDB)
	if err != nil {
		t.Fatalf("read incr db: %v", err)
	}
	full, err := helper.ReadDb(fullDB)
	if err != nil {
		t.Fatalf("read full db: %v", err)
	}

	assertSameGraph(t, incr, full)

	// Sanity: the mutation's new edges actually landed (main -> Helper2, and the
	// cross-package transitive method call main -> lib.(T).M survived).
	main, ok := incr.Resources["equiv.main"]
	if !ok {
		t.Fatalf("equiv.main missing from incremental graph")
	}
	calls := strings.Join(main.Connections["calls"], ",")
	if !strings.Contains(calls, "equiv.Helper2") {
		t.Errorf("expected main to call equiv.Helper2 after incremental update, got calls=%v", main.Connections["calls"])
	}
}

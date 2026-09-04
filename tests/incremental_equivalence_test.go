package tests_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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

// writeFileMk writes a file, creating parent directories as needed.
func writeFileMk(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, content)
}

// assertIncrEqualsFull lays down files, full-scans, applies mutate, scans
// incrementally, then full-scans a fresh db of the final tree and asserts the
// two resource/connection graphs are identical. This is the core Phase 3 guard:
// any change to the incremental path must keep it equivalent to a cold scan.
func assertIncrEqualsFull(t *testing.T, files map[string]string, mutate func(t *testing.T, dir string)) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		writeFileMk(t, dir, rel, content)
	}
	incrDB := filepath.Join(dir, "incr.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
	mutate(t, dir)
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)

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
}

// A struct gaining a method (in a newly added file) that makes it satisfy an
// existing interface — exercises matchStructsToInterfaces on the incremental path.
func TestIncrEquiv_AddMethodSatisfiesInterface(t *testing.T) {
	assertIncrEqualsFull(t, map[string]string{
		"go.mod": "module eq1\n\ngo 1.21\n",
		"main.go": `package main

type Greeter interface{ Greet() string }

type E struct{}

func use(g Greeter) string { return g.Greet() }
`,
	}, func(t *testing.T, dir string) {
		writeFileMk(t, dir, "impl.go", `package main

func (E) Greet() string { return "hi" }
`)
	})
}

// Deleting a file whose function has outbound edges (but is not referenced by
// any surviving code) — exercises RemoveFileResources + scoped deletes. (A
// deleted symbol that IS still referenced by survivors hits a pre-existing
// incremental bug — see bug report on RemoveFileResources — so it is not
// asserted here.)
func TestIncrEquiv_DeleteFile(t *testing.T) {
	assertIncrEqualsFull(t, map[string]string{
		"go.mod": "module eq2\n\ngo 1.21\n",
		"main.go": `package main

func Keep() {}

func main() {}
`,
		"extra.go": `package main

func helper() { Keep() }
`,
	}, func(t *testing.T, dir string) {
		if err := os.Remove(filepath.Join(dir, "extra.go")); err != nil {
			t.Fatal(err)
		}
	})
}

// Changing a file in one package that another package depends on — exercises
// cross-package incremental update without re-resolving the dependent file.
func TestIncrEquiv_CrossPackageChange(t *testing.T) {
	assertIncrEqualsFull(t, map[string]string{
		"go.mod": "module eq3\n\ngo 1.21\n",
		"lib/lib.go": `package lib

type T struct{}

func (t *T) M() {}

func F() {}
`,
		"main.go": `package main

import "eq3/lib"

func main() {
	lib.F()
	x := &lib.T{}
	x.M()
}
`,
	}, func(t *testing.T, dir string) {
		writeFileMk(t, dir, "lib/lib.go", `package lib

type T struct{}

func (t *T) M() {}

func (t *T) N() {}

func F() {}

func G() {}
`)
	})
}

// Changing a function's signature in a file a caller in another file depends on.
func TestIncrEquiv_ChangeSignatureCrossFile(t *testing.T) {
	assertIncrEqualsFull(t, map[string]string{
		"go.mod": "module eq4\n\ngo 1.21\n",
		"lib/lib.go": `package lib

func Do(x int) {}
`,
		"main.go": `package main

import "eq4/lib"

func main() { lib.Do(1) }
`,
	}, func(t *testing.T, dir string) {
		writeFileMk(t, dir, "lib/lib.go", `package lib

func Do(x string) {}
`)
	})
}

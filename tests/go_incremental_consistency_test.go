package tests_test

// Go incremental scans must land the graph a cold scan of the same tree produces, on BOTH
// update paths: the scoped partial path a single-file edit takes, and the full two-phase path
// (forced here with ARAC_NO_PARTIAL, and taken for real by deletions and multi-file batches).
// Each test below reproduces one divergence from the release audit (GO-7, GO-10, GO-11, GO-13,
// SC-5, SC-6) and compares against a cold scan of the final tree.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// goScanPaths names the two incremental paths. "partial" requires the scoped path to have
// handled the edit (asserted through PartialIncrementalCount); "full" disables it. goIncrVsCold
// also takes "default": whichever path the scan picks.
var goScanPaths = []string{"partial", "full"}

// rewrite replaces a file and makes it look newer than the manifest.
func rewrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	writeFileMk(t, dir, rel, content)
	bumpMTime(t, filepath.Join(dir, rel))
}

// goIncrVsCold scans files cold, applies mutate, scans incrementally along path, then compares
// the result with a cold scan of the final tree: same resources, same edges, same spans. It
// returns the incremental graph for further assertions.
func goIncrVsCold(t *testing.T, path string, files map[string]string, mutate func(t *testing.T, dir string)) *domain.Topology {
	t.Helper()
	if path == "full" {
		t.Setenv("ARAC_NO_PARTIAL", "1")
	}
	dir := t.TempDir()
	for rel, content := range files {
		writeFileMk(t, dir, rel, content)
	}
	db := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := partialTestRegistry()
	mgr := topology.New()
	mgr.Load(db)
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	mutate(t, dir)
	before := topology.PartialIncrementalCount()
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("incremental scan: %v", err)
	}
	if path == "partial" && topology.PartialIncrementalCount() == before {
		t.Fatalf("the edit did not take the partial path")
	}

	coldDB := filepath.Join(t.TempDir(), "cold.db")
	cold := topology.New()
	cold.Load(coldDB)
	if _, err := cold.FullReScan(dir, reg); err != nil {
		t.Fatalf("cold scan: %v", err)
	}
	incrTopo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatal(err)
	}
	coldTopo, err := helper.ReadDb(coldDB)
	if err != nil {
		t.Fatal(err)
	}
	assertSameGraph(t, incrTopo, coldTopo)
	for id, c := range coldTopo.Resources {
		if i, ok := incrTopo.Resources[id]; ok && (i.Location.StartsAt != c.Location.StartsAt || i.Location.EndsAt != c.Location.EndsAt) {
			t.Errorf("%s spans %d-%d incrementally, %d-%d cold", id, i.Location.StartsAt, i.Location.EndsAt, c.Location.StartsAt, c.Location.EndsAt)
		}
	}
	return incrTopo
}

func edgeTo(topo *domain.Topology, src, kind, target string) bool {
	for _, t := range topo.Resources[src].Connections[kind] {
		if t == target {
			return true
		}
	}
	return false
}

const goModApp = "module example.com/app\n\ngo 1.22\n"

var repeatedInits = map[string]string{
	"go.mod": goModApp,
	"a.go":   "package app\n\nfunc helperA() {}\n\nfunc init() {\n\thelperA()\n}\n\nfunc Alpha() int { return 1 }\n\nfunc init() {\n\tAlpha()\n}\n",
	"b.go":   "package app\n\nfunc helperB() {}\n\nfunc init() {\n\thelperB()\n}\n",
}

// GO-7: editing a.go kept only a.go's init under the shared id, and b.go's init lost its edge.
func TestGoIncr_RepeatedInitsSurviveAnEdit(t *testing.T) {
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, repeatedInits, func(t *testing.T, dir string) {
				rewrite(t, dir, "a.go", repeatedInits["a.go"]+"\n// edited\n")
			})
			if !edgeTo(incr, "example.com/app.init#3", "calls", "example.com/app.helperB") {
				t.Errorf("b.go's init lost its call to helperB")
			}
		})
	}
}

// GO-7: a new file sorting first takes the plain id; every other init moves up one ordinal.
func TestGoIncr_RepeatedInitsRenumberOnAnEarlierFile(t *testing.T) {
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			goIncrVsCold(t, path, repeatedInits, func(t *testing.T, dir string) {
				rewrite(t, dir, "0.go", "package app\n\nfunc init() {\n\thelperB()\n}\n")
			})
		})
	}
}

// GO-7: deleting the file that holds the plain id hands it to the next declaration.
func TestGoIncr_RepeatedInitsDeleteTheFirstFile(t *testing.T) {
	incr := goIncrVsCold(t, "full", repeatedInits, func(t *testing.T, dir string) {
		if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
			t.Fatal(err)
		}
	})
	if !edgeTo(incr, "example.com/app.init", "calls", "example.com/app.helperB") {
		t.Errorf("b.go's init must now be the plain id")
	}
}

// GO-7 with build-tag variants: removing the first variant must not strip the callers' edge to
// the id the second one still declares, nor warn them it was removed.
func TestGoIncr_BuildTagVariantDeleted(t *testing.T) {
	files := map[string]string{
		"go.mod":          goModApp,
		"plat_linux.go":   "//go:build linux\n\npackage app\n\nfunc open() int { return lin() }\n\nfunc lin() int { return 1 }\n",
		"plat_windows.go": "//go:build windows\n\npackage app\n\nfunc open() int { return win() }\n\nfunc win() int { return 2 }\n",
		"use.go":          "package app\n\nfunc Use() int { return open() }\n",
	}
	incr := goIncrVsCold(t, "full", files, func(t *testing.T, dir string) {
		if err := os.Remove(filepath.Join(dir, "plat_linux.go")); err != nil {
			t.Fatal(err)
		}
	})
	if !edgeTo(incr, "example.com/app.Use", "calls", "example.com/app.open") {
		t.Errorf("Use lost its call to open")
	}
	for id := range incr.Warnings {
		if strings.HasPrefix(id, "example.com/app.Use@") {
			t.Errorf("Use was warned about a function that still exists: %s", id)
		}
	}
	// Removing the variant from the file instead: the plain id changes hands, which the partial
	// path hands to the full one (a caller in another file faces a different declaration).
	for _, path := range []string{"default", "full"} {
		t.Run("edit/"+path, func(t *testing.T) {
			goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "plat_linux.go", "//go:build linux\n\npackage app\n\nfunc lin() int { return 1 }\n")
			})
		})
	}
}

// A renamed file re-declares its own ids; it must not become the second declaration of each.
func TestGoIncr_RenamedFileKeepsPlainIDs(t *testing.T) {
	incr := goIncrVsCold(t, "full", repeatedInits, func(t *testing.T, dir string) {
		if err := os.Rename(filepath.Join(dir, "a.go"), filepath.Join(dir, "z.go")); err != nil {
			t.Fatal(err)
		}
	})
	for id := range incr.Resources {
		if strings.Contains(id, "Alpha#") || strings.Contains(id, "helperA#") {
			t.Errorf("rename minted an ordinal for a single declaration: %s", id)
		}
	}
}

// SC-5: a new file's bodies were resolved before its structs had their methods attached.
func TestGoIncr_NewFileCallsMethodOfItsOwnStruct(t *testing.T) {
	incr := goIncrVsCold(t, "partial", map[string]string{
		"go.mod":       goModApp,
		"shapes/sq.go": "package shapes\n\ntype Sq struct{ s float64 }\n\nfunc (q Sq) Area() float64 { return q.s * q.s }\n",
	}, func(t *testing.T, dir string) {
		rewrite(t, dir, "shapes/tri.go", "package shapes\n\ntype Tri struct{ b, h float64 }\n\nfunc (t Tri) Area() float64 { return t.b * t.h / 2 }\n\nfunc TriArea() float64 {\n\tt := Tri{}\n\treturn t.Area()\n}\n")
	})
	if !edgeTo(incr, "example.com/app/shapes.TriArea", "calls", "example.com/app/shapes.(Tri).Area") {
		t.Errorf("TriArea must call (Tri).Area")
	}
}

// SC-6: fixing a call to a function that did not exist dropped the warning without resolving
// the caller again, so the edge (and its call-site record) never appeared.
func TestGoIncr_MissingCalleeAppears(t *testing.T) {
	files := map[string]string{
		"go.mod":        goModApp,
		"shapes/s.go":   "package shapes\n\nfunc Double(x int) int { return x * 2 }\n",
		"consumer/c.go": "package consumer\n\nimport \"example.com/app/shapes\"\n\nfunc Report() int {\n\treturn shapes.Triple(1)\n}\n",
	}
	for _, path := range []string{"full"} {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "shapes/s.go", files["shapes/s.go"]+"\nfunc Triple(x int) int { return x * 3 }\n")
			})
			if !edgeTo(incr, "example.com/app/consumer.Report", "calls", "example.com/app/shapes.Triple") {
				t.Errorf("Report must call Triple once it exists")
			}
			if len(incr.Resources["example.com/app/consumer.Report"].Connections["__call_sites"]) == 0 {
				t.Errorf("the call-site record must be written too")
			}
			for id := range incr.Warnings {
				if strings.Contains(id, "Triple") {
					t.Errorf("stale warning %s", id)
				}
			}
		})
	}
}

// SC-6 on the default path: the partial path cannot re-resolve a file it did not load, so the
// edit must reach the full path and land the same graph.
func TestGoIncr_MissingCalleeAppearsDefaultPath(t *testing.T) {
	t.Setenv("ARAC_NO_PARTIAL", "")
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", goModApp)
	writeFileMk(t, dir, "shapes/s.go", "package shapes\n\nfunc Double(x int) int { return x * 2 }\n")
	writeFileMk(t, dir, "consumer/c.go", "package consumer\n\nimport \"example.com/app/shapes\"\n\nfunc Report() int {\n\treturn shapes.Triple(1)\n}\n")
	incrDB := filepath.Join(dir, "incr.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
	rewrite(t, dir, "shapes/s.go", "package shapes\n\nfunc Double(x int) int { return x * 2 }\n\nfunc Triple(x int) int { return x * 3 }\n")
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
	fullDB := filepath.Join(dir, "full.db")
	mustRun(t, dir, "scan", "--hard", "-root", dir, "-output", fullDB)
	incr, err := helper.ReadDb(incrDB)
	if err != nil {
		t.Fatal(err)
	}
	full, err := helper.ReadDb(fullDB)
	if err != nil {
		t.Fatal(err)
	}
	assertSameGraph(t, incr, full)
}

// GO-13: a named type appearing where a struct was (or out of nowhere) gave a signature in
// another file that already spelled it no uses_named_type edge.
func TestGoIncr_NamedTypeAppears(t *testing.T) {
	files := map[string]string{
		"go.mod":        goModApp,
		"shapes/s.go":   "package shapes\n\ntype Meter struct{ v float64 }\n",
		"consumer/c.go": "package consumer\n\nimport \"example.com/app/shapes\"\n\ntype Holder struct{ m shapes.Meter }\n\nfunc Conv(m shapes.Meter) int { return 1 }\n",
	}
	for _, path := range []string{"full"} {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "shapes/s.go", "package shapes\n\ntype Meter float64\n")
			})
			if !edgeTo(incr, "example.com/app/consumer.Conv", "uses_named_type", "example.com/app/shapes.Meter") {
				t.Errorf("Conv's signature names shapes.Meter")
			}
			if !edgeTo(incr, "example.com/app/consumer.Holder", "uses_named_type", "example.com/app/shapes.Meter") {
				t.Errorf("Holder's field names shapes.Meter")
			}
		})
	}
	// The default path must get there too (by handing the edit to the full path).
	t.Run("default", func(t *testing.T) {
		dir := t.TempDir()
		for rel, content := range files {
			writeFileMk(t, dir, rel, content)
		}
		incrDB := filepath.Join(dir, "incr.db")
		mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
		rewrite(t, dir, "shapes/s.go", "package shapes\n\ntype Meter float64\n")
		mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
		fullDB := filepath.Join(dir, "full.db")
		mustRun(t, dir, "scan", "--hard", "-root", dir, "-output", fullDB)
		incr, _ := helper.ReadDb(incrDB)
		full, _ := helper.ReadDb(fullDB)
		assertSameGraph(t, incr, full)
	})
}

// GO-13: removing the last import of a dependency left its node in the graph for good.
func TestGoIncr_LastImportOfDependencyRemoved(t *testing.T) {
	files := map[string]string{
		"go.mod": goModApp,
		"a.go":   "package app\n\nimport \"github.com/foo/bar\"\n\nfunc A() { bar.X() }\n",
		"b.go":   "package app\n\nimport \"github.com/foo/kept\"\n\nfunc B() { kept.Y() }\n",
		"c.go":   "package app\n\nimport \"github.com/foo/kept\"\n\nfunc C() { kept.Y() }\n",
	}
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "a.go", "package app\n\nfunc A() {}\n")
			})
			if _, ok := incr.Resources["github.com/foo/bar"]; ok {
				t.Errorf("a dependency nothing imports must be pruned")
			}
		})
		t.Run(path+"/still-imported", func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "b.go", "package app\n\nfunc B() {}\n")
			})
			if _, ok := incr.Resources["github.com/foo/kept"]; !ok {
				t.Errorf("a dependency c.go still imports must stay")
			}
		})
	}
}

// GO-13: the partial path stopped expanding its package closure after eight rounds, so a type
// chain crossing more packages than that resolved differently from a cold scan.
func TestGoIncr_DeepTypeChain(t *testing.T) {
	const depth = 11
	files := map[string]string{"go.mod": goModApp}
	for i := 1; i <= depth; i++ {
		pkg := "p" + itoaT(i)
		if i < depth {
			next := "p" + itoaT(i+1)
			files[pkg+"/p.go"] = "package " + pkg + "\n\nimport \"example.com/app/" + next + "\"\n\ntype T struct{}\n\nfunc (T) Next() " + next + ".T { return " + next + ".T{} }\n"
		} else {
			files[pkg+"/p.go"] = "package " + pkg + "\n\ntype T struct{}\n\nfunc (T) Leaf() int { return 1 }\n"
		}
	}
	files["p1/get.go"] = "package p1\n\nfunc Get() T { return T{} }\n"
	body := "package c\n\nimport \"example.com/app/p1\"\n\nfunc Use() int {\n\tv0 := p1.Get()\n"
	for i := 1; i < depth; i++ {
		body += "\tv" + itoaT(i) + " := v" + itoaT(i-1) + ".Next()\n"
	}
	body += "\treturn v" + itoaT(depth-1) + ".Leaf()\n}\n"
	files["c/c.go"] = body
	incr := goIncrVsCold(t, "partial", files, func(t *testing.T, dir string) {
		rewrite(t, dir, "c/c.go", body+"\n// edited\n")
	})
	if !edgeTo(incr, "example.com/app/c.Use", "calls", "example.com/app/p"+itoaT(depth)+".(T).Leaf") {
		t.Errorf("the last hop of the chain must resolve")
	}
}

// GO-11 on the update paths: a method added in another file makes a named type satisfy an
// interface.
func TestGoIncr_MethodOnNamedTypeInAnotherFile(t *testing.T) {
	files := map[string]string{
		"go.mod": goModApp,
		"i.go":   "package app\n\ntype Stringer interface {\n\tString() string\n}\n",
		"t.go":   "package app\n\ntype Celsius float64\n\ntype Kelvin float64\n",
	}
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "m.go", "package app\n\nfunc (k Kelvin) String() string { return \"k\" }\n")
			})
			if !edgeTo(incr, "example.com/app.Kelvin", "implements", "example.com/app.Stringer") {
				t.Errorf("Kelvin must implement Stringer")
			}
		})
	}
}

// GO-10 on the update paths: an edit inside a nested module keeps its module's ids.
func TestGoIncr_NestedModuleEdit(t *testing.T) {
	files := map[string]string{
		"go.mod":         goModApp,
		"main.go":        "package app\n\nfunc Main() int { return 1 }\n",
		"sub/go.mod":     "module example.com/sub\n\ngo 1.22\n",
		"sub/s.go":       "package sub\n\nimport \"example.com/sub/inner\"\n\nfunc Use() int { return inner.F() }\n",
		"sub/inner/i.go": "package inner\n\nfunc F() int { return 2 }\n\nfunc G() int { return 3 }\n",
	}
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			incr := goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "sub/s.go", "package sub\n\nimport \"example.com/sub/inner\"\n\nfunc Use() int { return inner.F() + inner.G() }\n")
			})
			if !edgeTo(incr, "example.com/sub.Use", "calls", "example.com/sub/inner.G") {
				t.Errorf("Use must call inner.G inside the nested module")
			}
		})
	}
}

// GO-13: with two New* functions for one struct the constructor depended on which file was
// re-parsed last.
func TestGoIncr_ConstructorChoiceStable(t *testing.T) {
	files := map[string]string{
		"go.mod": goModApp,
		"a.go":   "package app\n\ntype Circle struct{ r float64 }\n\nfunc NewCircle(r float64) *Circle { return &Circle{r} }\n",
		"b.go":   "package app\n\nfunc NewUnitCircle() *Circle { return &Circle{1} }\n",
	}
	for _, path := range goScanPaths {
		t.Run(path, func(t *testing.T) {
			goIncrVsCold(t, path, files, func(t *testing.T, dir string) {
				rewrite(t, dir, "a.go", files["a.go"]+"\n// edited\n")
			})
		})
	}
}

func itoaT(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoaT(i/10) + string(rune('0'+i%10))
}

package goscanner

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

func scanTree(t *testing.T, files map[string]string) (string, *domain.Topology) {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	topo.Root = dir
	return dir, topo
}

func mustResource(t *testing.T, topo *domain.Topology, id string) domain.Resource {
	t.Helper()
	res, ok := topo.Resources[id]
	if !ok {
		ids := resourceIDs(topo)
		sort.Strings(ids)
		t.Fatalf("missing %s; ids: %v", id, ids)
	}
	return res
}

func resourceHasEdge(res domain.Resource, kind, target string) bool {
	for _, t := range res.Connections[kind] {
		if t == target {
			return true
		}
	}
	return false
}

// GO-7: a package may declare init (and _) any number of times, in any number of files. Every
// declaration is its own node; the first in file order keeps the plain id.
func TestRepeatedInitsAreSeparateNodes(t *testing.T) {
	dir, topo := scanTree(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"a.go":   "package app\n\nfunc helperA() {}\n\nfunc init() {\n\thelperA()\n}\n\nfunc Alpha() int { return 1 }\n\nfunc init() {\n\tAlpha()\n}\n",
		"b.go":   "package app\n\nfunc helperB() {}\n\nfunc init() {\n\thelperB()\n}\n\nfunc _() {}\n",
		"c.go":   "package app\n\nfunc _() {}\n",
	})
	a, b, c := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go"), filepath.Join(dir, "c.go")
	for _, want := range []struct {
		id, path string
		line     int
		calls    string
	}{
		{"example.com/app.init", a, 5, "example.com/app.helperA"},
		{"example.com/app.init#2", a, 11, "example.com/app.Alpha"},
		{"example.com/app.init#3", b, 5, "example.com/app.helperB"},
		{"example.com/app._", b, 9, ""},
		{"example.com/app._#2", c, 3, ""},
	} {
		res := mustResource(t, topo, want.id)
		if res.Location.Path != want.path || res.Location.StartsAt != want.line {
			t.Errorf("%s at %s:%d, want %s:%d", want.id, res.Location.Path, res.Location.StartsAt, want.path, want.line)
		}
		if want.calls != "" && (len(res.Connections["calls"]) != 1 || res.Connections["calls"][0] != want.calls) {
			t.Errorf("%s calls %v, want only %s", want.id, res.Connections["calls"], want.calls)
		}
	}
	if !resourceHasEdge(topo.Resources[b], "has_function", "example.com/app.init#3") || resourceHasEdge(topo.Resources[b], "has_function", "example.com/app.init") {
		t.Errorf("b.go must own init#3 and only it, got %v", topo.Resources[b].Connections["has_function"])
	}
}

// The pinned side of GO-7: one declaration keeps the id every existing graph holds, and a call
// to a build-tag variant resolves to the plain id.
func TestSingleDeclarationKeepsPlainID(t *testing.T) {
	_, topo := scanTree(t, map[string]string{
		"go.mod":          "module example.com/app\n\ngo 1.22\n",
		"inits.go":        "package app\n\nvar ready bool\n\nfunc init() {\n\tready = true\n}\n",
		"plat_linux.go":   "//go:build linux\n\npackage app\n\nfunc open() int { return 1 }\n",
		"plat_windows.go": "//go:build windows\n\npackage app\n\nfunc open() int { return 2 }\n",
		"use.go":          "package app\n\nfunc Use() int { return open() }\n",
	})
	mustResource(t, topo, "example.com/app.init")
	if _, ok := topo.Resources["example.com/app.init#2"]; ok {
		t.Error("a single init must not get an ordinal")
	}
	open := mustResource(t, topo, "example.com/app.open")
	if filepath.Base(open.Location.Path) != "plat_linux.go" {
		t.Errorf("the plain id belongs to the first variant in file order, got %s", open.Location.Path)
	}
	if filepath.Base(mustResource(t, topo, "example.com/app.open#2").Location.Path) != "plat_windows.go" {
		t.Error("the second variant is open#2")
	}
	if !resourceHasEdge(topo.Resources["example.com/app.Use"], "calls", "example.com/app.open") {
		t.Errorf("Use must call the plain id, got %v", topo.Resources["example.com/app.Use"].Connections["calls"])
	}
}

// GO-7 on the update path: editing one file that declares init must leave every other
// declaration exactly as a cold scan has it.
func TestUpdateFileKeepsSiblingInits(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"a.go":   "package app\n\nfunc helperA() {}\n\nfunc init() {\n\thelperA()\n}\n",
		"b.go":   "package app\n\nfunc helperB() {}\n\nfunc init() {\n\thelperB()\n}\n",
	}
	dir, topo := scanTree(t, files)
	// Give a.go a second init, which pushes b.go's from #2 to #3.
	writeTree(t, dir, map[string]string{"a.go": "package app\n\nfunc helperA() {}\n\nfunc init() {\n\thelperA()\n}\n\nfunc init() {}\n"})
	if _, err := NewGoScanner().UpdateFile(topo, filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	cold, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example.com/app.init", "example.com/app.init#2", "example.com/app.init#3"} {
		got, want := mustResource(t, topo, id), mustResource(t, cold, id)
		if got.Location != want.Location {
			t.Errorf("%s at %+v after update, cold scan has %+v", id, got.Location, want.Location)
		}
		if len(got.Connections["calls"]) != len(want.Connections["calls"]) {
			t.Errorf("%s calls %v after update, cold scan %v", id, got.Connections["calls"], want.Connections["calls"])
		}
	}
	if !resourceHasEdge(topo.Resources["example.com/app.init#3"], "calls", "example.com/app.helperB") {
		t.Error("b.go's init lost its edge")
	}
	if !resourceHasEdge(topo.Resources[filepath.Join(dir, "b.go")], "has_function", "example.com/app.init#3") {
		t.Errorf("b.go must list its renamed init, got %v", topo.Resources[filepath.Join(dir, "b.go")].Connections["has_function"])
	}
}

// GO-11: methods on a non-struct defined type belong to it and make it satisfy interfaces.
func TestMethodsOnNamedTypes(t *testing.T) {
	_, topo := scanTree(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"h.go": `package app

type Server interface {
	Serve(n int) int
}

type Stringer interface {
	String() string
}

type HandlerFunc func(int) int

func (f HandlerFunc) Serve(n int) int { return f(n) }

type Celsius float64

func (c Celsius) String() string { return "c" }

type Plain int
`,
	})
	hf := mustResource(t, topo, "example.com/app.HandlerFunc")
	if !resourceHasEdge(hf, "methods", "example.com/app.(HandlerFunc).Serve") || !resourceHasEdge(hf, "implements", "example.com/app.Server") {
		t.Errorf("HandlerFunc: methods=%v implements=%v", hf.Connections["methods"], hf.Connections["implements"])
	}
	c := mustResource(t, topo, "example.com/app.Celsius")
	if !resourceHasEdge(c, "implements", "example.com/app.Stringer") || resourceHasEdge(c, "implements", "example.com/app.Server") {
		t.Errorf("Celsius implements %v, want only Stringer", c.Connections["implements"])
	}
	if !resourceHasEdge(mustResource(t, topo, "example.com/app.Server"), "implemented_by", "example.com/app.HandlerFunc") {
		t.Error("Server must list HandlerFunc as implemented_by")
	}
	if p := mustResource(t, topo, "example.com/app.Plain"); len(p.Connections["implements"]) > 0 || len(p.Connections["methods"]) > 0 {
		t.Errorf("a type without methods implements nothing, got %v", p.Connections)
	}
}

// GO-12: a generic constructor's result carries type arguments; the struct is found anyway.
func TestGenericConstructorDetected(t *testing.T) {
	_, topo := scanTree(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"b.go": `package app

type Box[T any] struct{ v T }

func NewBox[T any]() *Box[T] { return &Box[T]{} }

type Pair[K comparable, V any] struct {
	k K
	v V
}

func NewPair[K comparable, V any](k K, v V) Pair[K, V] { return Pair[K, V]{k, v} }

type Plain struct{}

func NewPlain() *Plain { return &Plain{} }

func NewSlice() []Plain { return nil }
`,
	})
	for sid, ctor := range map[string]string{
		"example.com/app.Box":   "example.com/app.NewBox",
		"example.com/app.Pair":  "example.com/app.NewPair",
		"example.com/app.Plain": "example.com/app.NewPlain",
	} {
		if got, _ := mustResource(t, topo, sid).Properties["constructor"].(string); got != ctor {
			t.Errorf("%s constructor = %q, want %q", sid, got, ctor)
		}
	}
}

// GO-13: with several New* functions for one struct, the choice must not depend on the order
// the functions are listed in -- the partial path listed them from a map.
func TestConstructorChoiceIsDeterministic(t *testing.T) {
	mk := func(id, name, path string, line int) golang.GolangFunction {
		return golang.GolangFunction{ID: id, Name: name, Loc: domain.Location{Path: path, StartsAt: line},
			Output: []golang.VariableDefinition{{Typing: "*Circle"}}}
	}
	fns := []golang.GolangFunction{
		mk("p.NewCircle", "NewCircle", "/r/a.go", 5),
		mk("p.NewUnit", "NewUnit", "/r/b.go", 3),
		mk("p.NewTiny", "NewTiny", "/r/a.go", 9),
	}
	orders := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {1, 2, 0}}
	for _, order := range orders {
		gt := &golang.GolangTopology{
			Functions: map[golang.FunctionID]golang.GolangFunction{},
			Structs:   map[golang.StructID]golang.GolangStruct{"p.Circle": {ID: "p.Circle", Name: "Circle"}},
		}
		var ids []golang.FunctionID
		for _, i := range order {
			gt.Functions[fns[i].ID] = fns[i]
			ids = append(ids, fns[i].ID)
		}
		assignConstructors(gt, "p", ids, func(golang.StructID) bool { return true })
		if got := gt.Structs["p.Circle"].Constructor; got == nil || *got != "p.NewUnit" {
			t.Errorf("order %v: constructor %v, want the last in file order (p.NewUnit)", order, got)
		}
	}
}

// Before this, the partial path walked gt.Functions -- a map -- to pick the constructor.
func TestDetectConstructorsIncrementalMatchesFull(t *testing.T) {
	for i := 0; i < 20; i++ {
		gt := &golang.GolangTopology{
			Functions: map[golang.FunctionID]golang.GolangFunction{},
			Structs:   map[golang.StructID]golang.GolangStruct{"p.Circle": {ID: "p.Circle", Name: "Circle"}},
			Packages:  map[golang.PackagePath]golang.GolangPackage{},
		}
		var ids []string
		for j, name := range []string{"NewA", "NewB", "NewC", "NewD", "NewE"} {
			id := "p." + name
			gt.Functions[id] = golang.GolangFunction{ID: id, Name: name, Loc: domain.Location{Path: "/r/x.go", StartsAt: j + 1},
				Output: []golang.VariableDefinition{{Typing: "Circle"}}}
			ids = append(ids, id)
		}
		detectConstructorsIncremental(gt, &ParseResult{PkgPath: "p"})
		if got := gt.Structs["p.Circle"].Constructor; got == nil || *got != "p.NewE" {
			t.Fatalf("run %d: incremental picked %v, want p.NewE", i, got)
		}
		gt.Packages["p"] = golang.GolangPackage{Path: "p", Connections: map[golang.ConnectionKind][]string{golang.ConnHasFunc: ids}}
		detectConstructors(gt)
		if got := gt.Structs["p.Circle"].Constructor; got == nil || *got != "p.NewE" {
			t.Fatalf("run %d: full picked %v, want p.NewE", i, got)
		}
	}
}

func TestBaseFunctionID(t *testing.T) {
	for in, want := range map[string]struct {
		base string
		n    int
	}{
		"p.init":      {"p.init", 1},
		"p.init#2":    {"p.init", 2},
		"p.(T)._#12":  {"p.(T)._", 12},
		"p.init#x":    {"p.init#x", 1},
		"p.init#1":    {"p.init#1", 1},
		"a.b/c.d.F#3": {"a.b/c.d.F", 3},
	} {
		base, n := baseFunctionID(in)
		if base != want.base || n != want.n {
			t.Errorf("baseFunctionID(%q) = %q, %d; want %q, %d", in, base, n, want.base, want.n)
		}
	}
}

// GO-7: a file that stops declaring one build-tag variant hands the plain id to the next; the
// callers still resolve to it, so nothing may tell them it was removed.
func TestVariantTakeoverIsNotARemoval(t *testing.T) {
	dir, topo := scanTree(t, map[string]string{
		"go.mod":          "module example.com/app\n\ngo 1.22\n",
		"plat_linux.go":   "//go:build linux\n\npackage app\n\nfunc open() int { return 1 }\n\nfunc lin() {}\n",
		"plat_windows.go": "//go:build windows\n\npackage app\n\nfunc open() int { return 2 }\n",
		"use.go":          "package app\n\nfunc Use() int { return open() }\n",
	})
	writeTree(t, dir, map[string]string{"plat_linux.go": "//go:build linux\n\npackage app\n\nfunc lin() {}\n"})
	reported, err := NewGoScanner().UpdateFile(topo, filepath.Join(dir, "plat_linux.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range reported {
		t.Errorf("reported %s: %s", w.ID, w.Message)
	}
	for id := range topo.Warnings {
		t.Errorf("stored warning %s", id)
	}
	if open := mustResource(t, topo, "example.com/app.open"); filepath.Base(open.Location.Path) != "plat_windows.go" {
		t.Errorf("the windows variant must hold the plain id now, got %s", open.Location.Path)
	}
	if !resourceHasEdge(topo.Resources["example.com/app.Use"], "calls", "example.com/app.open") {
		t.Error("Use lost its call")
	}
	if _, ok := topo.Resources["example.com/app.open#2"]; ok {
		t.Error("open#2 must be gone")
	}
}

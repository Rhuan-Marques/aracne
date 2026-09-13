package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// An incremental scan must equal a cold one for edits that DELETE or RENAME something too.
//
// Removing a file strips every edge that points at what it held, but a scanner rebuilds some
// relationships whole-graph from records and method sets -- `Circle implements Shape` is
// justified by an `impl Shape for Circle` that may live in a third file, a Go type implements an
// interface because of methods that may live in another file -- and nothing re-ran those passes
// after the removal. Renaming a Rust type away from a body-less `impl` in another file left that
// file's methods attached to a type that no longer existed. Each case names the edge or node the
// edit is about, so a comparison that passes because BOTH scans got it wrong fails.

type deletionCase struct {
	name    string
	files   map[string]string // the tree before the edit
	remove  []string          // files the edit deletes
	edit    map[string]string // files the edit writes, new or rewritten
	gone    []string          // edges ("src -kind-> tgt") or resource ids the cold scan must NOT have
	present []string          // edges the cold scan must still have
}

const deletionShapesRS = "pub trait Shape {\n    fn area(&self) -> f64;\n}\n\npub struct Circle {\n    pub r: f64,\n}\n"

const deletionImplsRS = "use crate::shapes::*;\n\nimpl Circle {\n    pub fn radius(&self) -> f64 {\n        self.r\n    }\n}\n\n" +
	"impl Shape for Circle {\n    fn area(&self) -> f64 {\n        self.r * self.r\n    }\n}\n"

func deletionCases() []deletionCase {
	rust := func() map[string]string {
		return map[string]string{
			"Cargo.toml":    additionCargo,
			"src/lib.rs":    "pub mod shapes;\npub mod impls;\n",
			"src/shapes.rs": deletionShapesRS,
			"src/impls.rs":  deletionImplsRS,
		}
	}
	return []deletionCase{
		{
			name:   "rust: the file holding `impl Shape for Circle` is deleted",
			files:  rust(),
			remove: []string{"src/impls.rs"},
			gone: []string{
				"q::shapes::Circle -implements-> q::shapes::Shape",
				"q::shapes::Shape -implemented_by-> q::shapes::Circle",
			},
		},
		{
			name:   "rust: the type a cross-file impl attaches to is deleted",
			files:  rust(),
			remove: []string{"src/shapes.rs"},
			gone:   []string{"q::shapes::Circle::radius", "q::shapes::Circle::area"},
		},
		{
			name:  "rust: the type a body-less cross-file impl attaches to is renamed",
			files: rust(),
			edit: map[string]string{
				"src/shapes.rs": strings.ReplaceAll(deletionShapesRS, "Circle", "Round"),
			},
			gone: []string{"q::shapes::Circle::radius", "q::shapes::Circle::area"},
		},
		{
			name:  "rust: the trait a cross-file impl names is renamed",
			files: rust(),
			edit: map[string]string{
				"src/shapes.rs": strings.ReplaceAll(deletionShapesRS, "Shape", "Form"),
			},
			gone: []string{"q::shapes::Circle -implements-> q::shapes::Shape"},
		},
		{
			name: "java: the interface a class implements is deleted",
			files: map[string]string{
				"pom.xml": additionPom,
				"src/main/java/com/ex/api/Shape.java": "package com.ex.api;\n\npublic interface Shape {\n" +
					"    double area();\n}\n",
				"src/main/java/com/ex/Circle.java": "package com.ex;\n\nimport com.ex.api.Shape;\n\n" +
					"public class Circle implements Shape {\n    public double area() { return 1.0; }\n}\n",
			},
			remove: []string{"src/main/java/com/ex/api/Shape.java"},
			gone:   []string{"com.ex.Circle -implements-> com.ex.api.Shape"},
		},
		{
			name: "go: the file holding the method that satisfied an interface is deleted",
			files: map[string]string{
				"go.mod":     "module example.com/g\n\ngo 1.21\n",
				"s/shape.go": "package s\n\ntype Shape interface {\n\tArea() float64\n}\n\ntype Rect struct{ W, H float64 }\n",
				"s/area.go":  "package s\n\nfunc (r Rect) Area() float64 { return r.W * r.H }\n",
			},
			remove: []string{"s/area.go"},
			gone:   []string{"example.com/g/s.Rect -implements-> example.com/g/s.Shape"},
		},
		{
			name: "go: a package's last file is deleted",
			files: map[string]string{
				"go.mod":      "module example.com/orph\n\ngo 1.21\n",
				"pkg/p.go":    "package pkg\n\nimport \"strings\"\n\nfunc Up(s string) string { return strings.ToUpper(s) }\n",
				"app/main.go": "package app\n\nimport \"fmt\"\n\nfunc Run() { fmt.Println(\"x\") }\n",
			},
			remove:  []string{"pkg/p.go"},
			gone:    []string{"example.com/orph/pkg", "strings"},
			present: []string{"example.com/orph/app -has_function-> example.com/orph/app.Run"},
		},
		{
			name: "javascript: a callee's file is deleted",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"a.js":         "export function f(x) { return x; }\n",
				"b.js":         "import { f } from './a.js';\nexport function run() { return f(1); }\n",
			},
			remove: []string{"a.js"},
			gone:   []string{"a.f"},
		},
	}
}

func TestIncrementalScanMatchesColdScanAfterADeletion(t *testing.T) {
	for _, c := range deletionCases() {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			baseline, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			baseEdges := additionEdges(baseline)
			for _, g := range c.gone {
				if _, isRes := baseline.Resources[g]; !isRes && !baseEdges[g] {
					t.Fatalf("the baseline has no %q, so this case proves nothing", g)
				}
			}

			for _, rel := range c.remove {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			}
			additionWrite(t, dir, c.edit, time.Now().Add(3*time.Second))
			if _, err := mgr.IncrementalScan(dir, reg); err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			incremental, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			cold := topology.New()
			cold.Load(filepath.Join(t.TempDir(), "cold.db"))
			if err := cold.FullScan(dir, reg); err != nil {
				t.Fatalf("cold FullScan: %v", err)
			}
			fresh, err := cold.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			coldEdges := additionEdges(fresh)
			for _, g := range c.gone {
				if _, isRes := fresh.Resources[g]; isRes || coldEdges[g] {
					t.Fatalf("the cold scan still has %q, so this case proves nothing", g)
				}
			}
			for _, edge := range c.present {
				if !coldEdges[edge] {
					t.Fatalf("the cold scan has no %q", edge)
				}
			}
			if diff := additionDiff(incremental, fresh); len(diff) > 0 {
				t.Errorf("incremental scan differs from a cold scan (- incremental only, + cold only):\n  %s",
					strings.Join(diff, "\n  "))
			}
		})
	}
}

// A deleted file's callers are warned and stripped rather than re-parsed, so the whole graph
// need not match a cold scan there (Go keeps `uses_package`, Python does not yet re-classify
// the dangling import). What must not survive is the private `__call_sites` record naming the
// removed callee: a cold scan cannot resolve the call, so it records nothing.
func TestDeletedCalleeLeavesNoCallSiteRecord(t *testing.T) {
	for _, c := range []struct {
		name   string
		files  map[string]string
		remove string
		callee string
	}{
		{
			name: "go",
			files: map[string]string{
				"go.mod":     "module example.com/w\n\ngo 1.21\n",
				"lib/lib.go": "package lib\n\nfunc Take(a int) int { return a }\n",
				"lib/o.go":   "package lib\n\nfunc Other() int { return 1 }\n",
				"app/app.go": "package app\n\nimport \"example.com/w/lib\"\n\nfunc Run() int { return lib.Take(1) }\n",
			},
			remove: "lib/lib.go",
			callee: "example.com/w/lib.Take",
		},
		{
			name: "python",
			files: map[string]string{
				"a.py": "def f(x):\n    return x\n",
				"b.py": "from a import f\n\n\ndef run():\n    return f(1)\n",
				"c.py": "def z():\n    return 0\n",
			},
			remove: "a.py",
			callee: "a.f",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			before, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(callSitesNaming(before, c.callee)) == 0 {
				t.Fatalf("the baseline records no call to %s, so this case proves nothing", c.callee)
			}

			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(c.remove))); err != nil {
				t.Fatal(err)
			}
			if _, err := mgr.IncrementalScan(dir, reg); err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			after, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if left := callSitesNaming(after, c.callee); len(left) > 0 {
				t.Errorf("call-site records still name the deleted %s: %v", c.callee, left)
			}
		})
	}
}

func callSitesNaming(topo *domain.Topology, callee string) []string {
	var out []string
	for id, res := range topo.Resources {
		for _, rec := range res.Connections["__call_sites"] {
			if strings.HasPrefix(rec, callee+"=>>") {
				out = append(out, id+" -> "+rec)
			}
		}
	}
	return out
}

// A file that failed to parse and is then deleted must stop being reported as a scan error. It
// never produced a node, so the removal used to find nothing to remove -- and nothing re-parses
// a deleted file, so the error stood until `scan --all`.
func TestDeletedFileTakesItsScanErrorWithIt(t *testing.T) {
	// CANONICAL: topo.Errors is keyed by the path the scan recorded, which it resolves first.
	dir := helper.CanonicalPath(t.TempDir())
	additionWrite(t, dir, map[string]string{
		"a.py": "def ok():\n    return 1\n",
		"b.py": "def bad(:\n    return 1\n",
	}, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	bad := filepath.Join(dir, "b.py")
	before, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, reported := before.Errors[bad]; !reported {
		t.Fatalf("the unparseable file was not reported, so this case proves nothing: %v", before.Errors)
	}

	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	after, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if msg, still := after.Errors[bad]; still {
		t.Errorf("the deleted file is still reported as a scan error: %s", msg)
	}
}

// The other route to the same stuck error: a deleted file that was also a caller of something
// whose signature changed in the same batch was re-parsed as a reverse caller before being
// removed -- which only recorded "no such file".
func TestDeletedReverseCallerIsNotReparsed(t *testing.T) {
	dir := t.TempDir()
	additionWrite(t, dir, map[string]string{
		"package.json": `{"name":"x"}`,
		"a.js":         "export function f(x) { return x; }\n",
		"b.js":         "import { f } from './a.js';\nexport function run() { return f(1); }\n",
		"c.js":         "export function z() {}\n",
	}, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	additionWrite(t, dir, map[string]string{"a.js": "export function f(x, y) { return x; }\n"},
		time.Now().Add(3*time.Second))
	if err := os.Remove(filepath.Join(dir, "b.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	after, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Errors) > 0 {
		t.Errorf("a deleted file must not be reported as a scan error: %v", after.Errors)
	}
}

// The single-file verb removes a deleted path the same way -- it is how OpenCode's edit-sync
// plugin and the `arac update-file` hook see a rename -- so it owes the same re-settling.
func TestUpdateFileOnADeletedPathRebuildsImplements(t *testing.T) {
	dir := t.TempDir()
	additionWrite(t, dir, map[string]string{
		"Cargo.toml":    additionCargo,
		"src/lib.rs":    "pub mod shapes;\npub mod impls;\n",
		"src/shapes.rs": deletionShapesRS,
		"src/impls.rs":  deletionImplsRS,
	}, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	const edge = "q::shapes::Circle -implements-> q::shapes::Shape"
	before, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !additionEdges(before)[edge] {
		t.Fatalf("the baseline has no %q, so this case proves nothing", edge)
	}

	impls := filepath.Join(dir, "src", "impls.rs")
	if err := os.Remove(impls); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.UpdateFile(impls, reg); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	after, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if additionEdges(after)[edge] {
		t.Errorf("%q outlived the file holding the impl that justified it", edge)
	}
}

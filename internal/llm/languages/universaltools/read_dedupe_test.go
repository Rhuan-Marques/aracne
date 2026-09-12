package universaltools_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/javascanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
)

// scanWithJava is scanFiles plus the Java scanner, and an optional config written where the
// scan reads it from (so a scan-time setting such as read.max_file_size applies).
func scanWithJava(t *testing.T, files map[string]string, cfg *helper.Config) (*topology.TopologyManager, *scanner.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
			t.Fatal(err)
		}
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(javascanner.NewJavaScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, reg, dir
}

// quietConfig is the default config with lazy descriptions off, so no read in these tests can
// reach for a description provider -- whatever API keys the environment happens to hold.
func quietConfig() *helper.Config {
	cfg := helper.DefaultConfig()
	off := false
	cfg.Descriptions.Lazy.Enabled = &off
	return cfg
}

func readOK(t *testing.T, rd *universaltools.Read, ids ...string) string {
	t.Helper()
	out, err := rd.ReadIDs(ids, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatalf("read %v: %v", ids, err)
	}
	return out
}

func mustCount(t *testing.T, out, needle string, want int) {
	t.Helper()
	if n := strings.Count(out, needle); n != want {
		t.Fatalf("%q appears %d times, want %d:\n%s", needle, n, want, out)
	}
}

const pyCircle = "class Circle:\n" +
	"    def __init__(self, r):\n        self.r = r\n\n" +
	"    def area(self):\n        return 3.14159 * self.r * self.r\n\n" +
	"    def describe(self):\n        return 'circle'\n"

const jsPoint = "class Point {\n  constructor(x, y) {\n    this.x = x;\n    this.y = y;\n  }\n" +
	"  norm() {\n    return Math.sqrt(this.x * this.x + this.y * this.y);\n  }\n" +
	"  label() {\n    return 'p';\n  }\n}\nmodule.exports = { Point };\n"

const javaOps = "package com.t;\n\npublic class ArrayOps {\n" +
	"    public int total(int[] xs) {\n        int s = 0;\n        for (int x : xs) {\n            s += x;\n        }\n        return s;\n    }\n\n" +
	"    public String join(String[] xs) {\n        return String.join(\",\", xs);\n    }\n}\n"

const goCircle = "package shapes\n\n// Circle is round.\ntype Circle struct {\n\tR float64\n}\n\n" +
	"// Area computes area.\nfunc (c Circle) Area() float64 {\n\treturn c.R * c.R * 3\n}\n\n" +
	"// Perim computes perimeter.\nfunc (c Circle) Perim() float64 {\n\treturn c.R * 6\n}\n"

// RD-6: batching a class with its own member, or two members of one class, printed the same
// source twice. In Python, JS and Java a class cut is the whole class, so once it is on the page
// every member is too -- in whichever order the batch names them.
func TestBatchedClassAndMembersPrintSourceOnce(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"shapes.py":                    pyCircle,
		"package.json":                 `{"name":"p"}`,
		"a.js":                         jsPoint,
		"pom.xml":                      "<project></project>\n",
		"src/main/java/com/t/Ops.java": javaOps,
	}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)

	cases := []struct {
		name    string
		ids     []string
		class   string
		members []string
	}{
		{"py two members", []string{"shapes.Circle.area", "shapes.Circle.describe"},
			"class Circle:", []string{"def area(self):", "def describe(self):"}},
		{"py class then member", []string{"shapes.Circle", "shapes.Circle.area"},
			"class Circle:", []string{"def area(self):"}},
		{"py member then class", []string{"shapes.Circle.area", "shapes.Circle"},
			"class Circle:", []string{"def area(self):"}},
		{"js two members", []string{"a.Point.norm", "a.Point.label"},
			"class Point {", []string{"norm() {", "label() {"}},
		{"js class then member", []string{"a.Point", "a.Point.norm"},
			"class Point {", []string{"norm() {"}},
		{"java two members", []string{"ArrayOps.total(int[])", "ArrayOps.join(String[])"},
			"public class ArrayOps {", []string{"public int total(int[] xs) {", "public String join(String[] xs) {"}},
		{"java class then member", []string{"com.t.ArrayOps", "ArrayOps.join(String[])"},
			"public class ArrayOps {", []string{"public String join(String[] xs) {"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := readOK(t, rd, tc.ids...)
			mustCount(t, out, tc.class, 1)
			for _, m := range tc.members {
				mustCount(t, out, m, 1)
			}
			if strings.Contains(out, "# UNRESOLVED") {
				t.Fatalf("every id should resolve:\n%s", out)
			}
		})
	}
}

// The Go half of RD-6: a struct batched with its methods printed the type declaration twice,
// because the struct registered its cut with the batch ledger only after every body existed.
// The per-method marker for an already-shown receiver is the intended Go rendering and stays.
func TestBatchedGoStructAndMethodsPrintTheTypeOnce(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/shape.go": goCircle,
	}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	for _, ids := range [][]string{
		{"shapes.Circle", "shapes.(Circle).Area", "shapes.(Circle).Perim"},
		{"shapes.(Circle).Area", "shapes.Circle"},
	} {
		out := readOK(t, rd, ids...)
		mustCount(t, out, "type Circle struct {", 1)
		mustCount(t, out, "func (c Circle) Area() float64 {", 1)
	}
	// Two methods of one struct: the second still gets its source, after a marker for the
	// receiver it shares with the first.
	out := readOK(t, rd, "shapes.(Circle).Area", "shapes.(Circle).Perim")
	mustCount(t, out, "type Circle struct {", 1)
	mustCount(t, out, "func (c Circle) Perim() float64 {", 1)
	mustCount(t, out, "enclosing type already shown in this response", 1)
}

// A class too long to inline rides along with none of its members, so a member batched with it
// printed only itself -- and the class read printed it again. The ledger never sees that pair;
// the batch-level check does.
func TestBatchedLargeClassAndMemberPrintSourceOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("class Big:\n    def target(self):\n        return 'target'\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "    def m%d(self):\n        return %d\n\n", i, i)
	}
	mgr, reg, _ := scanWithJava(t, map[string]string{"big.py": b.String()}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	for _, ids := range [][]string{{"big.Big", "big.Big.target"}, {"big.Big.target", "big.Big"}} {
		out := readOK(t, rd, ids...)
		mustCount(t, out, "class Big:", 1)
		mustCount(t, out, "def target(self):", 1)
	}
}

// The batch-level check must not swallow a member the class read did not actually print: over
// read.max_symbol_lines the class is abridged, and a member in the elided part has to keep its
// own body.
func TestAbridgedClassDoesNotSwallowItsMember(t *testing.T) {
	var b strings.Builder
	b.WriteString("class Big:\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "    def m%d(self):\n        return %d\n\n", i, i)
	}
	b.WriteString("    def last(self):\n        return 'last'\n")
	mgr, reg, _ := scanWithJava(t, map[string]string{"big.py": b.String()}, nil)
	cfg := quietConfig()
	limit := 20
	cfg.Read.MaxSymbolLines = &limit
	out := readOK(t, universaltools.NewRead(mgr, cfg, false, reg), "big.Big", "big.Big.last")
	// The abridged class elides the body of its last method; the member read must supply it.
	mustCount(t, out, "return 'last'", 1)
}

// RD-7: the schema says duplicates are ignored, but they were ignored only by the string typed.
// Different spellings of one resource each resolved and each printed its source.
func TestBatchIgnoresDifferentSpellingsOfOneResource(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"shapes.py":   "class Point:\n    x: float = 0.0\n    y: float = 0.0\n",
		"consumer.py": "from shapes import Point\n\ndef render(p):\n    return str(p.x)\n",
	}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	out := readOK(t, rd, "Point", "shapes.Point", "shapes/Point", "shapes::Point", "shapes.py:Point",
		"render", "consumer.render")
	mustCount(t, out, "class Point:", 1)
	mustCount(t, out, "def render(p):", 1)
	if strings.Contains(out, "# UNRESOLVED") {
		t.Fatalf("a repeated spelling is not a miss:\n%s", out)
	}
}

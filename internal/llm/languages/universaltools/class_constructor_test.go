package universaltools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
)

// scanFiles writes files into a fresh project and full-scans it with every scanner these tests
// need, returning the manager, the registry and the project root.
func scanFiles(t *testing.T, files map[string]string) (*topology.TopologyManager, *scanner.Registry, string) {
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
	if err := os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(dir, ".aracne", "topology.db")); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, reg, dir
}

// TestClassReadPrintsTheConstructorOnce pins RD-1.
//
// ClassUnit appended the constructor's cut after the class's, which is right for Go -- NewX is
// a separate function -- and wrong for Python, JavaScript and TypeScript, where the
// constructor sits inside the class body: every class read printed it a second time after the
// class had closed.
func TestClassReadPrintsTheConstructorOnce(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		id    string
		ctor  string
	}{
		{
			name: "javascript",
			files: map[string]string{
				"package.json": `{"name":"p"}`,
				"a.js": "class Point {\n  constructor(x, y) {\n    this.x = x;\n    this.y = y;\n  }\n" +
					"  norm() {\n    return Math.sqrt(this.x * this.x + this.y * this.y);\n  }\n}\n" +
					"module.exports = { Point };\n",
			},
			id:   "a.Point",
			ctor: "constructor(x, y) {",
		},
		{
			name: "typescript",
			files: map[string]string{
				"package.json":  `{"name":"t"}`,
				"tsconfig.json": `{"compilerOptions":{}}`,
				"box.ts": "export class Box {\n  private w: number;\n  constructor(w: number) {\n" +
					"    this.w = w;\n  }\n  width(): number {\n    return this.w;\n  }\n}\n",
			},
			id:   "box.Box",
			ctor: "constructor(w: number) {",
		},
		{
			name: "python",
			files: map[string]string{
				"shapes.py": "class Circle:\n    def __init__(self, r):\n        self.r = r\n\n" +
					"    def area(self):\n        return 3.14159 * self.r * self.r\n",
			},
			id:   "shapes.Circle",
			ctor: "def __init__(self, r):",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, reg, _ := scanFiles(t, tc.files)
			out, err := universaltools.NewRead(mgr, helper.DefaultConfig(), false, reg).
				ReadIDs([]string{tc.id}, universaltools.ReadIDsOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(out, tc.ctor); n != 1 {
				t.Fatalf("the constructor must be printed exactly once, got %d:\n%s", n, out)
			}
		})
	}
}

// TestGoStructReadStillAppendsItsConstructor is the other half of RD-1: in Go the constructor
// is a separate function after the type, so the struct read must keep appending it.
func TestGoStructReadStillAppendsItsConstructor(t *testing.T) {
	mgr, reg, _ := scanFiles(t, map[string]string{
		"go.mod": "module probe\n\ngo 1.25\n",
		"point.go": "package probe\n\n// Point is a point.\ntype Point struct {\n\tX, Y int\n}\n\n" +
			"// NewPoint builds a Point.\nfunc NewPoint(x, y int) *Point {\n\treturn &Point{X: x, Y: y}\n}\n",
	})
	out, err := universaltools.NewRead(mgr, helper.DefaultConfig(), false, reg).
		ReadIDs([]string{"probe.Point"}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "func NewPoint(x, y int) *Point {"); n != 1 {
		t.Fatalf("a Go struct read must carry its constructor exactly once, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "type Point struct {") {
		t.Fatalf("the struct itself must still be printed:\n%s", out)
	}
}

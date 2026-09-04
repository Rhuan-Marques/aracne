package pyscanner

import (
	"os"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// The scanner must capture the first-body-statement line and any existing
// docstring's span, so descriptions apply can insert/replace docstrings precisely.
func TestParseFile_CapturesDocstringPositions(t *testing.T) {
	src := "def render(shape):\n" + // 1
		"    \"\"\"Existing docstring.\"\"\"\n" + // 2
		"    return shape.describe()\n" + // 3
		"\n" + // 4
		"def plain(\n" + // 5
		"    x,\n" + // 6
		"):\n" + // 7
		"    return x\n" + // 8
		"\n" + // 9
		"class Shape:\n" + // 10
		"    \"\"\"Class doc.\"\"\"\n" + // 11
		"    pass\n" // 12

	path := "zz_docpos_demo.py"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	pr, err := ParseFile(path, python.PackagePath("demo"), ".")
	if err != nil {
		t.Fatal(err)
	}

	fns := map[string]python.PythonFunction{}
	for _, fp := range pr.Functions {
		fns[fp.Function.Name] = fp.Function
	}

	render := fns["render"]
	if render.BodyLine != 2 || render.DocStart != 2 || render.DocEnd != 2 {
		t.Errorf("render: BodyLine=%d Doc=%d..%d, want 2/2..2", render.BodyLine, render.DocStart, render.DocEnd)
	}

	plain := fns["plain"]
	if plain.BodyLine != 8 || plain.DocStart != 0 {
		t.Errorf("plain: BodyLine=%d DocStart=%d, want 8/0 (multi-line sig, no docstring)", plain.BodyLine, plain.DocStart)
	}

	var shape python.PythonClass
	for _, c := range pr.Classes {
		if c.Name == "Shape" {
			shape = c
		}
	}
	if shape.BodyLine != 11 || shape.DocStart != 11 || shape.DocEnd != 11 {
		t.Errorf("Shape: BodyLine=%d Doc=%d..%d, want 11/11..11", shape.BodyLine, shape.DocStart, shape.DocEnd)
	}
}

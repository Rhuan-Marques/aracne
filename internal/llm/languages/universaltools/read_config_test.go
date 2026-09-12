package universaltools_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
)

// A class whose base is described and a method whose callee is described: the two neighbour
// shapes the old "off" still rendered (the base-class loop takes no visibility at all, and a
// described callee rendered Normal).
const pyContext = "class Shape:\n" +
	"    \"\"\"Base of all shapes.\"\"\"\n\n" +
	"    def area(self):\n        raise NotImplementedError\n\n\n" +
	"def helper(x):\n" +
	"    \"\"\"Scales a length.\"\"\"\n" +
	"    a = x\n    b = a * 2\n    c = b * 3\n    d = c * 4\n    return d\n\n\n" +
	"class Sq(Shape):\n" +
	"    \"\"\"A square.\"\"\"\n\n" +
	"    def area(self):\n        return helper(2)\n"

func contextConfig(filter string) *helper.Config {
	cfg := quietConfig()
	cfg.Read.ContextFilter = filter
	return cfg
}

// RD-9: context_filter "off" is documented as "the code asked for, nothing around it", and
// still emitted a "# CONTEXT:" block.
func TestContextFilterOffEmitsNoContext(t *testing.T) {
	mgr, reg, dir := scanWithJava(t, map[string]string{"shapes.py": pyContext}, nil)

	normal := readOK(t, universaltools.NewRead(mgr, contextConfig("normal"), false, reg), "shapes.Sq", "shapes.Sq.area")
	if !strings.Contains(normal, "# CONTEXT:") {
		t.Fatalf("fixture drifted: normal should have context to turn off:\n%s", normal)
	}

	rd := universaltools.NewRead(mgr, contextConfig("off"), false, reg)
	for _, ids := range [][]string{{"shapes.Sq"}, {"shapes.Sq.area"}, {"shapes.py"}} {
		if out := readOK(t, rd, ids...); strings.Contains(out, "# CONTEXT:") {
			t.Fatalf("off must emit no context section for %v:\n%s", ids, out)
		}
	}
	// A shell window is a read too.
	slice, err := rd.ReadSlice(filepath.Join(dir, "shapes.py"), 18, 18)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(slice, "# CONTEXT:") {
		t.Fatalf("off must emit no context section for a window:\n%s", slice)
	}
}

// A package read has no source: its listing is the answer, so "off" leaves it alone.
func TestContextFilterOffKeepsAPackageListing(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/shape.go": goCircle,
	}, nil)
	out, err := universaltools.NewRead(mgr, contextConfig("off"), false, reg).
		ReadIDs([]string{"example.com/p/shapes"}, universaltools.ReadIDsOptions{Kinds: helper.AllReadKinds()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## struct example.com/p/shapes.Circle") {
		t.Fatalf("a package read under off must still list what the package holds:\n%s", out)
	}
}

func paramNames(rd *universaltools.Read) []string {
	var names []string
	for _, p := range rd.Parameters() {
		names = append(names, p.Name)
	}
	return names
}

// RD-10: a symbol over read.max_symbol_lines is abridged with a marker naming `full: true` as
// the way back to the exact bytes -- but `full` was only in the schema for skeleton file reads,
// so on the MCP surface the model was pointed at a parameter it had never been shown.
func TestFullIsAdvertisedWheneverSymbolsAreCapped(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/shape.go": goCircle,
	}, nil)

	// The MCP shape: the harness keeps its own read, so file paths are not advertised.
	cfg := quietConfig()
	cfg.Mode = helper.ModeMCP
	names := paramNames(universaltools.NewRead(mgr, cfg, true, reg))
	if strings.Join(names, ",") != "ids,full" {
		t.Fatalf("with max_symbol_lines on, `full` must be advertised; got %v", names)
	}
	for _, p := range universaltools.NewRead(mgr, cfg, true, reg).Parameters() {
		if p.Name == "full" && strings.Contains(p.Description, "No effect on non-file ids") {
			t.Fatalf("`full` also lifts the symbol cap; its description must not deny it: %q", p.Description)
		}
	}

	// Nothing abridged anywhere: the knob is dead weight and stays out of the schema.
	off := 0
	cfg.Read.MaxSymbolLines = &off
	if names := paramNames(universaltools.NewRead(mgr, cfg, true, reg)); strings.Join(names, ",") != "ids" {
		t.Fatalf("with no cap and no skeleton, only ids should be advertised; got %v", names)
	}
}

// The abridged marker must name the escape hatch, and must not send the model to read the same
// id again -- which returns the same abridged body.
func TestAbridgedSymbolMarkerNamesFull(t *testing.T) {
	var b strings.Builder
	b.WriteString("package shapes\n\nfunc Huge(n int) int {\n\ttotal := 0\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "\ttotal += n * %d // step %d\n", i, i)
	}
	b.WriteString("\treturn total\n}\n")
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"go.mod":         "module example.com/p\n\ngo 1.25\n",
		"shapes/huge.go": b.String(),
	}, nil)
	cfg := quietConfig()
	limit := 10
	cfg.Read.MaxSymbolLines = &limit
	rd := universaltools.NewRead(mgr, cfg, false, reg)

	out := readOK(t, rd, "shapes.Huge")
	if !strings.Contains(out, "⋯") || !strings.Contains(out, "full: true") {
		t.Fatalf("the abridged body must be marked and name `full: true`:\n%s", out)
	}
	if strings.Contains(out, `for its source`) {
		t.Fatalf("the marker must not send the model back to the same abridged read:\n%s", out)
	}
	full, err := rd.ReadIDs([]string{"shapes.Huge"}, universaltools.ReadIDsOptions{ForceFullFile: true})
	if err != nil || !strings.Contains(full, "step 39") {
		t.Fatalf("full must return the exact body (err %v):\n%s", err, full)
	}
}

// RD-8: read.max_file_size -- "files above this are neither read nor indexed" -- was enforced
// by neither: an over-limit source file was parsed, and a whole-file read served it in full.
func TestMaxFileSizeKeepsAFileOutOfTheIndex(t *testing.T) {
	var big strings.Builder
	big.WriteString("package shapes\n\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&big, "func Big%d() int { return %d }\n", i, i)
	}
	cfg := quietConfig()
	cfg.Read.MaxFileSize = 1024
	mgr, reg, dir := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/big.go":   big.String(),
		"shapes/shape.go": goCircle,
	}, cfg)
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["example.com/p/shapes.Big0"]; ok {
		t.Fatal("a file over read.max_file_size must not be indexed")
	}
	if _, ok := topo.Resources["example.com/p/shapes.Circle"]; !ok {
		t.Fatal("a file under the limit must still be indexed")
	}

	// A file that grows past the limit leaves the index on its next single-file update.
	shape := filepath.Join(dir, "shapes", "shape.go")
	grown := goCircle + strings.Repeat("// padding padding padding padding padding padding\n", 30)
	if err := os.WriteFile(shape, []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.UpdateFile(shape, reg); err != nil {
		t.Fatal(err)
	}
	if topo, err = mgr.ReadAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["example.com/p/shapes.Circle"]; ok {
		t.Fatal("a file that grew past read.max_file_size must drop out on re-index")
	}
}

// The read half of RD-8: a file already in the index -- scanned before it grew, or before the
// limit applied -- is refused whole, and its declarations stay readable one by one.
func TestMaxFileSizeRefusesAWholeIndexedFile(t *testing.T) {
	mgr, reg, _ := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/shape.go": goCircle,
	}, nil)
	cfg := quietConfig()
	cfg.Read.MaxFileSize = 64
	cfg.Read.FileMode = helper.FileModeFull
	rd := universaltools.NewRead(mgr, cfg, false, reg)

	_, err := rd.ReadIDs([]string{"shapes/shape.go"}, universaltools.ReadIDsOptions{})
	if err == nil || !strings.Contains(err.Error(), "read.max_file_size") {
		t.Fatalf("an indexed file over the limit must be refused, got %v", err)
	}
	if out := readOK(t, rd, "shapes.(Circle).Area"); !strings.Contains(out, "func (c Circle) Area() float64 {") {
		t.Fatalf("a declaration inside it must still read:\n%s", out)
	}

	// Under the limit nothing changes.
	cfg.Read.MaxFileSize = 1 << 20
	if out := readOK(t, universaltools.NewRead(mgr, cfg, false, reg), "shapes/shape.go"); !strings.Contains(out, "type Circle struct {") {
		t.Fatalf("a file under the limit must read whole:\n%s", out)
	}
}

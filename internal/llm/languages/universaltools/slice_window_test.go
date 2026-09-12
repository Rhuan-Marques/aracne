package universaltools_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
)

const goMulti = "package shapes\n\n" +
	"func Multi(\n" + // 3
	"\ta int,\n" + // 4
	"\tb int,\n" + // 5
	") int {\n" + // 6
	"\tx := a + b\n" + // 7
	"\ty := x * 2\n" + // 8
	"\tz := y - 1\n" + // 9
	"\treturn z\n" + // 10
	"}\n" // 11

// RD-5: a window that opens inside a multi-line signature printed the whole signature as its
// frame, then the window's own copy of the same lines. A real `sed` prints each line once.
func TestSliceWindowInsideAMultiLineSignaturePrintsEachLineOnce(t *testing.T) {
	mgr, reg, dir := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/multi.go": goMulti,
	}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	out, err := rd.ReadSlice(filepath.Join(dir, "shapes", "multi.go"), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := "func Multi(\n\ta int,\n\tb int,\n) int {\n\tx := a + b\n\ty := x * 2\n⋯ +3 lines of Multi ⋯\n"
	if !strings.Contains(out, want) {
		t.Fatalf("window 4-8 should be the frame's first line, then lines 4-8 once:\nwant\n%s\ngot\n%s", want, out)
	}
}

const pyBox = "class Box:\n" + // 1
	"    \"\"\"A box.\"\"\"\n" + // 2
	"\n" + // 3
	"    size = 1\n" + // 4
	"\n" + // 5
	"    def grow(\n" + // 6
	"        self,\n" + // 7
	"        by,\n" + // 8
	"    ):\n" + // 9
	"        n = by\n" + // 10
	"        m = n + 1\n" + // 11
	"        k = m + 2\n" + // 12
	"        return k\n" + // 13
	"\n" + // 14
	"    def other(self):\n" + // 15
	"        return 0\n" // 16

// The nested half of RD-5: an enclosing class's marker counted every line between its own
// signature and the window -- including the method signature printed right below it.
func TestSliceMarkersCountOnlyTheLinesTheyHide(t *testing.T) {
	mgr, reg, dir := scanWithJava(t, map[string]string{"box.py": pyBox}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	out, err := rd.ReadSlice(filepath.Join(dir, "box.py"), 11, 12)
	if err != nil {
		t.Fatal(err)
	}
	want := "class Box:\n" +
		"⋯ +4 lines of Box ⋯\n" + // lines 2-5
		"    def grow(\n        self,\n        by,\n    ):\n" +
		"⋯ +1 lines of grow ⋯\n" + // line 10
		"        m = n + 1\n        k = m + 2\n"
	if !strings.Contains(out, want) {
		t.Fatalf("each marker must count only what it hides:\nwant\n%s\ngot\n%s", want, out)
	}
	mustCount(t, out, "def grow(", 1)
}

// The single-line-signature window, which was already right, stays as it was.
func TestSliceWindowInsideAFunctionBodyKeepsItsFrame(t *testing.T) {
	mgr, reg, dir := scanWithJava(t, map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.25\n",
		"shapes/shape.go": goCircle,
	}, nil)
	rd := universaltools.NewRead(mgr, quietConfig(), false, reg)
	// goCircle: Area is lines 9-11; line 10 is its only body line.
	out, err := rd.ReadSlice(filepath.Join(dir, "shapes", "shape.go"), 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := "func (c Circle) Area() float64 {\n\treturn c.R * c.R * 3\n⋯ +1 lines of Area ⋯\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want\n%s\ngot\n%s", want, out)
	}
}

func lineRangeConfig() *helper.Config {
	cfg := quietConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	return cfg
}

// RD-11: intercept_line_ranges advertises a method's span in every context entry and marker that
// names it, and promises that reading exactly that span returns what the resource read would.
// A method inside a class never qualified: the class covers the window without fitting in it.
func TestSliceOfAMembersExactSpanIsItsResourceRead(t *testing.T) {
	mgr, reg, dir := scanWithJava(t, map[string]string{"shapes.py": pyCircle}, lineRangeConfig())
	rd := universaltools.NewRead(mgr, lineRangeConfig(), false, reg)
	path := filepath.Join(dir, "shapes.py")

	// pyCircle: area is lines 5-6, inside Circle (1-9).
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if span := universaltools.SpanOf(topo, "shapes.Circle.area"); span != "shapes.py:5-6" {
		t.Fatalf("fixture drifted: area is advertised at %q", span)
	}
	slice, err := rd.ReadSlice(path, 5, 6)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := rd.ReadIDs([]string{"shapes.Circle.area"}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if slice != resource {
		t.Fatalf("reading the advertised span must equal the resource read:\n--- span\n%s\n--- resource\n%s", slice, resource)
	}

	// One line more is a slice of the class, not a declaration's span, and stays framed.
	framed, err := rd.ReadSlice(path, 5, 7)
	if err != nil {
		t.Fatal(err)
	}
	if framed == resource || !strings.Contains(framed, "⋯") {
		t.Fatalf("a window that is not a declaration's span must stay a framed slice:\n%s", framed)
	}
}

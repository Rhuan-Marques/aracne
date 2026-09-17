package universaltools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// read.max_symbol_lines and an annotation want opposite things: the cap says "the signature is
// enough", the annotation says "this specific line must be on screen". A warning report that
// answers by showing a signature the warned line is not in has failed at the one job it has.

// longFn is a function of n body lines, with `mark` written at body line `at` (1-based).
func longFn(n, at int, mark string) string {
	var b strings.Builder
	b.WriteString("func Big() {\n")
	for i := 1; i <= n; i++ {
		if i == at {
			b.WriteString("\t" + mark + "\n")
			continue
		}
		fmt.Fprintf(&b, "\tstep%d()\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

func symbolUnit(body string) readunit.Unit {
	return readunit.Unit{ID: "pkg.Big", Kind: domain.ResourceFunction, Body: body}
}

// The whole point: the marked line survives a cap that would otherwise have deleted it.
func TestWindowedAbridgementKeepsTheAnnotatedLine(t *testing.T) {
	body := longFn(300, 200, "missing()")
	u := symbolUnit(body)
	anns := []readunit.Annotation{{Line: "\tmissing()", Note: "[use_missing_node] pkg.missing does not exist"}}

	got := abridgeSymbolBody(u, 20, anns, 6)

	if !strings.Contains(got, "\tmissing()") {
		t.Fatalf("the annotated line was elided away -- the one thing this must not do:\n%s", got)
	}
	if !strings.HasPrefix(got, "func Big() {") {
		t.Fatalf("the signature no longer leads:\n%s", got)
	}
	// Context either side, so the line can be read in situ.
	for _, want := range []string{"step194()", "step206()"} {
		if !strings.Contains(got, want) {
			t.Errorf("the window did not reach %s:\n%s", want, got)
		}
	}
	// And the rest really is gone -- this is still an abridgement.
	if strings.Contains(got, "step100()") {
		t.Errorf("nothing was elided; the cap did not apply:\n%s", got)
	}
	if !strings.Contains(got, "⋯") {
		t.Errorf("no elision marker stands in for what was dropped:\n%s", got)
	}
	// The closing line, so the block does not read as truncated output.
	if !strings.Contains(got, "\n}") {
		t.Errorf("the declaration's closing line was dropped:\n%s", got)
	}
	// The escape hatch is stated once, under the body, not at every gap.
	if n := strings.Count(got, "full: true"); n != 1 {
		t.Errorf("the `full: true` remedy appears %d times, want exactly 1:\n%s", n, got)
	}
}

// Two anchors close together are one region, not two windows with a pointless marker between.
func TestWindowsThatOverlapMerge(t *testing.T) {
	body := longFn(300, 200, "first()")
	body = strings.Replace(body, "\tstep204()", "\tsecond()", 1)
	u := symbolUnit(body)
	anns := []readunit.Annotation{
		{Line: "\tfirst()", Note: "[use_missing_node] a does not exist"},
		{Line: "\tsecond()", Note: "[use_missing_node] b does not exist"},
	}

	got := abridgeSymbolBody(u, 20, anns, 6)

	for _, want := range []string{"first()", "second()", "step202()"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s missing -- the two windows did not merge:\n%s", want, got)
		}
	}
	// Signature gap, then one merged region, then the tail gap. Three would mean the windows
	// were emitted separately with a marker standing in for two lines that are both shown.
	if n := strings.Count(got, "not shown ⋯"); n != 2 {
		t.Errorf("got %d gap markers, want 2 (one before the region, one after):\n%s", n, got)
	}
}

// An elision marker costs a line of its own, so standing it in for one or two real lines is a
// net loss that reads as noise.
func TestPinholeGapsAreFilledRatherThanElided(t *testing.T) {
	body := longFn(300, 200, "first()")
	body = strings.Replace(body, "\tstep215()", "\tsecond()", 1)
	u := symbolUnit(body)
	anns := []readunit.Annotation{
		{Line: "\tfirst()", Note: "[x] a"},
		{Line: "\tsecond()", Note: "[x] b"},
	}
	// Radius 6 puts the windows at [194,206] and [209,221], leaving exactly two lines --
	// 207 and 208 -- between them.
	got := abridgeSymbolBody(u, 20, anns, 6)

	for _, want := range []string{"step207()", "step208()"} {
		if !strings.Contains(got, want) {
			t.Errorf("a two-line gap was elided instead of filled:\n%s", got)
		}
	}
	// Filled, not filled AND marked: the signature gap and the tail gap are the only two.
	if n := strings.Count(got, "not shown ⋯"); n != 2 {
		t.Errorf("got %d gap markers, want 2 -- a filled gap was marked as well:\n%s", n, got)
	}
}

// THE OTHER SIDE OF THE BOUNDARY. The fill is `gap <= 2`, and every test above only exercises
// the filling half -- an off-by-one to `<= 3` would pass all of them. A three-line gap must
// still be elided, or "pinhole" quietly becomes "any small gap".
func TestAThreeLineGapIsStillElided(t *testing.T) {
	body := longFn(300, 200, "first()")
	// Radius 6 puts the windows at [194,206] and [210,222]: lines 207, 208 and 209 between.
	body = strings.Replace(body, "\tstep216()", "\tsecond()", 1)
	u := symbolUnit(body)

	got := abridgeSymbolBody(u, 20, []readunit.Annotation{
		{Line: "\tfirst()", Note: "[x] a"},
		{Line: "\tsecond()", Note: "[x] b"},
	}, 6)

	if strings.Contains(got, "step208()") {
		t.Errorf("a three-line gap was filled; the pinhole rule has grown by one:\n%s", got)
	}
	if !strings.Contains(got, "⋯ 3 lines not shown ⋯") {
		t.Errorf("the three-line gap is missing its marker:\n%s", got)
	}
}

// A marker that says "193 lines" when it swallowed 190 is worse than no number: it is the one
// part of an abridged body the reader cannot check against anything.
func TestGapCountsAreArithmeticallyRight(t *testing.T) {
	u := symbolUnit(longFn(300, 200, "missing()"))
	got := abridgeSymbolBody(u, 20, []readunit.Annotation{{Line: "\tmissing()", Note: "[x] y"}}, 6)

	// Body lines 1-300, anchor at 200, radius 6 keeps 194-206 (13 lines: 2*6+1).
	// So the gaps are 1-193 and 207-300, and 193 + 94 = 287 = 300 - 13.
	for _, want := range []string{"⋯ 193 lines not shown ⋯", "⋯ 94 lines not shown ⋯"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// The trailer totals the gaps rather than counting something else.
	if !strings.Contains(got, "⋯ 287 lines of pkg.Big not shown") {
		t.Errorf("the trailer does not total the gaps (want 287 = 193+94):\n%s", got)
	}
	if n := strings.Count(got, "step"); n != 12 {
		t.Errorf("kept %d context lines, want 12 (13 window lines, one of which is the mark):\n%s", n, got)
	}
}

// A window near either end of a body has nowhere to expand to, and must clamp rather than
// index out of the slice or silently drop the closing line.
func TestWindowsClampAtTheBodyEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   int
		near string
	}{
		{"first body line", 1, "step2()"},
		{"last body line", 300, "step299()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := symbolUnit(longFn(300, tc.at, "missing()"))
			got := abridgeSymbolBody(u, 20, []readunit.Annotation{{Line: "\tmissing()", Note: "[x] y"}}, 6)

			if !strings.Contains(got, "\tmissing()") {
				t.Fatalf("the annotated line was lost at the edge:\n%s", got)
			}
			if !strings.Contains(got, tc.near) {
				t.Errorf("the window did not reach %s:\n%s", tc.near, got)
			}
			if !strings.HasPrefix(got, "func Big() {") {
				t.Errorf("the signature no longer leads:\n%s", got)
			}
			if !strings.Contains(got, "\n}") {
				t.Errorf("the closing line was dropped:\n%s", got)
			}
		})
	}
}

// THE REGRESSION GUARD for every read in the product that is not a warning report.
func TestNoAnnotationsIsByteIdenticalToTheOldAbridgement(t *testing.T) {
	u := symbolUnit(longFn(300, 200, "whatever()"))

	plain := abridgeSymbolBody(u, 20, nil, 6)
	if strings.Contains(plain, "not shown ⋯") {
		t.Fatalf("an unannotated body took the windowed path:\n%s", plain)
	}
	if !strings.Contains(plain, "over the read.max_symbol_lines cap") {
		t.Fatalf("an unannotated body no longer gets the plain marker:\n%s", plain)
	}
	// And an annotation that matches nothing must fall back to exactly the same text, rather
	// than inventing a shape out of a body it could not find an anchor in.
	unmatched := abridgeSymbolBody(u, 20,
		[]readunit.Annotation{{Line: "\tnot_in_this_body()", Note: "[x] y"}}, 6)
	if unmatched != plain {
		t.Errorf("an unmatched anchor changed the output:\ngot:\n%s\nwant:\n%s", unmatched, plain)
	}
	// A zero window is the documented "signature alone" setting.
	if zero := abridgeSymbolBody(u, 20,
		[]readunit.Annotation{{Line: "\twhatever()", Note: "[x] y"}}, 0); zero != plain {
		t.Errorf("read.annotation_window 0 did not fall back to the plain marker:\n%s", zero)
	}
}

// A body under the cap is never touched, annotated or not.
func TestUnderTheCapIsVerbatimEvenWhenAnnotated(t *testing.T) {
	u := symbolUnit(longFn(5, 3, "missing()"))
	got := abridgeSymbolBody(u, 100, []readunit.Annotation{{Line: "\tmissing()", Note: "[x] y"}}, 6)
	if got != u.Body {
		t.Errorf("an under-cap body was rewritten:\ngot:\n%s\nwant:\n%s", got, u.Body)
	}
}

// A marker standing in for fifty lines of function body must not jut out to the margin as
// though it were a sibling declaration -- which is what taking the FOLLOWING line's indent
// does to the last gap, since the line after it is the closing brace at column zero.
func TestGapMarkersSitInsideTheBlock(t *testing.T) {
	u := symbolUnit(longFn(300, 200, "missing()"))
	got := abridgeSymbolBody(u, 20, []readunit.Annotation{{Line: "\tmissing()", Note: "[x] y"}}, 4)

	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "not shown ⋯") || strings.Contains(line, "read.max_symbol_lines") {
			continue // the trailing escape-hatch marker is deliberately unindented
		}
		if !strings.HasPrefix(line, "\t") {
			t.Errorf("a gap marker sits at column zero:\n%s", got)
		}
	}
}

// A LOCAL declaration is not the member. `var x T` deep in a Go function used to be taken as the
// body's last declaration, so the split landed on it and every line above became the uncapped
// lead: a warning aimed at line 95 of a method carried all 95 lines, windowing never ran.
func TestALocalDeclarationDoesNotMoveTheSplit(t *testing.T) {
	body := longFn(300, 200, "missing()")
	body = strings.Replace(body, "\tstep150()", "\tvar ingester *Ingester", 1)
	body = "type Holder struct {\n\tn int\n}\n\n" + body
	u := symbolUnit(body)

	got := abridgeSymbolBody(u, 20, []readunit.Annotation{{Line: "\tmissing()", Note: "[x] y"}}, 6)

	if strings.Contains(got, "step100()") {
		t.Fatalf("the lines above a local var were kept whole; the split landed on the local:\n%s", got)
	}
	if !strings.HasPrefix(got, "type Holder struct {") || !strings.Contains(got, "func Big() {") {
		t.Errorf("the receiver type and signature must both still lead:\n%s", got)
	}
	if !strings.Contains(got, "\tmissing()") {
		t.Errorf("the annotated line was lost:\n%s", got)
	}
}

func TestSplitLastDeclarationFindsMembersNotLocals(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantLast string
	}{
		{"go local var", "func A() {\n\tx := 1\n\tvar y int\n\t_ = y\n}\n", ""},
		{"go local in if", "func A() {\n\tif ok {\n\t\tconst c = 1\n\t}\n}\n", ""},
		{"go method after type", "type T struct {\n\tn int\n}\n\nfunc (t T) M() {\n\tvar z int\n}\n", "func (t T) M() {"},
		{"python method in class", "class A:\n    def one(self):\n        pass\n\n    def two(self):\n        def inner():\n            pass\n        return inner\n", "    def two(self):"},
		{"js const in class method", "class A {\n  one() {}\n  two() {\n    const x = 1\n    return x\n  }\n}\n", ""},
		{"rust local let", "struct S {}\n\nfn m() {\n    let v = 1;\n}\n", "fn m() {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, last := splitLastDeclaration(tc.body)
			first := strings.SplitN(last, "\n", 2)[0]
			want := tc.wantLast
			if want == "" {
				want = strings.SplitN(tc.body, "\n", 2)[0]
			}
			if first != want {
				t.Errorf("split at %q, want %q", first, want)
			}
		})
	}
}

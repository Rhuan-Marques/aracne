package readunit

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
)

// An annotation is a rendering concern, applied on the way out. Body itself stays the source
// as the file has it, because four separate things compare it: dropRepeatedSource's Contains
// test, bodyBytes' over-serve denominator, renderstate.MarkRendered, and the abridger.

func TestAnnotateMarksTheNamedLine(t *testing.T) {
	body := "func Shout(s string) string {\n\treturn ToUpper(s)\n}"
	got := Annotate(body, []Annotation{{Line: "\treturn ToUpper(s)", Note: "[use_missing_node] pkg.ToUpper does not exist"}})

	want := "func Shout(s string) string {\n\treturn ToUpper(s) <- [use_missing_node] pkg.ToUpper does not exist\n}"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Indentation is what tells two otherwise identical lines apart, so it is significant; a cut
// may or may not carry trailing whitespace, so that is not.
func TestAnnotateIgnoresTrailingButNotLeadingWhitespace(t *testing.T) {
	body := "if x {\n\t\treturn nil\n}"
	if got := Annotate(body, []Annotation{{Line: "\t\treturn nil   ", Note: "[x] y"}}); !strings.Contains(got, "return nil <- [x] y") {
		t.Errorf("trailing whitespace defeated the match:\n%s", got)
	}
	if got := Annotate(body, []Annotation{{Line: "return nil", Note: "[x] y"}}); got != body {
		t.Errorf("an anchor at the wrong depth matched anyway:\n%s", got)
	}
}

// Two warnings can name one line -- a call that is both missing and re-signatured. Appending
// in place would change the text the second lookup is still searching for.
func TestAnnotateKeepsBothNotesOnOneLine(t *testing.T) {
	body := "func f() {\n\tbroken()\n}"
	got := Annotate(body, []Annotation{
		{Line: "\tbroken()", Note: "[a] one"},
		{Line: "\tbroken()", Note: "[b] two"},
	})
	if !strings.Contains(got, "broken() <- [a] one; [b] two") {
		t.Errorf("the second note was dropped:\n%s", got)
	}
}

func TestAnnotateIsIdentityWithNothingToDo(t *testing.T) {
	body := "func f() {\n\treturn\n}"
	for _, anns := range [][]Annotation{nil, {}, {{Line: "\tnope()", Note: "[x] y"}}, {{Line: "\treturn", Note: ""}}} {
		if got := Annotate(body, anns); got != body {
			t.Errorf("Annotate(%v) rewrote an untouched body:\n%s", anns, got)
		}
	}
}

// The ledger must see the SOURCE, not the decorated copy: MarkRendered keys on the exact cut,
// so recording an annotated one would stop a later context entry recognizing the declaration
// this response already showed.
func TestWriteGroupAnnotatesTheOutputButLedgersTheRawBody(t *testing.T) {
	body := "func Shout(s string) string {\n\treturn ToUpper(s)\n}"
	st := renderstate.New()

	out := Render([]Unit{{
		ID: "pkg.Shout", Path: "p.go", Label: "p.go", Body: body,
		Annotations: []Annotation{{Line: "\treturn ToUpper(s)", Note: "[use_missing_node] gone"}},
	}}, Options{State: st})

	if !strings.Contains(out, "return ToUpper(s) <- [use_missing_node] gone") {
		t.Fatalf("the note never reached the page:\n%s", out)
	}
	if !st.ParentSeen(body) {
		t.Error("the ledger recorded the annotated body, so the raw declaration is no longer recognized as shown")
	}
}

// Every existing render in the product has no annotations, and must be untouched by this.
func TestRenderWithoutAnnotationsIsUnchanged(t *testing.T) {
	u := Unit{ID: "pkg.F", Path: "p.go", Label: "p.go", Body: "func F() {}"}
	plain := Render([]Unit{u}, Options{State: renderstate.New()})
	if strings.Contains(plain, "<-") {
		t.Fatalf("an unannotated render grew an arrow:\n%s", plain)
	}
}

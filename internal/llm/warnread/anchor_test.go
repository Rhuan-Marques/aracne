package warnread

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The anchor is what turns a warning from "two ids above the code" into a mark on the line
// that caused it. Everything below is about picking the RIGHT line, because a note on the
// wrong one is worse than no note: it sends the fix somewhere the bug is not.

// A doc comment that merely MENTIONS the missing name is not the reference that is broken.
// testing_ground/go/dotimport is the real case this is taken from: the comment above Shout
// reads "upper-cases its argument using the DOT-IMPORTED ToUpper", and it sits inside the Go
// function cut, so a plain substring search marks the prose instead of the call under it.
func TestReferenceLineSkipsADocCommentThatNamesTheSymbol(t *testing.T) {
	lines := strings.Split(`// Shout upper-cases its argument using the DOT-IMPORTED ToUpper (no "strings."
// qualifier) — exercises name resolution under a dot import.
func Shout(s string) string {
	return ToUpper(s)
}`, "\n")

	got, ok := referenceLine(lines, "ToUpper")
	if !ok {
		t.Fatal("no reference found at all")
	}
	if strings.Contains(got, "//") {
		t.Fatalf("anchored on the doc comment instead of the call:\n%s", got)
	}
	if strings.TrimSpace(got) != "return ToUpper(s)" {
		t.Fatalf("anchored on %q, want the call line", got)
	}
}

// A whole identifier, not a substring: `Add` must not match `Address`.
func TestReferenceLineMatchesWholeIdentifiersOnly(t *testing.T) {
	lines := []string{"\tvar Address string", "\treturn lib.Add(1, 2)"}
	got, ok := referenceLine(lines, "Add")
	if !ok || strings.TrimSpace(got) != "return lib.Add(1, 2)" {
		t.Fatalf("got %q (ok=%v), want the lib.Add call", got, ok)
	}
}

// An interface_conflict is not about a call: the thing to go fix is the implementer's own
// declaration, so that is the line, docstring and decorators notwithstanding.
func TestDeclarationLineFindsTheClassLine(t *testing.T) {
	lines := strings.Split(`class IncompleteShape(Shape):
    """Subclass that does NOT implement all abstract methods (area is missing)."""

    def describe(self) -> str:
        return "incomplete"`, "\n")

	got, ok := declarationLine(lines, "IncompleteShape")
	if !ok || got != "class IncompleteShape(Shape):" {
		t.Fatalf("got %q (ok=%v), want the class line", got, ok)
	}
}

// Ids are module paths, FQNs or Rust paths; the source calls the tail of them.
func TestShortNameTakesTheLastSegment(t *testing.T) {
	for id, want := range map[string]string{
		"github.com/Rhuan-Marques/aracne/testing_ground/go/dotimport.ToUpper": "ToUpper",
		"com.acme.shapes.Shape#area":                                          "area",
		"crate::shapes::Circle":                                               "Circle",
		"bare":                                                                "bare",
	} {
		if got := shortName(id); got != want {
			t.Errorf("shortName(%q) = %q, want %q", id, got, want)
		}
	}
}

// The note is short because the line it sits on already says which code is broken -- the
// summary that used to repeat that is gone.
func TestNoteForIsShortAndKindSpecific(t *testing.T) {
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"pkg.IncompleteShape": {ID: "pkg.IncompleteShape", Name: "IncompleteShape"},
	}}

	for _, tc := range []struct {
		name string
		w    domain.TopologyWarning
		want string
	}{{
		name: "use_missing_node names the thing that is not there",
		w: domain.TopologyWarning{
			Kind: domain.WarnUseMissingNode, SourceID: "pkg.Shout", TargetID: "pkg.ToUpper",
			Message: "function pkg.Shout calls pkg.ToUpper which does not exist in package pkg",
		},
		want: "[use_missing_node] pkg.ToUpper does not exist",
	}, {
		// Baseline is SignatureBaseline's machine form, not prose -- it must not leak into a
		// note. The shape to match is in the report already, as the callee's own source.
		name: "signature_changed names the callee and not its encoded baseline",
		w: domain.TopologyWarning{
			Kind: domain.WarnSignatureChanged, SourceID: "lib.Add", TargetID: "main.CallA",
			Baseline: `Add|[{"Name":"a","Typing":"int"}]|[]`,
		},
		want: "[signature_changed] lib.Add changed signature",
	}, {
		// The implementer's name leads the message, and the implementer is the line this is
		// written on -- so it is said twice unless stripped.
		name: "interface_conflict drops the subject the line already shows",
		w: domain.TopologyWarning{
			Kind: domain.WarnInterfaceConflict, SourceID: "pkg.Shape", TargetID: "pkg.IncompleteShape",
			Message: "IncompleteShape declares it implements Shape but does not provide area",
		},
		want: "[interface_conflict] implements Shape but does not provide area",
	}, {
		name: "a scan error has only its message",
		w:    domain.TopologyWarning{Kind: "", SourceID: "a.go", Message: "error updating a.go: boom"},
		want: "[warning] error updating a.go: boom",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := noteFor(tc.w, topo); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// A warning whose fix site the graph no longer holds has no line to be written on. It must
// say so rather than guess -- the caller lists it under "Also warned, no code to show".
func TestAnchorForDeclinesWhenTheFixSiteIsGone(t *testing.T) {
	topo := &domain.Topology{Resources: map[string]domain.Resource{}}
	if _, _, ok := anchorFor(nil, topo, domain.TopologyWarning{
		Kind: domain.WarnUseMissingNode, SourceID: "gone.Caller", TargetID: "gone.Callee",
	}); ok {
		t.Fatal("anchored a warning whose source is not in the graph")
	}
}

// The uncovered tail is the one thing standing between the old summary and a report that is
// silently less complete than it was. It must be empty when everything was annotated.
func TestUncoveredTailIsEmptyWhenEverythingWasAnnotated(t *testing.T) {
	ws := []domain.TopologyWarning{{ID: "w1"}, {ID: "w2"}}
	if got := uncoveredTail(ws, map[string]bool{"w1": true, "w2": true}); got != "" {
		t.Fatalf("tail should be invisible when nothing was left out, got:\n%s", got)
	}
	got := uncoveredTail(ws, map[string]bool{"w1": true})
	if !strings.Contains(got, "Also warned, no code to show:") {
		t.Fatalf("the uncovered warning was dropped entirely:\n%s", got)
	}
}

// readunit.Annotate and the windower must agree about WHICH occurrence they mean, or the
// window is centred on one line and the note printed on another.
func TestAnchorIndexIsFirstMatchAndRefusesWeakAnchors(t *testing.T) {
	lines := []string{"\treturn ToUpper(s)", "\t}", "\treturn ToUpper(s)"}
	if got := readunit.AnchorIndex(lines, readunit.Annotation{Line: "\treturn ToUpper(s)"}); got != 0 {
		t.Errorf("AnchorIndex = %d, want the first match (0)", got)
	}
	// A bare brace occurs everywhere, so "the first match" would be an arbitrary one.
	if got := readunit.AnchorIndex(lines, readunit.Annotation{Line: "\t}"}); got != -1 {
		t.Errorf("AnchorIndex on a weak anchor = %d, want -1", got)
	}
}

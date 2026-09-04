package readunit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// unit builds a minimal Unit whose context lists the given neighbour IDs.
func unit(id, path string, line int, neighbors ...string) Unit {
	u := Unit{ID: id, Path: path, Label: path, Line: line, Fence: "go", Body: "func " + id + "() {}"}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		for _, n := range neighbors {
			if st.Renderable(n) {
				fmt.Fprintf(b, "## %s: n\n", n)
			}
		}
	}
	return u
}

func TestRenderGroupsByFileWithOneContext(t *testing.T) {
	out := Render([]Unit{
		unit("A", "a.go", 10, "X"),
		unit("C", "b.go", 1, "X", "Y"),
		unit("B", "a.go", 2, "Y"),
	}, Options{})

	// One fence per file, in first-requested order, with members in source order.
	if got := strings.Count(out, "```a.go"); got != 1 {
		t.Fatalf("expected exactly one a.go group, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "```b.go"); got != 1 {
		t.Fatalf("expected exactly one b.go group, got %d:\n%s", got, out)
	}
	if strings.Index(out, "```a.go") > strings.Index(out, "```b.go") {
		t.Fatalf("groups should follow request order:\n%s", out)
	}
	if strings.Index(out, "func B()") > strings.Index(out, "func A()") {
		t.Fatalf("members should be in source order within a group:\n%s", out)
	}

	// Exactly one CONTEXT section for the whole batch, and each neighbour listed once even
	// though two units reach it.
	if got := strings.Count(out, "# CONTEXT:"); got != 1 {
		t.Fatalf("expected exactly one CONTEXT section, got %d:\n%s", got, out)
	}
	for _, n := range []string{"X", "Y"} {
		if got := strings.Count(out, "## "+n+": n"); got != 1 {
			t.Fatalf("neighbour %s listed %d times, want 1:\n%s", n, got, out)
		}
	}
}

func TestRenderExcludesRequestedResourcesFromContext(t *testing.T) {
	// B is both requested and a neighbour of A. It is shown as source, so it must not also be
	// described in CONTEXT -- that duplication is what this renderer exists to remove.
	out := Render([]Unit{
		unit("A", "a.go", 1, "B", "X"),
		unit("B", "b.go", 1),
	}, Options{})

	if strings.Contains(out, "## B: n") {
		t.Fatalf("B is rendered as source and must not appear in CONTEXT:\n%s", out)
	}
	if !strings.Contains(out, "## X: n") {
		t.Fatalf("genuine outside neighbour X missing from CONTEXT:\n%s", out)
	}
}

func TestRenderFileAbsorbsItsMembers(t *testing.T) {
	// Asking for a file and a function inside it must print the file once, not the function
	// twice, and must keep that function out of CONTEXT.
	file := Unit{
		ID: "a.go", Path: "a.go", Label: "a.go", Fence: "go",
		Body:   "package a\n\nfunc A() {}",
		Covers: []string{"A"},
	}
	out := Render([]Unit{file, unit("A", "a.go", 5, "X")}, Options{})

	if got := strings.Count(out, "func A()"); got != 1 {
		t.Fatalf("A rendered %d times, want 1:\n%s", got, out)
	}
	if strings.Contains(out, "## A:") {
		t.Fatalf("a file's own member must not appear in CONTEXT:\n%s", out)
	}
	// The absorbed unit's neighbours go with it: only the file is rendered.
	if strings.Contains(out, "## X: n") {
		t.Fatalf("absorbed unit should not contribute its context:\n%s", out)
	}
}

func TestRenderDedupesImportsPerGroupNotPerResponse(t *testing.T) {
	// Two files that both import "os" must EACH say so. Suppressing the second would make that
	// group read as though it imported nothing.
	block := func(imports, deps []string) string {
		if len(imports) == 0 {
			return ""
		}
		return "import (" + strings.Join(imports, " ") + ")\n"
	}
	a := unit("A", "a.go", 1)
	a.Imports, a.ImportBlock = []string{"os", "fmt"}, block
	b := unit("B", "b.go", 1)
	b.Imports, b.ImportBlock = []string{"os"}, block

	out := Render([]Unit{a, b}, Options{})

	if got := strings.Count(out, "os"); got < 2 {
		t.Fatalf("os should appear in both groups' import blocks, saw %d:\n%s", got, out)
	}
}

func TestRenderMergesImportsWithinAGroup(t *testing.T) {
	block := func(imports, deps []string) string {
		return "import (" + strings.Join(imports, " ") + ")\n"
	}
	a := unit("A", "a.go", 1)
	a.Imports, a.ImportBlock = []string{"os"}, block
	b := unit("B", "a.go", 2)
	b.Imports, b.ImportBlock = []string{"os", "fmt"}, block

	out := Render([]Unit{a, b}, Options{})

	if got := strings.Count(out, "import ("); got != 1 {
		t.Fatalf("a group gets one pooled import block, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "os"); got != 1 {
		t.Fatalf("os pooled once within a group, saw %d:\n%s", got, out)
	}
	if !strings.Contains(out, "fmt") {
		t.Fatalf("second unit's import missing from the pooled block:\n%s", out)
	}
}

func TestRenderIncomingIsOptionalAndDeduped(t *testing.T) {
	ref := domain.ResourceRef{ID: "Caller", Kind: domain.ResourceFunction, Description: "calls it"}
	a, b := unit("A", "a.go", 1), unit("B", "b.go", 1)
	a.Incoming, b.Incoming = []domain.ResourceRef{ref}, []domain.ResourceRef{ref}

	if out := Render([]Unit{a, b}, Options{}); strings.Contains(out, "# USED BY:") {
		t.Fatalf("USED BY must stay off unless include_incoming is set:\n%s", out)
	}
	out := Render([]Unit{a, b}, Options{IncludeIncoming: true})
	if got := strings.Count(out, "Caller"); got != 1 {
		t.Fatalf("shared caller listed %d times, want 1:\n%s", got, out)
	}
}

func TestRenderEmpty(t *testing.T) {
	if out := Render(nil, Options{}); out != "" {
		t.Fatalf("no units should render nothing, got %q", out)
	}
}

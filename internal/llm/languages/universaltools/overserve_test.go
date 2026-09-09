package universaltools

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The read surface's half of terminal.max_overserve: what it measures, and what it serves
// instead when the answer outgrew the source it was built around.
//
// The trigger is deliberately hard to reach here, and that is the point -- a read's additions
// are already capped by the context budget, so the ceiling is the backstop for the cases that
// cap does not describe. What has to hold is what it measures and what it falls back TO;
// helper.OverserveBudget owns when.

func fixtureUnits() []readunit.Unit {
	return []readunit.Unit{{
		ID:    "demo/pkg.Hub",
		Kind:  domain.ResourceFunction,
		Path:  "pkg/nodes.go",
		Label: "pkg/nodes.go",
		Fence: "go",
		Body:  "func Hub(n int) int {\n\treturn n\n}",
		Context: func(b *strings.Builder, _ *renderstate.State) {
			b.WriteString("## demo/pkg.Node00: adds a step\n")
		},
		Incoming: []domain.ResourceRef{{ID: "demo/pkg.Caller"}},
	}}
}

// The denominator is the SOURCE a read delivers. Counting the sections aracne wrote around it
// would make every answer proportionate to itself.
func TestBodyBytesIsTheSourceAndNothingElse(t *testing.T) {
	units := fixtureUnits()
	want := len(units[0].Body)
	if got := bodyBytes(units); got != want {
		t.Errorf("bodyBytes = %d, want the body's %d", got, want)
	}
	if got := bodyBytes(nil); got != 0 {
		t.Errorf("bodyBytes of nothing = %d, want 0", got)
	}
}

// The fallback is the source and its imports: what the harness's own read would have handed
// back, with none of the sections that made this answer the expensive one.
func TestWithoutTopologyDropsOnlyTheSectionsAracneAdded(t *testing.T) {
	units := fixtureUnits()

	full := readunit.Render(units, readunit.Options{
		IncludeIncoming: true,
		State:           renderstate.New(),
	})
	if !strings.Contains(full, "# CONTEXT:") {
		t.Fatalf("the fixture renders no context section to drop:\n%s", full)
	}

	plain := readunit.Render(withoutTopology(units), readunit.Options{
		State: renderstate.New(),
	})
	if strings.Contains(plain, "# CONTEXT:") || strings.Contains(plain, "# USED BY:") {
		t.Errorf("a stripped read still carries aracne's sections:\n%s", plain)
	}
	if !strings.Contains(plain, "func Hub(n int) int {") {
		t.Errorf("stripping lost the source the caller asked for:\n%s", plain)
	}
	if len(plain) >= len(full) {
		t.Errorf("the fallback is not smaller: %d bytes against %d", len(plain), len(full))
	}
	// And it is a copy: the units the caller still holds are the ones it built.
	if units[0].Context == nil || units[0].Incoming == nil {
		t.Error("stripping mutated the caller's units")
	}
}

package tools

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The `read` option on warnings_list is INJECTED: the read path lives in universaltools, which
// imports this package, so the tool takes a closure instead of building one. These pin the two
// halves that live here -- what the schema advertises, and whether Run calls it.

// hasParam reports whether the tool's schema carries a property by that name.
func hasParam(params []Parameter, name string) bool {
	for _, p := range params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// A schema property is re-sent on every request, so a project that left features.warning_reads
// off must not be charged for an option the tool cannot honour.
func TestWarningsListHidesReadWhenNoExpanderIsWired(t *testing.T) {
	tool := NewWarningsList(nil)
	if hasParam(tool.Parameters(), "read") {
		t.Error("read is advertised with no expander wired")
	}
	if strings.Contains(tool.Description(), "read=true") {
		t.Error("the description advertises an option the schema does not carry")
	}
}

func TestWarningsListAdvertisesReadWhenWired(t *testing.T) {
	tool := NewWarningsList(nil).WithReads(func([]domain.TopologyWarning) string { return "" })
	if !hasParam(tool.Parameters(), "read") {
		t.Fatal("read is not advertised even though an expander is wired")
	}
	if !strings.Contains(tool.Description(), "read=true") {
		t.Error("the description does not mention the option the schema carries")
	}
}

// WithReads(nil) is the same switched-off state as never calling it, so a caller can wire
// conditionally without branching.
func TestWarningsListWithNilReaderStaysOff(t *testing.T) {
	if hasParam(NewWarningsList(nil).WithReads(nil).Parameters(), "read") {
		t.Error("WithReads(nil) advertised the option anyway")
	}
}

// Run must call the expander only when the caller asked, and must hand it the SORTED list --
// the expansion takes the first N and promises the next call continues from there.
func TestWarningsListRunPassesTheSortedListToTheExpander(t *testing.T) {
	warnings := []domain.TopologyWarning{
		{ID: "3", Kind: domain.WarnSignatureChanged, SourceID: "pkg.Callee", TargetID: "pkg.Zulu"},
		{ID: "1", Kind: domain.WarnSignatureChanged, SourceID: "pkg.Callee", TargetID: "pkg.Alpha"},
		{ID: "2", Kind: domain.WarnNodeRemoved, SourceID: "pkg.Mike", TargetID: "pkg.Gone"},
	}

	var got []domain.TopologyWarning
	expander := func(ws []domain.TopologyWarning) string {
		got = append([]domain.TopologyWarning(nil), ws...)
		return "EXPANSION"
	}

	out := NewWarningsList(nil).WithReads(expander).render(warnings, true)

	if len(got) != 3 {
		t.Fatalf("the expander saw %d warnings, want 3", len(got))
	}
	// node_removed sorts before signature_changed, then by source, then by target.
	if got[0].Kind != domain.WarnNodeRemoved || got[1].TargetID != "pkg.Alpha" || got[2].TargetID != "pkg.Zulu" {
		t.Errorf("the expander was handed an unsorted list: %v", got)
	}
	if !strings.Contains(out, "EXPANSION") {
		t.Errorf("read=true did not append the expansion:\n%s", out)
	}

	got = nil
	if out := NewWarningsList(nil).WithReads(expander).render(warnings, false); strings.Contains(out, "EXPANSION") {
		t.Errorf("the expansion was appended without read=true:\n%s", out)
	}
	if got != nil {
		t.Error("the expander ran without read=true")
	}
}

// A model that sends read=true to a project with the option switched off gets the listing, not
// an error about a key it was never offered.
func TestWarningsListIgnoresReadWithNoExpander(t *testing.T) {
	out := NewWarningsList(nil).render([]domain.TopologyWarning{
		{ID: "1", Kind: domain.WarnNodeRemoved, SourceID: "pkg.A", TargetID: "pkg.B", Message: "gone"},
	}, true)
	if !strings.Contains(out, "node_removed") {
		t.Errorf("read=true with no expander did not fall back to a plain listing:\n%s", out)
	}
}

package topogrep

import (
	"fmt"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func lineMatch(path string, line int, id, text string) Match {
	return Match{Path: path, Line: line, Text: text, ResourceID: id, MatchedOn: MatchContent}
}

func nodeMatch(id string, source MatchSource) Match {
	return Match{Path: "a.go", Line: 1, Text: "func " + id + "() {", ResourceID: id,
		MatchedOn: source, NodeHit: true}
}

// A node row is annotated whatever the budget says -- it is in the result BECAUSE of its name,
// and without the header it reads as an unexplained declaration line. So it is always worth
// describing.
func TestLazyTargetsAlwaysIncludeNodeRows(t *testing.T) {
	res := &Result{Matches: []Match{nodeMatch("pkg.Foo", MatchTitle)}}
	if got := LazyTargets(res, 0); len(got) != 1 || got[0] != "pkg.Foo" {
		t.Fatalf("targets = %v, want [pkg.Foo]", got)
	}
}

func TestLazyTargetsSkipAlreadyDescribed(t *testing.T) {
	m := nodeMatch("pkg.Foo", MatchTitle)
	m.Description = "already documented"
	if got := LazyTargets(&Result{Matches: []Match{m}}, 0); len(got) != 0 {
		t.Fatalf("targets = %v, want nothing", got)
	}
}

func TestLazyTargetsIncludeLineMatchResources(t *testing.T) {
	res := &Result{
		Matches: []Match{
			lineMatch("a.go", 10, "pkg.Foo", "\tif err != nil {"),
			lineMatch("a.go", 11, "pkg.Foo", "\t\treturn err"),
			lineMatch("b.go", 4, "pkg.Bar", "\treturn err"),
		},
		DistinctResources: 2,
	}
	got := LazyTargets(res, 0)
	if len(got) != 2 || got[0] != "pkg.Foo" || got[1] != "pkg.Bar" {
		t.Fatalf("targets = %v, want both resources in match order", got)
	}
}

func TestLazyTargetsDedupe(t *testing.T) {
	res := &Result{
		Matches: []Match{
			lineMatch("a.go", 10, "pkg.Foo", "one"),
			lineMatch("a.go", 11, "pkg.Foo", "two"),
		},
		DistinctResources: 1,
	}
	if got := LazyTargets(res, 0); len(got) != 1 {
		t.Fatalf("targets = %v, want one entry per resource", got)
	}
}

// The point of the projection. Above AnnotateLimit distinct resources, FormatResult prints no
// headers at all -- so a description written now would be paid for and never shown.
func TestLazyTargetsSkipLineMatchesWhenAnnotationIsOff(t *testing.T) {
	var matches []Match
	for i := 0; i < AnnotateLimit+5; i++ {
		matches = append(matches, lineMatch("a.go", i+1, "pkg.Fn"+string(rune('A'+i%26))+string(rune('a'+i/26)), "hit"))
	}
	res := &Result{Matches: matches, DistinctResources: AnnotateLimit + 5}
	if got := LazyTargets(res, 0); len(got) != 0 {
		t.Fatalf("targets = %v, want nothing: this result prints no headers", got)
	}
}

// A node row still qualifies in that same result, because node rows are annotated
// unconditionally.
func TestLazyTargetsKeepNodeRowsWhenAnnotationIsOff(t *testing.T) {
	matches := []Match{nodeMatch("pkg.Named", MatchTitle)}
	for i := 0; i < AnnotateLimit+5; i++ {
		matches = append(matches, lineMatch("a.go", i+1, "pkg.Fn"+string(rune('A'+i%26))+string(rune('a'+i/26)), "hit"))
	}
	res := &Result{Matches: matches, DistinctResources: AnnotateLimit + 5}
	got := LazyTargets(res, 0)
	if len(got) != 1 || got[0] != "pkg.Named" {
		t.Fatalf("targets = %v, want just the node row", got)
	}
}

// The projection stops where the annotation budget would: filling every one of these would
// push the headers past AnnotateOverheadBudget and turn annotation off for the whole result,
// which is strictly worse than not filling at all.
func TestLazyTargetsStopAtTheAnnotationBudget(t *testing.T) {
	// Twenty resources whose headers currently fit comfortably (360 bytes of header against
	// 2240 of content, well under the 25% budget), but where describing all twenty would add
	// 2400 bytes of prose and take annotation down with it.
	build := func() *Result {
		var matches []Match
		for i := 0; i < 20; i++ {
			matches = append(matches, lineMatch("a.go", i+1,
				fmt.Sprintf("pkg.Fn%06d", i), strings.Repeat("x", 100)))
		}
		return &Result{Matches: matches, DistinctResources: 20}
	}

	res := build()
	if !annotationFits(res.Matches) {
		t.Fatal("fixture is wrong: annotation must be ON before the fill for this test to mean anything")
	}

	targets := LazyTargets(res, 0)
	if len(targets) == 0 {
		t.Fatal("the projection allowed nothing, though annotation had room for some")
	}
	if len(targets) == 20 {
		t.Fatal("the projection allowed everything; it is not bounding anything")
	}

	// Filling exactly what the projection allowed keeps annotation on...
	describe(res, targets)
	if !annotationFits(res.Matches) {
		t.Fatal("filling the projected targets broke annotation, which the projection exists to prevent")
	}

	// ...and filling every resource would not, which is what makes the bound load-bearing.
	all := build()
	var everything []string
	for _, m := range all.Matches {
		everything = append(everything, m.ResourceID)
	}
	describe(all, everything)
	if annotationFits(all.Matches) {
		t.Fatal("fixture is wrong: describing everything was supposed to break annotation")
	}
}

// describe stamps a budget-length description onto every named resource, the way a completed
// fill would.
func describe(res *Result, ids []string) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for i := range res.Matches {
		if want[res.Matches[i].ResourceID] {
			res.Matches[i].Description = strings.Repeat("d", domain.DescriptionBudgetFunction)
		}
	}
}

func TestLazyTargetsToleratesEmptyResults(t *testing.T) {
	if got := LazyTargets(nil, 0); got != nil {
		t.Fatalf("targets = %v, want nil", got)
	}
	if got := LazyTargets(&Result{}, 0); got != nil {
		t.Fatalf("targets = %v, want nil", got)
	}
}

func TestApplyDescriptionsRewritesFromTheTopology(t *testing.T) {
	res := &Result{Matches: []Match{
		lineMatch("a.go", 1, "pkg.Foo", "hit"),
		lineMatch("a.go", 2, "pkg.Missing", "hit"),
		{Path: "a.go", Line: 3, Text: "hit"}, // no resource
	}}
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"pkg.Foo": {ID: "pkg.Foo", Description: "freshly written"},
	}}
	ApplyDescriptions(res, topo)
	if res.Matches[0].Description != "freshly written" {
		t.Errorf("match 0 = %q", res.Matches[0].Description)
	}
	if res.Matches[1].Description != "" || res.Matches[2].Description != "" {
		t.Error("a match with no matching resource should be left alone")
	}
}

func TestApplyDescriptionsToleratesNil(t *testing.T) {
	ApplyDescriptions(nil, &domain.Topology{})
	ApplyDescriptions(&Result{}, nil)
}

// The whole reason the search fill exists: a described node renders its header, an undescribed
// one renders a bare id.
func TestFormatResultShowsAFilledDescription(t *testing.T) {
	res := &Result{
		Matches:           []Match{lineMatch("a.go", 10, "pkg.Foo", "\treturn err")},
		DistinctResources: 1,
	}
	opt := Options{Pattern: "err"}
	if strings.Contains(FormatResult(res, opt), "—") {
		t.Fatal("an undescribed node should not render a description separator")
	}
	ApplyDescriptions(res, &domain.Topology{Resources: map[string]domain.Resource{
		"pkg.Foo": {ID: "pkg.Foo", Description: "wraps the retry loop"},
	}})
	out := FormatResult(res, opt)
	if !strings.Contains(out, "pkg.Foo — wraps the retry loop") {
		t.Fatalf("output does not carry the new description:\n%s", out)
	}
}

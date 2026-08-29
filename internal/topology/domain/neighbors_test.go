package domain

import "testing"

// graph builds a topology from (id, kind, connections) triples.
func graph(resources ...Resource) *Topology {
	topo := &Topology{Resources: map[string]Resource{}}
	for _, r := range resources {
		topo.Resources[r.ID] = r
	}
	return topo
}

func TestFileMembersIsTransitive(t *testing.T) {
	topo := graph(
		Resource{ID: "f.go", Kind: ResourceFile, Connections: map[string][]string{
			"has_function": {"Free"},
			"has_struct":   {"T"},
		}},
		Resource{ID: "T", Kind: ResourceStruct, Connections: map[string][]string{
			"methods":     {"T.M"},
			"constructor": {"NewT"},
		}},
		Resource{ID: "T.M", Kind: ResourceMethod},
		Resource{ID: "NewT", Kind: ResourceFunction},
		Resource{ID: "Free", Kind: ResourceFunction},
	)

	got := map[string]bool{}
	for _, id := range FileMembers(topo, "f.go") {
		got[id] = true
	}
	// A class's methods and constructor live in the file too; missing them would let them
	// surface in CONTEXT even though the file's source already shows them.
	for _, want := range []string{"Free", "T", "T.M", "NewT"} {
		if !got[want] {
			t.Fatalf("FileMembers missing %q: %v", want, got)
		}
	}
	if got["f.go"] {
		t.Fatal("the file should not list itself as a member")
	}
}

func TestFileMembersTerminatesOnCycles(t *testing.T) {
	// A containment cycle must not hang the walk.
	topo := graph(
		Resource{ID: "f.go", Kind: ResourceFile, Connections: map[string][]string{"has_struct": {"A"}}},
		Resource{ID: "A", Kind: ResourceStruct, Connections: map[string][]string{"methods": {"B"}}},
		Resource{ID: "B", Kind: ResourceMethod, Connections: map[string][]string{"methods": {"A"}}},
	)
	if got := len(FileMembers(topo, "f.go")); got != 2 {
		t.Fatalf("expected 2 members, got %d", got)
	}
}

func TestOutgoingNeighborsSkipsSelfAndExcluded(t *testing.T) {
	topo := graph(
		Resource{ID: "A", Kind: ResourceFunction, Connections: map[string][]string{
			"calls":       {"B", "C"},
			"uses_struct": {"T"},
			// Containment and reverse edges are not "outgoing": the first is the body, the
			// second belongs to USED BY.
			"has_function":    {"Nested"},
			"implemented_by":  {"Impl"},
			"uses_dependency": {"fmt"},
		}},
		Resource{ID: "B", Kind: ResourceFunction},
		Resource{ID: "C", Kind: ResourceFunction},
		Resource{ID: "T", Kind: ResourceStruct},
		Resource{ID: "Nested", Kind: ResourceFunction},
		Resource{ID: "Impl", Kind: ResourceStruct},
	)

	got := map[string]bool{}
	for _, n := range OutgoingNeighbors(topo, []string{"A"}, map[string]bool{"C": true}) {
		got[n.ID] = true
	}
	if !got["B"] || !got["T"] {
		t.Fatalf("expected B and T as neighbours: %v", got)
	}
	if got["C"] {
		t.Fatal("excluded id must not be returned")
	}
	if got["A"] {
		t.Fatal("a queried id is never its own neighbour")
	}
	if got["Nested"] || got["Impl"] {
		t.Fatalf("containment/reverse edges are not outgoing neighbours: %v", got)
	}
	// A dependency target that is not itself a resource is skipped rather than invented.
	if got["fmt"] {
		t.Fatal("unknown target should be skipped")
	}
}

func TestLocationContains(t *testing.T) {
	class := Location{Path: "a.py", StartsAt: 1, EndsAt: 50}
	method := Location{Path: "a.py", StartsAt: 10, EndsAt: 12}
	if !class.Contains(method) {
		t.Fatal("a Python class cut contains its method")
	}
	if method.Contains(class) {
		t.Fatal("containment is not symmetric")
	}
	// A Go struct declaration sits above its methods, so it contains none of them.
	structDecl := Location{Path: "a.go", StartsAt: 1, EndsAt: 3}
	goMethod := Location{Path: "a.go", StartsAt: 5, EndsAt: 7}
	if structDecl.Contains(goMethod) {
		t.Fatal("a Go type declaration must not claim to contain its method")
	}
	if class.Contains(Location{Path: "b.py", StartsAt: 10, EndsAt: 12}) {
		t.Fatal("containment must not cross files")
	}
	// An unmeasurable span must not be treated as containing anything.
	if (Location{Path: "a.py"}).Contains(method) {
		t.Fatal("a zero span contains nothing")
	}
}

func TestInlineParentThreshold(t *testing.T) {
	f := ContextFilter{MaxInlineParentLines: 40}
	if !f.InlineParent(40) {
		t.Fatal("a parent exactly at the ceiling still inlines")
	}
	if f.InlineParent(41) {
		t.Fatal("a parent over the ceiling must not inline")
	}
	// An unmeasurable span keeps the pre-threshold behaviour: such cuts are one-liners.
	if !f.InlineParent(0) {
		t.Fatal("unknown span should inline")
	}
	if (ContextFilter{MaxInlineParentLines: 0}).InlineParent(1) {
		t.Fatal("0 disables inlining entirely")
	}
	if !(ContextFilter{MaxInlineParentLines: -1}).InlineParent(10000) {
		t.Fatal("negative means no ceiling")
	}
}

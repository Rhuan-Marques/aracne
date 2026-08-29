package renderstate

import "testing"

func TestFirstTimeDedupesByID(t *testing.T) {
	st := New()
	if !st.FirstTime("A") {
		t.Fatal("first sighting should render")
	}
	if st.FirstTime("A") {
		t.Fatal("a neighbour reachable by two edges must render once")
	}
}

func TestExcludeIDKeepsBodyOutOfContext(t *testing.T) {
	st := New()
	st.ExcludeID("A")
	if !st.IsExcluded("A") {
		t.Fatal("ExcludeID should mark the id")
	}
	if st.Renderable("A") {
		t.Fatal("a resource shown as source must not be renderable in CONTEXT")
	}
	if !st.Renderable("B") {
		t.Fatal("an unrelated id stays renderable")
	}
	if st.Renderable("B") {
		t.Fatal("Renderable also consumes the FirstTime slot")
	}
}

func TestResetImportsIsScopedToImports(t *testing.T) {
	st := New()
	st.ExcludeID("A")
	if got := st.NewImports([]string{"os", "fmt"}); len(got) != 2 {
		t.Fatalf("first block carries both imports, got %v", got)
	}
	if got := st.NewImports([]string{"os"}); len(got) != 0 {
		t.Fatalf("a repeat within the same group is suppressed, got %v", got)
	}

	st.ResetImports()

	if got := st.NewImports([]string{"os"}); len(got) != 1 {
		t.Fatalf("a new group must re-declare its own imports, got %v", got)
	}
	// Everything else the state tracks is response-wide and must survive the reset.
	if !st.IsExcluded("A") {
		t.Fatal("ResetImports must not clear the exclusion ledger")
	}
}

func TestParentSeenIsCheckAndSet(t *testing.T) {
	st := New()
	if st.ParentSeen("type T struct{}") {
		t.Fatal("first sighting is not seen")
	}
	if !st.ParentSeen("type T struct{}") {
		t.Fatal("second sighting must back-reference")
	}
	// MarkRendered feeds the same set, so a cut shown in the body is not repeated as a
	// neighbour's enclosing type.
	st2 := New()
	st2.MarkRendered("type U struct{}")
	if !st2.ParentSeen("type U struct{}") {
		t.Fatal("MarkRendered should suppress a later parent render")
	}
}

func TestBudgetReportsWhatItWithheld(t *testing.T) {
	st := New()
	st.MaxEntries = 1
	if !st.Allow(10) {
		t.Fatal("first entry fits")
	}
	if st.Allow(10) {
		t.Fatal("second entry is over the entry cap")
	}
	if st.Omitted() != 1 {
		t.Fatalf("Omitted = %d, want 1", st.Omitted())
	}
	if st.Trailer() == "" {
		t.Fatal("silent truncation reads as 'that is all there is'")
	}
}

func TestNilStateIsUsable(t *testing.T) {
	var st *State
	if !st.FirstTime("A") || !st.Renderable("A") || st.IsExcluded("A") {
		t.Fatal("a nil state must not suppress rendering")
	}
	st.ExcludeID("A")
	st.ResetImports()
	st.MarkRendered("x")
	if st.ParentSeen("x") {
		t.Fatal("nil state tracks nothing")
	}
}

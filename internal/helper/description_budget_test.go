package helper

import (
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

// The cap is a gate on descriptions coming IN. A rejected write must leave the row
// exactly as it was rather than landing a truncated version of itself.
func TestUpdateDescriptionRejectsOverBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true) // proj/src/a.Foo is a struct: 100-char budget

	over := strings.Repeat("x", domain.DescriptionBudgetType+1)
	if err := UpdateDescription(path, "struct", "proj/src/a.Foo", over); err == nil {
		t.Fatal("want an error for an over-budget description, got nil")
	}
	topo, err := ReadDb(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := topo.Resources["proj/src/a.Foo"].Description; got != "" {
		t.Fatalf("a rejected write must not touch the row, got %q", got)
	}

	atBudget := strings.Repeat("x", domain.DescriptionBudgetType)
	if err := UpdateDescription(path, "struct", "proj/src/a.Foo", atBudget); err != nil {
		t.Fatalf("a description at the budget should land: %v", err)
	}
}

// Descriptions already in the database are grandfathered: nothing re-checks them, and a
// bulk restore can put a pre-cap description back verbatim. Only the next single write
// against that row has to fit.
func TestExistingLongDescriptionsSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)

	long := strings.Repeat("x", domain.DescriptionBudgetType*3)
	n, err := UpdateDescriptions(path, map[string]string{"proj/src/a.Foo": long})
	if err != nil {
		t.Fatalf("bulk restore must stay uncapped: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row changed, got %d", n)
	}

	topo, err := ReadDb(path)
	if err != nil {
		t.Fatal(err)
	}
	if topo.Resources["proj/src/a.Foo"].Description != long {
		t.Fatal("an existing over-budget description must be readable back unchanged")
	}

	// Reading it back is fine; replacing it with another over-budget one is not.
	if err := UpdateDescription(path, "struct", "proj/src/a.Foo", long); err == nil {
		t.Fatal("a new over-budget write is rejected even when the row already holds a long one")
	}
}

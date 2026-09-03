package helper

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

// seedDB writes a topology whose descriptions bypass DescriptionForStorage, the way rows
// written before that gate existed are grandfathered in a real database.
func seedOversizedDB(t *testing.T, rows map[string]struct {
	kind domain.ResourceKind
	desc string
},
) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{Resources: map[string]domain.Resource{}}
	for id, r := range rows {
		topo.Resources[id] = domain.Resource{ID: id, Name: id, Kind: r.kind,
			Location: domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 2}}
	}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	for id, r := range rows {
		if err := forceDescription(dbPath, id, r.desc); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	return dbPath
}

func forceDescription(dbPath, id, desc string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		_, err := db.Exec("UPDATE resources SET description = ? WHERE id = ?", desc, id)
		return err
	})
}

func descOf(t *testing.T, dbPath, id string) string {
	t.Helper()
	var out string
	if err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		return db.QueryRow("SELECT COALESCE(description,'') FROM resources WHERE id = ?", id).Scan(&out)
	}); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return out
}

// The sweep removes exactly the over-budget rows and leaves everything else untouched. This is
// the population `descriptions clear --oversized` exists for: 34% of harvested Go doc comments
// on grafana/k6, written before DescriptionForStorage gated the port.
func TestClearOversizedDescriptions(t *testing.T) {
	fits := "Serve starts the listener and blocks until the context is cancelled."
	over := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	typeOver := strings.Repeat("y", domain.DescriptionBudgetType+1)
	db := seedOversizedDB(t, map[string]struct {
		kind domain.ResourceKind
		desc string
	}{
		"fn:ok":   {domain.ResourceFunction, fits},
		"fn:over": {domain.ResourceFunction, over},
		"st:over": {domain.ResourceStruct, typeOver},
		"fn:none": {domain.ResourceFunction, ""},
	})

	n, err := ClearOversizedDescriptions(db, nil)
	if err != nil {
		t.Fatalf("ClearOversizedDescriptions: %v", err)
	}
	if n != 2 {
		t.Errorf("cleared %d, want 2", n)
	}
	if got := descOf(t, db, "fn:ok"); got != fits {
		t.Errorf("a within-budget description must survive, got %q", got)
	}
	for _, id := range []string{"fn:over", "st:over"} {
		if got := descOf(t, db, id); got != "" {
			t.Errorf("%s should be cleared, got %d chars", id, len(got))
		}
	}
	// Idempotent: a second sweep has nothing left to do.
	if n2, err := ClearOversizedDescriptions(db, nil); err != nil || n2 != 0 {
		t.Errorf("second sweep cleared %d (err %v), want 0", n2, err)
	}
}

// --target restricts the sweep, so a project can clear one kind without touching the rest.
func TestClearOversizedDescriptionsRespectsTargets(t *testing.T) {
	over := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	typeOver := strings.Repeat("y", domain.DescriptionBudgetType+1)
	db := seedOversizedDB(t, map[string]struct {
		kind domain.ResourceKind
		desc string
	}{
		"fn:over": {domain.ResourceFunction, over},
		"st:over": {domain.ResourceStruct, typeOver},
	})

	n, err := ClearOversizedDescriptions(db, []domain.ResourceKind{domain.ResourceStruct})
	if err != nil {
		t.Fatalf("ClearOversizedDescriptions: %v", err)
	}
	if n != 1 {
		t.Errorf("cleared %d, want 1 (structs only)", n)
	}
	if descOf(t, db, "fn:over") == "" {
		t.Error("a function description must survive a struct-only sweep")
	}
	if descOf(t, db, "st:over") != "" {
		t.Error("the struct description should be cleared")
	}
}

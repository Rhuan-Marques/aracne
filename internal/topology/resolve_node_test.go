package topology

import (
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// managerWith returns a manager over a temp database holding the given resources.
func managerWith(t *testing.T, resources map[string]domain.Resource) *TopologyManager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{Root: filepath.Dir(dbPath), Language: "go", Resources: resources}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	mgr := New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return mgr
}

// TestResolveNodeIDRejectsUnknownWithCandidates is the regression test for the defect that
// made the whole bug pipeline lossy: an unknown node_id was accepted, stored, and then
// silently deleted by CleanupOrphanedBugs at the next scan that touched the graph. A hunter
// that misspelled one FQN lost that finding with no signal to anyone.
func TestResolveNodeIDRejectsUnknownWithCandidates(t *testing.T) {
	mgr := managerWith(t, map[string]domain.Resource{
		"proj/pkg.Divide": {ID: "proj/pkg.Divide", Kind: domain.ResourceFunction, Name: "Divide"},
	})

	_, err := mgr.ResolveNodeID("does.not.Exist")
	if err == nil {
		t.Fatal("an unknown node id must be rejected, not stored and silently reaped later")
	}
	if !strings.Contains(err.Error(), "does.not.Exist") {
		t.Errorf("the error should name the id the caller passed: %v", err)
	}
}

// TestResolveNodeIDSnapsToCanonicalID pins the other half. Resource ids are rooted
// differently per language, so a hunter reading source routinely guesses a suffix rather than
// the stored id. Snapping keeps the finding AND converges two hunters' spellings of the same
// node onto one id, which is what makes duplicate detection work at all.
func TestResolveNodeIDSnapsToCanonicalID(t *testing.T) {
	mgr := managerWith(t, map[string]domain.Resource{
		"worktree/src/app.Flask.register_blueprint": {
			ID:   "worktree/src/app.Flask.register_blueprint",
			Kind: domain.ResourceMethod,
			Name: "register_blueprint",
		},
	})

	got, err := mgr.ResolveNodeID("app.Flask.register_blueprint")
	if err != nil {
		t.Fatalf("a unique suffix should resolve: %v", err)
	}
	if got != "worktree/src/app.Flask.register_blueprint" {
		t.Errorf("ResolveNodeID = %q, want the canonical stored id", got)
	}
}

// TestResolveNodeIDFailsOpenWithoutTopology: a project that has not scanned yet must still be
// able to file bugs. Refusing here would make the tool unusable in exactly the situation
// where a user is trying it out for the first time.
func TestResolveNodeIDFailsOpenWithoutTopology(t *testing.T) {
	mgr := New()
	if err := mgr.Load(filepath.Join(t.TempDir(), "missing.db")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := mgr.ResolveNodeID("whatever.Thing")
	if err != nil {
		t.Fatalf("an unreadable topology must fail open, got: %v", err)
	}
	if got != "whatever.Thing" {
		t.Errorf("fail-open should return the id unchanged, got %q", got)
	}
}

// TestBugReportedOnUnknownNodeDoesNotSurviveAScan documents WHY the validation exists, by
// exercising the loss path directly: a bug filed against an id the topology does not hold is
// removed by the orphan cleanup, with no error and no record.
func TestBugReportedOnUnknownNodeDoesNotSurviveAScan(t *testing.T) {
	mgr := managerWith(t, map[string]domain.Resource{
		"proj/pkg.Divide": {ID: "proj/pkg.Divide", Kind: domain.ResourceFunction, Name: "Divide"},
	})

	if _, err := mgr.CreateBug("does.not.Exist", "hallucinated node"); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := helper.CleanupOrphanedBugs(mgr.DbPath(), topo); err != nil {
		t.Fatalf("CleanupOrphanedBugs: %v", err)
	}

	bugs, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(bugs) != 0 {
		t.Fatalf("expected the orphaned bug to be reaped, got %d", len(bugs))
	}
	// ...which is exactly why ResolveNodeID must reject the id up front.
	if _, err := mgr.ResolveNodeID("does.not.Exist"); err == nil {
		t.Fatal("ResolveNodeID must reject the id that the cleanup would silently reap")
	}
}

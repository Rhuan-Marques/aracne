package cli

import (
	"path/filepath"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// A warning must reach the model whoever discovered it.
//
// driftCheck used to report the warnings from the IncrementalScan it ran itself, which works
// only while nothing else keeps the topology fresh. Turn on `arac scanner run` -- which exists
// precisely so freshness is not paid for on the read path -- and the hook's own scan finds the
// file already indexed, produces nothing, and the model is told nothing. Measured on a real
// fixture, editing a function with eleven cross-file callers: 11 warnings reported without the
// watcher, 0 with it, the warnings correct and present in the database both times.

func warnDB(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:      dir,
		Resources: map[string]domain.Resource{},
		Warnings:  map[string]domain.TopologyWarning{},
		Errors:    map[string]string{},
	}
	for _, id := range ids {
		topo.Warnings[id] = domain.TopologyWarning{
			ID: id, SourceID: "pkg.F", TargetID: "pkg.Caller",
			Kind: domain.WarnSignatureChanged, Message: "verify " + id,
		}
	}
	if err := helper.WriteDb(topo, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func addWarning(t *testing.T, db string, ids ...string) {
	t.Helper()
	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		topo.Warnings[id] = domain.TopologyWarning{
			ID: id, SourceID: "pkg.F", TargetID: "pkg.Caller",
			Kind: domain.WarnSignatureChanged, Message: "verify " + id,
		}
	}
	if err := helper.WriteDb(topo, db); err != nil {
		t.Fatal(err)
	}
}

func TestUnreportedWarningsReportsWhatIsNewInTheTable(t *testing.T) {
	db := warnDB(t, "pre-existing")
	// The pre-tool seed records what this call FOUND, so the pre-existing warning is not
	// dumped on the model as if its command had caused it.
	seedReportedWarnings(db)

	// Something else -- the watcher, a scan in another terminal -- adds two warnings.
	addWarning(t, db, "new-a", "new-b")

	got := unreportedWarnings(db)
	if len(got) != 2 {
		t.Fatalf("reported %d warnings, want the 2 that appeared: %+v", len(got), got)
	}
	for _, w := range got {
		if w.ID == "pre-existing" {
			t.Error("a warning that predates this command was reported as if it were new")
		}
	}
}

func TestUnreportedWarningsDoesNotRepeatItself(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)
	addWarning(t, db, "w1")

	if n := len(unreportedWarnings(db)); n != 1 {
		t.Fatalf("first call reported %d, want 1", n)
	}
	if n := len(unreportedWarnings(db)); n != 0 {
		t.Errorf("second call repeated %d warning(s); an agent that sees the same warning after "+
			"every command learns to skim past all of them", n)
	}
}

// A break that is fixed and then reintroduced is worth knowing about the second time. The
// stored set is REPLACED rather than accumulated, so an id that left the table and came back
// is absent from the record when it returns.
func TestUnreportedWarningsReportsAReintroducedBreak(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)

	addWarning(t, db, "w1")
	if n := len(unreportedWarnings(db)); n != 1 {
		t.Fatalf("first break reported %d, want 1", n)
	}

	// fixed
	topo, _ := helper.ReadDb(db)
	delete(topo.Warnings, "w1")
	if err := helper.WriteDb(topo, db); err != nil {
		t.Fatal(err)
	}
	if n := len(unreportedWarnings(db)); n != 0 {
		t.Fatalf("fixing produced %d warnings", n)
	}

	// broken again
	addWarning(t, db, "w1")
	if n := len(unreportedWarnings(db)); n != 1 {
		t.Error("the same break reintroduced must be reported again")
	}
}

// The seed is what stops a first shell write dumping the repository's existing warnings, and
// it must not overwrite a record that already exists -- that would re-arm the dump on every
// tool call.
func TestSeedIsWriteOnceAndSuppressesPreExisting(t *testing.T) {
	db := warnDB(t, "old-1", "old-2")
	seedReportedWarnings(db)
	if n := len(unreportedWarnings(db)); n != 0 {
		t.Fatalf("seeded state still reported %d pre-existing warning(s)", n)
	}
	addWarning(t, db, "new")
	// A second seed (the next tool call's PreToolUse) must not reset the record.
	seedReportedWarnings(db)
	got := unreportedWarnings(db)
	if len(got) != 1 || got[0].ID != "new" {
		t.Errorf("after a second seed, reported %+v; want only the new warning", got)
	}
}

// Every failure here is a bookkeeping failure, and a hook that breaks the agent's tool call is
// worse than one that misses a warning.
func TestWarnStateSurvivesAMissingDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.db")
	seedReportedWarnings(missing)
	if got := unreportedWarnings(missing); got != nil {
		t.Errorf("a missing database produced %+v, want nothing", got)
	}
}

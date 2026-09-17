package helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// jobsProject writes a database holding the given resource ids, so the LEFT JOIN in
// ReadDescriptionJobStatus has rows to join against.
func jobsProject(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	topo := &domain.Topology{Root: dir, Language: "go", Resources: map[string]domain.Resource{}}
	for _, id := range ids {
		topo.Resources[id] = domain.Resource{
			ID: id, Name: id, Kind: domain.ResourceFunction, Language: "go",
			Location: domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 2},
		}
	}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func targets(ids ...string) []DescriptionJobTarget {
	out := make([]DescriptionJobTarget, 0, len(ids))
	for _, id := range ids {
		out = append(out, DescriptionJobTarget{ID: id, Fingerprint: "fp:" + id})
	}
	return out
}

// claim is the common call, with a lease comfortably in the future.
func claim(t *testing.T, dbPath, jobID string, now time.Time, maxWorkers int, ids ...string) ([]string, []string) {
	t.Helper()
	won, watch, err := ClaimDescriptionTargets(dbPath, jobID, os.Getpid(), targets(ids...),
		now, now.Add(10*time.Minute), maxWorkers)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return won, watch
}

// THE CENTRAL GUARANTEE. Two fills over the same resource: one owns it, the other is told to
// watch. If this ever regresses, two workers describe one resource and the whole point of the
// table is gone.
func TestClaimIsExclusiveAndTellsTheLoserToWatch(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	now := time.Now()

	won, watch := claim(t, db, "job-1", now, 0, "pkg.A")
	if len(won) != 1 || won[0] != "pkg.A" || len(watch) != 0 {
		t.Fatalf("the first claim must win outright: won=%v watch=%v", won, watch)
	}

	// The second fill has to heartbeat-check against a LIVE claim, so bump it first the way a
	// started worker would.
	if _, err := HeartbeatDescriptionJob(db, "job-1", now); err != nil {
		t.Fatal(err)
	}
	won2, watch2 := claim(t, db, "job-2", now, 0, "pkg.A")
	if len(won2) != 0 {
		t.Fatalf("a live claim must not be stolen: won=%v", won2)
	}
	if len(watch2) != 1 || watch2[0] != "pkg.A" {
		t.Fatalf("the loser must be told to watch the live claim, got %v", watch2)
	}
}

// THE PARTITION. A fill that overlaps someone else's live work must still start everything else
// -- this is the behaviour the owner asked for explicitly, and the reason claim returns two
// lists rather than a bool.
func TestClaimPartitionsWonAndWatched(t *testing.T) {
	db := jobsProject(t, "pkg.A", "pkg.B", "pkg.C")
	now := time.Now()

	claim(t, db, "job-1", now, 0, "pkg.A")
	if _, err := HeartbeatDescriptionJob(db, "job-1", now); err != nil {
		t.Fatal(err)
	}

	won, watch := claim(t, db, "job-2", now, 0, "pkg.A", "pkg.B", "pkg.C")
	if len(won) != 2 || won[0] != "pkg.B" || won[1] != "pkg.C" {
		t.Fatalf("the non-overlapping ids must be claimed, got %v", won)
	}
	if len(watch) != 1 || watch[0] != "pkg.A" {
		t.Fatalf("the overlapping id must be watched, got %v", watch)
	}
}

func TestStaleClaimIsReclaimed(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	old := time.Now().Add(-time.Hour)

	claim(t, db, "job-1", old, 0, "pkg.A")
	// A worker that ran and then died: it heartbeated once, long ago.
	if _, err := HeartbeatDescriptionJob(db, "job-1", old); err != nil {
		t.Fatal(err)
	}

	won, _ := claim(t, db, "job-2", time.Now(), 0, "pkg.A")
	if len(won) != 1 {
		t.Fatalf("a claim whose worker stopped heartbeating must be reclaimable, got %v", won)
	}
}

func TestExpiredLeaseIsReclaimedEvenWhileHeartbeating(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	now := time.Now()

	// A lease that has already run out, with a heartbeat as fresh as it gets: the lease is the
	// backstop for a worker that outlived its own watchdog, so it must win over liveness.
	if _, _, err := ClaimDescriptionTargets(db, "job-1", os.Getpid(), targets("pkg.A"),
		now, now.Add(-time.Second), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := HeartbeatDescriptionJob(db, "job-1", now); err != nil {
		t.Fatal(err)
	}

	won, _ := claim(t, db, "job-2", now, 0, "pkg.A")
	if len(won) != 1 {
		t.Fatalf("an expired lease must be reclaimable, got %v", won)
	}
}

// A worker that dies before it can mark anything failed used to buy a fresh spawn on every
// read, forever. A stale claim that never heartbeated is a FAILED START and gets the cooldown.
func TestNeverStartedClaimBecomesFailedInsteadOfRespawning(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	old := time.Now().Add(-time.Hour)

	claim(t, db, "job-1", old, 0, "pkg.A") // claimed, then the worker never came up

	won, watch := claim(t, db, "job-2", time.Now(), 0, "pkg.A")
	if len(won) != 0 {
		t.Fatalf("a failed start must cool down, not re-spawn immediately: won=%v", won)
	}
	if len(watch) != 0 {
		t.Fatalf("a cooling-down failure is nobody's live work, so there is nothing to watch: %v", watch)
	}

	jobs, err := ListDescriptionJobs(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != DescriptionJobFailed {
		t.Fatalf("want one failed row, got %+v", jobs)
	}

	// Past the cooldown it is claimable again.
	won, _ = claim(t, db, "job-3", time.Now().Add(DescriptionJobFailCooldown+time.Second), 0, "pkg.A")
	if len(won) != 1 {
		t.Fatalf("the cooldown must expire, got %v", won)
	}
}

// A claim generating a description of code that has since moved is not worth waiting for.
func TestChangedFingerprintTakesOverALiveClaim(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	now := time.Now()

	claim(t, db, "job-1", now, 0, "pkg.A")
	if _, err := HeartbeatDescriptionJob(db, "job-1", now); err != nil {
		t.Fatal(err)
	}

	won, _, err := ClaimDescriptionTargets(db, "job-2", os.Getpid(),
		[]DescriptionJobTarget{{ID: "pkg.A", Fingerprint: "moved"}},
		now, now.Add(time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(won) != 1 {
		t.Fatalf("a claim on superseded code must yield, got %v", won)
	}
}

// The cap is checked inside the claim transaction, and hitting it must not cost the watch list.
func TestWorkerCapBlocksClaimsButNotWatching(t *testing.T) {
	db := jobsProject(t, "pkg.A", "pkg.B")
	now := time.Now()

	claim(t, db, "job-1", now, 0, "pkg.A")
	if _, err := HeartbeatDescriptionJob(db, "job-1", now); err != nil {
		t.Fatal(err)
	}

	won, watch := claim(t, db, "job-2", now, 1, "pkg.A", "pkg.B")
	if len(won) != 0 {
		t.Fatalf("at the cap nothing may be claimed, got %v", won)
	}
	if len(watch) != 1 || watch[0] != "pkg.A" {
		t.Fatalf("a capped fill still watches live work, got %v", watch)
	}
}

// The heartbeat is the ownership check: zero rows means stop.
func TestHeartbeatReportsLostOwnership(t *testing.T) {
	db := jobsProject(t, "pkg.A")
	now := time.Now()

	claim(t, db, "job-1", now, 0, "pkg.A")
	owned, err := HeartbeatDescriptionJob(db, "job-1", now)
	if err != nil || owned != 1 {
		t.Fatalf("want 1 owned, got %d (%v)", owned, err)
	}

	if _, err := RequestDescriptionJobCancel(db, "job-1"); err != nil {
		t.Fatal(err)
	}
	owned, err = HeartbeatDescriptionJob(db, "job-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if owned != 0 {
		t.Fatalf("a cancelled job must report no ownership, got %d", owned)
	}

	// And the same for rows that simply vanished underneath it.
	claim(t, db, "job-2", now, 0, "pkg.A")
	if err := ClearDescriptionJobs(db); err != nil {
		t.Fatal(err)
	}
	if owned, err = HeartbeatDescriptionJob(db, "job-2", now); err != nil || owned != 0 {
		t.Fatalf("cleared rows must report no ownership, got %d (%v)", owned, err)
	}
}

func TestReadDescriptionJobStatusJoinsDescriptionAndClaim(t *testing.T) {
	db := jobsProject(t, "pkg.A", "pkg.B")
	now := time.Now()
	claim(t, db, "job-1", now, 0, "pkg.A")
	if err := UpdateDescription(db, domain.ResourceFunction, "pkg.B", "does a thing"); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDescriptionJobStatus(db, []string{"pkg.A", "pkg.B", "pkg.GONE"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["pkg.A"].Claimed || got["pkg.A"].Description != "" {
		t.Fatalf("pkg.A should be claimed and undescribed, got %+v", got["pkg.A"])
	}
	if got["pkg.B"].Claimed || got["pkg.B"].Description != "does a thing" {
		t.Fatalf("pkg.B should be described and unclaimed, got %+v", got["pkg.B"])
	}
	if _, ok := got["pkg.GONE"]; ok {
		t.Fatal("a resource that is not in the graph must be absent, so a watcher stops waiting for it")
	}
}

// Every reader must treat a database that has never run a fill as empty, not as broken.
func TestJobReadersTolerateAMissingTable(t *testing.T) {
	db := jobsProject(t, "pkg.A")

	status, err := ReadDescriptionJobStatus(db, []string{"pkg.A"})
	if err != nil {
		t.Fatalf("status on a missing table must not error: %v", err)
	}
	if status["pkg.A"].Claimed {
		t.Fatal("nothing can be claimed when the table does not exist")
	}
	if jobs, jerr := ListDescriptionJobs(db); jerr != nil || len(jobs) != 0 {
		t.Fatalf("list: %v %v", jobs, jerr)
	}
	if tg, terr := ReadDescriptionJobTargets(db, "job-1"); terr != nil || len(tg) != 0 {
		t.Fatalf("targets: %v %v", tg, terr)
	}
	if owned, herr := HeartbeatDescriptionJob(db, "job-1", time.Now()); herr != nil || owned != 0 {
		t.Fatalf("heartbeat: %d %v", owned, herr)
	}
	if err := ClearDescriptionJobs(db); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := ReleaseDescriptionJob(db, "job-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestSettleAndReleaseRemoveClaims(t *testing.T) {
	db := jobsProject(t, "pkg.A", "pkg.B")
	now := time.Now()
	claim(t, db, "job-1", now, 0, "pkg.A", "pkg.B")

	if err := SettleDescriptionJobTargets(db, "job-1", []string{"pkg.A"}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := ListDescriptionJobs(db)
	if len(jobs) != 1 || jobs[0].ResourceID != "pkg.B" {
		t.Fatalf("settling one id must leave the other, got %+v", jobs)
	}

	if err := ReleaseDescriptionJob(db, "job-1"); err != nil {
		t.Fatal(err)
	}
	if jobs, _ = ListDescriptionJobs(db); len(jobs) != 0 {
		t.Fatalf("release must drop everything the job held, got %+v", jobs)
	}
}

// A hard rebuild clears the ledger, but it must not delete work in flight -- that would licence
// a second worker on a resource already being described.
func TestClearStaleSparesLiveClaims(t *testing.T) {
	db := jobsProject(t, "pkg.A", "pkg.B")
	now := time.Now()

	claim(t, db, "job-live", now, 0, "pkg.A")
	if _, err := HeartbeatDescriptionJob(db, "job-live", now); err != nil {
		t.Fatal(err)
	}
	claim(t, db, "job-dead", now.Add(-time.Hour), 0, "pkg.B")

	if err := ClearStaleDescriptionJobs(db, now); err != nil {
		t.Fatal(err)
	}
	jobs, _ := ListDescriptionJobs(db)
	if len(jobs) != 1 || jobs[0].ResourceID != "pkg.A" {
		t.Fatalf("only the live claim may survive, got %+v", jobs)
	}
}

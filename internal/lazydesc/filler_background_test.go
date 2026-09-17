package lazydesc

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// recordingSpawn stands in for launching a worker. Nothing is actually started: the test plays
// the worker's part itself, which is the only way to drive the timing that matters -- a job that
// is still running when a read gives up on it.
type recordingSpawn struct {
	mu   sync.Mutex
	jobs []string
	err  error
}

func (r *recordingSpawn) spawn(dbPath, jobID, harness string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.jobs = append(r.jobs, jobID)
	return nil
}

func (r *recordingSpawn) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

func (r *recordingSpawn) lastJob(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.jobs) == 0 {
		t.Fatal("no worker was spawned")
	}
	return r.jobs[len(r.jobs)-1]
}

func withSpawn(t *testing.T, s *recordingSpawn) {
	t.Helper()
	restore := spawnWorker
	spawnWorker = s.spawn
	t.Cleanup(func() { spawnWorker = restore })
}

// backgroundFiller builds a Filler on the detached path with a short wait.
func backgroundFiller(t *testing.T, mgr *topology.TopologyManager, waitSeconds int) *Filler {
	t.Helper()
	return NewWithGenerator(mgr, lazyBackgroundConfig(waitSeconds), "", &fakeGenerator{})
}

// claimedIDs is what a worker would find in the table for a job.
func claimedIDs(t *testing.T, mgr *topology.TopologyManager, jobID string) []string {
	t.Helper()
	targets, err := helper.ReadDescriptionJobTargets(mgr.DbPath(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(targets))
	for _, tg := range targets {
		out = append(out, tg.ID)
	}
	return out
}

func TestBackgroundFillClaimsAndSpawnsOnce(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)
	f := backgroundFiller(t, mgr, 1)

	topo, _ := mgr.ReadAll()
	f.FillForRead(topo, []string{"seed"})

	if spawn.count() != 1 {
		t.Fatalf("one fill must start exactly one worker, got %d", spawn.count())
	}
	if ids := claimedIDs(t, mgr, spawn.lastJob(t)); len(ids) == 0 {
		t.Fatal("the worker was spawned with no claimed resources to work on")
	}
}

// THE PARTITION, which is the behaviour this whole design turns on.
//
// A second read that overlaps work already in flight must do BOTH things at once: watch the
// resource somebody else owns, and start a worker for everything else it planned. Starting
// everything would pay twice for the overlap; standing down would abandon the rest.
func TestBackgroundFillWatchesInFlightAndStartsTheRest(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)

	// A live worker already owns "callee".
	now := time.Now()
	topo, _ := mgr.ReadAll()
	held := helper.DescriptionJobTarget{
		ID: "callee", Fingerprint: helper.DescriptionAttemptFingerprint(topo.Resources["callee"]),
	}
	won, _, err := helper.ClaimDescriptionTargets(mgr.DbPath(), "other-job", 999,
		[]helper.DescriptionJobTarget{held}, now, now.Add(time.Hour), 0)
	if err != nil || len(won) != 1 {
		t.Fatalf("setup claim: %v %v", won, err)
	}
	if _, err := helper.HeartbeatDescriptionJob(mgr.DbPath(), "other-job", now); err != nil {
		t.Fatal(err)
	}

	f := backgroundFiller(t, mgr, 1)
	f.FillForRead(topo, []string{"seed"})

	if spawn.count() != 1 {
		t.Fatalf("the rest of the plan must still be started: %d workers spawned", spawn.count())
	}
	mine := claimedIDs(t, mgr, spawn.lastJob(t))
	for _, id := range mine {
		if id == "callee" {
			t.Fatal("the in-flight resource was claimed a second time: two workers would describe it")
		}
	}
	if len(mine) == 0 {
		t.Fatal("nothing else was claimed, so the overlap cost the read its whole plan")
	}
	// "Thing" is a neighbour of seed that nobody held, so it must be in this worker's job.
	found := false
	for _, id := range mine {
		if id == "Thing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unclaimed neighbour was not started, got %v", mine)
	}
}

// When everything a read planned is already in flight there is nothing to start -- but there is
// still something to wait for, and that is the case where one read renders a description another
// read paid for.
func TestBackgroundFillWatchesWithoutSpawningWhenAllHeld(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)

	topo, _ := mgr.ReadAll()
	now := time.Now()
	var held []helper.DescriptionJobTarget
	for _, id := range []string{"callee", "Thing"} {
		held = append(held, helper.DescriptionJobTarget{
			ID: id, Fingerprint: helper.DescriptionAttemptFingerprint(topo.Resources[id]),
		})
	}
	if _, _, err := helper.ClaimDescriptionTargets(mgr.DbPath(), "other-job", 999, held,
		now, now.Add(time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := helper.HeartbeatDescriptionJob(mgr.DbPath(), "other-job", now); err != nil {
		t.Fatal(err)
	}

	// The other worker lands one description while this read waits.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = mgr.UpdateDescription("callee", domain.ResourceFunction, "described by the other worker")
		_ = helper.SettleDescriptionJobTargets(mgr.DbPath(), "other-job", []string{"callee"})
	}()

	f := backgroundFiller(t, mgr, 3)
	changed := f.FillForRead(topo, []string{"seed"})

	if spawn.count() != 0 {
		t.Fatalf("nothing was free to claim, so no worker should have started: %d", spawn.count())
	}
	if !changed {
		t.Fatal("a description landed while the read waited, so it must report a change and re-render")
	}
}

// A read must never wait out its deadline for a worker that has died. The claim goes stale, and
// the watch has to notice.
func TestBackgroundFillStopsWaitingOnADeadWorker(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)

	topo, _ := mgr.ReadAll()
	// A claim that was never heartbeated and is already past its lease: nobody is generating.
	stale := time.Now().Add(-time.Hour)
	var held []helper.DescriptionJobTarget
	for _, id := range []string{"callee", "Thing"} {
		held = append(held, helper.DescriptionJobTarget{
			ID: id, Fingerprint: helper.DescriptionAttemptFingerprint(topo.Resources[id]),
		})
	}
	if _, _, err := helper.ClaimDescriptionTargets(mgr.DbPath(), "dead-job", 999, held,
		stale, stale.Add(time.Minute), 0); err != nil {
		t.Fatal(err)
	}

	f := backgroundFiller(t, mgr, 30) // a long wait it must not actually spend
	start := time.Now()
	f.FillForRead(topo, []string{"seed"})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the read waited %s on a dead worker", elapsed)
	}
}

// A spawn that fails must leave nothing claimed, or every later read watches a job that will
// never report.
func TestBackgroundFillReleasesClaimsWhenSpawnFails(t *testing.T) {
	mgr := project(t, fixture())
	withSpawn(t, &recordingSpawn{err: errors.New("no such binary")})
	f := backgroundFiller(t, mgr, 1)

	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a failed spawn reported a change")
	}
	jobs, err := helper.ListDescriptionJobs(mgr.DbPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("a failed spawn left %d claim(s) behind: %+v", len(jobs), jobs)
	}
}

// The read path must start no goroutines at all. The inline fill it replaces started parallel+2
// per read and abandoned them on its deadline; trading that for a wait that can outlive its
// caller would be a worse bargain than the one being undone.
func TestBackgroundFillStartsNoGoroutinesOnTheReadPath(t *testing.T) {
	mgr := project(t, fixture())
	withSpawn(t, &recordingSpawn{})
	f := backgroundFiller(t, mgr, 1)
	topo, _ := mgr.ReadAll()

	before := runtime.NumGoroutine()
	f.FillForRead(topo, []string{"seed"})

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("the read path leaked goroutines: %d before, %d after", before, got)
	}
}

// A worker, and anything a worker started, must never fill. This is what breaks the chain
// worker -> `claude -p` -> that project's hooks -> `arac cmd` -> a fill -> another worker.
func TestWorkerProcessesNeverFill(t *testing.T) {
	t.Setenv(descriptionWorkerEnv, "1")
	mgr := project(t, fixture())
	if f := New(mgr, lazyBackgroundConfig(1), ""); f != nil {
		t.Fatal("a description worker built a Filler: the spawn chain is unbounded")
	}
}

// timeout_seconds <= 0 means DO NOT WAIT, not "wait forever". Under a background worker the old
// reading would be a `cat` that hangs until generation finishes.
func TestZeroTimeoutDoesNotWait(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)
	f := backgroundFiller(t, mgr, 0)

	topo, _ := mgr.ReadAll()
	start := time.Now()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a fill that waited for nothing reported a change")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a zero timeout still waited %s", elapsed)
	}
	// The work still starts; it just lands for a later read.
	if spawn.count() != 1 {
		t.Fatalf("not waiting must not mean not working: %d workers spawned", spawn.count())
	}
}

// At the cap a fill claims nothing -- there is no capacity to work it -- but it must still watch
// whatever it planned that is already in flight.
func TestBackgroundFillRespectsTheWorkerCap(t *testing.T) {
	mgr := project(t, fixture())
	spawn := &recordingSpawn{}
	withSpawn(t, spawn)

	topo, _ := mgr.ReadAll()
	now := time.Now()
	// Fill the cap with live workers holding resources this read does not want. Each needs its
	// OWN resource: the cap counts distinct workers, and a second job claiming a resource the
	// first already holds simply loses the CAS and never becomes a worker at all.
	spare := []string{"caller", "described", "file", "member"}
	if len(spare) < helper.DefaultLazyMaxWorkers {
		t.Fatalf("the fixture has %d spare resources, need %d to fill the cap",
			len(spare), helper.DefaultLazyMaxWorkers)
	}
	for i := 0; i < helper.DefaultLazyMaxWorkers; i++ {
		job := fmt.Sprintf("filler-job-%d", i)
		won, _, err := helper.ClaimDescriptionTargets(mgr.DbPath(), job, 999,
			[]helper.DescriptionJobTarget{{ID: spare[i], Fingerprint: "fp"}}, now, now.Add(time.Hour), 0)
		if err != nil || len(won) != 1 {
			t.Fatalf("setup: job %s claimed %v (%v)", job, won, err)
		}
		if _, err := helper.HeartbeatDescriptionJob(mgr.DbPath(), job, now); err != nil {
			t.Fatal(err)
		}
	}

	cfg := lazyBackgroundConfig(1)
	f := NewWithGenerator(mgr, cfg, "", &fakeGenerator{})
	f.FillForRead(topo, []string{"seed"})

	if spawn.count() != 0 {
		t.Fatalf("at the cap no worker may start, got %d", spawn.count())
	}
}

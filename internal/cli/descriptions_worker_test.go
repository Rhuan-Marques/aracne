package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// workerProject writes a database with undescribed resources, and a config naming a provider
// that withFakeRunner stands in for.
func workerProject(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	topo := &domain.Topology{Root: dir, Language: "go", Resources: map[string]domain.Resource{}}
	for i, id := range ids {
		topo.Resources[id] = domain.Resource{
			ID: id, Name: id, Kind: domain.ResourceFunction, Language: "go",
			Location: domain.Location{Path: "a.go", StartsAt: i*10 + 1, EndsAt: i*10 + 5},
		}
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatal(err)
	}
	cfg := helper.DefaultConfig()
	cfg.Descriptions.Provider = helper.ProviderNameCLI
	cfg.Descriptions.CLIProviderCommand = "true"
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper.ConfigPath(dbPath), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// claimFor takes the claims a spawning read would have taken, so the worker has a job to find.
func claimFor(t *testing.T, dbPath, jobID string, ids ...string) {
	t.Helper()
	resources, err := helper.ReadResourcesByIDs(dbPath, ids)
	if err != nil {
		t.Fatal(err)
	}
	tg := make([]helper.DescriptionJobTarget, 0, len(ids))
	for _, id := range ids {
		tg = append(tg, helper.DescriptionJobTarget{
			ID: id, Fingerprint: helper.DescriptionAttemptFingerprint(resources[id]),
		})
	}
	now := time.Now()
	won, _, err := helper.ClaimDescriptionTargets(dbPath, jobID, 4242, tg, now, now.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(won) != len(ids) {
		t.Fatalf("setup: wanted to claim %d ids, got %d", len(ids), len(won))
	}
}

// fakeRunner stands in for a description runner: it writes through the same
// TopologyManager.UpdateDescription the real ones use, so the worker's settle logic sees exactly
// what it would see in production.
type fakeRunner struct {
	manager *topology.TopologyManager
	answer  func(descriptionResource) string
	block   chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, batch []descriptionResource, _ *domain.Topology, _ int) (string, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	wrote := 0
	for _, res := range batch {
		desc := f.answer(res)
		if desc == "" {
			continue
		}
		if err := f.manager.UpdateDescription(res.ID, res.Kind, desc); err != nil {
			continue
		}
		wrote++
	}
	if wrote == 0 {
		// The same verdict the real CLI runner reports when the model declines everything.
		return "", errNoUsableDescriptions
	}
	return "ok", nil
}

// withFakeRunner installs a runner for the worker to use, the seam newDescriptionRunnerFn exists
// for. A CLI-provider config would otherwise really exec a command.
func withFakeRunner(t *testing.T, answer func(descriptionResource) string, block chan struct{}) {
	t.Helper()
	restore := newDescriptionRunnerFn
	newDescriptionRunnerFn = func(manager *topology.TopologyManager, _ *scanner.Registry,
		_ *helper.Config, _ helper.ResolvedLazyDescriptions) (descriptionRunner, string, error) {
		return &fakeRunner{manager: manager, answer: answer, block: block}, "fake", nil
	}
	t.Cleanup(func() { newDescriptionRunnerFn = restore })
}

func TestWorkerDescribesItsClaimAndSettlesIt(t *testing.T) {
	db := workerProject(t, "pkg.A", "pkg.B")
	claimFor(t, db, "job-1", "pkg.A", "pkg.B")
	withFakeRunner(t, func(r descriptionResource) string { return "describes " + r.ID }, nil)

	if code := runDescriptionsWorker("job-1", db, helper.DefaultLazyHarness, func(int) {}); code != 0 {
		t.Fatalf("worker exited %d, want 0", code)
	}

	status, err := helper.ReadDescriptionJobStatus(db, []string{"pkg.A", "pkg.B"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pkg.A", "pkg.B"} {
		if status[id].Description == "" {
			t.Errorf("%s was not described", id)
		}
		if status[id].Claimed {
			t.Errorf("%s is still claimed: a settled resource must leave no row behind", id)
		}
	}
}

// A resource the model declines is an ANSWER about that resource, so it goes in the attempt
// ledger -- otherwise every later read re-plans it and buys a provider call per read forever.
func TestWorkerRecordsADeclinedResource(t *testing.T) {
	db := workerProject(t, "pkg.A")
	claimFor(t, db, "job-1", "pkg.A")
	withFakeRunner(t, func(descriptionResource) string { return "" }, nil)

	runDescriptionsWorker("job-1", db, helper.DefaultLazyHarness, func(int) {})

	attempts, err := helper.ReadDescriptionAttempts(db, []string{"pkg.A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := attempts["pkg.A"]; !ok {
		t.Error("a declined resource must be recorded, or it is re-planned by every later read")
	}
	jobs, _ := helper.ListDescriptionJobs(db)
	if len(jobs) != 0 {
		t.Errorf("the claim must still be settled, got %+v", jobs)
	}
}

// A duplicate spawn must be a no-op, not a second run over somebody else's work.
func TestWorkerWithNoClaimsExitsQuietly(t *testing.T) {
	db := workerProject(t, "pkg.A")
	withFakeRunner(t, func(descriptionResource) string {
		t.Error("a worker with no claimed rows must not describe anything")
		return "x"
	}, nil)
	if code := runDescriptionsWorker("job-nobody", db, helper.DefaultLazyHarness, func(int) {}); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

// Cancelling through the table must reach the worker, and it must settle rather than orphan.
func TestWorkerStopsWhenItLosesOwnership(t *testing.T) {
	db := workerProject(t, "pkg.A")
	claimFor(t, db, "job-1", "pkg.A")

	block := make(chan struct{})
	withFakeRunner(t, func(r descriptionResource) string { return "describes " + r.ID }, block)

	done := make(chan int, 1)
	go func() {
		done <- runDescriptionsWorker("job-1", db, helper.DefaultLazyHarness, func(int) {})
	}()

	// Let the worker come up and beat once, then take its claims away.
	time.Sleep(300 * time.Millisecond)
	if _, err := helper.RequestDescriptionJobCancel(db, "job-1"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(helper.DescriptionJobHeartbeatInterval + 5*time.Second):
		close(block)
		t.Fatal("a cancelled worker must stop within a heartbeat, not run to its timeout")
	}
	close(block)
}

// The startup heartbeat is what makes a failed start distinguishable from a mid-run death. A
// worker that ran at all must leave started_at set.
func TestWorkerMarksItsClaimAsStarted(t *testing.T) {
	db := workerProject(t, "pkg.A")
	claimFor(t, db, "job-1", "pkg.A")

	jobs, _ := helper.ListDescriptionJobs(db)
	if len(jobs) != 1 || !jobs[0].StartedAt.IsZero() {
		t.Fatalf("a fresh claim must be unstarted, got %+v", jobs)
	}

	block := make(chan struct{})
	withFakeRunner(t, func(descriptionResource) string { return "x" }, block)
	go runDescriptionsWorker("job-1", db, helper.DefaultLazyHarness, func(int) {})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ = helper.ListDescriptionJobs(db)
		if len(jobs) == 1 && !jobs[0].StartedAt.IsZero() {
			close(block)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(block)
	t.Fatal("the worker never stamped started_at, so a crash would look like a failed start")
}

// The log is the only channel a detached worker has, so it must not be allowed to grow forever.
func TestWorkerLogIsTrimmed(t *testing.T) {
	db := workerProject(t, "pkg.A")
	path := filepath.Join(filepath.Dir(db), descriptionWorkerLog)
	if err := os.WriteFile(path, []byte(strings.Repeat("x\n", descriptionWorkerLogMax)), 0o644); err != nil {
		t.Fatal(err)
	}
	appendDescriptionWorkerLog(db, "job-1: something went wrong")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > descriptionWorkerLogMax {
		t.Errorf("the log must be trimmed past %d bytes, got %d", descriptionWorkerLogMax, info.Size())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "something went wrong") {
		t.Error("trimming must keep the tail: the most recent failure is the one being explained")
	}
}

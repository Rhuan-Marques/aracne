package tests_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// The detached worker, with a real binary, a real fork and a real database.
//
// Everything else about this feature is tested in-process with a fake spawn, which is the right
// way to test the LOGIC and no way at all to test the claim that matters: that a process outlives
// the read that started it, and that nothing is left running afterwards. Those are properties of
// fork, setsid and file descriptors, and only an actual process has them.
//
// Every test here ends in a sweep that fails if any worker for its own database survived, because
// "does not leave processes running" is the promise this feature has to keep.

// backgroundProject scans a project and configures it for a detached fill through a scripted
// provider, so the worker really runs and really writes, with no API key anywhere.
func backgroundProject(t *testing.T, providerScript string, waitSeconds int) string {
	t.Helper()
	dir := lazyProject(t)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	script := filepath.Join(dir, "provider.sh")
	writeFile(t, script, providerScript)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	cfg.Descriptions.Provider = helper.ProviderNameCLI
	cfg.Descriptions.CLIProviderCommand = script
	cfg.Descriptions.StyleExemplars = 0
	on, background := true, true
	cfg.Descriptions.Lazy.Enabled = &on
	cfg.Descriptions.Lazy.Background = &background
	cfg.Descriptions.Lazy.TimeoutSeconds = &waitSeconds
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, helper.ConfigPath(dbPath), string(raw))

	t.Cleanup(func() { assertNoWorkersLeft(t, dbPath) })
	return dir
}

// describingProvider answers in the format ParseLazyDescriptions expects, after a delay that
// lets a test watch a job that is still running.
func describingProvider(delaySeconds int) string {
	// The prompt arrives on stdin, one `### <id>` heading per resource, and the reply format is
	// `id :: description` -- prompts.LazyDescriptionSeparator, the same one the real providers
	// answer in.
	return fmt.Sprintf(`#!/bin/sh
sleep %d
ids=$(grep -oE '^### .+' | sed 's/^### //')
for id in $ids; do
  printf '%%s :: generated in the background\n' "$id"
done
`, delaySeconds)
}

// assertNoWorkersLeft is the promise, checked: no `arac descriptions worker` for THIS database
// may survive the test. Scoped by the database path so it can never match a developer's own
// processes.
func assertNoWorkersLeft(t *testing.T, dbPath string) {
	t.Helper()
	// Give a worker that is shutting down a moment to actually go.
	deadline := time.Now().Add(20 * time.Second)
	for {
		alive := workersFor(dbPath)
		if len(alive) == 0 {
			return
		}
		if time.Now().After(deadline) {
			for _, pid := range alive {
				_ = exec.Command("kill", "-9", pid).Run()
			}
			t.Fatalf("description worker(s) %v survived the test: this feature must not leave "+
				"processes running", alive)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// workersFor returns the pids of description workers running against one database.
func workersFor(dbPath string) []string {
	out, err := exec.Command("pgrep", "-f", "descriptions worker.*"+dbPath).Output()
	if err != nil {
		return nil // pgrep exits 1 when nothing matches
	}
	var pids []string
	for _, line := range strings.Fields(string(out)) {
		if line != "" {
			pids = append(pids, line)
		}
	}
	return pids
}

func jobRows(t *testing.T, dbPath string) []helper.DescriptionJob {
	t.Helper()
	jobs, err := helper.ListDescriptionJobs(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func descriptionCount(t *testing.T, dbPath string, ids ...string) int {
	t.Helper()
	status, err := helper.ReadDescriptionJobStatus(dbPath, ids)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, st := range status {
		if strings.TrimSpace(st.Description) != "" {
			n++
		}
	}
	return n
}

// projectIDs are the ids lazyProject's module produces. They carry the module path, which is
// what `arac read pkg.Describe` resolves its argument to.
func projectIDs() []string {
	return []string{
		"demo/pkg.Circle", "demo/pkg.Shape", "demo/pkg.Total",
		"demo/pkg.Describe", "demo/pkg.(Circle).Area",
	}
}

// THE CENTRAL CLAIM: the read returns before the work is done, and the work still finishes.
//
// Under the old inline fill this was the case that wasted everything -- the deadline killed the
// provider mid-answer and the next read started from scratch. Here the read gives up waiting,
// renders what it has, exits, and the description still lands.
func TestReadReturnsEarlyAndTheWorkLandsAfterwards(t *testing.T) {
	dir := backgroundProject(t, describingProvider(3), 1) // provider slower than the wait
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	start := time.Now()
	mustRun(t, dir, "read", "pkg.Describe")
	elapsed := time.Since(start)

	if elapsed > 15*time.Second {
		t.Fatalf("the read waited %s: it should give up at its timeout, not wait for the worker", elapsed)
	}
	if len(workersFor(dbPath)) == 0 && descriptionCount(t, dbPath, projectIDs()...) == 0 {
		t.Fatal("no worker survived the read and nothing was described: the work was abandoned")
	}

	// The parent is gone; the worker is not. Wait for it to finish on its own.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if descriptionCount(t, dbPath, projectIDs()...) > 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("nothing was ever described; jobs: %+v", jobRows(t, dbPath))
}

// THE REGRESSION THAT WOULD BREAK EVERY HOOK.
//
// An intercepted `cat` is a Bash tool call whose stdout the harness reads to EOF. If the worker
// inherits that pipe, the harness waits for the WORKER, not for the read -- and the feature whose
// whole purpose is to stop a read hanging becomes the reason it hangs.
func TestSpawningAWorkerDoesNotHoldTheParentsStdout(t *testing.T) {
	dir := backgroundProject(t, describingProvider(5), 1)

	cmd := exec.Command(AracBin, "read", "pkg.Describe")
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Reading to EOF is exactly what a harness does. It must return while the worker is still
	// alive, which is what proves the worker is not holding the write end.
	drained := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stdout)
		drained <- err
	}()

	select {
	case <-drained:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the parent's stdout never reached EOF: the worker inherited it and would hang a hook")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("read failed: %v", err)
	}
}

// Two reads over overlapping neighbourhoods must not describe the same resource twice.
func TestTwoReadsDoNotStartTwoWorkersForOneResource(t *testing.T) {
	dir := backgroundProject(t, describingProvider(4), 1)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	first := exec.Command(AracBin, "read", "pkg.Describe")
	first.Dir = dir
	second := exec.Command(AracBin, "read", "pkg.Describe")
	second.Dir = dir
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	// Let the first read claim before the second one plans the same resources.
	time.Sleep(1500 * time.Millisecond)
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	_ = second.Wait()

	// One claim per resource is the invariant; the primary key enforces it, and this is the
	// end-to-end proof that two real processes cannot both take one.
	seen := map[string]string{}
	for _, j := range jobRows(t, dbPath) {
		if prev, ok := seen[j.ResourceID]; ok && prev != j.JobID {
			t.Fatalf("%s is claimed by two jobs (%s, %s)", j.ResourceID, prev, j.JobID)
		}
		seen[j.ResourceID] = j.JobID
	}
	if len(workersFor(dbPath)) > 1 {
		// More than one worker is fine in principle -- the second read may legitimately have
		// started one for resources the first did not take -- but they must not share a resource,
		// which the loop above already established.
		t.Logf("two workers are running over disjoint resources, which is the partition working")
	}
}

// A worker must be stoppable, by a person, without a pid.
func TestDescriptionJobsCanStopAWorker(t *testing.T) {
	dir := backgroundProject(t, describingProvider(30), 1) // long enough to catch in the act
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	mustRun(t, dir, "read", "pkg.Describe")

	// Wait for the worker to be visible in the table.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && len(jobRows(t, dbPath)) == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	if len(jobRows(t, dbPath)) == 0 {
		t.Skip("the worker settled before it could be observed; nothing to stop")
	}

	out := mustRun(t, dir, "descriptions", "jobs")
	if !strings.Contains(out, "RESOURCE") {
		t.Fatalf("`descriptions jobs` printed nothing useful:\n%s", out)
	}
	mustRun(t, dir, "descriptions", "jobs", "--stop", "all")

	// It must go within a heartbeat or so, not run out its timeout.
	deadline = time.Now().Add(helper.DescriptionJobHeartbeatInterval + 30*time.Second)
	for time.Now().Before(deadline) {
		if len(workersFor(dbPath)) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("a stopped worker was still running well past a heartbeat")
}

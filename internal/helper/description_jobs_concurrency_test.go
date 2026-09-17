package helper

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The claim has to be exclusive across PROCESSES, not goroutines.
//
// Every intercepted `cat` and `grep` is a fresh `arac`, so the in-process test above proves the
// wrong thing on its own: it shares one sqliteLock mutex, which no second process can see. Here
// six real processes race for the same ten resources through nothing but SQLite, released at a
// common instant. Exactly one may win each resource; every loser must be told to watch it.
//
// Modelled on TestSTO02_ConcurrentUpdateFileKeepsEveryWarning, which is the repo's template for
// this shape.
const (
	claimChildEnv   = "ARAC_TEST_CLAIM_CHILD"
	claimChildDB    = "ARAC_TEST_CLAIM_DB"
	claimChildJob   = "ARAC_TEST_CLAIM_JOB"
	claimChildStart = "ARAC_TEST_CLAIM_START"
	claimChildOut   = "ARAC_TEST_CLAIM_OUT"
)

// TestClaimChildProcess is the child half: skipped in a normal run, re-executed by the parent.
func TestClaimChildProcess(t *testing.T) {
	if os.Getenv(claimChildEnv) == "" {
		t.Skip("child half of the cross-process claim test")
	}
	if ns, err := strconv.ParseInt(os.Getenv(claimChildStart), 10, 64); err == nil {
		if wait := time.Until(time.Unix(0, ns)); wait > 0 {
			time.Sleep(wait)
		}
	}
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		ids = append(ids, fmt.Sprintf("pkg.R%d", i))
	}
	now := time.Now()
	won, watch, err := ClaimDescriptionTargets(os.Getenv(claimChildDB), os.Getenv(claimChildJob),
		os.Getpid(), targets(ids...), now, now.Add(10*time.Minute), 0)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Report what this process won and watched, so the parent can add them up.
	line := strings.Join(won, ",") + "|" + strings.Join(watch, ",") + "\n"
	if err := os.WriteFile(os.Getenv(claimChildOut), []byte(line), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
}

func TestClaimIsExclusiveAcrossProcesses(t *testing.T) {
	const children = 6
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		ids = append(ids, fmt.Sprintf("pkg.R%d", i))
	}
	db := jobsProject(t, ids...)
	outDir := t.TempDir()

	// Every child claims, then immediately heartbeats nothing: the claims stay live for their
	// lease, so a loser must genuinely lose rather than reclaim a stale row.
	start := time.Now().Add(2 * time.Second).UnixNano()
	procs := make([]*exec.Cmd, 0, children)
	outs := make([]string, 0, children)
	for i := 0; i < children; i++ {
		out := filepath.Join(outDir, fmt.Sprintf("child%d", i))
		outs = append(outs, out)
		cmd := exec.Command(os.Args[0], "-test.run=TestClaimChildProcess")
		cmd.Env = append(os.Environ(),
			claimChildEnv+"=1",
			claimChildDB+"="+db,
			claimChildJob+"="+fmt.Sprintf("job-%d", i),
			claimChildStart+"="+strconv.FormatInt(start, 10),
			claimChildOut+"="+out,
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
		procs = append(procs, cmd)
	}
	for i, cmd := range procs {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child %d failed: %v", i, err)
		}
	}

	winners := map[string]int{}
	for _, out := range outs {
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read child result: %v", err)
		}
		parts := strings.SplitN(strings.TrimSpace(string(raw)), "|", 2)
		for _, id := range strings.Split(parts[0], ",") {
			if id != "" {
				winners[id]++
			}
		}
	}

	var doubled []string
	for id, n := range winners {
		if n != 1 {
			doubled = append(doubled, fmt.Sprintf("%s won by %d processes", id, n))
		}
	}
	sort.Strings(doubled)
	if len(doubled) > 0 {
		t.Fatalf("a resource was claimed more than once, so two workers would describe it:\n%s",
			strings.Join(doubled, "\n"))
	}
	if len(winners) != len(ids) {
		t.Fatalf("every resource should have found exactly one owner, got %d of %d",
			len(winners), len(ids))
	}

	// And the table agrees: one row per resource, no duplicates.
	jobs, err := ListDescriptionJobs(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != len(ids) {
		t.Fatalf("want %d claim rows, got %d", len(ids), len(jobs))
	}
}

package cli

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Hooks for parallel tool calls are separate processes, and each PostToolUse reads the ledger,
// diffs it against the table and writes it back. Unlocked, two of them could both read the
// ledger before either wrote it, and both report the same warning. The ledger is a file, so
// goroutines racing on it here race exactly the way two hook processes do.
func TestConcurrentHooksReportAWarningOnce(t *testing.T) {
	for round := 0; round < 5; round++ {
		db := warnDB(t, "pre-existing")
		seedReportedWarnings(db)
		addWarning(t, db, "new")

		const hooks = 8
		var start sync.WaitGroup
		var done sync.WaitGroup
		start.Add(1)
		var reported int64
		for i := 0; i < hooks; i++ {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				for _, w := range unreportedWarnings(db) {
					if w.ID == "new" {
						atomic.AddInt64(&reported, 1)
					}
				}
			}()
		}
		start.Done()
		done.Wait()
		if reported != 1 {
			t.Fatalf("round %d: the new warning was reported %d times across %d concurrent hooks, want exactly once",
				round, reported, hooks)
		}
	}
}

// A lock left behind by a hook that died holding it must not cost every later hook the full
// wait, nor stop the ledger from being kept.
func TestStaleWarnStateLockIsTakenOver(t *testing.T) {
	db := warnDB(t, "pre-existing")
	seedReportedWarnings(db)
	addWarning(t, db, "new")

	lock := warnStateFile(db) + ".lock"
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	began := time.Now()
	got := unreportedWarnings(db)
	if waited := time.Since(began); waited >= warnStateLockWait {
		t.Errorf("a stale lock cost the full %s wait (%s)", warnStateLockWait, waited)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("got %+v, want exactly the new warning", got)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the lock must be released after use, stat: %v", err)
	}
	if n := len(unreportedWarnings(db)); n != 0 {
		t.Errorf("the ledger must have recorded the report, got %d again", n)
	}
}

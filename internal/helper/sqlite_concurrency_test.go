package helper

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func TestConcurrentSQLiteAccessDoesNotLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{
		Root:     t.TempDir(),
		Language: "go",
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "Foo"},
			"f2": {ID: "f2", Kind: domain.ResourceFunction, Name: "Bar"},
		},
		Warnings: make(map[string]domain.TopologyWarning),
		Errors:   make(map[string]string),
	}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 160)
	for worker := 0; worker < 16; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := ReadDb(dbPath); err != nil {
					errs <- fmt.Errorf("ReadDb: %w", err)
				}
				if err := UpdateDescription(dbPath, domain.ResourceFunction, "f1", fmt.Sprintf("worker %d iter %d", worker, i)); err != nil {
					errs <- fmt.Errorf("UpdateDescription: %w", err)
				}
				if err := CreateBug(dbPath, domain.KnownBug{ID: fmt.Sprintf("bug_%d_%d", worker, i), NodeID: "f1", Description: "test", State: domain.BugPending}); err != nil {
					errs <- fmt.Errorf("CreateBug: %w", err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if isSQLiteLocked(err) {
			t.Fatalf("unexpected sqlite lock error: %v", err)
		}
		if err != nil {
			t.Fatalf("unexpected db error: %v", err)
		}
	}
}

// UpdateBugState, ReadBugs and GetCallers used to call sql.Open directly: no per-database
// mutex, no migration, no retry, and -- because their DSN was written in another driver's
// parameter names -- no busy timeout either. A concurrent writer made them fail outright where
// every other entrance waits and retries.
func TestBugStateWritesWaitForAConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	if err := withSQLiteWrite(dbPath, func(db *sql.DB) error { return createSchema(db) }); err != nil {
		t.Fatalf("createSchema: %v", err)
	}
	bug := domain.KnownBug{ID: "b1", NodeID: "pkg.Fn", Description: "d", State: domain.BugPending}
	if err := CreateBug(dbPath, bug); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	// Hold a write transaction open on the locked path while the bug write runs.
	held := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withSQLiteWrite(dbPath, func(db *sql.DB) error {
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO info (key, value) VALUES ('probe', 'x')"); err != nil {
				tx.Rollback()
				return err
			}
			close(held)
			time.Sleep(300 * time.Millisecond)
			return tx.Commit()
		})
	}()
	<-held

	if err := UpdateBugState(dbPath, "b1", domain.BugAcknowledged); err != nil {
		t.Fatalf("UpdateBugState under contention: %v (it must queue behind the writer, not fail)", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("holder: %v", err)
	}

	bugs, err := ReadBugs(dbPath, "", domain.BugAcknowledged)
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(bugs) != 1 || bugs[0].ID != "b1" {
		t.Fatalf("acknowledged bugs = %v, want [b1]", bugs)
	}
}

// The DSN has to be spelled the way modernc.org/sqlite reads it. `_busy_timeout=N` is
// mattn/go-sqlite3's name and this driver discards it silently, which left the connections
// that relied on it alone running with no timeout at all.
func TestOpenSQLiteAppliesBusyTimeout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	db, err := openSQLite(dbPath, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()
	var ms int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if ms != sqliteBusyTimeoutMillis {
		t.Fatalf("busy_timeout = %d, want %d (the DSN parameter is being ignored)", ms, sqliteBusyTimeoutMillis)
	}
}

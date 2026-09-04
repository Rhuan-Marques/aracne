package helper

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

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

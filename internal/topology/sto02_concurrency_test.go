package topology_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// STO-02. Every topology writer reads the whole warnings table, adds its own rows and rewrites
// it wholesale (helper.WriteIncremental), with nothing serializing read -> compute -> write
// ACROSS PROCESSES. Concurrent writers are the normal case here: the model's parallel tool
// calls are parallel PostToolUse hooks, each one an `arac update-file` process. Six of them
// each reported their node_removed warning and only three survived in the database -- and
// nothing re-raises a lost one, because the removal that caused it is already in the graph.
//
// The test drives the real UpdateFile path from concurrent PROCESSES, the way
// internal/llm/tools/edit_concurrency_test.go drives Edit.

const (
	updateChildEnv   = "ARAC_TEST_UPDATE_CHILD"
	updateChildDB    = "ARAC_TEST_UPDATE_DB"
	updateChildFile  = "ARAC_TEST_UPDATE_FILE"
	updateChildStart = "ARAC_TEST_UPDATE_START"
)

// TestUpdateFileChildProcess is the child half of TestSTO02_*: skipped in a normal run, and
// re-executed by the parent with the environment set.
func TestUpdateFileChildProcess(t *testing.T) {
	if os.Getenv(updateChildEnv) == "" {
		t.Skip("child half of the concurrent update-file test")
	}
	// All children enter their read-modify-write window at the same instant, the way parallel
	// tool calls do.
	if ns, err := strconv.ParseInt(os.Getenv(updateChildStart), 10, 64); err == nil {
		if wait := time.Until(time.Unix(0, ns)); wait > 0 {
			time.Sleep(wait)
		}
	}
	mgr := topology.New()
	mgr.Load(os.Getenv(updateChildDB))
	if _, err := mgr.UpdateFile(os.Getenv(updateChildFile), goOnlyRegistry()); err != nil {
		t.Fatalf("update-file: %v", err)
	}
}

func TestSTO02_ConcurrentUpdateFileKeepsEveryWarning(t *testing.T) {
	const pairs = 6
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module conc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	callees := make([]string, pairs)
	for i := 0; i < pairs; i++ {
		callees[i] = filepath.Join(pkgDir, fmt.Sprintf("callee%d.go", i))
		src := fmt.Sprintf("package pkg\n\nfunc Callee%d() int { return %d }\n", i, i)
		if err := os.WriteFile(callees[i], []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		caller := fmt.Sprintf("package pkg\n\nfunc Caller%d() int { return Callee%d() }\n", i, i)
		if err := os.WriteFile(filepath.Join(pkgDir, fmt.Sprintf("caller%d.go", i)), []byte(caller), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	db := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	mgr.Load(db)
	if err := mgr.FullScan(dir, goOnlyRegistry()); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Rename every callee: each caller now names a symbol that is gone, so each update-file
	// raises its own node_removed warning.
	for i := 0; i < pairs; i++ {
		src := fmt.Sprintf("package pkg\n\nfunc Renamed%d() int { return %d }\n", i, i)
		if err := os.WriteFile(callees[i], []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now().Add(2 * time.Second).UnixNano()
	procs := make([]*exec.Cmd, 0, pairs)
	for i := 0; i < pairs; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestUpdateFileChildProcess")
		cmd.Env = append(os.Environ(),
			updateChildEnv+"=1",
			updateChildDB+"="+db,
			updateChildFile+"="+callees[i],
			updateChildStart+"="+strconv.FormatInt(start, 10),
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

	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	var missing []string
	for i := 0; i < pairs; i++ {
		name := fmt.Sprintf("Callee%d", i)
		found := false
		for _, w := range topo.Warnings {
			if strings.Contains(w.Message, name) || strings.Contains(w.SourceID, name) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d of %d node_removed warnings were dropped by concurrent writers: %v\n(the database holds %d warnings)",
			len(missing), pairs, missing, len(topo.Warnings))
	}

	// The file manifest is the same read-modify-write, one file further out: each update reads
	// the whole manifest, adds its own stamp and writes it back, so a lost write leaves that
	// file reported stale by `arac check-updates` for good.
	_, modified, _, diffErr := helper.DiffScanFiles(helper.CanonicalPath(dir), "go", helper.ManifestPath(db))
	if diffErr != nil {
		t.Fatalf("diff after concurrent updates: %v", diffErr)
	}
	if len(modified) > 0 {
		t.Fatalf("concurrent update-file lost %d manifest stamps, so those files stay stale: %v", len(modified), modified)
	}
}

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// The drift scan must be rooted at the PROJECT, not at the process's working directory.
//
// It used to pass a literal "." while RunPreToolScan, doing the same job on the way in, reads
// the stored topology root. The guard is a hook process whose cwd is not dependable -- which is
// the entire reason guardDBPath resolves the database from the hook event's cwd, from
// $CLAUDE_PROJECT_DIR, or by walking upward. So "." was routinely a subdirectory: the ignore
// matcher was built against the wrong base, DetectAll saw only the languages below it, and a
// subdirectory with no source at all made IncrementalScan return "no language scanner
// detected", which runGuardScan swallows -- the backstop silently doing nothing from the moment
// the model ran `cd`.
func TestDriftCheckScansTheProjectRootFromASubdirectory(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"),
		[]byte("module driftcheck.test\n\ngo 1.25.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(project, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc Alpha() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// A subdirectory with no source in it, which is where the agent's shell has cd'd to.
	sub := filepath.Join(project, "docs")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(project, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := mgr.FullScan(project, NewScannerRegistry()); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	// The change the drift check exists to notice, made by something outside the session.
	if err := os.WriteFile(src, []byte("package main\n\nfunc Alpha() {}\n\nfunc Beta() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}

	driftCheck(dbPath, "Bash", 0)

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	found := false
	for _, res := range topo.Resources {
		if res.Name == "Beta" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("driftCheck run from a subdirectory did not re-index the project root; " +
			"Beta is missing from the topology")
	}
}

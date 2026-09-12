package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// STO-04. `arac scan -root ./sub` with the default -output put the database under the CWD
// while the stored root was the subdirectory. On the second run Relocation infers the project
// root from where the database sits, reads the mismatch as "the project moved", and runs
// FullReScan over the CWD -- silently widening the graph to everything beside the project,
// with one line about a move that never happened.
func TestSTO04_ExplicitRootKeepsItsOwnDatabase(t *testing.T) {
	dir := t.TempDir()
	backend := filepath.Join(dir, "backend")
	if err := os.MkdirAll(backend, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "go.mod"), []byte("module backend\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "main.go"), []byte("package main\n\nfunc Inside() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling the project does not contain. Nothing about `-root backend` may reach it.
	scratch := filepath.Join(dir, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "t.go"), []byte("package scratch\n\nfunc Unrelated() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	RunScan([]string{"-root", "backend"})
	// The second run is where the re-rooting happened: the first one is what records the
	// mismatch Relocation then reads as a move.
	RunScan([]string{"-root", "backend"})

	db := filepath.Join(backend, DefaultDBRelative)
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("an explicit -root with the default -output must keep its database under that root: %v", err)
	}
	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if got, want := filepath.Clean(topo.Root), helper.CanonicalPath(backend); got != want {
		t.Errorf("stored root widened to %q, want %q", got, want)
	}
	for _, res := range topo.Resources {
		if res.Name == "Unrelated" {
			t.Fatalf("the scan widened past -root: it indexed %s (%s)", res.ID, res.Location.Path)
		}
	}
}

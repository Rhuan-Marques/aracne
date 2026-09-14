package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestInitRegistryRebuildsAMovedProject pins SC-2 for the read verbs.
//
// `arac read`, `arac serve`, `arac grep` and `arac check-updates` all open the topology through
// InitRegistry. After `mv p1 p2` they answered from paths under p1 -- reads failed with "no such
// file", check-updates with "access root". Opening a moved project must rebuild it under p2 and
// keep the descriptions it had.
func TestInitRegistryRebuildsAMovedProject(t *testing.T) {
	// CANONICAL: the root this test reads back was stored through helper.CanonicalPath, as
	// every scan verb stores it, so an expectation built from an unresolved temp dir compares
	// two spellings of one directory. macOS hands every t.TempDir() out under /var, which is a
	// symlink to /private/var.
	parent := helper.CanonicalPath(t.TempDir())
	p1 := filepath.Join(parent, "p1")
	for rel, body := range map[string]string{
		"go.mod":          "module example.com/p\n\ngo 1.22\n",
		"shapes/shape.go": "package shapes\n\nfunc Undocumented() int { return 4 }\n",
	} {
		full := filepath.Join(p1, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mgr, _ := InitRegistry(filepath.Join(p1, ".aracne", "topology.db"))
	if err := mgr.UpdateDescription("example.com/p/shapes.Undocumented", domain.ResourceFunction, "MANUAL"); err != nil {
		t.Fatal(err)
	}

	p2 := filepath.Join(parent, "p2")
	if err := os.Rename(p1, p2); err != nil {
		t.Fatal(err)
	}
	moved, reg := InitRegistry(filepath.Join(p2, ".aracne", "topology.db"))
	topo, err := moved.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if topo.Root != p2 {
		t.Fatalf("stored root = %s, want %s", topo.Root, p2)
	}
	fn := topo.Resources["example.com/p/shapes.Undocumented"]
	if fn.Description != "MANUAL" {
		t.Fatalf("description lost across the move: %q", fn.Description)
	}
	if _, err := moved.Cut(fn.Location); err != nil {
		t.Fatalf("read after the move: %v", err)
	}
	if len(topo.Warnings) != 0 {
		t.Fatalf("a move changed no code but raised warnings: %v", topo.Warnings)
	}
	if health, err := moved.IndexHealth("", reg); err != nil || health.Stale() {
		t.Fatalf("check-updates after the move: %+v, %v", health, err)
	}
}

package topology_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// scanProject writes files, scans them, and returns the manager.
func scanProject(t *testing.T, files map[string]string) (*topology.TopologyManager, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(dir, ".aracne", "topology.db")); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, dir
}

// TestNoSelfPackageEdge pins AR-06.
//
// An unqualified name resolved against the caller's OWN package still recorded a uses_package
// edge to it. gotools renders that edge, so a read of almost any function in a multi-file
// package opened with `import ("<its own package>")` -- a self-import, a compile error in Go,
// printed inside the fence a read promises is verbatim source. It fired only when the callee
// lived in a different FILE of the same package.
func TestNoSelfPackageEdge(t *testing.T) {
	mgr, _ := scanProject(t, map[string]string{
		"go.mod":  "module probe\n\ngo 1.25\n",
		"dep.go":  "package probe\n\nfunc Helper() int { return 7 }\n",
		"main.go": "package probe\n\nfunc Entry() int { return Helper() }\n",
	})
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := topo.Resources["probe.Entry"]
	if !ok {
		t.Fatalf("probe.Entry not indexed; ids: %v", idsOf(topo))
	}
	if calls := entry.Connections["calls"]; len(calls) != 1 || calls[0] != "probe.Helper" {
		t.Fatalf("expected the cross-file call to still resolve, got calls=%v", calls)
	}
	if pkgs := entry.Connections["uses_package"]; len(pkgs) != 0 {
		t.Fatalf("a package does not use itself: probe.Entry records uses_package=%v", pkgs)
	}
}

// TestCrossPackageEdgeStillRecorded is the other half of AR-06: dropping the SELF edge must
// not drop the real ones.
func TestCrossPackageEdgeStillRecorded(t *testing.T) {
	mgr, _ := scanProject(t, map[string]string{
		"go.mod":     "module probe\n\ngo 1.25\n",
		"sub/sub.go": "package sub\n\nfunc Twice(n int) int { return n * 2 }\n",
		"app/app.go": "package app\n\nimport \"probe/sub\"\n\nfunc Entry() int { return sub.Twice(1) }\n",
	})
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	entry := topo.Resources["probe/app.Entry"]
	pkgs := entry.Connections["uses_package"]
	if len(pkgs) != 1 || pkgs[0] != "probe/sub" {
		t.Fatalf("expected uses_package=[probe/sub], got %v", pkgs)
	}
}

// TestIncrementalScanDoesNotRePlanAnUnchangedTree pins AR-03 end to end: after one scan, a
// second incremental scan over an untouched tree must find nothing to do.
func TestIncrementalScanDoesNotRePlanAnUnchangedTree(t *testing.T) {
	mgr, dir := scanProject(t, map[string]string{
		"go.mod":  "module probe\n\ngo 1.25\n",
		"main.go": "package probe\n\nfunc Entry() int { return 1 }\n",
	})
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())

	added, _, _, err := helper.DiffScanFiles(dir, "go", helper.ManifestPath(mgr.DbPath()))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("a freshly scanned tree must have nothing added, got %v", added)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatal(err)
	}
}

func idsOf(topo *domain.Topology) []string {
	out := make([]string, 0, len(topo.Resources))
	for id := range topo.Resources {
		out = append(out, id)
	}
	return out
}

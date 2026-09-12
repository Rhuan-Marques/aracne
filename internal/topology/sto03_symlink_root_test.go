package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// STO-03. filepath.Abs does not resolve symlinks and filepath.WalkDir Lstats its root, so a
// root whose last component is a symlink (a dotfiles checkout, a /work bind mount, WSL) was
// visited as a LINK and never descended into. Detection still succeeded -- Stat follows links
// and finds the go.mod behind one -- so the scan reported success over an empty graph, and the
// hint it printed blamed scan.ignore. Afterwards the stored root disagreed with the real path
// every `arac update-file` passes, and ids were minted from the relative path between them
// (`_/../real/pkg.One`).
func TestSTO03_SymlinkedRootIndexesTheRealTree(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "go.mod"), []byte("module symroot\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(real, "pkg", "a.go")
	if err := os.WriteFile(srcPath, []byte("package pkg\n\nfunc One() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	db := filepath.Join(link, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	mgr.Load(db)
	if err := mgr.FullScan(link, goOnlyRegistry()); err != nil {
		t.Fatalf("scan through a symlinked root: %v", err)
	}

	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if !hasFunctionNamed(topo, "One") {
		t.Fatalf("a scan reached through a symlinked root indexed nothing: %d resources, root %q",
			len(topo.Resources), topo.Root)
	}

	// The second half of the same bug: an update-file naming the file by its REAL path has to
	// land on the nodes the scan already minted, not mint a second set relative to a root it
	// does not share.
	// Both spellings: the hook passes whatever the agent's cwd makes of the path, so the link
	// path and the real path have to name the same nodes.
	before := len(topo.Resources)
	for _, p := range []string{srcPath, filepath.Join(link, "pkg", "a.go")} {
		if _, err := mgr.UpdateFile(p, goOnlyRegistry()); err != nil {
			t.Fatalf("update-file %s: %v", p, err)
		}
	}
	topo, err = helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db after update: %v", err)
	}
	if len(topo.Resources) != before {
		t.Errorf("update-file through the real path changed the node count %d -> %d: it minted a second set of ids",
			before, len(topo.Resources))
	}
	for id := range topo.Resources {
		if strings.Contains(id, "..") {
			t.Errorf("id minted against the wrong root: %q", id)
		}
	}

	// And an incremental scan still has a manifest it agrees with.
	if _, err := mgr.IncrementalScan(link, goOnlyRegistry()); err != nil {
		t.Errorf("incremental scan after a symlinked-root scan: %v", err)
	}
}

// hasFunctionNamed reports whether the topology holds a function node of that name.
func hasFunctionNamed(topo *domain.Topology, name string) bool {
	for _, res := range topo.Resources {
		if res.Kind == domain.ResourceFunction && res.Name == name {
			return true
		}
	}
	return false
}

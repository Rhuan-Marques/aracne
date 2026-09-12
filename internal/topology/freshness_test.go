package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// TestStaleFiles pins the check every read runs before it trusts a recorded span (RD-4): a
// file is stale when it changed on disk after the manifest stamped it, or was deleted; a file
// the manifest never recorded is not judged.
func TestStaleFiles(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	for name, body := range map[string]string{
		"go.mod":  "module probe\n\ngo 1.25\n",
		"main.go": "package probe\n\nfunc Entry() int { return 1 }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
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

	if got := mgr.StaleFiles([]string{mainGo}); len(got) != 0 {
		t.Fatalf("a file just scanned is fresh, got stale %v", got)
	}
	if got := mgr.StaleFiles([]string{filepath.Join(dir, "unrecorded.go"), "github.com/x/dep"}); len(got) != 0 {
		t.Fatalf("paths the manifest never recorded must not be judged, got %v", got)
	}

	later := time.Now().Add(5 * time.Second)
	if err := os.WriteFile(mainGo, []byte("package probe\n\n// moved\nfunc Entry() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(mainGo, later, later); err != nil {
		t.Fatal(err)
	}
	if got := mgr.StaleFiles([]string{mainGo, mainGo}); len(got) != 1 || got[0] != mainGo {
		t.Fatalf("a file modified after its manifest stamp is stale (once), got %v", got)
	}

	// Re-indexing it is what makes it fresh again: the single-file update restamps it.
	if _, err := mgr.UpdateFile(mainGo, reg); err != nil {
		t.Fatal(err)
	}
	if got := mgr.StaleFiles([]string{mainGo}); len(got) != 0 {
		t.Fatalf("a re-indexed file is fresh, got stale %v", got)
	}

	if err := os.Remove(mainGo); err != nil {
		t.Fatal(err)
	}
	if got := mgr.StaleFiles([]string{mainGo}); len(got) != 1 {
		t.Fatalf("a recorded file that is gone from disk is stale, got %v", got)
	}
}

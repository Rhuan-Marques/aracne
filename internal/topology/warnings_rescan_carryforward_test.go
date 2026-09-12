package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// WRN-01. A full rescan rebuilt the warnings table from nothing, so every signature_changed,
// node_removed and use_missing_node row vanished while the call that caused it was still
// broken. That path is not rare: `arac scan --all`, `scan.pre_tool: "full"` (before EVERY tool
// call), a relocated project, and any edit to go.mod / Cargo.toml / pom.xml / build.gradle,
// which sends an ordinary incremental scan through FullReScan. docs/architecture.md says these
// warnings stand while the cause stands and surface through warnings_list.
//
// Carrying them forward is only half of it: a rescan must also DROP the ones whose cause is
// gone, or a caller fixed between scans would be reported forever.

func rescanWarnCount(t *testing.T, mgr *topology.TopologyManager, kind domain.WarningKind) int {
	t.Helper()
	w, err := mgr.ListWarnings("", "", kind)
	if err != nil {
		t.Fatal(err)
	}
	return len(w)
}

func TestWRN01_FullReScanKeepsASignatureWarningWhoseCallerIsStillBroken(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "target.go", "package testproject\n\nfunc FuncA(x int) int { return x }\n")
	p.write(t, "caller.go", "package testproject\n\nfunc Caller() int { return FuncA(1) }\n")
	mgr := p.scan(t)
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())

	p.writeForIncrementalScan(t, "target.go", "package testproject\n\nfunc FuncA(x int, y int) int { return x + y }\n")
	p.incrementalScan(t, mgr)
	if got := rescanWarnCount(t, mgr, domain.WarnSignatureChanged); got != 1 {
		t.Fatalf("incremental scan raised %d signature_changed warning(s), want 1", got)
	}

	if _, err := mgr.FullReScan(p.dir, reg); err != nil {
		t.Fatalf("FullReScan: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnSignatureChanged); got != 1 {
		t.Fatalf("after a full rescan %d signature_changed warning(s) remain, want 1: Caller still calls FuncA(1)", got)
	}

	// And the other half: once the caller is fixed, the rescan must drop it.
	p.writeForIncrementalScan(t, "caller.go", "package testproject\n\nfunc Caller() int { return FuncA(1, 2) }\n")
	if _, err := mgr.FullReScan(p.dir, reg); err != nil {
		t.Fatalf("FullReScan after fix: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnSignatureChanged); got != 0 {
		t.Fatalf("after the caller was fixed %d signature_changed warning(s) remain, want 0", got)
	}
}

func TestWRN01_FullReScanKeepsNodeRemovedUntilTheSymbolIsBack(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "target.go", "package testproject\n\nfunc FuncA() int { return 42 }\n")
	p.write(t, "caller.go", "package testproject\n\nfunc Caller() int { return FuncA() }\n")
	mgr := p.scan(t)
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())

	if err := os.Remove(filepath.Join(p.dir, "target.go")); err != nil {
		t.Fatal(err)
	}
	p.incrementalScan(t, mgr)
	if got := rescanWarnCount(t, mgr, domain.WarnNodeRemoved); got == 0 {
		t.Fatal("incremental scan raised no node_removed warning after the target file was deleted")
	}

	if _, err := mgr.FullReScan(p.dir, reg); err != nil {
		t.Fatalf("FullReScan: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnNodeRemoved); got == 0 {
		t.Fatal("a full rescan dropped the node_removed warning, though Caller still calls the deleted FuncA")
	}

	// Putting the symbol back must retire it.
	path := filepath.Join(p.dir, "target.go")
	if err := os.WriteFile(path, []byte("package testproject\n\nfunc FuncA() int { return 42 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.FullReScan(p.dir, reg); err != nil {
		t.Fatalf("FullReScan after restore: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnNodeRemoved); got != 0 {
		t.Fatalf("after the symbol was restored %d node_removed warning(s) remain, want 0", got)
	}
}

// A second language, because the carry-forward lives in the language-neutral rescan path.
func TestWRN01_FullReScanKeepsAPythonSignatureWarning(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(3 * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	write("lib.py", "def add(a, b):\n    return a + b\n")
	write("app.py", "from lib import add\n\n\ndef caller():\n    return add(1, 2)\n")

	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	write("lib.py", "def add(a, b, c):\n    return a + b + c\n")
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnSignatureChanged); got != 1 {
		t.Fatalf("incremental scan raised %d signature_changed warning(s), want 1", got)
	}

	if _, err := mgr.FullReScan(dir, reg); err != nil {
		t.Fatalf("FullReScan: %v", err)
	}
	if got := rescanWarnCount(t, mgr, domain.WarnSignatureChanged); got != 1 {
		t.Fatalf("after a full rescan %d signature_changed warning(s) remain, want 1: caller still calls add(1, 2)", got)
	}
}

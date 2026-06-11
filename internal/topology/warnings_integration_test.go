package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
)

type warnProj struct {
	dir string
}

func newWarnProj(t *testing.T) *warnProj {
	dir, err := os.MkdirTemp("", "Aracne-warn-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return &warnProj{dir: dir}
}

func (p *warnProj) write(t *testing.T, name, content string) {
	if err := os.WriteFile(filepath.Join(p.dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func (p *warnProj) writeForIncrementalScan(t *testing.T, name, content string) {
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func (p *warnProj) scan(t *testing.T) *topology.TopologyManager {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	mgr := topology.New()
	mgr.Load(filepath.Join(p.dir, "topology.db"))
	if err := mgr.FullScan(p.dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func (p *warnProj) updateFile(t *testing.T, mgr *topology.TopologyManager, name string) {
	p.updateFileWarnings(t, mgr, name)
}

func (p *warnProj) updateFileWarnings(t *testing.T, mgr *topology.TopologyManager, name string) []domain.TopologyWarning {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	warnings, err := mgr.UpdateFile(filepath.Join(p.dir, name), reg)
	if err != nil {
		t.Fatal(err)
	}
	return warnings
}

func (p *warnProj) incrementalScan(t *testing.T, mgr *topology.TopologyManager) []domain.TopologyWarning {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	warnings, err := mgr.IncrementalScan(p.dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	return warnings
}

func countWarns(t *testing.T, mgr *topology.TopologyManager, kind domain.WarningKind) int {
	w, err := mgr.ListWarnings("", "", kind)
	if err != nil {
		t.Fatal(err)
	}
	return len(w)
}

// --- WarnNodeRemoved: function removed from separate file ---
func TestWarnNodeRemoved_Func(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Result int
func Caller() int { return FuncA() }
`)
	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "target.go", "package testproject\n")
	p.updateFile(t, mgr, "target.go")

	if n := countWarns(t, mgr, domain.WarnNodeRemoved); n == 0 {
		t.Fatal("expected WarnNodeRemoved after removing FuncA")
	}

	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)
	p.updateFile(t, mgr, "target.go")

	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after restoring FuncA, got %d", n)
	}
}

func TestUpdateFileReturnsNewTopologyWarnings(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Result int
func Caller() int { return FuncA() }
`)
	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "target.go", "package testproject\n")
	warnings := p.updateFileWarnings(t, mgr, "target.go")
	if len(warnings) == 0 {
		t.Fatal("expected update-file to return newly added topology warnings")
	}
	if warnings[0].Kind != domain.WarnNodeRemoved {
		t.Fatalf("expected returned WarnNodeRemoved, got %q", warnings[0].Kind)
	}

	warnings = p.updateFileWarnings(t, mgr, "target.go")
	if len(warnings) != 0 {
		t.Fatalf("expected no newly added warnings on unchanged update, got %d", len(warnings))
	}
}

func TestIncrementalScanClearsNodeRemovedWhenCallerStopsUsingRemovedNode(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Result int
func Caller() int { return FuncA() }
`)
	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.writeForIncrementalScan(t, "target.go", "package testproject\n")
	p.incrementalScan(t, mgr)
	if n := countWarns(t, mgr, domain.WarnNodeRemoved); n == 0 {
		t.Fatal("expected WarnNodeRemoved after removing FuncA")
	}

	p.writeForIncrementalScan(t, "caller.go", `package testproject
var Result int
func Caller() int { return 0 }
`)
	p.incrementalScan(t, mgr)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected stale warning to clear after caller stopped using removed node, got %d", n)
	}
}

func TestIncrementalScanClearsSignatureChangedWhenCallerIsEdited(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA(1) }
`)
	p.write(t, "target.go", `package testproject
func FuncA(x int) int { return x }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.writeForIncrementalScan(t, "target.go", `package testproject
func FuncA(x int, y int) int { return x + y }
`)
	p.incrementalScan(t, mgr)
	if n := countWarns(t, mgr, domain.WarnSignatureChanged); n == 0 {
		t.Fatal("expected WarnSignatureChanged")
	}

	p.writeForIncrementalScan(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA(1, 2) }
`)
	p.incrementalScan(t, mgr)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected stale signature warning to clear after caller edit, got %d", n)
	}
}

// --- WarnNodeRemoved: struct removed from separate file ---
func TestWarnNodeRemoved_Struct(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "user.go", `package testproject
func User() int { var s MyStr; return s.X }
`)
	p.write(t, "defs.go", `package testproject
type MyStr struct { X int }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "defs.go", "package testproject\n")
	p.updateFile(t, mgr, "defs.go")

	if n := countWarns(t, mgr, domain.WarnNodeRemoved); n == 0 {
		t.Fatal("expected WarnNodeRemoved after removing MyStr")
	}

	p.write(t, "defs.go", `package testproject
type MyStr struct { X int }
`)
	p.updateFile(t, mgr, "defs.go")

	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after restoring MyStr, got %d", n)
	}
}

// --- WarnSignatureChanged: verify warning is created ---
func TestWarnSignatureChanged(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA(1) }
`)
	p.write(t, "target.go", `package testproject
func FuncA(x int) int { return x }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "target.go", `package testproject
func FuncA(x int, y int) int { return x + y }
`)
	p.updateFile(t, mgr, "target.go")

	if n := countWarns(t, mgr, domain.WarnSignatureChanged); n == 0 {
		t.Fatal("expected WarnSignatureChanged")
	}
}

// --- WarnUseMissingNode: call non-existent function ---
func TestWarnUseMissingNode_Func(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA() }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, domain.WarnUseMissingNode); n == 0 {
		t.Fatal("expected WarnUseMissingNode for non-existent FuncA")
	}

	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)
	p.updateFile(t, mgr, "target.go")
	p.updateFile(t, mgr, "target.go")

	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after adding FuncA, got %d", n)
	}
}

// --- WarnUseMissingNode: struct composite lit referencing non-existent struct ---
func TestWarnUseMissingNode_StructRef(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "user.go", `package testproject
var G int
func User() MyStr { return MyStr{} }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, domain.WarnUseMissingNode); n == 0 {
		t.Fatal("expected WarnUseMissingNode for non-existent MyStr")
	}

	p.write(t, "defs.go", `package testproject
type MyStr struct { X int }
`)
	p.updateFile(t, mgr, "defs.go")
	p.updateFile(t, mgr, "defs.go")

	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after adding MyStr, got %d", n)
	}
}

// --- WarnCleanup: removing the caller also removes the warning ---
func TestWarnCleanup_CallerRemoved(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA() }
`)
	p.write(t, "target.go", `package testproject
func FuncA() int { return 42 }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "target.go", "package testproject\n")
	p.updateFile(t, mgr, "target.go")
	if n := countWarns(t, mgr, domain.WarnNodeRemoved); n == 0 {
		t.Fatal("expected WarnNodeRemoved after removing FuncA")
	}

	p.write(t, "caller.go", "package testproject\n")
	p.updateFile(t, mgr, "caller.go")
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after removing caller, got %d", n)
	}
}

// --- SignatureChanged + then caller removal cleans up ---
func TestWarnSignatureChanged_RemoveCaller(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject
var Res int
func Caller() int { return FuncA(1) }
`)
	p.write(t, "target.go", `package testproject
func FuncA(x int) int { return x }
`)

	mgr := p.scan(t)
	p.write(t, "target.go", `package testproject
func FuncA(x int, y int) int { return x + y }
`)
	p.updateFile(t, mgr, "target.go")
	if n := countWarns(t, mgr, domain.WarnSignatureChanged); n == 0 {
		t.Fatal("expected WarnSignatureChanged")
	}

	p.write(t, "caller.go", "package testproject\n")
	p.updateFile(t, mgr, "caller.go")
	if n := countWarns(t, mgr, domain.WarnSignatureChanged); n != 0 {
		t.Fatalf("expected SignatureChanged cleaned up after caller removed, got %d", n)
	}
}

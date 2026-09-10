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

// A caller that reaches its callee through a value bound from a METHOD call used to be
// invisible to signature_changed.
//
// The Go resolver typed `x := pkg.Func()` and nothing else with a selector on the left, so
// `mgr, err := s.manager()` left mgr untyped and mgr.Profiles() resolved to no node at all.
// With no calls edge into Profiles, getCallers returned an empty list and a breaking change to
// its signature raised NO warning -- the topology re-synced, reported nothing, and the model
// was told everything was fine. The shape is `x, err := recv.Thing()`, which is most Go.
//
// Split across two files on purpose: the scanner skips callers that lived in the OLD version
// of the edited file, so a same-file caller would pass whether or not the edge exists.
func TestSignatureChangedFindsCallerThroughMethodReturnedValue(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "manager.go", `package testproject

type Manager struct{}

func (m *Manager) Profiles(n int) int { return n }
`)
	p.write(t, "server.go", `package testproject

type Server struct{}

func (s *Server) manager() (*Manager, error) { return &Manager{}, nil }

func (s *Server) Handle() int {
	mgr, err := s.manager()
	if err != nil {
		return 0
	}
	return mgr.Profiles(1)
}
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 warnings after the first scan, got %d", n)
	}

	p.writeForIncrementalScan(t, "manager.go", `package testproject

type Manager struct{}

func (m *Manager) Profiles(n int, verbose bool) int { return n }
`)
	p.incrementalScan(t, mgr)

	want := "testproject.(Server).Handle"
	warns, err := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range warns {
		if w.TargetID == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a signature_changed warning naming caller %q, got %+v", want, warns)
	}
}

// A CALLER IN THE EDITED FILE IS STILL A CALLER TO VERIFY.
//
// Both signature-warning producers used to skip referrers declared in the file the update
// re-parsed -- goscanner by testing the caller against its own oldFunctions map, and
// helper.ExpandSignatureWarnings through a skipPaths set -- on the reasoning that the re-parse
// is authoritative for that file. That holds for a symbol that was REMOVED: the re-parse finds
// the dangling reference and reports use_missing_node against the same caller
// (TestSameFileCallerOfARemovedSymbolStillWarns below). It does not hold for a signature
// change, where the callee still resolves and the re-parse has nothing to complain about.
//
// So the single most common breaking edit -- widen a helper's parameter list, fix its callers
// afterwards -- reported nothing whenever the caller happened to live beside it. The code did
// not compile and `arac warnings list` said "No warnings found".
func TestSameFileCallerOfAChangedSignatureIsWarned(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "pkg.go", `package testproject
func Helper(a int) int { return a * 2 }
func SameFileCaller() int { return Helper(1) }
`)
	p.write(t, "other.go", `package testproject
func CrossFileCaller() int { return Helper(2) }
`)

	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected 0 after scan, got %d", n)
	}

	p.write(t, "pkg.go", `package testproject
func Helper(a int, b int) int { return a * b }
func SameFileCaller() int { return Helper(1) }
`)
	p.updateFile(t, mgr, "pkg.go")

	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var sameFile, crossFile bool
	for _, w := range topo.Warnings {
		if w.Kind != domain.WarnSignatureChanged {
			continue
		}
		switch w.TargetID {
		case "testproject.SameFileCaller":
			sameFile = true
		case "testproject.CrossFileCaller":
			crossFile = true
		}
	}
	if !crossFile {
		t.Error("the cross-file caller must be warned about (this always worked)")
	}
	if !sameFile {
		t.Error("the caller in the edited file must be warned about too: the callee still " +
			"resolves, so nothing else reports it and the build is broken silently")
	}
}

// And the warning still retires itself once the call site fits, so a caller fixed in the same
// edit costs nothing. Without this the fix above would trade a missing warning for a permanent
// one, which is the failure that moved node_removed off the self-attributed shape.
func TestSameFileSignatureWarningRetiresWhenTheCallerIsFixed(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "pkg.go", `package testproject
func Helper(a int) int { return a * 2 }
func SameFileCaller() int { return Helper(1) }
`)
	mgr := p.scan(t)

	p.write(t, "pkg.go", `package testproject
func Helper(a int, b int) int { return a * b }
func SameFileCaller() int { return Helper(1) }
`)
	p.updateFile(t, mgr, "pkg.go")
	if n := countWarns(t, mgr, domain.WarnSignatureChanged); n == 0 {
		t.Fatal("expected a signature warning for the same-file caller")
	}

	p.write(t, "pkg.go", `package testproject
func Helper(a int, b int) int { return a * b }
func SameFileCaller() int { return Helper(1, 2) }
`)
	p.updateFile(t, mgr, "pkg.go")
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("a fixed call site must retire the warning, got %d", n)
	}
}

// The case the same-file skip WAS right about, kept so the fix above cannot be undone by
// re-introducing it: a removed symbol is reported against its same-file caller by the re-parse
// itself, as use_missing_node.
func TestSameFileCallerOfARemovedSymbolStillWarns(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "pkg.go", `package testproject
func Helper(a int) int { return a * 2 }
func SameFileCaller() int { return Helper(1) }
`)
	mgr := p.scan(t)

	p.write(t, "pkg.go", `package testproject
func SameFileCaller() int { return Helper(1) }
`)
	p.updateFile(t, mgr, "pkg.go")

	if n := countWarns(t, mgr, domain.WarnUseMissingNode); n == 0 {
		t.Fatal("expected use_missing_node for the same-file caller of a deleted symbol")
	}
}

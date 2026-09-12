package topology_test

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A Go callee reduced to zero parameters reaches the contract matcher in two shapes: an empty
// parameter list from a fresh parse, and no "input" property at all once it has been read back
// from SQLite. The single-file partial path reads the callee from the database, so the call the
// agent fixed -- Take(1) becoming Take() -- was judged "cannot say" there and the
// signature_changed warning stood until something forced the full path.
func TestZeroParamCalleeWarningClearsOnThePartialPath(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "target.go", "package testproject\nfunc Take(a int) int { return a }\n")
	p.write(t, "caller.go", "package testproject\nfunc Caller() int { return Take(1) }\n")
	mgr := p.scan(t)

	p.writeForIncrementalScan(t, "target.go", "package testproject\nfunc Take() int { return 0 }\n")
	p.incrementalScan(t, mgr)
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("dropping the parameter should warn the caller, got %d", n)
	}

	// A touch that is not a fix keeps it: the call still passes one argument.
	p.writeForIncrementalScan(t, "caller.go", "package testproject\n\n// touched\nfunc Caller() int { return Take(1) }\n")
	p.incrementalScan(t, mgr)
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("a call that still does not fit must keep its warning, got %d", n)
	}

	before := topology.PartialIncrementalCount()
	p.writeForIncrementalScan(t, "caller.go", "package testproject\nfunc Caller() int { return Take() }\n")
	p.incrementalScan(t, mgr)
	if topology.PartialIncrementalCount() == before {
		t.Fatal("the fix did not take the partial path, so this case proves nothing")
	}
	if n := sigWarns(t, mgr); n != 0 {
		w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
		t.Errorf("fixing the call should clear the warning on the partial path, %d left: %+v", n, w)
	}
}

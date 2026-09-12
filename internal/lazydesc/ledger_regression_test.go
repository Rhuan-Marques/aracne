package lazydesc

import (
	"errors"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// DE-4: a provider that FAILED -- a CLI exiting non-zero ("not logged in"), an HTTP 401/429/5xx
// -- has said nothing about the resources. It used to be persisted exactly like a model that
// declined them, so one bad minute disabled the lazy fill for everything it touched until the
// code changed. The next process must try them again.
func TestProviderFailureIsNotPersisted(t *testing.T) {
	mgr := project(t, fixture())
	failing := &fakeGenerator{err: errors.New("claude: exit status 1: not logged in")}
	f := NewWithGenerator(mgr, lazyConfig(true), "", failing)
	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a failed fill reported a change")
	}

	// The same process does not hammer a broken provider: one attempt per process.
	calls := failing.calls()
	if calls == 0 {
		t.Fatal("the generator was never called")
	}
	f.FillForRead(topo, []string{"seed"})
	if failing.calls() != calls {
		t.Errorf("a provider failure was retried within the process (%d -> %d calls)", calls, failing.calls())
	}

	// The next process -- the next intercepted `cat` -- tries again once the provider works.
	next := NewWithGenerator(mgr, lazyConfig(true), "", &fakeGenerator{})
	topo, _ = mgr.ReadAll()
	if !next.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a transient provider failure was persisted: the next process never retried")
	}
	if got := descriptionOf(t, mgr, "callee"); got != "described callee" {
		t.Errorf("callee description = %q after the provider recovered", got)
	}
}

// The other side of DE-4, pinned so the fix cannot overreach: a model that ANSWERED and
// declined a resource is still remembered across processes, which is what the ledger is for.
func TestDeclinedResourceIsStillPersisted(t *testing.T) {
	mgr := project(t, fixture())
	declines := &fakeGenerator{answer: func(Request) string { return "" }}
	f := NewWithGenerator(mgr, lazyConfig(true), "", declines)
	topo, _ := mgr.ReadAll()
	f.FillForRead(topo, []string{"seed"})
	if declines.calls() == 0 {
		t.Fatal("the generator was never called")
	}

	gen := &fakeGenerator{}
	next := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	topo, _ = mgr.ReadAll()
	if next.FillForRead(topo, []string{"seed"}) || gen.calls() != 0 {
		t.Fatalf("a declined resource was re-planned by the next process (%d calls)", gen.calls())
	}
}

// DE-3: a target that was WRITTEN is not recorded. The record outlived the description it was
// about -- `arac scan --hard` wipes descriptions under unchanged fingerprints -- and then
// suppressed the one fill that could put the description back, forever.
func TestWrittenDescriptionsAreNotLedgered(t *testing.T) {
	mgr := project(t, fixture())
	f := NewWithGenerator(mgr, lazyConfig(true), "", &fakeGenerator{})
	topo, _ := mgr.ReadAll()
	if !f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("the first fill wrote nothing")
	}
	recorded, err := helper.ReadDescriptionAttempts(mgr.DbPath(), []string{"callee", "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Errorf("written targets were ledgered as attempts: %v", recorded)
	}

	// The descriptions are lost under unchanged fingerprints, with the ledger left alone.
	if _, err := helper.ClearDescriptions(mgr.DbPath(), nil); err != nil {
		t.Fatal(err)
	}
	gen := &fakeGenerator{}
	next := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	topo, _ = mgr.ReadAll()
	if !next.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a description that was written and then lost was never described again")
	}
	if got := descriptionOf(t, mgr, "callee"); got != "described callee" {
		t.Errorf("callee description = %q", got)
	}
}

package lazydesc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// fakeGenerator answers every request with a canned description and records what it was asked.
type fakeGenerator struct {
	mu      sync.Mutex
	batches [][]string
	err     error
	// answer overrides the canned description; return "" to omit the id from the reply.
	answer func(Request) string
}

func (g *fakeGenerator) Describe(_ context.Context, batch Batch) (map[string]string, error) {
	g.mu.Lock()
	var seen []string
	for _, r := range batch.Resources {
		seen = append(seen, r.ID)
	}
	g.batches = append(g.batches, seen)
	err, answer := g.err, g.answer
	g.mu.Unlock()

	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range batch.Resources {
		text := fmt.Sprintf("described %s", r.Name)
		if answer != nil {
			text = answer(r)
		}
		if text != "" {
			out[r.ID] = text
		}
	}
	return out, nil
}

func (g *fakeGenerator) asked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, b := range g.batches {
		out = append(out, b...)
	}
	return out
}

func (g *fakeGenerator) calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.batches)
}

// project writes a topology to a temp database and returns a manager over it.
func project(t *testing.T, topo *domain.Topology) *topology.TopologyManager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topology.db")
	if err := helper.WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	mgr := topology.New()
	if err := mgr.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return mgr
}

func lazyConfig(on bool) *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Descriptions.Lazy.Enabled = &on
	// The exemplar block reads the topology; it is exercised in its own package and only
	// adds noise here.
	cfg.Descriptions.StyleExemplars = 0
	return cfg
}

func descriptionOf(t *testing.T, mgr *topology.TopologyManager, id string) string {
	t.Helper()
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return topo.Resources[id].Description
}

func TestFillWritesDescriptionsForNeighbours(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)

	topo, _ := mgr.ReadAll()
	if !f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("FillForRead reported no change")
	}
	if got := descriptionOf(t, mgr, "callee"); got != "described callee" {
		t.Fatalf("callee description = %q", got)
	}
	if got := descriptionOf(t, mgr, "Thing"); got != "described Thing" {
		t.Fatalf("Thing description = %q", got)
	}
	// The resource that was read keeps its own (absent) description: its body was the answer.
	if got := descriptionOf(t, mgr, "seed"); got != "" {
		t.Fatalf("the requested resource was described: %q", got)
	}
	// And a resource that already had one is untouched.
	if got := descriptionOf(t, mgr, "described"); got != "already documented" {
		t.Fatalf("an existing description was overwritten: %q", got)
	}
}

// The switch has to actually switch it off, all the way down to not calling the generator.
func TestFillDisabledDoesNothing(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	f := NewWithGenerator(mgr, lazyConfig(false), "", gen)
	if f != nil {
		t.Fatal("a disabled config should produce a nil Filler")
	}
	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a nil Filler reported a change")
	}
	if f.FillForNodes(topo, []string{"callee"}) {
		t.Fatal("a nil Filler reported a change")
	}
	if f.Enabled() {
		t.Fatal("a nil Filler should not report itself enabled")
	}
	if gen.calls() != 0 {
		t.Fatalf("the generator was called %d times with the feature off", gen.calls())
	}
	if got := descriptionOf(t, mgr, "callee"); got != "" {
		t.Fatalf("a disabled fill wrote %q", got)
	}
}

func TestFillForNodesDescribesTheNodesThemselves(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)

	topo, _ := mgr.ReadAll()
	if !f.FillForNodes(topo, []string{"seed", "callee"}) {
		t.Fatal("FillForNodes reported no change")
	}
	if got := descriptionOf(t, mgr, "seed"); got != "described seed" {
		t.Fatalf("seed description = %q", got)
	}
	if got := descriptionOf(t, mgr, "callee"); got != "described callee" {
		t.Fatalf("callee description = %q", got)
	}
}

// A node tried once is not tried again in this process, whatever the outcome. Without this a
// resource the model declines to describe costs a provider call on every read that names it,
// forever.
func TestFillDoesNotRetryWithinAProcess(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{answer: func(Request) string { return "" }} // answers for nothing
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)

	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a fill that wrote nothing reported a change")
	}
	first := gen.calls()
	if first == 0 {
		t.Fatal("the generator was never called")
	}
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("the retry reported a change")
	}
	if gen.calls() != first {
		t.Fatalf("the generator was called again (%d -> %d)", first, gen.calls())
	}
}

// A generator that fails is a read that renders as it would have anyway. It is never a read
// that fails.
func TestFillSurvivesAFailingGenerator(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{err: errors.New("provider exploded")}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)

	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a failed fill reported a change")
	}
	if got := descriptionOf(t, mgr, "callee"); got != "" {
		t.Fatalf("a failed fill wrote %q", got)
	}
}

// A partial answer is kept. Four descriptions the project did not have a moment ago beat a
// clean failure.
func TestFillKeepsAPartialAnswer(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{answer: func(r Request) string {
		if r.ID == "callee" {
			return "described callee"
		}
		return ""
	}}
	cfg := lazyConfig(true)
	one := 1
	cfg.Descriptions.Lazy.BatchSize = &one
	f := NewWithGenerator(mgr, cfg, "", gen)

	topo, _ := mgr.ReadAll()
	if !f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a partial fill reported no change")
	}
	if got := descriptionOf(t, mgr, "callee"); got != "described callee" {
		t.Fatalf("callee = %q", got)
	}
	if got := descriptionOf(t, mgr, "Thing"); got != "" {
		t.Fatalf("Thing was written despite no answer: %q", got)
	}
}

// An over-budget description is rejected on the way in, exactly as it is for the
// update_description tool -- the fill must not be a back door around the cap that keeps every
// later CONTEXT block small.
func TestFillRejectsOverBudgetDescriptions(t *testing.T) {
	mgr := project(t, fixture())
	long := strings.Repeat("x", domain.DescriptionBudgetFunction+50)
	gen := &fakeGenerator{answer: func(r Request) string {
		if r.ID == "callee" {
			return long
		}
		return "described " + r.Name
	}}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)

	topo, _ := mgr.ReadAll()
	f.FillForRead(topo, []string{"seed"})
	if got := descriptionOf(t, mgr, "callee"); got != "" {
		t.Fatalf("an over-budget description was stored: %q", got)
	}
	if got := descriptionOf(t, mgr, "Thing"); got != "described Thing" {
		t.Fatalf("a rejected sibling took the batch down with it: %q", got)
	}
}

func TestFillRespectsMaxNodes(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	cfg := lazyConfig(true)
	one := 1
	cfg.Descriptions.Lazy.MaxNodes = &one
	f := NewWithGenerator(mgr, cfg, "", gen)

	topo, _ := mgr.ReadAll()
	f.FillForRead(topo, []string{"seed"})
	if got := gen.asked(); len(got) != 1 {
		t.Fatalf("asked for %v, want one node", got)
	}
}

func TestFillBatchesAndCarriesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	if err := os.WriteFile(src, []byte("package a\n\nfunc callee() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	topo := fixture()
	res := topo.Resources["callee"]
	res.Location = domain.Location{Path: src, StartsAt: 3, EndsAt: 3}
	topo.Resources["callee"] = res

	mgr := project(t, topo)
	var gotSource string
	gen := &fakeGenerator{answer: func(r Request) string {
		if r.ID == "callee" {
			gotSource = r.Source
		}
		return "described " + r.Name
	}}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	topoRead, _ := mgr.ReadAll()
	f.FillForRead(topoRead, []string{"seed"})

	if !strings.Contains(gotSource, "func callee()") {
		t.Fatalf("source handed to the generator = %q, want the cut of the resource", gotSource)
	}
}

// A resource whose file is gone still goes to the generator, with no source. Dropping it would
// mean a read of a moved file quietly stops filling anything.
func TestFillToleratesAnUnreadableCut(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	topo, _ := mgr.ReadAll()
	if !f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a fill over unreadable sources reported no change")
	}
}

// No generator at all -- the shape of an unconfigured project -- is a no-op, and specifically
// one that leaves the nodes free to be tried again once a key appears.
func TestFillWithoutAGeneratorIsANoOpAndNotRecorded(t *testing.T) {
	mgr := project(t, fixture())
	restore := GeneratorFactory
	GeneratorFactory = func(helper.ResolvedLazyDescriptions) (Generator, error) { return nil, nil }
	defer func() { GeneratorFactory = restore }()

	f := New(mgr, lazyConfig(true), "")
	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a fill with no generator reported a change")
	}

	// Now a generator appears; the same nodes must still be reachable.
	gen := &fakeGenerator{}
	f2 := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	if !f2.FillForRead(topo, []string{"seed"}) {
		t.Fatal("nodes skipped for want of a generator were not retried")
	}
}

// A factory that errors is indistinguishable, from the read's point of view, from one that
// reports no provider.
func TestFillWithAFailingFactoryIsANoOp(t *testing.T) {
	mgr := project(t, fixture())
	restore := GeneratorFactory
	GeneratorFactory = func(helper.ResolvedLazyDescriptions) (Generator, error) {
		return nil, errors.New("no key")
	}
	defer func() { GeneratorFactory = restore }()

	f := New(mgr, lazyConfig(true), "")
	topo, _ := mgr.ReadAll()
	if f.FillForRead(topo, []string{"seed"}) {
		t.Fatal("a failing factory reported a change")
	}
}

func TestNewRequiresAManagerAndAConfig(t *testing.T) {
	mgr := project(t, fixture())
	if New(nil, lazyConfig(true), "") != nil {
		t.Fatal("a nil manager should produce a nil Filler")
	}
	if New(mgr, nil, "") != nil {
		t.Fatal("a nil config should produce a nil Filler")
	}
}

// Two reads that overlap on the same nodes claim them exactly once between them. Without the
// claim, a project whose reads run concurrently would pay for the same description twice.
func TestConcurrentFillsClaimEachNodeOnce(t *testing.T) {
	mgr := project(t, fixture())
	gen := &fakeGenerator{}
	f := NewWithGenerator(mgr, lazyConfig(true), "", gen)
	topo, _ := mgr.ReadAll()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.FillForRead(topo, []string{"seed"})
		}()
	}
	wg.Wait()

	seen := map[string]int{}
	for _, id := range gen.asked() {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s was described %d times, want once", id, n)
		}
	}
	if len(seen) == 0 {
		t.Fatal("nothing was described at all")
	}
}

// A deadline that has already passed must return, not hang. The read is what is waiting.
func TestFillHonoursItsDeadline(t *testing.T) {
	mgr := project(t, fixture())
	release := make(chan struct{})
	gen := &blockingGenerator{release: release}
	cfg := lazyConfig(true)
	one := 1
	cfg.Descriptions.Lazy.TimeoutSeconds = &one
	f := NewWithGenerator(mgr, cfg, "", gen)

	topo, _ := mgr.ReadAll()
	done := make(chan bool, 1)
	go func() { done <- f.FillForRead(topo, []string{"seed"}) }()

	select {
	case changed := <-done:
		if changed {
			t.Fatal("a fill that timed out reported a change")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the fill did not return when its deadline expired")
	}
	close(release)
}

// blockingGenerator never answers until its channel is closed or its context is cancelled.
type blockingGenerator struct{ release chan struct{} }

func (g *blockingGenerator) Describe(ctx context.Context, _ Batch) (map[string]string, error) {
	select {
	case <-g.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

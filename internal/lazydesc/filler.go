package lazydesc

import (
	"context"
	"strings"
	"sync"
	"time"

	"aracne/internal/helper"
	"aracne/internal/prompts"
	"aracne/internal/topogrep"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// Filler is the read path's entry point: plan, generate, write, report whether the topology
// changed underneath the caller.
//
// A nil *Filler is a working, switched-off Filler. Every method starts with a nil check, so
// the callers -- three read paths and three search paths -- hold a *Filler unconditionally and
// none of them grows a second branch for the feature being off.
type Filler struct {
	mgr     *topology.TopologyManager
	cfg     *helper.Config
	lazyCfg helper.ResolvedLazyDescriptions

	// mu guards attempted, and nothing else.
	//
	// Deliberately NOT held across the generation. A fill can legitimately take a minute, and
	// a lock spanning it would block a concurrent read that needs nothing at all -- turning a
	// feature that makes one read slower into one that makes every read slower. The
	// bookkeeping is what needs to be atomic: two reads that overlap on a node see it claimed
	// exactly once, and the loser renders without it rather than waiting for the winner. That
	// is the same output it would have had with the feature off, one read earlier.
	mu sync.Mutex
	// attempted is every id this process has already tried, whether it succeeded or not.
	//
	// The failures are the point. A resource the model declines to describe -- an empty
	// function, a generated stub, a body the cut could not read -- would otherwise be
	// re-planned by every subsequent read that names it, buying a provider call per read
	// forever. Successes are in here too, but they drop out of the plan on their own the
	// moment the topology is re-read.
	//
	// Process-scoped on purpose. `arac` is mostly one-shot, and a long-lived server that
	// restarts gets a fresh chance at whatever failed; persisting the failures would need a
	// migration and an invalidation rule for "the code changed since".
	attempted map[string]bool

	// gen is resolved once, lazily, on the first fill that has something to do. Building it
	// eagerly would probe the environment on every read on a warm repo, where the answer is
	// always "nothing to fill".
	gen     Generator
	genOnce sync.Once
}

// New returns a Filler for this project, or nil when lazy generation is off.
//
// nil is the switched-off state rather than a flag on the struct because it makes the
// disabled path free at every call site: a nil receiver, one branch, no config re-read.
func New(mgr *topology.TopologyManager, cfg *helper.Config, harness string) *Filler {
	if mgr == nil || cfg == nil || !cfg.LazyDescriptionsEnabled() {
		return nil
	}
	return &Filler{
		mgr:       mgr,
		cfg:       cfg,
		lazyCfg:   cfg.EffectiveLazyDescriptions(harness),
		attempted: map[string]bool{},
	}
}

// NewWithGenerator is New with the generator supplied, for tests and for callers that already
// hold one.
func NewWithGenerator(mgr *topology.TopologyManager, cfg *helper.Config, harness string, gen Generator) *Filler {
	f := New(mgr, cfg, harness)
	if f == nil {
		return nil
	}
	f.genOnce.Do(func() { f.gen = gen })
	return f
}

// planOptions resolves the config once into what the planner reads.
func (f *Filler) planOptions() PlanOptions {
	return PlanOptions{
		Targets:           f.cfg.Descriptions.Kinds,
		Filter:            f.cfg.EffectiveContextFilter(),
		IncludeNotVisible: f.cfg.Descriptions.IncludeNotVisible,
		IncludeIncoming:   f.cfg.EffectiveIncludeIncoming(),
		MaxNodes:          f.lazyCfg.MaxNodes,
	}
}

// FillForRead describes whatever a read of `ids` is about to name and cannot describe.
//
// It reports whether the database changed, which is the caller's signal to re-read the
// topology and render again. False means "render what you already have" and covers every
// failure as well as the ordinary warm-repo case.
func (f *Filler) FillForRead(topo *domain.Topology, ids []string) bool {
	if f == nil || topo == nil {
		return false
	}
	return f.fill(topo, ReadTargets(topo, ids, f.planOptions()))
}

// FillForReadIn is FillForRead bounded to a set of ids -- the windowed read, whose context
// section is restricted to what the window itself mentions.
func (f *Filler) FillForReadIn(topo *domain.Topology, ids []string, restrict map[string]bool) bool {
	if f == nil || topo == nil {
		return false
	}
	opt := f.planOptions()
	opt.Restrict = restrict
	return f.fill(topo, ReadTargets(topo, ids, opt))
}

// FillForNodes describes the nodes themselves -- the search case, where the node's own header
// is the result row.
func (f *Filler) FillForNodes(topo *domain.Topology, ids []string) bool {
	if f == nil || topo == nil {
		return false
	}
	return f.fill(topo, NodeTargets(topo, ids, f.planOptions()))
}

// Enabled reports whether this Filler will do anything. It exists so a caller can skip
// assembling a candidate list it is about to throw away.
func (f *Filler) Enabled() bool { return f != nil }

// fill runs a plan to completion, or to its deadline.
func (f *Filler) fill(topo *domain.Topology, targets []Target) bool {
	if len(targets) == 0 {
		return false
	}
	// Resolved before the claim: with no provider nothing is claimed, so the next process --
	// or the next read after a key appears in the environment -- still gets a clean first try.
	if f.generator() == nil {
		return false
	}
	pending := f.claim(targets)
	if len(pending) == 0 {
		return false
	}

	ctx := context.Background()
	if f.lazyCfg.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(f.lazyCfg.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	results := f.run(ctx, topo, pending)
	return f.write(pending, results) > 0
}

// claim takes the targets this fill is responsible for, marking them attempted so no other
// fill on this Filler picks them up -- whether or not the attempt succeeds.
//
// The failures are the point of recording them. A resource the model declines to describe
// would otherwise be re-planned by every subsequent read that names it, buying a provider call
// per read forever.
func (f *Filler) claim(targets []Target) []Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	pending := make([]Target, 0, len(targets))
	for _, t := range targets {
		if f.attempted[t.ID] {
			continue
		}
		f.attempted[t.ID] = true
		pending = append(pending, t)
	}
	return pending
}

// generator resolves the generator once per Filler.
func (f *Filler) generator() Generator {
	f.genOnce.Do(func() {
		// The error is deliberately dropped. There is nowhere to report it: this runs
		// inside a read whose output is the answer to a different question, and a factory
		// that cannot build a generator produces the same outcome as one that reports no
		// provider -- the read renders as if the feature were off.
		gen, err := GeneratorFactory(f.lazyCfg)
		if err == nil {
			f.gen = gen
		}
	})
	return f.gen
}

// run fans the batches out and collects whatever comes back.
//
// "In the background, then waited on" is exactly this: the batches are goroutines so several
// completions are in flight at once, and the read blocks until they are done or the deadline
// expires. A batch that fails takes only itself down -- its siblings' descriptions are still
// written, because four descriptions the project did not have is a better answer than none.
func (f *Filler) run(ctx context.Context, topo *domain.Topology, targets []Target) map[string]string {
	batches := chunk(targets, f.lazyCfg.BatchSize)
	parallel := f.lazyCfg.Parallel
	if parallel > len(batches) {
		parallel = len(batches)
	}
	if parallel < 1 {
		parallel = 1
	}

	jobs := make(chan []Target)
	out := make(chan map[string]string, len(batches))
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				descs, err := f.gen.Describe(ctx, f.batch(topo, batch))
				if err != nil {
					out <- nil
					continue
				}
				out <- descs
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, batch := range batches {
			select {
			case jobs <- batch:
			case <-ctx.Done():
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(out)
		close(done)
	}()

	merged := map[string]string{}
	collect := func() {
		for descs := range out {
			for id, desc := range descs {
				merged[id] = desc
			}
		}
	}
	select {
	case <-done:
		collect()
	case <-ctx.Done():
		// The deadline expired. Whatever already landed in the buffered channel is kept --
		// it was generated and paid for -- and the outstanding goroutines are left to their
		// own cancellation rather than waited on, so the read returns on time.
		for {
			select {
			case descs, ok := <-out:
				if !ok {
					return merged
				}
				for id, desc := range descs {
					merged[id] = desc
				}
			default:
				return merged
			}
		}
	}
	return merged
}

// batch turns targets into a request, cutting each resource's source and choosing the
// house-style exemplars for it.
func (f *Filler) batch(topo *domain.Topology, targets []Target) Batch {
	reqs := make([]Request, 0, len(targets))
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		reqs = append(reqs, Request{ID: t.ID, Name: t.Name, Kind: t.Kind, Source: f.source(topo, t.ID)})
		ids = append(ids, t.ID)
	}
	var exemplars []prompts.DescriptionExemplar
	if n := f.cfg.Descriptions.StyleExemplars; n > 0 {
		exemplars = prompts.BuildDescriptionExemplars(topo, ids, n)
	}
	return Batch{Resources: reqs, Exemplars: exemplars}
}

// source cuts a resource's own text.
//
// Deliberately the RAW cut, and deliberately not the read tool. Two reasons, and both matter:
// a "# CONTEXT:" tree of the resource's neighbours would pull the description toward what the
// resource touches instead of what it does (the same reasoning as makeResourceReader in the
// sweep), and going through the read path from inside a read would be a fill that can trigger
// a fill.
func (f *Filler) source(topo *domain.Topology, id string) string {
	res, ok := topo.Resources[id]
	if !ok {
		return ""
	}
	entry, err := f.mgr.Cut(res.Location)
	if err != nil || entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.Cut)
}

// write stores the descriptions and returns how many landed.
//
// Each write goes through TopologyManager.UpdateDescription, so an over-budget description is
// rejected and an id that no longer exists is an error -- the same two gates the
// update_description tool applies. A rejection is dropped silently: the resource stays
// undescribed, it is already in the attempted set, and the read it was for renders without it.
func (f *Filler) write(targets []Target, descriptions map[string]string) int {
	if len(descriptions) == 0 {
		return 0
	}
	kinds := make(map[string]domain.ResourceKind, len(targets))
	for _, t := range targets {
		kinds[t.ID] = t.Kind
	}
	written := 0
	for _, t := range targets {
		desc, ok := descriptions[t.ID]
		if !ok || strings.TrimSpace(desc) == "" {
			continue
		}
		if err := f.mgr.UpdateDescription(t.ID, kinds[t.ID], desc); err != nil {
			continue
		}
		written++
	}
	return written
}

// chunk splits targets into batches.
func chunk(targets []Target, size int) [][]Target {
	if size <= 0 {
		size = helper.DefaultLazyBatchSize
	}
	var out [][]Target
	for start := 0; start < len(targets); start += size {
		end := start + size
		if end > len(targets) {
			end = len(targets)
		}
		out = append(out, targets[start:end])
	}
	return out
}

// FillSearch describes the nodes a search found and could not describe, then writes the new
// prose back into the result in place.
//
// It is the one entry point all three search surfaces share -- the MCP `grep` tool, `arac
// grep` and `arac cmd -- grep` -- so the rule for which nodes are worth describing lives in
// one place rather than being restated, and drifting, three times.
func (f *Filler) FillSearch(topo *domain.Topology, res *topogrep.Result) bool {
	if f == nil || topo == nil || res == nil {
		return false
	}
	ids := topogrep.LazyTargets(res, 0)
	if len(ids) == 0 {
		return false
	}
	if !f.FillForNodes(topo, ids) {
		return false
	}
	refreshed, err := f.mgr.ReadAll()
	if err != nil {
		return false
	}
	topogrep.ApplyDescriptions(res, refreshed)
	return true
}

package lazydesc

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
	harness string
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
	// A description worker, and everything a description worker started, never fills. Without
	// this the CLI provider closes a loop that spends real money: worker -> `claude -p` -> that
	// project's hooks -> `arac cmd` -> a lazy fill -> another worker -> `claude -p` -> ... Each
	// link is behaving correctly; the chain is not. See descriptionWorkerEnv for why the guard
	// is inherited rather than passed.
	if WorkerProcess() {
		return nil
	}
	return &Filler{
		mgr:       mgr,
		cfg:       cfg,
		harness:   harness,
		lazyCfg:   cfg.EffectiveLazyDescriptions(harness),
		attempted: map[string]bool{},
	}
}

// NewBounded is New with the read's wait replaced by a shorter one.
//
// For a caller that has its own, tighter budget -- the guard's read proxy has five seconds for
// the whole answer, where a read has forty-five. It bounds only the WAIT: the worker it starts
// runs to its own timeout either way, so a caller that can spare almost no time still leaves the
// project warmer than it found it.
func NewBounded(mgr *topology.TopologyManager, cfg *helper.Config, harness string, wait time.Duration) *Filler {
	f := New(mgr, cfg, harness)
	if f == nil {
		return nil
	}
	if seconds := int(wait.Seconds()); seconds < f.lazyCfg.TimeoutSeconds {
		f.lazyCfg.TimeoutSeconds = seconds
	}
	return f
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

// fill hands a plan to a background worker and waits for as much of it as the read can afford.
//
// THE PARTITION IS THE WHOLE THING, and it is per resource, not per read. Every target is put
// through one compare-and-swap, and the answers split three ways:
//
//   - WON -- nobody live held it. It goes to a worker this fill spawns.
//   - LOST to a live claim -- somebody is already generating it. It is watched, not re-started.
//   - LOST to a cooling-down failure -- nobody is generating it and nobody should be, yet.
//
// A read that plans forty resources, three of which another worker already has, spawns one
// worker for the other thirty-seven and waits on all forty. Doing one OR the other is what makes
// this feature either wasteful or useless: start everything and two workers describe one resource
// and both bills are real; stand down because something was taken and resources nobody is
// working on are abandoned.
//
// Then it waits, up to timeout_seconds, and returns whether anything landed. What has not landed
// by then is rendered as undescribed -- and keeps being generated. That is the difference from
// the inline fill this replaces, which killed the provider call on its deadline and threw away
// every token already spent on it.
func (f *Filler) fill(topo *domain.Topology, targets []Target) bool {
	if len(targets) == 0 {
		return false
	}
	if !f.lazyCfg.Background {
		return f.fillInline(topo, targets)
	}
	// Resolved before anything is claimed: with no provider there is nothing to spawn, and a
	// claim taken on behalf of a worker that will never exist has to be cleaned up again. The
	// next process -- or the next read after a key appears in the environment -- still gets a
	// clean first try.
	if f.generator() == nil {
		return false
	}
	pending := f.claim(targets)
	if len(pending) == 0 {
		return false
	}

	jobID := NewJobID()
	now := time.Now()
	lease := now.Add(time.Duration(f.lazyCfg.WorkerTimeoutSeconds)*time.Second + helper.DescriptionJobLeaseSlack)

	claims := make([]helper.DescriptionJobTarget, 0, len(pending))
	for _, t := range pending {
		claims = append(claims, helper.DescriptionJobTarget{ID: t.ID, Fingerprint: t.Fingerprint})
	}
	won, watch, err := helper.ClaimDescriptionTargets(f.mgr.DbPath(), jobID, os.Getpid(),
		claims, now, lease, f.lazyCfg.MaxWorkers)
	if err != nil {
		f.release(pending)
		return false
	}

	// Nothing was won, so there is no worker to start -- but there may still be live work worth
	// waiting for, which is exactly the case where a second read renders a description the first
	// read paid for.
	if len(won) > 0 {
		if err := spawnWorker(f.mgr.DbPath(), jobID, f.harness); err != nil {
			// The claims were taken for a worker that does not exist. Releasing them is what
			// stops every later read watching a job that will never report; if the release
			// fails too, the claims were born unstarted and the next fill collects them.
			_ = helper.ReleaseDescriptionJob(f.mgr.DbPath(), jobID)
			f.release(pending)
			return false
		}
	}
	// The in-process set is not the authority here -- the claim table is -- and holding ids this
	// fill did not win would stop a LATER fill in this same process from watching them.
	f.release(unclaimed(pending, won))

	landed := f.waitForDescriptions(append(append([]string{}, won...), watch...),
		time.Duration(f.lazyCfg.TimeoutSeconds)*time.Second)
	return landed
}

// unclaimed is the targets this fill planned but does not own.
func unclaimed(pending []Target, won []string) []Target {
	owned := make(map[string]bool, len(won))
	for _, id := range won {
		owned[id] = true
	}
	var out []Target
	for _, t := range pending {
		if !owned[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// fillInline is the previous behaviour, kept behind `descriptions.lazy.background: false`.
//
// It generates inside the read and abandons whatever has not finished when the deadline passes,
// which costs the tokens already spent on the batch in flight. That is the trade it always made.
// It stays reachable because spawning is not always possible or wanted: a locked-down CI image, a
// sandbox that forbids it, and the benchmark harness, which deliberately refuses to leave a
// process running between runs.
func (f *Filler) fillInline(topo *domain.Topology, targets []Target) bool {
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

	results, settled, failed := f.run(ctx, topo, pending)
	written := f.write(pending, results)
	// Recorded AFTER the run, and only for the targets whose batch came back with an answer
	// that did not describe them: a target the model declined is exactly what the record
	// exists to hold. A target that was written is NOT recorded. It drops out of the plan on
	// its own while it has a description, and a record of it would outlive that description
	// -- a hard rebuild, or a file deleted and restored, empties it under an unchanged
	// fingerprint, and the record then suppressed the one fill that could put it back.
	//
	// A target whose batch the DEADLINE cut off is not an answer either. Recording it -- as
	// this once did, for the whole pending set -- persisted a timeout as though it were an
	// answer, and since the record only expires when the code changes, the first slow read on
	// a cold repository abandoned up to max_nodes resources for good: exactly the reads the
	// feature exists for. Those are released instead, so the next read, in this process or the
	// next, tries again.
	//
	// A batch the PROVIDER failed on (a CLI that exited non-zero, an HTTP 401/429/5xx, a
	// refused connection) says nothing about its resources either: it is the transport that
	// failed, not the model that declined. It stays claimed in this process, so one broken
	// provider costs one attempt per process rather than one per read, but it is never
	// persisted -- an expired login or a rate limit would otherwise disable the fill for those
	// resources until their code changed.
	f.recordAttempts(unwritten(settled, written))
	f.release(unsettled(pending, append(settled, failed...)))
	return len(written) > 0
}

// unwritten is the targets whose description did not land.
func unwritten(targets []Target, written map[string]bool) []Target {
	var out []Target
	for _, t := range targets {
		if !written[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// unsettled is the pending targets whose batch did not come back before the deadline. settled
// is every target whose batch did come back, answered or failed.
func unsettled(pending, settled []Target) []Target {
	done := make(map[string]bool, len(settled))
	for _, t := range settled {
		done[t.ID] = true
	}
	var out []Target
	for _, t := range pending {
		if !done[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// release returns targets to the unclaimed pool, so a later fill on this Filler may try them.
func (f *Filler) release(targets []Target) {
	if len(targets) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range targets {
		delete(f.attempted, t.ID)
	}
}

// claim takes the targets this fill is responsible for, marking them attempted so no other
// fill on this Filler picks them up -- whether or not the attempt succeeds.
//
// The failures are the point of recording them. A resource the model declines to describe
// would otherwise be re-planned by every subsequent read that names it, buying a provider call
// per read forever.
//
// TWO LEDGERS, and the second is what makes the first mean anything on the terminal surface.
// The in-process set stops two overlapping fills in ONE process from both claiming a node.
// The persisted one (helper.ReadDescriptionAttempts) stops the next PROCESS from re-trying
// what this one already failed at -- and every intercepted `cat` and `grep` is a new process,
// so without it the guard guarded nothing where it was needed most. A recorded attempt expires
// when the resource's fingerprint changes, so edited code is always tried again.
func (f *Filler) claim(targets []Target) []Target {
	recorded := f.recordedAttempts(targets)

	f.mu.Lock()
	defer f.mu.Unlock()
	pending := make([]Target, 0, len(targets))
	for _, t := range targets {
		if f.attempted[t.ID] {
			continue
		}
		if fp, seen := recorded[t.ID]; seen && fp == t.Fingerprint && t.Fingerprint != "" {
			f.attempted[t.ID] = true // do not re-consult the table for this id either
			continue
		}
		f.attempted[t.ID] = true
		pending = append(pending, t)
	}
	return pending
}

// recordedAttempts reads the persisted ledger, best-effort: an unreadable table means every
// target is unattempted, which is the behaviour this feature had before the table existed.
func (f *Filler) recordedAttempts(targets []Target) map[string]string {
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	recorded, err := helper.ReadDescriptionAttempts(f.mgr.DbPath(), ids)
	if err != nil {
		return nil
	}
	return recorded
}

// recordAttempts persists what this fill tried, so the next process does not try it again.
// Best-effort, like everything else here: a read must not fail because a bookkeeping row
// could not be written.
func (f *Filler) recordAttempts(targets []Target) {
	attempts := make(map[string]string, len(targets))
	for _, t := range targets {
		if t.ID != "" && t.Fingerprint != "" {
			attempts[t.ID] = t.Fingerprint
		}
	}
	_ = helper.RecordDescriptionAttempts(f.mgr.DbPath(), attempts)
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

// batchResult is one batch's reply. settled is true only for a batch the provider answered;
// failed is true for one the provider errored on while the deadline still stood. A batch the
// deadline cut off is neither: it is not an answer about its resources and must not be
// remembered as one.
type batchResult struct {
	targets []Target
	descs   map[string]string
	settled bool
	failed  bool
}

// run fans the batches out and collects whatever comes back, plus the targets whose batch the
// provider answered and those whose batch it failed on -- see fill for why the three outcomes
// are handled differently.
//
// "In the background, then waited on" is exactly this: the batches are goroutines so several
// completions are in flight at once, and the read blocks until they are done or the deadline
// expires. A batch that fails takes only itself down -- its siblings' descriptions are still
// written, because four descriptions the project did not have is a better answer than none.
func (f *Filler) run(ctx context.Context, topo *domain.Topology, targets []Target) (map[string]string, []Target, []Target) {
	batches := chunk(targets, f.lazyCfg.BatchSize)
	parallel := f.lazyCfg.Parallel
	if parallel > len(batches) {
		parallel = len(batches)
	}
	if parallel < 1 {
		parallel = 1
	}

	jobs := make(chan []Target)
	out := make(chan batchResult, len(batches))
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				descs, err := f.gen.Describe(ctx, f.batch(topo, batch))
				// Only a reply is an answer. A provider error while the deadline still stands
				// is a failed transport, and one raised because the deadline passed is a
				// timeout; neither says anything about the resources.
				settled := err == nil
				failed := err != nil && ctx.Err() == nil
				if err != nil {
					descs = nil
				}
				out <- batchResult{targets: batch, descs: descs, settled: settled, failed: failed}
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
	var settled, failed []Target
	take := func(r batchResult) {
		for id, desc := range r.descs {
			merged[id] = desc
		}
		if r.settled {
			settled = append(settled, r.targets...)
		}
		if r.failed {
			failed = append(failed, r.targets...)
		}
	}
	collect := func() {
		for r := range out {
			take(r)
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
			case r, ok := <-out:
				if !ok {
					return merged, settled, failed
				}
				take(r)
			default:
				return merged, settled, failed
			}
		}
	}
	return merged, settled, failed
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
	// Benchmark escape hatch: with ARACNE_DESCRIBE_FROM_COMMIT set, describe the resource as
	// it stood at that commit rather than as it stands on disk. OFF by default and meant to
	// stay off outside a harness -- see CleanSourceEnv in cleansource.go for why a normal user
	// must be described from their working tree. A miss (file created during the run, range
	// shifted past the committed bytes, cut that no longer contains the declaration) yields no
	// description at all rather than a description of the wrong code.
	if ref := cleanSourceRef(); ref != "" {
		cut, ok := cutFromCommit(ref, res.Location.Path, res.Name,
			res.Location.StartsAt, res.Location.EndsAt)
		if !ok {
			return ""
		}
		return cut
	}
	entry, err := f.mgr.Cut(res.Location)
	if err != nil || entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.Cut)
}

// write stores the descriptions and returns the ids that landed.
//
// Each write goes through TopologyManager.UpdateDescription, so an over-budget description is
// rejected and an id that no longer exists is an error -- the same two gates the
// update_description tool applies. A rejection is dropped silently: the resource stays
// undescribed, it is already in the attempted set, and the read it was for renders without it.
func (f *Filler) write(targets []Target, descriptions map[string]string) map[string]bool {
	written := map[string]bool{}
	if len(descriptions) == 0 {
		return written
	}
	kinds := make(map[string]domain.ResourceKind, len(targets))
	for _, t := range targets {
		kinds[t.ID] = t.Kind
	}
	for _, t := range targets {
		desc, ok := descriptions[t.ID]
		if !ok || strings.TrimSpace(desc) == "" {
			continue
		}
		if err := f.mgr.UpdateDescription(t.ID, kinds[t.ID], desc); err != nil {
			continue
		}
		written[t.ID] = true
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

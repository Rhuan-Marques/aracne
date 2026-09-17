package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/progress"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// The detached description worker: `arac descriptions worker`.
//
// A hidden verb, in the `arac guard` tradition -- dispatched but absent from the usage text,
// because nothing but aracne itself ever runs it. A person who wants this work done by hand runs
// `arac descriptions generate`, which is the same machinery with a terminal attached.
//
// WHAT IT IS FOR. A lazy fill claims the resources a read is about to show, spawns one of these
// and waits for whatever lands before the read's own deadline. The read then renders and exits.
// This process does not: it finishes the work and writes it to the database for the next read
// that names those resources. That is the entire point -- the tokens a read could not wait for
// are no longer thrown away.
//
// WHAT BOUNDS IT, because nobody is watching it and nothing will notice if it misbehaves:
//
//   - a context deadline at worker_timeout_seconds, which ends the run gracefully: deferred
//     settles run, the provider call is cancelled, and everything already written stays written;
//   - a watchdog underneath that, which exits the process outright if the graceful path does not
//     return -- a provider that ignores its context cannot turn this into a process that runs
//     forever;
//   - the heartbeat, which doubles as an ownership check: the moment this worker stops owning
//     its claims -- cancelled, superseded, cleared by a rebuild, or the database deleted from
//     under it -- it is doing duplicate work or work for nobody, and it stops;
//   - the lease in the claim row, which expires whatever this process does, so even an
//     impossible survivor cannot hold a resource against the rest of the project.
//
// It also settles its claims PER BATCH rather than at the end, so being killed at any moment
// costs at most the one batch in flight.

const (
	// descriptionWorkerWatchdogGrace is how long past the context deadline the watchdog waits
	// before ending the process itself.
	descriptionWorkerWatchdogGrace = 30 * time.Second
	// descriptionWorkerExitGrace bounds the settle-and-report the watchdog attempts on its way
	// out. It is deliberately short: this is best-effort bookkeeping running after the run has
	// already overshot, and an unsettled claim goes stale on its own.
	descriptionWorkerExitGrace = 2 * time.Second

	// descriptionWorkerLog is where a worker's failures go, since its stderr is /dev/null.
	descriptionWorkerLog = "descriptions.log"
	// descriptionWorkerLogMax bounds that file. Append-only logs grow, and this codebase has
	// already had one bloat incident (maxStoredErrors, db.go).
	descriptionWorkerLogMax = 1 << 20
)

// RunDescriptionsWorker is the entry point for the hidden verb.
func RunDescriptionsWorker(args []string) {
	fs := flag.NewFlagSet("descriptions-worker", flag.ExitOnError)
	jobID := fs.String("job", "", "Job id whose claimed resources this worker describes")
	dbPath := fs.String("db", "", "Absolute path to the topology database")
	harness := fs.String("harness", helper.DefaultLazyHarness, "Harness block to resolve the description model against")
	fs.Parse(args)

	os.Exit(runDescriptionsWorker(*jobID, *dbPath, *harness, os.Exit))
}

// runDescriptionsWorker is RunDescriptionsWorker over its exit, so a test can run the whole
// lifecycle in-process and still observe the watchdog firing.
func runDescriptionsWorker(jobID, dbPath, harness string, exit func(int)) int {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(dbPath) == "" {
		return 2
	}
	dbPath = helper.CanonicalPath(dbPath)

	// The targets come from the table, not from argv: it is the only copy, so the worker's idea
	// of its job and the project's idea of it cannot disagree. No rows means there is nothing to
	// do -- a duplicate or racing spawn, or a job whose claims were released while this process
	// was starting -- and the right response to that is to exit quietly, not to look for work.
	targets, err := helper.ReadDescriptionJobTargets(dbPath, jobID)
	if err != nil || len(targets) == 0 {
		return 0
	}

	// LoadConfig, not EnsureConfig: a detached background process must never write a config
	// file. If there is nothing to read, there is nothing this worker was configured to do.
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if cfg == nil {
		releaseAndLog(dbPath, jobID, "no readable config")
		return 1
	}
	descCfg := cfg.EffectiveLazyDescriptions(harness)

	manager := topology.New()
	manager.Load(dbPath)
	reg := NewScannerRegistry()

	runner, _, err := newDescriptionRunnerFn(manager, reg, cfg, descCfg)
	if err != nil || runner == nil {
		detail := "no description provider configured"
		if err != nil {
			detail = err.Error()
		}
		failAndLog(dbPath, jobID, targetIDs(targets), detail)
		return 1
	}

	timeout := time.Duration(descCfg.WorkerTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// SIGTERM must settle claims rather than orphan them: `pkill arac`, a shutting-down machine
	// and a supervisor reclaiming a container all arrive this way, and a claim abandoned without
	// a word holds its resource until the heartbeat goes stale.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	// The watchdog is the floor under the context, not an alternative to it. A provider that
	// ignores cancellation, a wedged cgo call, a filesystem that never answers -- none of them
	// can be talked out of by a context, and all of them would otherwise be a process that runs
	// forever with nobody watching.
	watchdog := time.AfterFunc(timeout+descriptionWorkerWatchdogGrace, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = helper.MarkDescriptionJobFailed(dbPath, jobID, targetIDs(targets),
				"worker exceeded its watchdog", time.Now())
			appendDescriptionWorkerLog(dbPath, fmt.Sprintf(
				"job %s: killed by watchdog after %s", jobID, timeout+descriptionWorkerWatchdogGrace))
		}()
		select {
		case <-done:
		case <-time.After(descriptionWorkerExitGrace):
		}
		// Cancelling first means the provider's process group is killed on the way out -- see
		// cliGenerator.Describe. Without it, exiting here would orphan a live `claude -p` and
		// everything it started, with no parent left to stop it.
		cancel()
		time.Sleep(descriptionWorkerExitGrace)
		exit(3)
	})
	defer watchdog.Stop()

	// The heartbeat keeps the claims alive AND asks, every tick, whether they are still ours.
	stopHeartbeat := startDescriptionHeartbeat(ctx, cancel, dbPath, jobID)
	defer stopHeartbeat()

	claimed := map[string]string{} // id -> fingerprint at claim time
	for _, t := range targets {
		claimed[t.ID] = t.Fingerprint
	}
	outstanding := map[string]bool{}
	for id := range claimed {
		outstanding[id] = true
	}

	var bar progress.Reporter // inert: nothing is watching a detached worker's progress
	runErr := runDescriptionGeneration(ctx, manager, runner,
		claimScopedPending(manager, dbPath, cfg, claimed),
		descCfg.BatchSize, descCfg.Parallel, descCfg.MaxRetries,
		cfg.Descriptions.StyleExemplars, false,
		func(batch []descriptionResource, batchErr error) {
			settleDescriptionBatch(dbPath, jobID, batch, batchErr, claimed, outstanding)
		}, &bar)

	// Whatever is still claimed when the run ends is this worker's to account for: a resource
	// left `running` would hold its place until the heartbeat went stale, and every read in the
	// meantime would watch a job that is no longer generating anything.
	remaining := sortedKeys(outstanding)
	if len(remaining) > 0 {
		if runErr != nil {
			failAndLog(dbPath, jobID, remaining, runErr.Error())
		} else {
			_ = helper.SettleDescriptionJobTargets(dbPath, jobID, remaining)
		}
	}
	if runErr != nil && ctx.Err() == nil {
		return 1
	}
	return 0
}

// startDescriptionHeartbeat runs the heartbeat until the context ends, and cancels the context
// the moment this worker stops owning its claims.
//
// Losing ownership means one of four things, and every one of them says stop: the job was
// cancelled through the table, its lease expired and someone re-claimed the resources, a hard
// rebuild cleared the ledger, or the database is gone because the project was deleted. In each
// case the work in flight is duplicate work or work for nobody. Consecutive ERRORS count too --
// by the fourth the claims have gone stale anyway, so somebody else may already own them.
func startDescriptionHeartbeat(ctx context.Context, cancel context.CancelFunc, dbPath, jobID string) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(helper.DescriptionJobHeartbeatInterval)
		defer ticker.Stop()
		// Beat once immediately. It is what marks the claim as STARTED, which is how a worker
		// that died on the way up is told apart from one that ran and stopped -- the first gets
		// a cooldown instead of an instant re-spawn on the next read.
		failures := 0
		beat := func() {
			owned, err := helper.HeartbeatDescriptionJob(dbPath, jobID, time.Now())
			if err != nil {
				failures++
				if failures >= 4 {
					cancel()
				}
				return
			}
			failures = 0
			if owned == 0 {
				cancel()
			}
		}
		beat()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				beat()
			}
		}
	}()
	return func() { <-done }
}

// claimScopedPending is the worker's idea of "what is left to do": only the resources it
// claimed, and only while they still describe the code they were claimed for.
//
// Only its own, because it has no mandate over the rest of the project and another worker may be
// describing those at this moment -- the sweep's whole-repository re-list would have this one
// wander into work it never claimed. Only while the fingerprint matches, because a resource
// edited mid-run would otherwise be described from a cut that no longer exists; dropping it here
// leaves it for the next read, whose claim will carry the new fingerprint.
func claimScopedPending(manager *topology.TopologyManager, dbPath string, cfg *helper.Config,
	claimed map[string]string) func() ([]descriptionResource, error) {
	targetSet := helper.DescribeTargetSet(cfg.Descriptions.Kinds)
	filter := cfg.EffectiveContextFilter()
	includeNotVisible := cfg.Descriptions.IncludeNotVisible
	return func() ([]descriptionResource, error) {
		resources, err := helper.ReadResourcesByIDs(dbPath, sortedKeys(toSet(claimed)))
		if err != nil {
			return nil, err
		}
		out := make([]descriptionResource, 0, len(resources))
		for id, res := range resources {
			if strings.TrimSpace(res.Description) != "" {
				continue
			}
			if fp, ok := claimed[id]; ok && fp != "" &&
				helper.DescriptionAttemptFingerprint(res) != fp {
				continue
			}
			if !helper.ShouldDescribe(res, targetSet, filter, includeNotVisible) {
				continue
			}
			out = append(out, descriptionResource{ID: id, Name: res.Name, Kind: res.Kind})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Kind != out[j].Kind {
				return out[i].Kind < out[j].Kind
			}
			return out[i].ID < out[j].ID
		})
		return out, nil
	}
}

// settleDescriptionBatch releases the claims a finished batch accounted for.
//
// Per batch, not per run, and that is what bounds the cost of being killed: everything already
// written is already settled, so the worst a `kill -9` or a watchdog can cost is the one batch
// in flight. A batch the PROVIDER failed on keeps its claims -- the transport failed, the
// resources are still this worker's to retry, and the wave loop will come back to them.
func settleDescriptionBatch(dbPath, jobID string, batch []descriptionResource, batchErr error,
	claimed map[string]string, outstanding map[string]bool) {
	// errNoUsableDescriptions is not a failure of the transport: the provider answered and
	// described nothing. That is a verdict about these resources, so it settles like a normal
	// batch and lands in the attempt ledger below. Any OTHER error is the transport, which says
	// nothing about the resources -- their claims stay, and the wave loop comes back to them.
	if batchErr != nil && !errors.Is(batchErr, errNoUsableDescriptions) {
		return
	}
	ids := make([]string, 0, len(batch))
	for _, res := range batch {
		ids = append(ids, res.ID)
	}
	status, err := helper.ReadDescriptionJobStatus(dbPath, ids)
	if err != nil {
		return
	}

	declined := map[string]string{}
	settle := make([]string, 0, len(ids))
	for _, id := range ids {
		st, ok := status[id]
		if !ok {
			// The resource left the graph while this batch was running. Nothing to describe
			// and nothing to remember about it.
			settle = append(settle, id)
			delete(outstanding, id)
			continue
		}
		if strings.TrimSpace(st.Description) == "" {
			// The provider answered and this resource got nothing: the model declined it. That
			// IS an answer about the resource, so it goes in the attempt ledger -- under the
			// fingerprint it has NOW, so the ledger's "expires when the code changes" rule
			// stays true even if the code moved while this batch ran.
			if fp := claimed[id]; fp != "" {
				declined[id] = fp
			}
		}
		settle = append(settle, id)
		delete(outstanding, id)
		// Out of the worker's own pending list too. Without this a declined resource is
		// re-listed on the next wave, re-batched and re-declined until it burns max_retries --
		// paying for the same refusal three times.
		delete(claimed, id)
	}
	if len(declined) > 0 {
		_ = helper.RecordDescriptionAttempts(dbPath, declined)
	}
	_ = helper.SettleDescriptionJobTargets(dbPath, jobID, settle)
}

// failAndLog records why these resources were given up on, in the one place a detached process
// can say anything at all.
func failAndLog(dbPath, jobID string, ids []string, detail string) {
	_ = helper.MarkDescriptionJobFailed(dbPath, jobID, ids, detail, time.Now())
	appendDescriptionWorkerLog(dbPath, fmt.Sprintf("job %s: %d resource(s) failed: %s",
		jobID, len(ids), detail))
}

// releaseAndLog drops every claim a job holds, for a worker that cannot start at all.
func releaseAndLog(dbPath, jobID, detail string) {
	_ = helper.ReleaseDescriptionJob(dbPath, jobID)
	appendDescriptionWorkerLog(dbPath, fmt.Sprintf("job %s: %s", jobID, detail))
}

// appendDescriptionWorkerLog writes one line to .aracne/descriptions.log.
//
// One O_APPEND write of one short line, the shape guard_log.go uses, so concurrent workers do
// not interleave. The file is truncated when it grows past descriptionWorkerLogMax: an
// append-only log with nothing trimming it is a slow leak, and this codebase has met that
// before.
func appendDescriptionWorkerLog(dbPath, line string) {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		return
	}
	path := filepath.Join(dir, descriptionWorkerLog)
	if info, err := os.Stat(path); err == nil && info.Size() > descriptionWorkerLogMax {
		trimDescriptionWorkerLog(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), line))
}

// trimDescriptionWorkerLog keeps the tail, which is the half worth having: the most recent
// failures are the ones somebody is trying to explain.
func trimDescriptionWorkerLog(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	keep := raw
	if len(keep) > descriptionWorkerLogMax/2 {
		keep = keep[len(keep)-descriptionWorkerLogMax/2:]
		if i := strings.IndexByte(string(keep), '\n'); i >= 0 {
			keep = keep[i+1:]
		}
	}
	_ = helper.AtomicWriteFile(path, keep, 0o644)
}

func targetIDs(targets []helper.DescriptionJobTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.ID)
	}
	sort.Strings(out)
	return out
}

func toSet(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

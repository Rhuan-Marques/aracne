package helper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The pending queue for transient warnings: warnings that must reach the agent exactly once
// and are never written to the topology.
//
// WHY A QUEUE AND NOT A RETURN VALUE. An Unverified signature verdict withdraws its stored row
// and emits a transient in its place (see ReconcileSignatureWarnings) -- a report about a call
// whose argument the scanner could not type. Because nothing stores it, the only copy used to
// live in the return value of whichever scan produced it, and that makes the report a race
// nobody wins: `arac scanner run`, a pre-tool scan, an `arac scan` in another terminal, the
// OpenCode sync plugin -- each of them re-parses the file, receives the transient and drops it,
// leaving the post-tool hook with nothing to report. The agent's one channel for "you retyped
// a parameter and these callers may not fit" is then silent precisely when something else is
// keeping the topology fresh, which is the same failure the reported-warnings ledger exists to
// fix for STORED warnings (see internal/cli/guard_warnstate.go).
//
// So a transient is queued the moment it is produced and removed only when it is REPORTED.
// Whoever discovers it, the agent hears about it once, on the next post-tool hook -- the same
// contract the ledger gives the warnings table, for the one kind of warning no table can hold.
//
// THIS IS NOT THE TOPOLOGY. The queue is derived session state about a checkout, like the
// ledger beside it, and deliberately not a row in the warnings table: a stored transient would
// be carried forward, re-judged and re-reported on every later scan, which is exactly what
// "transient" exists to prevent.

// pendingTransientsFile is where the queue lives -- next to the topology and the ledger,
// because it is per-checkout state about this working copy.
func pendingTransientsFile(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "pending-transients.json")
}

// maxPendingTransients bounds the queue.
//
// Growth is already bounded by editing: a transient is raised only when a signature CHANGES
// between two parses, so repeated scans of unchanged code add nothing. The cap is for the case
// nothing else covers -- a long stretch of signature churn with no post-tool hook to drain it,
// as in a scripted refactor -- where the alternative is a file that grows without limit and a
// report nobody can read. The OLDEST entries go: the newest are the ones describing the code
// the agent is working on now.
const maxPendingTransients = 200

// pendingLockWait and pendingLockStale mirror the ledger's limits; the locked section is one
// small read and one small write, so either is reached only when something is already wrong.
const (
	pendingLockWait  = 3 * time.Second
	pendingLockStale = 15 * time.Second
)

// withPendingLock runs fn while holding the queue's lock.
//
// An O_EXCL lock file rather than flock, for the reason the ledger uses one: it means the same
// thing on every platform. Best-effort throughout -- queueing runs inside a scan and draining
// runs inside a hook, and neither may fail or hang over bookkeeping. Deliberately NOT the
// topology lock: queueing happens while that one is already held.
func withPendingLock(dbPath string, fn func()) {
	lockPath := pendingTransientsFile(dbPath) + ".lock"
	deadline := time.Now().Add(pendingLockWait)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			defer os.Remove(lockPath)
			fn()
			return
		}
		if !os.IsExist(err) {
			fn() // the directory will not take a lock file at all
			return
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > pendingLockStale {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			fn()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readPendingTransients returns the queued warnings, or nothing at all when the file is
// absent or unreadable. A torn file costs the reports it held; it must never cost the scan or
// the hook that found it.
func readPendingTransients(dbPath string) []domain.TopologyWarning {
	raw, err := os.ReadFile(pendingTransientsFile(dbPath))
	if err != nil {
		return nil
	}
	var queued []domain.TopologyWarning
	if err := json.Unmarshal(raw, &queued); err != nil {
		return nil
	}
	return queued
}

func writePendingTransients(dbPath string, queued []domain.TopologyWarning) {
	if len(queued) == 0 {
		_ = os.Remove(pendingTransientsFile(dbPath))
		return
	}
	raw, err := json.Marshal(queued)
	if err != nil {
		return
	}
	_ = AtomicWriteFile(pendingTransientsFile(dbPath), raw, 0o644)
}

// QueueTransients records the transient warnings in a scan's output so a later report can find
// them, and ignores everything else in the list: a stored warning is in the table, where the
// ledger already sees it however it got there.
//
// Called on the way out of every manager entry point that returns warnings, rather than at
// each of the places that MAKE a transient. The bug this queue fixes was a producer whose
// output nothing read, and spreading the call across the three reconcile sites would leave the
// next one added to the same fate.
//
// DEDUPLICATED BY ID, keeping the newest raise. Two scans can raise the same transient about
// the same call -- reporting both spends the agent's attention on one finding twice -- and when
// the call changed between them, the later one is the one that is true. A transient already
// REPORTED in the same state is not queued at all (see the reported-transients ledger below).
func QueueTransients(dbPath string, warnings []domain.TopologyWarning) {
	if dbPath == "" {
		return
	}
	fresh := make([]domain.TopologyWarning, 0, len(warnings))
	for _, w := range warnings {
		if w.Transient && w.ID != "" {
			fresh = append(fresh, w)
		}
	}
	if len(fresh) == 0 {
		return
	}
	withPendingLock(dbPath, func() {
		queued := readPendingTransients(dbPath)
		reported := readReportedTransients(dbPath)
		at := make(map[string]int, len(queued))
		for i, w := range queued {
			at[w.ID] = i
		}
		dropped := map[string]bool{}
		for _, w := range fresh {
			if _, shown := reported[reportedTransientKey(w)]; shown {
				// Already shown in this exact state: a re-raise, not a new finding. And the code
				// is back in a state the agent has seen, so a copy queued from a state in between
				// describes code that no longer exists -- `git stash` raises one against the
				// reverted callee, `git stash pop` puts the reported one back.
				if _, have := at[w.ID]; have {
					dropped[w.ID] = true
				}
				continue
			}
			delete(dropped, w.ID)
			if i, have := at[w.ID]; have {
				// The newer raise describes the code as it is now; the report and the ledger
				// entry it leaves must name that state, not the one it replaced.
				queued[i] = w
				continue
			}
			at[w.ID] = len(queued)
			queued = append(queued, w)
		}
		if len(dropped) > 0 {
			kept := queued[:0]
			for _, w := range queued {
				if !dropped[w.ID] {
					kept = append(kept, w)
				}
			}
			queued = kept
		}
		if len(queued) > maxPendingTransients {
			queued = queued[len(queued)-maxPendingTransients:]
		}
		writePendingTransients(dbPath, queued)
	})
}

// DrainTransients returns the queued warnings and empties the queue, so the caller about to
// report them is the only one that will, and records them as reported.
//
// The read and the clear are one locked step. Two post-tool hooks can fire for a single native
// edit -- the guard's drift check and the edit-sync plugin -- and if both could read before
// either cleared, both would report the same finding.
//
// Anything queued that was already reported in the same state is dropped here too, not only at
// QueueTransients: the queue can hold a copy raised before its twin was shown.
func DrainTransients(dbPath string) []domain.TopologyWarning {
	if dbPath == "" {
		return nil
	}
	var drained []domain.TopologyWarning
	withPendingLock(dbPath, func() {
		queued := readPendingTransients(dbPath)
		if len(queued) == 0 {
			return
		}
		writePendingTransients(dbPath, nil)
		reported := readReportedTransients(dbPath)
		for _, w := range queued {
			if _, shown := reported[reportedTransientKey(w)]; !shown {
				drained = append(drained, w)
			}
		}
		markReportedTransients(dbPath, reported, drained)
	})
	domain.SortWarnings(drained)
	return drained
}

// RequeueTransients puts back transients that were drained but NOT shown -- a report page
// the byte budget cut short -- and forgets that they were reported, which DrainTransients
// recorded on the assumption that everything it returned would be.
func RequeueTransients(dbPath string, unshown []domain.TopologyWarning) {
	if dbPath == "" || len(unshown) == 0 {
		return
	}
	withPendingLock(dbPath, func() {
		reported := readReportedTransients(dbPath)
		changed := false
		for _, w := range unshown {
			key := reportedTransientKey(w)
			if _, ok := reported[key]; ok {
				delete(reported, key)
				changed = true
			}
		}
		if changed {
			writeReportedTransients(dbPath, reported)
		}
	})
	QueueTransients(dbPath, unshown)
}

// PeekTransients returns the queued warnings WITHOUT draining them, for a surface that pages
// through them -- `arac warnings list --read` and the warnings_list tool's `read` -- and will
// drain exactly the ones it ends up showing (RemoveTransients). Draining up front would lose
// every transient past the page.
func PeekTransients(dbPath string) []domain.TopologyWarning {
	if dbPath == "" {
		return nil
	}
	var queued []domain.TopologyWarning
	withPendingLock(dbPath, func() { queued = readPendingTransients(dbPath) })
	domain.SortWarnings(queued)
	return queued
}

// RemoveTransients drops the given warnings from the queue: they have now been shown, and a
// transient is reported once. Anything not named stays for the next report or page.
func RemoveTransients(dbPath string, shown []domain.TopologyWarning) {
	gone := map[string]bool{}
	for _, w := range shown {
		if w.Transient && w.ID != "" {
			gone[w.ID] = true
		}
	}
	if dbPath == "" || len(gone) == 0 {
		return
	}
	withPendingLock(dbPath, func() {
		queued := readPendingTransients(dbPath)
		kept := queued[:0]
		for _, w := range queued {
			if !gone[w.ID] {
				kept = append(kept, w)
			}
		}
		writePendingTransients(dbPath, kept)
		var marked []domain.TopologyWarning
		for _, w := range shown {
			if w.Transient && w.ID != "" {
				marked = append(marked, w)
			}
		}
		markReportedTransients(dbPath, readReportedTransients(dbPath), marked)
	})
}

// The reported-transients ledger: which transients the agent has already been shown, and in
// what state.
//
// WHY THE QUEUE ALONE WAS NOT ENOUGH. The queue makes a transient reach the agent once per
// RAISE, and a raise is not a finding. Any command that takes the code away and puts it back
// -- `git stash && go test ./...; git stash pop`, a checkout of a file and back, a formatter
// rewriting a tree -- makes the next scan see every signature change happen again, and every
// unverified caller was raised, queued and reported a second time: on a measured run, thirteen
// reports the agent had read a minute earlier, ~20 KB of them, paged across two tool calls.
//
// So a transient is keyed by its id AND its state (helper.TransientState: the callee's
// signature and the caller's calls and body). The same pair in the same shape is not reported
// again; change either endpoint and the key changes with it, so the new question is.
//
// THE CALLER HALF FOLLOWS THE AGENT'S OWN EDITS (RefreshTransientCallers). A caller edited after
// the report, while its callee still stands where the report left it, is the agent acting on
// that report -- and a caller-only edit never raises anything, so without the refresh the entry
// kept the caller as it was when the warning fired. The next full re-judgement then found a
// "new" caller and repeated the report. Measured on the same run after the first fix: the agent
// retyped five handlers, then updated their caller, then ran `git stash && go test; git stash
// pop`, and all five came back.
//
// Stored warnings do not need this: the guard's ledger diffs the warnings table, and a row that
// leaves and comes back between two reports is never seen to leave.

// reportedTransientsFile sits beside the queue, and is guarded by the same lock.
func reportedTransientsFile(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "reported-transients.json")
}

// Bounds on the ledger. It only grows -- a key is never discharged, because a transient has no
// discharge event -- so a long session would otherwise keep every state of every pair it ever
// reported. Past maxReportedTransients the OLDEST reports go, down to reportedTransientsKeep, so
// a ledger at the limit is not rewritten and trimmed on every single report. Forgetting an old
// key costs at most one repeated report, if that exact state ever comes back.
const (
	maxReportedTransients  = 2000
	reportedTransientsKeep = 1500
)

// reportedTransient is one ledger entry: when it was reported, and the endpoints that let
// RefreshTransientCallers recompute its state.
type reportedTransient struct {
	At     int64  `json:"at"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// reportedTransientKey is what "already shown" is judged by.
func reportedTransientKey(w domain.TopologyWarning) string {
	return w.ID + "#" + w.State
}

// readReportedTransients returns key -> entry. Absent or unreadable reads as empty -- including a
// ledger in an older format: the failure mode is a repeated report, never a lost one.
func readReportedTransients(dbPath string) map[string]reportedTransient {
	out := map[string]reportedTransient{}
	raw, err := os.ReadFile(reportedTransientsFile(dbPath))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]reportedTransient{}
	}
	return out
}

func writeReportedTransients(dbPath string, reported map[string]reportedTransient) {
	if len(reported) == 0 {
		_ = os.Remove(reportedTransientsFile(dbPath))
		return
	}
	raw, err := json.Marshal(reported)
	if err != nil {
		return
	}
	_ = AtomicWriteFile(reportedTransientsFile(dbPath), raw, 0o644)
}

// markReportedTransients records ws as reported now and writes the ledger, pruned. Caller holds
// the pending lock.
func markReportedTransients(dbPath string, reported map[string]reportedTransient, ws []domain.TopologyWarning) {
	if len(ws) == 0 {
		return
	}
	now := time.Now().UnixNano()
	for _, w := range ws {
		reported[reportedTransientKey(w)] = reportedTransient{At: now, Source: w.SourceID, Target: w.TargetID}
	}
	pruneReportedTransients(reported)
	writeReportedTransients(dbPath, reported)
}

// pruneReportedTransients drops the oldest entries once the ledger passes its cap. Ties on the
// timestamp (one report marks many keys at once) break by key, so the result is deterministic.
func pruneReportedTransients(reported map[string]reportedTransient) {
	if len(reported) <= maxReportedTransients {
		return
	}
	keys := make([]string, 0, len(reported))
	for k := range reported {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if reported[keys[i]].At != reported[keys[j]].At {
			return reported[keys[i]].At < reported[keys[j]].At
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys[:len(keys)-reportedTransientsKeep] {
		delete(reported, k)
	}
}

// RefreshTransientCallers brings the caller half of every reported and queued transient up to
// the caller as the topology now holds it -- but only where the callee still stands in the state
// the entry records. That is the agent editing a caller in answer to a report (or before one it
// has not drained yet), which acknowledges the new caller; the report is not news again.
//
// Where the CALLEE moved, nothing is touched: that is what makes the next raise a new question,
// and it is also what keeps a revert from being absorbed -- `git stash` takes the callee back
// with the caller, so the stashed state never overwrites what the agent was shown.
//
// Called by every scan on its way out, BEFORE QueueTransients, so a transient raised by the same
// scan is compared against callers that already reflect the edits that preceded it. Best-effort:
// a database it cannot read leaves every entry as it was.
func RefreshTransientCallers(dbPath string) {
	if dbPath == "" {
		return
	}
	withPendingLock(dbPath, func() {
		reported := readReportedTransients(dbPath)
		queued := readPendingTransients(dbPath)
		if len(reported) == 0 && len(queued) == 0 {
			return
		}
		idSet := map[string]bool{}
		for _, e := range reported {
			idSet[e.Source], idSet[e.Target] = true, true
		}
		for _, w := range queued {
			idSet[w.SourceID], idSet[w.TargetID] = true, true
		}
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			if id != "" {
				ids = append(ids, id)
			}
		}
		resources, err := ReadResourcesByIDs(dbPath, ids)
		if err != nil {
			return
		}
		// refreshed returns the state with its caller half recomputed, when the callee half still
		// matches what the state recorded.
		refreshed := func(w domain.TopologyWarning) (string, bool) {
			callee, caller, ok := strings.Cut(w.State, ".")
			if !ok || transientCalleeState(w, resources) != callee {
				return "", false
			}
			now := transientCallerState(w, resources)
			if now == caller {
				return "", false
			}
			return callee + "." + now, true
		}

		// Only the LATEST report of each pair is what the agent last saw, so only it follows the
		// caller. An older entry whose callee half happens to match again -- the callee retyped back
		// to a shape reported earlier, against a caller that has changed since -- is a new question,
		// and refreshing it would absorb the report that asks it.
		latest := map[string]string{}
		for key, e := range reported {
			id, _, ok := strings.Cut(key, "#")
			if !ok {
				continue
			}
			if cur, have := latest[id]; !have || e.At > reported[cur].At || (e.At == reported[cur].At && key > cur) {
				latest[id] = key
			}
		}
		ledgerChanged := false
		for key, e := range reported {
			id, state, ok := strings.Cut(key, "#")
			if !ok || latest[id] != key {
				continue
			}
			w := domain.TopologyWarning{ID: id, SourceID: e.Source, TargetID: e.Target, State: state}
			next, ok := refreshed(w)
			if !ok {
				continue
			}
			delete(reported, key)
			w.State = next
			reported[reportedTransientKey(w)] = e
			ledgerChanged = true
		}
		if ledgerChanged {
			writeReportedTransients(dbPath, reported)
		}

		queueChanged := false
		for i, w := range queued {
			if next, ok := refreshed(w); ok {
				queued[i].State = next
				queueChanged = true
			}
		}
		if queueChanged {
			writePendingTransients(dbPath, queued)
		}
	})
}

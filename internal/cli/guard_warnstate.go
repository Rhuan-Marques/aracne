package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The guard reports a topology warning by what is NEW IN THE TABLE, not by what its own scan
// happened to produce.
//
// WHY THAT DISTINCTION IS THE WHOLE FEATURE. driftCheck used to return the warnings from the
// IncrementalScan it ran itself. That works only when nothing else is keeping the topology
// fresh -- and the moment anything is, the scan finds the file already indexed, produces no
// warnings, and the model is told nothing. Measured on a real fixture, editing a function with
// eleven cross-file callers:
//
//	no background scanner        11 signature warnings reported
//	`arac scanner run` beside it  0 warnings reported
//
// The warnings existed in both cases and were correct in both cases; in the second they were
// discovered by the watcher a second earlier, so the hook's own scan had nothing left to find.
// A project that turns on the watcher to move freshness off the read path was silently turning
// off its warning channel, which is the one capability with no substitute.
//
// So the guard keeps a small record of which warning IDs the model has already been shown, and
// reports the difference. Whoever discovers a warning -- the watcher, the pre-tool scan, the
// drift scan, an `arac scan` in another terminal -- the model hears about it exactly once.

// warnStateFile is where the already-reported set lives. Inside .aracne because it is derived
// state about this checkout, and per-checkout because that is the scope of a session: a fresh
// clone should be told about the warnings it starts with the first time it writes.
func warnStateFile(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "guard-reported-warnings.json")
}

// readReportedWarnings returns the IDs already shown to the model, and whether a record
// existed at all. The two are different: no record means "this session has shown nothing yet",
// which is what suppresses a first-write dump of every pre-existing warning.
//
// A file that is THERE and unreadable counts as seeded, with an unknown set. That asymmetry is
// deliberate and it is the safe direction: reporting a warning twice costs a few lines, while
// treating a torn or unparseable record as "never seeded" makes seedReportedWarnings write
// every warning standing at that moment in as already-delivered -- permanently suppressing the
// one channel the design says has no substitute.
func readReportedWarnings(dbPath string) (map[string]bool, bool) {
	raw, err := os.ReadFile(warnStateFile(dbPath))
	if err != nil {
		return map[string]bool{}, false
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return map[string]bool{}, true
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, true
}

// writeReportedWarnings records the set, best-effort. A hook must not fail a tool call because
// it could not write a bookkeeping file.
//
// ATOMIC, and sorted. os.WriteFile truncates first and writes second, and hooks for concurrent
// tool calls are separate processes -- so an interrupted write left a file that parses as
// nothing, which readReportedWarnings used to read as "never seeded". Sorted because the set
// comes from a map and a stable file is one a person can diff.
func writeReportedWarnings(dbPath string, ids map[string]bool) {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	_ = helper.AtomicWriteFile(warnStateFile(dbPath), raw, 0o644)
}

// currentWarningIDs reads the whole warnings table as an id set.
func currentWarningIDs(dbPath string) (map[string]bool, []domain.TopologyWarning, bool) {
	warnings, err := helper.ReadAllWarnings(dbPath)
	if err != nil {
		return nil, nil, false
	}
	ids := make(map[string]bool, len(warnings))
	list := make([]domain.TopologyWarning, 0, len(warnings))
	for id, w := range warnings {
		ids[id] = true
		list = append(list, w)
	}
	return ids, list, true
}

// How long a hook waits for another hook's turn on the ledger, and how old a lock must be before
// it is taken to belong to a process that died holding it. The locked section is one table read
// and one small file write, so either limit is reached only when something is already wrong --
// and then the ledger is updated unlocked, which at worst repeats a warning.
const (
	warnStateLockWait  = 3 * time.Second
	warnStateLockStale = 15 * time.Second
)

// withWarnStateLock runs fn while holding the ledger's lock.
//
// Hooks for parallel tool calls are separate processes, and each one reads the ledger, diffs it
// against the warnings table and writes it back. Two of them interleaving that read-modify-write
// both read the ledger before either wrote it, and both reported the same warning. The table read
// is inside the lock too: a hook that read an older table could otherwise write back a ledger
// missing a warning another hook had just reported, and it would be reported again.
//
// An O_EXCL lock file rather than flock, because it is the one primitive that means the same on
// every platform the guard runs on. It is best-effort by design: a hook must never fail, or hang,
// a tool call over bookkeeping.
func withWarnStateLock(dbPath string, fn func()) {
	lockPath := warnStateFile(dbPath) + ".lock"
	deadline := time.Now().Add(warnStateLockWait)
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
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > warnStateLockStale {
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

// seedReportedWarnings records the current warnings as already-seen WITHOUT reporting them.
//
// Called before a tool runs, so the post-tool comparison is against the state the tool found
// rather than against nothing. Without it the first shell write of a session would dump every
// warning the repository already had -- none of which the model caused, all of which it would
// then learn to skim past.
func seedReportedWarnings(dbPath string) {
	withWarnStateLock(dbPath, func() { seedReportedWarningsLocked(dbPath) })
}

func seedReportedWarningsLocked(dbPath string) {
	ids, _, ok := currentWarningIDs(dbPath)
	if !ok {
		return
	}
	seen, seeded := readReportedWarnings(dbPath)
	if !seeded {
		writeReportedWarnings(dbPath, ids)
		return
	}
	// PRUNED to what the table still holds, every time.
	//
	// unreportedWarnings replaces the record wholesale, which is what is supposed to make a
	// warning that was fixed and reintroduced count as new again -- but it only runs from
	// driftCheck, so an id that left the table and came back between two shell writes was
	// still in the record when it returned and was filtered out silently. Pruning here, on a
	// pass that already reads the table before every tool call, gives that rule the
	// opportunity it was missing: the moment a warning is gone, so is its record.
	pruned := make(map[string]bool, len(seen))
	changed := false
	for id := range seen {
		if ids[id] {
			pruned[id] = true
			continue
		}
		changed = true
	}
	if changed {
		writeReportedWarnings(dbPath, pruned)
	}
}

// unreportedWarnings returns the warnings the model has not been shown, and marks them shown.
//
// A warning that was reported, then fixed, then reintroduced counts as new again: the stored
// set is replaced by the CURRENT one rather than accumulated, so an id that left the table and
// came back is absent from the record when it returns. That is the behaviour an agent needs --
// the second break is as worth knowing about as the first.
func unreportedWarnings(dbPath string) (fresh []domain.TopologyWarning) {
	withWarnStateLock(dbPath, func() { fresh = unreportedWarningsLocked(dbPath) })
	return fresh
}

func unreportedWarningsLocked(dbPath string) []domain.TopologyWarning {
	ids, list, ok := currentWarningIDs(dbPath)
	if !ok {
		return nil
	}
	seen, _ := readReportedWarnings(dbPath)
	var fresh []domain.TopologyWarning
	for _, w := range list {
		if !seen[w.ID] {
			fresh = append(fresh, w)
		}
	}
	writeReportedWarnings(dbPath, ids)
	// AND THE QUEUED TRANSIENTS, which no table can hold.
	//
	// An Unverified signature verdict withdraws its stored row and emits a transient in its
	// place, so the diff above is structurally blind to it: retype a parameter and every
	// caller passing a local was judged, withdrawn, and never mentioned. Those reports are
	// queued as they are raised and drained HERE, at the report -- not by whichever scan
	// happened to raise them. See helper.QueueTransients for why that distinction is the
	// whole mechanism.
	//
	// Draining is destructive, which is why it belongs on this function and not beside it:
	// every caller of unreportedWarnings is about to show the agent what it returns.
	return append(fresh, helper.DrainTransients(dbPath)...)
}

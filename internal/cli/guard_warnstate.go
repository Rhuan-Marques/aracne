package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
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
func readReportedWarnings(dbPath string) (map[string]bool, bool) {
	raw, err := os.ReadFile(warnStateFile(dbPath))
	if err != nil {
		return map[string]bool{}, false
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return map[string]bool{}, false
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, true
}

// writeReportedWarnings records the set, best-effort. A hook must not fail a tool call because
// it could not write a bookkeeping file.
func writeReportedWarnings(dbPath string, ids map[string]bool) {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	_ = os.WriteFile(warnStateFile(dbPath), raw, 0o644)
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

// seedReportedWarnings records the current warnings as already-seen WITHOUT reporting them.
//
// Called before a tool runs, so the post-tool comparison is against the state the tool found
// rather than against nothing. Without it the first shell write of a session would dump every
// warning the repository already had -- none of which the model caused, all of which it would
// then learn to skim past.
func seedReportedWarnings(dbPath string) {
	if _, seeded := readReportedWarnings(dbPath); seeded {
		return
	}
	if ids, _, ok := currentWarningIDs(dbPath); ok {
		writeReportedWarnings(dbPath, ids)
	}
}

// unreportedWarnings returns the warnings the model has not been shown, and marks them shown.
//
// A warning that was reported, then fixed, then reintroduced counts as new again: the stored
// set is replaced by the CURRENT one rather than accumulated, so an id that left the table and
// came back is absent from the record when it returns. That is the behaviour an agent needs --
// the second break is as worth knowing about as the first.
func unreportedWarnings(dbPath string) []domain.TopologyWarning {
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
	return fresh
}

package helper

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The reported-transients ledger: a transient is reported once per STATE of its two endpoints.

func stateTransient(id, state string) domain.TopologyWarning {
	return domain.TopologyWarning{
		ID: id, SourceID: "pkg.Target", TargetID: "pkg.Caller",
		Kind: domain.WarnSignatureChanged, Transient: true, State: state,
		Message: "Target changed signature, check if caller still supports it.",
	}
}

func ledgerDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "topology.db")
}

func drainedIDs(db string) []string {
	var out []string
	for _, w := range DrainTransients(db) {
		out = append(out, w.ID+"#"+w.State)
	}
	return out
}

// The bug this ledger exists for: the same finding raised again over the same code is not news.
func TestAReportedTransientRaisedAgainInTheSameStateIsNotReported(t *testing.T) {
	db := ledgerDB(t)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "A")})
	if got := drainedIDs(db); len(got) != 1 {
		t.Fatalf("first report: %v, want one", got)
	}
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "A")})
	if got := drainedIDs(db); len(got) != 0 {
		t.Errorf("a re-raise in the reported state was reported again: %v", got)
	}
}

// Changing either endpoint is a new question, and the agent must hear it.
func TestATransientInANewStateIsReportedAgain(t *testing.T) {
	db := ledgerDB(t)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "A")})
	DrainTransients(db)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "B")})
	if got := drainedIDs(db); len(got) != 1 || got[0] != "t-1#B" {
		t.Errorf("the new state was not reported: %v", got)
	}
}

// Reverting the code and restoring it inside one command: the revert raises the pair in an
// intermediate state, the restore raises it in the state already reported. Neither reaches the
// agent -- the intermediate one describes code that is gone.
func TestStashAndPopReportsNothing(t *testing.T) {
	db := ledgerDB(t)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "edited")})
	DrainTransients(db)

	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "reverted")}) // git stash
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "edited")})   // git stash pop
	if got := drainedIDs(db); len(got) != 0 {
		t.Errorf("stash/pop re-reported: %v", got)
	}
}

// A revert that STAYS is a real new state, and is reported.
func TestARevertThatStaysIsReported(t *testing.T) {
	db := ledgerDB(t)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "edited")})
	DrainTransients(db)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "reverted")})
	if got := drainedIDs(db); len(got) != 1 || got[0] != "t-1#reverted" {
		t.Errorf("drained %v, want the reverted state", got)
	}
}

// Two raises before any report: the newer one is what the code is now.
func TestTheNewestRaiseReplacesTheQueuedOne(t *testing.T) {
	db := ledgerDB(t)
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "A")})
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "B")})
	if got := drainedIDs(db); len(got) != 1 || got[0] != "t-1#B" {
		t.Errorf("drained %v, want only the newest state", got)
	}
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-1", "B")})
	if got := drainedIDs(db); len(got) != 0 {
		t.Errorf("the newest state was recorded wrong: %v", got)
	}
}

// A report page that drained a transient but did not show it gives it back, reportable.
func TestARequeuedTransientIsNotCountedAsReported(t *testing.T) {
	db := ledgerDB(t)
	w := stateTransient("t-1", "A")
	QueueTransients(db, []domain.TopologyWarning{w})
	DrainTransients(db)
	RequeueTransients(db, []domain.TopologyWarning{w})
	if got := drainedIDs(db); len(got) != 1 {
		t.Errorf("a paged-out transient was lost: %v", got)
	}
}

// `warnings list --read` drains by RemoveTransients, and what it shows is reported too.
func TestTransientsShownByAPageAreReported(t *testing.T) {
	db := ledgerDB(t)
	w := stateTransient("t-1", "A")
	QueueTransients(db, []domain.TopologyWarning{w})
	RemoveTransients(db, PeekTransients(db))
	QueueTransients(db, []domain.TopologyWarning{w})
	if got := drainedIDs(db); len(got) != 0 {
		t.Errorf("a transient shown by a page was reported again: %v", got)
	}
}

// The ledger never discharges, so it is bounded: past the cap the OLDEST reports are forgotten,
// down to the keep size, and the newest survive.
func TestTheLedgerForgetsTheOldestPastItsCap(t *testing.T) {
	db := ledgerDB(t)
	// A full ledger, each entry reported one tick after the last; the next report tips it over.
	total := maxReportedTransients + 1
	full := make(map[string]reportedTransient, maxReportedTransients)
	for i := 0; i < total-1; i++ {
		full[reportedTransientKey(stateTransient(fmt.Sprintf("t-%05d", i), "A"))] = reportedTransient{At: int64(i + 1)}
	}
	writeReportedTransients(db, full)
	QueueTransients(db, []domain.TopologyWarning{stateTransient(fmt.Sprintf("t-%05d", total-1), "A")})
	DrainTransients(db)
	reported := readReportedTransients(db)
	if len(reported) > maxReportedTransients || len(reported) < reportedTransientsKeep {
		t.Fatalf("ledger holds %d entries, want between %d and %d", len(reported), reportedTransientsKeep, maxReportedTransients)
	}
	if _, ok := reported[reportedTransientKey(stateTransient("t-00000", "A"))]; ok {
		t.Error("the oldest entry survived pruning")
	}
	if _, ok := reported[reportedTransientKey(stateTransient(fmt.Sprintf("t-%05d", total-1), "A"))]; !ok {
		t.Error("the newest entry was pruned")
	}
	// Forgetting costs at most a repeat: the oldest is reportable again.
	QueueTransients(db, []domain.TopologyWarning{stateTransient("t-00000", "A")})
	if got := drainedIDs(db); len(got) != 1 {
		t.Errorf("a forgotten entry was not reportable: %v", got)
	}
}

// The fingerprint moves with each endpoint, and only with what matters to the call.
func TestTransientStateTracksBothEndpoints(t *testing.T) {
	w := domain.TopologyWarning{ID: "w", SourceID: "pkg.Target", TargetID: "pkg.Caller", Baseline: "Target|[string]|[bool]"}
	site := contract.CallSite{CalleeID: "pkg.Target", N: 1}
	resources := func(calleeInput string, callerHash string, n int) map[string]domain.Resource {
		s := site
		s.N = n
		return map[string]domain.Resource{
			"pkg.Target": {ID: "pkg.Target", Name: "Target", Properties: map[string]any{"input": calleeInput}},
			"pkg.Caller": {ID: "pkg.Caller", Name: "Caller", NormHash: callerHash, Connections: map[string][]string{
				contract.CallSitesConn: {contract.EncodeCallSite(s), contract.EncodeCallSite(contract.CallSite{CalleeID: "pkg.Other", N: n})},
			}},
		}
	}
	base := TransientState(w, resources("[]byte", "h1", 1))
	if base != TransientState(w, resources("[]byte", "h1", 1)) {
		t.Fatal("the same endpoints fingerprinted differently")
	}
	if base == TransientState(w, resources("int", "h1", 1)) {
		t.Error("a callee signature change kept the fingerprint")
	}
	if base == TransientState(w, resources("[]byte", "h2", 1)) {
		t.Error("a caller body change kept the fingerprint")
	}
	if base == TransientState(w, resources("[]byte", "h1", 2)) {
		t.Error("a caller call change kept the fingerprint")
	}
	missing := resources("[]byte", "h1", 1)
	delete(missing, "pkg.Caller")
	if base == TransientState(w, missing) {
		t.Error("a missing caller kept the fingerprint")
	}
}

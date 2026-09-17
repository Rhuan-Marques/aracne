package cli

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The seam between the two halves of the signature-warning lifecycle.
//
// WHY THIS FILE EXISTS. Both halves were correct and tested, and the feature still reached
// nothing. helper.ReconcileSignatureWarnings answers an Unverified verdict by WITHDRAWING the
// stored warning and emitting a transient in its place -- pinned end to end in
// internal/topology/warnings_transient_test.go, through the manager's RETURN VALUE. Every hook
// that reports to the model, meanwhile, reported by what was new in the warnings TABLE, which
// is what makes the channel survive a watcher finding the breakage first. A transient is never
// in the table, so the two contracts cancelled: retype a parameter, and every caller passing a
// local was judged Unverified, withdrawn, turned into a transient and dropped. The model saw
// "edit succeeded" and nothing else.
//
// Neither suite could catch that. The topology tests assert on the manager's return value,
// which was always right; the renderer test builds a transient by hand and never routes one
// through the ledger. The gap was the wiring between them, so that is what these pin: a
// transient is QUEUED where it is raised (helper.QueueTransients, called on the way out of
// every manager entry point) and drained only by a REPORT -- never by the scan that found it.

func transientWarning(id string) domain.TopologyWarning {
	return domain.TopologyWarning{
		ID: id, SourceID: "pkg.Target", TargetID: "pkg.CallerLocal",
		Kind: domain.WarnSignatureChanged, Transient: true,
		Message: "Target changed signature, ignore if still fits.",
	}
}

// The defining case: a warning that exists in no table still reaches the report.
func TestAQueuedTransientIsReportedThoughItIsNotInTheTable(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})

	got := unreportedWarnings(db)
	if len(got) != 1 {
		t.Fatalf("reported %d warnings, want the queued transient: %+v", len(got), got)
	}
	if !got[0].Transient || got[0].ID != "t-1" {
		t.Errorf("reported the wrong warning: %+v", got[0])
	}
}

// THE POINT OF THE QUEUE. A scan that discovers the breakage must not consume the report --
// that is what made `arac scanner run` and a scan in another terminal silence the channel.
// Queueing is what a scan does; only a report drains.
func TestAScanDoesNotStealAQueuedTransient(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)
	// Whoever scanned first -- the watcher, the pre-tool scan, another terminal -- queues it
	// and throws its own copy away.
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})
	// More scans of the same unchanged code raise nothing and must take nothing.
	helper.QueueTransients(db, nil)
	if n := len(helper.DrainTransients(db)); n != 1 {
		t.Fatalf("the queue held %d transients after unrelated scans, want 1", n)
	}
}

// A transient is news about the edit that produced it, so exactly one report may carry it. The
// guard's drift check and the edit-sync plugin can both fire for one native Edit.
func TestATransientIsNotReportedTwiceByTwoHooks(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})

	if got := unreportedWarnings(db); len(got) != 1 {
		t.Fatalf("first hook reported %d, want 1", len(got))
	}
	if got := unreportedWarnings(db); len(got) != 0 {
		t.Errorf("second hook repeated the transient: %+v", got)
	}
}

// Two scans raising the same transient about the same unchanged call say nothing new.
func TestTheSameTransientQueuedTwiceIsReportedOnce(t *testing.T) {
	db := warnDB(t)
	seedReportedWarnings(db)
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})

	if got := unreportedWarnings(db); len(got) != 1 {
		t.Errorf("reported %d copies of one transient, want 1: %+v", len(got), got)
	}
}

// The pre-tool pass must not drain the queue. It runs on the way INTO a tool call, where a
// hook cannot address the model -- so a transient it swallowed would be lost exactly like the
// scan-stolen one this queue exists to prevent.
func TestThePreToolSeedDoesNotDrainTheQueue(t *testing.T) {
	db := warnDB(t)
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})
	seedReportedWarnings(db)

	if got := unreportedWarnings(db); len(got) != 1 {
		t.Errorf("the pre-tool seed consumed the queued transient: %+v", got)
	}
}

// Only transients are queued. A stored warning handed to QueueTransients is already in the
// table, where the ledger diff sees it however it got there; queueing it too would report it
// twice for every edit.
func TestAStoredWarningIsNotQueued(t *testing.T) {
	db := warnDB(t)
	stored := domain.TopologyWarning{
		ID: "stored-1", SourceID: "pkg.F", TargetID: "pkg.Caller",
		Kind: domain.WarnSignatureChanged, Message: "verify stored-1",
	}
	helper.QueueTransients(db, []domain.TopologyWarning{stored})
	if got := helper.DrainTransients(db); len(got) != 0 {
		t.Errorf("queued a stored warning: %+v", got)
	}
}

// An unreadable database must not turn the queue into a report: the ledger cannot say what has
// already been shown, and the rest of this hook fails toward silence for the same reason.
func TestNoDatabaseReportsNothing(t *testing.T) {
	db := filepath.Join(t.TempDir(), "absent.db")
	helper.QueueTransients(db, []domain.TopologyWarning{transientWarning("t-1")})
	if got := unreportedWarnings(db); got != nil {
		t.Errorf("reported %+v with no topology to check against", got)
	}
}

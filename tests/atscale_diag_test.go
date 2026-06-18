package tests_test

// Minimal isolated repro for the most pervasive incremental/cold divergence the
// at-scale suite surfaces (filed as bug ..._1): an incremental re-parse of a
// file adds a spurious `uses_interface` edge that a cold scan never produces.
//
// This test makes NO semantic change — it only appends a comment to consumer.go
// and re-scans — so the only variable is the scan path (incremental vs cold).
// It strict-asserts that the incremental graph equals the cold full scan; it is
// expected to FAIL until the goscanner incremental UpdateFile path reproduces
// the cold Scan's edge derivation.

import (
	"path/filepath"
	"testing"
)

func TestAtScaleGo_GhostUsesInterfaceOnReparse(t *testing.T) {
	t.Parallel()
	root := copyCorpus(t)
	dbDir := t.TempDir()
	incrDB := filepath.Join(dbDir, "incr.db")
	fullDB := filepath.Join(dbDir, "full.db")

	mustRun(t, root, "scan", "-default", "-root", root, "-output", incrDB)
	// No-op edit: force a re-parse of consumer.go without changing its meaning.
	touchCorpusFile(t, root, "go/consumer/consumer.go")
	mustRun(t, root, "scan", "-default", "-root", root, "-output", incrDB)
	mustRun(t, root, "scan", "-all", "-root", root, "-output", fullDB)

	incr := readTopo(t, incrDB)
	full := readTopo(t, fullDB)

	// consumer.Report does not reference shapes.Shape; a cold scan never adds a
	// uses_interface edge for it. The incremental re-parse currently does.
	assertNoConn(t, incr, "incremental", idConsumerReport, "uses_interface", idShape)
	assertNoConn(t, full, "full", idConsumerReport, "uses_interface", idShape)
}

package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// driftScanTimeout bounds the backstop. A hook that hangs is worse than a hook that misses:
// the agent is blocked behind it on every shell call.
const driftScanTimeout = 20 * time.Second

// driftCheck re-syncs the topology after a shell command that may have written source, and
// returns any warnings the re-scan produced.
//
// WHY A BACKSTOP EXISTS AT ALL. The classifier in toolspec can only refuse the shapes it
// knows. A benchmark run had eight of twenty-seven agents edit source through paths nobody had
// enumerated -- `python3 - <<EOF … open(p,'w') … EOF`, `cp`, `git apply` -- leaving the
// topology describing code that no longer existed and, worse, emitting no warnings at all for
// changes that plainly deserved them. Every one of those is now classified, and the next
// unenumerated spelling will not be.
//
// So this detects the EFFECT rather than the command: whatever wrote the file, an incremental
// scan notices the file changed. That turns an arms race nobody can win into one that
// converges, and it is the reason the classifier work above is allowed to stay heuristic.
//
// It is deliberately cheap to skip and safe to fail: an unreadable topology, a scan error or a
// timeout all return nothing, exactly like the rest of this hook.
func driftCheck(dbPath string) string {
	type result struct {
		warnings []domain.TopologyWarning
		err      error
	}
	// Deliberately NOT InitRegistry: that helper scans-on-missing and calls os.Exit on
	// failure, which is correct for a CLI command and catastrophic inside a hook -- it would
	// take the agent's shell call down with it. A hook with no topology to check has nothing
	// to say, so bail rather than build one.
	if _, err := os.Stat(dbPath); err != nil {
		return ""
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return ""
	}
	reg := NewScannerRegistry()

	done := make(chan result, 1)
	go func() {
		defer func() {
			// A panic in a scanner must not take down the agent's shell call either.
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("scan panicked: %v", r)}
			}
		}()
		w, err := mgr.IncrementalScan(".", reg)
		done <- result{warnings: w, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return ""
		}
		return formatDriftWarnings(res.warnings)
	case <-time.After(driftScanTimeout):
		return ""
	}
}

// formatDriftWarnings renders the re-scan's warnings the same way edit/write already render
// theirs, so a warning reads identically whichever path produced the change.
func formatDriftWarnings(warnings []domain.TopologyWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Topology re-synced after a shell command wrote to the project.\n")
	b.WriteString("Topology warnings (functions that may need manual review):\n")
	for _, w := range warnings {
		fmt.Fprintf(&b, "  - [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// mayHaveWrittenSource reports whether a Bash command is worth a drift check.
//
// Running an incremental scan after every shell call would be the wrong trade -- most are
// `ls`, `go build` or a test run. The check fires when the classifier saw a write, and ALSO
// when it recognized nothing at all: an unclassified command is precisely the case the
// backstop exists for, since a command the classifier understands is already governed by the
// guard's own rules.
func mayHaveWrittenSource(keys []string) bool {
	sawSomething := false
	for _, k := range keys {
		switch k {
		case "edit", "write":
			return true
		case "bash":
			// Appended to every Bash command; says nothing about intent.
		default:
			sawSomething = true
		}
	}
	return !sawSomething
}

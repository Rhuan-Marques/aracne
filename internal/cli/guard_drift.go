package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
)

// guardScanTimeout bounds every scan a hook runs. A hook that hangs is worse than a hook that
// misses: the agent is blocked behind it on every tool call.
const guardScanTimeout = 20 * time.Second

// runGuardScan opens the topology and runs scan inside a bounded, panic-proof goroutine,
// reporting the warnings it produced. Every failure -- no database, a load error, a scan
// error, a panic, a timeout -- returns nil, because a hook that breaks the agent's tool call
// is worse than a hook that skips a scan.
//
// Deliberately NOT InitRegistry: that helper scans-on-missing and calls os.Exit on failure,
// which is correct for a CLI command and catastrophic inside a hook -- it would take the
// agent's tool call down with it. A hook with no topology has nothing to do, so bail rather
// than build one.
func runGuardScan(dbPath string, scan func(*topology.TopologyManager, *scanner.Registry) ([]domain.TopologyWarning, error)) []domain.TopologyWarning {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return nil
	}
	reg := NewScannerRegistry()

	type result struct {
		warnings []domain.TopologyWarning
		err      error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			// A panic in a scanner must not take down the agent's tool call either.
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("scan panicked: %v", r)}
			}
		}()
		w, err := scan(mgr, reg)
		done <- result{warnings: w, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return nil
		}
		return res.warnings
	case <-time.After(guardScanTimeout):
		return nil
	}
}

// preToolScan runs the scan.pre_tool scan BEFORE the tool call the guard is about to let
// through, so the graph the call is answered from describes the code as it is now.
//
// WHY BEFORE AND NOT ONLY AFTER. The post-call drift check (below) covers writes the guard
// saw. It cannot cover what happened OUTSIDE the session -- a `git checkout`, a rebase, a
// teammate's edit, an editor save, a build step run in another terminal -- and those land on
// the very next read, which is answered from spans that no longer line up. An incremental scan
// diffs the manifest and re-parses only what changed, so the common case (nothing moved since
// the last tool call) costs one directory walk and writes nothing.
//
// Nothing is REPORTED here on purpose. A PreToolUse hook cannot address the model without
// blocking the call, and a warning surfaced on the way into an unrelated call would be
// attributed to that call rather than to the change that produced it. The warnings are not
// lost: the scan persists them in the topology, where `warnings_list` / `arac warnings list`
// still finds them, and the post-call check reports the ones a shell write causes.
func preToolScan(dbPath string, cfg *helper.Config) {
	mode := cfg.EffectivePreToolScan()
	if mode == helper.PreToolScanNone {
		return
	}
	runGuardScan(dbPath, func(mgr *topology.TopologyManager, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
		return nil, mgr.RunPreToolScan(reg, mode)
	})
}

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
	warnings := runGuardScan(dbPath, func(mgr *topology.TopologyManager, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
		return mgr.IncrementalScan(".", reg)
	})
	return formatDriftWarnings(warnings)
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

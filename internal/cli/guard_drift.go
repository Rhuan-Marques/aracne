package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// guardScanTimeout bounds every scan a hook runs. A hook that hangs is worse than a hook that
// misses: the agent is blocked behind it on every tool call.
const guardScanTimeout = 20 * time.Second

// GuardHookTimeoutSeconds is the `timeout` `arac setup` writes onto its own hook entries.
//
// DERIVED FROM THE BUDGETS BELOW IT, not chosen. It was hard-coded at 30 while the guard's own
// stages could run longer than that: driftCheck ran indexHasDrifted (20s) and then a scan (20s)
// back to back for a worst case of 40, and the PreToolUse path reaches ~31 (a 20s pre-tool
// scan, then trackedFiles at 3, the untracked check at 3 and the read proxy at 5). Past the
// harness's timeout the hook is killed, and on the PostToolUse side that discards the warning
// report -- the channel the contract says has no substitute.
//
// driftCheck now shares ONE budget across its two stages (see driftCheckBudget), so the two
// paths cost at most guardScanTimeout plus the small checks around them. This leaves margin
// over that rather than tracking it exactly: the number is a ceiling on a pathological run, not
// a target.
const GuardHookTimeoutSeconds = 45

// driftCheckBudget is the wall clock the whole post-tool drift check may spend, SHARED by the
// staleness probe and the scan it gates. Two independent 20s deadlines summed to 40 against a
// hook allowed 30; one deadline is what makes the arithmetic in GuardHookTimeoutSeconds true.
const driftCheckBudget = guardScanTimeout

// runGuardScan opens the topology and runs scan inside a bounded, panic-proof goroutine,
// reporting the warnings it produced. Every failure -- no database, a load error, a scan
// error, a panic, a timeout -- returns nil, because a hook that breaks the agent's tool call
// is worse than a hook that skips a scan.
//
// Deliberately NOT InitRegistry: that helper scans-on-missing and calls os.Exit on failure,
// which is correct for a CLI command and catastrophic inside a hook -- it would take the
// agent's tool call down with it. A hook with no topology has nothing to do, so bail rather
// than build one.
//
// The budget is the caller's rather than a constant of its own, because a caller that runs two
// bounded stages has to spend ONE deadline across both -- see driftCheck. A non-positive budget
// is already out of time and skips the scan.
func runGuardScan(dbPath string, budget time.Duration, scan func(*topology.TopologyManager, *scanner.Registry) ([]domain.TopologyWarning, error)) []domain.TopologyWarning {
	if budget <= 0 {
		return nil
	}
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
	case <-time.After(budget):
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
	// Record what the repository already looks like BEFORE the tool runs, so the post-tool
	// comparison is against the state this call found. Done regardless of the scan mode --
	// it is one id query, and skipping it under `pre_tool: none` would make the first shell
	// write of a session dump every pre-existing warning as if the model had caused it.
	seedReportedWarnings(dbPath)

	mode := cfg.EffectivePreToolScan()
	if mode == helper.PreToolScanNone {
		return
	}
	runGuardScan(dbPath, guardScanTimeout, func(mgr *topology.TopologyManager, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
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
func driftCheck(dbPath, toolName string) string {
	// The scan still runs: it is what makes the topology describe the file the command just
	// wrote. Its RETURN value is deliberately ignored.
	//
	// ROOTED AT THE PROJECT, NOT AT THE PROCESS'S WORKING DIRECTORY. This passed a literal
	// "." while RunPreToolScan, doing the same job on the way in, reads the stored topo.Root.
	// The guard is a hook process, and guardDBPath exists precisely because its cwd is not
	// dependable -- it resolves the database from the hook event's cwd, from
	// $CLAUDE_PROJECT_DIR, or by walking upward. So "." was routinely a subdirectory, where
	// applyPathVisibility builds the ignore matcher against the wrong base, DetectAll sees
	// only the languages below that directory, and a directory with none at all makes
	// IncrementalScan return "no language scanner detected" -- which runGuardScan swallows,
	// leaving the backstop silently doing nothing the moment the model ran `cd`.
	// SKIPPED WHEN NOTHING ON DISK MOVED. The PreToolUse scan for this same call already
	// re-indexed whatever had drifted, and mayHaveWrittenSource is true for every command the
	// classifier does not recognize -- `ls`, `go build`, `npm test`, `git status`, `make`,
	// which is most of what an agent types. Those paid for a second full manifest walk to
	// discover the tree was unchanged. IndexHealth is the same primitive `arac check-updates`
	// uses and answers that in one pass. The REPORT below still runs either way: it reads the
	// table, not this scan, which is the whole point of the ledger (see guard_warnstate.go).
	//
	// ONE DEADLINE ACROSS BOTH STAGES. The probe and the scan used to carry a 20s timeout each,
	// so a slow repository could spend 40s inside a hook the generated settings.json allows 30
	// -- and a killed PostToolUse hook loses the report below, which is the whole reason this
	// function exists. See GuardHookTimeoutSeconds.
	root := ProjectRootFor(dbPath)
	deadline := time.Now().Add(driftCheckBudget)
	if indexHasDrifted(dbPath, root, time.Until(deadline)) {
		runGuardScan(dbPath, time.Until(deadline), func(mgr *topology.TopologyManager, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
			return mgr.IncrementalScan(root, reg)
		})
	}
	// Report by what is new in the TABLE rather than by what this scan produced. An
	// IncrementalScan only emits warnings for files IT finds drifted, so anything that
	// re-indexed the change first -- `arac scanner run`, the pre-tool scan, an `arac scan` in
	// another terminal -- leaves this scan with nothing to find and the model with nothing to
	// read. Measured on a real fixture, editing a function with eleven cross-file callers:
	// 11 warnings reported without the watcher, 0 with it. The warnings were correct and
	// present in the database both times. See guard_warnstate.go.
	fresh := unreportedWarnings(dbPath)
	// Recorded here because nothing downstream can see it: hook output reaches the model as
	// additionalContext, which the transcript does not carry. See logGuardWarnings.
	logGuardWarnings(toolName, fresh)
	return formatDriftWarnings(fresh)
}

// indexHasDrifted reports whether any file on disk differs from what the manifest recorded.
//
// Fails toward SCANNING: an unreadable database, an unusable root, a budget already spent or
// any error at all returns true, so the only thing this can cost is the scan that used to run
// unconditionally. Bounded and panic-proof for the same reason as everything else on this path,
// and bounded by the CALLER's remaining budget rather than by a deadline of its own.
func indexHasDrifted(dbPath, root string, budget time.Duration) bool {
	if budget <= 0 {
		return true
	}
	type result struct{ drifted bool }
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{true}
			}
		}()
		mgr := topology.New()
		if err := mgr.Load(dbPath); err != nil {
			done <- result{true}
			return
		}
		health, err := mgr.IndexHealth(root, NewScannerRegistry())
		done <- result{err != nil || health.Stale()}
	}()
	select {
	case r := <-done:
		return r.drifted
	case <-time.After(budget):
		return true
	}
}

// formatDriftWarnings renders the re-scan's warnings the same way edit/write already render
// theirs, so a warning reads identically whichever path produced the change.
func formatDriftWarnings(warnings []domain.TopologyWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	// DELIBERATELY SAYS NOTHING ABOUT THE CAUSE. This used to open "Topology re-synced after a
	// shell command wrote to the project", which is a claim the renderer is in no position to
	// make: the warnings come from unreportedWarnings, which reports what is NEW IN THE TABLE
	// however it got there -- a native edit, the pre-tool scan, `arac scanner run`, an
	// `arac scan` in another terminal. Measured on a real fixture, the line arrived attached to
	// an `ls` that had written nothing, sending the model looking for a shell write that never
	// happened. What the model can act on is the warning; the sentence above it only has to
	// not be false.
	b.WriteString("Topology re-synced.\n")
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

package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/warnread"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// warningKinds is the set `--kind` accepts, in the order the summary prints them.
var warningKinds = []domain.WarningKind{
	domain.WarnUseMissingNode, domain.WarnNodeRemoved,
	domain.WarnSignatureChanged, domain.WarnInterfaceConflict,
}

// parseWarningKind resolves a `--kind` value, naming the alternatives on a miss.
func parseWarningKind(value string) (domain.WarningKind, error) {
	want := domain.WarningKind(strings.TrimSpace(value))
	names := make([]string, 0, len(warningKinds))
	for _, k := range warningKinds {
		if k == want {
			return k, nil
		}
		names = append(names, string(k))
	}
	return "", fmt.Errorf("unknown warning kind %q (valid: %s)", value, strings.Join(names, ", "))
}

// Lists topology warnings filtered by source, target, or kind with sorted output and summary counts.
func RunWarningsList(args []string) {
	dbPath := ProjectDBPath(DefaultDBRelative)
	sourceID := ""
	targetID := ""
	readCode := false
	var kind domain.WarningKind
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--read":
			readCode = true
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		case "--source":
			if i+1 < len(args) {
				sourceID = args[i+1]
				i++
			}
		case "--target":
			if i+1 < len(args) {
				targetID = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				// VALIDATED, because the failure mode is silence. An unknown kind matched
				// nothing and printed "No warnings found." with exit 0 -- indistinguishable
				// from a clean topology, which is the answer a caller is most likely to
				// believe. `--kind signature-changed` (a hyphen) is the easy mistake, and the
				// usage banner lists the four spellings right next to it.
				k, err := parseWarningKind(args[i+1])
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				kind = k
				i++
			}
		default:
			// A TYPO IS A FAILURE, for the reason --kind above is validated: the failure
			// mode is silence. `arac warnings list --raed` printed the listing, ignored the
			// flag and exited 0 -- indistinguishable from a flag that ran and found nothing,
			// which is the reading a caller is most likely to believe. It cost a real
			// debugging session: `--read` typed at an older binary that predates the flag
			// looked exactly like a broken feature.
			fmt.Fprintf(os.Stderr, "Error: unknown argument %q\n", args[i])
			fmt.Fprintln(os.Stderr, "Usage: arac warnings list [--db <path>] [--source <id>] [--target <id>] [--kind <kind>] [--read]")
			os.Exit(1)
		}
	}

	manager, _ := InitRegistry(dbPath)
	warnings, err := manager.ListWarnings(sourceID, targetID, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(warnings) == 0 {
		fmt.Println("No warnings found.")
		return
	}

	// The shared total order, not a local one. `--read` expands the FIRST N of this list, and
	// the note it prints promises the next page starts where this one stopped -- which is only
	// true if every surface agrees on the order. See domain.SortWarnings.
	domain.SortWarnings(warnings)

	// --read REPLACES the listing rather than preceding it: the expansion writes each warning
	// on the line that caused it, so printing the summary too would say everything twice. The
	// listing is still what a bare `arac warnings list` prints, and still the fallback when
	// the expansion comes back empty -- see printWarningReads.
	if readCode && printWarningReads(dbPath, warnings) {
		return
	}
	printWarningList(warnings)
}

// printWarningList is the plain listing: counts by kind, then one entry per warning.
func printWarningList(warnings []domain.TopologyWarning) {
	counts := make(map[domain.WarningKind]int)
	for _, w := range warnings {
		counts[w.Kind]++
	}

	fmt.Printf("Found %d warning(s):\n\n", len(warnings))
	for _, k := range warningKinds {
		if c := counts[k]; c > 0 {
			fmt.Printf("  %s: %d\n", k, c)
		}
	}
	fmt.Println()

	for _, w := range warnings {
		fmt.Printf("  [%s] %s\n", w.Kind, w.Message)
		fmt.Printf("    source: %s\n", w.SourceID)
		if w.TargetID != "" {
			fmt.Printf("    target: %s\n", w.TargetID)
		}
		fmt.Println()
	}
}

// printWarningReads answers `--read`: the full source of the code the longest prefix of the
// warnings that fits features.warning_read_max_bytes names, so the listing is something to fix
// from rather than something to look up.
//
// AN OFF FEATURE SAYS SO. A flag that silently does nothing is worse than no flag -- the
// caller typed it, got a plain listing, and has no way to tell "nothing to read" from "this
// project has the feature off". It is a note rather than an error because the command still
// did its job: the warnings were listed.
// It reports whether it printed the expansion. False means the caller should fall back to the
// plain listing, so a --read that could not expand anything still answers the question the
// command was asked.
func printWarningReads(dbPath string, warnings []domain.TopologyWarning) bool {
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if !cfg.WarningReadsEnabled() {
		fmt.Println("--read did nothing: features.warning_reads is off in .aracne/config.json.")
		fmt.Println()
		return false
	}
	// warnread.NoBudget, not the hook's deadline: the caller asked for exactly this and is
	// waiting for it. See the constant.
	// QUEUED TRANSIENTS ARE PAGED TOO. The plain listing leaves them out on purpose -- it means
	// "actually broken" (see domain.TopologyWarning.Transient) -- but --read is the surface a
	// report's "N left" note points at, and a transient the report's budget paged out went back
	// to the queue for exactly this call. Drained only as they are shown, so the rest stay for
	// the next page.
	transients := helper.PeekTransients(dbPath)
	all := append(append([]domain.TopologyWarning(nil), warnings...), transients...)
	section, shown := warnread.SectionPage(dbPath, NewScannerRegistry(), all, warnread.Options{
		Budget:   warnread.NoBudget,
		Headline: warnread.HeadlinePull,
		Reserve:  1, // the newline Println ends it with
	})
	if section == "" {
		fmt.Println("--read found nothing to read: every warning names code the graph no longer holds,")
		fmt.Println("or a kind read.kinds does not allow.")
		fmt.Println()
		return false
	}
	fmt.Println(section)
	helper.RemoveTransients(dbPath, shown)
	return true
}

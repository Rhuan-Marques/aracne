package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

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
	var kind domain.WarningKind
	for i := 0; i < len(args); i++ {
		switch args[i] {
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

	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Kind != warnings[j].Kind {
			return warnings[i].Kind < warnings[j].Kind
		}
		return warnings[i].SourceID < warnings[j].SourceID
	})

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

package cli

import (
	"fmt"
	"os"
	"sort"

	"aracne/internal/topology/domain"
)

// Lists topology warnings filtered by source, target, or kind with sorted output and summary counts.
func RunWarningsList(args []string) {
	dbPath := ".aracne/topology.db"
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
				kind = domain.WarningKind(args[i+1])
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
	for _, k := range []domain.WarningKind{domain.WarnUseMissingNode, domain.WarnNodeRemoved, domain.WarnSignatureChanged,
		domain.WarnInterfaceConflict} {
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

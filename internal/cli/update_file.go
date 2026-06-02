package cli

import (
	"fmt"
	"os"
)

func RunUpdateFile(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp update-file <path> [--db <dbpath>]")
		os.Exit(1)
	}
	path := args[0]
	dbPath := ".ltp/topology.db"
	for i := 1; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		}
	}
	manager, reg := InitRegistry(dbPath)
	warnings, err := manager.UpdateFile(path, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Warning number %d", len(warnings))
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
}

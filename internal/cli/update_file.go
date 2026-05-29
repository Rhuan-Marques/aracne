package cli

import (
	"fmt"
	"os"
)

func RunUpdateFile(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp update-file <path>")
		os.Exit(1)
	}
	path := args[0]
	manager, reg := InitRegistry(".ltp/topology.db")
	warnings, err := manager.UpdateFile(path, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating file: %v\n", err)
		os.Exit(1)
	}
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
}
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"aracne/internal/helper"
)

// Detects added, modified, and deleted files by comparing current source against the manifest.
func RunCheckUpdates(args []string) {
	dbPath := ".aracne/topology.db"
	root := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		case "--root":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		}
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading topology: %v\n", err)
		os.Exit(1)
	}

	projectRoot := root
	if projectRoot == "" {
		projectRoot = topo.Root
	}
	if projectRoot == "" {
		projectRoot = "."
	}

	manifestPath := helper.ManifestPath(dbPath)

	var added, modified, deleted []string
	languages := topo.Languages
	if len(languages) == 0 && topo.Language != "" {
		languages = []string{topo.Language}
	}
	for _, lang := range languages {
		a, m, d, diffErr := helper.DiffScanFiles(projectRoot, lang, manifestPath)
		if diffErr != nil {
			fmt.Fprintf(os.Stderr, "Error checking updates: %v\n", diffErr)
			os.Exit(1)
		}
		added = append(added, a...)
		modified = append(modified, m...)
		deleted = append(deleted, d...)
	}

	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)

	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		fmt.Println("All files are up to date.")
		return
	}

	total := len(added) + len(modified) + len(deleted)
	fmt.Printf("%d file(s) not up to date:\n\n", total)

	if len(added) > 0 {
		fmt.Printf("  Added (%d):\n", len(added))
		for _, f := range added {
			rel, _ := filepath.Rel(projectRoot, f)
			fmt.Printf("    + %s\n", rel)
		}
		fmt.Println()
	}

	if len(modified) > 0 {
		fmt.Printf("  Modified (%d):\n", len(modified))
		for _, f := range modified {
			rel, _ := filepath.Rel(projectRoot, f)
			fmt.Printf("    ~ %s\n", rel)
		}
		fmt.Println()
	}

	if len(deleted) > 0 {
		fmt.Printf("  Deleted (%d):\n", len(deleted))
		for _, f := range deleted {
			rel, _ := filepath.Rel(projectRoot, f)
			fmt.Printf("    - %s\n", rel)
		}
		fmt.Println()
	}
}

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"aracne/internal/helper"
)

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
	manifest := helper.ReadManifest(manifestPath)

	manifestTimes := make(map[string]time.Time)
	for path, ts := range manifest {
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err == nil {
			manifestTimes[path] = t
		}
	}

	var added, modified, deleted []string
	lang := topo.Language
	currentFiles := helper.CollectSourceFiles(projectRoot, lang)
	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
		t, inManifest := manifestTimes[f]
		if !inManifest {
			added = append(added, f)
		} else {
			fi, err := os.Stat(f)
			if err == nil && fi.ModTime().UTC().After(t) {
				modified = append(modified, f)
			}
		}
	}

	for path := range manifestTimes {
		if !currentSet[path] {
			deleted = append(deleted, path)
		}
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

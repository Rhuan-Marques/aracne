package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RunCheckUpdates reports index health: which source files have drifted from the topology
// since the last scan.
//
// It is the surface that answers "should I believe this graph". Everything else answers what
// the graph SAYS; nothing said whether it is still true. A database restored beside source it
// was not built from -- a benchmark fixture unfrozen into a reset worktree, a checkout made
// while no scanner was watching -- produces reads that fail in ways that look like bad IDs,
// and nothing pointed at the real cause. Exits 1 when the index is stale, so a script (or a
// benchmark harness) can gate on it instead of discovering the problem inside a run.
//
// The diff itself is TopologyManager.IndexHealth, the same primitive the read tool blames a
// miss on and the watch loop acts on, so the three cannot drift apart.
func RunCheckUpdates(args []string) {
	dbPath := ".aracne/topology.db"
	root := ""
	asJSON := false
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
		case "--json":
			asJSON = true
		}
	}

	manager, _ := InitRegistry(dbPath)
	health, err := manager.IndexHealth(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking updates: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		payload := map[string]any{
			"db":       dbPath,
			"root":     health.Root,
			"stale":    health.Stale(),
			"drifted":  health.Drifted(),
			"added":    relativize(health.Root, health.Added),
			"modified": relativize(health.Root, health.Modified),
			"deleted":  relativize(health.Root, health.Deleted),
		}
		out, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(out))
		if health.Stale() {
			os.Exit(1)
		}
		return
	}

	if !health.Stale() {
		fmt.Println("All files are up to date.")
		return
	}

	fmt.Printf("%d file(s) not up to date:\n\n", health.Drifted())
	section := func(label, marker string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Printf("  %s (%d):\n", label, len(paths))
		for _, f := range relativize(health.Root, paths) {
			fmt.Printf("    %s %s\n", marker, f)
		}
		fmt.Println()
	}
	section("Added", "+", health.Added)
	section("Modified", "~", health.Modified)
	section("Deleted", "-", health.Deleted)
	fmt.Println("The topology is STALE for these files. Run `arac scan` to re-index.")
	os.Exit(1)
}

// relativize renders absolute file paths against the topology root, leaving anything outside
// it absolute rather than printing a "../.." chain.
func relativize(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil || len(rel) >= 2 && rel[:2] == ".." {
			out = append(out, p)
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

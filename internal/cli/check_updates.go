package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
	dbPath, root, asJSON, err := parseCheckUpdatesArgs(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		// 2, not 1: exit 1 is this command's answer ("stale"), and a script gating on it must
		// not read a mistyped flag as a stale index.
		os.Exit(2)
	}

	manager, reg := InitRegistry(dbPath)
	// The registry, so a language the tree has acquired since the last scan is reported as new
	// rather than as nothing at all -- which is the whole question this command answers.
	health, err := manager.IndexHealth(root, reg)
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

// parseCheckUpdatesArgs reads check-updates' flags: --db <path>, --root <path>, --json.
//
// AN UNKNOWN FLAG IS AN ERROR. The hand-rolled loop this replaces skipped anything it did not
// recognise, so `arac check-updates --josn` ran the text report and exited 0 -- a script gating
// on the JSON, or on a --db it misspelled, got an answer about a different question and no sign
// of the typo. A stray positional argument is refused for the same reason. Errors and -h usage
// go to out.
func parseCheckUpdatesArgs(args []string, out io.Writer) (dbPath, root string, asJSON bool, err error) {
	fs := flag.NewFlagSet("check-updates", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&dbPath, "db", ProjectDBPath(DefaultDBRelative), "Topology database path")
	fs.StringVar(&root, "root", "", "Project root (default: the one the topology records)")
	fs.BoolVar(&asJSON, "json", false, "Print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return "", "", false, err
	}
	if fs.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(out, "%v\nUsage: arac check-updates [--json] [--db <path>] [--root <path>]\n", err)
		return "", "", false, err
	}
	return dbPath, root, asJSON, nil
}

// relativize renders absolute file paths against the topology root, leaving anything outside
// it absolute rather than printing a "../.." chain.
func relativize(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil || !domain.RelInside(rel) {
			out = append(out, p)
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

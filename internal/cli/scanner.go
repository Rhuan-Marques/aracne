package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Dispatches scanner subcommands; routes to RunScannerRun for the "run" action.
func RunScanner(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac scanner <run> [--db <path>]")
		os.Exit(1)
	}
	switch args[0] {
	case "run":
		RunScannerRun(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown scanner command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: arac scanner <run> [--db <path>]")
		os.Exit(1)
	}
}

// Watches source files for changes and runs incremental scans to update the topology database.
func RunScannerRun(args []string) {
	dbPath := ".aracne/topology.db"
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		}
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: no topology database found at %s. Run 'Aracne scan' first.\n", dbPath)
		os.Exit(1)
	}

	// Use the manifest directly (JSON, not SQLite) to detect changes,
	// avoiding concurrent SQLite access with IncrementalScan.
	manifestPath := helper.ManifestPath(dbPath)

	// Read the topology once up front to get root.
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading topology: %v\n", err)
		os.Exit(1)
	}
	root := topo.Root
	if root == "" {
		root = "."
	}
	if info, statErr := os.Stat(root); statErr != nil {
		fmt.Fprintf(os.Stderr, "Error: topology root %s is not accessible: %v. Re-run 'arac scan --root .' from the project root.\n", root, statErr)
		os.Exit(1)
	} else if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: topology root %s is not a directory. Re-run 'arac scan --root .' from the project root.\n", root)
		os.Exit(1)
	}

	reg := NewScannerRegistry()
	manager := topology.New()
	manager.Load(dbPath)

	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	// Install the path-visibility filter up front so the watch-loop's change
	// detection (DiffScanFiles below) skips hidden paths from the first tick,
	// matching what IncrementalScan applies internally.
	domain.SetActivePathVisibility(domain.BuildPathVisibility(root, cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore))
	frequency := cfg.Scanner.UpdateFrequency
	if frequency <= 0 {
		frequency = 200
	}

	fmt.Fprintf(os.Stderr, "Scanner running on %s (checking every %dms)...\n", dbPath, frequency)

	var scanMu sync.Mutex

	sleep := func() {
		time.Sleep(time.Duration(frequency) * time.Millisecond)
	}

	diffChanged := func() (added, modified, deleted []string, err error) {
		for _, ls := range reg.DetectAll(root) {
			a, m, d, diffErr := helper.DiffScanFiles(root, ls.Name(), manifestPath)
			if diffErr != nil {
				return nil, nil, nil, diffErr
			}
			added = append(added, a...)
			modified = append(modified, m...)
			deleted = append(deleted, d...)
		}
		return added, modified, deleted, nil
	}

	for {
		if !scanMu.TryLock() {
			sleep()
			continue
		}

		added, modified, deleted, diffErr := diffChanged()
		if diffErr != nil {
			scanMu.Unlock()
			fmt.Fprintf(os.Stderr, "[%s] Scan error: %v\n", time.Now().Format("15:04:05"), diffErr)
			sleep()
			continue
		}
		changed := len(added) + len(modified) + len(deleted)

		if changed == 0 {
			scanMu.Unlock()
			sleep()
			continue
		}

		_, scanErr := manager.IncrementalScan(root, reg)
		scanMu.Unlock()

		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "[%s] Scan error: %v\n", time.Now().Format("15:04:05"), scanErr)

			if len(deleted) > 0 {
				topo, readErr := helper.ReadDb(dbPath)
				if readErr == nil {
					for _, path := range deleted {
						helper.RemoveFileResources(topo, path)
					}
					helper.CleanupOrphanedWarnings(topo)
					if writeErr := helper.WriteDb(topo, dbPath); writeErr == nil {
						helper.SyncManifest(topo, dbPath)
					}
				}
			}

			sleep()
			continue
		}

		timestamp := time.Now().Format("15:04:05")
		changedFiles := append(added, modified...)
		sort.Strings(changedFiles)

		fmt.Printf("[%s] Updated %d files:\n", timestamp, len(changedFiles))
		for _, f := range changedFiles {
			rel, rerr := filepath.Rel(root, f)
			if rerr != nil {
				fmt.Printf("\t%s\n", f)
			} else {
				fmt.Printf("\t%s\n", rel)
			}
		}

		if len(deleted) > 0 {
			sort.Strings(deleted)
			fmt.Printf("[%s] Removed %d files:\n", timestamp, len(deleted))
			for _, f := range deleted {
				rel, rerr := filepath.Rel(root, f)
				if rerr != nil {
					fmt.Printf("\t%s\n", f)
				} else {
					fmt.Printf("\t%s\n", rel)
				}
			}
		}

		sleep()
	}
}

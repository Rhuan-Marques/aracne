package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// Analyzes a project and builds/updates the topology database with configurable scan modes (hard, full, or incremental).
func RunScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	root := fs.String("root", ".", "Root folder of the project to analyze")
	output := fs.String("output", ".aracne/topology.db", "Output SQLite database path")
	allFlag := fs.Bool("all", false, "Re-scan all files (preserves existing descriptions)")
	hardFlag := fs.Bool("hard", false, "Force full rebuild from scratch (clears descriptions and bugs)")
	defaultFlag := fs.Bool("default", false, "Force default incremental scan (overrides config)")
	debug := fs.Bool("debug", false, "Compare warnings before and after scan, print differences")
	fs.Parse(args)

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

	cfgPath := helper.ConfigPath(*output)
	cfg := helper.EnsureConfig(cfgPath)

	explicitFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = true
	})

	var beforeWarnings map[string]domain.TopologyWarning
	if *debug {
		if _, statErr := os.Stat(*output); statErr == nil {
			if beforeTopo, readErr := helper.ReadDb(*output); readErr == nil {
				beforeWarnings = beforeTopo.Warnings
			}
		}
	}

	fmt.Printf("Analyzing project at: %s\n", *root)

	start := time.Now()
	reg := NewScannerRegistry()

	resolvedMode := cfg.Scan.Mode
	switch {
	case explicitFlags["hard"] && *hardFlag:
		resolvedMode = helper.ScanModeHard
	case explicitFlags["all"] && *allFlag:
		resolvedMode = helper.ScanModeAll
	case explicitFlags["default"] && *defaultFlag:
		resolvedMode = helper.ScanModeDefault
	}

	switch resolvedMode {
	case helper.ScanModeHard:
		fmt.Println("Hard scan: rebuilding topology from scratch")
		if err := manager.FullScan(*root, reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		manager.DeleteAllBugs()
	case helper.ScanModeAll:
		fmt.Println("Full re-scan: processing all files")
		if _, err := manager.FullReScan(*root, reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Println("Incremental scan: processing only changed files")
		warnings, err := manager.IncrementalScan(*root, reg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(warnings) > 0 {
			fmt.Printf("\n%d warning(s) during scan:\n", len(warnings))
			for _, w := range warnings {
				if w.Kind != "" {
					fmt.Printf("  [%s] %s\n", w.Kind, w.Message)
				} else {
					fmt.Printf("  %s\n", w.Message)
				}
			}
			fmt.Println()
		}
	}
	elapsed := time.Since(start)

	topo, err := helper.ReadDb(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading output: %v\n", err)
		os.Exit(1)
	}

	if *debug && beforeWarnings != nil {
		added, removed := DiffWarnings(beforeWarnings, topo.Warnings)
		if len(added) > 0 || len(removed) > 0 {
			fmt.Printf("\n=== Warning Differences ===\n\n")
			if len(added) > 0 {
				fmt.Printf("Added (%d):\n", len(added))
				for _, w := range added {
					fmt.Printf("  + [%s] %s\n    source: %s", w.Kind, w.Message, w.SourceID)
					if w.TargetID != "" {
						fmt.Printf("\n    target: %s", w.TargetID)
					}
					fmt.Print("\n\n")
				}
			}
			if len(removed) > 0 {
				fmt.Printf("Resolved (%d):\n", len(removed))
				for _, w := range removed {
					fmt.Printf("  - [%s] %s\n    source: %s", w.Kind, w.Message, w.SourceID)
					if w.TargetID != "" {
						fmt.Printf("\n    target: %s", w.TargetID)
					}
					fmt.Print("\n\n")
				}
			}
		} else {
			fmt.Println("\nNo warning differences detected.")
		}
	}

	pkgCount := 0
	fileCount := 0
	funcCount := 0
	typeCount := 0
	ifaceCount := 0
	varCount := 0
	depCount := 0

	for _, res := range topo.Resources {
		switch res.Kind {
		case domain.ResourcePackage:
			pkgCount++
		case domain.ResourceFile:
			fileCount++
		case domain.ResourceFunction, domain.ResourceMethod:
			funcCount++
		case domain.ResourceType, domain.ResourceNamedType:
			typeCount++
		case domain.ResourceInterface:
			ifaceCount++
		case domain.ResourceVariable:
			varCount++
		case domain.ResourceDependency:
			depCount++
		}
	}

	fmt.Printf("Topology written to: %s\n", *output)
	fmt.Printf("Analyzed in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("-%d packages\n-%d files\n-%d functions\n-%d types\n-%d interfaces\n-%d variables\n-%d dependencies\n-%d errors\n",
		pkgCount, fileCount, funcCount, typeCount, ifaceCount, varCount, depCount, len(topo.Errors))
}

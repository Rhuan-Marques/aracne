package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
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
	verbose := fs.Bool("verbose", false, "Print the changed files detected during an incremental scan")
	fs.BoolVar(verbose, "v", false, "Shorthand for --verbose")
	workers := fs.Int("workers", 0, "Max files parsed concurrently during a full scan (0 = auto, one per CPU). Lower it to cap peak RAM.")
	progressFlag := fs.String("progress", "auto", "Scan progress bar: auto (on a terminal above 15 files), always, or never")
	fs.Parse(args)

	explicitFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = true
	})

	// A BARE `arac scan` SCANS THE PROJECT IT IS RUN INSIDE, not the directory it is run from.
	//
	// This was the last verb resolving the default database literally, and ProjectDBPath's own
	// comment describes what that costs: run from a subdirectory it built a SECOND, partial
	// topology under `<subdir>/.aracne/` beside a fresh default config -- so the project's mode
	// was silently replaced by `cli` for that subtree, `arac cmd`'s upward walk found the nested
	// database first, and interception stopped. `arac init` refuses the same situation with a
	// pointer; this created it without a word, and created the config even when the scan then
	// failed.
	//
	// Only a BARE `arac scan` walks up. Naming either flag is the caller saying where, and
	// `arac scan -root .` is exactly how someone deliberately sets a subdirectory up as its own
	// project -- which is what `arac init`'s refusal tells them to type, so it has to keep
	// meaning that rather than merging a subtree into the parent's graph.
	if !explicitFlags["output"] && !explicitFlags["root"] {
		*output = ProjectDBPath(*output)
		*root = ProjectRootFor(*output)
	}

	// A NAMED ROOT KEEPS ITS DATABASE UNDER ITSELF. The default `-output` is relative, so
	// `arac scan -root ./backend` wrote `<cwd>/.aracne/topology.db` while storing `<cwd>/backend`
	// as the root -- and Relocation reads the project root off the database's own location, so
	// the very next scan saw the two disagree, reported a move that never happened, and
	// re-indexed the WHOLE cwd. The mismatch is what has to go, not the inference: a database
	// somewhere else is how a genuine move is told from a subdirectory scan. Naming `-output`
	// too still puts it exactly where the caller asked.
	if explicitFlags["root"] && !explicitFlags["output"] {
		*output = filepath.Join(*root, DefaultDBRelative)
	}

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

	cfgPath := helper.ConfigPath(*output)
	cfg := helper.EnsureConfig(cfgPath)

	// Install the path-visibility and scan.ignore filters up front: the language
	// detection and progress-bar sizing below both walk the tree before the scan
	// itself installs them (TopologyManager.applyPathVisibility), and an ignored
	// tree must be invisible to every walk, not just the parsing one. The scan
	// re-installs them, so this is idempotent.
	domain.SetActivePathVisibility(domain.BuildPathVisibility(*root, cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(*root, cfg.Scan.Ignore))

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

	// The scan mode comes from the flags alone: --hard, --all, or the incremental default.
	resolvedMode := helper.ScanModeDefault
	switch {
	case explicitFlags["hard"] && *hardFlag:
		resolvedMode = helper.ScanModeHard
	case explicitFlags["all"] && *allFlag:
		resolvedMode = helper.ScanModeAll
	case explicitFlags["default"] && *defaultFlag:
		resolvedMode = helper.ScanModeDefault
	}

	// Resolve parallelism: config baseline, overridden by an explicit --workers.
	// <= 0 means auto (one worker per CPU), applied inside scanner.Workers().
	resolvedWorkers := cfg.Scan.Workers
	if explicitFlags["workers"] {
		resolvedWorkers = *workers
	}
	scanner.SetWorkers(resolvedWorkers)

	// Resolve the progress bar: config baseline, overridden by an explicit
	// --progress. "auto" turns it on only on a terminal and only when the project
	// is large enough to be worth a bar.
	progressMode := cfg.Scan.Progress
	if explicitFlags["progress"] {
		progressMode = strings.ToLower(strings.TrimSpace(*progressFlag))
	}
	showProgress := false
	switch progressMode {
	case helper.ProgressAlways:
		showProgress = true
	case helper.ProgressNever:
		showProgress = false
	default: // auto (also the fallback for an unset/legacy config value)
		showProgress = stderrIsTerminal() && countSourceFiles(*root, reg) > helper.ProgressFileThreshold
	}
	scanner.SetProgressEnabled(showProgress)

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
		// A moved project cannot be diffed against a manifest of the old paths; IncrementalScan
		// would rebuild it anyway, and this says why the scan is a full one.
		if oldRoot, newRoot, moved := manager.Relocation(); moved {
			fmt.Printf("Project moved from %s to %s: re-indexing under the new root\n", oldRoot, newRoot)
			if _, err := manager.SyncRelocation(reg); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
		if *verbose {
			var langs []string
			for _, ls := range reg.DetectAll(*root) {
				langs = append(langs, ls.Name())
			}
			reportChangedFiles(*root, *output, cfg, langs)
		}
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
	structCount := 0
	namedTypeCount := 0
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
		case domain.ResourceStruct:
			structCount++
		case domain.ResourceNamedType:
			namedTypeCount++
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
	fmt.Printf("-%d packages\n-%d files\n-%d functions\n-%d structs\n-%d named types\n-%d interfaces\n-%d variables\n-%d dependencies\n-%d errors\n",
		pkgCount, fileCount, funcCount, structCount, namedTypeCount, ifaceCount, varCount, depCount, len(topo.Errors))
	printScanErrors(topo.Errors)

	warnEmptyScan(*root, fileCount, reg)
}

// maxPrintedScanErrors is how many scan errors the summary spells out before it stops and
// says how many are left.
const maxPrintedScanErrors = 5

// printScanErrors spells out what the error count counted.
//
// A NUMBER WITH NO WAY TO READ IT IS NOT A REPORT. The errors have always been stored -- as
// `error:<key>` rows in `info`, capped by writeScanErrors -- and nothing anywhere read them
// back: `arac warnings list` lists topology WARNINGS, a different table, and /api/summary
// reports warnings and bugs and no errors at all. So the summary printed "-3 errors" and left
// a SQLite client as the only way to find out what they were. This repository reported three
// for a long time; one of them was a source file the scanner could not parse.
func printScanErrors(errs map[string]string) {
	if len(errs) == 0 {
		return
	}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i >= maxPrintedScanErrors {
			fmt.Fprintf(os.Stderr, "  … and %d more (every error is stored in the topology; "+
				"`arac warnings list` covers warnings, not these)\n", len(keys)-maxPrintedScanErrors)
			break
		}
		fmt.Fprintf(os.Stderr, "  error: %s: %s\n", k, firstLine(errs[k]))
	}
}

// firstLine keeps a multi-line diagnostic (a Python traceback, say) to one line in a summary.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

// warnEmptyScan reports a scan that detected a project but indexed nothing.
//
// "-0 files -0 functions -0 errors" and exit 0 is indistinguishable from
// success, and it was the visible symptom of a real bug for every repo living
// under a hidden directory. A scanner that claimed the root and then produced
// nothing is always worth saying out loud.
func warnEmptyScan(root string, fileCount int, reg *scanner.Registry) {
	if fileCount > 0 {
		return
	}
	var langs []string
	for _, ls := range reg.DetectAll(root) {
		langs = append(langs, ls.Name())
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if len(langs) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: no source files indexed under %s and no language was detected.\n", abs)
		return
	}
	fmt.Fprintf(os.Stderr,
		"Warning: detected %s under %s but indexed 0 files. Check that the sources are not excluded by scan.ignore.\n",
		strings.Join(langs, ", "), abs)
}

// reportChangedFiles prints, for a verbose incremental scan, the source files
// that changed since the last scan — the set the incremental pass re-processes.
// It runs the same DiffScanFiles change detection IncrementalScan performs
// internally, under the same path-visibility and scan.ignore filters, so the
// list matches exactly what the scan will update.
func reportChangedFiles(root, dbPath string, cfg *helper.Config, languages []string) {
	// Install the active filters so change detection skips hidden/ignored paths,
	// matching IncrementalScan. This is idempotent: the scan re-installs them.
	domain.SetActivePathVisibility(domain.BuildPathVisibility(root, cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore))

	manifestPath := helper.ManifestPath(dbPath)
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		fmt.Println("No manifest yet: all source files will be processed.")
		return
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("No topology database yet: all source files will be processed.")
		return
	}

	var added, modified, deleted []string
	for _, lang := range languages {
		a, m, d, err := helper.DiffScanFiles(root, lang, manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: change detection for %s failed: %v\n", lang, err)
			return
		}
		added = append(added, a...)
		modified = append(modified, m...)
		deleted = append(deleted, d...)
	}

	total := len(added) + len(modified) + len(deleted)
	fmt.Printf("Changed files found: %d (%d added, %d modified, %d deleted)\n",
		total, len(added), len(modified), len(deleted))
	if total == 0 {
		return
	}
	fmt.Println("Files needing update:")
	printChangedList(root, "+", added)
	printChangedList(root, "~", modified)
	printChangedList(root, "-", deleted)
}

// printChangedList prints files under a change marker, sorted, relative to root.
func printChangedList(root, marker string, files []string) {
	sort.Strings(files)
	for _, f := range files {
		fmt.Printf("  %s %s\n", marker, relForDisplay(root, f))
	}
}

// relForDisplay expresses p relative to root in forward-slash form for readable
// output, falling back to p unchanged when it lies outside root.
func relForDisplay(root, p string) string {
	if absRoot, err := filepath.Abs(root); err == nil {
		if rel, relErr := filepath.Rel(absRoot, p); relErr == nil && domain.RelInside(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return p
}

// stderrIsTerminal reports whether stderr is an interactive terminal, so the
// "auto" progress bar (which draws with carriage returns) stays off when output
// is piped or redirected.
func stderrIsTerminal() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// countSourceFiles returns an approximate count of the source files the scanners
// would parse under root. It is used only to decide whether the "auto" progress
// bar is worth showing, so it need not exactly match each scanner's own file
// discovery: it walks once, counting files a registered scanner claims by
// extension that are real (non-test) source files.
func countSourceFiles(root string, reg *scanner.Registry) int {
	count := 0
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// path != root so a dot-named root is not pruned by its own basename
			name := d.Name()
			if path != root {
				if name == ".git" || name == ".aracne" || name == "node_modules" || name == "vendor" || name == "__pycache__" {
					return filepath.SkipDir
				}
				if strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				// IsSourceFile below already rejects ignored files one by one,
				// which keeps the count correct -- but only pruning the
				// directory keeps the walk from descending into a large ignored
				// tree just to reject every file in it.
				if domain.PathPruneDir(path) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ls := reg.DetectFile(path)
		if ls == nil {
			return nil
		}
		if helper.IsSourceFile(root, path, ls.Name()) {
			count++
		}
		return nil
	})
	return count
}

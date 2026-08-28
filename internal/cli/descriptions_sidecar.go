package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"aracne/internal/helper"
)

// `arac descriptions export` / `import` — the ID-independent backup for LLM-authored
// descriptions. See internal/helper/descriptions_sidecar.go for why this exists: every
// description-preservation path in the scanners is keyed on the resource ID alone, so a
// change to any ID format silently discards them, and `scan --hard` discards them
// unconditionally.

const defaultSidecarPath = ".aracne/" + helper.DescriptionsSidecarName

// RunDescriptionsExport writes every stored description to a JSONL sidecar keyed by a
// stable identity rather than by resource ID.
func RunDescriptionsExport(args []string) {
	fs := flag.NewFlagSet("descriptions-export", flag.ExitOnError)
	out := fs.String("out", defaultSidecarPath, "path to write the JSONL sidecar")
	dbPath := fs.String("db", ".aracne/topology.db", "topology database")
	fs.Parse(args)

	manager, _ := InitRegistry(*dbPath)
	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: read topology: %v\n", err)
		os.Exit(1)
	}

	recs := helper.BuildDescriptionRecords(topo, topo.Root)
	if err := helper.WriteDescriptionRecords(*out, recs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write %s: %v\n", *out, err)
		os.Exit(1)
	}

	withHash := 0
	for _, r := range recs {
		if r.SrcSHA256 != "" {
			withHash++
		}
	}
	fmt.Printf("Exported %d description(s) to %s\n", len(recs), *out)
	fmt.Printf("  %d have a source hash (needed to survive a file move); %d do not.\n",
		withHash, len(recs)-withHash)
}

// RunDescriptionsImport re-attaches an exported sidecar to the current topology, matching
// through progressively weaker identity tiers and reporting which tier each record used.
func RunDescriptionsImport(args []string) {
	fs := flag.NewFlagSet("descriptions-import", flag.ExitOnError)
	in := fs.String("in", defaultSidecarPath, "path to the JSONL sidecar to restore")
	dbPath := fs.String("db", ".aracne/topology.db", "topology database")
	dryRun := fs.Bool("dry-run", false, "report what would be restored without writing")
	// A migration that silently restores 60% of descriptions looks like success in the
	// console and is a disaster on disk. This makes the acceptable loss explicit.
	minRate := fs.Float64("min-rate", 0, "fail (exit 2) when the match rate is below this (0..1)")
	reportPath := fs.String("report", "", "write unmatched records to this JSONL path")
	fs.Parse(args)

	recs, err := helper.ReadDescriptionRecords(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: read %s: %v\n", *in, err)
		os.Exit(1)
	}
	if len(recs) == 0 {
		fmt.Printf("No records in %s; nothing to restore.\n", *in)
		return
	}

	manager, _ := InitRegistry(*dbPath)
	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: read topology: %v\n", err)
		os.Exit(1)
	}

	result := helper.MatchDescriptions(recs, topo, topo.Root)
	printImportReport(result, *dryRun)

	if *reportPath != "" && len(result.Unmatched) > 0 {
		if err := helper.WriteDescriptionRecords(*reportPath, result.Unmatched); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write %s: %v\n", *reportPath, err)
		} else {
			fmt.Printf("  unmatched records written to %s\n", *reportPath)
		}
	}

	if !*dryRun && len(result.Matched) > 0 {
		updates := make(map[string]string, len(result.Matched))
		for _, m := range result.Matched {
			updates[m.ResourceID] = m.Record.Description
		}
		n, err := helper.UpdateDescriptions(*dbPath, updates)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: write descriptions: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Restored %d description(s) into %s\n", n, *dbPath)
	} else if *dryRun {
		fmt.Println("(dry run — nothing written)")
	}

	if *minRate > 0 && result.MatchRate() < *minRate {
		fmt.Fprintf(os.Stderr,
			"\nFAIL: match rate %.1f%% is below the required %.1f%%. "+
				"The topology no longer contains %d of the %d described resources.\n",
			result.MatchRate()*100, *minRate*100, len(result.Unmatched), result.Total())
		os.Exit(2)
	}
}

// printImportReport shows per-tier accounting. The tier breakdown is the point: an import
// that matched everything at `exact_id` proves nothing about a scheme change, while one
// that matched at `path_identity` proves the stable key did its job.
func printImportReport(r helper.DescriptionImportResult, dryRun bool) {
	verb := "would restore"
	if !dryRun {
		verb = "restoring"
	}
	fmt.Printf("Sidecar: %d record(s); %s %d, skipping %d already described, %d unmatched "+
		"(match rate %.1f%%)\n",
		r.Total(), verb, len(r.Matched), len(r.Skipped), len(r.Unmatched), r.MatchRate()*100)
	for _, tier := range helper.MatchTiers {
		if n := r.ByTier[tier]; n > 0 {
			fmt.Printf("  %-14s %d\n", tier, n)
		}
	}
	if len(r.Ambiguous) > 0 {
		fmt.Printf("  %-14s %d (several resources share the identity; left unattached "+
			"rather than guessed)\n", "ambiguous", len(r.Ambiguous))
	}
	if len(r.Unmatched) > 0 {
		fmt.Println("  unmatched sample:")
		for _, rec := range sampleRecords(r.Unmatched, 5) {
			fmt.Printf("    [%s] %s  (%s)\n", rec.Kind, rec.ID, rec.RelPath)
		}
	}
}

// sampleRecords returns up to n records, sorted for a stable console output.
func sampleRecords(recs []helper.DescriptionRecord, n int) []helper.DescriptionRecord {
	out := append([]helper.DescriptionRecord(nil), recs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// SidecarPathFor returns the conventional sidecar path beside a given topology database,
// so callers outside this file (the ID migration) agree on the location.
func SidecarPathFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), helper.DescriptionsSidecarName)
}

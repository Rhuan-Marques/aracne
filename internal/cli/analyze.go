package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
)

func RunAnalyze(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac analyze <dead-code> [flags]")
		os.Exit(1)
	}
	switch args[0] {
	case "dead-code":
		RunAnalyzeDeadCode(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown analyze subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: arac analyze <dead-code> [flags]")
		os.Exit(1)
	}
}

func RunAnalyzeDeadCode(args []string) {
	fs := flag.NewFlagSet("analyze dead-code", flag.ExitOnError)
	kindFilter := fs.String("kind", "", "Resource kind to filter (function, type, interface, named_type, variable)")
	pkgFilter := fs.String("package", "", "Package path to filter (e.g. aracne/internal/cli)")
	exportedOnly := fs.Bool("exported-only", false, "Only report exported dead code (possible external users)")
	certainOnly := fs.Bool("certain-only", false, "Only report unexported dead code (safe to delete)")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	dbPath := fs.String("db", ".aracne/topology.db", "Topology database path")
	fs.Parse(args)

	mgr, _ := InitRegistry(*dbPath)

	topo, err := mgr.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading topology: %v\n", err)
		os.Exit(1)
	}

	gt := golang.FromGeneric(topo)
	if gt == nil {
		fmt.Fprintln(os.Stderr, "Error: topology is not a Go project")
		os.Exit(1)
	}

	report := golang.FindDeadResources(gt)

	if *kindFilter != "" {
		kind, err := parseDeadCodeKind(*kindFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		report = report.FilterByKinds(kind)
	}

	if *pkgFilter != "" {
		report = report.FilterByPackage(*pkgFilter)
	}

	if *certainOnly {
		report = report.FilterCertain()
	}

	if *exportedOnly {
		report = report.FilterPossible()
	}

	if *jsonOutput {
		type jsonEntry struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Name       string `json:"name"`
			File       string `json:"file"`
			Line       int    `json:"line"`
			Confidence string `json:"confidence"`
			Reason     string `json:"reason"`
		}
		var entries []jsonEntry
		for _, res := range report.Resources {
			entries = append(entries, jsonEntry{
				ID:         res.ID,
				Kind:       string(res.Kind),
				Name:       res.Name,
				File:       res.Location.Path,
				Line:       res.Location.StartsAt,
				Confidence: string(res.Confidence),
				Reason:     res.Reason,
			})
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Println(string(data))
		return
	}

	certain, possible := report.Count()
	total := certain + possible

	if total == 0 {
		fmt.Println("No dead code found.")
		return
	}

	fmt.Printf("Found %d dead resource(s) (%d certain, %d possible):\n\n", total, certain, possible)

	if certain > 0 {
		fmt.Println("Certain (unexported — safe to delete):")
		for _, res := range report.Resources {
			if res.Confidence != golang.DeadCertain {
				continue
			}
			loc := fmt.Sprintf("%s:%d", shortenPath(res.Location.Path), res.Location.StartsAt)
			fmt.Printf("  [%s] %s (%s)\n", res.Kind, res.ID, loc)
		}
		fmt.Println()
	}

	if possible > 0 {
		fmt.Println("Possible (exported — may have external users):")
		for _, res := range report.Resources {
			if res.Confidence != golang.DeadPossible {
				continue
			}
			loc := fmt.Sprintf("%s:%d", shortenPath(res.Location.Path), res.Location.StartsAt)
			fmt.Printf("  [%s] %s (%s)\n", res.Kind, res.ID, loc)
		}
		fmt.Println()
	}
}

func parseDeadCodeKind(value string) (domain.ResourceKind, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	s = strings.TrimSuffix(s, "s")
	switch s {
	case "function", "method":
		return domain.ResourceFunction, nil
	case "type", "struct":
		return domain.ResourceType, nil
	case "interface":
		return domain.ResourceInterface, nil
	case "named_type":
		return domain.ResourceNamedType, nil
	case "variable", "external_var", "extvar":
		return domain.ResourceVariable, nil
	default:
		return "", fmt.Errorf("unknown resource kind %q (valid: function, type, interface, named_type, variable)", value)
	}
}

func shortenPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

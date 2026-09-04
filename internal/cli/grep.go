package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Searches file contents with a regex pattern against the topology database, returning
// path:line:match output with the enclosing resource named once above its matches.
func RunGrep(args []string) {
	fs := flag.NewFlagSet("grep", flag.ExitOnError)
	dbPath := fs.String("db", ".aracne/topology.db", "Topology database path")
	glob := fs.String("glob", "", "Filename glob, e.g. '*.go' or '**/*_test.ts'")
	typ := fs.String("type", "", "Language shorthand: go, py, js, ts, rust, java, ...")
	ignoreCase := fs.Bool("i", false, "Case-insensitive match")
	mode := fs.String("output-mode", "content", "content | files_with_matches | count")
	headLimit := fs.Int("head-limit", 0, "Max matching lines (default 200; -1 for no limit)")
	before := fs.Int("B", 0, "Lines of context before each match")
	after := fs.Int("A", 0, "Lines of context after each match")
	context := fs.Int("C", 0, "Lines of context on both sides of each match")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac grep [flags] <pattern> [path]")
		fs.PrintDefaults()
		os.Exit(1)
	}

	path := "."
	if fs.NArg() > 1 {
		path = fs.Arg(1)
	}
	if *context > 0 {
		*before, *after = *context, *context
	}

	manager, _ := InitRegistry(*dbPath)
	topo, err := manager.ReadAll()
	if err != nil {
		topo = nil
	}

	// Honour the project's own scan.ignore rules, so a search does not descend into build
	// output the scanner has been told to skip, and grep.description_kinds, which limits
	// which kinds may match on their description. A nil kind slice means "not
	// configured" and lets topogrep apply its defaults.
	var ignore *domain.IgnoreMatcher
	var descriptionKinds []domain.ResourceKind
	lineRange := false
	cfg := helper.LoadConfig(helper.ConfigPath(*dbPath))
	if cfg != nil {
		descriptionKinds = cfg.Grep.DescriptionKinds
		lineRange = cfg.LineRangeIdentification()
		if topo != nil && topo.Root != "" {
			ignore = domain.BuildIgnoreMatcher(topo.Root, cfg.Scan.Ignore)
		}
	}

	opt := topogrep.Options{
		Pattern:    fs.Arg(0),
		Root:       path,
		Glob:       *glob,
		Type:       *typ,
		IgnoreCase: *ignoreCase,
		Mode:       topogrep.OutputMode(*mode),
		HeadLimit:  *headLimit,
		Before:     *before,
		After:      *after,
		Ignore:     ignore,
		LineRange:  lineRange,

		DescriptionKinds: descriptionKinds,
	}
	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Same lazy fill the MCP grep tool runs: the CLI and the tool answer the same question,
	// so they must answer it with the same descriptions.
	lazydesc.New(manager, cfg, "").FillSearch(topo, res)
	fmt.Println(topogrep.FormatResult(res, opt))
}

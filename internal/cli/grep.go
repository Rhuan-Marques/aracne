package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Searches file contents with a regex pattern against the topology database, returning
// path:line:match output with the enclosing resource named once above its matches.
func RunGrep(args []string) {
	fs := flag.NewFlagSet("grep", flag.ExitOnError)
	dbPath := fs.String("db", DefaultDBRelative, "Topology database path")
	glob := fs.String("glob", "", "Filename glob, e.g. '*.go' or '**/*_test.ts'")
	typ := fs.String("type", "", "Language shorthand: go, py, js, ts, rust, java, ...")
	ignoreCase := fs.Bool("i", false, "Case-insensitive match")
	mode := fs.String("output-mode", "content", "content | files_with_matches | count")
	headLimit := fs.Int("head-limit", 0, "Max matching lines (default 200; -1 for no limit)")
	before := fs.Int("B", 0, "Lines of context before each match")
	after := fs.Int("A", 0, "Lines of context after each match")
	context := fs.Int("C", 0, "Lines of context on both sides of each match")
	fs.Parse(args)
	*dbPath = ProjectDBPath(*dbPath)
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
		Globs:      splitGlobList(*glob),
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
	out := topogrep.FormatResult(res, opt)
	// The over-serve ceiling, against what a plain grep would have printed. There is no real
	// command to hand back to here, so the fallback is this same result rendered the way grep
	// would have rendered it.
	if topogrep.HasTextualMatch(res) &&
		!cfg.WithinOverserve(out, topogrep.RawBytes(res, opt), helper.OverserveSearchFree) {
		out = topogrep.FormatResult(topogrep.WithoutTopology(res), opt)
	}
	// grep's exit vocabulary. `arac cmd -- grep` has always answered this way and the
	// contract points models at `arac grep`, which always exited 0 -- so a script or an
	// agent branching on the status could not tell "found nothing" from "found something",
	// and the "no matches" prose landed on stdout where a `$(...)` capture turns it into a
	// filename. The message belongs on stderr and the status belongs to the result.
	if !res.Found(opt.Mode) {
		if strings.TrimSpace(out) != "" {
			fmt.Fprintln(os.Stderr, out)
		}
		os.Exit(1)
	}
	fmt.Println(out)
}

// splitGlobList turns the --glob flag into the list topogrep takes, so the CLI can pass more
// than one the way grep's repeatable --include does.
func splitGlobList(glob string) []string {
	var out []string
	for _, g := range strings.Split(glob, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

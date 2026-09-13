package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/shellcmd"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
)

// Searches file contents with a regex pattern against the topology database, returning
// path:line:match output with the enclosing resource named once above its matches.
func RunGrep(args []string) {
	os.Exit(runGrep(args, os.Stdout, os.Stderr))
}

// runGrep is RunGrep with its streams and exit status handed back, so it can be tested.
//
// IT RESOLVES ITS OPERANDS THE WAY `arac cmd -- grep` DOES, through grepRoots. It used to take
// fs.Arg(1) as THE path and hand topogrep a single root, so `arac grep pat a.go b.go` searched
// a.go, dropped b.go and exited 0; and a resource id -- which the intercepted shell grep scopes
// to that declaration -- failed with a raw `stat path:` error. This is the surface the guard's
// nudge names after a native Grep, so it has to be at least as capable as the grep it is
// recommended over.
func runGrep(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", DefaultDBRelative, "Topology database path")
	glob := fs.String("glob", "", "Filename glob(s), comma-separated, e.g. '*.go' or '**/*_test.ts'")
	exclude := fs.String("exclude", "", "Filename glob(s) to skip, comma-separated")
	excludeDir := fs.String("exclude-dir", "", "Directory name glob(s) to skip, comma-separated")
	typ := fs.String("type", "", "Language shorthand: go, py, js, ts, rust, java, ...")
	ignoreCase := fs.Bool("i", false, "Case-insensitive match")
	fixed := fs.Bool("F", false, "Treat the pattern as a literal string, not a regex")
	word := fs.Bool("w", false, "Match whole words (the pattern must be made of word characters)")
	line := fs.Bool("x", false, "Match whole lines")
	maxCount := fs.Int("m", 0, "At most N matches from each file (0 = no cap)")
	mode := fs.String("output-mode", "content", "content | files_with_matches | count")
	headLimit := fs.Int("head-limit", 0, "Max matching lines (default 200; -1 for no limit)")
	before := fs.Int("B", 0, "Lines of context before each match")
	after := fs.Int("A", 0, "Lines of context after each match")
	context := fs.Int("C", 0, "Lines of context on both sides of each match")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	*dbPath = ProjectDBPath(*dbPath)
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "Usage: arac grep [flags] <pattern> [path | resource-id]...")
		fs.PrintDefaults()
		return 2
	}
	pattern := fs.Arg(0)
	if *word && !shellcmd.WordSafe(pattern) {
		// Wrapping anything else in \b…\b finds fewer matches than grep -w; see shellcmd.wordSafe.
		fmt.Fprintf(stderr, "arac grep: -w needs a pattern made of word characters, got %q\n", pattern)
		return 2
	}
	// topogrep renders anything it does not recognise as content, uncapped -- so a typo such as
	// `-output-mode count_only` used to print every matching line and exit 0.
	switch topogrep.OutputMode(*mode) {
	case topogrep.OutputContent, topogrep.OutputFiles, topogrep.OutputCount:
	default:
		fmt.Fprintf(stderr, "arac grep: unknown -output-mode %q (want content, files_with_matches or count)\n", *mode)
		return 2
	}
	if *context > 0 {
		*before, *after = *context, *context
	}

	manager, _ := InitRegistry(*dbPath)
	topo, err := manager.ReadAll()
	if err != nil {
		// Degrading to a plain content search is right -- the matches are still the matches,
		// and refusing to search because the graph is broken would be worse. Doing it SILENTLY
		// is not: the node-name and description tiers simply vanish, so a query that only ever
		// matched a description comes back empty and reads as "no such code". Said on stderr,
		// so the result on stdout stays exactly what a caller parses.
		fmt.Fprintf(stderr, "arac grep: the topology could not be read (%v); searching file contents only. "+
			"Name and description matches are unavailable until it is rebuilt with `arac scan --hard`.\n", err)
		topo = nil
	}
	cfg := helper.LoadConfig(helper.ConfigPath(*dbPath))

	operands := fs.Args()[1:]
	roots, restrict, ok := grepRoots(manager, operands, cfg)
	if !ok {
		fmt.Fprintf(stderr, "arac grep: %s: no such file, directory or resource\n", strings.Join(operands, ", "))
		return 2
	}

	// Pruning is the .gitignore hierarchy's, applied inside topogrep; scan.ignore is the
	// scanner's rule about the topology and deliberately has no say over what a search can
	// reach on disk. grep.description_kinds limits which kinds may match on their
	// description; a nil slice means "not configured" and lets topogrep apply its defaults.
	opt := topogrep.Options{
		Pattern:      anchorPattern(pattern, *fixed, *word, *line),
		Roots:        roots,
		Globs:        splitGlobList(*glob),
		ExcludeGlobs: splitGlobList(*exclude),
		ExcludeDirs:  splitGlobList(*excludeDir),
		Type:         *typ,
		IgnoreCase:   *ignoreCase,
		Mode:         topogrep.OutputMode(*mode),
		HeadLimit:    *headLimit,
		PerFileLimit: max(*maxCount, 0),
		Before:       *before,
		After:        *after,
		LineRange:    cfg.LineRangeIdentification(),

		DescriptionKinds: cfg.Grep.DescriptionKinds,
	}
	// The span goes on the search, not on its result, so the head limit counts the resource's
	// own matches. See scopeToSpan.
	scopeToSpan(&opt, restrict)
	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
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
			fmt.Fprintln(stderr, out)
		}
		return 1
	}
	fmt.Fprintln(stdout, out)
	return 0
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

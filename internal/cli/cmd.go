package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/shellcmd"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// RunCmd is `arac cmd -- <command…>`: run a shell read or search, answered from the topology
// when aracne has something better to say and by the real binary when it does not.
//
// WHY THIS IS A CLI VERB. The guard hook rewrites an intercepted command into this, so the
// two must agree exactly on what is modelled -- a hook that rewrites something this verb then
// passes through spends a subprocess for nothing. Making it a real command means that contract
// is testable from a shell, a human gets the same behaviour as an agent, and the topology load
// happens in a subprocess instead of inside the hook's timeout.
//
// The passthrough branch is not a failure mode, it is half the product: `arac cmd -- xxd f`,
// `arac cmd -- head -5 CHANGELOG.md` and `arac cmd -- tail -f log` must all be byte-identical
// to running the command directly, or interception cannot be safely turned on by default.
func RunCmd(args []string) {
	argv := args
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: arac cmd -- <command> [args...]")
		fmt.Fprintln(os.Stderr, "Runs the command, answering it from the topology when aracne can.")
		os.Exit(1)
	}

	if out, status, ok := serveCommand(argv); ok {
		fmt.Print(out)
		// A search that found nothing exits 1, the way grep and rg do. Callers -- and agents
		// -- branch on that status, so an answer that always succeeds is a changed command.
		if status != 0 {
			os.Exit(status)
		}
		return
	}
	passthrough(argv)
}

// serveCommand returns aracne's answer for a command, or false to run the real thing.
//
// Every gate below returns false rather than an approximation. The command the caller typed is
// always a correct answer; a topology-framed answer to a question aracne misread is not.
func serveCommand(argv []string) (string, int, bool) {
	req := shellcmd.Parse(argv)
	if req.Kind == shellcmd.KindPassthrough {
		return "", 0, false
	}
	dbPath := guardDBPath("")
	if !fileExists(dbPath) {
		return "", 0, false
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	// Reads are answered only in the two intercepting modes. Searches are answered in all
	// four: the annotated grep reaches node names and stored descriptions, which no other
	// surface offers and no plain grep can find, so there is never a mode in which handing a
	// search back to the real binary is the better answer.
	if req.Kind == shellcmd.KindRead && !cfg.InterceptReads() {
		return "", 0, false
	}

	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return "", 0, false
	}
	// The registry is passed so a read can re-parse a single file whose recorded span has
	// drifted, rather than handing back a range error the caller cannot act on.
	rd := universaltools.NewRead(mgr, cfg, false, NewScannerRegistry())

	switch req.Kind {
	case shellcmd.KindRead:
		out, ok := serveRead(rd, cfg, req, dbPath)
		return out, 0, ok
	case shellcmd.KindGrep:
		return serveGrep(mgr, cfg, req)
	}
	return "", 0, false
}

// serveRead answers a read command. Every operand must be answerable: a half-enhanced
// `cat a.go b.go`, part topology and part raw bytes, is harder to read than either.
func serveRead(rd *universaltools.Read, cfg *helper.Config,
	req shellcmd.Request, dbPath string) (string, bool) {

	blocks := make([]string, 0, len(req.Operands))
	for _, operand := range req.Operands {
		out, ok := serveOneRead(rd, cfg, req, operand, dbPath)
		if !ok {
			return "", false
		}
		blocks = append(blocks, out)
	}
	if len(blocks) == 0 {
		return "", false
	}
	answer := strings.Join(blocks, "\n")
	if !withinBudget(answer, rawWindowBytes(req, req.Operands), cfg) {
		return "", false
	}
	return answer, true
}

// serveOneRead answers a read for a single operand: a path aracne indexes, or a resource ID.
func serveOneRead(rd *universaltools.Read, cfg *helper.Config,
	req shellcmd.Request, operand, dbPath string) (string, bool) {

	path, from, to, ok := resolveReadOperand(rd, cfg, req, operand, dbPath)
	if !ok {
		return "", false
	}
	// A whole-file (or whole-resource) request goes through the ordinary read, which already
	// applies read.file_mode. Re-implementing it here as a 1..N slice would emit the file
	// verbatim plus a context block -- strictly more than `cat`, which is the exact regression
	// skeleton mode exists to prevent.
	if req.Window.Mode == shellcmd.WholeFile {
		out, err := rd.ReadIDs([]string{operand}, universaltools.ReadIDsOptions{Kinds: helper.AllReadKinds()})
		if err != nil || strings.TrimSpace(out) == "" {
			return "", false
		}
		return out, true
	}
	out, err := rd.ReadSlice(path, from, to)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}

// resolveReadOperand turns one operand plus the command's window into an absolute line range
// in a file, or reports that aracne should stay out of the way.
//
// The two cases the brief calls out are both here. A path aracne indexes is windowed against
// the FILE; a resource ID is windowed against that resource's BODY, so `head -20 app.Flask`
// means the first twenty lines of the class rather than of whatever file holds it.
func resolveReadOperand(rd *universaltools.Read, cfg *helper.Config, req shellcmd.Request,
	operand, dbPath string) (path string, from, to int, ok bool) {

	if info, err := os.Stat(operand); err == nil {
		if info.IsDir() {
			return "", 0, 0, false
		}
		abs, absErr := filepath.Abs(operand)
		if absErr != nil {
			return "", 0, 0, false
		}
		// An unindexed file is Case 3: aracne would answer it with the same raw bytes the
		// command prints, minus the window. There is nothing to trade for the interception.
		tracked, tErr := helper.TrackedFiles(dbPath, []string{abs})
		if tErr != nil || !tracked[abs] {
			return "", 0, 0, false
		}
		total := countFileLines(abs)
		if total == 0 {
			return "", 0, 0, false
		}
		f, t, wOK := resolveWindow(req.Window, 1, total)
		return abs, f, t, wOK
	}

	// Not a path on disk. It may be a resource ID -- the one operand a plain shell command
	// could never answer at all.
	//
	// Deliberately ungated by mode. Whether aracne ADVERTISES ids is a contract decision that
	// ModeInterceptLineRanges answers differently from ModeInterceptID; whether it ACCEPTS one, having
	// already decided to answer this command, is not a decision at all. Refusing an id here
	// would refuse a question aracne can answer, in favour of a `cat` that will fail.
	rPath, bodyFrom, bodyTo, err := rd.BodyBounds(operand)
	if err != nil || rPath == "" {
		return "", 0, 0, false
	}
	f, t, wOK := resolveWindow(req.Window, bodyFrom, bodyTo)
	return rPath, f, t, wOK
}

// resolveWindow maps a command's window onto an absolute, inclusive line range inside
// [lo, hi] -- the whole file for a path, or a resource's body for an ID.
//
// A line number in the command counts from the start of whatever was named, so it is offset by
// lo. For a file lo is 1 and the offset vanishes, which is why the same arithmetic serves both
// and no "is this a resource?" flag is needed: `sed -n '5,9p' app.py` is lines 5-9 of the file,
// and `sed -n '5,9p' app.Flask` is lines 5-9 of the class body.
func resolveWindow(w shellcmd.Window, lo, hi int) (from, to int, ok bool) {
	if lo < 1 || hi < lo {
		return 0, 0, false
	}
	switch w.Mode {
	case shellcmd.WholeFile:
		return lo, hi, true
	case shellcmd.Head:
		if w.N < 1 {
			return 0, 0, false
		}
		return lo, min(hi, lo+w.N-1), true
	case shellcmd.Tail:
		if w.N < 1 {
			return 0, 0, false
		}
		return max(lo, hi-w.N+1), hi, true
	case shellcmd.FromLine:
		// A window past the end is not an error -- `tail -n +900` on a 40-line file prints
		// nothing -- but there is no slice to frame, so the real command says it better.
		start := lo + w.From - 1
		if start > hi {
			return 0, 0, false
		}
		return start, hi, true
	case shellcmd.Range:
		start, end := lo+w.From-1, lo+w.To-1
		if start > hi {
			return 0, 0, false
		}
		return start, min(end, hi), true
	}
	return 0, 0, false
}

// serveGrep answers a search with the topology-annotated grep, which finds what a plain grep
// cannot: node names and stored descriptions.
func serveGrep(mgr *topology.TopologyManager, cfg *helper.Config, req shellcmd.Request) (string, int, bool) {
	topo, err := mgr.ReadAll()
	if err != nil || topo == nil {
		return "", 0, false
	}

	root := "."
	var restrict *domain.Location
	switch len(req.Operands) {
	case 0:
		// Only reached for the tools whose path-less form already means "the tree"; see
		// shellcmd.searchesCwdByDefault.
	case 1:
		r, loc, ok := grepScope(mgr, req.Operands[0], cfg)
		if !ok {
			return "", 0, false
		}
		root, restrict = r, loc
	default:
		// Several roots is a shape topogrep has no option for, and merging separate searches
		// would misreport the caps that make its output bounded.
		return "", 0, false
	}

	pattern := req.Grep.Pattern
	if req.Grep.Fixed {
		pattern = regexpQuote(pattern)
	}
	if req.Grep.WholeWord {
		pattern = `\b(?:` + pattern + `)\b`
	}

	opt := topogrep.Options{
		Pattern:          pattern,
		Root:             root,
		Glob:             req.Grep.Glob,
		Type:             req.Grep.Type,
		IgnoreCase:       req.Grep.IgnoreCase,
		Mode:             topogrep.OutputMode(req.Grep.Mode),
		HeadLimit:        req.Grep.HeadLimit,
		Before:           req.Grep.Before,
		After:            req.Grep.After,
		DescriptionKinds: cfg.Grep.DescriptionKinds,
		LineRange:        cfg.LineRangeIdentification(),
	}
	if topo.Root != "" {
		opt.Ignore = domain.BuildIgnoreMatcher(topo.Root, cfg.Scan.Ignore)
	}
	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		return "", 0, false
	}
	if restrict != nil {
		restrictResultToRange(res, restrict.StartsAt, restrict.EndsAt)
	}
	// After the range restriction, not before: a node the window dropped is a node this
	// answer will never print, and describing it would be paying for a line nobody sees.
	lazydesc.New(mgr, cfg, "").FillSearch(topo, res)
	status := 0
	if len(res.Matches) == 0 {
		status = 1
	}
	return strings.TrimRight(topogrep.FormatResult(res, opt), "\n") + "\n", status, true
}

// grepScope turns a search's single operand into a root path, plus the line range to keep when
// the operand named a resource rather than a directory or file.
func grepScope(mgr *topology.TopologyManager, operand string, cfg *helper.Config) (string, *domain.Location, bool) {
	if _, err := os.Stat(operand); err == nil {
		return operand, nil, true
	}
	// Same reasoning as resolveReadOperand: an operand that is not a path may still be a
	// resource, and scoping a search to one declaration is a question no plain grep has.
	topo, err := mgr.ReadAll()
	if err != nil {
		return "", nil, false
	}
	res, ok := topo.Resources[helper.NormalizeResourceID(operand)]
	if !ok {
		// Fall back to the forgiving resolver so a trailing-part id works here too.
		rd := universaltools.NewRead(mgr, cfg, false, nil)
		path, from, to, bErr := rd.BodyBounds(operand)
		if bErr != nil {
			return "", nil, false
		}
		return displayRoot(path), &domain.Location{Path: path, StartsAt: from, EndsAt: to}, true
	}
	if res.Location.Path == "" {
		return "", nil, false
	}
	if res.Kind == domain.ResourceFile {
		return displayRoot(res.Location.Path), nil, true
	}
	return displayRoot(res.Location.Path), &res.Location, true
}

// displayRoot spells a search root the way the caller would have. topogrep echoes the root it
// walked into every result row, so handing it the absolute path a resource carries turns a
// one-line hit into a full filesystem path -- pure width, repeated per match.
func displayRoot(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, relErr := filepath.Rel(wd, path)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// restrictResultToRange narrows a search to the lines of one resource, recomputing the
// accounting so the rendered trailer still describes what was actually found.
func restrictResultToRange(res *topogrep.Result, from, to int) {
	if res == nil || from < 1 || to < from {
		return
	}
	kept := res.Matches[:0]
	files := map[string]bool{}
	counts := map[string]int{}
	resources := map[string]bool{}
	for _, m := range res.Matches {
		if m.Line < from || m.Line > to {
			continue
		}
		kept = append(kept, m)
		files[m.Path] = true
		counts[m.Path]++
		if !m.NodeHit && m.ResourceID != "" {
			resources[m.ResourceID] = true
		}
	}
	res.Matches = kept
	res.Files = res.Files[:0]
	for p := range files {
		res.Files = append(res.Files, p)
	}
	res.Counts = counts
	res.Total = len(kept)
	res.Truncated = false
	res.DistinctResources = len(resources)
}

// regexpQuote escapes a literal pattern for `-F`/`fgrep`, where the caller expects no regex
// interpretation at all.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// rawWindowBytes estimates what the real command would have printed, which is the denominator
// the over-serve budget is measured against.
func rawWindowBytes(req shellcmd.Request, operands []string) int {
	total := 0
	for _, operand := range operands {
		abs, err := filepath.Abs(operand)
		if err != nil {
			continue
		}
		info, statErr := os.Stat(abs)
		if statErr != nil {
			continue
		}
		lines := countFileLines(abs)
		if lines == 0 {
			continue
		}
		want := lines
		switch req.Window.Mode {
		case shellcmd.Head, shellcmd.Tail:
			want = req.Window.N
		case shellcmd.Range:
			want = req.Window.To - req.Window.From + 1
		case shellcmd.FromLine:
			want = lines - req.Window.From + 1
		}
		if want < 1 {
			want = 1
		}
		if want > lines {
			want = lines
		}
		total += int(info.Size()) * want / lines
	}
	return total
}

// withinBudget bounds the answer against what was asked for.
//
// The same trade guard_proxy.go makes: past a few multiples of the request, an enriched read is
// no longer a cheaper read -- it is a way to spend the context window on one `head -1`. Below
// the floor everything passes, because a small window legitimately expands to its enclosing
// signature plus a context block, and refusing that would disable the feature for the case it
// exists to serve.
func withinBudget(answer string, rawBytes int, cfg *helper.Config) bool {
	factor := cfg.EffectiveTerminalMaxOverserve()
	if factor <= 0 {
		return true
	}
	budget := rawBytes * factor
	if budget < proxyMinBudget {
		budget = proxyMinBudget
	}
	if budget > proxyMaxBytes {
		budget = proxyMaxBytes
	}
	return len(answer) <= budget
}

// passthrough runs the command the caller actually typed and exits with its status.
//
// This is what makes interception safe to ship on by default: whenever aracne is not certain
// it has a better answer, the shell behaves exactly as it would have.
func passthrough(argv []string) {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "arac cmd: %s: command not found\n", argv[0])
		os.Exit(127)
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "arac cmd: %v\n", runErr)
		os.Exit(1)
	}
}

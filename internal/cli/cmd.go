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
	"golang.org/x/term"
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
	// `--piped` is the guard telling this verb that a pipeline, not the model, reads the
	// answer -- so it must be the rows a real command would have printed and nothing else.
	// See serveGrep and topogrep.Options.Plain.
	argv, piped := parseCmdFlags(args)
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: arac cmd [--piped] -- <command> [args...]")
		fmt.Fprintln(os.Stderr, "Runs the command, answering it from the topology when aracne can.")
		fmt.Fprintln(os.Stderr, "  --piped  the answer feeds a pipeline: render it as the plain command would")
		os.Exit(1)
	}

	out, status, refusal, ok := serveCommand(argv, piped)
	// A refusal is aracne saying no to a command it UNDERSTOOD, which is a different answer
	// from having nothing to add. It goes to stderr with a non-zero status because that is
	// what the command it replaced would have done, and because passing it through instead
	// would run `cat` on a resource id and report "No such file or directory" -- a true
	// sentence about the wrong thing.
	if refusal != nil {
		fmt.Fprintf(os.Stderr, "arac cmd: %v\n", refusal)
		os.Exit(1)
	}
	if ok {
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

// parseCmdFlags splits `arac cmd`'s own flags from the command it is to run.
//
// Everything after `--` is the command, verbatim. Before it, only the flags this verb defines
// are read; anything else ends the flag list, so a caller who forgot the `--` still gets their
// command run rather than a usage error about its first argument.
func parseCmdFlags(args []string) (argv []string, piped bool) {
	for len(args) > 0 {
		switch args[0] {
		case "--":
			return args[1:], piped
		case "--piped":
			piped = true
			args = args[1:]
		default:
			return args, piped
		}
	}
	return nil, piped
}

// serveCommand returns aracne's answer for a command, or false to run the real thing.
//
// Every gate below returns false rather than an approximation. The command the caller typed is
// always a correct answer; a topology-framed answer to a question aracne misread is not.
//
// The one exception is the refusal in the third return. "aracne has nothing to add" and
// "read.kinds says this may not be read here" are opposite answers that were once the same
// `false`: the first hands the command back to the shell, and the second must not, because
// the shell would then answer a question about a resource id as though it were a filename.
func serveCommand(argv []string, piped bool) (out string, status int, refusal error, ok bool) {
	req := shellcmd.Parse(argv)
	if req.Kind == shellcmd.KindPassthrough {
		return "", 0, nil, false
	}
	// A COMMAND THAT WOULD NOT HAVE RUN IS NOT A QUESTION ARACNE WAS ASKED.
	//
	// aracne answers IN PLACE OF the real command, and the whole safety argument for
	// interception is that whatever it does not model runs exactly as it would have. Serving
	// a command whose binary is not installed inverts that: on a box without ripgrep,
	// `rg foo` came back as a full annotated search with exit 0, so the model concluded
	// ripgrep was available -- and the next `rg` carrying a flag shellcmd does not model
	// (`-o`, `--files`, `--hidden`) fell through to passthrough and returned
	// "rg: command not found", exit 127. aracne had manufactured a capability and then
	// withdrawn it unpredictably. Falling through here reproduces the real failure exactly,
	// because passthrough() is what produces it.
	if !commandIsAvailable(argv[0]) {
		return "", 0, nil, false
	}
	// A PATH-LESS rg READS ITS STDIN WHEN STDIN HOLDS DATA. ripgrep, ag and ack walk the working
	// directory only when stdin is a terminal or /dev/null -- which is what an agent's shell
	// hands them -- and search the file or pipe they were given otherwise; ugrep reads any stdin
	// that is not a terminal. Parse cannot tell which, because it does no I/O, but this process
	// was handed exactly the stdin the real command would have been, so it can. Answering
	// `rg retry < README.md` with a search of the tree returned five files' matches for a
	// question about one.
	if req.Kind == shellcmd.KindGrep && stdinWouldBeSearched(req.Grep.Stdin, os.Stdin) {
		return "", 0, nil, false
	}
	dbPath := guardDBPath("")
	if !fileExists(dbPath) {
		return "", 0, nil, false
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	// Reads are answered only in the two intercepting modes. Searches are answered in all
	// four: the annotated grep reaches node names and stored descriptions, which no other
	// surface offers and no plain grep can find, so there is never a mode in which handing a
	// search back to the real binary is the better answer.
	if req.Kind == shellcmd.KindRead && !cfg.InterceptReads() {
		return "", 0, nil, false
	}

	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return "", 0, nil, false
	}
	// The registry is passed so a read can re-parse a single file whose recorded span has
	// drifted, rather than handing back a range error the caller cannot act on.
	rd := universaltools.NewRead(mgr, cfg, false, NewScannerRegistry())

	switch req.Kind {
	case shellcmd.KindRead:
		out, refusal, ok := serveRead(rd, cfg, req, dbPath)
		return out, 0, refusal, ok
	case shellcmd.KindGrep:
		gOut, gStatus, gOK := serveGrep(mgr, cfg, req, argv[0], piped)
		return gOut, gStatus, nil, gOK
	}
	return "", 0, nil, false
}

// shellResolvedCommands are the commands a shell resolves itself rather than finding on PATH,
// so exec.LookPath is the wrong question for them.
//
// One entry, and it earns it: `Get-Content` is the PowerShell reader shellcmd models, and it
// is a cmdlet -- there is no executable of that name anywhere, on any machine. Judging it by
// LookPath would refuse to serve the one shape the PowerShell surface has, and hand it to a
// passthrough that cannot run it either.
var shellResolvedCommands = map[string]bool{"get-content": true}

// commandIsAvailable reports whether the shell could actually have run this command word.
func commandIsAvailable(word string) bool {
	if shellResolvedCommands[shellcmd.Base(word)] {
		return true
	}
	_, err := exec.LookPath(word)
	return err == nil
}

// stdinWouldBeSearched reports whether the real command, handed this stdin, would search it
// instead of the working directory. See shellcmd.StdinRule for which test each tool applies.
func stdinWouldBeSearched(rule shellcmd.StdinRule, stdin *os.File) bool {
	if stdin == nil {
		return false
	}
	switch rule {
	case shellcmd.StdinIfData:
		// ripgrep's own test: a regular file, a pipe or a socket is data someone put there. A
		// terminal and /dev/null -- the Bash tool's stdin -- are character devices, and rg
		// walks the tree for both, as it does for a stdin it cannot even stat.
		info, err := stdin.Stat()
		if err != nil {
			return false
		}
		mode := info.Mode()
		return mode.IsRegular() || mode&(os.ModeNamedPipe|os.ModeSocket) != 0
	case shellcmd.StdinUnlessTerminal:
		return !term.IsTerminal(int(stdin.Fd()))
	}
	return false
}

// serveRead answers a read command. Every operand must be answerable: a half-enhanced
// `cat a.go b.go`, part topology and part raw bytes, is harder to read than either.
//
// One refused operand refuses the whole command, and for the same reason: `cat a.go pkg.Thing`
// cannot be half an answer and half a "no".
func serveRead(rd *universaltools.Read, cfg *helper.Config,
	req shellcmd.Request, dbPath string) (string, error, bool) {

	// SEVERAL OPERANDS ARE SEVERAL ANSWERS, and only for the readers where that is true.
	// `cat`, `head` and `tail` restart at line 1 per file, so answering each in turn means
	// what the command means; the group header above each block carries the filename that
	// head's own `==> f <==` banner would have. `sed` and `awk` number lines across the
	// whole concatenation and never reach here with more than one operand -- shellcmd
	// passes those through rather than answer them per file.
	blocks := make([]string, 0, len(req.Operands))
	resolved := make([]domain.Location, 0, len(req.Operands))
	// The first operand answered as a RESOURCE ID, if any: the shell cannot serve that one, so
	// an answer too large to give is refused rather than handed to it. See the budget below.
	idOperand, idLoc := "", domain.Location{}
	for _, operand := range req.Operands {
		path, from, to, refusal, ok := resolveReadOperand(rd, cfg, req, operand, dbPath)
		if refusal != nil {
			return "", fmt.Errorf("%s: %w", operand, refusal), false
		}
		if !ok {
			// The operand resolved but its window falls outside what is there. That is a
			// real, empty answer -- `tail -n +900` on a 40-line file prints nothing -- and
			// NOT a reason to hand the command back: for a resource id the real command
			// would then report "No such file or directory" about an id, which is a true
			// sentence about the wrong thing. See resolveReadOperand.
			if path != "" {
				continue
			}
			return "", nil, false
		}
		out, err := renderReadOperand(rd, req, operand, path, from, to)
		if err != nil || strings.TrimSpace(out) == "" {
			return "", nil, false
		}
		blocks = append(blocks, out)
		resolved = append(resolved, domain.Location{Path: path, StartsAt: from, EndsAt: to})
		if _, statErr := os.Stat(operand); statErr != nil && idOperand == "" {
			idOperand, idLoc = operand, domain.Location{Path: path, StartsAt: from, EndsAt: to}
		}
	}
	if len(blocks) == 0 {
		// Every operand resolved to an empty window. The command printed nothing, and so
		// does this -- with a zero exit status, which is what it would have had.
		if len(req.Operands) > 0 {
			return "", nil, true
		}
		return "", nil, false
	}
	answer := strings.Join(blocks, "\n")
	if raw := rawWindowBytes(resolved); !withinBudget(answer, raw, cfg) {
		// OVER BUDGET IS NOT "NOTHING TO ADD" WHEN AN OPERAND IS AN ID. For a path, the real
		// command is a correct answer and passing through is right. For a resource id the
		// shell runs `cat <id>` and reports "No such file or directory" about an id that
		// resolves perfectly well -- the outcome rawWindowBytes was written to prevent,
		// reached anyway through the absolute ceiling (helper.OverserveMaxBytes). So it is
		// refused, with the move that does fit.
		if idOperand != "" {
			return "", overBudgetRefusal(idOperand, idLoc, len(answer),
				cfg.OverserveBudget(raw, helper.OverserveReadFree)), false
		}
		return "", nil, false
	}
	return answer, nil, true
}

// overBudgetRefusal names the size, the ceiling and a read of part of the resource that fits.
func overBudgetRefusal(operand string, loc domain.Location, size, budget int) error {
	kb := func(n int) int { return (n + 1023) / 1024 }
	end := min(loc.EndsAt, loc.StartsAt+99)
	return fmt.Errorf("%s renders to %d KB, over the %d KB ceiling for this read; read part of "+
		"it (`sed -n '%d,%dp' %s`) or raise terminal.max_overserve",
		operand, kb(size), kb(budget), loc.StartsAt, end, displayRoot(loc.Path))
}

// looksLikeFilePath reports whether an operand that is not on disk still reads as a FILE the
// caller mistyped rather than as a resource id: its last segment ends in a short lowercase
// extension (`util.go`, `README.md`). Such a miss keeps the real command's own "No such file or
// directory"; only an id-shaped miss is answered with the resolver's suggestions.
func looksLikeFilePath(operand string) bool {
	base := operand
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	dot := strings.LastIndex(base, ".")
	if dot <= 0 || dot == len(base)-1 || len(base)-dot-1 > 5 {
		return false
	}
	for _, r := range base[dot+1:] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// renderReadOperand renders one already-resolved operand.
//
// A whole-file (or whole-resource) request goes through the ordinary read, which already
// applies read.file_mode. Re-implementing it here as a 1..N slice would emit the file
// verbatim plus a context block -- strictly more than `cat`, which is the exact regression
// skeleton mode exists to prevent.
//
// No Kinds override: read.kinds gates this surface like every other one, and the gate has
// already run in resolveReadOperand -- which is where the WINDOWED branch below can also
// reach it -- so this call agreeing with it is belt and braces rather than the check.
func renderReadOperand(rd *universaltools.Read, req shellcmd.Request,
	operand, path string, from, to int) (string, error) {

	if req.Window.Mode == shellcmd.WholeFile {
		return rd.ReadIDs([]string{operand}, universaltools.ReadIDsOptions{})
	}
	return rd.ReadSlice(path, from, to)
}

// resolveReadOperand turns one operand plus the command's window into an absolute line range
// in a file, or reports that aracne should stay out of the way -- or that read.kinds refuses
// what the operand named.
//
// The two cases the brief calls out are both here. A path aracne indexes is windowed against
// the FILE; a resource ID is windowed against that resource's BODY, so `head -20 app.Flask`
// means the first twenty lines of the class rather than of whatever file holds it.
//
// WHY THE read.kinds GATE LIVES HERE and not one layer down. The whole-file branch would reach
// it on its own -- it calls ReadIDs, which gates. The WINDOWED branch never does: it renders a
// slice of a file through ReadSlice, which is addressed by path and line and knows nothing
// about the id the caller typed. Gating both at the point where the operand is still an
// operand is the only place the two branches can be made to agree.
func resolveReadOperand(rd *universaltools.Read, cfg *helper.Config, req shellcmd.Request,
	operand, dbPath string) (path string, from, to int, refusal error, ok bool) {

	kinds := cfg.EffectiveReadKinds()
	allowed := rd.ReadKinds()

	if info, err := os.Stat(operand); err == nil {
		if info.IsDir() {
			return "", 0, 0, nil, false
		}
		abs, absErr := filepath.Abs(operand)
		if absErr != nil {
			return "", 0, 0, nil, false
		}
		// An unindexed file is Case 3: aracne would answer it with the same raw bytes the
		// command prints, minus the window. There is nothing to trade for the interception.
		tracked, tErr := helper.TrackedFiles(dbPath, []string{abs})
		if tErr != nil || !tracked[abs] {
			return "", 0, 0, nil, false
		}
		// A tracked file read is a read of kind "file", and a project that took "file" out of
		// read.kinds has said not to serve one. Refused rather than passed through: passing
		// through would print the file, which is the thing the setting just declined.
		if !allowed[domain.ResourceFile] {
			return "", 0, 0, universaltools.KindRefusal(domain.ResourceFile, kinds), false
		}
		total := countFileLines(abs)
		if total == 0 {
			return "", 0, 0, nil, false
		}
		f, t, wOK := resolveWindow(req.Window, 1, total)
		// The path is returned whether or not the window landed, so the caller can tell an
		// empty window on a resolved target ("print nothing") from an operand aracne cannot
		// serve at all ("run the real command").
		return abs, f, t, nil, wOK
	}

	// Not a path on disk. It may be a resource ID -- the one operand a plain shell command
	// could never answer at all.
	//
	// Deliberately ungated by mode. Whether aracne ADVERTISES ids is a contract decision that
	// ModeInterceptLineRanges answers differently from ModeInterceptID; whether it ACCEPTS one, having
	// already decided to answer this command, is not a decision at all.
	//
	// It is NOT ungated by kind. An id that resolves to a kind read.kinds excludes is refused
	// here, and the refusal is the answer: handing `cat pkg.SomeType` back to the shell gets
	// "No such file or directory", which reads as "you typed the id wrong" when the truth is
	// that the project does not serve that kind. An id that resolves to NOTHING is the other
	// case and still passes through -- there is no policy in a typo.
	if kind := rd.KindOf(operand); kind != "" && !allowed[kind] {
		return "", 0, 0, universaltools.KindRefusal(kind, kinds), false
	}
	rPath, bodyFrom, bodyTo, err := rd.BodyBounds(operand)
	if err != nil || rPath == "" {
		// A NEAR MISS IS ANSWERED, NOT PASSED THROUGH. Handed to the shell, `cat RunGuardd`
		// reports "No such file or directory" -- a filesystem error about a symbol -- while the
		// resolver already knows the id the caller meant; the intercept_id contract promises
		// "a miss returns the nearest candidates rather than an error". An operand with no
		// candidates at all, or one shaped like a mistyped FILE, still passes through: there is
		// no policy in a typo, and the real command says it better.
		if !looksLikeFilePath(operand) {
			if hint := rd.Suggestion(operand); hint != "" {
				return "", 0, 0, errors.New(hint), false
			}
		}
		return "", 0, 0, nil, false
	}
	f, t, wOK := resolveWindow(req.Window, bodyFrom, bodyTo)
	return rPath, f, t, nil, wOK
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
//
// UNLESS A PIPELINE IS READING IT. Everything aracne ADDS to a search -- the `#` resource
// headers, the rows a node earned on its name or its description, the trailer, the tiered
// ranking, the 200-row cap -- is addressed to a reader, and a pipeline is not one. The guard
// only rewrites a piped search when every stage keeps whole lines in order, and that promise
// only holds if the lines are the ones the real command would have produced: a cap the
// consumer cannot see loses matches silently (measured at 181 real against 120 through the
// pipe), extra header rows are counted and capped alongside the matches, and the ranking
// changes WHICH rows a `| head -N` keeps. Plain mode is those four additions turned off. What
// survives is the reason to intercept at all: the walk still honours scan.ignore and skips the
// trees no search wants.
func serveGrep(mgr *topology.TopologyManager, cfg *helper.Config, req shellcmd.Request, argv0 string, piped bool) (string, int, bool) {
	topo, err := mgr.ReadAll()
	if err != nil || topo == nil {
		return "", 0, false
	}

	roots, restrict, ok := grepRoots(mgr, req.Operands, cfg)
	if !ok {
		return "", 0, false
	}

	pattern := anchorPattern(req.Grep.Pattern, req.Grep.Fixed, req.Grep.WholeWord, req.Grep.WholeLine)

	opt := topogrep.Options{
		Pattern:          pattern,
		Roots:            roots,
		Globs:            req.Grep.Globs,
		ExcludeGlobs:     req.Grep.ExcludeGlobs,
		ExcludeDirs:      req.Grep.ExcludeDirs,
		Type:             req.Grep.Type,
		IgnoreCase:       req.Grep.IgnoreCase,
		WithFilename:     req.Grep.WithFilename,
		LineNumbers:      req.Grep.LineNumbers,
		Mode:             topogrep.OutputMode(req.Grep.Mode),
		PerFileLimit:     req.Grep.MaxCount,
		Before:           req.Grep.Before,
		After:            req.Grep.After,
		DescriptionKinds: cfg.Grep.DescriptionKinds,
		LineRange:        cfg.LineRangeIdentification(),
		// The shell surface answers the way grep answers: nothing at all when nothing
		// matched, a bare number for one file's count, cap advice spelled in flags a
		// caller can actually type. See topogrep.Options.Terse.
		Terse: true,
		Plain: piped,
		// -R, and the filename filters as grep applies them: in order, and to the operands too.
		FollowLinks: req.Grep.FollowLinks,
		GrepFilters: gnuGrepFamily[req.Name],
		CountZeros:  countsZeros[req.Name],
		// grep in a UTF-8 locale and rg both read `é` as a word character; Go does not. A word
		// test that lands beside one is left to the real command.
		UnicodeWords: true,
	}
	if opt.GrepFilters {
		for _, f := range req.Grep.Filters {
			opt.NameFilters = append(opt.NameFilters, topogrep.NameFilter{Glob: f.Glob, Exclude: f.Exclude})
		}
	}
	// RIPGREP DECIDES WHICH FILES IT SEARCHES, and no walk here reproduces how: .gitignore,
	// .ignore and .rgignore at every level and above the root, the global excludes, hidden files,
	// its own glob dialect (`*.{json,yaml}`, `a/**/b`, `!dir/`) and its type table. Serving `rg`
	// as a `grep -r` listed .git hooks, hidden CI files and gitignored bundles, and found nothing
	// for every brace glob. So rg is asked -- `rg --files` with the same filters and roots is the
	// exact list -- and the search visits that list, spelled as rg spells it.
	if req.Name == "rg" {
		files, listed := ripgrepFiles(argv0, req, roots)
		if !listed {
			return "", 0, false
		}
		opt.Only, opt.Globs, opt.ExcludeGlobs, opt.Type = files, nil, nil, ""
	}
	scopeToSpan(&opt, restrict)
	// An error is a search that could not look, or one that met what it does not model
	// (topogrep.ErrUnmodelled): either way the real command answers.
	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		return "", 0, false
	}
	// After the search, which kept to the resource's span: a node outside it is a node this
	// answer will never print, and describing it would be paying for a line nobody sees.
	lazydesc.New(mgr, cfg, "").FillSearch(topo, res)
	// grep's exit vocabulary, and it is mode-aware because the modes answer different
	// questions: a result carrying nothing but node rows has something to PRINT in content
	// mode and nothing to report as a file list or a count.
	status := 0
	if !res.Found(opt.Mode) {
		status = 1
	}
	out := topogrep.FormatResult(res, opt)
	// The ceiling the read surface applies, against what a plain grep would have printed. This
	// surface has the one fallback the search tools do not -- the command the caller typed --
	// so an answer that outgrew its question becomes the real search.
	if topogrep.HasTextualMatch(res) &&
		!cfg.WithinOverserve(out, topogrep.RawBytes(res, opt), helper.OverserveSearchFree) {
		return "", 0, false
	}
	if strings.TrimSpace(out) == "" {
		// Terse mode renders an empty result as nothing at all. Printing a lone newline
		// would still be a line for `$(...)` to capture.
		return "", status, true
	}
	return strings.TrimRight(out, "\n") + "\n", status, true
}

// gnuGrepFamily are the commands whose --include, --exclude and --exclude-dir follow GNU grep's
// rules, and countsZeros the ones whose -c prints `path:0` for a file with no match. ripgrep
// and ag print only files that matched.
var (
	gnuGrepFamily = map[string]bool{"grep": true, "egrep": true, "fgrep": true}
	countsZeros   = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "ug": true, "ack": true}
)

// ripgrepFiles is the list of files ripgrep would search for this request, spelled as it prints
// them: `rg --files` with the same globs, in the same order, the same type and the same roots.
// Nothing else in the request changes which files rg opens -- every flag that would (--hidden,
// -u, --no-ignore, -L, a type exclusion) is one shellcmd does not model, so the command never
// got this far. False when rg cannot list them; the real command then reports why.
func ripgrepFiles(argv0 string, req shellcmd.Request, roots []string) ([]string, bool) {
	bin, err := exec.LookPath(argv0)
	if err != nil {
		return nil, false
	}
	args := []string{"--files", "--null"}
	for _, f := range req.Grep.Filters {
		glob := f.Glob
		if f.Exclude {
			glob = "!" + glob
		}
		args = append(args, "--glob="+glob)
	}
	if req.Grep.Type != "" {
		args = append(args, "--type="+req.Grep.Type)
	}
	args = append(append(args, "--"), roots...)
	// Stdin is left at /dev/null, the terminal-like stdin under which a path-less rg walks the
	// working directory -- the only case serveCommand lets through.
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, true
}

// anchorPattern applies grep's literal and anchoring flags to a pattern.
//
// `-w` and `-x` are anchorings of the pattern, not knobs. -w must only ever be asked for a
// pattern made of word characters, where `\b…\b` is exactly POSIX's rule; callers check
// shellcmd.WordSafe first and send anything else to the real grep or refuse it.
func anchorPattern(pattern string, fixed, word, line bool) string {
	if fixed {
		pattern = regexpQuote(pattern)
	}
	if word {
		pattern = `\b(?:` + pattern + `)\b`
	}
	if line {
		pattern = `^(?:` + pattern + `)$`
	}
	return pattern
}

// grepRoots turns a search's operands into the paths to walk, plus the line range to keep
// when a single operand named a resource rather than a file or directory.
//
// SEVERAL OPERANDS ARE ONE SEARCH. `grep foo *.go` is what a shell glob produces and what an
// agent types; walking them together keeps one set of caps and one honest accounting, where
// running a search per operand would report each cap against a fraction of the answer.
func grepRoots(mgr *topology.TopologyManager, operands []string,
	cfg *helper.Config) ([]string, *domain.Location, bool) {

	switch len(operands) {
	case 0:
		// Only reached for the tools whose path-less form already means "the tree", and for
		// `grep -r` with no operand; see shellcmd.parseGrep. No roots rather than ".": the
		// working directory is walked either way, but only a `.` the caller typed is echoed as
		// the `./` on every row.
		return nil, nil, true
	case 1:
		root, loc, ok := grepScope(mgr, operands[0], cfg)
		if !ok {
			return nil, nil, false
		}
		return []string{root}, loc, true
	}
	// More than one. Every operand must be a real path: a resource id among them would need
	// its own line-range restriction, and there is only one result to restrict.
	roots := make([]string, 0, len(operands))
	for _, operand := range operands {
		if _, err := os.Stat(operand); err != nil {
			return nil, nil, false
		}
		roots = append(roots, operand)
	}
	return roots, nil, true
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
	if relErr != nil || !domain.RelInside(rel) {
		return path
	}
	return rel
}

// scopeToSpan confines a search to the lines of the resource an operand named, and leaves it
// alone when the operands named none.
//
// The span goes ON THE SEARCH, not on its finished result, and the difference is the head
// limit. Trimming the result kept only rows that had already survived the 200-row cap, so a
// resource whose file held more than 200 earlier matches came back empty with exit 1 -- while
// `-c` on the same operand said 2 -- and the trim reset the truncation flag, so nothing said a
// cap had been involved. See topogrep.Options.FromLine.
func scopeToSpan(opt *topogrep.Options, loc *domain.Location) {
	if loc == nil || loc.StartsAt < 1 || loc.EndsAt < loc.StartsAt {
		return
	}
	opt.FromLine, opt.ToLine = loc.StartsAt, loc.EndsAt
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
//
// Measured from the RESOLVED ranges rather than re-derived from the operands. An operand that
// is a resource id cannot be stat'd, so the operand form scored it as zero bytes -- and an
// answer over the 12KB floor then failed the budget and fell through to the real command,
// which reports "No such file or directory" about an id.
func rawWindowBytes(resolved []domain.Location) int {
	total := 0
	for _, loc := range resolved {
		info, err := os.Stat(loc.Path)
		if err != nil {
			continue
		}
		lines := countFileLines(loc.Path)
		if lines == 0 {
			continue
		}
		want := loc.EndsAt - loc.StartsAt + 1
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
// Past a few multiples of the request, an enriched read is no longer a cheaper read -- it is a
// way to spend the context window on one `head -1`. See helper.OverserveBudget for the bounds
// and for the other surfaces that measure themselves the same way.
func withinBudget(answer string, rawBytes int, cfg *helper.Config) bool {
	return cfg.WithinOverserve(answer, rawBytes, helper.OverserveReadFree)
}

// passthrough runs the command the caller actually typed and exits with its status.
//
// This is what makes interception safe to ship on by default: whenever aracne is not certain
// it has a better answer, the shell behaves exactly as it would have.
//
// EXACTLY AS IT WOULD HAVE, WITH ONE LIMIT. LookPath finds the binary on PATH, and a shell
// FUNCTION or alias of the same name is invisible to a child process: bash exports one only
// when it was explicitly exported, and the wrappers that matter in practice -- Claude Code
// ships a `grep` function around ugrep -- are not. So for a wrapped command the fallback runs
// the binary rather than the wrapper, and their flag surfaces can differ. There is no way to
// re-enter a function the calling shell never handed us, so the claim in docs/architecture.md
// §7B is scoped to the binary rather than the shell's own resolution.
func passthrough(argv []string) {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "arac cmd: %s: command not found\n", argv[0])
		os.Exit(127)
	}
	cmd := exec.Command(bin, argv[1:]...)
	// argv[0] is what a command CALLS ITSELF in its own diagnostics, and a shell passes the
	// word the caller typed. Leaving exec.LookPath's absolute path there turns
	// "cat: x: No such file or directory" into "/usr/bin/cat: x: No such file or directory"
	// -- a different string for anything that reads it, and a puzzle for a model that did
	// not know a wrapper was in the way.
	cmd.Args[0] = argv[0]
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

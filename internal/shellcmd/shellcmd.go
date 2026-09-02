// Package shellcmd turns an already-split shell command line into the question it asks --
// "the first 20 lines of this file", "these lines of that one", "this pattern under that
// path" -- or says plainly that it does not model the command.
//
// WHY IT IS ITS OWN PACKAGE. Two callers need the same answer and must never disagree: the
// PreToolUse guard hook, which decides whether to rewrite a command, and `arac cmd`, which
// has to serve whatever the hook rewrote. A hook that rewrites a command `arac cmd` then
// passes through costs a subprocess for nothing; the reverse silently changes what a
// command means. One parser, no I/O, exhaustively table-tested.
//
// THE RULE THAT MAKES INTERCEPTION SAFE. A flag this package does not model returns
// KindPassthrough. Not "best effort", not "ignore the flag" -- passthrough. `head -c 40`
// counts bytes, `grep -o` prints matches rather than lines, `tail -f` follows: answering any
// of those with a topology-framed line window would be a wrong answer wearing a helpful
// face. Every reader below therefore ends with an explicit default that bails.
package shellcmd

import (
	"regexp"
	"strconv"
	"strings"
)

// Kind is what aracne can do with a command.
type Kind int

const (
	// KindPassthrough means: run the real command, unchanged. The safe default.
	KindPassthrough Kind = iota
	// KindRead means the command asks for some lines of a file or resource.
	KindRead
	// KindGrep means the command searches for a pattern.
	KindGrep
)

// WindowMode is which lines a read command asked for.
type WindowMode int

const (
	// WholeFile is `cat f` -- no window at all.
	WholeFile WindowMode = iota
	// Head is the first N lines.
	Head
	// Tail is the last N lines.
	Tail
	// Range is an explicit From..To.
	Range
	// FromLine is From..end of file (`tail -n +40`).
	FromLine
)

// Window is the line selection a read command asked for.
type Window struct {
	Mode WindowMode
	// N is the count for Head and Tail.
	N int
	// From and To are inclusive 1-based line numbers for Range; From alone for FromLine.
	From int
	To   int
}

// GrepRequest is a search command reduced to the knobs topogrep.Options actually has.
// Anything a plain grep can do that topogrep cannot is a passthrough, so every field here
// has a direct destination.
type GrepRequest struct {
	Pattern    string
	IgnoreCase bool
	// WholeWord is `-w`: the caller expects word boundaries, which topogrep has no flag for,
	// so the pattern is wrapped instead.
	WholeWord bool
	// Fixed is `-F`: the pattern is literal text, which the caller expects NOT to be read as
	// a regex.
	Fixed bool
	// Mode is topogrep's OutputMode spelling: "content", "files_with_matches" or "count".
	Mode      string
	Before    int
	After     int
	HeadLimit int
	Glob      string
	Type      string
}

// Request is one parsed command.
type Request struct {
	Kind Kind
	// Name is the base command word (`head`, `sed`, `rg`), lowercased and de-pathed.
	Name string
	// Operands are the file paths or resource IDs the command names, in order.
	Operands []string
	Window   Window
	Grep     GrepRequest
	// Why records the reason a command was not modelled. Diagnostic only -- never shown to
	// a model, since the passthrough it explains is invisible by design.
	Why string
}

// pass builds a passthrough answer with its reason.
func pass(name, why string) Request {
	return Request{Kind: KindPassthrough, Name: name, Why: why}
}

// Parse classifies an already-split command line. argv[0] is the command word.
//
// The input is argv, not a shell string, because both callers already have it split by a
// real shell: the guard hands `arac cmd` the original command text and the shell re-splits
// it, which is the only tokenizer guaranteed to agree with what would otherwise have run.
func Parse(argv []string) Request {
	if len(argv) == 0 {
		return pass("", "empty command")
	}
	name := Base(argv[0])
	args := argv[1:]

	switch name {
	case "cat", "less", "more":
		return parseDumper(name, args)
	case "bat":
		return parseBat(name, args)
	case "head":
		return parseHead(name, args)
	case "tail":
		return parseTail(name, args)
	case "sed":
		return parseSed(name, args)
	case "awk", "gawk", "mawk":
		return parseAwk(name, args)
	case "git":
		return parseGit(name, args)
	case "get-content":
		return parseGetContent(name, args)
	case "grep", "egrep", "fgrep", "rg", "ack", "ag", "ug":
		return parseGrep(name, args)
	}
	// nl, tac, strings, xxd, od, hexdump and everything else. These ARE reads -- the guard
	// classifies them as such and warns on them -- but they ask for a TRANSFORMATION of the
	// bytes, not a window into them. There is no honest topology-framed answer to `xxd`.
	return pass(name, "command not modelled")
}

// Base strips a directory prefix and a trailing .exe and lowercases, so `/usr/bin/sed`,
// `\sed` and `SED.EXE` all resolve to `sed`.
func Base(tok string) string {
	tok = strings.ToLower(strings.Trim(tok, `'"`))
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

// --- read commands ---------------------------------------------------------

// parseDumper handles the commands that mean "the whole file": cat, less, more. Any flag at
// all bails, because every one of them changes the rendering (`cat -A`, `less -N`) and none
// of them survives a topology-framed answer.
func parseDumper(name string, args []string) Request {
	ops, ok := plainOperands(args)
	if !ok {
		return pass(name, "flags are not modelled for "+name)
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: Window{Mode: WholeFile}}
}

// batRange matches `-r 10:20` / `--line-range=10:20`, and the open-ended `10:` / `:20`.
var batRange = regexp.MustCompile(`^(\d*):(\d*)$`)

// parseBat handles `bat`, which is `cat` with one window flag worth modelling.
func parseBat(name string, args []string) Request {
	var ops []string
	w := Window{Mode: WholeFile}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-r" || a == "--line-range":
			if i+1 >= len(args) {
				return pass(name, "-r without a range")
			}
			i++
			r, ok := batWindow(args[i])
			if !ok {
				return pass(name, "unmodelled -r range")
			}
			w = r
		case strings.HasPrefix(a, "--line-range="):
			r, ok := batWindow(strings.TrimPrefix(a, "--line-range="))
			if !ok {
				return pass(name, "unmodelled --line-range")
			}
			w = r
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		default:
			ops = append(ops, a)
		}
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: w}
}

// batWindow turns bat's `A:B`, `A:` and `:B` into a Window.
func batWindow(spec string) (Window, bool) {
	m := batRange.FindStringSubmatch(spec)
	if m == nil {
		return Window{}, false
	}
	from, to := atoi(m[1]), atoi(m[2])
	switch {
	case from > 0 && to >= from:
		return Window{Mode: Range, From: from, To: to}, true
	case from > 0 && m[2] == "":
		return Window{Mode: FromLine, From: from}, true
	case m[1] == "" && to > 0:
		return Window{Mode: Range, From: 1, To: to}, true
	}
	return Window{}, false
}

var (
	// `-40`
	bareCount = regexp.MustCompile(`^-(\d+)$`)
	// `-n40`
	attachedCount = regexp.MustCompile(`^-n(\d+)$`)
)

// parseHead models the first-N-lines forms of `head` and nothing else. `-c` counts bytes and
// a negative `-n` means "all but the last N" -- neither is a line window.
func parseHead(name string, args []string) Request {
	n := 10 // head's own default
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case bareCount.MatchString(a):
			n = atoi(bareCount.FindStringSubmatch(a)[1])
		case attachedCount.MatchString(a):
			n = atoi(attachedCount.FindStringSubmatch(a)[1])
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return pass(name, "-n without a count")
			}
			i++
			v := atoi(args[i])
			if v <= 0 {
				return pass(name, "unmodelled -n "+args[i])
			}
			n = v
		case strings.HasPrefix(a, "--lines="):
			v := atoi(strings.TrimPrefix(a, "--lines="))
			if v <= 0 {
				return pass(name, "unmodelled --lines")
			}
			n = v
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		default:
			ops = append(ops, a)
		}
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: Window{Mode: Head, N: n}}
}

// parseTail models the last-N-lines and from-line-N forms. `-f`/`-F` follow a growing file,
// which is not a read of anything aracne holds.
func parseTail(name string, args []string) Request {
	w := Window{Mode: Tail, N: 10}
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case bareCount.MatchString(a):
			w = Window{Mode: Tail, N: atoi(bareCount.FindStringSubmatch(a)[1])}
		case attachedCount.MatchString(a):
			w = Window{Mode: Tail, N: atoi(attachedCount.FindStringSubmatch(a)[1])}
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return pass(name, "-n without a count")
			}
			i++
			t, ok := tailWindow(args[i])
			if !ok {
				return pass(name, "unmodelled -n "+args[i])
			}
			w = t
		case strings.HasPrefix(a, "--lines="):
			t, ok := tailWindow(strings.TrimPrefix(a, "--lines="))
			if !ok {
				return pass(name, "unmodelled --lines")
			}
			w = t
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		default:
			ops = append(ops, a)
		}
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: w}
}

// tailWindow reads tail's count argument, where a leading `+` flips the meaning from "the
// last N" to "from line N onward".
func tailWindow(spec string) (Window, bool) {
	if strings.HasPrefix(spec, "+") {
		if n := atoi(spec[1:]); n > 0 {
			return Window{Mode: FromLine, From: n}, true
		}
		return Window{}, false
	}
	if n := atoi(spec); n > 0 {
		return Window{Mode: Tail, N: n}, true
	}
	return Window{}, false
}

var (
	sedRange = regexp.MustCompile(`^(\d+),(\d+)p$`)
	sedOne   = regexp.MustCompile(`^(\d+)p$`)
	sedToEnd = regexp.MustCompile(`^(\d+),\$p$`)
)

// parseSed models `sed -n` used purely to print a line range, which is the one sed form that
// is a read. Everything else -- a substitution, `-i`, more than one script -- is either a
// mutation or a transformation, and both must run for real.
func parseSed(name string, args []string) Request {
	var script string
	var ops []string
	sawN := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n" || a == "--quiet" || a == "--silent":
			sawN = true
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) || script != "" {
				return pass(name, "unmodelled -e")
			}
			i++
			script = args[i]
		case strings.HasPrefix(a, "--expression="):
			if script != "" {
				return pass(name, "more than one script")
			}
			script = strings.TrimPrefix(a, "--expression=")
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		case script == "":
			script = a
		default:
			ops = append(ops, a)
		}
	}
	if !sawN {
		// Without -n, sed prints every line AND the range again. That is a transformation.
		return pass(name, "sed without -n prints the whole stream")
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	w, ok := sedWindow(strings.Trim(script, `'"`))
	if !ok {
		return pass(name, "unmodelled sed script "+script)
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: w}
}

// sedWindow recognizes the four print-range spellings that are a pure line window.
func sedWindow(script string) (Window, bool) {
	script = strings.TrimSpace(script)
	switch {
	case sedRange.MatchString(script):
		m := sedRange.FindStringSubmatch(script)
		from, to := atoi(m[1]), atoi(m[2])
		if from < 1 || to < from {
			return Window{}, false
		}
		return Window{Mode: Range, From: from, To: to}, true
	case sedOne.MatchString(script):
		n := atoi(sedOne.FindStringSubmatch(script)[1])
		if n < 1 {
			return Window{}, false
		}
		return Window{Mode: Range, From: n, To: n}, true
	case sedToEnd.MatchString(script):
		n := atoi(sedToEnd.FindStringSubmatch(script)[1])
		if n < 1 {
			return Window{}, false
		}
		return Window{Mode: FromLine, From: n}, true
	case script == "$p":
		return Window{Mode: Tail, N: 1}, true
	}
	return Window{}, false
}

var (
	awkRange = regexp.MustCompile(`^NR>=(\d+)&&NR<=(\d+)$`)
	awkEq    = regexp.MustCompile(`^NR==(\d+)$`)
	awkLE    = regexp.MustCompile(`^NR<=(\d+)$`)
	awkGE    = regexp.MustCompile(`^NR>=(\d+)$`)
)

// parseAwk models the handful of NR comparisons people use awk for when they want a line
// window. Any real awk program passes through.
func parseAwk(name string, args []string) Request {
	var program string
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		case program == "":
			program = a
		default:
			ops = append(ops, a)
		}
	}
	if program == "" || len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	w, ok := awkWindow(program)
	if !ok {
		return pass(name, "unmodelled awk program")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: w}
}

// awkWindow normalizes an awk one-liner and matches it against the NR forms. The trailing
// action is dropped only when it is a bare print, since anything else transforms the line.
func awkWindow(program string) (Window, bool) {
	p := strings.Trim(strings.TrimSpace(program), `'"`)
	p = strings.NewReplacer(" ", "", "\t", "").Replace(p)
	for _, action := range []string{"{print}", "{print$0}", "{print;}"} {
		p = strings.TrimSuffix(p, action)
	}
	if strings.ContainsAny(p, "{}") {
		return Window{}, false
	}
	switch {
	case awkRange.MatchString(p):
		m := awkRange.FindStringSubmatch(p)
		from, to := atoi(m[1]), atoi(m[2])
		if from < 1 || to < from {
			return Window{}, false
		}
		return Window{Mode: Range, From: from, To: to}, true
	case awkEq.MatchString(p):
		n := atoi(awkEq.FindStringSubmatch(p)[1])
		return Window{Mode: Range, From: n, To: n}, n >= 1
	case awkLE.MatchString(p):
		n := atoi(awkLE.FindStringSubmatch(p)[1])
		return Window{Mode: Head, N: n}, n >= 1
	case awkGE.MatchString(p):
		n := atoi(awkGE.FindStringSubmatch(p)[1])
		return Window{Mode: FromLine, From: n}, n >= 1
	}
	return Window{}, false
}

// parseGit models the two subcommands that print a worktree file's contents. The rev must be
// HEAD: any other revision names bytes that are not on disk, and the topology only indexes
// what is.
func parseGit(name string, args []string) Request {
	if len(args) == 0 {
		return pass(name, "no subcommand")
	}
	var spec string
	switch args[0] {
	case "show":
		if len(args) != 2 {
			return pass(name, "unmodelled git show")
		}
		spec = args[1]
	case "cat-file":
		// `git cat-file -p HEAD:path` is the only spelling that prints contents plainly.
		if len(args) != 3 || args[1] != "-p" {
			return pass(name, "unmodelled git cat-file")
		}
		spec = args[2]
	default:
		return pass(name, "git "+args[0]+" is not a read")
	}
	rev, path, found := strings.Cut(spec, ":")
	if !found || rev != "HEAD" || path == "" {
		return pass(name, "not a HEAD: path")
	}
	return Request{Kind: KindRead, Name: name, Operands: []string{path}, Window: Window{Mode: WholeFile}}
}

// parseGetContent models the PowerShell reader. Its count flags are the same three questions
// head and tail ask.
func parseGetContent(name string, args []string) Request {
	w := Window{Mode: WholeFile}
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		lower := strings.ToLower(a)
		switch {
		case lower == "-totalcount" || lower == "-first" || lower == "-head":
			if i+1 >= len(args) {
				return pass(name, lower+" without a count")
			}
			i++
			n := atoi(args[i])
			if n <= 0 {
				return pass(name, "unmodelled count")
			}
			w = Window{Mode: Head, N: n}
		case lower == "-last" || lower == "-tail":
			if i+1 >= len(args) {
				return pass(name, lower+" without a count")
			}
			i++
			n := atoi(args[i])
			if n <= 0 {
				return pass(name, "unmodelled count")
			}
			w = Window{Mode: Tail, N: n}
		case lower == "-path" || lower == "-literalpath":
			if i+1 >= len(args) {
				return pass(name, lower+" without a path")
			}
			i++
			ops = append(ops, args[i])
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		default:
			ops = append(ops, a)
		}
	}
	if len(ops) == 0 {
		return pass(name, "reads stdin")
	}
	return Request{Kind: KindRead, Name: name, Operands: ops, Window: w}
}

// --- grep ------------------------------------------------------------------

// Output modes, spelled as topogrep.OutputMode does. Duplicated as plain strings so this
// package stays free of every other one -- the caller converts.
const (
	OutputContent = "content"
	OutputFiles   = "files_with_matches"
	OutputCount   = "count"
)

// attachedCtx matches the glued context forms `-A3`, `-B2`, `-C1`.
var attachedCtx = regexp.MustCompile(`^-([ABC])(\d+)$`)

// parseGrep maps a search command onto the knobs topogrep has. Flags that change what a
// match IS (`-v` inverts, `-o` prints the match not the line, `-P` is a different regex
// dialect) or that suppress the location aracne annotates (`-h`) all pass through: aracne's
// answer would be a different question's answer.
func parseGrep(name string, args []string) Request {
	g := GrepRequest{Mode: OutputContent}
	var ops []string
	patternSet := false
	args = expandShortFlags(args)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i" || a == "--ignore-case" || a == "-y":
			g.IgnoreCase = true
		case a == "-w" || a == "--word-regexp":
			g.WholeWord = true
		case a == "-F" || a == "--fixed-strings":
			g.Fixed = true
		// Accepted and ignored: aracne always reports path:line, always recurses into a
		// directory operand, and Go's regexp is already an extended dialect.
		case a == "-n" || a == "--line-number" || a == "-H" || a == "--with-filename",
			a == "-r" || a == "-R" || a == "--recursive" || a == "--dereference-recursive",
			a == "-E" || a == "--extended-regexp",
			a == "-s" || a == "--no-messages",
			a == "--binary-files=without-match", strings.HasPrefix(a, "--color"):
		case a == "-l" || a == "--files-with-matches":
			g.Mode = OutputFiles
		case a == "-c" || a == "--count":
			g.Mode = OutputCount
		case attachedCtx.MatchString(a):
			m := attachedCtx.FindStringSubmatch(a)
			setContext(&g, m[1], atoi(m[2]))
		case a == "-A" || a == "-B" || a == "-C":
			if i+1 >= len(args) {
				return pass(name, a+" without a count")
			}
			i++
			setContext(&g, strings.TrimPrefix(a, "-"), atoi(args[i]))
		case strings.HasPrefix(a, "--after-context="):
			g.After = atoi(strings.TrimPrefix(a, "--after-context="))
		case strings.HasPrefix(a, "--before-context="):
			g.Before = atoi(strings.TrimPrefix(a, "--before-context="))
		case strings.HasPrefix(a, "--context="):
			n := atoi(strings.TrimPrefix(a, "--context="))
			g.Before, g.After = n, n
		case a == "-m" || a == "--max-count":
			if i+1 >= len(args) {
				return pass(name, "-m without a count")
			}
			i++
			g.HeadLimit = atoi(args[i])
		case strings.HasPrefix(a, "--max-count="):
			g.HeadLimit = atoi(strings.TrimPrefix(a, "--max-count="))
		case strings.HasPrefix(a, "--include="):
			g.Glob = strings.TrimPrefix(a, "--include=")
		case strings.HasPrefix(a, "--glob="):
			g.Glob = strings.TrimPrefix(a, "--glob=")
		case a == "-g" || a == "--glob" || a == "--include":
			if i+1 >= len(args) {
				return pass(name, a+" without a glob")
			}
			i++
			g.Glob = args[i]
		case a == "-t" || a == "--type":
			if i+1 >= len(args) {
				return pass(name, "-t without a type")
			}
			i++
			g.Type = args[i]
		case strings.HasPrefix(a, "--type="):
			g.Type = strings.TrimPrefix(a, "--type=")
		case a == "-e" || a == "--regexp":
			if i+1 >= len(args) || patternSet {
				return pass(name, "unmodelled -e")
			}
			i++
			g.Pattern, patternSet = args[i], true
		case strings.HasPrefix(a, "--regexp="):
			if patternSet {
				return pass(name, "more than one pattern")
			}
			g.Pattern, patternSet = strings.TrimPrefix(a, "--regexp="), true
		case strings.HasPrefix(a, "-"):
			return pass(name, "unmodelled flag "+a)
		case !patternSet:
			g.Pattern, patternSet = a, true
		default:
			ops = append(ops, a)
		}
	}
	if !patternSet || g.Pattern == "" {
		return pass(name, "no pattern")
	}
	// egrep/fgrep are the flagless spellings of -E and -F.
	if name == "fgrep" {
		g.Fixed = true
	}
	// Where a path-less search looks is NOT the same question across these tools. `grep foo`
	// reads stdin; `rg foo` walks the working directory. Serving the first from the topology
	// would answer a search of the whole tree when the caller asked about piped input -- so
	// only the tools that already mean "the tree" may omit a path.
	if len(ops) == 0 && !searchesCwdByDefault[name] {
		return pass(name, name+" without a path reads stdin")
	}
	return Request{Kind: KindGrep, Name: name, Operands: ops, Grep: g}
}

// searchesCwdByDefault lists the search tools whose no-path form walks the working directory
// rather than reading stdin.
var searchesCwdByDefault = map[string]bool{"rg": true, "ag": true, "ack": true, "ug": true}

// clusterable is a short flag that takes no argument, so it may appear glued to others
// (`-rn`, `-in`). Flags that consume the next token are deliberately absent: expanding a
// cluster containing one would silently drop its argument.
var clusterable = map[byte]bool{
	'i': true, 'w': true, 'F': true, 'E': true, 'n': true, 'H': true,
	'r': true, 'R': true, 's': true, 'l': true, 'c': true, 'y': true,
}

// expandShortFlags rewrites `-rn` into `-r -n` so the flag loop sees one flag per token.
// A cluster holding anything not in `clusterable` is left exactly as it is, which sends it
// to the loop's default branch and passes the whole command through -- the right answer,
// since we cannot know whether the unknown letter wanted the next token.
func expandShortFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if len(a) < 3 || a[0] != '-' || a[1] == '-' || !allClusterable(a[1:]) {
			out = append(out, a)
			continue
		}
		for i := 1; i < len(a); i++ {
			out = append(out, "-"+string(a[i]))
		}
	}
	return out
}

// allClusterable reports whether every byte of a short-flag run takes no argument.
func allClusterable(letters string) bool {
	for i := 0; i < len(letters); i++ {
		if !clusterable[letters[i]] {
			return false
		}
	}
	return true
}

// setContext applies -A/-B/-C, where -C is both sides.
func setContext(g *GrepRequest, which string, n int) {
	switch which {
	case "A":
		g.After = n
	case "B":
		g.Before = n
	case "C":
		g.Before, g.After = n, n
	}
}

// --- shared ----------------------------------------------------------------

// plainOperands returns the arguments when NONE of them is a flag, reporting false as soon
// as one is. `-` (stdin) counts as a flag: there is no file to look up.
func plainOperands(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return nil, false
		}
		out = append(out, a)
	}
	return out, true
}

// atoi returns 0 for anything that is not a plain non-negative integer, which every caller
// treats as "not a count".
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

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
	// so the pattern is wrapped instead. Only ever set for a pattern made entirely of word
	// characters -- see wordSafe.
	WholeWord bool
	// WholeLine is `-x`: the match must be the entire line, which is an anchoring of the
	// pattern rather than a knob.
	WholeLine bool
	// Fixed is `-F`: the pattern is literal text, which the caller expects NOT to be read as
	// a regex.
	Fixed bool
	// WithFilename is `-H`: print the path on every row, even for a single file operand.
	// LineNumbers is `-n`: print the line number.
	//
	// BOTH ARE CARRIED RATHER THAN ASSUMED, and they used to be neither. They sat in the
	// "accepted and ignored" branch below on the grounds that "aracne always reports
	// path:line" -- which is not true of the shell surface, where a single named file prints
	// bare `line:text` and there is no way to ask for the path back. So `-H` was silently
	// dropped, and `-n` was silently ADDED to every row that did not ask for it. Either one
	// moves the field a caller reads: `grep -H -n pat f | cut -d: -f1` is a list of paths to
	// the real grep and a list of line numbers to aracne.
	WithFilename bool
	LineNumbers  bool
	// Mode is topogrep's OutputMode spelling: "content", "files_with_matches" or "count".
	Mode   string
	Before int
	After  int
	// MaxCount is `-m`: at most this many matches FROM EACH FILE. It is deliberately not
	// called HeadLimit. topogrep's head limit caps the whole result, and `grep -rm 1 foo .`
	// -- the idiom for "one hit per file", an index of the tree -- means something entirely
	// different from "one hit".
	MaxCount     int
	Globs        []string
	ExcludeGlobs []string
	ExcludeDirs  []string
	Type         string
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
		// A zero count is not a small window, it is no window: `head -0` prints nothing.
		// Answering it with head's ten-line default is the opposite of what was asked, and
		// classifying it as a read at all makes the guard rewrite a command `arac cmd`
		// then hands straight back.
		case bareCount.MatchString(a):
			v := atoi(bareCount.FindStringSubmatch(a)[1])
			if v < 1 {
				return pass(name, "unmodelled count "+a)
			}
			n = v
		case attachedCount.MatchString(a):
			v := atoi(attachedCount.FindStringSubmatch(a)[1])
			if v < 1 {
				return pass(name, "unmodelled count "+a)
			}
			n = v
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
		// As in parseHead: `tail -0` prints nothing, so it is not a window aracne models.
		case bareCount.MatchString(a):
			v := atoi(bareCount.FindStringSubmatch(a)[1])
			if v < 1 {
				return pass(name, "unmodelled count "+a)
			}
			w = Window{Mode: Tail, N: v}
		case attachedCount.MatchString(a):
			v := atoi(attachedCount.FindStringSubmatch(a)[1])
			if v < 1 {
				return pass(name, "unmodelled count "+a)
			}
			w = Window{Mode: Tail, N: v}
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
//
// MORE THAN ONE FILE IS A PASSTHROUGH, and this is the difference between sed and head. sed
// concatenates its operands into ONE stream and does not reset its line numbers per file, so
// `sed -n '1,5p' a.go b.go` prints lines 1-5 of the concatenation -- usually nothing from
// b.go at all. Answering it per file returns lines the command never printed, which is a
// confident wrong answer of exactly the kind this package exists to refuse. awk's NR is
// cumulative for the same reason; head, tail and cat genuinely are per file and stay served.
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
	if len(ops) > 1 {
		return pass(name, "sed numbers lines across all its files as one stream")
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
	if len(ops) > 1 {
		return pass(name, "NR runs across all of awk's files as one stream")
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

// WHY `git show` AND `git cat-file` ARE NOT MODELLED.
//
// They were, for the HEAD: spelling, on the reasoning that HEAD names bytes aracne already
// has. It does not. `git show HEAD:f` prints what is COMMITTED, the topology indexes what is
// CHECKED OUT, and the two differ exactly when the question is worth asking -- when the file
// has uncommitted changes and the caller is asking what the last commit held. Answering from
// the worktree there returns the caller's own edit as though it were the committed version,
// which is the confident wrong answer this package exists to avoid. It was also served in a
// directory that is not a git repository at all, where the real command exits 128.
//
// Deciding correctly would mean running git to compare the worktree against the blob, and
// this package does no I/O by design -- both callers depend on Parse being a pure function of
// argv. So git reaches the real binary, which is the only thing that can answer it.

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

// wordChars is the POSIX word-constituent set, which is what `-w` is defined against.
var wordRun = regexp.MustCompile(`^[0-9A-Za-z_]+$`)

// wordSafe reports whether wrapping a pattern in `\b…\b` means what `-w` means.
//
// It usually does not. POSIX `-w` requires the match to be bounded by NON-word characters,
// which is satisfied between two non-word characters too: `grep -w '=='` finds `if x == ""`.
// RE2's `\b` is a boundary between a word and a non-word character, so `\b==\b` requires a
// word character on the far side and matches nothing at all -- a search that answers "no
// matches" to a pattern the real grep finds.
//
// The two agree exactly when the pattern begins and ends with a word character and contains
// nothing else, so that is the only case aracne claims. Anything else reaches the real grep.
func wordSafe(pattern string) bool {
	return wordRun.MatchString(pattern)
}

// includeFamily and typeFamily say which search tools own which filename filters.
//
// A tool must not be credited with a flag it does not have. `grep -t go` is an ERROR in GNU
// grep -- exit 2 and a usage message -- so answering it with a type-filtered search invents
// a capability, and the model that learns the spelling here finds it fails everywhere else.
var (
	includeFamily = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "ug": true}
	typeFamily    = map[string]bool{"rg": true, "ag": true, "ack": true, "ug": true}
)

// parseGrep maps a search command onto the knobs topogrep has. Flags that change what a
// match IS (`-v` inverts, `-o` prints the match not the line, `-P` is a different regex
// dialect) or that suppress the location aracne annotates (`-h`) all pass through: aracne's
// answer would be a different question's answer.
func parseGrep(name string, args []string) Request {
	g := GrepRequest{Mode: OutputContent}
	var ops []string
	patternSet := false
	sawRecursive := false
	endOfFlags := false
	dialect := defaultDialect(name)
	// Expanded per token INSIDE the loop, not in a pass over the whole argv, so `--` is
	// honoured first. The pre-pass shredded a post-`--` pattern into flags: `grep -- -rn f`
	// came back with the pattern `-r` and `-n` as an operand, where the real grep searches
	// for the literal string "-rn". Nothing downstream could have recovered the pattern.
	for i := 0; i < len(args); i++ {
		a := args[i]
		// `--` ends the options: every token after it is the pattern or an operand, even
		// one that starts with a dash. Reading `-foo` as a flag after it searches for
		// something else entirely.
		if endOfFlags {
			if !patternSet {
				g.Pattern, patternSet = a, true
			} else {
				ops = append(ops, a)
			}
			continue
		}
		if expanded, ok := expandCluster(a); ok {
			// COPIED, not spliced in place. `args` is argv[1:] -- the CALLER's backing
			// array -- and `append(args[:i], ...)` writes straight into it whenever the
			// slice has spare capacity. Parse is documented as a pure function of argv and
			// both callers hand it a slice whose length equals its capacity today, so the
			// splice happened to reallocate; a caller that built argv with append would
			// have had its command silently rewritten, and RunCmd passes the same slice on
			// to passthrough(). Measured on a slice with spare capacity, `grep -rn foo .`
			// came back as `grep -r -n foo` -- the path operand gone.
			next := make([]string, 0, len(args)+len(expanded))
			next = append(next, args[:i]...)
			next = append(next, expanded...)
			next = append(next, args[i+1:]...)
			args = next
			a = args[i]
		}
		switch {
		case a == "--":
			endOfFlags = true
		case a == "-i" || a == "--ignore-case" || a == "-y":
			g.IgnoreCase = true
		case a == "-w" || a == "--word-regexp":
			g.WholeWord = true
		case a == "-x" || a == "--line-regexp":
			g.WholeLine = true
		// The three dialect flags, last one winning, as GNU grep resolves them. Which
		// dialect is in force decides what the pattern MEANS, so it is tracked rather
		// than assumed -- see defaultDialect.
		case a == "-F" || a == "--fixed-strings":
			dialect = dialectFixed
		case a == "-E" || a == "--extended-regexp":
			dialect = dialectERE
		case a == "-G" || a == "--basic-regexp":
			dialect = dialectBRE
		// Accepted and ignored: aracne always recurses into a directory operand.
		case a == "-r" || a == "-R" || a == "--recursive" || a == "--dereference-recursive":
			sawRecursive = true
		// CARRIED, NOT IGNORED. These two sat in the branch below on the reasoning that
		// "aracne always reports path:line" -- which is not true on the shell surface, where
		// a single named file prints a bare `line:text`. So `-H` was silently dropped and
		// `-n` was silently ADDED to rows that never asked for it. See GrepRequest.
		case a == "-n" || a == "--line-number":
			g.LineNumbers = true
		case a == "-H" || a == "--with-filename":
			g.WithFilename = true
		case a == "-s" || a == "--no-messages",
			a == "--binary-files=without-match", strings.HasPrefix(a, "--color"):
		case a == "-l" || a == "--files-with-matches":
			g.Mode = OutputFiles
		case a == "-c" || a == "--count":
			g.Mode = OutputCount
		case a == "-A" || a == "-B" || a == "-C":
			if i+1 >= len(args) {
				return pass(name, a+" without a count")
			}
			i++
			n, ok := positiveCount(args[i])
			if !ok {
				return pass(name, "unmodelled "+a+" "+args[i])
			}
			setContext(&g, strings.TrimPrefix(a, "-"), n)
		case strings.HasPrefix(a, "--after-context="),
			strings.HasPrefix(a, "--before-context="),
			strings.HasPrefix(a, "--context="):
			flag, value, _ := strings.Cut(a, "=")
			n, ok := positiveCount(value)
			if !ok {
				return pass(name, "unmodelled "+a)
			}
			setContext(&g, map[string]string{
				"--after-context": "A", "--before-context": "B", "--context": "C",
			}[flag], n)
		// -m is a PER-FILE cap. A zero or non-numeric argument is not one: `grep -m 0`
		// prints nothing at all, and answering it with the default cap is the opposite.
		case a == "-m" || a == "--max-count":
			if i+1 >= len(args) {
				return pass(name, "-m without a count")
			}
			i++
			n, ok := positiveCount(args[i])
			if !ok {
				return pass(name, "unmodelled -m "+args[i])
			}
			g.MaxCount = n
		case strings.HasPrefix(a, "--max-count="):
			n, ok := positiveCount(strings.TrimPrefix(a, "--max-count="))
			if !ok {
				return pass(name, "unmodelled "+a)
			}
			g.MaxCount = n
		// Filename filters, each repeatable and each OR-ed with the others -- which is
		// what grep means by them. Keeping only the last silently dropped every match the
		// earlier one would have found.
		case strings.HasPrefix(a, "--include="):
			if !includeFamily[name] {
				return pass(name, name+" has no --include")
			}
			g.Globs = append(g.Globs, strings.TrimPrefix(a, "--include="))
		case a == "--include":
			if !includeFamily[name] || i+1 >= len(args) {
				return pass(name, "unmodelled --include")
			}
			i++
			g.Globs = append(g.Globs, args[i])
		case strings.HasPrefix(a, "--exclude="):
			if !includeFamily[name] {
				return pass(name, name+" has no --exclude")
			}
			g.ExcludeGlobs = append(g.ExcludeGlobs, strings.TrimPrefix(a, "--exclude="))
		case a == "--exclude":
			if !includeFamily[name] || i+1 >= len(args) {
				return pass(name, "unmodelled --exclude")
			}
			i++
			g.ExcludeGlobs = append(g.ExcludeGlobs, args[i])
		case strings.HasPrefix(a, "--exclude-dir="):
			if !includeFamily[name] {
				return pass(name, name+" has no --exclude-dir")
			}
			g.ExcludeDirs = append(g.ExcludeDirs, strings.TrimPrefix(a, "--exclude-dir="))
		case a == "--exclude-dir":
			if !includeFamily[name] || i+1 >= len(args) {
				return pass(name, "unmodelled --exclude-dir")
			}
			i++
			g.ExcludeDirs = append(g.ExcludeDirs, args[i])
		case strings.HasPrefix(a, "--glob="):
			if !typeFamily[name] {
				return pass(name, name+" has no --glob")
			}
			g.Globs = append(g.Globs, strings.TrimPrefix(a, "--glob="))
		case a == "-g" || a == "--glob":
			if !typeFamily[name] || i+1 >= len(args) {
				return pass(name, "unmodelled "+a)
			}
			i++
			// ripgrep's `-g !pat` is a NEGATED glob, and reading it as a positive one
			// searches exactly the files the caller asked to skip.
			if strings.HasPrefix(args[i], "!") {
				g.ExcludeGlobs = append(g.ExcludeGlobs, strings.TrimPrefix(args[i], "!"))
				continue
			}
			g.Globs = append(g.Globs, args[i])
		case a == "-t" || a == "--type":
			if !typeFamily[name] || i+1 >= len(args) {
				return pass(name, "unmodelled "+a)
			}
			i++
			g.Type = args[i]
		case strings.HasPrefix(a, "--type="):
			if !typeFamily[name] {
				return pass(name, name+" has no --type")
			}
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
	// `-w` is only `\b…\b` for a pattern made of word characters; anything else has to
	// reach the real grep rather than a wrap that finds fewer matches. See wordSafe.
	if g.WholeWord && !wordSafe(g.Pattern) {
		return pass(name, "-w on a pattern \\b cannot express: "+g.Pattern)
	}
	// The dialect is settled. `-F` rides into topogrep as a flag; BRE cannot ride at all,
	// because topogrep compiles with Go's regexp and there is no RE2 switch for it, so the
	// pattern is rewritten here or the command passes through.
	g.Fixed = dialect == dialectFixed
	if dialect == dialectBRE {
		re2, ok := breToRE2(g.Pattern)
		if !ok {
			return pass(name, "BRE pattern with no RE2 equivalent: "+g.Pattern)
		}
		g.Pattern = re2
	}
	// Where a path-less search looks is NOT the same question across these tools. `grep foo`
	// reads stdin; `rg foo` walks the working directory. Serving the first from the topology
	// would answer a search of the whole tree when the caller asked about piped input -- so
	// only the tools that already mean "the tree" may omit a path.
	//
	// `grep -r foo` is the exception, and it has been one since GNU grep 2.11: with -r and no
	// operand it searches the working directory rather than stdin.
	if len(ops) == 0 && !searchesCwdByDefault[name] && !sawRecursive {
		return pass(name, name+" without a path reads stdin")
	}
	return Request{Kind: KindGrep, Name: name, Operands: ops, Grep: g}
}

// positiveCount reads a count argument that must be a plain integer of at least 1. It reports
// false for a zero, a negative and a word, each of which means something the caller asked for
// that a default would not deliver.
func positiveCount(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// searchesCwdByDefault lists the search tools whose no-path form walks the working directory
// rather than reading stdin.
var searchesCwdByDefault = map[string]bool{"rg": true, "ag": true, "ack": true, "ug": true}

// clusterable is a short flag that takes no argument, so it may appear glued to others
// (`-rn`, `-in`).
var clusterable = map[byte]bool{
	'i': true, 'w': true, 'x': true, 'F': true, 'E': true, 'G': true, 'n': true, 'H': true,
	'r': true, 'R': true, 's': true, 'l': true, 'c': true, 'y': true,
}

// valueTaking is a short flag that consumes a value -- either glued to it (`-m1`, `-A3`,
// `-epattern`) or as the next token (`-m 1`). It may only appear LAST in a cluster, because
// everything after it in the token is its argument.
//
// numericValue is the subset whose glued value must be digits. `-A3` is a count and `-Ax` is
// nothing at all, so a non-numeric tail there means the token was never the flag it looked
// like and the whole command passes through.
var (
	valueTaking  = map[byte]bool{'m': true, 'A': true, 'B': true, 'C': true, 'e': true}
	numericValue = map[byte]bool{'m': true, 'A': true, 'B': true, 'C': true}
)

// expandCluster splits one short-flag run -- `-rn` into `-r -n`, `-rm1` into `-r -m1`, which
// is how grep itself reads a cluster ending in a flag that takes a value -- reporting false
// for anything it cannot account for letter by letter.
//
// A cluster it cannot account for completely is left EXACTLY as it is, which sends it to the
// flag loop's default branch and passes the whole command through. That is the right answer:
// an unknown letter may have wanted the next token, and expanding it would silently drop the
// argument -- the "ignore the flag" this package forbids.
func expandCluster(a string) ([]string, bool) {
	if len(a) < 3 || a[0] != '-' || a[1] == '-' {
		return nil, false
	}
	var out []string
	for i := 1; i < len(a); i++ {
		switch c := a[i]; {
		case clusterable[c]:
			out = append(out, "-"+string(c))
		case valueTaking[c]:
			// Last in the cluster: the rest of the token, if any, is its glued value, and
			// an empty rest means the value is the next token. Either way nothing after it
			// is a flag, and splitting it into two tokens is what lets the flag loop read
			// `-A3`, `-m1` and `-rm 5` with one branch each rather than three.
			rest := a[i+1:]
			if numericValue[c] && rest != "" && !allDigits(rest) {
				return nil, false
			}
			out = append(out, "-"+string(c))
			if rest != "" {
				out = append(out, rest)
			}
			return out, true
		default:
			return nil, false
		}
	}
	return out, true
}

// allDigits reports whether s is a non-empty run of decimal digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
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

// --- regex dialects ---------------------------------------------------------

// regexDialect is the syntax a search command's pattern is written in.
//
// WHY THIS EXISTS. topogrep compiles with Go's regexp, which is RE2: a superset of POSIX
// ERE, but NOT of POSIX BRE. Plain `grep` is BRE, and the two dialects disagree in both
// directions -- `\|` is alternation in BRE and a literal pipe in RE2, while a bare `+` is
// a literal in BRE and a quantifier in RE2. Reading one as the other does not fail loudly;
// it returns a confident, well-formed answer to a different question. Treating the absence
// of `-E` as "near enough to extended" was exactly the "ignore the flag" this package's
// doc comment forbids, so the dialect is carried instead of assumed.
type regexDialect int

const (
	dialectBRE regexDialect = iota
	dialectERE
	dialectFixed
)

// defaultDialect is the syntax a search tool uses when no dialect flag is given. Only
// POSIX grep defaults to BRE; egrep is the flagless spelling of -E and fgrep of -F, and
// rg/ack/ag/ug all default to ERE-or-richer dialects RE2 already covers.
func defaultDialect(name string) regexDialect {
	switch name {
	case "grep":
		return dialectBRE
	case "fgrep":
		return dialectFixed
	default:
		return dialectERE
	}
}

// breToRE2 rewrites a POSIX Basic Regular Expression into the RE2 source that means the
// same thing: BRE's escaped operators (`\(`, `\|`, `\+`, `\{`) shed their backslash, and
// the ordinary characters RE2 would read as operators (`(`, `|`, `+`, `{`) gain one.
//
// ok is false where BRE expresses something RE2 cannot (a backreference), where the input
// is malformed, or where the rewrite fails to compile. Every one of those is a
// passthrough, per the package rule: a pattern this function cannot vouch for must reach
// the real grep rather than a near-miss of it.
func breToRE2(pat string) (string, bool) {
	var b strings.Builder
	b.Grow(len(pat) + 8)
	// leading marks the positions where BRE has nothing to repeat, which is where `*` is
	// an ordinary character and `^` is an anchor: the start of the pattern, and just past
	// a `\(` or a `\|`.
	leading := true
	for i := 0; i < len(pat); i++ {
		switch c := pat[i]; c {
		case '\\':
			if i+1 >= len(pat) {
				return "", false // a trailing backslash is undefined
			}
			i++
			switch d := pat[i]; {
			case d >= '1' && d <= '9':
				return "", false // a backreference; RE2 has no such thing
			case d == '(' || d == ')' || d == '{' || d == '}' || d == '|' || d == '+' || d == '?':
				b.WriteByte(d) // BRE's escaped operator is RE2's bare one
				leading = d == '(' || d == '|'
			case d == '<' || d == '>':
				b.WriteString(`\b`) // GNU's word boundaries
				leading = false
			default:
				b.WriteByte('\\')
				b.WriteByte(d)
				leading = false
			}
		case '(', ')', '{', '}', '|', '+', '?':
			b.WriteByte('\\') // ordinary in BRE, an operator in RE2
			b.WriteByte(c)
			leading = false
		case '*':
			if leading {
				b.WriteString(`\*`) // nothing to repeat, so it repeats nothing
			} else {
				b.WriteByte('*')
			}
			leading = false
		case '^':
			// An anchor only where it leads, ordinary anywhere else. It does not itself
			// give a following `*` something to repeat, so `leading` survives it.
			if leading {
				b.WriteByte('^')
			} else {
				b.WriteString(`\^`)
			}
		case '$':
			if breEndAnchor(pat, i) {
				b.WriteByte('$')
			} else {
				b.WriteString(`\$`)
			}
			leading = false
		case '[':
			end, ok := breBracket(pat, i)
			if !ok {
				return "", false
			}
			b.WriteString(pat[i : end+1])
			i = end
			leading = false
		default:
			b.WriteByte(c)
			leading = false
		}
	}
	out := b.String()
	if _, err := regexp.Compile(out); err != nil {
		return "", false
	}
	return out, true
}

// breEndAnchor reports whether the `$` at i anchors rather than matching a literal `$`:
// BRE anchors only at the very end of the pattern, or of a `\(…\)` branch.
func breEndAnchor(pat string, i int) bool {
	if i == len(pat)-1 {
		return true
	}
	return i+2 < len(pat) && pat[i+1] == '\\' && (pat[i+2] == ')' || pat[i+2] == '|')
}

// breBracket returns the index of the `]` closing the bracket expression opening at i.
// Bracket expressions are copied through untouched, because POSIX and RE2 read their
// contents the same way -- with one exception: inside them POSIX treats `\` as an ordinary
// character where RE2 treats it as an escape, so a bracket holding one is not translatable.
func breBracket(pat string, i int) (int, bool) {
	j := i + 1
	if j < len(pat) && pat[j] == '^' {
		j++
	}
	if j < len(pat) && pat[j] == ']' {
		j++ // a `]` in the first position is a member, not the terminator
	}
	for j < len(pat) {
		switch {
		case pat[j] == '\\':
			return 0, false
		case pat[j] == '[' && j+1 < len(pat) && (pat[j+1] == '.' || pat[j+1] == '='):
			// A collating element or equivalence class. RE2 has neither, and it does not
			// reject them either -- it reads `[[.a.]]` as the class {`[`, `.`, `a`} followed
			// by a literal `]`, so the compile check in breToRE2 cannot catch this one.
			return 0, false
		case pat[j] == '[' && j+1 < len(pat) && pat[j+1] == ':':
			// A character class, which RE2 does support. Its own `]` does not close the
			// bracket, so step over the whole `[:…:]`.
			k := strings.Index(pat[j+2:], ":]")
			if k < 0 {
				return 0, false
			}
			j += 2 + k + 2
		case pat[j] == ']':
			return j, true
		default:
			j++
		}
	}
	return 0, false // unterminated
}

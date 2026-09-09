package shellcmd

import (
	"strings"
	"testing"
)

// THE GREP MATRIX.
//
// Interception's promise for a search is narrow and absolute: the command the model typed
// must never come back WORSE than it would have from the real binary. That splits into two
// obligations this file tests at the parser, which is where both are decided:
//
//   - A flag aracne claims must land on the topogrep knob that MEANS the same thing. A flag
//     read as the wrong knob returns a confident answer to a different question.
//   - A flag aracne does not model must reach KindPassthrough, so the real grep runs.
//
// The rows below are the grep vocabulary an agent actually types. Every one is either
// asserted to map somewhere exact, or asserted to pass through -- there is no third answer,
// and a flag that quietly does nothing is the failure this table exists to catch.

// grepOf parses a command line that must classify as a search, and returns its GrepRequest.
func grepOf(t *testing.T, cmd string) GrepRequest {
	t.Helper()
	got := Parse(strings.Fields(cmd))
	if got.Kind != KindGrep {
		t.Fatalf("%q: kind = %v (%s), want grep", cmd, got.Kind, got.Why)
	}
	return got.Grep
}

// oneGlob asserts a request carries exactly the one filename filter given.
func oneGlob(g GrepRequest, want string) bool {
	return len(g.Globs) == 1 && g.Globs[0] == want
}

// mustPass asserts a command reaches the real binary untouched.
func mustPass(t *testing.T, cmd string) {
	t.Helper()
	if got := Parse(strings.Fields(cmd)); got.Kind != KindPassthrough {
		t.Errorf("%q: kind = %v, want passthrough", cmd, got.Kind)
	}
}

// The everyday spellings. Each row names the ONE thing it is checking, so a failure says
// which knob moved rather than "the table broke".
func TestGrepEverydayFlagsMapToTheRightKnob(t *testing.T) {
	for _, tc := range []struct {
		cmd    string
		check  func(GrepRequest) bool
		expect string
	}{
		// Case folding, in all three spellings a model uses.
		{"grep -i needle .", func(g GrepRequest) bool { return g.IgnoreCase }, "IgnoreCase"},
		{"grep --ignore-case needle .", func(g GrepRequest) bool { return g.IgnoreCase }, "IgnoreCase"},
		{"grep -ri needle .", func(g GrepRequest) bool { return g.IgnoreCase }, "IgnoreCase in a cluster"},

		// Output modes.
		{"grep -rl needle .", func(g GrepRequest) bool { return g.Mode == OutputFiles }, "files_with_matches"},
		{"grep -r --files-with-matches needle .", func(g GrepRequest) bool { return g.Mode == OutputFiles }, "files_with_matches"},
		{"grep -rc needle .", func(g GrepRequest) bool { return g.Mode == OutputCount }, "count"},
		{"grep -r --count needle .", func(g GrepRequest) bool { return g.Mode == OutputCount }, "count"},

		// Context, in every spelling grep accepts.
		{"grep -A 3 needle .", func(g GrepRequest) bool { return g.After == 3 && g.Before == 0 }, "After=3"},
		{"grep -A3 needle .", func(g GrepRequest) bool { return g.After == 3 && g.Before == 0 }, "After=3"},
		{"grep --after-context=3 needle .", func(g GrepRequest) bool { return g.After == 3 }, "After=3"},
		{"grep -B 2 needle .", func(g GrepRequest) bool { return g.Before == 2 && g.After == 0 }, "Before=2"},
		{"grep -B2 needle .", func(g GrepRequest) bool { return g.Before == 2 }, "Before=2"},
		{"grep --before-context=2 needle .", func(g GrepRequest) bool { return g.Before == 2 }, "Before=2"},
		{"grep -C 1 needle .", func(g GrepRequest) bool { return g.Before == 1 && g.After == 1 }, "both sides"},
		{"grep -C1 needle .", func(g GrepRequest) bool { return g.Before == 1 && g.After == 1 }, "both sides"},
		{"grep --context=1 needle .", func(g GrepRequest) bool { return g.Before == 1 && g.After == 1 }, "both sides"},
		{"grep -A 2 -B 1 needle .", func(g GrepRequest) bool { return g.After == 2 && g.Before == 1 }, "asymmetric context"},

		// Filename filters.
		{"grep -r --include=*.go needle .", func(g GrepRequest) bool { return oneGlob(g, "*.go") }, "Glob"},
		{"grep -r --include *.go needle .", func(g GrepRequest) bool { return oneGlob(g, "*.go") }, "Glob"},
		{"rg -g *.go needle", func(g GrepRequest) bool { return oneGlob(g, "*.go") }, "Glob"},
		{"rg --glob=*.rs needle", func(g GrepRequest) bool { return oneGlob(g, "*.rs") }, "Glob"},
		{"rg -t go needle", func(g GrepRequest) bool { return g.Type == "go" }, "Type"},
		{"rg --type=py needle", func(g GrepRequest) bool { return g.Type == "py" }, "Type"},

		// Pattern spellings.
		{"grep -e needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "-e pattern"},
		{"grep --regexp=needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "--regexp pattern"},
		{"grep -F needle .", func(g GrepRequest) bool { return g.Fixed }, "Fixed"},
		{"fgrep needle .", func(g GrepRequest) bool { return g.Fixed }, "Fixed by tool name"},
		{"grep -w needle .", func(g GrepRequest) bool { return g.WholeWord }, "WholeWord"},

		// Caps.
		{"grep -m 5 needle .", func(g GrepRequest) bool { return g.MaxCount == 5 }, "MaxCount"},
		{"grep --max-count=5 needle .", func(g GrepRequest) bool { return g.MaxCount == 5 }, "MaxCount"},

		// Accepted-and-ignored: aracne always reports path:line and always recurses.
		{"grep -rn needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "-r -n are free"},
		{"grep -H needle f.go", func(g GrepRequest) bool { return g.Pattern == "needle" }, "-H is free"},
		{"grep -rns needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "-s is free"},
		{"grep -rn --color=never needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "--color is free"},
	} {
		if !tc.check(grepOf(t, tc.cmd)) {
			t.Errorf("%q: %s not set as expected: %+v", tc.cmd, tc.expect, grepOf(t, tc.cmd))
		}
	}
}

// Every flag that changes what a MATCH IS, or where it is reported from. topogrep has no knob
// for any of them, so each must reach the real binary. A near-miss here is the one failure
// mode interception may never have.
func TestGrepUnmodelledFlagsAllPassThrough(t *testing.T) {
	for _, cmd := range []string{
		// What counts as a match.
		"grep -v needle .",             // inverted
		"grep --invert-match needle .", // inverted
		"grep -P needle .",             // PCRE: lookaround, \d, backreferences
		"grep --perl-regexp needle .",  // ditto
		"grep -f pats.txt .",           // patterns from a file
		"grep -e a -e b .",             // more than one pattern
		"grep --regexp=a --regexp=b .", // more than one pattern
		"grep -w -e a -e b .",          // ditto, with a modelled flag alongside

		// What is reported.
		"grep -o needle .",              // the match, not the line
		"grep --only-matching needle .", // ditto
		"grep -h needle .",              // suppresses the filename aracne annotates by
		"grep --no-filename needle .",   // ditto
		"grep -L needle .",              // files WITHOUT a match
		"grep -q needle .",              // exit status only; prints nothing
		"grep --quiet needle .",         // ditto
		"grep -Z needle .",              // NUL after each filename
		"grep --null needle .",          // ditto
		"grep -z needle .",              // NUL-delimited input
		"grep -b needle .",              // byte offsets
		"grep --byte-offset needle .",   // ditto
		"grep -T needle .",              // aligned output
		"grep -u needle .",              // unix byte offsets
		"grep -a needle .",              // binary as text
		"grep --text needle .",          // ditto
		"grep -I needle .",              // skip binaries
		"grep --binary-files=text needle .",
		"grep --line-buffered needle .",
		"grep -d skip needle .",        // directory action
		"grep --devices=skip needle .", // device action
		"grep -Q needle .",             // not a grep flag at all

		// Where it looks.
		"grep --exclude-from=list needle .", // ditto, from a file
		"rg --type-not=test needle",         // rg's negative type filter
		"rg --no-ignore needle",             // changes which files are walked
		"rg --hidden needle",                // ditto
		"rg -u needle",                      // ditto
		"rg --no-heading needle",            // a different output shape
		"rg --json needle",                  // a different output format entirely
		"rg -S needle",                      // smart case: neither -i nor not
		"rg --smart-case needle",            // ditto
		"rg -U needle",                      // multiline
		"rg --multiline needle",             // ditto
		"rg -P needle",                      // PCRE2
		"rg --files-without-match needle",   // the -L question

		// Not a search at all.
		"grep needle",    // reads stdin
		"grep -i needle", // still stdin
		"grep .",         // no pattern
		"grep",           // nothing
	} {
		mustPass(t, cmd)
	}
}

// A cluster may END in a flag that takes a value, which is how grep itself reads `-rm 5` and
// `-nA3`. The value follows, glued or as the next token, and nothing after the letter is a
// flag -- so the expansion has to stop there rather than keep splitting letters.
func TestGrepClustersEndingInAValueTakingFlagExpand(t *testing.T) {
	for _, tc := range []struct {
		cmd   string
		check func(GrepRequest) bool
		what  string
	}{
		{"grep -nA 3 needle .", func(g GrepRequest) bool { return g.After == 3 }, "-nA 3"},
		{"grep -nA3 needle .", func(g GrepRequest) bool { return g.After == 3 }, "-nA3"},
		{"grep -rm 5 needle .", func(g GrepRequest) bool { return g.MaxCount == 5 }, "-rm 5"},
		{"grep -rm5 needle .", func(g GrepRequest) bool { return g.MaxCount == 5 }, "-rm5"},
		{"grep -rC2 needle .", func(g GrepRequest) bool { return g.Before == 2 && g.After == 2 }, "-rC2"},
		{"grep -re needle .", func(g GrepRequest) bool { return g.Pattern == "needle" }, "-re"},
	} {
		if !tc.check(grepOf(t, tc.cmd)) {
			t.Errorf("%q: %s did not land: %+v", tc.cmd, tc.what, grepOf(t, tc.cmd))
		}
	}
}

// A cluster holding a letter aracne does not model is left EXACTLY as it is, and the whole
// command passes through. Expanding it would answer with the flag dropped, which is the
// "ignore the flag" this package forbids.
func TestGrepClustersHoldingAnUnmodelledFlagPassThrough(t *testing.T) {
	for _, cmd := range []string{
		"grep -rt go needle .", // -t is not grep's flag at all
		"grep -rno needle .",   // -o prints the match, not the line
		"grep -rnv needle .",   // -v inverts
		"grep -rnq needle .",   // -q prints nothing
		"grep -rnP needle .",   // a different regex dialect
		"grep -rA x needle .",  // -A's value is not a count
		"grep -rAx needle .",   // ditto, glued
	} {
		mustPass(t, cmd)
	}
}

// The clusters that DO expand, because every letter in them takes no argument. These are the
// spellings an agent types most, so a parser that refuses them is interception that never
// fires.
func TestGrepClustersOfArgumentlessFlagsExpand(t *testing.T) {
	for _, tc := range []struct {
		cmd   string
		check func(GrepRequest) bool
		what  string
	}{
		{"grep -rn needle .", func(g GrepRequest) bool { return !g.IgnoreCase }, "-rn"},
		{"grep -rni needle .", func(g GrepRequest) bool { return g.IgnoreCase }, "-rni sets -i"},
		{"grep -irn needle .", func(g GrepRequest) bool { return g.IgnoreCase }, "-irn sets -i"},
		{"grep -rnl needle .", func(g GrepRequest) bool { return g.Mode == OutputFiles }, "-rnl sets -l"},
		{"grep -rnc needle .", func(g GrepRequest) bool { return g.Mode == OutputCount }, "-rnc sets -c"},
		{"grep -rnw needle .", func(g GrepRequest) bool { return g.WholeWord }, "-rnw sets -w"},
		{"grep -rnF needle .", func(g GrepRequest) bool { return g.Fixed }, "-rnF sets -F"},
		{"grep -rniw needle .", func(g GrepRequest) bool { return g.IgnoreCase && g.WholeWord }, "-rniw sets both"},
	} {
		if !tc.check(grepOf(t, tc.cmd)) {
			t.Errorf("%q: %s failed: %+v", tc.cmd, tc.what, grepOf(t, tc.cmd))
		}
	}
}

// -m is grep's PER-FILE cap: `grep -rm 1 foo .` is the standard way to ask for one hit in
// EACH file -- an index of the whole tree, one line per file. It lands on its own field for
// exactly that reason: a cap on the whole result would answer with one line, total.
func TestGrepMaxCountIsPerFileNotPerResult(t *testing.T) {
	for _, cmd := range []string{
		"grep -r -m 1 needle .",
		"grep -rm1 needle .",
		"grep --max-count=1 -r needle .",
		"grep -m 1 needle main.go",
	} {
		if g := grepOf(t, cmd); g.MaxCount != 1 {
			t.Errorf("%q: MaxCount = %d, want 1", cmd, g.MaxCount)
		}
	}
}

// -m has the same attached spelling as the context flags, and grep accepts it. Modelling
// `-A3` but not `-m3` is an inconsistency the caller pays for in a passthrough.
func TestGrepMaxCountAcceptsTheAttachedSpelling(t *testing.T) {
	if g := grepOf(t, "grep -m3 needle main.go"); g.MaxCount != 3 {
		t.Errorf("`-m3` is grep's own spelling of `-m 3`: MaxCount = %d, want 3", g.MaxCount)
	}
}

// A count flag with a nonsense or zero argument must not silently become "no limit".
// `grep -m 0` prints nothing at all; answering it with the default 200-line cap is the
// opposite of what was asked.
func TestGrepDegenerateCountsPassThrough(t *testing.T) {
	for _, cmd := range []string{
		"grep -m 0 needle main.go",
		"grep --max-count=0 needle main.go",
		"grep -m abc needle main.go",
		"grep --max-count=abc needle main.go",
		"grep -A x needle main.go",
		"grep --after-context=x needle main.go",
	} {
		mustPass(t, cmd)
	}
}

// More than one --include is grep's way of saying "or": `--include=*.go --include=*.md`
// searches both. A parser that keeps only the last one silently drops every match from the
// first, which is a lost answer wearing a successful exit status.
func TestGrepRepeatedFilenameFiltersAreNotSilentlyDropped(t *testing.T) {
	for _, cmd := range []string{
		"grep -rn --include=*.go --include=*.md needle .",
		"rg -g *.go -g *.md needle",
	} {
		g := grepOf(t, cmd)
		if len(g.Globs) != 2 || g.Globs[0] != "*.go" || g.Globs[1] != "*.md" {
			t.Errorf("%q: globs = %q, want both -- the earlier one's matches are otherwise "+
				"lost behind a successful exit status", cmd, g.Globs)
		}
	}
}

// --exclude and --exclude-dir subtract, and subtracting is a knob topogrep now has. A
// passthrough was safe but gave up the annotated search for the single most common way an
// agent narrows a sweep.
func TestGrepExclusionsAreModelled(t *testing.T) {
	g := grepOf(t, "grep -rn --exclude=*_test.go --exclude-dir=build needle .")
	if len(g.ExcludeGlobs) != 1 || g.ExcludeGlobs[0] != "*_test.go" {
		t.Errorf("exclude globs = %q", g.ExcludeGlobs)
	}
	if len(g.ExcludeDirs) != 1 || g.ExcludeDirs[0] != "build" {
		t.Errorf("exclude dirs = %q", g.ExcludeDirs)
	}
	// ripgrep spells a negated glob `-g !pat`, and reading it as a positive one searches
	// exactly the files the caller asked to skip.
	rg := grepOf(t, "rg -g !*_test.go needle")
	if len(rg.Globs) != 0 || len(rg.ExcludeGlobs) != 1 || rg.ExcludeGlobs[0] != "*_test.go" {
		t.Errorf("`rg -g !*_test.go`: globs = %q, excludes = %q", rg.Globs, rg.ExcludeGlobs)
	}
}

// -x is `^(?:pat)$`, which is an anchoring rather than a knob -- and one topogrep can hold.
func TestGrepWholeLineIsModelled(t *testing.T) {
	if g := grepOf(t, "grep -rnx needle ."); !g.WholeLine {
		t.Error("-x did not set WholeLine")
	}
}

// GNU grep has searched the working directory for `grep -r pat` with no operand since 2.11.
// Treating it as a read of stdin passed through the single most common recursive spelling.
func TestGrepRecursiveWithNoPathSearchesTheTree(t *testing.T) {
	for _, cmd := range []string{"grep -r needle", "grep -rn needle", "grep -R needle"} {
		if got := Parse(strings.Fields(cmd)); got.Kind != KindGrep {
			t.Errorf("%q: kind = %v (%s), want grep", cmd, got.Kind, got.Why)
		}
	}
	// Without -r it really is stdin.
	mustPass(t, "grep needle")
}

// A tool must not be credited with a flag it does not have. `grep -t go` is an ERROR in GNU
// grep (exit 2, a usage message); answering it with a type-filtered search invents a
// capability, and the model learns a spelling that fails everywhere else.
func TestSearchToolsAreOnlyGivenTheirOwnFlags(t *testing.T) {
	for _, cmd := range []string{
		"grep -rn -t go needle .",       // -t is ripgrep's
		"grep -rn --type=go needle .",   // ditto
		"grep -rn -g *.go needle .",     // -g is ripgrep's
		"grep -rn --glob=*.go needle .", // ditto
	} {
		mustPass(t, cmd)
	}
	for _, cmd := range []string{
		"rg --include=*.go needle", // --include is grep's
	} {
		mustPass(t, cmd)
	}
}

// `-w` is only `\b…\b` for a pattern made of word characters. POSIX bounds a -w match by
// NON-word characters, which is satisfied between two of them too -- `grep -w ””==”'` finds
// `if x == ""` and `\b==\b` finds nothing. Everything else reaches the real grep.
func TestWholeWordIsClaimedOnlyWhereBoundariesMeanTheSameThing(t *testing.T) {
	for _, cmd := range []string{"grep -w needle .", "grep -w Total .", "grep -w x_1 ."} {
		if g := grepOf(t, cmd); !g.WholeWord {
			t.Errorf("%q: a word-character pattern is exactly what -w is for", cmd)
		}
	}
	for _, cmd := range []string{
		"grep -w == .",      // no word character to bound against
		"grep -w -> .",      // ditto
		"grep -w foo.bar .", // a dot is not a word character
		"grep -w foo|bar .", // alternation: the boundaries are per branch
		"grep -F -w ++ .",   // literal, still not word-bounded
	} {
		mustPass(t, cmd)
	}
}

// `--` ends the options, so the next word is the pattern even when it starts with a dash.
// Passing through is safe; answering it as an unknown flag is what happens today, and the
// row is here so a future model of `--` cannot regress into treating `-foo` as a flag.
func TestGrepEndOfOptionsIsNeverReadAsAFlag(t *testing.T) {
	got := Parse(strings.Fields("grep -rn -- -foo ."))
	if got.Kind == KindGrep && got.Grep.Pattern != "-foo" {
		t.Errorf("`grep -- -foo` searched for %q, not the literal pattern `-foo`", got.Grep.Pattern)
	}
}

// Several file operands is the shape a shell glob produces (`grep foo *.go`) and one topogrep
// has no option for. It must pass through rather than search one of them.
func TestGrepWithSeveralOperandsPassesThroughOrKeepsThemAll(t *testing.T) {
	got := Parse(strings.Fields("grep -n needle a.go b.go c.go"))
	if got.Kind == KindGrep && len(got.Operands) != 3 {
		t.Errorf("operands = %v, want all three or a passthrough", got.Operands)
	}
}

// The dialect an agent's pattern is written in decides what it MEANS. These are the patterns
// that actually appear in transcripts; each must survive into RE2 saying the same thing.
func TestGrepRealWorldPatternsSurviveTheDialectRewrite(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{
		// Plain grep is BRE. Parentheses, braces, + and ? are ORDINARY characters there.
		{`grep func(ctx .`, `func\(ctx`},
		{`grep TODO(rhuan) .`, `TODO\(rhuan\)`},
		{`grep a+b .`, `a\+b`},
		{`grep foo? .`, `foo\?`},
		{`grep ^func .`, `^func`},
		{`grep );$ .`, `\);$`},
		{`grep [Ee]rror .`, `[Ee]rror`},
		{`grep .*Error .`, `.*Error`},
		{`grep \.go$ .`, `\.go$`},
		{`grep ^[[:space:]]*return .`, `^[[:space:]]*return`},
		{`grep \<Serve\> .`, `\bServe\b`},

		// -E and the extended-by-default tools take the pattern as written.
		{`grep -E ^func\s+\(?[A-Z] .`, `^func\s+\(?[A-Z]`},
		{`egrep (foo|bar)+ .`, `(foo|bar)+`},
		{`rg ^\s*func\s+\w+\( .`, `^\s*func\s+\w+\(`},
		{`rg (?i)todo .`, `(?i)todo`},

		// -F means every byte is literal; the escaping happens downstream, so the parser
		// carries the pattern through untouched and flags it.
		{`grep -F fmt.Sprintf(" .`, `fmt.Sprintf("`},
	} {
		got := Parse(strings.Fields(tc.cmd))
		if got.Kind != KindGrep {
			t.Errorf("%q: kind = %v (%s), want grep", tc.cmd, got.Kind, got.Why)
			continue
		}
		if got.Grep.Pattern != tc.want {
			t.Errorf("%q: pattern = %q, want %q", tc.cmd, got.Grep.Pattern, tc.want)
		}
	}
}

// The rewrite has to be a rewrite, not a guess: anything BRE can say and RE2 cannot must
// reach the real grep. These are the shapes that actually show up.
func TestGrepPatternsRE2CannotExpressReachTheRealGrep(t *testing.T) {
	for _, cmd := range []string{
		`grep \(foo\)\1 .`,     // a backreference
		`grep ^\(.*\)\1$ .`,    // the duplicated-line idiom
		`grep [[.hyphen.]] .`,  // a collating element
		`grep [a\]b] .`,        // a backslash inside a bracket expression
		`grep [unterminated .`, // malformed
	} {
		mustPass(t, cmd)
	}
}

// A search tool's path-less form is not the same question across tools, and answering the
// wrong one turns a search of piped input into a search of the whole repository.
func TestPathlessFormsFollowEachToolsOwnDefault(t *testing.T) {
	for _, cmd := range []string{"grep needle", "egrep needle", "fgrep needle"} {
		mustPass(t, cmd) // POSIX grep reads stdin
	}
	for _, cmd := range []string{"rg needle", "ag needle", "ack needle", "ug needle"} {
		if got := Parse(strings.Fields(cmd)); got.Kind != KindGrep {
			t.Errorf("%q walks the working directory: kind = %v (%s), want grep", cmd, got.Kind, got.Why)
		}
	}
}

// TestFilenameAndLineNumberFlagsAreCarried pins the two flags that used to be discarded.
//
// They sat in the "accepted and ignored" branch on the reasoning that "aracne always reports
// path:line" -- which is not true on the shell surface, where a single named file prints a bare
// `line:text`. So `-H` was silently dropped and `-n` was silently ADDED to rows that never asked
// for it. Either one moves the field a caller reads: `grep -H -n pat f | cut -d: -f1` is a list
// of paths to the real grep and a list of line numbers to aracne.
func TestFilenameAndLineNumberFlagsAreCarried(t *testing.T) {
	for _, tt := range []struct {
		argv       []string
		filename   bool
		lineNumber bool
	}{
		{[]string{"grep", "pat", "f.go"}, false, false},
		{[]string{"grep", "-n", "pat", "f.go"}, false, true},
		{[]string{"grep", "--line-number", "pat", "f.go"}, false, true},
		{[]string{"grep", "-H", "pat", "f.go"}, true, false},
		{[]string{"grep", "--with-filename", "pat", "f.go"}, true, false},
		{[]string{"grep", "-Hn", "pat", "f.go"}, true, true}, // a cluster
		{[]string{"grep", "-rn", "pat", "."}, false, true},   // the common shape
		{[]string{"grep", "-rnH", "pat", "."}, true, true},
	} {
		got := Parse(tt.argv)
		if got.Kind != KindGrep {
			t.Fatalf("%v: Kind = %v, want KindGrep (%s)", tt.argv, got.Kind, got.Why)
		}
		if got.Grep.WithFilename != tt.filename {
			t.Errorf("%v: WithFilename = %v, want %v", tt.argv, got.Grep.WithFilename, tt.filename)
		}
		if got.Grep.LineNumbers != tt.lineNumber {
			t.Errorf("%v: LineNumbers = %v, want %v", tt.argv, got.Grep.LineNumbers, tt.lineNumber)
		}
	}
}

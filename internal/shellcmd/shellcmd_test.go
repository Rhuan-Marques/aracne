package shellcmd

import (
	"strings"
	"testing"
)

// want is the compact expectation for one command line.
type want struct {
	kind Kind
	win  Window
	ops  []string
}

func check(t *testing.T, cmd string, w want) {
	t.Helper()
	got := Parse(strings.Fields(cmd))
	if got.Kind != w.kind {
		t.Fatalf("%q: kind = %v (%s), want %v", cmd, got.Kind, got.Why, w.kind)
	}
	if w.kind == KindPassthrough {
		return
	}
	if got.Window != w.win {
		t.Errorf("%q: window = %+v, want %+v", cmd, got.Window, w.win)
	}
	if len(w.ops) > 0 && strings.Join(got.Operands, ",") != strings.Join(w.ops, ",") {
		t.Errorf("%q: operands = %v, want %v", cmd, got.Operands, w.ops)
	}
}

// The read table. Every row aracne claims to model, plus the neighbouring spelling it must
// NOT claim -- that pairing is the whole point: an unmodelled flag has to stay a real
// command, or interception silently answers a different question.
func TestReadCommandsAreModelledOrPassedThrough(t *testing.T) {
	whole := Window{Mode: WholeFile}
	for _, tc := range []struct {
		cmd string
		w   want
	}{
		// cat / less / more: the whole file, and nothing with a flag.
		{"cat main.go", want{KindRead, whole, []string{"main.go"}}},
		{"cat a.go b.go", want{KindRead, whole, []string{"a.go", "b.go"}}},
		{"less main.go", want{KindRead, whole, nil}},
		{"more main.go", want{KindRead, whole, nil}},
		{"cat -n main.go", want{kind: KindPassthrough}},
		{"cat -A main.go", want{kind: KindPassthrough}},
		{"cat", want{kind: KindPassthrough}},

		// head: every count spelling, then the ones that are not line counts.
		{"head main.go", want{KindRead, Window{Mode: Head, N: 10}, nil}},
		{"head -40 main.go", want{KindRead, Window{Mode: Head, N: 40}, nil}},
		{"head -n 40 main.go", want{KindRead, Window{Mode: Head, N: 40}, nil}},
		{"head -n40 main.go", want{KindRead, Window{Mode: Head, N: 40}, nil}},
		{"head --lines=40 main.go", want{KindRead, Window{Mode: Head, N: 40}, nil}},
		{"head -c 40 main.go", want{kind: KindPassthrough}},
		{"head -z main.go", want{kind: KindPassthrough}},
		{"head -n -5 main.go", want{kind: KindPassthrough}}, // "all but the last 5"
		{"head -20", want{kind: KindPassthrough}},           // stdin

		// tail: last-N and from-line-N are different questions.
		{"tail main.go", want{KindRead, Window{Mode: Tail, N: 10}, nil}},
		{"tail -5 main.go", want{KindRead, Window{Mode: Tail, N: 5}, nil}},
		{"tail -n 5 main.go", want{KindRead, Window{Mode: Tail, N: 5}, nil}},
		{"tail -n5 main.go", want{KindRead, Window{Mode: Tail, N: 5}, nil}},
		{"tail -n +40 main.go", want{KindRead, Window{Mode: FromLine, From: 40}, nil}},
		{"tail --lines=+40 main.go", want{KindRead, Window{Mode: FromLine, From: 40}, nil}},
		{"tail -f main.go", want{kind: KindPassthrough}},
		{"tail -F main.go", want{kind: KindPassthrough}},
		{"tail -c 100 main.go", want{kind: KindPassthrough}},

		// bat.
		{"bat main.go", want{KindRead, whole, nil}},
		{"bat -r 10:20 main.go", want{KindRead, Window{Mode: Range, From: 10, To: 20}, nil}},
		{"bat --line-range=10:20 main.go", want{KindRead, Window{Mode: Range, From: 10, To: 20}, nil}},
		{"bat -r 10: main.go", want{KindRead, Window{Mode: FromLine, From: 10}, nil}},
		{"bat -r :20 main.go", want{KindRead, Window{Mode: Range, From: 1, To: 20}, nil}},
		{"bat --style=plain main.go", want{kind: KindPassthrough}},

		// sed: only -n print ranges.
		{"sed -n 120,160p main.go", want{KindRead, Window{Mode: Range, From: 120, To: 160}, nil}},
		{"sed -n 120p main.go", want{KindRead, Window{Mode: Range, From: 120, To: 120}, nil}},
		{"sed -n 120,$p main.go", want{KindRead, Window{Mode: FromLine, From: 120}, nil}},
		{"sed -n $p main.go", want{KindRead, Window{Mode: Tail, N: 1}, nil}},
		{"sed -n -e 5,9p main.go", want{KindRead, Window{Mode: Range, From: 5, To: 9}, nil}},
		{"sed 120,160p main.go", want{kind: KindPassthrough}},   // no -n: prints everything twice
		{"sed -i s/a/b/ main.go", want{kind: KindPassthrough}},  // a mutation
		{"sed -n s/a/b/p main.go", want{kind: KindPassthrough}}, // a transformation
		{"sed -n 160,120p main.go", want{kind: KindPassthrough}},

		// awk NR forms.
		{"awk NR>=10&&NR<=20 main.go", want{KindRead, Window{Mode: Range, From: 10, To: 20}, nil}},
		{"awk NR==7 main.go", want{KindRead, Window{Mode: Range, From: 7, To: 7}, nil}},
		{"awk NR<=7 main.go", want{KindRead, Window{Mode: Head, N: 7}, nil}},
		{"awk NR>=7 main.go", want{KindRead, Window{Mode: FromLine, From: 7}, nil}},
		{"awk {print$2} main.go", want{kind: KindPassthrough}},
		{"awk /foo/ main.go", want{kind: KindPassthrough}},

		// git: only a HEAD: path.
		// git is not modelled at all. `git show HEAD:f` prints the COMMITTED bytes and the
		// topology indexes the CHECKED-OUT ones -- the same file only while nothing is
		// uncommitted, which is precisely when nobody asks.
		{"git show HEAD:main.go", want{kind: KindPassthrough}},
		{"git cat-file -p HEAD:main.go", want{kind: KindPassthrough}},
		{"git show abc123:main.go", want{kind: KindPassthrough}},
		{"git show HEAD", want{kind: KindPassthrough}},
		{"git log --oneline", want{kind: KindPassthrough}},

		// PowerShell.
		{"Get-Content main.go", want{KindRead, whole, nil}},
		{"Get-Content -TotalCount 20 main.go", want{KindRead, Window{Mode: Head, N: 20}, nil}},
		{"Get-Content -Tail 20 main.go", want{KindRead, Window{Mode: Tail, N: 20}, nil}},

		// The presentation/encoding readers. These are reads, but they ask for a
		// TRANSFORMATION of the bytes; there is no honest topology-framed answer.
		{"nl main.go", want{kind: KindPassthrough}},
		{"tac main.go", want{kind: KindPassthrough}},
		{"xxd main.go", want{kind: KindPassthrough}},
		{"od -c main.go", want{kind: KindPassthrough}},
		{"hexdump -C main.go", want{kind: KindPassthrough}},
		{"strings main.go", want{kind: KindPassthrough}},

		// Not our business at all.
		{"ls -la", want{kind: KindPassthrough}},
		{"go build ./...", want{kind: KindPassthrough}},
		{"", want{kind: KindPassthrough}},
	} {
		check(t, tc.cmd, tc.w)
	}
}

func TestGrepFlagsMapOntoTopogrepOptions(t *testing.T) {
	for _, tc := range []struct {
		cmd    string
		assert func(*testing.T, Request)
	}{
		{"grep foo main.go", func(t *testing.T, r Request) {
			if r.Grep.Pattern != "foo" || r.Grep.Mode != OutputContent {
				t.Errorf("got %+v", r.Grep)
			}
			if len(r.Operands) != 1 || r.Operands[0] != "main.go" {
				t.Errorf("operands = %v", r.Operands)
			}
		}},
		{"grep -rn foo .", func(t *testing.T, r Request) {
			if r.Grep.Pattern != "foo" {
				t.Errorf("pattern = %q", r.Grep.Pattern)
			}
		}},
		{"grep -i -w foo .", func(t *testing.T, r Request) {
			if !r.Grep.IgnoreCase || !r.Grep.WholeWord {
				t.Errorf("got %+v", r.Grep)
			}
		}},
		{"grep -C 3 foo .", func(t *testing.T, r Request) {
			if r.Grep.Before != 3 || r.Grep.After != 3 {
				t.Errorf("got %+v", r.Grep)
			}
		}},
		{"grep -A2 -B1 foo .", func(t *testing.T, r Request) {
			if r.Grep.After != 2 || r.Grep.Before != 1 {
				t.Errorf("got %+v", r.Grep)
			}
		}},
		{"grep -l foo .", func(t *testing.T, r Request) {
			if r.Grep.Mode != OutputFiles {
				t.Errorf("mode = %q", r.Grep.Mode)
			}
		}},
		{"grep -c foo .", func(t *testing.T, r Request) {
			if r.Grep.Mode != OutputCount {
				t.Errorf("mode = %q", r.Grep.Mode)
			}
		}},
		{"grep --include=*.go foo .", func(t *testing.T, r Request) {
			if len(r.Grep.Globs) != 1 || r.Grep.Globs[0] != "*.go" {
				t.Errorf("globs = %q", r.Grep.Globs)
			}
		}},
		{"rg -t go foo", func(t *testing.T, r Request) {
			if r.Grep.Type != "go" || len(r.Operands) != 0 {
				t.Errorf("got %+v ops=%v", r.Grep, r.Operands)
			}
		}},
		{"grep -e foo main.go", func(t *testing.T, r Request) {
			if r.Grep.Pattern != "foo" {
				t.Errorf("pattern = %q", r.Grep.Pattern)
			}
		}},
		{"fgrep foo main.go", func(t *testing.T, r Request) {
			if !r.Grep.Fixed {
				t.Error("fgrep must imply -F")
			}
		}},
		{"grep -m 5 foo .", func(t *testing.T, r Request) {
			// -m is a PER-FILE cap, which is not topogrep's head limit under another name.
			if r.Grep.MaxCount != 5 {
				t.Errorf("max count = %d", r.Grep.MaxCount)
			}
		}},
	} {
		got := Parse(strings.Fields(tc.cmd))
		if got.Kind != KindGrep {
			t.Fatalf("%q: kind = %v (%s), want grep", tc.cmd, got.Kind, got.Why)
		}
		tc.assert(t, got)
	}
}

// The flags that change what a MATCH is, or hide where it came from. Answering these with a
// topology-annotated line list is answering a different question.
func TestGrepFlagsThatChangeTheAnswerPassThrough(t *testing.T) {
	for _, cmd := range []string{
		"grep -o foo main.go", // prints the match, not the line
		"grep -v foo main.go", // inverts
		"grep -P foo main.go", // a different regex dialect
		"grep -h foo main.go", // drops the filename aracne annotates by
		"grep -L foo main.go", // files WITHOUT matches
		"grep -q foo main.go", // exit status only
		"grep -z foo main.go", // NUL-delimited
		"grep -a foo main.go", // force binary as text
		"grep main.go",        // no path is fine, but no pattern is not
		"grep -e a -e b .",    // two patterns
		"grep -f pats.txt .",  // patterns from a file
	} {
		if got := Parse(strings.Fields(cmd)); got.Kind != KindPassthrough {
			t.Errorf("%q: kind = %v, want passthrough", cmd, got.Kind)
		}
	}
}

// A quoted operand arrives as ONE argv element because a real shell split it. Nothing in the
// parser may re-split on spaces.
func TestOperandsWithSpacesSurvive(t *testing.T) {
	got := Parse([]string{"head", "-5", "my file.py"})
	if got.Kind != KindRead || len(got.Operands) != 1 || got.Operands[0] != "my file.py" {
		t.Fatalf("got kind=%v operands=%v", got.Kind, got.Operands)
	}
}

func TestBaseNormalizesTheCommandWord(t *testing.T) {
	for _, tc := range [][2]string{
		{"/usr/bin/sed", "sed"},
		{"sed.exe", "sed"},
		{"SED", "sed"},
		{`C:\tools\rg.exe`, "rg"},
	} {
		if got := Base(tc[0]); got != tc[1] {
			t.Errorf("Base(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

// `grep -rn foo .` is the single most common spelling in the wild. A parser that treats the
// cluster as one unknown flag passes through every one of them, which is interception that
// never fires.
func TestShortFlagClustersExpand(t *testing.T) {
	for _, cmd := range []string{"grep -rn foo .", "grep -in foo .", "grep -rni foo ."} {
		got := Parse(strings.Fields(cmd))
		if got.Kind != KindGrep {
			t.Fatalf("%q: kind = %v (%s)", cmd, got.Kind, got.Why)
		}
		if strings.Contains(cmd, "i") && !got.Grep.IgnoreCase {
			t.Errorf("%q: -i lost in the cluster", cmd)
		}
	}
	// A cluster may end in a flag that takes an argument -- grep reads `-nA 3` as `-n -A 3`
	// -- but the letter has to be LAST, because everything after it is its value.
	if got := Parse(strings.Fields("grep -nA 3 foo .")); got.Kind != KindGrep || got.Grep.After != 3 {
		t.Errorf("cluster ending in -A: kind = %v after = %d", got.Kind, got.Grep.After)
	}
	// A cluster holding a letter aracne does not model must not be expanded at all: we
	// cannot know whether it wanted the next token.
	if got := Parse(strings.Fields("grep -nO 3 foo .")); got.Kind != KindPassthrough {
		t.Errorf("cluster with an unmodelled flag: kind = %v, want passthrough", got.Kind)
	}
}

// A path-less search means different things to different tools, and guessing wrong turns a
// question about piped input into a search of the whole repository.
func TestPathlessSearchFollowsTheToolsOwnDefault(t *testing.T) {
	if got := Parse(strings.Fields("grep foo")); got.Kind != KindPassthrough {
		t.Errorf("`grep foo` reads stdin: kind = %v, want passthrough", got.Kind)
	}
	if got := Parse(strings.Fields("rg foo")); got.Kind != KindGrep {
		t.Errorf("`rg foo` walks the cwd: kind = %v (%s), want grep", got.Kind, got.Why)
	}
}

// The dialect table. topogrep compiles with Go's regexp (RE2), so a pattern written in
// POSIX BRE -- which is what plain `grep` takes -- has to be rewritten before it means the
// same thing. The rows come in pairs on purpose: each BRE operator that RE2 spells bare,
// and each character BRE leaves ordinary that RE2 would read as an operator. Getting
// either backwards returns a confident answer to a different question, which is the one
// failure mode interception may never have.
func TestGrepBREPatternsAreTranslatedForRE2(t *testing.T) {
	// The POSIX classes follow LC_CTYPE (see asciiCtype), so the expectations below pin it
	// rather than inheriting whatever locale the machine running the tests has.
	t.Setenv("LC_ALL", "C.UTF-8")

	for _, tc := range []struct{ cmd, want string }{
		// BRE's escaped operators lose the backslash.
		{`grep a\|b .`, `a|b`},
		{`grep a\+ .`, `a+`},
		{`grep a\?b .`, `a?b`},
		{`grep \(ab\)\|c .`, `(ab)|c`},
		{`grep a\{2,3\} .`, `a{2,3}`},
		{`grep \<word\> .`, `\bword\b`},

		// ...and the same characters, bare, are ordinary in BRE and must gain one.
		{`grep a|b .`, `a\|b`},
		{`grep a+b .`, `a\+b`},
		{`grep a?b .`, `a\?b`},
		{`grep (ab) .`, `\(ab\)`},
		{`grep a{2} .`, `a\{2\}`},

		// Anchors and `*` are positional in BRE: operators where they lead or trail,
		// ordinary characters anywhere else.
		{`grep ^foo$ .`, `^foo$`},
		{`grep a^b .`, `a\^b`},
		// Only the FIRST caret anchors. A second one is ordinary even though a `*` after it
		// would still have nothing to repeat -- the two positional rules are not the same rule.
		{`grep ^^foo .`, `^\^foo`},
		{`grep ^* .`, `^\*`},
		{`grep \(^a\)\|^b .`, `(^a)|^b`},
		{`grep a$b .`, `a\$b`},
		{`grep *foo .`, `\*foo`},
		{`grep a*b .`, `a*b`},

		// A bracket expression is copied through: POSIX and RE2 read its contents alike,
		// so the `+` inside stays ordinary without any help.
		{`grep [a+b] .`, `[a+b]`},
		{`grep [[:alpha:]]+ .`, `[\p{L}]\+`},

		// The other dialects are already RE2 or already literal, and are left alone.
		{`grep -E a|b .`, `a|b`},
		{`egrep a|b .`, `a|b`},
		{`rg a+b .`, `a+b`},
		{`grep -F a+b .`, `a+b`},
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

// What BRE can say and RE2 cannot. Each of these has to reach the real grep rather than a
// rewrite that nearly means it.
func TestGrepBREPatternsRE2CannotExpressPassThrough(t *testing.T) {
	for _, cmd := range []string{
		`grep \(a\)\1 .`, // a backreference; RE2 has none
		`grep [a\]b] .`,  // `\` is ordinary inside POSIX brackets and an escape in RE2
		`grep [abc .`,    // unterminated bracket
		`grep [[.a.]] .`, // a collating element RE2 will not compile
	} {
		if got := Parse(strings.Fields(cmd)); got.Kind != KindPassthrough {
			t.Errorf("%q: kind = %v, want passthrough", cmd, got.Kind)
		}
	}
}

// -E, -F and -G are one setting spelled three ways, and GNU grep lets the last one win.
// Tracking them independently made `-F -E` both fixed and extended.
func TestGrepDialectFlagsResolveLastWins(t *testing.T) {
	for _, tc := range []struct {
		cmd     string
		pattern string
		fixed   bool
	}{
		{`grep -F -E a|b .`, `a|b`, false},
		{`grep -E -F a|b .`, `a|b`, true},
		{`grep -E -G a\|b .`, `a|b`, false},
		{`fgrep a+b .`, `a+b`, true},
		{`grep -rnG a\|b .`, `a|b`, false}, // -G must survive a short-flag cluster
	} {
		got := Parse(strings.Fields(tc.cmd))
		if got.Kind != KindGrep {
			t.Errorf("%q: kind = %v (%s), want grep", tc.cmd, got.Kind, got.Why)
			continue
		}
		if got.Grep.Pattern != tc.pattern || got.Grep.Fixed != tc.fixed {
			t.Errorf("%q: pattern = %q fixed = %v, want %q / %v",
				tc.cmd, got.Grep.Pattern, got.Grep.Fixed, tc.pattern, tc.fixed)
		}
	}
}

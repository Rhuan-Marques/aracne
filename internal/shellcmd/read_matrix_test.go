package shellcmd

import (
	"strings"
	"testing"
)

// THE READ MATRIX.
//
// A read command names a WINDOW: "the first twenty lines of this", "these lines of that".
// Interception may frame that window -- with the signature of the declaration it opens inside,
// with a context block -- but it may never answer a different window, and it may never answer
// a command whose flags mean something else entirely.
//
// So every row below is one of two things: a spelling that must land on an exact Window, or a
// spelling that must reach KindPassthrough. A flag read as the wrong window returns a
// confident answer to a different question, which is the one failure interception cannot have;
// a flag quietly dropped is the same failure wearing a helpful face.

// windowOf parses a command line that must classify as a read, and returns its Window.
func windowOf(t *testing.T, cmd string) Window {
	t.Helper()
	got := Parse(strings.Fields(cmd))
	if got.Kind != KindRead {
		t.Fatalf("%q: kind = %v (%s), want read", cmd, got.Kind, got.Why)
	}
	return got.Window
}

// mustPassRead asserts a read spelling reaches the real binary untouched.
func mustPassRead(t *testing.T, cmd string) {
	t.Helper()
	if got := Parse(strings.Fields(cmd)); got.Kind != KindPassthrough {
		t.Errorf("%q: kind = %v, want passthrough", cmd, got.Kind)
	}
}

// Every window spelling an agent types, and the exact Window it means. The rows are grouped by
// command because that is how they break: one reader's flag loop drifts, not all of them.
func TestReadWindowsMapExactly(t *testing.T) {
	whole := Window{Mode: WholeFile}
	for _, tc := range []struct {
		cmd  string
		want Window
	}{
		// The whole-file commands.
		{"cat f.go", whole},
		{"less f.go", whole},
		{"more f.go", whole},
		{"bat f.go", whole},

		// head: the first N, in every spelling coreutils accepts.
		{"head f.go", Window{Mode: Head, N: 10}},
		{"head -1 f.go", Window{Mode: Head, N: 1}},
		{"head -40 f.go", Window{Mode: Head, N: 40}},
		{"head -n 40 f.go", Window{Mode: Head, N: 40}},
		{"head -n40 f.go", Window{Mode: Head, N: 40}},
		{"head --lines=40 f.go", Window{Mode: Head, N: 40}},
		{"head -n 1 f.go", Window{Mode: Head, N: 1}},

		// tail: the last N, and the from-line-N form that a leading + selects.
		{"tail f.go", Window{Mode: Tail, N: 10}},
		{"tail -1 f.go", Window{Mode: Tail, N: 1}},
		{"tail -20 f.go", Window{Mode: Tail, N: 20}},
		{"tail -n 20 f.go", Window{Mode: Tail, N: 20}},
		{"tail -n20 f.go", Window{Mode: Tail, N: 20}},
		{"tail --lines=20 f.go", Window{Mode: Tail, N: 20}},
		{"tail -n +30 f.go", Window{Mode: FromLine, From: 30}},
		{"tail --lines=+30 f.go", Window{Mode: FromLine, From: 30}},

		// sed, in the four print-range spellings that are a pure window.
		{"sed -n 12,40p f.go", Window{Mode: Range, From: 12, To: 40}},
		{"sed -n 12p f.go", Window{Mode: Range, From: 12, To: 12}},
		{"sed -n 12,$p f.go", Window{Mode: FromLine, From: 12}},
		{"sed -n $p f.go", Window{Mode: Tail, N: 1}},
		{"sed --quiet 12,40p f.go", Window{Mode: Range, From: 12, To: 40}},
		{"sed -n -e 12,40p f.go", Window{Mode: Range, From: 12, To: 40}},
		{"sed -n --expression=12,40p f.go", Window{Mode: Range, From: 12, To: 40}},

		// awk, in the NR comparisons people reach for when they want a window.
		{"awk NR>=12&&NR<=40 f.go", Window{Mode: Range, From: 12, To: 40}},
		{"awk NR==12 f.go", Window{Mode: Range, From: 12, To: 12}},
		{"awk NR<=12 f.go", Window{Mode: Head, N: 12}},
		{"awk NR>=12 f.go", Window{Mode: FromLine, From: 12}},
		{"gawk NR==12 f.go", Window{Mode: Range, From: 12, To: 12}},
		{"mawk NR==12 f.go", Window{Mode: Range, From: 12, To: 12}},

		// bat's own range flag, including its open-ended forms.
		{"bat -r 10:20 f.go", Window{Mode: Range, From: 10, To: 20}},
		{"bat --line-range 10:20 f.go", Window{Mode: Range, From: 10, To: 20}},
		{"bat --line-range=10:20 f.go", Window{Mode: Range, From: 10, To: 20}},
		{"bat -r 10: f.go", Window{Mode: FromLine, From: 10}},
		{"bat -r :20 f.go", Window{Mode: Range, From: 1, To: 20}},

		// PowerShell's reader asks head's and tail's questions with other words.
		{"get-content f.go", whole},
		{"get-content -TotalCount 20 f.go", Window{Mode: Head, N: 20}},
		{"get-content -First 20 f.go", Window{Mode: Head, N: 20}},
		{"get-content -Head 20 f.go", Window{Mode: Head, N: 20}},
		{"get-content -Last 20 f.go", Window{Mode: Tail, N: 20}},
		{"get-content -Tail 20 f.go", Window{Mode: Tail, N: 20}},
		{"get-content -Path f.go", whole},
		{"get-content -LiteralPath f.go -Tail 5", Window{Mode: Tail, N: 5}},
	} {
		if got := windowOf(t, tc.cmd); got != tc.want {
			t.Errorf("%q: window = %+v, want %+v", tc.cmd, got, tc.want)
		}
	}
}

// The operands, which are what the window is applied TO. Losing one, re-splitting one, or
// mistaking a script for a path all produce an answer about the wrong file.
func TestReadOperandsSurviveIntact(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want []string
	}{
		{[]string{"cat", "a.go"}, []string{"a.go"}},
		{[]string{"cat", "a.go", "b.go", "c.go"}, []string{"a.go", "b.go", "c.go"}},
		{[]string{"head", "-5", "my file.py"}, []string{"my file.py"}},
		{[]string{"sed", "-n", "1,10p", "a.go"}, []string{"a.go"}},
		{[]string{"awk", "NR==5", "a.go"}, []string{"a.go"}},
		// A resource id stands exactly where a path does.
		{[]string{"head", "-10", "example.com/proj.Serve"}, []string{"example.com/proj.Serve"}},
	} {
		got := Parse(tc.argv)
		if got.Kind != KindRead {
			t.Errorf("%v: kind = %v (%s), want read", tc.argv, got.Kind, got.Why)
			continue
		}
		if strings.Join(got.Operands, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("%v: operands = %q, want %q", tc.argv, got.Operands, tc.want)
		}
	}
}

// sed and awk number lines across ALL their operands as one stream, so a window over several
// files is not a window aracne can answer per file. head, tail and cat genuinely are per file.
//
// The regression this pins: `sed -n '1,4p' a.go b.go` prints four lines, all from a.go, and
// aracne answered it with lines 1-4 of BOTH files -- lines the command never printed.
func TestMultiFileStreamReadersPassThrough(t *testing.T) {
	for _, argv := range [][]string{
		{"sed", "-n", "1,10p", "a.go", "b.go"},
		{"sed", "-n", "5p", "a.go", "b.go"},
		{"awk", "NR==5", "a.go", "b.go"},
		{"awk", "NR>=1&&NR<=5", "a.go", "b.go"},
	} {
		if got := Parse(argv); got.Kind != KindPassthrough {
			t.Errorf("%v: kind = %v, want passthrough (multi-file stream reader)", argv, got.Kind)
		}
	}
	// The per-file readers are unaffected: those really do restart at line 1 per operand.
	for _, argv := range [][]string{
		{"head", "-5", "a.go", "b.go"},
		{"tail", "-5", "a.go", "b.go"},
		{"cat", "a.go", "b.go"},
	} {
		if got := Parse(argv); got.Kind != KindRead || len(got.Operands) != 2 {
			t.Errorf("%v: kind = %v operands = %q, want a two-operand read", argv, got.Kind, got.Operands)
		}
	}
}

// `--` ends the options, so a pattern after it is a pattern even when it looks like a flag
// cluster. Expanding clusters in a pass over the whole argv shredded it: `grep -- -rn f`
// parsed as pattern "-r" with "-n" as an operand, where the real grep searches for "-rn".
func TestDashDashProtectsAFlagShapedPattern(t *testing.T) {
	for _, tc := range []struct {
		argv    []string
		pattern string
		ops     []string
	}{
		{[]string{"grep", "--", "-rn", "file.go"}, "-rn", []string{"file.go"}},
		{[]string{"grep", "--", "-i", "."}, "-i", []string{"."}},
		{[]string{"grep", "-rn", "foo", "."}, "foo", []string{"."}},
		{[]string{"grep", "-rn", "--", "-x", "."}, "-x", []string{"."}},
	} {
		got := Parse(tc.argv)
		if got.Kind != KindGrep {
			t.Errorf("%v: kind = %v (%s), want grep", tc.argv, got.Kind, got.Why)
			continue
		}
		if got.Grep.Pattern != tc.pattern {
			t.Errorf("%v: pattern = %q, want %q", tc.argv, got.Grep.Pattern, tc.pattern)
		}
		if strings.Join(got.Operands, "\x00") != strings.Join(tc.ops, "\x00") {
			t.Errorf("%v: operands = %q, want %q", tc.argv, got.Operands, tc.ops)
		}
	}
}

// Everything that is not a line window. Each row is a way answering it with one would change
// what the command means -- a different unit, a different rendering, a mutation, or a stream
// that has no end.
func TestNonWindowReadsPassThrough(t *testing.T) {
	for _, cmd := range []string{
		// A different UNIT: bytes, not lines.
		"head -c 40 f.go",
		"head --bytes=40 f.go",
		"tail -c 40 f.go",
		"tail --bytes=40 f.go",

		// A different SELECTION: "all but the last N" is not "the first N".
		"head -n -5 f.go",
		"head --lines=-5 f.go",
		"tail -n -5 f.go",

		// A different RENDERING. Every one of these transforms the bytes, and a
		// topology-framed window would answer as though it had not.
		"cat -n f.go", "cat -b f.go", "cat -A f.go", "cat -e f.go", "cat -t f.go",
		"cat -v f.go", "cat -s f.go", "cat -E f.go", "cat -T f.go", "cat --number f.go",
		"less -N f.go", "more -d f.go",
		"bat -n f.go", "bat --style=numbers f.go", "bat -p f.go", "bat --plain f.go",
		"nl f.go", "tac f.go", "xxd f.go", "od -c f.go", "hexdump -C f.go", "strings f.go",
		"rev f.go", "fold -w 80 f.go", "expand f.go", "column -t f.go", "pr f.go",

		// A stream that does not end, or that watches for changes.
		"tail -f f.go", "tail -F f.go", "tail --follow f.go", "tail --retry f.go",
		"tail -s 1 f.go", "tail --sleep-interval=1 f.go",

		// Quiet and verbose change the framing head and tail print around files.
		"head -q f.go", "head -v f.go", "head --quiet f.go", "head --verbose f.go",
		"tail -q f.go", "tail -v f.go",
		"head -z f.go", "tail -z f.go",

		// sed that is not a pure print-range.
		"sed -i s/a/b/ f.go",      // a mutation
		"sed s/a/b/ f.go",         // a substitution
		"sed 12,40p f.go",         // without -n every line is printed twice
		"sed -n 12,40d f.go",      // delete, not print
		"sed -n /foo/p f.go",      // an address, not a line window
		"sed -n 12,40!p f.go",     // negated
		"sed -n 0,10p f.go",       // line 0 does not exist
		"sed -n 40,12p f.go",      // an empty range
		"sed -n -e 1p -e 5p f.go", // two scripts
		"sed -E -n 12,40p f.go",   // a dialect flag aracne does not model here
		"sed -n 1~2p f.go",        // a GNU step address
		"sed -n 12,+5p f.go",      // a relative range
		"sed -n 12,40P f.go",      // print the first line of the pattern space

		// awk that is a program, not a window.
		"awk {print$2} f.go",
		"awk NR>=12&&NR<=40{print$1} f.go",
		"awk -F, NR==5 f.go",
		"awk -v x=1 NR==5 f.go",
		"awk END{print} f.go",
		"awk /foo/ f.go",
		"awk NR%2==0 f.go",

		// bat ranges that are not a range.
		"bat -r : f.go",
		"bat -r abc f.go",
		"bat -r 20:10 f.go",

		// git, in every spelling. `git show HEAD:f` prints what is COMMITTED, and the
		// topology indexes what is CHECKED OUT -- the two differ exactly when the question
		// is worth asking. Deciding correctly means running git, and this package does no
		// I/O, so all of it reaches the real binary.
		"git show HEAD:f.go",
		"git cat-file -p HEAD:f.go",
		"git show HEAD~1:f.go",
		"git show main:f.go",
		"git show abc1234:f.go",
		"git show HEAD",
		"git diff f.go",
		"git log f.go",
		"git blame f.go",
		"git cat-file -t HEAD:f.go",

		// PowerShell forms that are not a line window.
		"get-content -Raw f.go",
		"get-content -Encoding Byte f.go",
		"get-content -Wait f.go",
		"get-content -TotalCount 0 f.go",

		// No file to look up at all.
		"cat", "head", "tail", "head -5", "tail -5", "sed -n 1,10p", "cat -",
		"bat", "get-content",
	} {
		mustPassRead(t, cmd)
	}
}

// A count that is not a count must not silently become the default. `head -n 0` prints
// nothing; answering it with head's ten-line default is the opposite of what was asked.
func TestDegenerateReadCountsPassThrough(t *testing.T) {
	for _, cmd := range []string{
		"head -n 0 f.go",
		"head --lines=0 f.go",
		"head -n abc f.go",
		"head --lines=abc f.go",
		"tail -n 0 f.go",
		"tail --lines=0 f.go",
		"tail -n abc f.go",
		"tail -n +0 f.go",
		"tail -n + f.go",
		"sed -n 0p f.go",
		"awk NR==0 f.go",
		"bat -r 0:5 f.go",
		"get-content -TotalCount abc f.go",
		"get-content -Tail 0 f.go",
	} {
		mustPassRead(t, cmd)
	}
}

// `head -0` is the bare-count spelling of `head -n 0`, and it prints nothing either.
func TestBareZeroCountPassesThrough(t *testing.T) {
	for _, cmd := range []string{"head -0 f.go", "tail -0 f.go"} {
		mustPassRead(t, cmd)
	}
}

// The command word is normalized, so an absolute path, a Windows extension and a shouted name
// all reach the same reader. Anything else here is a different program.
func TestReadCommandWordNormalization(t *testing.T) {
	for _, cmd := range []string{
		"/bin/cat f.go",
		"/usr/bin/head -5 f.go",
		"CAT f.go",
		"Get-Content f.go",
	} {
		if got := Parse(strings.Fields(cmd)); got.Kind != KindRead {
			t.Errorf("%q: kind = %v (%s), want read", cmd, got.Kind, got.Why)
		}
	}
}

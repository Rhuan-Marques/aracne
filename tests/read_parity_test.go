package tests_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// READ PARITY.
//
// A read command names a WINDOW. Interception is allowed to FRAME that window -- the signature
// of the declaration it opens inside, an elision marker, a context block -- and it is allowed
// to answer a whole-FILE read with the file's shape instead of its text, which is the trade
// skeleton mode exists to make. What it may never do is answer a different window.
//
// Three properties, and each test below is one of them:
//
//	COMPLETE    every line the real command would have printed is in aracne's answer.
//	BOUNDED     no line from BELOW the window, and above it only a covering signature.
//	VERBATIM    every source line rendered is the file's line, byte for byte -- because a
//	            model builds an `edit` old_string out of it, and text aracne composed would
//	            miss.
//
// Where aracne deliberately returns LESS -- a whole-file skeleton -- the omission has to be
// ACCOUNTED FOR: an elision marker naming what is not shown. A silent omission is the failure
// mode a model cannot detect, because a shorter file looks exactly like a smaller file.

// --- fixture ---------------------------------------------------------------

// Every line of the fixture carries a unique tag, which is what makes the assertions exact:
// a tag in the output names the line it came from, so "is this line present" and "is it
// verbatim" are the same question asked twice.
var lineTag = regexp.MustCompile(`\b([LCG]\d\d)\b`)

// readFixture is a scanned project whose files are tagged line by line.
func readFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, full, body)
	}
	write("go.mod", "module demo\n\ngo 1.21\n")

	// L01..L07 are the PREAMBLE: package clause and imports. They belong to no declaration,
	// which is exactly why they are the lines a skeleton is most likely to lose.
	write("pkg/shapes.go", `package pkg // L01

import ( // L03
	"fmt" // L04
	"strings" // L05
) // L06

// Shape is anything with an area. L08
type Shape interface { // L09
	Area() float64 // L10
} // L11

// Circle is a round shape. L13
type Circle struct { // L14
	Radius float64 // L15
} // L16

// Area returns the circle area. L18
func (c Circle) Area() float64 { // L19
	return 3.14159 * c.Radius * c.Radius // L20
} // L21

// Describe renders a shape as text. L23
func Describe(s Shape) string { // L24
	var b strings.Builder // L25
	b.WriteString("shape:") // L26
	b.WriteString(fmt.Sprintf("%T", s)) // L27
	b.WriteString(" area=") // L28
	b.WriteString(fmt.Sprintf("%.2f", s.Area())) // L29
	b.WriteString("done") // L30
	total := Total([]Shape{s}) // L31
	b.WriteString(fmt.Sprintf("total=%.2f", total)) // L32
	return b.String() // L33
} // L34

// Total sums the areas of every shape. L36
func Total(shapes []Shape) float64 { // L37
	sum := 0.0 // L38
	for _, s := range shapes { // L39
		sum += s.Area() // L40
	} // L41
	return sum // L42
} // L43
`)

	// The class body is tagged C.. rather than L.., so a marker claiming the body is not
	// shown can be checked against the body actually being absent.
	write("app/main.py", `import sys # L01
import os # L02


class Runner: # L05
    """Runs things. C06"""
    def __init__(self, name): # C07
        self.name = name # C08
        self.count = 0 # C09

    def run(self, times): # C11
        for i in range(times): # C12
            if i % 2 == 0: # C13
                self.count += 1 # C14
            else: # C15
                self.count -= 1 # C16
        return self.count # C17


def main(): # L20
    r = Runner("x") # L21
    r.run(10) # L22
    print(r.count) # L23
`)

	// A declaration longer than the skeleton threshold, so a whole-file read of this file
	// actually elides something and the size claim has something to measure.
	var long strings.Builder
	long.WriteString("package pkg // G01\n\n// Walk is deliberately long. G03\nfunc Walk(n int) int { // G04\n")
	for i := 5; i < 30; i++ {
		fmt.Fprintf(&long, "\tn += %d // G%02d\n", i, i)
	}
	long.WriteString("\treturn n // G30\n} // G31\n")
	write("pkg/long.go", long.String())

	write("NOTES.md", "alpha\nbeta\ngamma\ndelta\nepsilon\n")
	write("ONELINE.txt", "only\n")
	write("NONEWLINE.txt", "no trailing newline")
	write("EMPTY.txt", "")

	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	// The read half is only intercepted in the two intercepting modes; the shipped default
	// intercepts searches alone, so a read-parity suite has to say which mode it tests.
	setMode(t, root, "intercept_line_ranges")
	return root
}

// --- runners ---------------------------------------------------------------

func runNative(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		t.Skipf("%s is not installed", argv[0])
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

func runAracRead(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(AracBin, append([]string{"cmd", "--"}, argv...)...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

// --- tag plumbing ----------------------------------------------------------

// fileTags maps a tag to the line number that carries it, and to the line's exact text.
func fileTags(t *testing.T, root, rel string) (map[string]int, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	at, text := map[string]int{}, map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		if m := lineTag.FindStringSubmatch(line); m != nil {
			at[m[1]] = i + 1
			text[m[1]] = line
		}
	}
	return at, text
}

// answerBody is aracne's rendering with the CONTEXT block removed. The context names
// neighbours and quotes their descriptions, which are prose ABOUT the file rather than lines
// OF it -- counting them as rendered source would make every assertion below vacuous.
func answerBody(out string) string {
	body, _, _ := strings.Cut(out, "\n# CONTEXT:")
	return body
}

// tagsIn returns the tags a rendering carries, each mapped to the line it was printed on.
func tagsIn(out string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(answerBody(out), "\n") {
		if m := lineTag.FindStringSubmatch(line); m != nil {
			found[m[1]] = line
		}
	}
	return found
}

// sortedKeys is for failure messages that have to be readable.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- COMPLETE --------------------------------------------------------------

// The windows an agent actually asks for. Every line the real command prints must be in
// aracne's answer -- a read that quietly returns fewer lines is the one failure a model
// cannot detect, because a shorter answer looks exactly like a shorter file.
func TestAWindowReturnsEveryLineItAskedFor(t *testing.T) {
	root := readFixture(t)

	for _, tc := range []struct {
		argv []string
		file string
	}{
		{[]string{"head", "-12", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"head", "-5", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"head", "-20", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"head", "-n", "30", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"head", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"tail", "-4", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"tail", "-12", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"tail", "-n", "+30", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "1,12p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "25,28p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "9,11p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "20p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "37,$p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"awk", "NR>=25&&NR<=28", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"awk", "NR==20", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"head", "-6", "app/main.py"}, "app/main.py"},
		{[]string{"sed", "-n", "11,17p", "app/main.py"}, "app/main.py"},
		{[]string{"tail", "-5", "app/main.py"}, "app/main.py"},
	} {
		want, _ := runNative(t, root, tc.argv...)
		got, _ := runAracRead(t, root, tc.argv...)
		have := tagsIn(got)

		var lost []string
		for _, line := range strings.Split(want, "\n") {
			m := lineTag.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if _, ok := have[m[1]]; !ok {
				lost = append(lost, m[1])
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			t.Errorf("`%s` dropped the lines %v it was asked for:\n%s",
				strings.Join(tc.argv, " "), lost, got)
		}
	}
}

// --- BOUNDED ---------------------------------------------------------------

// A frame extends UPWARD -- the signature of the declaration the window opens inside -- and
// never downward. A line below the window is a line the caller did not ask for and, for a
// `head`, is the difference between a peek and the whole file.
func TestAWindowReturnsNothingBelowIt(t *testing.T) {
	root := readFixture(t)
	at, _ := fileTags(t, root, "pkg/shapes.go")

	for _, tc := range []struct {
		argv []string
		last int // the last line the command asked for
	}{
		{[]string{"head", "-12", "pkg/shapes.go"}, 12},
		{[]string{"head", "-5", "pkg/shapes.go"}, 5},
		{[]string{"head", "-20", "pkg/shapes.go"}, 20},
		{[]string{"sed", "-n", "1,12p", "pkg/shapes.go"}, 12},
		{[]string{"sed", "-n", "9,11p", "pkg/shapes.go"}, 11},
		{[]string{"sed", "-n", "25,28p", "pkg/shapes.go"}, 28},
		{[]string{"awk", "NR==20", "pkg/shapes.go"}, 20},
	} {
		got, _ := runAracRead(t, root, tc.argv...)
		for tag := range tagsIn(got) {
			if at[tag] > tc.last {
				t.Errorf("`%s` returned line %d (%s), which is below the window it asked for:\n%s",
					strings.Join(tc.argv, " "), at[tag], tag, got)
			}
		}
	}
}

// Above the window, only the signature of a declaration the window is INSIDE may be shown.
// The fixture's declarations are known, so "is this frame legitimate" is a question with an
// exact answer rather than a budget.
func TestAboveTheWindowOnlyACoveringSignatureIsShown(t *testing.T) {
	root := readFixture(t)
	at, _ := fileTags(t, root, "pkg/shapes.go")

	// The declarations of pkg/shapes.go, by the tag on their first line.
	decls := []struct{ start, end int }{
		{at["L09"], at["L11"]}, // Shape
		{at["L14"], at["L16"]}, // Circle
		{at["L19"], at["L21"]}, // (Circle).Area
		{at["L24"], at["L34"]}, // Describe
		{at["L37"], at["L43"]}, // Total
	}
	covers := func(line, from int) bool {
		for _, d := range decls {
			if d.start <= line && from <= d.end && d.start <= from {
				return true
			}
		}
		return false
	}

	for _, tc := range []struct {
		argv []string
		from int
	}{
		{[]string{"sed", "-n", "25,28p", "pkg/shapes.go"}, 25},
		{[]string{"sed", "-n", "40,42p", "pkg/shapes.go"}, 40},
		{[]string{"tail", "-4", "pkg/shapes.go"}, 40},
		{[]string{"awk", "NR==20", "pkg/shapes.go"}, 20},
	} {
		got, _ := runAracRead(t, root, tc.argv...)
		for tag := range tagsIn(got) {
			line := at[tag]
			if line >= tc.from || covers(line, tc.from) {
				continue
			}
			t.Errorf("`%s` returned line %d (%s) from above the window, and it is not the "+
				"signature of a declaration the window is inside:\n%s",
				strings.Join(tc.argv, " "), line, tag, got)
		}
	}
}

// --- VERBATIM --------------------------------------------------------------

// Every source line rendered is the file's line, byte for byte. A model pastes one into an
// `edit` old_string; text aracne composed, reflowed or re-indented would miss.
func TestEveryRenderedLineIsTheFilesLine(t *testing.T) {
	root := readFixture(t)

	for _, tc := range []struct {
		argv []string
		file string
	}{
		{[]string{"head", "-12", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"sed", "-n", "25,28p", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"tail", "-6", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"cat", "pkg/shapes.go"}, "pkg/shapes.go"},
		{[]string{"cat", "app/main.py"}, "app/main.py"},
		{[]string{"sed", "-n", "11,17p", "app/main.py"}, "app/main.py"},
	} {
		_, text := fileTags(t, root, tc.file)
		got, _ := runAracRead(t, root, tc.argv...)
		for tag, rendered := range tagsIn(got) {
			want, known := text[tag]
			if !known {
				continue // a tag that is not this file's, from a second operand
			}
			if rendered != want {
				t.Errorf("`%s` rendered %s as %q, but the file's line is %q",
					strings.Join(tc.argv, " "), tag, rendered, want)
			}
		}
	}
}

// --- ACCOUNTED FOR ---------------------------------------------------------

// A whole-file read answers with the file's SHAPE, and that is the trade the surface exists to
// make -- but the shape has to include the part of a file that is not a declaration. The
// package clause and the imports are the first thing anyone reads a file for, they are five
// lines, and a skeleton that drops them drops them SILENTLY: there is no marker, and the model
// has no way to know it was not told.
func TestAWholeFileReadKeepsThePreamble(t *testing.T) {
	root := readFixture(t)

	for _, tc := range []struct {
		file     string
		preamble []string
	}{
		{"pkg/shapes.go", []string{"L01", "L03", "L04", "L05", "L06"}},
		{"app/main.py", []string{"L01", "L02"}},
	} {
		got, _ := runAracRead(t, root, "cat", tc.file)
		have := tagsIn(got)
		var missing []string
		for _, tag := range tc.preamble {
			if _, ok := have[tag]; !ok {
				missing = append(missing, tag)
			}
		}
		if len(missing) > 0 {
			t.Errorf("`cat %s` dropped the package/import preamble %v with no marker to say so:\n%s",
				tc.file, missing, got)
		}
	}
}

// An elision marker is a promise about what is NOT in the answer. A marker saying a
// declaration's body is not shown, above an answer that then shows the body, is worse than no
// marker: it tells the reader to go and fetch something they already have.
func TestAnElisionMarkerDoesNotContradictTheAnswer(t *testing.T) {
	root := readFixture(t)

	got, _ := runAracRead(t, root, "cat", "app/main.py")
	body := answerBody(got)
	if !strings.Contains(body, "⋯") {
		return // nothing was elided, so there is nothing to contradict
	}
	// The class body is tagged C.., so "the marker says it is not shown" and "it is not
	// shown" are checkable against each other.
	var shown []string
	for tag := range tagsIn(got) {
		if strings.HasPrefix(tag, "C") {
			shown = append(shown, tag)
		}
	}
	if !strings.Contains(body, "Runner") {
		return
	}
	if len(shown) > 0 {
		sort.Strings(shown)
		t.Errorf("`cat app/main.py` elided Runner's body and then printed %v of it:\n%s",
			shown, got)
	}
}

// Nothing is rendered twice. A line shown once as a frame and again as content is the same
// bytes charged to the caller twice.
func TestNoSourceLineIsRenderedTwice(t *testing.T) {
	root := readFixture(t)

	for _, argv := range [][]string{
		{"cat", "pkg/shapes.go"},
		{"cat", "app/main.py"},
		{"head", "-20", "pkg/shapes.go"},
		{"sed", "-n", "20,30p", "pkg/shapes.go"},
	} {
		got, _ := runAracRead(t, root, argv...)
		seen := map[string]int{}
		for _, line := range strings.Split(answerBody(got), "\n") {
			if m := lineTag.FindStringSubmatch(line); m != nil {
				seen[m[1]]++
			}
		}
		for tag, n := range seen {
			if n > 1 {
				t.Errorf("`%s` rendered %s %d times:\n%s", strings.Join(argv, " "), tag, n, got)
			}
		}
	}
}

// --- the surface's own contract --------------------------------------------

// A whole-file read has to be CHEAPER than the `cat` it replaced. That is the entire reason
// the terminal surface defaults to skeleton mode: a file read that costs more than the command
// it intercepted makes aracne worse than doing nothing, for the shape it should win most
// easily.
func TestAWholeFileReadCostsLessThanCat(t *testing.T) {
	root := readFixture(t)

	// Files holding a declaration longer than the skeleton threshold: something is elided,
	// so the answer must be smaller than the command it replaced.
	for _, file := range []string{"pkg/long.go", "app/main.py"} {
		want, _ := runNative(t, root, "cat", file)
		got, _ := runAracRead(t, root, "cat", file)
		if len(got) >= len(want) {
			t.Errorf("`cat %s`: aracne returned %d bytes where cat returns %d (%.2fx):\n%s",
				file, len(got), len(want), float64(len(got))/float64(len(want)), got)
		}
	}

	// A file whose every declaration is short has nothing to elide, so its skeleton IS the
	// file. That is the honest answer and it is allowed to cost the fence around it -- but
	// not more, because there is nothing else it could be spending bytes on.
	const fenceAllowance = 64
	want, _ := runNative(t, root, "cat", "pkg/shapes.go")
	got, _ := runAracRead(t, root, "cat", "pkg/shapes.go")
	if len(got) > len(want)+fenceAllowance {
		t.Errorf("`cat pkg/shapes.go`: nothing is elidable, so the answer should be the file "+
			"plus its fence; got %d bytes against cat's %d:\n%s", len(got), len(want), got)
	}
}

// Under intercept_line_ranges the model is never handed a resource id, because the whole point
// of the mode is that a span is a command it can run and an id is a token it has to be taught.
// A marker that names an id leaks exactly that.
func TestMarkersSpeakTheIdentificationModesVocabulary(t *testing.T) {
	root := readFixture(t)

	for _, argv := range [][]string{
		{"cat", "app/main.py"},
		{"cat", "pkg/shapes.go"},
		{"sed", "-n", "25,28p", "pkg/shapes.go"},
		{"tail", "-4", "pkg/shapes.go"},
	} {
		got, _ := runAracRead(t, root, argv...)
		for _, line := range strings.Split(got, "\n") {
			if !strings.Contains(line, "⋯") {
				continue
			}
			if strings.Contains(line, "demo/") || strings.Contains(line, "app/main.Runner") {
				t.Errorf("`%s` printed a resource id in a marker under intercept_line_ranges:\n%s",
					strings.Join(argv, " "), line)
			}
		}
	}
}

// --- passthrough, which is what makes the rest safe to ship ----------------

// Anything aracne does not model must be indistinguishable from the command itself -- stdout,
// stderr and exit status.
func TestUnmodelledAndUnindexedReadsAreByteIdentical(t *testing.T) {
	root := readFixture(t)

	for _, argv := range [][]string{
		{"cat", "-n", "pkg/shapes.go"},        // a rendering, not a window
		{"head", "-c", "20", "pkg/shapes.go"}, // bytes, not lines
		{"head", "-n", "-5", "pkg/shapes.go"}, // all but the last five
		{"tail", "-c", "20", "pkg/shapes.go"},
		{"head", "-0", "pkg/shapes.go"}, // prints nothing
		{"head", "-n", "0", "pkg/shapes.go"},
		{"sed", "-n", "1~2p", "pkg/shapes.go"}, // a step address
		{"sed", "12,40p", "pkg/shapes.go"},     // without -n, every line twice
		{"awk", "{print $1}", "pkg/shapes.go"},
		{"nl", "NOTES.md"},
		{"tac", "NOTES.md"},
		{"cat", "NOTES.md"},        // a real file with no topology nodes
		{"head", "-3", "NOTES.md"}, // ditto
		{"cat", "ONELINE.txt"},
		{"cat", "NONEWLINE.txt"},
		{"cat", "EMPTY.txt"},
		{"head", "-3", "EMPTY.txt"},
		{"sed", "-n", "100,200p", "pkg/shapes.go"}, // a window past the end
		{"tail", "-n", "+100", "pkg/shapes.go"},    // ditto
	} {
		want, wantStatus := runNative(t, root, argv...)
		got, gotStatus := runAracRead(t, root, argv...)
		if got != want {
			t.Errorf("`%s` diverged from the real command:\ngot:  %q\nwant: %q",
				strings.Join(argv, " "), got, want)
		}
		if gotStatus != wantStatus {
			t.Errorf("`%s`: exit %d, want %d", strings.Join(argv, " "), gotStatus, wantStatus)
		}
	}
}

// A command that fails must fail the way it would have, and that includes what it CALLS
// itself. `arac cmd -- cat missing` resolving cat's absolute path turns "cat: missing: No such
// file" into "/usr/bin/cat: missing: No such file" -- a different string for anything that
// reads it, and a puzzle for a model that did not know a wrapper was there.
func TestPassthroughErrorsNameTheCommandTheCallerTyped(t *testing.T) {
	root := readFixture(t)

	for _, argv := range [][]string{
		{"cat", "no-such-file.go"},
		{"head", "-3", "no-such-file.go"},
		{"cat", "pkg"},
		{"head", "-3", "pkg"},
	} {
		bin, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		native := exec.Command(bin, argv[1:]...)
		// argv[0] is what the command CALLS ITSELF in its own diagnostics. A shell passes
		// the word the caller typed; resolving it to an absolute path first is what turns
		// "cat: x: No such file" into "/usr/bin/cat: x: No such file".
		native.Args[0] = argv[0]
		native.Dir = root
		wantErr, _ := native.CombinedOutput()

		arac := exec.Command(AracBin, append([]string{"cmd", "--"}, argv...)...)
		arac.Dir = root
		gotErr, _ := arac.CombinedOutput()

		if string(gotErr) != string(wantErr) {
			t.Errorf("`%s` stderr diverged:\ngot:  %q\nwant: %q",
				strings.Join(argv, " "), gotErr, wantErr)
		}
		if native.ProcessState.ExitCode() != arac.ProcessState.ExitCode() {
			t.Errorf("`%s`: exit %d, want %d", strings.Join(argv, " "),
				arac.ProcessState.ExitCode(), native.ProcessState.ExitCode())
		}
	}
}

// `git show HEAD:path` asks what is COMMITTED. Answering it from the working tree is a wrong
// answer to the one question the command exists for -- and it is wrong precisely when it
// matters, which is when the file has uncommitted changes.
func TestGitShowAnswersFromTheCommitNotTheWorktree(t *testing.T) {
	root := readFixture(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}
	// An uncommitted line. `git show HEAD:` must not know about it.
	path := filepath.Join(root, "pkg", "shapes.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("\nfunc Uncommitted() {} // L99\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")

	got, _ := runAracRead(t, root, "git", "show", "HEAD:pkg/shapes.go")
	if strings.Contains(got, "L99") || strings.Contains(got, "Uncommitted") {
		t.Errorf("`git show HEAD:pkg/shapes.go` returned an uncommitted line:\n%s", got)
	}
}

// A read of a file in a directory that is not a git repository must fail the way git fails.
func TestGitShowOutsideARepositoryFailsLikeGit(t *testing.T) {
	root := readFixture(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	want, wantStatus := runNative(t, root, "git", "show", "HEAD:pkg/shapes.go")
	got, gotStatus := runAracRead(t, root, "git", "show", "HEAD:pkg/shapes.go")
	if gotStatus != wantStatus {
		t.Errorf("outside a repository: exit %d, want %d\ngot:\n%s", gotStatus, wantStatus, got)
	}
	if wantStatus != 0 && strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("outside a repository: aracne answered %q where git answered %q", got, want)
	}
}

// A served read succeeds; the shell's own vocabulary survives for the rest.
func TestReadExitStatusVocabulary(t *testing.T) {
	root := readFixture(t)

	for _, tc := range []struct {
		argv []string
		want int
	}{
		{[]string{"head", "-5", "pkg/shapes.go"}, 0},
		{[]string{"cat", "pkg/shapes.go"}, 0},
		{[]string{"sed", "-n", "1,5p", "pkg/shapes.go"}, 0},
		{[]string{"cat", "no-such-file.go"}, 1},
		{[]string{"head", "-3", "no-such-file.go"}, 1},
	} {
		if _, got := runAracRead(t, root, tc.argv...); got != tc.want {
			t.Errorf("`%s`: exit %d, want %d", strings.Join(tc.argv, " "), got, tc.want)
		}
	}
}

// A resource id stands where a path does, and the window applies to the resource's BODY --
// `head -3 pkg.Describe` is the first three lines of the function, not of the file that holds
// it. The signature is framing, so it does not spend one of the three.
func TestAResourceIDIsWindowedAgainstItsOwnBody(t *testing.T) {
	root := readFixture(t)
	_, text := fileTags(t, root, "pkg/shapes.go")

	got, status := runAracRead(t, root, "head", "-3", "demo/pkg.Describe")
	if status != 0 {
		t.Fatalf("head on a resource id exited %d:\n%s", status, got)
	}
	have := tagsIn(got)
	if _, ok := have["L24"]; !ok {
		t.Errorf("the declaration's own signature %q is missing:\n%s", text["L24"], got)
	}
	for _, tag := range []string{"L25", "L26", "L27"} {
		if _, ok := have[tag]; !ok {
			t.Errorf("body line %s (%q) is missing from `head -3`:\n%s", tag, text[tag], got)
		}
	}
	if _, ok := have["L33"]; ok {
		t.Errorf("`head -3` returned the end of the function:\n%s", got)
	}

	tail, _ := runAracRead(t, root, "tail", "-2", "demo/pkg.Describe")
	if _, ok := tagsIn(tail)["L33"]; !ok {
		t.Errorf("`tail -2` on a resource id lost its last line:\n%s", tail)
	}
	if _, ok := tagsIn(tail)["L25"]; ok {
		t.Errorf("`tail -2` on a resource id returned its first line:\n%s", tail)
	}
}

// Several operands are several answers, and they must all be there. A half-enhanced `cat a b`
// -- one file framed, the other missing -- is harder to read than either.
func TestSeveralOperandsAreAllAnswered(t *testing.T) {
	root := readFixture(t)

	got, status := runAracRead(t, root, "cat", "pkg/shapes.go", "app/main.py")
	if status != 0 {
		t.Fatalf("exit %d:\n%s", status, got)
	}
	if !strings.Contains(got, "pkg/shapes.go") || !strings.Contains(got, "app/main.py") {
		t.Errorf("`cat a b` did not answer for both files:\n%s", got)
	}
	// One unanswerable operand hands the WHOLE command back, rather than answering half.
	mixed, _ := runAracRead(t, root, "cat", "pkg/shapes.go", "NOTES.md")
	want, _ := runNative(t, root, "cat", "pkg/shapes.go", "NOTES.md")
	if mixed != want {
		t.Errorf("`cat indexed unindexed` must pass through whole:\ngot:  %q\nwant: %q", mixed, want)
	}
}

// The window arithmetic itself, at the edges where an off-by-one hides: the first line, the
// last line, a window larger than the file, a file of one line.
func TestWindowEdges(t *testing.T) {
	root := readFixture(t)
	at, _ := fileTags(t, root, "pkg/shapes.go")
	total := 0
	for _, line := range at {
		if line > total {
			total = line
		}
	}

	for _, tc := range []struct {
		argv    []string
		wantTag string
		absent  string
	}{
		{[]string{"head", "-1", "pkg/shapes.go"}, "L01", "L03"},
		{[]string{"sed", "-n", "1p", "pkg/shapes.go"}, "L01", "L03"},
		{[]string{"tail", "-1", "pkg/shapes.go"}, fmt.Sprintf("L%02d", total), "L01"},
		{[]string{"sed", "-n", "$p", "pkg/shapes.go"}, fmt.Sprintf("L%02d", total), "L01"},
		{[]string{"tail", "-n", "+" + strconv.Itoa(total), "pkg/shapes.go"}, fmt.Sprintf("L%02d", total), "L01"},
	} {
		got, _ := runAracRead(t, root, tc.argv...)
		have := tagsIn(got)
		if _, ok := have[tc.wantTag]; !ok {
			t.Errorf("`%s` lost %s:\n%s", strings.Join(tc.argv, " "), tc.wantTag, got)
		}
		if _, ok := have[tc.absent]; ok {
			t.Errorf("`%s` returned %s, which is outside it (%v):\n%s",
				strings.Join(tc.argv, " "), tc.absent, sortedKeys(have), got)
		}
	}
}

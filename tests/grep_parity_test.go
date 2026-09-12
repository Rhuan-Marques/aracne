package tests_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// GREP PARITY.
//
// Interception's promise for a search is that the command comes back no WORSE than the real
// binary would have answered it. "No worse" is not "identical": aracne is allowed to add --
// a resource header above a run of matches, a node whose NAME or stored DESCRIPTION matched
// where the file's text never did. It is not allowed to subtract, to renumber, to restate a
// line as something other than what the file holds, or to answer a counting question with a
// different count.
//
// So every test below is one of two shapes:
//
//	SUPERSET   every (path, line) the real grep reported must appear in aracne's answer, and
//	           the text it reports for that line must be the file's line, byte for byte.
//	EXACT      a question whose answer is a NUMBER or a decision -- `-c`, `-l`, the exit
//	           status, a per-file cap -- where "aracne may add" is not available, because
//	           adding changes the answer rather than enriching it.
//
// Where aracne diverges DELIBERATELY (a bounded result, pruned ignore directories, tiered
// ordering), the divergence is pinned by its own test rather than left to drift.

// --- fixture ---------------------------------------------------------------

// grepFixture is a scanned project shaped for search comparison: several files, several
// languages, indentation that matters, a dot-directory, a binary, and a file with more
// matches than the default cap.
func grepFixture(t *testing.T) string {
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
	write("pkg/shapes.go", `package pkg

import (
	"fmt"
	"strings"
)

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

// Circle is a round shape.
type Circle struct {
	Radius float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 {
	return 3.14159 * c.Radius * c.Radius
}

// Describe renders a shape as text.
func Describe(s Shape) string {
	var b strings.Builder
	b.WriteString("shape:")
	b.WriteString(fmt.Sprintf("%T", s))
	total := Total([]Shape{s})
	b.WriteString(fmt.Sprintf("total=%.2f", total))
	return b.String()
}

// Total sums the areas of every shape.
func Total(shapes []Shape) float64 {
	sum := 0.0
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}
`)
	write("pkg/util.go", `package pkg

import "errors"

var ErrEmpty = errors.New("empty")

// Validate checks a name.
func Validate(name string) error {
	if name == "" {
		return ErrEmpty
	}
	return nil
}

// helper is unexported.
func helper(a, b int) int {
	return a + b
}
`)
	write("cmd/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}\n")
	write("README.md", "# Demo\n\nThis project has a Describe function.\n\nTODO: write the docs\n")
	write("notes.txt", "alpha\nbeta\nTODO fix this\ngamma\n")
	// A dot-directory a model searches for constantly (`grep -rn runs-on .`).
	write(".github/workflows/ci.yml", "name: ci\njobs:\n  build:\n    runs-on: ubuntu-latest\n")
	// A file whose match count exceeds topogrep's default cap.
	var big strings.Builder
	big.WriteString("package pkg\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&big, "// swarm%d marker\n", i)
	}
	write("pkg/swarm.go", big.String())

	// A binary. Real grep says "binary file matches" and prints no content; whatever aracne
	// does, it must not put NUL bytes in a model's context window.
	if err := os.WriteFile(filepath.Join(root, "blob.bin"),
		[]byte("swarmneedle\x00\x01\x02binary\nswarmneedle again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return root
}

// fixturePaths are the display paths a result row may be keyed by, longest first so a prefix
// test never matches the shorter of two nested names.
var fixturePaths = []string{
	".github/workflows/ci.yml",
	"pkg/shapes.go",
	"pkg/util.go",
	"pkg/swarm.go",
	"cmd/main.go",
	"README.md",
	"notes.txt",
	"blob.bin",
	"go.mod",
}

// --- runners ---------------------------------------------------------------

// runGrep runs the real grep and returns stdout and its exit status.
func runGrep(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	bin, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("grep is not installed")
	}
	cmd := exec.Command(bin, argv...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

// runAracGrep runs the same command through `arac cmd`, which is what the guard's rewrite
// makes the shell execute.
func runAracGrep(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(AracBin, append([]string{"cmd", "--", "grep"}, argv...)...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

// --- row parsing -----------------------------------------------------------

// row is one rendered result line: a match (`path:line:text`) or a context line
// (`path-line-text`).
type row struct {
	path string
	line int
	text string
}

func (r row) key() string { return fmt.Sprintf("%s:%d", r.path, r.line) }

// parseRows splits an output into match rows and context rows, discarding aracne's resource
// headers and trailers, which are the additions it is allowed to make.
func parseRows(out string) (matches, context []row) { return parseRowsIn(out, "") }

// parseRowsIn is parseRows for an output that may not name its file: both greps omit the
// filename when exactly one file was searched, so `implied` is the path every bare `N:text`
// row belongs to. Re-attaching it here keeps one row shape for every comparison below.
func parseRowsIn(out, implied string) (matches, context []row) {
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" || strings.HasPrefix(ln, "# ") || strings.HasPrefix(ln, "…") {
			continue
		}
		// The real grep prefixes a `.` root with "./"; aracne does not. Same file.
		ln = strings.TrimPrefix(ln, "./")
		// A bare row opens with its line number. The separator that follows it is the one
		// the path takes too -- `:` for a match, `-` for context.
		if implied != "" && ln[0] >= '0' && ln[0] <= '9' {
			if i := strings.IndexFunc(ln, func(r rune) bool { return r < '0' || r > '9' }); i > 0 {
				ln = implied + string(ln[i]) + ln
			}
		}
		for _, p := range fixturePaths {
			if !strings.HasPrefix(ln, p) || len(ln) <= len(p) {
				continue
			}
			sep := ln[len(p)]
			if sep != ':' && sep != '-' {
				continue
			}
			rest := ln[len(p)+1:]
			end := strings.IndexByte(rest, sep)
			if end < 0 {
				continue
			}
			n, err := strconv.Atoi(rest[:end])
			if err != nil {
				continue
			}
			r := row{path: p, line: n, text: rest[end+1:]}
			if sep == ':' {
				matches = append(matches, r)
			} else {
				context = append(context, r)
			}
			break
		}
	}
	return matches, context
}

// singleFileOperand is the path a command named when it named exactly one file, and "" for
// every other shape. That is the one case where grep -- and now aracne -- prints no filename,
// so it is what the rows have to be read against.
func singleFileOperand(root string, argv []string) string {
	var files []string
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, a)); err == nil && !info.IsDir() {
			files = append(files, a)
		}
	}
	if len(files) == 1 {
		return files[0]
	}
	return ""
}

func keys(rows []row) map[string]row {
	out := make(map[string]row, len(rows))
	for _, r := range rows {
		out[r.key()] = r
	}
	return out
}

// fileLine returns line n of a file, exactly as it is stored.
func fileLine(t *testing.T, root, rel string, n int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if n < 1 || n > len(lines) {
		t.Fatalf("%s has no line %d", rel, n)
	}
	return lines[n-1]
}

// --- SUPERSET: aracne may add, never subtract ------------------------------

// The commands an agent actually types. Every match the real grep found must be in aracne's
// answer; a search that quietly returns fewer hits is the one failure a model cannot detect,
// because a shorter result looks exactly like a smaller codebase.
func TestNoMatchTheRealGrepFindsIsEverLost(t *testing.T) {
	root := grepFixture(t)

	for _, argv := range [][]string{
		{"-rn", "func", "."},
		{"-rn", "WriteString", "."},
		{"-rin", "todo", "."},
		{"-rn", "^func", "."},
		{"-rn", "float64$", "."},
		{"-rnw", "Total", "."},
		{"-rnF", "fmt.Sprintf", "."},
		{"-rnE", "func (Total|Describe)", "."},
		{"-rn", "--include=*.go", "func", "."},
		{"-rn", "Err[A-Z]", "."},
		{"-rn", "func", "pkg"},
		{"-n", "Total", "pkg/shapes.go"},
		{"-rn", "runs-on", "."},
	} {
		want, wantStatus := runGrep(t, root, append([]string{"-H"}, argv...)...)
		got, _ := runAracGrep(t, root, argv...)
		wantRows, _ := parseRows(want)
		gotRows, _ := parseRowsIn(got, singleFileOperand(root, argv))
		have := keys(gotRows)

		var lost []string
		for _, r := range wantRows {
			if _, ok := have[r.key()]; !ok {
				lost = append(lost, r.key())
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			t.Errorf("grep %s lost %d of %d matches: %v\naracne said:\n%s",
				strings.Join(argv, " "), len(lost), len(wantRows), lost, got)
		}
		// A search the real grep answered with hits must not come back as a failure.
		if wantStatus == 0 {
			if _, gotStatus := runAracGrep(t, root, argv...); gotStatus != 0 {
				t.Errorf("grep %s: real grep exited 0, aracne exited %d",
					strings.Join(argv, " "), gotStatus)
			}
		}
	}
}

// A match's TEXT is the line, and a model pastes it straight into an edit. Trimming its
// indentation makes the row a description of the line rather than the line -- and in Python,
// where indentation is the syntax, it also loses the only clue to what block the hit is in.
func TestAMatchRowCarriesTheFilesLineVerbatim(t *testing.T) {
	root := grepFixture(t)

	got, _ := runAracGrep(t, root, "-rn", "sum", ".")
	matches, _ := parseRows(got)
	if len(matches) == 0 {
		t.Fatalf("no matches to check:\n%s", got)
	}
	for _, r := range matches {
		if want := fileLine(t, root, r.path, r.line); r.text != want {
			t.Errorf("%s: text = %q, want the file's line %q", r.key(), r.text, want)
		}
	}
}

// --- context: the numbers have to be the lines --------------------------------

// Every context row names a line number. Real grep's `path-N-` is the line at N; a row that
// says 34 and carries the text of line 35 is a wrong answer with a plausible shape, and the
// model has no way to notice.
func TestContextRowsNameTheLineTheyActuallyCarry(t *testing.T) {
	root := grepFixture(t)

	for _, argv := range [][]string{
		{"-rn", "-B", "2", "Total", "."},
		{"-rn", "-A", "2", "Total", "."},
		{"-rn", "-C", "1", "sum", "."},
		{"-rn", "-B", "3", "return", "pkg/util.go"},
	} {
		got, _ := runAracGrep(t, root, argv...)
		_, context := parseRowsIn(got, singleFileOperand(root, argv))
		if len(context) == 0 {
			t.Errorf("grep %s produced no context rows:\n%s", strings.Join(argv, " "), got)
			continue
		}
		for _, r := range context {
			if want := fileLine(t, root, r.path, r.line); r.text != want {
				t.Errorf("grep %s: context row %s carries %q, but line %d of %s is %q",
					strings.Join(argv, " "), r.key(), r.text, r.line, r.path, want)
			}
		}
	}
}

// Real grep prints each line once: overlapping context windows are merged, and a line that
// matched is printed as a match, not also as its neighbour's context. Repeating them inflates
// the answer with bytes that say nothing new -- the exact cost interception is supposed to cut.
func TestContextIsMergedRatherThanRepeated(t *testing.T) {
	root := grepFixture(t)

	for _, argv := range [][]string{
		{"-rn", "-C", "1", "sum", "."},
		{"-rn", "-A", "2", "-B", "1", "sum", "."},
		{"-rn", "-B", "2", "Total", "pkg/shapes.go"},
	} {
		got, _ := runAracGrep(t, root, argv...)
		matches, context := parseRowsIn(got, singleFileOperand(root, argv))

		seen := map[string]int{}
		for _, r := range append(append([]row{}, matches...), context...) {
			seen[r.key()]++
		}
		var repeated []string
		for k, n := range seen {
			if n > 1 {
				repeated = append(repeated, fmt.Sprintf("%s x%d", k, n))
			}
		}
		if len(repeated) > 0 {
			sort.Strings(repeated)
			t.Errorf("grep %s printed the same line more than once: %v\n%s",
				strings.Join(argv, " "), repeated, got)
		}
	}
}

// --- EXACT: the questions whose answer is a number ---------------------------

// `-c` is a COUNT. A count that includes rows the file's text never produced -- a node whose
// name or stored description matched -- is not a count of anything the caller asked about, and
// nothing in the output says so.
func TestCountModeCountsWhatTheRealGrepCounts(t *testing.T) {
	root := grepFixture(t)

	for _, pattern := range []string{"func", "WriteString", "Total", "TODO"} {
		want, _ := runGrep(t, root, "-rc", pattern, ".")
		got, _ := runAracGrep(t, root, "-rc", pattern, ".")

		wantCounts := map[string]string{}
		for _, ln := range strings.Split(strings.TrimSpace(want), "\n") {
			path, n, ok := strings.Cut(strings.TrimPrefix(ln, "./"), ":")
			if ok && n != "0" {
				wantCounts[path] = n
			}
		}
		for _, ln := range strings.Split(strings.TrimSpace(got), "\n") {
			path, n, ok := strings.Cut(strings.TrimPrefix(ln, "./"), ":")
			if !ok {
				continue
			}
			if w, known := wantCounts[path]; known && w != n {
				t.Errorf("grep -rc %s %s: aracne counted %s, the real grep counted %s",
					pattern, path, n, w)
			}
		}
	}
}

// `-c` on ONE file prints a bare number -- that is the whole reason a caller reaches for it
// instead of piping to `wc -l`. Prefixing the path changes the field the caller reads.
func TestCountOfASingleFileIsABareNumber(t *testing.T) {
	root := grepFixture(t)

	want, _ := runGrep(t, root, "-c", "func", "pkg/util.go")
	got, _ := runAracGrep(t, root, "-c", "func", "pkg/util.go")
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("grep -c func pkg/util.go: aracne said %q, the real grep said %q",
			strings.TrimSpace(got), strings.TrimSpace(want))
	}
}

// Content obeys the same rule `-c` does, and for the same reason. `grep -n pat file` prints
// `line:text`: the filename is a field grep adds only when it searched more than ONE file,
// and a caller who typed the path is reading the first field for a line number. Prefixing it
// moves that field silently -- `cut -d: -f1` stops being a list of line numbers, and
// `sed -n "$(grep -n pat f | cut -d: -f1)p" f` reads a path where it wanted an address.
//
// This is the one addition aracne may not make. A resource header above a run of matches is
// an extra LINE, which a caller can skip; a path welded onto every row is an extra FIELD,
// which changes what the rows the caller did want mean.
func TestContentOfASingleFileIsNotPathPrefixed(t *testing.T) {
	root := grepFixture(t)

	const file = "pkg/util.go"
	for _, argv := range [][]string{
		{"-n", "func", file},
		{"-rn", "func", file},
		{"-n", "-B", "1", "return", file},
	} {
		want, _ := runGrep(t, root, argv...)
		got, _ := runAracGrep(t, root, argv...)
		spelled := strings.Join(argv, " ")

		for _, ln := range strings.Split(got, "\n") {
			// Headers and trailers are additions aracne is allowed to make, and the header
			// is the one place naming the file still says something.
			if ln == "" || ln == "--" || strings.HasPrefix(ln, "# ") || strings.HasPrefix(ln, "…") {
				continue
			}
			if strings.HasPrefix(ln, file) {
				t.Errorf("grep %s: row %q names the file the caller already named; "+
					"the real grep answered:\n%s", spelled, ln, want)
			}
		}

		// Having dropped the field, the rows must still BE the answer: every line the real
		// grep reported, carrying the file's text.
		wantRows, wantCtx := parseRowsIn(want, file)
		gotRows, gotCtx := parseRowsIn(got, file)
		if len(wantRows) == 0 {
			t.Fatalf("grep %s found nothing to compare:\n%s", spelled, want)
		}
		have := keys(append(append([]row{}, gotRows...), gotCtx...))
		for _, r := range append(append([]row{}, wantRows...), wantCtx...) {
			mine, ok := have[r.key()]
			if !ok {
				t.Errorf("grep %s lost line %d:\n%s", spelled, r.line, got)
				continue
			}
			if mine.text != r.text {
				t.Errorf("grep %s: line %d reads %q, the file holds %q",
					spelled, r.line, mine.text, r.text)
			}
		}
	}
}

// A search that found nothing prints NOTHING and exits 1. Every caller that captures grep's
// stdout -- `files=$(grep -rl …)`, `[ -z "$(grep …)" ]` -- reads prose as a result.
func TestAnEmptyResultPrintsNothingOnStdout(t *testing.T) {
	root := grepFixture(t)

	// Nothing at all, for the modes where the real grep prints nothing at all.
	for _, argv := range [][]string{
		{"-rl", "zzz-no-such-token", "."},
		{"-rn", "zzz-no-such-token", "."},
		{"-c", "zzz-no-such-token", "pkg/util.go"},
	} {
		want, wantStatus := runGrep(t, root, argv...)
		got, gotStatus := runAracGrep(t, root, argv...)
		if strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Errorf("grep %s with no matches: aracne printed %q on stdout, the real grep printed %q",
				strings.Join(argv, " "), got, want)
		}
		if gotStatus != wantStatus {
			t.Errorf("grep %s: exit %d, want %d", strings.Join(argv, " "), gotStatus, wantStatus)
		}
	}

	// `grep -rc` over a tree prints `path:0` for every file it looked at, which aracne does
	// not reproduce -- listing every unmatched file is noise, and the question was "how many
	// matches". What it may never do is answer with prose: whatever comes back has to parse
	// as counts, and the status still has to say nothing was found.
	got, gotStatus := runAracGrep(t, root, "-rc", "zzz-no-such-token", ".")
	for _, ln := range strings.Split(strings.TrimSpace(got), "\n") {
		if ln == "" {
			continue
		}
		if _, n, ok := strings.Cut(ln, ":"); !ok || n != "0" {
			t.Errorf("grep -rc with no matches printed %q, which is not a count", ln)
		}
	}
	if gotStatus != 1 {
		t.Errorf("grep -rc with no matches: exit %d, want 1", gotStatus)
	}
}

// `-l` and `-c` answer "which files contain this text" and "how many times". A node that
// matched on its NAME or its DESCRIPTION is a genuine aracne finding and belongs in content
// mode, where the header explains it. In a bare file list it is indistinguishable from a
// textual hit, so `grep -rl` reports files that do not contain the string -- and the caller's
// next move is to open them.
func TestFileListAndCountModesReportOnlyTextualMatches(t *testing.T) {
	root := grepFixture(t)

	// A pattern that matches resource IDs and nothing in any file's text. The reference grep
	// is told to skip `.aracne`, which holds the topology database -- the one file in the
	// tree that really does contain every resource id, and the one no search descends into.
	const idOnly = "demo/pkg"
	want, wantStatus := runGrep(t, root, "-rl", "--exclude-dir=.aracne", idOnly, ".")
	got, gotStatus := runAracGrep(t, root, "-rl", idOnly, ".")
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("grep -rl %q: aracne listed %q, but no file contains that text (real grep: %q)",
			idOnly, strings.TrimSpace(got), strings.TrimSpace(want))
	}
	if gotStatus != wantStatus {
		t.Errorf("grep -rl %q: exit %d, want %d (nothing matched)", idOnly, gotStatus, wantStatus)
	}

	// The same rows inflate `-c`, where the damage is worse: a count is a number, and there
	// is no header to say that five of them came from a resource id rather than the file.
	wantC, _ := runGrep(t, root, "-rc", "--exclude-dir=.aracne", idOnly, ".")
	gotC, _ := runAracGrep(t, root, "-rc", idOnly, ".")
	for _, ln := range strings.Split(strings.TrimSpace(gotC), "\n") {
		path, n, ok := strings.Cut(strings.TrimPrefix(ln, "./"), ":")
		if !ok || n == "0" {
			continue
		}
		if !strings.Contains(wantC, path+":"+n) {
			t.Errorf("grep -rc %q: aracne counted %s matches in %s, which contains the "+
				"string zero times", idOnly, n, path)
		}
	}
}

// `-m N` is grep's PER-FILE cap. `grep -rm 1 foo .` is how a caller asks for one hit in each
// file -- a repository-wide index, one line per file. Reading it as a cap on the whole result
// answers with one line, total.
func TestMaxCountCapsEachFileNotTheWholeResult(t *testing.T) {
	root := grepFixture(t)

	want, _ := runGrep(t, root, "-Hrn", "-m", "1", "func", ".")
	got, _ := runAracGrep(t, root, "-rn", "-m", "1", "func", ".")
	wantRows, _ := parseRows(want)
	gotRows, _ := parseRows(got)

	wantFiles := map[string]bool{}
	for _, r := range wantRows {
		wantFiles[r.path] = true
	}
	gotFiles := map[string]bool{}
	for _, r := range gotRows {
		gotFiles[r.path] = true
	}
	if len(gotFiles) < len(wantFiles) {
		t.Errorf("grep -rn -m 1 func .: aracne covered %d files, the real grep covered %d "+
			"(a per-file cap became a cap on the whole result)\n%s", len(gotFiles), len(wantFiles), got)
	}
}

// `-w` is not `\b…\b`. POSIX requires the match to be bounded by non-word constituents, which
// is also satisfied at a boundary between two non-word characters -- so `grep -w '=='` finds
// `if name == ""`. `\b` requires a word character on the other side, and finds nothing.
func TestWholeWordMatchesWhatGrepWMatches(t *testing.T) {
	root := grepFixture(t)

	for _, pattern := range []string{"Total", "==", "sum", "b"} {
		want, wantStatus := runGrep(t, root, "-Hrnw", pattern, ".")
		got, _ := runAracGrep(t, root, "-rnw", pattern, ".")
		wantRows, _ := parseRows(want)
		have := keys(func() []row { m, _ := parseRows(got); return m }())

		var lost []string
		for _, r := range wantRows {
			if _, ok := have[r.key()]; !ok {
				lost = append(lost, r.key())
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			t.Errorf("grep -rnw %q lost %v (exit status was %d for the real grep)\naracne said:\n%s",
				pattern, lost, wantStatus, got)
		}
	}
}

// A binary file is not text, and real grep refuses to print its bytes for a reason. Whatever
// aracne decides to do with one, NUL bytes must never reach the caller's terminal -- or, for
// an agent, its context window.
func TestBinaryFileBytesAreNeverPrinted(t *testing.T) {
	root := grepFixture(t)

	got, status := runAracGrep(t, root, "-rn", "swarmneedle", ".")
	if strings.ContainsRune(got, 0) {
		t.Errorf("a NUL byte from blob.bin reached stdout:\n%q", got)
	}
	// The file still MATCHED, and saying so is grep's own wording. Silently dropping it
	// would be the other way to get this wrong.
	//
	// `./blob.bin`, not `blob.bin`: real grep echoes the operand it walked, so a search rooted
	// at `.` reports every path under it with that prefix. filepath.Join swallows it during the
	// walk and topogrep puts it back on the shell surface -- the one that stands in for the real
	// command. See topogrep.echoRoot.
	if !strings.Contains(got, "Binary file ./blob.bin matches") {
		t.Errorf("the binary match was not reported at all:\n%s", got)
	}
	if status != 0 {
		t.Errorf("exit %d, want 0: the file matched", status)
	}
	// A count is still a count, and a file list still lists it.
	counts, _ := runAracGrep(t, root, "-rc", "swarmneedle", ".")
	if !strings.Contains(counts, "blob.bin:2") {
		t.Errorf("binary matches must still be counted:\n%s", counts)
	}
	files, _ := runAracGrep(t, root, "-rl", "swarmneedle", ".")
	if !strings.Contains(files, "blob.bin") {
		t.Errorf("binary matches must still list the file:\n%s", files)
	}
}

// --- DELIBERATE divergences, pinned ------------------------------------------

// aracne bounds every content result and says so. That IS fewer lines than grep printed, and
// it is the trade the tool exists to make -- but the trailer has to be actionable from the
// surface the caller is on. `head_limit` is an MCP tool parameter; a shell caller cannot type
// it, and telling them to raise it is advice they cannot follow.
func TestATruncatedSearchTellsTheShellCallerWhatToDo(t *testing.T) {
	root := grepFixture(t)

	got, _ := runAracGrep(t, root, "-rn", "marker", ".")
	if !strings.Contains(got, "not shown") {
		t.Fatalf("a 300-match search was not capped, or did not say so:\n%s",
			got[max(0, len(got)-400):])
	}
	trailer := got[strings.LastIndex(got, "\n…")+1:]
	if strings.Contains(trailer, "head_limit") && !strings.Contains(trailer, "-m") {
		t.Errorf("the truncation trailer offers `head_limit`, which no shell grep accepts; "+
			"the shell spelling is `-m N`:\n%s", trailer)
	}
}

// A dot-directory is not noise. `.github` is where an agent looks for CI configuration, and
// a search that silently returns nothing there reads as "this project has no CI" -- which is
// what a blanket "skip every name starting with a dot" produced. The heavy trees are named
// explicitly instead, and scan.ignore covers whatever a given project wants gone.
func TestDotDirectoriesAreSearched(t *testing.T) {
	root := grepFixture(t)

	for _, argv := range [][]string{
		{"-rn", "runs-on", "."},
		{"-rn", "runs-on", ".github"},
		{"-rn", "runs-on", ".github/workflows"},
	} {
		got, status := runAracGrep(t, root, argv...)
		if !strings.Contains(got, "runs-on") || status != 0 {
			t.Errorf("grep %s: exit %d, output:\n%s", strings.Join(argv, " "), status, got)
		}
	}
	// aracne's own store stays pruned: it holds a copy of every id in the project, so
	// searching it answers a question about the index rather than about the code.
	if got, _ := runAracGrep(t, root, "-rl", "topology", "."); strings.Contains(got, ".aracne") {
		t.Errorf("the search descended into .aracne:\n%s", got)
	}
}

// `grep foo a.go b.go` is what a shell glob produces, and it is ONE search: walking the
// operands together keeps one set of caps and one honest accounting, where a search per
// operand would report each cap against a fraction of the answer.
func TestSeveralFileOperandsAreOneSearch(t *testing.T) {
	root := grepFixture(t)

	want, _ := runGrep(t, root, "-Hn", "func", "pkg/shapes.go", "pkg/util.go")
	got, status := runAracGrep(t, root, "-n", "func", "pkg/shapes.go", "pkg/util.go")
	wantRows, _ := parseRows(want)
	have := keys(func() []row { m, _ := parseRows(got); return m }())
	for _, r := range wantRows {
		if _, ok := have[r.key()]; !ok {
			t.Errorf("two file operands lost %s:\n%s", r.key(), got)
		}
	}
	if status != 0 {
		t.Errorf("exit %d, want 0", status)
	}
	// A directory operand alongside a file works the same way, and a file reached through
	// both is reported once.
	dup, _ := runAracGrep(t, root, "-n", "func", "pkg", "pkg/util.go")
	dupRows, _ := parseRows(dup)
	seen := map[string]int{}
	for _, r := range dupRows {
		seen[r.key()]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("overlapping operands reported %s %d times:\n%s", k, n, dup)
		}
	}
}

// --exclude and --exclude-dir subtract, and they are how an agent narrows a sweep. A
// passthrough was safe and gave up the annotated search for the most common narrowing there
// is; answering them with the filter IGNORED would be far worse.
func TestExclusionsActuallyExclude(t *testing.T) {
	root := grepFixture(t)

	got, _ := runAracGrep(t, root, "-rn", "--exclude-dir=pkg", "func", ".")
	if strings.Contains(got, "pkg/") {
		t.Errorf("--exclude-dir=pkg did not exclude it:\n%s", got)
	}
	if !strings.Contains(got, "cmd/main.go") {
		t.Errorf("--exclude-dir=pkg excluded more than it was asked to:\n%s", got)
	}

	got, _ = runAracGrep(t, root, "-rn", "--exclude=*.md", "func", ".")
	if strings.Contains(got, "README.md") {
		t.Errorf("--exclude=*.md did not exclude it:\n%s", got)
	}
	if !strings.Contains(got, "pkg/util.go") {
		t.Errorf("--exclude=*.md excluded more than it was asked to:\n%s", got)
	}
}

// -x anchors the pattern to the whole line. Answering it unanchored returns every line that
// merely CONTAINS the pattern, which for a short pattern is most of the file.
func TestWholeLineIsAnchored(t *testing.T) {
	root := grepFixture(t)

	want, _ := runGrep(t, root, "-Hnx", "}", "pkg/util.go")
	got, _ := runAracGrep(t, root, "-nx", "}", "pkg/util.go")
	wantRows, _ := parseRows(want)
	gotRows, _ := parseRowsIn(got, "pkg/util.go")
	if len(gotRows) != len(wantRows) {
		t.Errorf("grep -nx '}' pkg/util.go: %d rows, the real grep found %d:\n%s",
			len(gotRows), len(wantRows), got)
	}
	for _, r := range gotRows {
		if strings.TrimSpace(r.text) != "}" {
			t.Errorf("-x returned a line that is not the whole pattern: %q", r.text)
		}
	}
}

// GNU grep has searched the working directory for `grep -r pat` with no operand since 2.11.
// Treating it as a read of stdin passed through the most common recursive spelling there is.
func TestRecursiveWithNoPathSearchesTheTree(t *testing.T) {
	root := grepFixture(t)

	got, status := runAracGrep(t, root, "-rn", "func")
	if status != 0 || !strings.Contains(got, "pkg/util.go") {
		t.Errorf("`grep -rn func` with no path: exit %d, output:\n%s", status, got)
	}
}

// Context is read in FILE order, with grep's own `--` between non-contiguous groups. The
// tiered ranking still decides which matches survive the head limit; it just does not decide
// the order they are read in, because a `-B2` window above an EARLIER match is a puzzle.
func TestContextRendersInFileOrder(t *testing.T) {
	root := grepFixture(t)

	got, _ := runAracGrep(t, root, "-rn", "-B", "2", "Total", "pkg/shapes.go")
	last := 0
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "# ") || ln == "--" || ln == "" {
			continue
		}
		rows, ctx := parseRowsIn(ln, "pkg/shapes.go")
		rows = append(rows, ctx...)
		if len(rows) != 1 {
			continue
		}
		if rows[0].line < last {
			t.Errorf("line %d came after line %d:\n%s", rows[0].line, last, got)
		}
		last = rows[0].line
	}
}

// Whatever else changes, `arac cmd` must never turn a search into an error the caller did not
// ask for, and must keep grep's exit vocabulary: 0 found, 1 not found, 2 could not look.
func TestExitStatusVocabularyIsPreserved(t *testing.T) {
	root := grepFixture(t)

	for _, tc := range []struct {
		argv []string
		want int
	}{
		{[]string{"-rn", "func", "."}, 0},
		{[]string{"-rn", "zzz-no-such-token", "."}, 1},
		{[]string{"-rn", "func", "no-such-directory"}, 2},
	} {
		if _, got := runAracGrep(t, root, tc.argv...); got != tc.want {
			t.Errorf("grep %s: exit %d, want %d", strings.Join(tc.argv, " "), got, tc.want)
		}
	}
}

// TestGrepHonoursExplicitFilenameAndLineNumberFlags pins the two flags aracne used to discard.
//
// `-H` was dropped, so a single named file came back as bare `line:text` where the real grep
// prints the path; `-n` was ignored in the other direction, so aracne INSERTED a line-number
// field into rows that never asked for one. Either moves the field a caller reads, and
// interception explicitly permits piping a search into a line filter.
func TestGrepHonoursExplicitFilenameAndLineNumberFlags(t *testing.T) {
	root := grepFixture(t)

	for _, argv := range [][]string{
		{"-H", "-n", "Total", "pkg/shapes.go"}, // path forced back onto a single file
		{"-H", "Total", "pkg/shapes.go"},       // path, no line numbers
		{"-n", "Total", "pkg/shapes.go"},       // bare, numbered
		{"Total", "pkg/shapes.go"},             // bare, unnumbered
		{"-r", "Total", "pkg"},                 // a tree, unnumbered
	} {
		want, _ := runGrep(t, root, argv...)
		got, _ := runAracGrep(t, root, argv...)

		// Every row the real grep printed must appear verbatim in aracne's answer. Aracne may
		// add its `# path:a-b` headers; it may not reshape a row.
		for _, line := range strings.Split(strings.TrimRight(want, "\n"), "\n") {
			if line == "" {
				continue
			}
			if !strings.Contains(got, line) {
				t.Errorf("grep %s: row %q is not in aracne's answer:\n%s",
					strings.Join(argv, " "), line, got)
			}
		}
	}
}

// runAracGrepPiped is runAracGrep the way the guard splices a search that feeds a pipeline:
// `--piped` asks for the rows the real grep would have printed and nothing else.
func runAracGrepPiped(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(AracBin, append([]string{"cmd", "--piped", "--", "grep"}, argv...)...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

// Overlapping context windows are ONE line-ordered stream, with `--` only between groups that do
// not touch -- which is what GNU grep prints. Each match's after-window used to be printed whole
// before the next match, so a match inside it came out below its own following lines:
// `31: 33- 34- 32: 35-` where grep prints `31: 32: 33- 34- 35-`. Under --piped a pipeline reads
// the rows, so the parity is byte for byte. The annotated rendering may add headers and node
// rows, but it must print every row the real grep printed, in file order, once.
func TestOverlappingContextWindowsRenderLikeGNUGrep(t *testing.T) {
	root := grepFixture(t)

	// `Shape` is on lines 8, 9, 24, 28 and 34 of pkg/shapes.go: two pairs whose windows overlap.
	for _, argv := range [][]string{
		{"-n", "-A3", "Shape", "pkg/shapes.go"},
		{"-n", "-B2", "Shape", "pkg/shapes.go"},
		{"-n", "-C2", "Shape", "pkg/shapes.go"},
		{"-A3", "Shape", "pkg/shapes.go"},
		{"-rn", "-A3", "Shape", "pkg"},
		// -m stops at N matches and prints the rest of the open window as CONTEXT, a matching
		// line included: grep prints line 9 as `9-`, and stops at 11.
		{"-n", "-m1", "-A3", "Shape", "pkg/shapes.go"},
	} {
		want, wantStatus := runGrep(t, root, argv...)
		got, gotStatus := runAracGrepPiped(t, root, argv...)
		if got != want || gotStatus != wantStatus {
			t.Errorf("grep %s --piped (exit %d):\n%s\nreal grep (exit %d):\n%s",
				strings.Join(argv, " "), gotStatus, got, wantStatus, want)
		}

		annotated, _ := runAracGrep(t, root, argv...)
		implied := singleFileOperand(root, argv)
		have := map[string]bool{}
		last := map[string]int{}
		for _, ln := range strings.Split(annotated, "\n") {
			matches, context := parseRowsIn(ln, implied)
			for _, r := range append(matches, context...) {
				if r.line <= last[r.path] {
					t.Errorf("grep %s: %s printed after line %d:\n%s",
						strings.Join(argv, " "), r.key(), last[r.path], annotated)
				}
				last[r.path] = r.line
				have[r.key()] = true
			}
		}
		wantMatches, wantContext := parseRowsIn(want, implied)
		for _, r := range append(wantMatches, wantContext...) {
			if !have[r.key()] {
				t.Errorf("grep %s: the real grep's row %s is missing:\n%s",
					strings.Join(argv, " "), r.key(), annotated)
			}
		}
	}
}

// A path-less rg searches its STDIN when stdin is a file or a pipe, and walks the working
// directory only when it is a terminal or /dev/null. `arac cmd` holds the stdin the real command
// would have had, so it hands the first two to the real binary: `rg retry < README.md` used to
// come back as a search of the whole tree, matches from five files for a question about one.
// /dev/null is what an agent's Bash tool hands a command, so that one is still answered.
func TestPathlessRipgrepReadingStdinRunsTheRealBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in rg is a shell script")
	}
	root := grepFixture(t)
	// A stand-in rg that only says it ran, so the suite needs no ripgrep and a passthrough is
	// unmistakable. It is needed for the answered case too: `arac cmd` serves a command only
	// when its binary is on PATH, and asks it which files it would search (`rg --files`), which
	// the stand-in answers with the fixture's non-hidden files.
	shims := t.TempDir()
	rg := filepath.Join(shims, "rg")
	writeFile(t, rg, "#!/bin/sh\nif [ \"$1\" = --files ]; then\n"+
		"  find . -type f ! -path './.*' | sed 's|^\\./||' | tr '\\n' '\\000'; exit 0\nfi\n"+
		"echo \"REAL rg $*\"\n")
	if err := os.Chmod(rg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := func(stdin io.Reader) string {
		t.Helper()
		cmd := exec.Command(AracBin, "cmd", "--", "rg", "Describe")
		cmd.Dir, cmd.Env, cmd.Stdin = root, env, stdin
		out, _ := cmd.Output()
		return string(out)
	}

	readme, err := os.Open(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer readme.Close()
	if got := run(readme); !strings.HasPrefix(got, "REAL rg") {
		t.Errorf("`rg Describe < README.md` was answered instead of run:\n%s", got)
	}
	if got := run(strings.NewReader("Describe\n")); !strings.HasPrefix(got, "REAL rg") {
		t.Errorf("`… | rg Describe` was answered instead of run:\n%s", got)
	}
	if got := run(nil); strings.Contains(got, "REAL rg") || !strings.Contains(got, "pkg/shapes.go") {
		t.Errorf("`rg Describe` with /dev/null on stdin must still be answered from the tree:\n%s", got)
	}
}

// --- PIPED: the rows the real command prints, and nothing else -----------------

// shellParityFixture is a scanned project shaped for the places a piped search used to print
// rows the real grep does not: three files for -c, a symbolic link into a directory, a binary, a
// CRLF file and a Latin-1 one, and a test file for the order of --include and --exclude.
func shellParityFixture(t *testing.T) string {
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
	write("cnt/a.go", "package cnt\n\n// Area is one.\nfunc A() {}\n")
	write("cnt/b.go", "package cnt\n\nfunc B() {}\n")
	write("cnt/c.go", "package cnt\n\nfunc C() {}\n")
	write("cnt/a_test.go", "package cnt\n\nfunc ATest() {}\n")
	write("README.md", "# demo\n\nfunc docs\n")
	write("tree/real/r.txt", "retry in real\n")
	write("elsewhere/e.txt", "retry elsewhere\n")
	write("crlf.txt", "retry\r\nnope\r\nretry again\r\n")
	write("latin1.txt", "caf\xe9 retry\nplain retry\n")
	if err := os.WriteFile(filepath.Join(root, "tree", "blob.bin"), []byte("retry\x00binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for link, target := range map[string]string{"linkdir": "tree/real", "tree/lnk": "../elsewhere"} {
			if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
				t.Fatal(err)
			}
		}
	}
	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return root
}

// Every search below is one the guard splices `--piped` into when a pipeline reads it, and that
// promise is the real grep's rows: its stdout byte for byte, and its exit status. The one
// deliberate difference is the order WITHIN a walked tree -- grep walks in readdir order, which
// no filesystem defines, and aracne lexically -- so a search that walks a directory is compared
// as a set of rows. Everything named on the command line is answered in the order given.
func TestPipedSearchPrintsTheRowsTheRealGrepPrints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture needs symbolic links")
	}
	root := shellParityFixture(t)
	t.Setenv("LC_ALL", "C.UTF-8")

	for _, tc := range []struct {
		argv []string
		tree bool // walks a directory: compare the rows as a set
	}{
		// SH-4: a path-less `grep -r` prints no `./`, and each root echoes its own spelling.
		{[]string{"-rn", "Area"}, true},
		{[]string{"-rl", "retry", "./tree", "elsewhere"}, false},
		// GR-4: `path:0` for every file searched, the pipeline behind `| grep ':0$'`.
		{[]string{"-c", "Area", "cnt/a.go", "cnt/b.go", "cnt/c.go"}, false},
		{[]string{"-c", "zzz", "cnt/a.go", "cnt/b.go"}, false},
		{[]string{"-rc", "Area", "cnt"}, true},
		// ... and a binary file's count is of the runs between newlines AND NULs, as grep counts.
		{[]string{"-c", "^binary", "tree/blob.bin"}, false},
		// GR-12: -cH, operand order, the operand's own spelling, a repeated operand.
		{[]string{"-cH", "Area", "cnt/a.go"}, false},
		{[]string{"-n", "func", "cnt/c.go", "cnt/a.go"}, false},
		{[]string{"-rn", "Area", "cnt/../cnt/a.go", "tree/"}, false},
		{[]string{"-n", "func", "cnt/a.go", "cnt/a.go"}, false},
		// GR-6: a linked directory named as an operand is followed; -R follows links in the walk.
		{[]string{"-rl", "retry", "linkdir"}, false},
		{[]string{"-rl", "retry", "tree"}, true},
		{[]string{"-Rl", "retry", "tree"}, true},
		// GR-13: the last matching filter wins, and operands are held to the filters.
		{[]string{"-rl", "--exclude=*_test.go", "--include=*.go", "func", "."}, true},
		{[]string{"-rl", "--include=*.go", "--exclude=*_test.go", "func", "cnt"}, true},
		{[]string{"-l", "--exclude=*_test.go", "func", "cnt/a_test.go", "cnt/a.go"}, false},
		{[]string{"-l", "--include=*.md", "func", "cnt/a.go"}, false},
		{[]string{"-rl", "--exclude-dir=cnt", "func", "./cnt"}, false},
		{[]string{"-rl", "--binary-files=without-match", "retry", "tree"}, true},
		// GR-14: CR is content to grep, and a Latin-1 row is suppressed in a UTF-8 locale.
		{[]string{"-n", "retry$", "crlf.txt"}, false},
		{[]string{"-n", "retry", "crlf.txt"}, false},
		{[]string{"-n", "retry", "latin1.txt"}, false},
		{[]string{"-c", "caf.", "latin1.txt"}, false},
	} {
		want, wantStatus := runGrep(t, root, tc.argv...)
		got, gotStatus := runAracGrepPiped(t, root, tc.argv...)
		if tc.tree {
			want, got = sortedRows(want), sortedRows(got)
		}
		if got != want || gotStatus != wantStatus {
			t.Errorf("grep %s --piped (exit %d):\n%q\nreal grep (exit %d):\n%q",
				strings.Join(tc.argv, " "), gotStatus, got, wantStatus, want)
		}
	}
}

func sortedRows(out string) string {
	rows := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

// --- rg: ripgrep's own file set ------------------------------------------------

// rgFixture is a project whose files ripgrep and a `grep -r` disagree about: an ignored bundle,
// a hidden directory, a binary, and the shapes rg's glob dialect has and fnmatch lacks.
func rgFixture(t *testing.T) string {
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
	write("pkg/a.go", "package pkg\n\n// Retry once.\nfunc Retry() {}\n")
	write("pkg/a_test.go", "package pkg\n\nfunc TestRetry() {}\n")
	write("cfg/a.json", "{\"retry\": 1}\n")
	write("cfg/b.yaml", "retry: 1\n")
	write("cfg/c.toml", "retry = 1\n")
	write("deep/x/y/Z.java", "class Z { int retry; }\n")
	write("dist/bundle.js", "retry()\n")
	write(".github/ci.yml", "retry: true\n")
	// .ignore, which ripgrep honours without a git repository.
	write(".ignore", "dist/\n")
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte("retry\x00binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return root
}

// GR-8: rg is served from the list of files rg itself would search -- its ignore files, hidden
// and binary rules, and its glob dialect -- so a piped rg prints the real rg's rows. Needs a real
// ripgrep on PATH; rg prints files in no fixed order, so the rows are compared as a set.
func TestPipedRipgrepPrintsTheRowsTheRealRipgrepPrints(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep is not installed")
	}
	root := rgFixture(t)

	for _, argv := range [][]string{
		{"-l", "retry"},
		{"-n", "retry", "."},
		{"-c", "retry"},
		{"-l", "-g", "*.{json,yaml}", "retry"},
		{"-l", "-g", "deep/**/*.java", "retry", "."},
		{"-l", "-g", "!cfg/", "retry"},
		{"-l", "-g", "*.go", "-g", "!*_test.go", "Retry"},
		{"-l", "-g", "!*_test.go", "-g", "*.go", "Retry"},
		{"-l", "-t", "go", "Retry"},
		{"-n", "retry", "cfg/"},
		{"-l", "retry", ".github"},
	} {
		real := exec.Command(rg, argv...)
		real.Dir = root
		wantOut, _ := real.Output()
		wantStatus := real.ProcessState.ExitCode()

		cmd := exec.Command(AracBin, append([]string{"cmd", "--piped", "--", "rg"}, argv...)...)
		cmd.Dir = root
		gotOut, _ := cmd.Output()
		gotStatus := cmd.ProcessState.ExitCode()

		want, got := sortedRows(string(wantOut)), sortedRows(string(gotOut))
		if got != want || gotStatus != wantStatus {
			t.Errorf("rg %s --piped (exit %d):\n%s\nreal rg (exit %d):\n%s",
				strings.Join(argv, " "), gotStatus, got, wantStatus, want)
		}
	}
	// And it was ANSWERED, not handed back: the annotated form carries a resource header.
	cmd := exec.Command(AracBin, "cmd", "--", "rg", "-n", "Retry", "pkg")
	cmd.Dir = root
	if out, _ := cmd.Output(); !strings.Contains(string(out), "# demo/pkg.Retry") {
		t.Errorf("`rg -n Retry pkg` was not served from the topology:\n%s", out)
	}
}

// GR-8, without a real ripgrep: the search asks `rg --files` which files to visit -- with the
// caller's globs in the order typed and its type -- and visits only those, spelled as rg spelled
// them. A stand-in rg answers the listing and says so for anything else, so a passthrough is
// unmistakable.
func TestRipgrepIsServedFromRipgrepsOwnFileList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in rg is a shell script")
	}
	root := rgFixture(t)
	shims := t.TempDir()
	logPath := filepath.Join(shims, "args.log")
	writeFile(t, filepath.Join(shims, "rg"), "#!/bin/sh\nif [ \"$1\" = --files ]; then\n"+
		"  echo \"$*\" >> '"+logPath+"'\n"+
		"  printf './pkg/a.go\\000./cfg/a.json\\000./blob.bin\\000'; exit 0\nfi\n"+
		"echo \"REAL rg $*\"\n")
	if err := os.Chmod(filepath.Join(shims, "rg"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(argv ...string) string {
		t.Helper()
		cmd := exec.Command(AracBin, append([]string{"cmd", "--", "rg"}, argv...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, _ := cmd.Output()
		return string(out)
	}

	got := run("-l", "-g", "*.go", "-g", "!*_test.go", "-t", "go", "retry", ".")
	if strings.Contains(got, "REAL rg") {
		t.Fatalf("a plain rg search was passed through:\n%s", got)
	}
	// Only listed files, under rg's spelling; the binary one rg met in a walk it skips.
	for _, ln := range strings.Split(strings.TrimSpace(got), "\n") {
		if ln != "./cfg/a.json" && ln != "./pkg/a.go" {
			t.Errorf("row %q is not a file rg listed (or is the binary it skips):\n%s", ln, got)
		}
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "--files --null --glob=*.go --glob=!*_test.go --type=go -- ."; strings.TrimSpace(string(logged)) != want {
		t.Errorf("rg was asked %q, want %q", strings.TrimSpace(string(logged)), want)
	}
	// rg's message for a binary file it was NAMED is its own; that runs for real.
	if got := run("retry", "blob.bin"); !strings.HasPrefix(got, "REAL rg") {
		t.Errorf("`rg retry blob.bin` was answered instead of run:\n%s", got)
	}
}

// SH-2 (word tests): in a UTF-8 locale grep reads `é` as a word character, which Go's `\b` and
// `\w` do not. Every form is compared with the real grep, piped (byte for byte) and annotated (the
// same rows, headers aside): the ones where the word test lands beside a non-ASCII letter run for
// real, and the ASCII controls stay answered.
func TestWordTestsAgreeWithGrepBesideNonASCIILetters(t *testing.T) {
	root := grepFixture(t)
	t.Setenv("LC_ALL", "C.UTF-8")
	writeFile(t, filepath.Join(root, "words.txt"),
		"// éRadius here\n// Radiusé there\n// xRadius ascii\n// Radius alone\n// café\n// naïve\n")
	rows := func(out string) string {
		var kept []string
		for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if !strings.HasPrefix(ln, "# ") {
				kept = append(kept, ln)
			}
		}
		return strings.Join(kept, "\n")
	}

	for _, argv := range [][]string{
		{"-nw", "Radius", "words.txt"},
		{"-cw", "Radius", "words.txt"},
		{"-n", `\bRadius`, "words.txt"},
		{"-n", `Radius\>`, "words.txt"},
		{"-nE", `caf\w`, "words.txt"},
		{"-nE", `na\W`, "words.txt"},
		{"-nw", "alone", "words.txt"},
		{"-nw", "sum", "pkg/shapes.go"},
	} {
		want, wantStatus := runGrep(t, root, argv...)
		piped, pipedStatus := runAracGrepPiped(t, root, argv...)
		if piped != want || pipedStatus != wantStatus {
			t.Errorf("grep %s --piped (exit %d):\n%q\nreal grep (exit %d):\n%q",
				strings.Join(argv, " "), pipedStatus, piped, wantStatus, want)
		}
		annotated, status := runAracGrep(t, root, argv...)
		if rows(annotated) != rows(want) || status != wantStatus {
			t.Errorf("grep %s (exit %d):\n%s\nreal grep (exit %d):\n%s",
				strings.Join(argv, " "), status, annotated, wantStatus, want)
		}
	}
}

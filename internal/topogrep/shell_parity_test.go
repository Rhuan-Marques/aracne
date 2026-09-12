package topogrep

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The shell surface (Terse) stands in for the real grep and rg, and under Plain it promises the
// rows they would print. These tests pin each place the rendering or the walk used to differ,
// and next to each, the deliberate behaviour the fix must not reach.

// shellTree writes files under a fresh directory and makes it the working directory, which is
// where the shell surface's operands are relative to.
func shellTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	t.Chdir(dir)
	return dir
}

func render(t *testing.T, opt Options, topo *domain.Topology) (string, *Result) {
	t.Helper()
	res, err := SearchWith(opt, topo)
	if err != nil {
		t.Fatalf("SearchWith(%+v): %v", opt, err)
	}
	return FormatResult(res, opt), res
}

// SH-4 / GR-12: each root keeps its own spelling. `grep -rl x ./a b` prints `./a/…` and `b/…`;
// one `./` operand used to put a `./` on every row. `grep -r x` with no operand prints no `./`
// at all, a directory operand's trailing slashes are dropped as grep drops them, and an
// uncleaned operand is echoed as typed.
func TestRowsSpellEachRootTheWayItWasTyped(t *testing.T) {
	shellTree(t, map[string]string{"a/x.go": "needle\n", "b/y.go": "needle\n"})

	for _, tc := range []struct {
		roots []string
		want  string
	}{
		{nil, "a/x.go\nb/y.go"},
		{[]string{"."}, "./a/x.go\n./b/y.go"},
		{[]string{"./a", "b"}, "./a/x.go\nb/y.go"},
		{[]string{"a/", "b//"}, "a/x.go\nb/y.go"},
		{[]string{"a/../a"}, "a/../a/x.go"},
		{[]string{"b/../b/y.go", "a/x.go"}, "b/../b/y.go\na/x.go"},
	} {
		opt := Options{Pattern: "needle", Roots: tc.roots, Mode: OutputFiles, Terse: true, Plain: true}
		if got, _ := render(t, opt, nil); got != tc.want {
			t.Errorf("roots %q: rows\n%s\nwant\n%s", tc.roots, got, tc.want)
		}
	}

	// Surfaces that imitate no command keep the cleaned relative path.
	opt := Options{Pattern: "needle", Roots: []string{"./a"}, Mode: OutputFiles}
	if got, _ := render(t, opt, nil); got != "a/x.go" {
		t.Errorf("the MCP/`arac grep` spelling changed: %q", got)
	}
}

// GR-12: several operands are answered in the order they were given, as grep answers them.
// Within one tree the walk stays lexical (grep's readdir order is not defined).
func TestOperandsAreAnsweredInTheOrderGiven(t *testing.T) {
	shellTree(t, map[string]string{"a.go": "needle a\n", "b.go": "needle b\n"})

	opt := Options{Pattern: "needle", Roots: []string{"b.go", "a.go"}, Terse: true, Plain: true}
	if got, _ := render(t, opt, nil); got != "b.go:needle b\na.go:needle a" {
		t.Errorf("rows not in operand order:\n%s", got)
	}
	opt.Mode = OutputCount
	if got, _ := render(t, opt, nil); got != "b.go:1\na.go:1" {
		t.Errorf("counts not in operand order:\n%s", got)
	}
}

// GR-12: a file named twice is searched twice by the real command, so under Plain -- the real
// command's rows -- the search gives up and lets it answer. The annotated answer keeps its
// documented choice of reporting the file once.
func TestADuplicateOperandIsUnmodelledOnlyUnderPlain(t *testing.T) {
	shellTree(t, map[string]string{"a.go": "needle\n"})

	opt := Options{Pattern: "needle", Roots: []string{"a.go", "./a.go"}, Terse: true, Plain: true}
	if _, err := SearchWith(opt, nil); !errors.Is(err, ErrUnmodelled) {
		t.Errorf("a repeated operand under Plain: err = %v, want ErrUnmodelled", err)
	}
	opt.Plain = false
	got, _ := render(t, opt, nil)
	if strings.Count(got, "needle") != 1 {
		t.Errorf("the annotated answer must still report the file once:\n%s", got)
	}
}

// GR-12 and GR-4: `-cH` names the one file it counted, and grep prints `path:0` for every
// file it searched. The zero rows are printed under Plain; the annotated answer leaves them
// out as noise, and a tool that prints none (rg) gets none.
func TestCountsPrintWhatTheRealCommandPrints(t *testing.T) {
	shellTree(t, map[string]string{"a.go": "Area\n", "b.go": "none\n", "c.go": "none\n"})
	files := []string{"a.go", "b.go", "c.go"}

	count := func(roots []string, mutate func(*Options)) string {
		opt := Options{Pattern: "Area", Roots: roots, Mode: OutputCount, Terse: true, CountZeros: true}
		mutate(&opt)
		got, _ := render(t, opt, nil)
		return got
	}
	for _, tc := range []struct {
		name   string
		roots  []string
		mutate func(*Options)
		want   string
	}{
		{"-cH one file", []string{"a.go"}, func(o *Options) { o.WithFilename = true }, "a.go:1"},
		{"-cH one file, no match", []string{"b.go"}, func(o *Options) { o.WithFilename = true }, "b.go:0"},
		{"-c one file, no match", []string{"b.go"}, func(o *Options) {}, "0"},
		{"piped -c files", files, func(o *Options) { o.Plain = true }, "a.go:1\nb.go:0\nc.go:0"},
		{"piped -c tree", nil, func(o *Options) { o.Plain = true }, "a.go:1\nb.go:0\nc.go:0"},
		{"piped -c, no match anywhere", files, func(o *Options) { o.Plain, o.Pattern = true, "zzz" },
			"a.go:0\nb.go:0\nc.go:0"},
		// Deliberate: the annotated answer to a count over several files lists the hits only.
		{"annotated -c files", files, func(o *Options) {}, "a.go:1"},
		// rg prints no zero rows, and nothing at all for one file with no match.
		{"rg piped -c files", files, func(o *Options) { o.Plain, o.CountZeros = true, false }, "a.go:1"},
		{"rg -c one file, no match", []string{"b.go"}, func(o *Options) { o.CountZeros = false }, ""},
	} {
		if got := count(tc.roots, tc.mutate); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// GR-6: a symbolic link named as a root is followed -- grep -r and rg both follow a link on the
// command line -- and its rows keep the link's name. A link met during the walk is skipped
// without FollowLinks (-r) and followed with it (-R).
func TestSymbolicLinksAreFollowedTheWayGrepFollowsThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dir := shellTree(t, map[string]string{"real/r.txt": "needle\n", "top.txt": "needle\n"})
	for link, target := range map[string]string{"linkdir": "real", "real/sub": "../top.txt"} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}

	list := func(opt Options) string {
		opt.Pattern, opt.Mode, opt.Terse, opt.Plain = "needle", OutputFiles, true, true
		got, _ := render(t, opt, nil)
		return got
	}
	if got := list(Options{Roots: []string{"linkdir"}}); got != "linkdir/r.txt" {
		t.Errorf("grep -rl needle linkdir: %q, want linkdir/r.txt", got)
	}
	if got := list(Options{Roots: []string{"real"}}); got != "real/r.txt" {
		t.Errorf("-r followed a link met in the walk: %q", got)
	}
	if got := list(Options{Roots: []string{"real"}, FollowLinks: true}); got != "real/r.txt\nreal/sub" {
		t.Errorf("-R did not follow the link: %q", got)
	}
	if got := list(Options{Roots: []string{"."}, FollowLinks: true}); got != "./linkdir/r.txt\n./linkdir/sub\n./real/r.txt\n./real/sub\n./top.txt" {
		t.Errorf("-R over the tree: %q", got)
	}
}

// GR-6: following links can loop, which grep -R reports in a warning, and a dangling link is an
// error that sets its exit status. Neither is modelled; the real command answers.
func TestLinkLoopsAndDanglingLinksAreLeftToTheRealGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dir := shellTree(t, map[string]string{"a/x.txt": "needle\n"})
	if err := os.Symlink("..", filepath.Join(dir, "a", "up")); err != nil {
		t.Fatal(err)
	}
	opt := Options{Pattern: "needle", Mode: OutputFiles, Terse: true, FollowLinks: true}
	if _, err := SearchWith(opt, nil); !errors.Is(err, ErrUnmodelled) {
		t.Errorf("a -R loop: err = %v, want ErrUnmodelled", err)
	}
	// Without -R the link is never followed, so there is no loop to meet.
	opt.FollowLinks = false
	if got, _ := render(t, opt, nil); got != "a/x.txt" {
		t.Errorf("-r must skip the looping link: %q", got)
	}

	if err := os.Remove(filepath.Join(dir, "a", "up")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "a", "dl")); err != nil {
		t.Fatal(err)
	}
	opt.FollowLinks = true
	if _, err := SearchWith(opt, nil); !errors.Is(err, ErrUnmodelled) {
		t.Errorf("a dangling link under -R: err = %v, want ErrUnmodelled", err)
	}
}

// GR-13: GNU grep's --include/--exclude rule. The last filter that matches decides; a file none
// matches is searched unless the first filter was an --include. Operands are held to the
// filters too, by name suffix, and a directory operand to --exclude-dir.
func TestFilenameFiltersFollowGNUGrep(t *testing.T) {
	shellTree(t, map[string]string{
		"cnt/a.go": "func\n", "cnt/a_test.go": "func\n", "cnt/README.md": "func\n",
	})
	list := func(roots []string, filters []NameFilter, excludeDirs ...string) string {
		opt := Options{Pattern: "func", Roots: roots, Mode: OutputFiles, Terse: true, Plain: true,
			GrepFilters: true, NameFilters: filters, ExcludeDirs: excludeDirs}
		got, _ := render(t, opt, nil)
		return got
	}
	inc := func(g string) NameFilter { return NameFilter{Glob: g} }
	exc := func(g string) NameFilter { return NameFilter{Glob: g, Exclude: true} }
	tree := []string{"cnt"}

	for _, tc := range []struct {
		name    string
		roots   []string
		filters []NameFilter
		dirs    []string
		want    string
	}{
		{"--exclude then --include", tree, []NameFilter{exc("*_test.go"), inc("*.go")}, nil,
			"cnt/README.md\ncnt/a.go\ncnt/a_test.go"},
		{"--include then --exclude", tree, []NameFilter{inc("*.go"), exc("*_test.go")}, nil, "cnt/a.go"},
		{"--include alone", tree, []NameFilter{inc("*.md")}, nil, "cnt/README.md"},
		{"a walked file is matched by base name", tree, []NameFilter{exc("cnt/a.go")}, nil,
			"cnt/README.md\ncnt/a.go\ncnt/a_test.go"},
		{"--exclude on a named file", []string{"cnt/a_test.go", "cnt/a.go"}, []NameFilter{exc("*_test.go")}, nil,
			"cnt/a.go"},
		{"--include on a named file", []string{"cnt/a.go"}, []NameFilter{inc("*.md")}, nil, ""},
		{"a named file is matched by suffix after a slash", []string{"cnt/a.go"}, []NameFilter{exc("nt/a.go")}, nil,
			"cnt/a.go"},
		{"--exclude-dir on a named directory", []string{"./cnt"}, nil, []string{"cnt"}, ""},
		{"a trailing slash is part of the name", []string{"cnt/"}, nil, []string{"cnt"},
			"cnt/README.md\ncnt/a.go\ncnt/a_test.go"},
	} {
		if got := list(tc.roots, tc.filters, tc.dirs...); got != tc.want {
			t.Errorf("%s: got\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}

	// A bare `grep -r` walks `.`, and grep never skips that for --exclude-dir.
	opt := Options{Pattern: "func", Mode: OutputFiles, Terse: true, GrepFilters: true, ExcludeDirs: []string{"."}}
	if got, _ := render(t, opt, nil); got == "" {
		t.Error("--exclude-dir=. skipped the implicit working directory")
	}
	// The other surfaces keep their own rule: a named file is searched whatever the filters say.
	opt = Options{Pattern: "func", Roots: []string{"cnt/a_test.go"}, Mode: OutputFiles,
		ExcludeGlobs: []string{"*_test.go"}}
	if got, _ := render(t, opt, nil); got != "cnt/a_test.go" {
		t.Errorf("`arac grep` must still search a file it was named: %q", got)
	}
}

// GR-14: under Plain a CRLF line is matched and printed as it is, CR included -- grep and rg
// both keep it. The annotated answer keeps dropping it (TestATrailingCarriageReturnIsNotContent).
func TestPlainKeepsTheCarriageReturn(t *testing.T) {
	shellTree(t, map[string]string{"crlf.txt": "needle\r\nplain\r\nneedle again\r\n"})

	opt := Options{Pattern: "needle$", Roots: []string{"crlf.txt"}, Terse: true, Plain: true}
	if got, res := render(t, opt, nil); got != "" || res.Found(OutputContent) {
		t.Errorf("`needle$` matched a CRLF line under Plain: %q", got)
	}
	opt.Pattern, opt.LineNumbers = "needle", true
	if got, _ := render(t, opt, nil); got != "1:needle\r\n3:needle again\r" {
		t.Errorf("rows = %q, want the CRs kept", got)
	}
}

// GR-14: bytes whose real output this package cannot reproduce under Plain. A line that is not
// UTF-8 is suppressed by grep in a UTF-8 locale; a NUL past the binary sniff makes grep stop
// printing where its own buffer ends. Either, in a file that matches, is left to the real
// command. A clean file beside them is unaffected, and the annotated answer still serves them.
func TestPlainLeavesMisEncodedAndLateBinaryFilesToTheRealGrep(t *testing.T) {
	late := "needle first\n" + strings.Repeat(strings.Repeat("y", 99)+"\n", 150) + "\x00\nneedle after\n"
	shellTree(t, map[string]string{
		"latin1.txt": "caf\xe9 needle\nplain needle\n",
		"late.txt":   late,
		"clean.txt":  "needle\n",
	})
	for _, file := range []string{"latin1.txt", "late.txt"} {
		opt := Options{Pattern: "needle", Roots: []string{file}, Terse: true, Plain: true}
		if _, err := SearchWith(opt, nil); !errors.Is(err, ErrUnmodelled) {
			t.Errorf("%s under Plain: err = %v, want ErrUnmodelled", file, err)
		}
		opt.Plain = false
		if _, err := SearchWith(opt, nil); err != nil {
			t.Errorf("%s annotated: %v", file, err)
		}
		// A file with no match prints nothing either way, so there is nothing to give up on.
		opt.Plain, opt.Pattern = true, "zzz"
		if _, err := SearchWith(opt, nil); err != nil {
			t.Errorf("%s under Plain with no match: %v", file, err)
		}
	}
	opt := Options{Pattern: "needle", Roots: []string{"clean.txt"}, Terse: true, Plain: true}
	if got, _ := render(t, opt, nil); got != "needle" {
		t.Errorf("clean.txt: %q", got)
	}
}

// GR-8: with Only set the search visits exactly the listed files -- ripgrep's own list, which
// alone knows its ignore, hidden and glob rules -- and prints each under the listed spelling. A
// binary file rg meets in a walk is skipped, as rg skips it; one it was named is left to rg,
// whose message for it grep does not print.
func TestOnlyTheListedFilesAreSearched(t *testing.T) {
	shellTree(t, map[string]string{
		"src/a.go":          "needle\n",
		"src/b.go":          "needle\n",
		"dist/bundle.js":    "needle\n",
		".github/ci.yml":    "needle\n",
		"node_modules/m.js": "needle\n",
	})
	if err := os.WriteFile("blob.bin", []byte("needle\x00binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	listed := []string{"./src/a.go", "./blob.bin", "./node_modules/m.js"}
	opt := Options{Pattern: "needle", Roots: []string{"."}, Mode: OutputFiles, Terse: true, Plain: true, Only: listed}
	if got, _ := render(t, opt, nil); got != "./node_modules/m.js\n./src/a.go" {
		t.Errorf("rows = %q, want only the listed text files", got)
	}
	// The annotated answer searches exactly rg's list too. node_modules is on it and is not
	// ignored in this tree, so it is searched rather than pruned by a name -- the hardcoded
	// list that used to prune it is gone, and nothing is left to explain in a trailing note.
	opt.Plain = false
	got, _ := render(t, opt, nil)
	if got == "" || strings.Contains(got, "dist") || !strings.Contains(got, "node_modules/m.js") {
		t.Errorf("annotated rg answer:\n%s", got)
	}
	if strings.Contains(got, "skipped") {
		t.Errorf("a search names no pruned tree any more:\n%s", got)
	}

	named := Options{Pattern: "needle", Roots: []string{"blob.bin"}, Terse: true, Only: []string{"blob.bin"}}
	if _, err := SearchWith(named, nil); !errors.Is(err, ErrUnmodelled) {
		t.Errorf("a named binary file under rg: err = %v, want ErrUnmodelled", err)
	}
}

// GR-9: a row printed bare -- annotation off, or a line outside every declaration -- used to sit
// under whatever resource header came last, attributing it to a declaration it is not in. Once
// a header has been printed, such a row gets its file's header, the one a line outside every
// declaration gets.
func TestABareRowIsNotPrintedUnderTheResourceAbove(t *testing.T) {
	dir := shellTree(t, map[string]string{
		"shape.go": "package shapes\n\n// Circle is round.\ntype Circle struct {\n\tRadius float64\n}\n",
	})
	topo := nodeTopo(domain.Resource{
		ID: "shapes.Circle", Name: "Circle", Kind: domain.ResourceStruct,
		Description: "Circle is round.",
		Location:    domain.Location{Path: filepath.Join(dir, "shape.go"), StartsAt: 4, EndsAt: 6},
	})
	got, _ := render(t, Options{Pattern: "round", Roots: []string{"shape.go"}, Terse: true, LineNumbers: true}, topo)
	want := "# shapes.Circle — Circle is round.\n4:type Circle struct {\n# shape.go\n3:// Circle is round."
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// GR-9: a header is printed only with a row under it. Two nodes declared on one line both match
// on their description; the second one's row is the line already printed, and its header used
// to stand alone.
func TestAHeaderIsNeverPrintedWithNothingUnderIt(t *testing.T) {
	dir := shellTree(t, map[string]string{"v.go": "package v\n\nvar A, B = 1, 2\n"})
	path := filepath.Join(dir, "v.go")
	topo := nodeTopo(
		domain.Resource{ID: "v.A", Name: "A", Kind: domain.ResourceFunction, Description: "pair value",
			Location: domain.Location{Path: path, StartsAt: 3, EndsAt: 3}},
		domain.Resource{ID: "v.B", Name: "B", Kind: domain.ResourceFunction, Description: "pair value",
			Location: domain.Location{Path: path, StartsAt: 3, EndsAt: 3}},
	)
	got, _ := render(t, Options{Pattern: "pair", Roots: []string{"v.go"}, Terse: true, LineNumbers: true}, topo)
	lines := strings.Split(got, "\n")
	if last := lines[len(lines)-1]; strings.HasPrefix(last, "# ") {
		t.Errorf("a header ends the answer with nothing under it:\n%s", got)
	}
	if strings.Count(got, "3:var A, B = 1, 2") != 1 {
		t.Errorf("the shared line must be printed once:\n%s", got)
	}
}

// SH-2 (word tests): grep in a UTF-8 locale and rg read `é` as a word character, Go's `\b` and
// `\w` do not -- so `grep -w Radius` skips `éRadius` where `\bRadius\b` matches it, and `caf\w`
// matches `café` where Go's does not. A word test that lands beside a non-ASCII byte is left to
// the real command; ASCII lines, a non-ASCII byte away from the match, and a pattern with no word
// test are answered as before.
func TestAWordTestBesideANonASCIILetterIsLeftToTheRealTool(t *testing.T) {
	shellTree(t, map[string]string{
		"pre.txt":   "// éRadius\n",
		"post.txt":  "// Radiusé\n",
		"cafe.txt":  "// café\n",
		"far.txt":   "// é is far from Radius here\n",
		"ascii.txt": "// xRadius and Radius\n",
	})
	search := func(pattern, file string) error {
		_, err := SearchWith(Options{Pattern: pattern, Roots: []string{file}, Terse: true, UnicodeWords: true}, nil)
		return err
	}
	for _, tc := range []struct {
		pattern, file string
		unmodelled    bool
	}{
		{`\b(?:Radius)\b`, "pre.txt", true}, // -w
		{`\b(?:Radius)\b`, "post.txt", true},
		{`\bRadius`, "pre.txt", true},
		{`caf\w`, "cafe.txt", true}, // Go finds nothing, grep finds the line
		{`[\w]+`, "cafe.txt", true}, // a class: anywhere on a non-ASCII line
		{`\b(?:Radius)\b`, "far.txt", false},
		{`\b(?:Radius)\b`, "ascii.txt", false},
		{`Radius`, "pre.txt", false}, // no word test at all
	} {
		err := search(tc.pattern, tc.file)
		if got := errors.Is(err, ErrUnmodelled); got != tc.unmodelled || (err != nil && !got) {
			t.Errorf("%s in %s: err = %v, want unmodelled=%v", tc.pattern, tc.file, err, tc.unmodelled)
		}
	}
	// Only the shell surface defers; the other surfaces have no real command to defer to.
	if _, err := SearchWith(Options{Pattern: `\b(?:Radius)\b`, Roots: []string{"pre.txt"}}, nil); err != nil {
		t.Errorf("without UnicodeWords: %v", err)
	}
}

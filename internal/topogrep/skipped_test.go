package topogrep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skippedFixture is a tree with a match in source, in two dependency trees and in aracne's store.
func skippedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"src/a.go":             "package a\n\nconst x = \"needle\"\n",
		"node_modules/dep.js":  "const needle = 1;\n",
		"vendor/lib/v.go":      "package lib // needle\n",
		".aracne/config.json":  "{\"needle\": true}\n",
		"assets/blob.bin":      "needle\x00binary\n",
		".github/workflow.yml": "name: needle\n",
		// .gitignore is honoured only inside a repository, so the fixture is one.
		".git/HEAD": "ref: refs/heads/main\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

// UNDER Plain THE ROWS ARE THE REAL COMMAND'S ROWS. Plain is what an intercepted search renders
// for a pipeline, and it pruned node_modules and vendor anyway, so `grep -rn X . | head` lost
// every dependency hit and nothing in the output said so.
func TestPlainSearchPrunesNothingARealGrepSearches(t *testing.T) {
	skippedFixture(t)
	res, err := SearchWith(Options{Pattern: "needle", Root: ".", Terse: true, Plain: true, LineNumbers: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, Options{Pattern: "needle", Root: ".", Terse: true, Plain: true, LineNumbers: true})
	for _, want := range []string{"node_modules/dep.js:1:", "vendor/lib/v.go:1:", "src/a.go:3:", ".aracne/config.json:1:"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plain search lost %s:\n%s", want, out)
		}
	}
	// GNU grep reports a binary match on stderr, so the pipeline sees no row for it.
	if strings.Contains(out, "Binary file") || strings.Contains(out, "skipped") {
		t.Errorf("a plain search printed a line the real command's stdout would not hold:\n%s", out)
	}
	if !res.Found(OutputContent) {
		t.Error("the search matched; its exit status must say so")
	}

	// The caller's own --exclude-dir still applies: that one they asked for.
	res, _ = SearchWith(Options{Pattern: "needle", Root: ".", Plain: true, ExcludeDirs: []string{"node_modules"}}, nil)
	for _, m := range res.Matches {
		if strings.Contains(m.Path, "node_modules") {
			t.Errorf("--exclude-dir=node_modules was ignored: %s", m.Path)
		}
	}
}

// An enriched search prunes what .gitignore prunes, and nothing else -- no hardcoded
// dependency list, no scan.ignore, and no trailer explaining either.
func TestEnrichedSearchPrunesOnlyWhatGitIgnores(t *testing.T) {
	dir := skippedFixture(t)
	// No .gitignore yet: the trees a list used to prune are ordinary directories.
	opt := Options{Pattern: "needle", Root: ".", Terse: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, opt)
	for _, want := range []string{"node_modules/dep.js", "vendor/lib/v.go", ".github/workflow.yml", "src/a.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("an untracked-but-not-ignored tree must be searched, lost %s:\n%s", want, out)
		}
	}
	// aracne's own store is never searched, and nothing says so.
	if strings.Contains(out, ".aracne") {
		t.Errorf("aracne's store is not searchable:\n%s", out)
	}
	if strings.Contains(out, "skipped") {
		t.Errorf("a search explains no pruning any more:\n%s", out)
	}

	// Now the project ignores them, and only then are they pruned.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\nvendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	out = FormatResult(res, opt)
	for _, gone := range []string{"node_modules", "vendor"} {
		if strings.Contains(out, gone) {
			t.Errorf("an ignored tree was searched anyway (%s):\n%s", gone, out)
		}
	}
	if !strings.Contains(out, ".github/workflow.yml") {
		t.Errorf("a dot-directory the project tracks must still be searched:\n%s", out)
	}
	// An empty answer is an empty answer: the note that used to stand in for one is gone.
	opt.Pattern = "only-in-deps-const needle = 1"
	res, _ = SearchWith(opt, nil)
	if out := FormatResult(res, opt); strings.Contains(out, "skipped") {
		t.Errorf("an empty search names nothing it did not look in, got %q", out)
	}
}

// scan.ignore is the SCANNER's rule about what earns a place in the topology. It used to
// prune the search as well, so a directory deliberately left out of the graph could not be
// grepped either -- with its files sitting right there on disk.
func TestScanIgnoreDoesNotPruneTheSearch(t *testing.T) {
	dir := skippedFixture(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Options{Pattern: "needle", Root: ".", Terse: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	// vendor/ is a scan.ignore-shaped tree: excluded from the topology in plenty of
	// projects, and still a tree whose source the caller may grep.
	if out := FormatResult(res, opt); !strings.Contains(out, "vendor/lib/v.go") {
		t.Errorf("vendor/ is not ignored by git here and must be searched:\n%s", out)
	}
}

// A PATH NAMED ON THE COMMAND LINE IS SEARCHED THOUGH IT IS IGNORED, which is ripgrep's rule:
// asking for a directory by name and being told nothing is in it answers a question nobody
// asked. The exemption covers the named directory only -- a rule matching the file itself
// still applies.
func TestNamedRootEscapesTheIgnoreHierarchy(t *testing.T) {
	dir := skippedFixture(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n*.bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Options{Pattern: "needle", Roots: []string{"node_modules"}, Terse: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out := FormatResult(res, opt); !strings.Contains(out, "dep.js") {
		t.Errorf("a named ignored directory must be searched:\n%s", out)
	}

	// But a rule that matches the file on its own name is not shielded by naming its parent.
	if err := os.WriteFile(filepath.Join(dir, "assets", "note.bin"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt = Options{Pattern: "needle", Roots: []string{"assets"}, Terse: true}
	res, _ = SearchWith(opt, nil)
	if out := FormatResult(res, opt); strings.Contains(out, "note.bin") {
		t.Errorf("*.bin matches the file itself and still applies:\n%s", out)
	}
}

// The hierarchy is a hierarchy: a .gitignore deeper in the tree decides for its own subtree,
// and `!` re-includes. Without both, the rules that actually keep a repository's huge
// generated trees out of a search are the ones that do not work.
func TestNestedIgnoreFilesAndNegation(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		".git/HEAD":         "ref: refs/heads/main\n",
		".gitignore":        "*.tmp\n!important.tmp\n",
		"sub/.gitignore":    "deep/\n",
		"a.tmp":             "needle\n",
		"important.tmp":     "needle\n",
		"sub/s.go":          "needle\n",
		"sub/deep/d.go":     "needle\n",
		"sub/inner/keep.go": "needle\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	opt := Options{Pattern: "needle", Root: ".", Terse: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, opt)
	for _, want := range []string{"important.tmp", "sub/s.go", "sub/inner/keep.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %s:\n%s", want, out)
		}
	}
	for _, gone := range []string{"a.tmp", "sub/deep/d.go"} {
		if strings.Contains(out, gone) {
			t.Errorf("searched %s, which the hierarchy excludes:\n%s", gone, out)
		}
	}
}

// .gitignore binds only inside a repository, which is ripgrep's rule: a .gitignore in a
// directory git does not track is a file somebody copied in, and pruning a tree on the
// strength of it skips source nothing is actually ignoring. .ignore is unconditional --
// it exists to tell a search what to skip, repository or not.
func TestGitignoreNeedsARepositoryAndDotIgnoreDoesNot(t *testing.T) {
	write := func(dir string, files map[string]string) {
		t.Helper()
		for rel, body := range files {
			full := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	opt := Options{Pattern: "needle", Root: ".", Terse: true}

	bare := t.TempDir()
	write(bare, map[string]string{".gitignore": "build/\n", "build/x.go": "needle\n", "top.go": "needle\n"})
	t.Chdir(bare)
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out := FormatResult(res, opt); !strings.Contains(out, "build/x.go") {
		t.Errorf("no repository, so .gitignore does not bind:\n%s", out)
	}

	// The same tree, with the same rule written where it always binds.
	plain := t.TempDir()
	write(plain, map[string]string{".ignore": "build/\n", "build/x.go": "needle\n", "top.go": "needle\n"})
	t.Chdir(plain)
	res, err = SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, opt)
	if strings.Contains(out, "build/x.go") {
		t.Errorf(".ignore binds without a repository:\n%s", out)
	}
	if !strings.Contains(out, "top.go") {
		t.Errorf("the rest of the tree is still searched:\n%s", out)
	}
}

// A match in a file the topology never scanned is a row like any other: the path, the line
// and the text. What it must NOT carry is a header standing in for the description it has
// not got -- "no description" is a phrase that costs tokens on every such row to say nothing.
func TestUnscannedFilesGetRowsWithoutAnEmptyHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("a needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	opt := Options{Pattern: "needle", Root: ".", Terse: true, LineNumbers: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, opt)
	if !strings.Contains(out, "notes.txt:1:a needle here") {
		t.Errorf("a file outside the topology is searched and printed plainly:\n%s", out)
	}
	if strings.Contains(out, "no description") || strings.Contains(out, "—") {
		t.Errorf("an absent description is absent, not spelled out:\n%s", out)
	}
}

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

// An enriched search still prunes the dependency trees -- and now says which, so an empty
// answer does not read as "nothing uses this". VCS metadata and aracne's store are pruned
// silently: no search is about them.
func TestEnrichedSearchNamesTheTreesItSkipped(t *testing.T) {
	skippedFixture(t)
	opt := Options{Pattern: "needle", Root: ".", Terse: true}
	res, err := SearchWith(opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Matches {
		if strings.Contains(m.Path, "node_modules") || strings.Contains(m.Path, "vendor") {
			t.Errorf("the enriched search descended into %s", m.Path)
		}
	}
	out := FormatResult(res, opt)
	if !strings.Contains(out, "… skipped ./node_modules, ./vendor") {
		t.Errorf("the trailer does not name the skipped trees:\n%s", out)
	}
	if strings.Contains(out, ".aracne") || strings.Contains(out, ".git,") {
		t.Errorf("aracne's own store is not worth naming:\n%s", out)
	}
	if !strings.Contains(out, ".github/workflow.yml") {
		t.Errorf("a dot-directory that is not noise must still be searched:\n%s", out)
	}

	// With no match at all, the note is the answer.
	opt.Pattern = "only-in-deps-const needle = 1"
	res, _ = SearchWith(opt, nil)
	if out := FormatResult(res, opt); !strings.HasPrefix(out, "… skipped") {
		t.Errorf("an empty search must still name what it did not look in, got %q", out)
	}
}

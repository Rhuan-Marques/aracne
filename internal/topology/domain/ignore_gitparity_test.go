package domain

import (
	"sort"
	"strings"
	"testing"
)

// gitParityPaths is the file tree every gitParityCases pattern is matched against.
var gitParityPaths = strings.Fields(`foo/a.go x/foo/b.go foo/sub/c.go s1.go s2.go s3.go sa.go d/s1.go
	a/b/c.go a/x/y/b/z.go b/k.go x/b/k.go doc/t.txt doc/sub/t.txt bar/q.go z/bar/q.go m.go
	xmy_folder/m.go ab.go s]/x.go s].go 1.go s-.go s[1].go a1.go app/[slug]/page.go
	app/s/page.go [slug]/x.go s*.go`)

// gitParityCases records, for each pattern alone in a .gitignore, exactly which of
// gitParityPaths `git check-ignore --no-index` reports as ignored (git 2.x, generated from a
// scratch repository). configuration.md calls scan.ignore ".gitignore-style" with no "!"
// negation as the one stated difference, so for everything else git is the oracle.
var gitParityCases = []struct {
	pattern string
	ignored []string
}{
	{`foo/**`, []string{"foo/a.go", "foo/sub/c.go"}},
	{`foo/`, []string{"foo/a.go", "x/foo/b.go", "foo/sub/c.go"}},
	{`foo`, []string{"foo/a.go", "x/foo/b.go", "foo/sub/c.go"}},
	{`/foo`, []string{"foo/a.go", "foo/sub/c.go"}},
	{`**/foo`, []string{"foo/a.go", "x/foo/b.go", "foo/sub/c.go"}},
	{`foo/*`, []string{"foo/a.go", "foo/sub/c.go"}},
	{`s[12].go`, []string{"s1.go", "s2.go", "d/s1.go"}},
	{`s[!1].go`, []string{"s2.go", "s3.go", "sa.go", "s].go", "s-.go", "s*.go"}},
	{`s[^1].go`, []string{"s2.go", "s3.go", "sa.go", "s].go", "s-.go", "s*.go"}},
	{`s[a-c].go`, []string{"sa.go"}},
	{`a/**/b`, []string{"a/b/c.go", "a/x/y/b/z.go"}},
	{`**/b/*.go`, []string{"a/b/c.go", "a/x/y/b/z.go", "b/k.go", "x/b/k.go"}},
	{`x/foo/`, []string{"x/foo/b.go"}},
	{`*my_folder/`, []string{"xmy_folder/m.go"}},
	{`doc/*.txt`, []string{"doc/t.txt"}},
	{`/bar/*.go`, []string{"bar/q.go"}},
	{`foo/**/`, []string{"foo/sub/c.go"}},
	{`**/foo/**`, []string{"foo/a.go", "x/foo/b.go", "foo/sub/c.go"}},
	{`a/**`, []string{"a/b/c.go", "a/x/y/b/z.go"}},
	{`[sd]*/`, []string{"foo/sub/c.go", "d/s1.go", "doc/t.txt", "doc/sub/t.txt", "s]/x.go", "app/s/page.go"}},
	{`s[]].go`, []string{"s].go"}},
	{`[[:digit:]].go`, []string{"1.go"}},
	{`s[[:alpha:]].go`, []string{"sa.go"}},
	{`[a-z][0-9].go`, []string{"s1.go", "s2.go", "s3.go", "d/s1.go", "a1.go"}},
	{`s[!a-z].go`, []string{"s1.go", "s2.go", "s3.go", "d/s1.go", "s].go", "s-.go", "s*.go"}},
	{`s[-a].go`, []string{"sa.go", "s-.go"}},
	{`s[a-].go`, []string{"sa.go", "s-.go"}},
	{`s\[1].go`, []string{"s[1].go"}},
	{`x/[fb]oo/`, []string{"x/foo/b.go"}},
	{`[fx]oo/**`, []string{"foo/a.go", "foo/sub/c.go"}},
	{`**/[fb]oo/**`, []string{"foo/a.go", "x/foo/b.go", "foo/sub/c.go"}},
	{`s[\]].go`, []string{"s].go"}},
	{`app/\[slug\]/`, []string{"app/[slug]/page.go"}},
	{`\[slug\]`, []string{"app/[slug]/page.go", "[slug]/x.go"}},
	{`[slug]`, []string{"app/s/page.go"}},
	{`s\*.go`, []string{"s*.go"}},
}

// TestIgnoreMatcher_GitParity pins ST-5 and ST-9 against git's own answers.
//
// ST-5: `foo/**` was compiled as the floating `foo/` -- the "/**" was stripped before the
// middle slash could anchor it -- so it ignored every nested directory named foo. ST-9: "[" and
// "]" were escaped as literals, so `s[12].go` and every other bracket class copied from a
// .gitignore silently matched nothing; "\" escapes come with them, since a literal bracket
// (`app/\[slug\]/`) is otherwise inexpressible. The rest of the table pins the behaviour those
// fixes sit next to: floating vs anchored rules, "**" in each position, dir-only rules.
func TestIgnoreMatcher_GitParity(t *testing.T) {
	for _, tc := range gitParityCases {
		m := BuildIgnoreMatcher("/repo", []string{tc.pattern})
		var got []string
		for _, p := range gitParityPaths {
			if m.Match("/repo/" + p) {
				got = append(got, p)
			}
		}
		want := append([]string(nil), tc.ignored...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("pattern %q:\n  aracne ignores %v\n  git ignores    %v", tc.pattern, got, want)
		}
	}
}

// TestIgnoreMatcher_AnchoredDirStarStarPrunes pins that ST-5's anchoring keeps `X/**` a
// directory rule a walk can prune whole, at the root only.
func TestIgnoreMatcher_AnchoredDirStarStarPrunes(t *testing.T) {
	m := BuildIgnoreMatcher("/repo", []string{"foo/**"})
	if !m.MatchDir("/repo/foo") {
		t.Error("foo/** must let a walk prune the root-level foo")
	}
	if m.MatchDir("/repo/x/foo") {
		t.Error("foo/** is anchored by its middle slash and must not prune a nested foo")
	}
}

// TestGlobToRegexp_BracketEdgeCases covers the bracket syntax git would abort on or that
// needs care in a regexp class: an unclosed "[" stays a literal, "/" is never a class member,
// and an invalid class compiles to an error the matcher drops rather than a panic.
func TestGlobToRegexp_BracketEdgeCases(t *testing.T) {
	re, err := GlobToRegexp("s[1.go")
	if err != nil || !re.MatchString("s[1.go") || re.MatchString("s1.go") {
		t.Errorf("an unclosed bracket is a literal: err=%v", err)
	}
	re, err = GlobToRegexp("a[!x]b")
	if err != nil || re.MatchString("a/b") || !re.MatchString("a-b") {
		t.Errorf("a negated class never matches a slash: err=%v", err)
	}
	re, err = GlobToRegexp("a[/x]b")
	if err != nil || re.MatchString("a/b") || !re.MatchString("axb") {
		t.Errorf("a slash member is dropped: err=%v", err)
	}
	if _, err := GlobToRegexp("[z-a]"); err == nil {
		t.Error("a reversed range cannot compile")
	}
	if m := BuildIgnoreMatcher("/repo", []string{"[z-a]", "gen/"}); !m.Match("/repo/gen/x.go") || m.Match("/repo/z") {
		t.Error("a pattern that cannot compile is dropped without affecting the others")
	}
}

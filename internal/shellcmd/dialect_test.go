package shellcmd

import (
	"regexp"
	"testing"
)

// ERE and ripgrep's syntax are not RE2 either. Passed through raw, RE2 read GNU's `{,3}` (zero
// to three) as a literal and GNU's `\d` (the letter d) as a digit, so `grep -rnE 'Ra{,3}dius'`
// came back "no matches" where grep found six. Each row here is a pattern whose meaning differs
// between the tool and RE2; it must reach the real tool.
func TestPatternsRE2WouldMisreadPassThrough(t *testing.T) {
	for _, argv := range [][]string{
		// GNU: an escaped letter is the letter itself (or a backreference).
		{"grep", "-rn", `\d`, "."},
		{"grep", "-rnE", `\d+`, "."},
		{"egrep", `\t`, "."},
		{"grep", "-E", `(a)\1`, "."},
		// GNU anchors the buffer with these; RE2 reads literals.
		{"grep", "-E", "\\`x", "."},
		{"grep", "-E", `x\'`, "."},
		// A start-of-word anchor before a non-word character never matches in GNU; `\b` would.
		{"grep", "-E", `\<-x`, "."},
		// POSIX reads `\` inside a bracket as itself; RE2 as an escape.
		{"grep", "-E", `[\.]Area`, "."},
		// GNU rejects a class name RE2 accepts.
		{"grep", "-E", `[[:word:]]`, "."},
		{"grep", `[[:word:]]`, "."},
		// A quantifier with nothing to repeat: GNU warns and reads it its own way.
		{"grep", "-E", `(?i)foo`, "."},
		{"grep", "-E", `*a`, "."},
		{"grep", "-E", `{1}a`, "."},
		{"grep", "-E", `a|*b`, "."},
		// A brace that starts an interval and is not one is an error to GNU, a literal to RE2.
		{"grep", "-E", `a{}`, "."},
		{"grep", "-E", `a{2,1}`, "."},
		{"grep", `a\{x\}`, "."},
		// ripgrep: `{,3}` and a stray brace are errors, `\<` is a word anchor, `\Q` and octal
		// escapes do not exist, and a class may nest or intersect.
		{"rg", `Ra{,3}dius`},
		{"rg", `a{`},
		{"rg", `\<Area`},
		{"rg", `\b{start}Area`},
		{"rg", `\Qa.b\E`},
		{"rg", `(a)\1`},
		{"rg", `[a[bc]]`},
		{"rg", `[a-z&&[^aeiou]]`},
		{"ug", `Ra{,3}dius`},
		// A newline separates patterns: grep ORs them, rg refuses them, RE2 never matches one.
		{"grep", "-rn", "Radius\nPerimeter", "."},
		{"grep", "-rnF", "Radius\nPerimeter", "."},
		{"rg", "Radius\nPerimeter"},
	} {
		if got := Parse(argv); got.Kind != KindPassthrough {
			t.Errorf("%q: kind = %v with pattern %q, want passthrough", argv, got.Kind, got.Grep.Pattern)
		}
	}
}

// What IS translated, is translated to the RE2 that means the same thing. The rows are the
// forms agents actually write, and each must still be answered.
func TestPatternsAreTranslatedToTheirRE2Meaning(t *testing.T) {
	// The POSIX classes follow LC_CTYPE (see asciiCtype), so the expectations below pin it
	// rather than inheriting whatever locale the machine running the tests has.
	t.Setenv("LC_ALL", "C.UTF-8")

	for _, tc := range []struct {
		argv []string
		want string
	}{
		// GNU's empty lower bound is zero.
		{[]string{"grep", "-rnE", `Ra{,3}dius`, "."}, `Ra{0,3}dius`},
		{[]string{"grep", "-rn", `Ra\{,1\}dius`, "."}, `Ra{0,1}dius`},
		{[]string{"grep", "-rnE", `Ra{1,3}dius`, "."}, `Ra{1,3}dius`},
		{[]string{"grep", "-rn", `a\{2\}`, "."}, `a{2}`},
		// A brace that cannot start an interval is a literal to both.
		{[]string{"grep", "-E", `struct {$`, "."}, `struct \{$`},
		{[]string{"grep", "-E", `a{x}`, "."}, `a\{x}`},
		// `\s` takes a vertical tab in GNU and rg; RE2's does not.
		{[]string{"grep", "-E", `func\s+\w+`, "."}, `func[\t\n\v\f\r\p{Z}]+\w+`},
		{[]string{"rg", `[\s,]+`}, `[\t\n\v\f\r\p{Z},]+`},
		// Word anchors next to a word character, the shape agents use.
		{[]string{"grep", "-E", `\<Area\>`, "."}, `\bArea\b`},
		{[]string{"grep", `\<Area\>`, "."}, `\bArea\b`},
		// Escaped punctuation, classes and groups the two syntaxes share.
		{[]string{"grep", "-E", `\.go$`, "."}, `\.go$`},
		{[]string{"grep", "-E", `^[[:space:]]*(func|type) `, "."}, `^[\t\n\v\f\r\p{Z}]*(func|type) `},
		{[]string{"rg", `(?i)todo`}, `(?i)todo`},
		{[]string{"rg", `(?:foo|bar)+\d{2}`}, `(?:foo|bar)+\d{2}`},
		{[]string{"rg", `\p{Greek}+`}, `\p{Greek}+`},
		{[]string{"rg", `fn\s+\w+\(`}, `fn[\t\n\v\f\r\p{Z}]+\w+\(`},
	} {
		got := Parse(tc.argv)
		if got.Kind != KindGrep {
			t.Errorf("%q: kind = %v (%s), want grep", tc.argv, got.Kind, got.Why)
			continue
		}
		if got.Grep.Pattern != tc.want {
			t.Errorf("%q: pattern = %q, want %q", tc.argv, got.Grep.Pattern, tc.want)
		}
	}
}

// The translations measured against the real tools. Each verdict below is what GNU grep 3.11
// (or ripgrep 14.1) printed for the line; the translated RE2 must agree on every one.
func TestTranslatedPatternsMatchWhatTheToolMatches(t *testing.T) {
	for _, tc := range []struct {
		argv    []string
		matches map[string]bool
	}{
		{[]string{"grep", "-E", `Ra{,3}dius`, "."}, map[string]bool{
			"Radius": true, "Rdius": true, "Raaadius": true, "Raaaadius": false, "Ra{,3}dius": false}},
		{[]string{"grep", `Ra\{,1\}dius`, "."}, map[string]bool{"Radius": true, "Rdius": true, "Raadius": false}},
		{[]string{"grep", "-E", `x\sy`, "."}, map[string]bool{"x\vy": true, "x y": true, "xy": false}},
		{[]string{"rg", `x\sy`}, map[string]bool{"x\vy": true, "x\ty": true, "x_y": false}},
		{[]string{"grep", "-E", `a{x}`, "."}, map[string]bool{"a{x}": true, "ax": false}},
	} {
		got := Parse(tc.argv)
		if got.Kind != KindGrep {
			t.Errorf("%q: kind = %v (%s), want grep", tc.argv, got.Kind, got.Why)
			continue
		}
		re := regexp.MustCompile(got.Grep.Pattern)
		for line, want := range tc.matches {
			if re.MatchString(line) != want {
				t.Errorf("%q (as %q) on %q: match = %v, the real tool says %v",
					tc.argv, got.Grep.Pattern, line, !want, want)
			}
		}
	}
}

// ag and ack are not grep: `-n` stops their recursion, ack's `-x` reads a file list from stdin,
// ag's `-t` searches all text, both number every line by default and both take a Perl regex.
// None of that is in grep's flag table, so neither is answered.
func TestAgAndAckAreNotReadAsGrep(t *testing.T) {
	for _, cmd := range []string{
		"ag Perimeter go", "ag -n Perimeter go", "ag -t Perimeter",
		"ack Perimeter go/shapes", "ack -x Perimeter", "ack -n Perimeter .",
	} {
		mustPass(t, cmd)
	}
}

// ripgrep shares most of grep's letters, not all. `-r` replaces every match (so `rg -rn foo`
// prints "n" for each hit), `-E` names an encoding, `--color` takes the next word as its
// value, and several grep flags do not exist in rg at all. A type is answered only when
// topogrep's file set for it is exactly rg's, and only one of them.
func TestRipgrepFlagsAreReadRipgrepsWay(t *testing.T) {
	for _, cmd := range []string{
		"rg -rn foo .", "rg -r X foo", "rg -E utf8 foo", "rg -R foo", "rg -G foo", "rg -y foo",
		"rg --recursive foo", "rg --extended-regexp foo", "rg --color never foo",
		"rg --binary-files=without-match foo",
		"rg -t cpp foo", "rg -t c foo", "rg -t java foo", "rg -t json foo", "rg -t sh foo",
		"rg -t javascript foo", "rg --type=cpp foo", "rg -t go -t py foo",
		"ug -t python foo", "ug -t js foo",
	} {
		mustPass(t, cmd)
	}

	for _, tc := range []struct {
		cmd    string
		check  func(GrepRequest) bool
		expect string
	}{
		{"rg -n foo src", func(g GrepRequest) bool { return g.LineNumbers && g.Pattern == "foo" }, "-n"},
		{"rg -i foo", func(g GrepRequest) bool { return g.IgnoreCase }, "-i"},
		{"rg -i -s foo", func(g GrepRequest) bool { return !g.IgnoreCase }, "-s after -i is case-sensitive"},
		{"rg -s -i foo", func(g GrepRequest) bool { return g.IgnoreCase }, "-i after -s is not"},
		{"rg -t go foo", func(g GrepRequest) bool { return g.Type == "go" }, "-t go"},
		{"rg -t js foo", func(g GrepRequest) bool { return g.Type == "js" }, "-t js"},
		{"rg --type=ts foo", func(g GrepRequest) bool { return g.Type == "ts" }, "--type=ts"},
		{"rg --color=never foo", func(g GrepRequest) bool { return g.Pattern == "foo" }, "--color=never"},
		{"ug -t go foo", func(g GrepRequest) bool { return g.Type == "go" }, "ug -t go"},
		// grep keeps grep's meanings for the letters rg reads differently.
		{"grep -rn foo .", func(g GrepRequest) bool { return g.LineNumbers }, "grep -r"},
		{"grep -rns foo .", func(g GrepRequest) bool { return g.Pattern == "foo" }, "grep -s"},
		{"grep -E -i foo .", func(g GrepRequest) bool { return g.IgnoreCase }, "grep -E"},
	} {
		if g := grepOf(t, tc.cmd); !tc.check(g) {
			t.Errorf("%q: %s not as expected: %+v", tc.cmd, tc.expect, g)
		}
	}
}

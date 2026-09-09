package helper

import (
	"strings"
	"testing"
)

// normCase is one pair of sources and whether their normalized hashes must agree.
type normCase struct {
	name string
	lang string
	a    string
	b    string
	// same is what NormHash must say. ExactHash is asserted separately, because the whole
	// point of having two hashes is that they disagree about formatting.
	same bool
}

func lines(s string) []string { return strings.Split(strings.TrimPrefix(s, "\n"), "\n") }

func normOf(t *testing.T, lang, src string) string {
	t.Helper()
	_, n, _ := BodyHashes(lang, "", lines(src))
	if n == "" {
		t.Fatalf("%s: body was refused as too small; the fixture needs more code:\n%s", lang, src)
	}
	return n
}

// A COMMENT EDIT MUST NOT MOVE THE HASH, AND A STRING EDIT MUST.
//
// These two requirements pull in opposite directions and they are the whole reason the
// normalizer carries quote state. The comment cases fail if it strips too little; the string
// cases fail if it strips too much, which is the dangerous direction -- two different bodies
// sharing a hash is a description landing on the wrong resource.
func TestNormalizeIgnoresCommentsAndRespectsStrings(t *testing.T) {
	cases := []normCase{
		{
			name: "go/comment reworded above and inside",
			lang: "go",
			a: `
func Total(xs []int) int {
	// add them up
	sum := 0
	for _, x := range xs {
		sum += x // accumulate
	}
	return sum
}`,
			b: `
func Total(xs []int) int {
	// COMPLETELY DIFFERENT WORDS HERE
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}`,
			same: true,
		},
		{
			name: "go/block comment and blank lines",
			lang: "go",
			a: `
func Total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}`,
			b: `
func Total(xs []int) int {

	/* a block
	   comment spanning lines */
	sum := 0

	for _, x := range xs {
		sum += x
	}

	return sum
}`,
			same: true,
		},
		{
			name: "go/reindented and CRLF",
			lang: "go",
			a: `
func Total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}`,
			b:    "\nfunc Total(xs []int) int {\r\n\tsum := 0   \r\n\tfor _, x := range xs {\r\n\t\tsum += x\t\r\n\t}\r\n\treturn sum\r\n}",
			same: true,
		},
		{
			name: "go/slashes inside a string are not a comment",
			lang: "go",
			a: `
func Endpoint() string {
	base := "http://example.com/a"
	path := "/v1/resource"
	return base + path
}`,
			b: `
func Endpoint() string {
	base := "http://example.com/b"
	path := "/v1/resource"
	return base + path
}`,
			same: false,
		},
		{
			name: "go/raw string holding a comment marker",
			lang: "go",
			a:    "\nfunc Doc() string {\n\ttpl := `line one // not a comment\nline two`\n\tn := len(tpl)\n\treturn tpl[:n]\n}",
			b:    "\nfunc Doc() string {\n\ttpl := `line one // not a comment\nline TWO`\n\tn := len(tpl)\n\treturn tpl[:n]\n}",
			same: false,
		},
		{
			name: "python/hash inside a string is not a comment",
			lang: "python",
			a: `
def find(text):
    pattern = re.compile("#tag-a")
    hits = pattern.findall(text)
    return sorted(hits)`,
			b: `
def find(text):
    pattern = re.compile("#tag-b")
    hits = pattern.findall(text)
    return sorted(hits)`,
			same: false,
		},
		{
			name: "python/comment reworded",
			lang: "python",
			a: `
def find(text):
    # locate the tags
    pattern = re.compile("tag")
    hits = pattern.findall(text)
    return sorted(hits)`,
			b: `
def find(text):
    # something else entirely
    pattern = re.compile("tag")
    hits = pattern.findall(text)
    return sorted(hits)  # trailing`,
			same: true,
		},
		{
			// The regex ends in an escaped `//`, so a blind strip cuts the line there and
			// throws away everything after it -- including the only text that differs. Both
			// bodies then hash the same, which is the misattribution the bail-out exists to
			// prevent. Mutation-tested: disabling regexPossible fails exactly this case.
			name: "js/blind strip inside a regex would collide two bodies",
			lang: "javascript",
			a: `
function clean(url) {
  const re = /https:\/\//; const tag = "alpha";
  const out = url.replace(re, tag);
  return out.trim();
}`,
			b: `
function clean(url) {
  const re = /https:\/\//; const tag = "beta";
  const out = url.replace(re, tag);
  return out.trim();
}`,
			same: false,
		},
		{
			// The other half of the ordering rule: `//` at the head of a line is a comment in
			// every position, and must never be mistaken for a regex just because a regex is
			// also legal at the start of a line.
			name: "js/leading line comment is still stripped",
			lang: "javascript",
			a: `
function clean(url) {
  const out = url.replace("a", "b");
  return out.trim();
}`,
			b: `
function clean(url) {
  // a leading comment, which is not a regex
  const out = url.replace("a", "b");
  return out.trim();
}`,
			same: true,
		},
		{
			name: "js/template literal holding a comment marker",
			lang: "javascript",
			a:    "\nfunction render(x) {\n  const s = `prefix // not a comment ${x}`;\n  const t = s.toUpperCase();\n  return t;\n}",
			b:    "\nfunction render(x) {\n  const s = `prefix // not a comment ${x}`;\n  // an actual comment\n  const t = s.toUpperCase();\n  return t;\n}",
			same: true,
		},
		{
			name: "rust/raw string holding a comment marker",
			lang: "rust",
			a: `
fn pattern() -> String {
    let raw = r#"// this is data, not a comment"#;
    let owned = raw.to_string();
    owned
}`,
			b: `
fn pattern() -> String {
    let raw = r#"// this is DATA, not a comment"#;
    let owned = raw.to_string();
    owned
}`,
			same: false,
		},
		{
			name: "rust/nested block comment and doc comments",
			lang: "rust",
			a: `
fn total(xs: &[i32]) -> i32 {
    let mut sum = 0;
    for x in xs {
        sum += x;
    }
    sum
}`,
			b: `
fn total(xs: &[i32]) -> i32 {
    /* outer /* inner */ still outer */
    /// doc line
    //! inner doc
    let mut sum = 0;
    for x in xs {
        sum += x;
    }
    sum
}`,
			same: true,
		},
		{
			name: "rust/lifetime is not a char literal",
			lang: "rust",
			a: `
fn longest<'a>(x: &'a str, y: &'a str) -> &'a str {
    if x.len() > y.len() {
        x
    } else {
        y
    }
}`,
			b: `
fn longest<'a>(x: &'a str, y: &'a str) -> &'a str {
    // pick the longer one
    if x.len() > y.len() {
        x
    } else {
        y
    }
}`,
			same: true,
		},
		{
			name: "java/text block holding a comment marker",
			lang: "java",
			a: `
String query() {
    String sql = """
        SELECT * FROM t -- keep
        WHERE url = 'http://a'
        """;
    return sql.strip();
}`,
			b: `
String query() {
    String sql = """
        SELECT * FROM t -- keep
        WHERE url = 'http://b'
        """;
    return sql.strip();
}`,
			same: false,
		},
		{
			name: "java/javadoc and line comments",
			lang: "java",
			a: `
int total(int[] xs) {
    int sum = 0;
    for (int x : xs) {
        sum += x;
    }
    return sum;
}`,
			b: `
int total(int[] xs) {
    /** javadoc that says nothing */
    int sum = 0;
    for (int x : xs) {
        sum += x; // accumulate
    }
    return sum;
}`,
			same: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ha, hb := normOf(t, tc.lang, tc.a), normOf(t, tc.lang, tc.b)
			if got := ha == hb; got != tc.same {
				t.Errorf("NormHash equal = %v, want %v\n--- a ---%s\n--- normalized a ---\n%s\n--- b ---%s\n--- normalized b ---\n%s",
					got, tc.same, tc.a, strings.Join(NormalizeBody(tc.lang, "", lines(tc.a)), "\n"),
					tc.b, strings.Join(NormalizeBody(tc.lang, "", lines(tc.b)), "\n"))
			}
		})
	}
}

// A LOGIC CHANGE MUST MOVE THE HASH. The normalizer removes commentary, not meaning; a design
// that let `+` and `-` share a fingerprint would carry a description onto code that no longer
// does what it says.
func TestNormalizeKeepsRealChanges(t *testing.T) {
	a := `
func Total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}`
	for _, tc := range []struct{ name, b string }{
		{"operator", strings.Replace(a, "sum += x", "sum -= x", 1)},
		// Deliberately not similarity matching: a renamed local is a different fingerprint.
		{"renamed local", strings.ReplaceAll(a, "sum", "total")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if normOf(t, "go", a) == normOf(t, "go", tc.b) {
				t.Error("a real change left the normalized hash unmoved")
			}
		})
	}
}

// THE TWO HASHES MUST DISAGREE ABOUT FORMATTING, or there is no reason to keep both.
func TestExactHashIsStricterThanNormHash(t *testing.T) {
	a := `
func Total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}`
	b := strings.Replace(a, "sum := 0", "// commentary\n\tsum := 0", 1)

	ea, na, _ := BodyHashes("go", "", lines(a))
	eb, nb, _ := BodyHashes("go", "", lines(b))
	if ea == "" || na == "" || eb == "" || nb == "" {
		t.Fatal("fixture was refused as too small")
	}
	if ea == eb {
		t.Error("ExactHash ignored a comment; it is supposed to be byte-exact")
	}
	if na != nb {
		t.Error("NormHash noticed a comment; it is supposed to ignore one")
	}
}

// THE TIER FLOOR. Short bodies ARE fingerprinted -- a global refusal cost 12.3% of the
// described declarations in aracne's own topology -- but they must stay out of the tier that
// has dropped the enclosing type, which is the one a shared one-liner can fool.
func TestShortBodiesAreFingerprintedButBelowTheWeakTierFloor(t *testing.T) {
	for _, tc := range []struct{ name, lang, src string }{
		{"go error method", "go", "\nfunc (e *E) Error() string { return e.msg }"},
		{"go stub", "go", "\nfunc Todo() {\n\tpanic(\"not implemented\")\n}"},
		{"rust todo", "rust", "\nfn todo() {\n    todo!()\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, n, normLines := BodyHashes(tc.lang, "", lines(tc.src))
			if n == "" {
				t.Fatal("a short body must still be fingerprinted; moved_source keeps the parent and can use it")
			}
			if normLines >= WeakTierMinLines {
				t.Errorf("normLines = %d, want below the weak-tier floor of %d", normLines, WeakTierMinLines)
			}
		})
	}
}

// A body that normalizes to nothing must not be fingerprinted, or every empty declaration in
// the repository lands in one bucket.
func TestEmptyBodyIsNotFingerprinted(t *testing.T) {
	e, n, normLines := BodyHashes("go", "", lines("\n// just a comment\n\n"))
	if e != "" || n != "" || normLines != 0 {
		t.Errorf("an all-comment span was fingerprinted: exact=%q norm=%q lines=%d", e, n, normLines)
	}
}

// A comment-padded one-liner must be measured on what it actually does, not on how long its
// span is -- a raw line count would wave it straight past the floor.
func TestCommentPaddingDoesNotInflateTheBodySize(t *testing.T) {
	src := "\n// a long explanatory comment that goes on and on for a while\n// and keeps going, well past sixty-four characters of prose\n// and still more\nfunc Nil() error { return nil }"
	_, _, normLines := BodyHashes("go", "", lines(src))
	if normLines >= WeakTierMinLines {
		t.Errorf("normLines = %d; comment padding inflated a one-line body past the floor", normLines)
	}
}

// An unknown language must not strip anything. Guessing at comment syntax is how a normalizer
// mangles a string it does not understand.
func TestUnknownLanguageStripsNothing(t *testing.T) {
	src := "\nsection one // looks like a comment\nsection two # looks like another\nsection three -- and another\n"
	got := NormalizeBody("cobol", "prog.cbl", lines(src))
	for _, want := range []string{"//", "#", "--"} {
		if !strings.Contains(strings.Join(got, "\n"), want) {
			t.Errorf("unknown language stripped %q; it must leave the text alone", want)
		}
	}
}

// The language falls back to the file extension, because resources written before the language
// column existed carry an empty one.
func TestLanguageFallsBackToExtension(t *testing.T) {
	src := `
func Total(xs []int) int {
	sum := 0
	// commentary
	for _, x := range xs {
		sum += x
	}
	return sum
}`
	byLang := NormalizeBody("go", "", lines(src))
	byExt := NormalizeBody("", "/tmp/x/total.go", lines(src))
	if strings.Join(byLang, "\n") != strings.Join(byExt, "\n") {
		t.Errorf("extension fallback disagreed with the language field:\n%q\nvs\n%q", byLang, byExt)
	}
}

package shellcmd

import (
	"regexp"
	"testing"
)

// GRD-02. GNU grep in a UTF-8 locale (and Claude Code's ugrep-backed `grep`) evaluates the
// POSIX classes and `\s` against Unicode. RE2's POSIX classes are ASCII-only, and the
// translator copied the class through verbatim, so an intercepted `grep 'caf[[:alpha:]]'`
// over a line holding "café" answered "no matches" with exit 1 -- a confident wrong answer
// that tells the model the text does not exist.
//
// Classes with a faithful Unicode equivalent are translated; the ones without one (glibc's
// punct/print/graph, whose membership is locale data rather than a property) make the pattern
// untranslatable, so the real command runs and its output is reproduced exactly.

func matches(t *testing.T, translate func(string) (string, bool), pat, subject string) bool {
	t.Helper()
	out, ok := translate(pat)
	if !ok {
		t.Fatalf("translate(%q) refused; the search should have been answerable", pat)
	}
	re, err := regexp.Compile(out)
	if err != nil {
		t.Fatalf("translate(%q) = %q, which does not compile: %v", pat, out, err)
	}
	return re.MatchString(subject)
}

func TestGRD02_ClassesMatchUnicodeLikeGrep(t *testing.T) {
	t.Setenv("LC_ALL", "C.UTF-8") // the Unicode reading is the UTF-8 locale's; see asciiCtype

	cases := []struct {
		name    string
		pat     string
		subject string
	}{
		{"alpha matches an accented letter", "caf[[:alpha:]]", "# café naïve"},
		{"alpha inside a larger class", "r[[:alpha:]0-9]sum", "résum"},
		{"upper matches an accented capital", "[[:upper:]]", "CAFÉ"[len("CAF"):]},
		{"lower matches an accented minuscule", "[[:lower:]]", "é"},
		{"alnum matches an accented letter", "[[:alnum:]]", "é"},
		{"backslash-s matches an em space", `a\sb`, "a b"},
		{"space class matches an em space", "a[[:space:]]b", "a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !matches(t, breToRE2, tc.pat, tc.subject) {
				t.Errorf("BRE %q did not match %q; GNU grep matches it", tc.pat, tc.subject)
			}
			if !matches(t, ereToRE2, tc.pat, tc.subject) {
				t.Errorf("ERE %q did not match %q; GNU grep matches it", tc.pat, tc.subject)
			}
			// ripgrep evaluates both forms against Unicode by default.
			if !matches(t, rustToRE2, tc.pat, tc.subject) {
				t.Errorf("rg %q did not match %q; ripgrep matches it", tc.pat, tc.subject)
			}
		})
	}
}

// ASCII behaviour is unchanged: a class must still NOT match what it never matched.
func TestGRD02_ClassesStillExcludeWhatTheyShould(t *testing.T) {
	t.Setenv("LC_ALL", "C.UTF-8") // the Unicode reading is the UTF-8 locale's; see asciiCtype

	if matches(t, breToRE2, "[[:alpha:]]", "123 _-") {
		t.Error("[[:alpha:]] matched a string with no letters")
	}
	if matches(t, breToRE2, "[[:digit:]]", "abc") {
		t.Error("[[:digit:]] matched a string with no digits")
	}
	if matches(t, breToRE2, "[[:upper:]]", "abc") {
		t.Error("[[:upper:]] matched a lowercase string")
	}
}

// A class aracne cannot reproduce faithfully makes the pattern untranslatable, so `arac cmd`
// passes the search through to the real binary instead of answering it differently.
func TestGRD02_UnmappableClassesArePassedThrough(t *testing.T) {
	t.Setenv("LC_ALL", "C.UTF-8") // the Unicode reading is the UTF-8 locale's; see asciiCtype

	for _, pat := range []string{"[[:punct:]]", "a[[:print:]]b", "[[:graph:]]x"} {
		if out, ok := breToRE2(pat); ok {
			t.Errorf("breToRE2(%q) = %q, but glibc's class is locale data aracne cannot reproduce; it must refuse", pat, out)
		}
		if out, ok := ereToRE2(pat); ok {
			t.Errorf("ereToRE2(%q) = %q; it must refuse", pat, out)
		}
	}
}

// Under an explicit C/POSIX locale the real grep reads the classes as ASCII, and so must the
// translation: answering "café" for `[[:alpha:]]` there would be the same bug pointing the
// other way.
func TestGRD02_TheCLocaleKeepsTheASCIIReading(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	out, ok := breToRE2("caf[[:alpha:]]")
	if !ok {
		t.Fatal("breToRE2 refused a pattern it can translate")
	}
	if out != "caf[[:alpha:]]" {
		t.Fatalf("C locale translated to %q, want the ASCII class kept", out)
	}
	if matches(t, breToRE2, "caf[[:alpha:]]", "# café") {
		t.Error("the C locale must not match an accented letter, because grep does not")
	}
}

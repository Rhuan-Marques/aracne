package prompts

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// A profile with a blank field renders a contract with a hole in it, and nothing says so: the
// section is simply absent, and the project silently gets less than the verbosity it asked for.
// Adding a language is the moment this happens, which is why the check is over the whole table.
func TestEveryLanguageProfileIsComplete(t *testing.T) {
	for lang, p := range languageProfiles {
		for _, f := range []struct {
			name, value string
		}{
			{"Display", p.Display},
			{"Graph", p.Graph},
			{"IDs", p.IDs},
			{"Output", p.Output},
			{"Semantics", p.Semantics},
			{"Examples[0]", p.Examples[0]},
			{"Examples[1]", p.Examples[1]},
		} {
			if strings.TrimSpace(f.value) == "" {
				t.Errorf("language %q: %s is empty; its contract section renders blank", lang, f.name)
			}
		}
	}
}

// Every language aracne has a scanner for needs a profile. A scanner without one is a project
// that gets the language-free contract and no explanation -- the sections that would have
// taught its ID spelling and its edge semantics are just not there.
//
// The list is written out rather than derived from the scanner registry because that registry
// lives in internal/cli, which imports this package.
func TestEveryScannedLanguageHasAProfile(t *testing.T) {
	for _, lang := range []string{"go", "python", "javascript", "typescript", "rust", "java"} {
		if _, ok := languageProfiles[lang]; !ok {
			t.Errorf("language %q has a scanner but no contract profile", lang)
		}
	}
}

// The `arac read` example is the one line in the contract most likely to be copied verbatim, so
// it has to be spelled in the project's OWN language. Showing a Go module path to a Java
// project teaches the wrong ID form in exactly the place the model reaches for one.
func TestTheReadExampleUsesTheProjectsOwnIDs(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	cfg.ContractVerbosity = helper.ContractVerbosityHigh

	for lang, p := range languageProfiles {
		got := ContractContent(cfg, []string{lang})
		want := "arac read " + p.Examples[0] + " " + p.Examples[1]
		if !strings.Contains(got, want) {
			t.Errorf("language %q: contract does not show %q as its read example\n%s", lang, want, got)
		}
	}

	// A mixed repo takes one ID from each of the first two languages, which is also what a
	// real call into it looks like.
	got := ContractContent(cfg, []string{"java", "typescript"})
	want := "arac read " + languageProfiles["java"].Examples[0] + " " + languageProfiles["typescript"].Examples[0]
	if !strings.Contains(got, want) {
		t.Errorf("a Java/TypeScript project does not show %q as its read example\n%s", want, got)
	}

	// And before the first scan there is no honest ID to show, so the example shows the
	// shape rather than another language's spelling.
	if got := ContractContent(cfg, nil); !strings.Contains(got, "arac read <id> <id>") {
		t.Errorf("a contract with no languages invents an ID for its example\n%s", got)
	}
}

// profilesFor is fed whatever the database holds, which on a fresh or half-scanned project can
// be empty strings, duplicates, or a language no scanner reads. None of those is an error; all
// of them have to come out as a clean list, because the caller renders whatever it is given.
func TestProfilesForIsTotal(t *testing.T) {
	got := profilesFor([]string{"go", "GO", " go ", "", "cobol", "python"})
	if len(got) != 2 || got[0].Display != "Go" || got[1].Display != "Python" {
		t.Fatalf("profilesFor returned %d profiles, want Go then Python", len(got))
	}
	if len(profilesFor(nil)) != 0 {
		t.Error("profilesFor(nil) invented a profile")
	}
}

func TestLanguageNamesReadsAsProse(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"go"}, "Go"},
		{[]string{"go", "python"}, "Go and Python"},
		{[]string{"go", "python", "rust"}, "Go, Python and Rust"},
	} {
		if got := languageNames(profilesFor(tc.in)); got != tc.want {
			t.Errorf("languageNames(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

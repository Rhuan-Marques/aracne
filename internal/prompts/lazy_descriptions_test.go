package prompts

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func wanted(ids ...string) map[string]domain.ResourceKind {
	out := map[string]domain.ResourceKind{}
	for _, id := range ids {
		out[id] = domain.ResourceFunction
	}
	return out
}

func TestParseLazyDescriptions(t *testing.T) {
	reply := "pkg.Foo :: parses a config file\npkg.(T).Bar :: writes the row back"
	got := ParseLazyDescriptions(reply, wanted("pkg.Foo", "pkg.(T).Bar"))
	if got["pkg.Foo"] != "parses a config file" {
		t.Errorf("Foo = %q", got["pkg.Foo"])
	}
	if got["pkg.(T).Bar"] != "writes the row back" {
		t.Errorf("Bar = %q", got["pkg.(T).Bar"])
	}
}

// Models decorate. The parser tolerates the decoration because a fill that threw the batch
// away over a stray fence would have burned a provider call for nothing.
func TestParseLazyDescriptionsToleratesDecoration(t *testing.T) {
	reply := "Here you go:\n```\n1. `pkg.Foo` :: parses a config file\n- **pkg.Bar**::writes the row\n```\nDone!"
	got := ParseLazyDescriptions(reply, wanted("pkg.Foo", "pkg.Bar"))
	if got["pkg.Foo"] != "parses a config file" {
		t.Errorf("Foo = %q", got["pkg.Foo"])
	}
	if got["pkg.Bar"] != "writes the row" {
		t.Errorf("Bar = %q", got["pkg.Bar"])
	}
	if len(got) != 2 {
		t.Errorf("parsed %d entries, want 2: %v", len(got), got)
	}
}

// The one thing it is strict about. A description stored against the wrong resource is worse
// than no description at all, because it is re-shown on every later lookup as if it were true.
func TestParseLazyDescriptionsDropsUnaskedIDs(t *testing.T) {
	reply := "pkg.Foo :: mine\npkg.Invented :: not mine"
	got := ParseLazyDescriptions(reply, wanted("pkg.Foo"))
	if _, ok := got["pkg.Invented"]; ok {
		t.Fatal("an id that was not assigned was accepted")
	}
	if len(got) != 1 {
		t.Fatalf("parsed %v, want only the assigned id", got)
	}
}

func TestParseLazyDescriptionsCollapsesWhitespace(t *testing.T) {
	got := ParseLazyDescriptions("pkg.Foo ::   parses   a  config\tfile  ", wanted("pkg.Foo"))
	if got["pkg.Foo"] != "parses a config file" {
		t.Fatalf("Foo = %q", got["pkg.Foo"])
	}
}

// A model that contradicts itself gets its first answer kept -- the one it gave before it
// started padding.
func TestParseLazyDescriptionsFirstAnswerWins(t *testing.T) {
	got := ParseLazyDescriptions("pkg.Foo :: first\npkg.Foo :: second", wanted("pkg.Foo"))
	if got["pkg.Foo"] != "first" {
		t.Fatalf("Foo = %q, want the first answer", got["pkg.Foo"])
	}
}

func TestParseLazyDescriptionsIgnoresJunk(t *testing.T) {
	for _, reply := range []string{"", "I cannot help with that.", "pkg.Foo", "pkg.Foo :: "} {
		if got := ParseLazyDescriptions(reply, wanted("pkg.Foo")); len(got) != 0 {
			t.Errorf("%q parsed to %v, want nothing", reply, got)
		}
	}
}

// The input must carry the id verbatim and the budget, because those are the two things the
// reply is checked against.
func TestLazyDescriptionsInputCarriesIDsAndBudgets(t *testing.T) {
	in := LazyDescriptionsInput([]DescriptionResource{
		{ID: "pkg.(T).Method", Name: "Method", Kind: domain.ResourceMethod, ReadOutput: "func (t T) Method() {}"},
		{ID: "pkg.Thing", Name: "Thing", Kind: domain.ResourceStruct},
	}, []DescriptionExemplar{{Name: "Other", Kind: domain.ResourceFunction, Description: "does a thing"}})

	for _, want := range []string{
		"pkg.(T).Method", "pkg.Thing", "func (t T) Method() {}",
		"House style examples", "does a thing", "Per-kind limits",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("input is missing %q", want)
		}
	}
	if !strings.Contains(in, "source unavailable") {
		t.Error("a resource with no source should say so rather than pretend it has one")
	}
}

// The system prompt has to state the format the parser implements, or the two drift and every
// reply is dropped as junk.
func TestLazyDescriptionsPromptStatesTheFormat(t *testing.T) {
	p := LazyDescriptionsPrompt()
	if !strings.Contains(p, LazyDescriptionSeparator) {
		t.Error("the prompt does not show the separator the parser splits on")
	}
	if strings.Contains(p, "update_description") {
		t.Error("the lazy prompt must not ask for a tool call: it runs without tools")
	}
}

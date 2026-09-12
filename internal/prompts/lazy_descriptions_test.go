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

// `_` is an id character, not only emphasis. Trimming it off both ends turned every dunder
// method into an id nobody asked for, so the line was dropped and the resource was retried
// until it "exceeded max retries" -- on every Python class in the corpus.
func TestParseLazyDescriptionsKeepsUnderscoreIDs(t *testing.T) {
	reply := strings.Join([]string{
		"shapes.Circle.__init__ :: Builds a circle",
		"shapes._private::Hidden helper",
		"**shapes.Circle.__repr__** :: Renders the circle",
		"2. `pkg.value_` :: Trailing underscore",
		"- *pkg._star* :: Wrapped in single emphasis",
	}, "\n")
	ids := wanted("shapes.Circle.__init__", "shapes._private", "shapes.Circle.__repr__", "pkg.value_", "pkg._star")
	got := ParseLazyDescriptions(reply, ids)
	for id := range ids {
		if got[id] == "" {
			t.Errorf("%s was dropped; parsed %v", id, got)
		}
	}
}

// A wrapper is only ever peeled as a matched pair, and only when what is left is an id that was
// asked for. The least-peeled match wins, because a token that already names a requested id was
// copied exactly.
func TestParseLazyDescriptionsPeelsOnlyMatchedPairs(t *testing.T) {
	// `__x__` is bold for x when x is what was asked for...
	if got := ParseLazyDescriptions("__pkg.x__ :: bold", wanted("pkg.x")); got["pkg.x"] != "bold" {
		t.Errorf("underscore-bold id not unwrapped: %v", got)
	}
	// ...and is itself the id when both were asked for.
	got := ParseLazyDescriptions("__pkg.x__ :: literal", wanted("pkg.x", "__pkg.x__"))
	if got["__pkg.x__"] != "literal" || got["pkg.x"] != "" {
		t.Errorf("an exact id was peeled into a different one: %v", got)
	}
	// An unmatched wrapper is part of the token, and a token that is no requested id is dropped.
	if got := ParseLazyDescriptions("_pkg.x :: half", wanted("pkg.x")); len(got) != 0 {
		t.Errorf("an unmatched `_` was stripped onto a different id: %v", got)
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

// DE-11: the no-space "::" fallback cut at the FIRST "::", and every Rust id is `a::b::c`, so
// the id came out as `a` and the line was dropped -- a nospace model lost every Rust
// description. The cut now lands on the "::" that ends a requested id.
func TestParseLazyDescriptionsNoSpaceSeparatorWithRustIDs(t *testing.T) {
	reply := strings.Join([]string{
		"rustfamily::shapes::Circle::area::Computes the area",
		"rustfamily::shapes::Circle:: Describes a circle",
		"- `rustfamily::shapes::new`::Builds a shape from std::f64 values",
		"rustfamily::nope::x::Not asked for",
	}, "\n")
	got := ParseLazyDescriptions(reply, wanted(
		"rustfamily::shapes::Circle::area", "rustfamily::shapes::Circle", "rustfamily::shapes::new"))
	for id, want := range map[string]string{
		"rustfamily::shapes::Circle::area": "Computes the area",
		"rustfamily::shapes::Circle":       "Describes a circle",
		"rustfamily::shapes::new":          "Builds a shape from std::f64 values",
	} {
		if got[id] != want {
			t.Errorf("%s = %q, want %q", id, got[id], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("parsed %d entries, want 3: %v", len(got), got)
	}

	// The spaced separator and non-Rust ids behave exactly as before.
	got = ParseLazyDescriptions("pkg.Foo::calls std::fmt\nrustfamily::a::b :: Does b",
		wanted("pkg.Foo", "rustfamily::a::b"))
	if got["pkg.Foo"] != "calls std::fmt" || got["rustfamily::a::b"] != "Does b" {
		t.Errorf("got %v", got)
	}
}

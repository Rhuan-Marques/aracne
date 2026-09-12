package contract

import (
	"encoding/json"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// fn builds a callee resource the way a scanner does: typed structs in Properties.
func fn(name string, params ...Param) domain.Resource {
	in := make([]map[string]any, 0, len(params))
	for _, p := range params {
		in = append(in, map[string]any{"Name": p.Name, "Typing": p.Typing, "TypingID": p.TypingID})
	}
	return domain.Resource{
		ID: "pkg." + name, Kind: domain.ResourceFunction, Name: name, Language: "go",
		Properties: map[string]any{"input": in},
	}
}

func p(name, typing string) Param { return Param{Name: name, Typing: typing} }

// args builds a call site with concrete per-position tokens.
func args(tokens ...string) CallSite {
	site := CallSite{N: len(tokens)}
	for _, t := range tokens {
		if t == "?" {
			site.Types = append(site.Types, nil)
			continue
		}
		tok := t
		site.Types = append(site.Types, &tok)
	}
	return site
}

// arity builds a call site that says how many arguments were passed and nothing else,
// which is the common case in a language with no inferable argument types.
func arity(n int) CallSite { return CallSite{N: n} }

func match(t *testing.T, env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	t.Helper()
	m := For("go")
	if m == nil {
		t.Fatal("no matcher registered for go")
	}
	return m.Match(env, callee, site)
}

// TestGoArity covers the rule with the most value and the least ambiguity: Go has no
// default arguments and no overloads, so a count that does not fit is always wrong.
func TestGoArity(t *testing.T) {
	two := fn("F", p("a", "int"), p("b", "int"))
	variadic := fn("V", p("a", "int"), p("rest", "...string"))

	cases := []struct {
		name string
		fn   domain.Resource
		site CallSite
		want Verdict
	}{
		{"exact", two, arity(2), Match},
		{"too few", two, arity(1), Mismatch},
		{"too many", two, arity(3), Mismatch},
		{"variadic, none passed", variadic, arity(1), Match},
		{"variadic, several passed", variadic, arity(4), Match},
		{"variadic, fixed part missing", variadic, arity(0), Mismatch},
		// f(xs...) forwards a slice whose length is not knowable from the syntax, so the
		// count cannot be judged. Guessing here would warn on correct code.
		{"spread call is not counted", two, CallSite{N: 1, Variadic: true}, Match},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := match(t, Env{}, c.fn, c.site)
			if got != c.want {
				t.Errorf("got %s (%s), want %s", got, why, c.want)
			}
		})
	}
}

// TestGoUntypedConstantsAreAssignable is the case that decides whether this feature is
// usable at all. Go's `5` is an UNTYPED integer constant, legal wherever any numeric type
// is wanted. Comparing recorded type text for equality would report a mismatch on f(5)
// against f(x float64) -- correct, ubiquitous code -- and bury the agent in false warnings.
func TestGoUntypedConstantsAreAssignable(t *testing.T) {
	cases := []struct {
		declared string
		token    string
		want     Verdict
	}{
		{"int", UntypedInt, Match},
		{"float64", UntypedInt, Match},
		{"uint8", UntypedInt, Match},
		{"byte", UntypedInt, Match},
		{"rune", UntypedInt, Match},
		{"complex128", UntypedInt, Match},
		{"float32", UntypedFloat, Match},
		{"int", UntypedFloat, Mismatch},
		{"string", UntypedString, Match},
		{"int", UntypedString, Mismatch},
		{"bool", UntypedBool, Match},
		{"string", UntypedBool, Mismatch},
		{"int32", UntypedRune, Match},
		{"*Thing", UntypedNil, Match},
		{"[]byte", UntypedNil, Match},
		{"map[string]int", UntypedNil, Match},
		{"chan int", UntypedNil, Match},
		{"int", UntypedNil, Mismatch},
	}
	for _, c := range cases {
		t.Run(c.declared+"<-"+c.token, func(t *testing.T) {
			got, why := match(t, Env{}, fn("F", p("a", c.declared)), args(c.token))
			if got != c.want {
				t.Errorf("f(a %s) called with %s: got %s (%s), want %s",
					c.declared, c.token, got, why, c.want)
			}
		})
	}
}

// TestGoNamedTypeFollowsItsUnderlying: `type Celsius float64` accepts 5 exactly as float64
// does, so the matcher has to resolve the name before judging.
func TestGoNamedTypeFollowsItsUnderlying(t *testing.T) {
	env := Env{Lookup: func(id string) (domain.Resource, bool) {
		if id == "pkg.Celsius" {
			return domain.Resource{
				ID: id, Kind: domain.ResourceNamedType,
				Properties: map[string]any{"underlying": "float64"},
			}, true
		}
		return domain.Resource{}, false
	}}
	callee := fn("F", Param{Name: "t", Typing: "Celsius", TypingID: "pkg.Celsius"})

	if got, why := match(t, env, callee, args(UntypedInt)); got != Match {
		t.Errorf("Celsius should accept an untyped int: got %s (%s)", got, why)
	}
	if got, _ := match(t, env, callee, args(UntypedString)); got != Mismatch {
		t.Errorf("Celsius should reject an untyped string, got %s", got)
	}
}

// TestGoInterfaceParametersAreNotJudged: passing *os.File to an io.Writer is correct code
// whose argument type does not equal its parameter type. Deciding satisfaction is real type
// checking; declining is the honest answer and the one that avoids warning on every
// interface parameter in the repository.
func TestGoInterfaceParametersAreNotJudged(t *testing.T) {
	env := Env{Lookup: func(id string) (domain.Resource, bool) {
		if id == "io.Writer" {
			return domain.Resource{ID: id, Kind: domain.ResourceInterface}, true
		}
		return domain.Resource{}, false
	}}
	callee := fn("F", Param{Name: "w", Typing: "Writer", TypingID: "io.Writer"})
	if got, why := match(t, env, callee, args("*os.File")); got == Mismatch {
		t.Errorf("an interface parameter must not be type-judged, got Mismatch (%s)", why)
	}
	for _, declared := range []string{"any", "interface{}", "error", "T"} {
		if got, why := match(t, Env{}, fn("F", p("x", declared)), args("*os.File")); got == Mismatch {
			t.Errorf("%s must not be type-judged, got Mismatch (%s)", declared, why)
		}
	}
}

// TestGoConcreteTypeMismatch is the true positive the feature exists to produce.
func TestGoConcreteTypeMismatch(t *testing.T) {
	callee := fn("F", p("a", "string"))
	got, why := match(t, Env{}, callee, args("int"))
	if got != Mismatch {
		t.Fatalf("passing int to a string parameter should mismatch, got %s", got)
	}
	if why == "" {
		t.Error("a mismatch must explain itself; the message is shown to the agent")
	}
}

// TestGoUnknownArgumentsAreSkippedPositionally: one unresolvable argument must not discard
// the information carried by the others.
func TestGoUnknownArgumentsAreSkippedPositionally(t *testing.T) {
	callee := fn("F", p("a", "string"), p("b", "string"))
	if got, _ := match(t, Env{}, callee, args("?", "int")); got != Mismatch {
		t.Error("a bad second argument must still be caught when the first is unknown")
	}
	if got, _ := match(t, Env{}, callee, args("?", "?")); got == Mismatch {
		t.Error("two unknown arguments carry no information and must not mismatch")
	}
}

// TestGoVariadicChecksEveryTrailingArgument: a variadic parameter absorbs all the
// remaining arguments, so checking only the first of them would miss the rest.
func TestGoVariadicChecksEveryTrailingArgument(t *testing.T) {
	callee := fn("V", p("a", "int"), p("rest", "...string"))
	if got, _ := match(t, Env{}, callee, args(UntypedInt, "string", "string")); got != Match {
		t.Error("all-string variadic tail should match")
	}
	if got, _ := match(t, Env{}, callee, args(UntypedInt, "string", "int")); got != Mismatch {
		t.Error("a bad argument late in the variadic tail must be caught")
	}
}

// TestParamsReadBothResourceShapes: the same function reaches the matcher as typed scanner
// structs and as the JSON round-trip of those read back from SQLite. An earlier fix in this
// area was silently disabled by exactly this mismatch.
func TestParamsReadBothResourceShapes(t *testing.T) {
	typed := domain.Resource{Properties: map[string]any{"input": []Param{
		{Name: "a", Typing: "int"}, {Name: "b", Typing: "string"},
	}}}
	raw, err := json.Marshal(typed.Properties)
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(raw, &props); err != nil {
		t.Fatal(err)
	}
	persisted := domain.Resource{Properties: props}

	a, b := Params(typed), Params(persisted)
	if len(a) != len(b) || len(a) != 2 {
		t.Fatalf("parsed %d params, persisted %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("position %d differs by provenance: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPositionalArity(t *testing.T) {
	cases := []struct {
		name     string
		params   []Param
		min, max int
	}{
		{"none", nil, 0, 0},
		{"two required", []Param{p("a", "int"), p("b", "int")}, 2, 2},
		{"one optional", []Param{p("a", "int"), {Name: "b", Optional: true}}, 1, 2},
		{"variadic tail", []Param{p("a", "int"), {Name: "r", Variadic: true}}, 1, -1},
		{"keyword-only excluded", []Param{p("a", "int"), {Name: "k", KeyOnly: true}}, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PositionalArity(c.params)
			if got.Min != c.min || got.Max != c.max {
				t.Errorf("got {%d,%d}, want {%d,%d}", got.Min, got.Max, c.min, c.max)
			}
		})
	}
}

// TestGoZeroParamCalleeIsJudgedTheSameInBothShapes: a function with no parameters arrives as
// an empty list from a fresh parse and with no "input" property at all from SQLite. Both are
// the same function, and a call that fits one fits the other.
func TestGoZeroParamCalleeIsJudgedTheSameInBothShapes(t *testing.T) {
	fresh := fn("Take")
	persisted := domain.Resource{ID: "pkg.Take", Kind: domain.ResourceFunction, Name: "Take",
		Language: "go", Properties: map[string]any{}}
	nullInput := domain.Resource{ID: "pkg.Take", Kind: domain.ResourceFunction, Name: "Take",
		Language: "go", Properties: map[string]any{"input": nil}}
	for name, callee := range map[string]domain.Resource{
		"fresh": fresh, "persisted": persisted, "null input": nullInput,
	} {
		if got, why := match(t, Env{}, callee, arity(0)); got != Match {
			t.Errorf("%s: Take() against a zero-parameter Take: got %s (%s), want match", name, got, why)
		}
		// And the case that must keep warning: an argument the function no longer takes.
		if got, _ := match(t, Env{}, callee, args(UntypedInt)); got != Mismatch {
			t.Errorf("%s: Take(1) against a zero-parameter Take: got %s, want mismatch", name, got)
		}
	}
}

// A database written by an older aracne build holds that build's lossy renderings -- every
// func type as "func(...)", every struct literal as "struct{...}", a directional channel as
// "chan T". The first scan after an upgrade compares them against the full rendering, and
// must not read the change of renderer as a change of type: otherwise every function taking a
// callback warns at once. Rich text on both sides is compared literally, which is the point of
// rendering it.
func TestSameGoTypeTextToleratesOlderRenderings(t *testing.T) {
	same := []struct{ old, cur string }{
		{"func(...)", "func(string) error"},
		{"func(...)", "func()"},
		{"[]func(...)", "[]func(string, int) (bool, error)"},
		{"map[string]func(...)", "map[string]func(int) int"},
		{"struct{...}", "struct{A int; B string}"},
		{"interface{}", "interface{Read(p []byte) (int, error)}"},
		{"chan int", "chan<- int"},
		{"chan int", "<-chan int"},
		{"int", "int"},
	}
	for _, c := range same {
		if !SameGoTypeText(c.old, c.cur) {
			t.Errorf("SameGoTypeText(%q, %q) = false, want true (an upgrade must not warn about unchanged code)", c.old, c.cur)
		}
		if !SameGoTypeText(c.cur, c.old) {
			t.Errorf("SameGoTypeText is not symmetric for %q / %q", c.cur, c.old)
		}
	}

	// Two full renderings are compared as written -- this is the change GO-07 exists to see.
	differ := [][2]string{
		{"func(string) error", "func(string, int) error"},
		{"func(string) error", "func(string) (int, error)"},
		{"chan<- int", "<-chan int"},
		{"struct{A int}", "struct{B string}"},
		{"struct{}", "struct{A int}"},
		{"interface{Close() error}", "interface{Close(n int) error}"},
		{"int", "string"},
		{"[]func(int) int", "[]func(string) int"},
	}
	for _, c := range differ {
		if SameGoTypeText(c[0], c[1]) {
			t.Errorf("SameGoTypeText(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

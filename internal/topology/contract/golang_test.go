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

// TestTypeChangeIsJudgedAgainstTheShapeTheCallerWasWrittenAgainst pins the three answers a
// changed parameter type can produce, and the one case that must produce none of them.
//
// This is the rule that used to not exist. Every judging matcher collapsed "I compared
// nothing" into Match, so retyping a parameter withdrew the warning for every caller whose
// argument the scanner could not name -- over half of them in a real Go repository. The
// verdicts below are the difference between a channel that goes quiet on the most common
// breaking edit in a typed language and one that does not.
func TestTypeChangeIsJudgedAgainstTheShapeTheCallerWasWrittenAgainst(t *testing.T) {
	cases := []struct {
		name string
		old  []Param // what the caller was written against
		now  []Param // what it faces after the edit
		site CallSite
		want Verdict
	}{{
		// The headline widening case. `any` takes everything the old type did and more, so
		// no caller can have broken and none should hear about it -- whatever it passes.
		name: "widening to any warns nobody",
		old:  []Param{p("s", "string")},
		now:  []Param{p("s", "any")},
		site: arity(1),
		want: Match,
	}, {
		name: "widening to any warns nobody even with a readable argument",
		old:  []Param{p("s", "string")},
		now:  []Param{p("s", "any")},
		site: args(UntypedInt),
		want: Match,
	}, {
		// Narrowing from `any`: the callers were legitimately passing anything, so each one
		// has to be judged on what it actually passes.
		name: "narrowing from any accepts an argument that fits",
		old:  []Param{p("v", "any")},
		now:  []Param{p("v", "string")},
		site: args(UntypedString),
		want: Match,
	}, {
		name: "narrowing from any rejects an argument that does not",
		old:  []Param{p("v", "any")},
		now:  []Param{p("v", "string")},
		site: args(UntypedInt),
		want: Mismatch,
	}, {
		// The case this whole change exists for: the call site never recorded what it
		// passes, so the question cannot be answered -- and answering it "fine" is what
		// used to happen.
		name: "narrowing from any cannot judge an unreadable argument",
		old:  []Param{p("v", "any")},
		now:  []Param{p("v", "string")},
		site: args("?"),
		want: Unverified,
	}, {
		name: "a retype cannot judge an unreadable argument either",
		old:  []Param{p("s", "string")},
		now:  []Param{p("s", "[]byte")},
		site: args("?"),
		want: Unverified,
	}, {
		name: "a call site that recorded no types at all is still unverified",
		old:  []Param{p("s", "string")},
		now:  []Param{p("s", "[]byte")},
		site: arity(1),
		want: Unverified,
	}, {
		// Precision, and the reason the baseline is consulted at all: doubt about the
		// position that moved must not spread to the ones that did not.
		name: "an unreadable argument at an UNCHANGED position is not doubt",
		old:  []Param{p("a", "string"), p("b", "int")},
		now:  []Param{p("a", "string"), p("b", "int64")},
		site: args("?", UntypedInt),
		want: Match,
	}, {
		name: "and the changed position still decides when it is the unreadable one",
		old:  []Param{p("a", "string"), p("b", "int")},
		now:  []Param{p("a", "string"), p("b", "int64")},
		site: args(UntypedString, "?"),
		want: Unverified,
	}, {
		// An interface parameter accepts implementers whose type text never equals it, so
		// it is declined rather than doubted -- the same rule that makes `any` silent.
		name: "narrowing to an interface is declined, not doubted",
		old:  []Param{p("v", "any")},
		now:  []Param{{Name: "v", Typing: "io.Writer", TypingID: "io.Writer"}},
		site: args("?"),
		want: Match,
	}, {
		// A real break still outranks an unreadable neighbour.
		name: "a demonstrable mismatch outranks an unreadable position",
		old:  []Param{p("a", "string"), p("b", "string")},
		now:  []Param{p("a", "int"), p("b", "int")},
		site: args("?", UntypedString),
		want: Mismatch,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			callee := fn("F", c.now...)
			env := Env{OldParams: c.old, Lookup: func(id string) (domain.Resource, bool) {
				if id == "io.Writer" {
					return domain.Resource{ID: id, Kind: domain.ResourceInterface}, true
				}
				return domain.Resource{}, false
			}}
			got, why := match(t, env, callee, c.site)
			if got != c.want {
				t.Fatalf("got %v (%s), want %v", got, why, c.want)
			}
			// An adverse verdict the reader cannot act on is not worth reporting.
			if (got == Mismatch || got == Unverified) && why == "" {
				t.Fatalf("verdict %v carries no explanation", got)
			}
		})
	}
}

// TestNoBaselineKeepsTheOlderBehaviour pins the evidence rule: doubt is only reported about a
// position something can show moved. A warning raised before baselines existed, or carried
// across a rescan without one, must judge exactly as it did before.
func TestNoBaselineKeepsTheOlderBehaviour(t *testing.T) {
	callee := fn("F", p("s", "string"))
	for _, site := range []CallSite{arity(1), args("?")} {
		if got, _ := match(t, Env{}, callee, site); got != Match {
			t.Fatalf("no baseline: got %v, want match", got)
		}
	}
}

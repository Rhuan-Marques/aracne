package contract

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Per-language rule tables.
//
// Every language answers "does this call still fit" differently, and the differences are
// not stylistic -- they decide whether a warning is a true positive or noise printed to an
// agent after every edit. Each block below states one language's rules as cases, so a
// change to a matcher has to say which rule it is changing.
//
// Two directions matter equally and both are covered throughout: a call that does NOT fit
// must produce Mismatch, and a call that DOES fit must never produce one. The second is the
// one that costs turns when it is wrong.

// res builds a callee in the given language.
func res(lang, name string, kind domain.ResourceKind, params ...Param) domain.Resource {
	in := make([]map[string]any, 0, len(params))
	for _, p := range params {
		in = append(in, map[string]any{
			"Name": p.Name, "Typing": p.Typing, "TypingID": p.TypingID,
			"Optional": p.Optional, "Variadic": p.Variadic, "KeyOnly": p.KeyOnly,
		})
	}
	return domain.Resource{
		ID: "m." + name, Kind: kind, Name: name, Language: lang,
		Properties: map[string]any{"input": in},
	}
}

func fnOf(lang, name string, params ...Param) domain.Resource {
	return res(lang, name, domain.ResourceFunction, params...)
}

// ifaceEnv resolves the given ids to interfaces and nothing else.
func ifaceEnv(ids ...string) Env {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return Env{Lookup: func(id string) (domain.Resource, bool) {
		if set[id] {
			return domain.Resource{ID: id, Kind: domain.ResourceInterface}, true
		}
		return domain.Resource{}, false
	}}
}

type langCase struct {
	name   string
	callee domain.Resource
	site   CallSite
	env    Env
	want   Verdict
}

func runLangCases(t *testing.T, lang string, cases []langCase) {
	t.Helper()
	m := For(lang)
	if m == nil {
		t.Fatalf("no matcher registered for %q", lang)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := m.Match(c.env, c.callee, c.site)
			if got != c.want {
				t.Errorf("got %s (%s), want %s", got, why, c.want)
			}
			if got == Mismatch && why == "" {
				t.Error("a mismatch must explain itself; the message is shown to the agent")
			}
		})
	}
}

// --- Go ---------------------------------------------------------------------

func TestGoRules(t *testing.T) {
	iface := ifaceEnv("io.Writer")
	runLangCases(t, "go", []langCase{
		{"exact arity fits", fnOf("go", "F", p("a", "int"), p("b", "int")), arity(2), Env{}, Match},
		{"one argument short", fnOf("go", "F", p("a", "int"), p("b", "int")), arity(1), Env{}, Mismatch},
		{"one argument too many", fnOf("go", "F", p("a", "int")), arity(2), Env{}, Mismatch},
		{"variadic absorbs extras",
			fnOf("go", "V", p("a", "int"), p("r", "...string")), arity(5), Env{}, Match},
		{"variadic still needs its fixed part",
			fnOf("go", "V", p("a", "int"), p("r", "...string")), arity(0), Env{}, Mismatch},

		// Generics: a type parameter accepts whatever the instantiation says, so comparing
		// an argument against the letter T would be comparing against a placeholder.
		{"type parameter is never judged",
			fnOf("go", "Map", p("x", "T")), args("string"), Env{}, Match},
		{"interface{} accepts anything",
			fnOf("go", "Any", p("x", "interface{}")), args("*os.File"), Env{}, Match},
		{"any accepts anything", fnOf("go", "Any", p("x", "any")), args("int"), Env{}, Match},

		// Interfaces: *os.File satisfies io.Writer, and their type text differs. Warning
		// here would fire on every interface parameter in a repository.
		{"interface parameter takes an implementer",
			fnOf("go", "W", Param{Name: "w", Typing: "Writer", TypingID: "io.Writer"}),
			args("*os.File"), iface, Match},

		{"concrete type mismatch is caught",
			fnOf("go", "S", p("s", "string")), args("int"), Env{}, Mismatch},
		{"untyped int fits a float parameter",
			fnOf("go", "F", p("f", "float64")), args(UntypedInt), Env{}, Match},
		// An unreadable argument removes only the TYPE dimension; arity still decides, and
		// an arity that fits is real evidence the call is fine.
		{"an unreadable argument leaves arity deciding",
			fnOf("go", "S", p("s", "string")), args("?"), Env{}, Match},
		{"an unreadable argument does not rescue a bad arity",
			fnOf("go", "S", p("s", "string"), p("t", "string")), args("?"), Env{}, Mismatch},
	})
}

// --- Rust -------------------------------------------------------------------

func TestRustRules(t *testing.T) {
	trait := ifaceEnv("crate::Draw")
	runLangCases(t, "rust", []langCase{
		// Rust is the strictest: no defaults, no overloads, no varargs, and self is
		// already excluded from the declared parameters by the parser.
		{"exact arity fits", fnOf("rust", "add", p("x", "i32"), p("y", "i32")), arity(2), Env{}, Match},
		{"too few", fnOf("rust", "add", p("x", "i32"), p("y", "i32")), arity(1), Env{}, Mismatch},
		{"too many", fnOf("rust", "add", p("x", "i32")), arity(2), Env{}, Mismatch},

		{"generic parameter is never judged",
			fnOf("rust", "g", p("x", "T")), args("i32"), Env{}, Match},
		{"impl Trait is never judged",
			fnOf("rust", "g", p("x", "impl Into<String>")), args("&str"), Env{}, Match},
		{"dyn Trait is never judged",
			fnOf("rust", "g", p("x", "&dyn Draw")), args("Circle"), Env{}, Match},
		{"trait parameter takes an implementer",
			fnOf("rust", "g", Param{Name: "d", Typing: "Draw", TypingID: "crate::Draw"}),
			args("Circle"), trait, Match},

		{"integer literal fits i32", fnOf("rust", "f", p("x", "i32")), args(UntypedInt), Env{}, Match},
		{"integer literal fits f64", fnOf("rust", "f", p("x", "f64")), args(UntypedInt), Env{}, Match},
		{"integer literal does not fit bool",
			fnOf("rust", "f", p("x", "bool")), args(UntypedInt), Env{}, Mismatch},
		// A string literal is &str; String needs a conversion, so treating them as
		// interchangeable would miss a real error.
		{"string literal fits &str", fnOf("rust", "f", p("s", "&str")), args(UntypedString), Env{}, Match},
		{"string literal does not fit String",
			fnOf("rust", "f", p("s", "String")), args(UntypedString), Env{}, Mismatch},
	})
}

// --- Java -------------------------------------------------------------------

func TestJavaRules(t *testing.T) {
	iface := ifaceEnv("com.x.Runner")
	dyn := func(n int) CallSite { return CallSite{N: n, Dyn: true} }
	runLangCases(t, "java", []langCase{
		{"exact arity fits", fnOf("java", "f", p("a", "int"), p("b", "String")), arity(2), Env{}, Match},
		{"too few", fnOf("java", "f", p("a", "int"), p("b", "String")), arity(1), Env{}, Mismatch},
		{"varargs absorbs extras",
			fnOf("java", "f", p("a", "int"), p("r", "String...")), arity(4), Env{}, Match},
		{"varargs with none passed",
			fnOf("java", "f", p("a", "int"), p("r", "String...")), arity(1), Env{}, Match},

		// The overload fallback connects a call to every same-named method when none has
		// the right arity, so a mismatch against those edges says nothing about the code.
		{"heuristic resolution never mismatches",
			fnOf("java", "f", p("a", "int"), p("b", "String")), dyn(1), Env{}, Unknown},
		{"a method reference has no argument list",
			fnOf("java", "f", p("a", "int")), CallSite{N: -1}, Env{}, Unknown},

		{"Object accepts anything", fnOf("java", "f", p("o", "Object")), args("String"), Env{}, Match},
		{"generic parameter is never judged",
			fnOf("java", "f", p("t", "T")), args("String"), Env{}, Match},
		{"interface parameter takes an implementer",
			fnOf("java", "f", Param{Name: "r", Typing: "Runner", TypingID: "com.x.Runner"}),
			args("MyTask"), iface, Match},

		{"int literal fits long", fnOf("java", "f", p("a", "long")), args(UntypedInt), Env{}, Match},
		{"int literal boxes to Integer", fnOf("java", "f", p("a", "Integer")), args(UntypedInt), Env{}, Match},
		{"null fits a reference type", fnOf("java", "f", p("s", "String")), args(UntypedNil), Env{}, Match},
		{"null does not fit a primitive",
			fnOf("java", "f", p("a", "int")), args(UntypedNil), Env{}, Mismatch},
	})
}

// --- Python -----------------------------------------------------------------

func opt(name, typing string) Param { return Param{Name: name, Typing: typing, Optional: true} }
func star(name string) Param        { return Param{Name: name, Optional: true, Variadic: true} }
func starstar(name string) Param {
	return Param{Name: name, Optional: true, Variadic: true, KeyOnly: true}
}
func kwonly(name string, req bool) Param {
	return Param{Name: name, KeyOnly: true, Optional: !req}
}

// kwCall passes n positional arguments plus the named keywords.
func kwCall(n int, names ...string) CallSite { return CallSite{N: n, Kwargs: names} }

func TestPythonRules(t *testing.T) {
	runLangCases(t, "python", []langCase{
		{"exact arity fits", fnOf("python", "add", p("x", "int"), p("y", "int")), arity(2), Env{}, Match},
		{"missing a required argument",
			fnOf("python", "add", p("x", "int"), p("y", "int")), arity(1), Env{}, Mismatch},
		{"too many arguments", fnOf("python", "add", p("x", "int")), arity(2), Env{}, Mismatch},

		// The escape hatches. Without these every call that legitimately omits a defaulted
		// parameter would warn -- which is most calls in most Python.
		{"a default may be omitted",
			fnOf("python", "add", p("x", "int"), opt("y", "int")), arity(1), Env{}, Match},
		{"a default may also be supplied",
			fnOf("python", "add", p("x", "int"), opt("y", "int")), arity(2), Env{}, Match},
		{"*args absorbs extras",
			fnOf("python", "f", p("x", "int"), star("args")), arity(5), Env{}, Match},
		{"**kwargs does not add a positional slot",
			fnOf("python", "f", p("x", "int"), starstar("kw")), arity(2), Env{}, Mismatch},

		// Keyword arguments, which no other language here can check.
		{"a keyword satisfies a required parameter",
			fnOf("python", "add", p("x", "int"), p("y", "int")), kwCall(1, "y"), Env{}, Match},
		{"a keyword naming no parameter is an error",
			fnOf("python", "add", p("x", "int"), p("y", "int")), kwCall(2, "z"), Env{}, Mismatch},
		{"**kwargs swallows any keyword",
			fnOf("python", "add", p("x", "int"), starstar("kw")), kwCall(1, "anything"), Env{}, Match},
		{"a required keyword-only parameter must be passed",
			fnOf("python", "f", p("x", "int"), kwonly("mode", true)), arity(1), Env{}, Mismatch},
		{"and is satisfied by name",
			fnOf("python", "f", p("x", "int"), kwonly("mode", true)), kwCall(1, "mode"), Env{}, Match},
		{"an optional keyword-only parameter may be omitted",
			fnOf("python", "f", p("x", "int"), kwonly("mode", false)), arity(1), Env{}, Match},

		// The receiver is declared but never passed.
		{"self is not counted",
			res("python", "m", domain.ResourceMethod, p("self", ""), p("a", "str")), arity(1), Env{}, Match},
		{"cls is not counted",
			res("python", "m", domain.ResourceMethod, p("cls", ""), p("a", "str")), arity(1), Env{}, Match},

		// A starred call supplies an unknown number under unknown names.
		{"a starred call cannot be judged",
			fnOf("python", "add", p("x", "int"), p("y", "int")),
			CallSite{N: 1, Variadic: true}, Env{}, Unknown},

		// Annotations, where present.
		{"annotated type mismatch is caught",
			fnOf("python", "f", p("s", "str")), args(UntypedInt), Env{}, Mismatch},
		{"int fits a float annotation (numeric tower)",
			fnOf("python", "f", p("x", "float")), args(UntypedInt), Env{}, Match},
		// These drop the TYPE dimension only. Arity still fits, so the call is judged fine
		// rather than left unjudged -- which is what lets a fixed call clear its warning.
		{"an unannotated parameter is not type-judged",
			fnOf("python", "f", p("x", "")), args(UntypedInt), Env{}, Match},
		{"Any is not type-judged", fnOf("python", "f", p("x", "Any")), args(UntypedInt), Env{}, Match},
		{"Optional is not type-judged",
			fnOf("python", "f", p("x", "Optional[int]")), args(UntypedString), Env{}, Match},
		{"a union is not type-judged",
			fnOf("python", "f", p("x", "int | str")), args(UntypedString), Env{}, Match},
	})
}

// --- TypeScript -------------------------------------------------------------

func TestTypeScriptRules(t *testing.T) {
	iface := ifaceEnv("m.Shape")
	rest := func(name string) Param { return Param{Name: "..." + name, Variadic: true} }
	runLangCases(t, "typescript", []langCase{
		{"exact arity fits", fnOf("typescript", "f", p("a", ""), p("b", "")), arity(2), Env{}, Match},
		{"too few", fnOf("typescript", "f", p("a", ""), p("b", "")), arity(1), Env{}, Mismatch},
		{"too many", fnOf("typescript", "f", p("a", "")), arity(2), Env{}, Mismatch},

		// Optional parameters and defaults may be omitted; without the markers these would
		// be false positives on ordinary TypeScript.
		{"an optional parameter may be omitted",
			fnOf("typescript", "f", p("a", ""), opt("b", "")), arity(1), Env{}, Match},
		{"a rest parameter absorbs extras",
			fnOf("typescript", "f", p("a", ""), rest("r")), arity(6), Env{}, Match},

		{"any is never judged", fnOf("typescript", "f", p("x", "any")), args("string"), Env{}, Match},
		{"a generic is never judged", fnOf("typescript", "f", p("x", "T")), args("string"), Env{}, Match},
		{"a union is never judged",
			fnOf("typescript", "f", p("x", "string|number")), args("boolean"), Env{}, Match},
		{"an interface parameter takes an implementer",
			fnOf("typescript", "f", Param{Name: "s", Typing: "Shape", TypingID: "m.Shape"}),
			args("Circle"), iface, Match},

		{"a spread call cannot be judged",
			fnOf("typescript", "f", p("a", ""), p("b", "")),
			CallSite{N: -1, Variadic: true}, Env{}, Unknown},
	})
}

// --- JavaScript -------------------------------------------------------------

// TestJavaScriptNeverJudges is a rule, not a gap. f(1) against function f(a, b) is legal
// JavaScript -- b is undefined -- and f(1,2,3) against function f(a) is legal too. There is
// no compiler to disagree with, so an arity warning would be a style opinion printed to an
// agent after every edit, on the language with the most files in most repositories.
func TestJavaScriptNeverJudges(t *testing.T) {
	callee := fnOf("javascript", "f", p("a", ""), p("b", ""))
	for _, site := range []CallSite{arity(0), arity(1), arity(2), arity(5), args("#int")} {
		if got, _ := For("javascript").Match(Env{}, callee, site); got != Unknown {
			t.Errorf("JavaScript must never judge arity; %d args gave %s", site.N, got)
		}
	}
}

// TestEveryRegisteredLanguageIsReachable guards the wiring: a matcher registered under a
// name no scanner produces would silently never run, and the whole feature would be a
// no-op for that language.
func TestEveryRegisteredLanguageIsReachable(t *testing.T) {
	// These are the exact domain.Resource.Language values the scanners write, confirmed by
	// scanning a fixture in each and reading the database.
	for _, lang := range []string{"go", "rust", "java", "python", "typescript", "javascript"} {
		if For(lang) == nil {
			t.Errorf("no matcher for %q, which a scanner does produce", lang)
		}
	}
	if For("cobol") != nil {
		t.Error("an unknown language must have no matcher, so it cannot start warning")
	}
}

// --- conformance: what the real world does to type text -----------------------
//
// Every case below is taken verbatim from two real Rust crates (vello, lalrpop), where the
// first version of this checker produced 39 warnings and every one was wrong. They are kept as
// tests rather than as a changelog entry because each is a different reason text comparison
// fails, and any of them coming back would put false warnings in front of an agent.

func TestSameTypeTextToleratesHowARealRepoWritesTypes(t *testing.T) {
	for _, c := range []struct {
		name, a, b string
		want       bool
	}{
		// vello: the impl fully qualifies, the trait imported it.
		{"qualified vs imported", "crate::peniko::Gradient", "Gradient", true},
		{"qualified vs imported, ref", "crate::schedule::LoadOp", "LoadOp", true},
		// vello: an associated type is a placeholder the impl is SUPPOSED to fill in.
		{"associated type", "u32", "Self::SourceValue", true},
		{"associated type, other side", "Self::Output", "DrawTag", true},
		// Still different where it counts: the reference markers are kept, so a borrow of one
		// type is not confused with a borrow of another.
		{"different types behind a ref", "&mut RenderContext", "&mut Scene", false},
		{"different types", "String", "u32", false},
		// A scanner that could not name a type must not be the reason anything is reported.
		{"unreadable", "", "Gradient", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := sameTypeText(c.a, c.b); got != c.want {
				t.Errorf("sameTypeText(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// Rust reports a missing method and nothing else.
//
// Deciding a Rust signature match needs `use` context, associated-type bindings, lifetimes,
// generics and per-impl identity -- none of which this package has. On vello and lalrpop the
// attempt produced 39 warnings, 0 correct. A warning printed to an agent after an edit that it
// cannot stand behind is worse than no warning: the channel gets skimmed past, and the real
// ones go with it.
func TestRustConformanceOnlyReportsAMissingMethod(t *testing.T) {
	req := Signature{Name: "draw", Input: []Param{{Name: "scale", Typing: "i32"}}}
	for _, provided := range []Signature{
		{Name: "draw", Input: []Param{{Name: "scale", Typing: "crate::x::Scale"}}},
		{Name: "draw", Input: []Param{{Name: "a", Typing: "i32"}, {Name: "b", Typing: "i32"}}},
		{Name: "draw"},
	} {
		if v, why := Satisfies("rust", req, provided); v == Mismatch {
			t.Errorf("rust must not report a signature mismatch, got: %s", why)
		}
	}
	// Java still does, because its ids encode the parameter list and a differing signature
	// there is a missing override rather than a loose one.
	if v, _ := Satisfies("java", req,
		Signature{Name: "draw", Input: []Param{{Name: "a", Typing: "int"}, {Name: "b", Typing: "int"}}}); v != Mismatch {
		t.Error("java must still catch an arity mismatch")
	}
}

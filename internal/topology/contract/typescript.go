package contract

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func init() {
	register(tsMatcher{lang: "typescript"})
	register(jsMatcher{})
}

// tsMatcher applies TypeScript's rules. Arity is binding, once optional parameters,
// defaults and rest parameters are accounted for -- all of which the parser now reports.
type tsMatcher struct{ lang string }

func (m tsMatcher) Language() string { return m.lang }

func (m tsMatcher) Match(env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	params := tsParams(callee)
	if len(params) == 0 && site.N <= 0 {
		return Unknown, ""
	}
	if site.N < 0 || site.Variadic {
		return Unknown, "" // a spread call supplies an unknown number of arguments
	}
	if a := PositionalArity(params); !a.Accepts(site.N) {
		return Mismatch, arityMessage(callee.Name, a, site.N)
	}
	if v, why := matchTypes(env, params, site, tsTypeOpaque, tsAccepts); v == Mismatch {
		return Mismatch, why
	}
	return Match, ""
}

// tsParams recovers the rest marker, which the parser spells in the name ("...rest").
func tsParams(callee domain.Resource) []Param {
	params := Params(callee)
	for i, p := range params {
		if strings.HasPrefix(p.Name, "...") {
			params[i].Variadic = true
			params[i].Name = strings.TrimPrefix(p.Name, "...")
		}
	}
	return params
}

// tsTypeOpaque declines every position the annotation does not pin down.
//
// This declines a great deal, and deliberately so: the scanner fills Typing only for types
// that resolve to a topology resource, so a primitive annotation is indistinguishable from
// no annotation at all. Treating an empty Typing as a type would compare a real argument
// against nothing. Arity carries TypeScript's value until the parser records primitive
// annotations too.
func tsTypeOpaque(env Env, p Param) bool {
	t := strings.TrimSpace(p.Typing)
	switch t {
	case "", "any", "unknown", "object":
		return true
	}
	if isLikelyTypeParam(t) {
		return true
	}
	// A union accepts several types and an interface accepts any implementer; comparing
	// written type text against either would report correct code as wrong.
	if strings.ContainsAny(t, "|&<") {
		return true
	}
	return env.kindOf(p.TypingID) == domain.ResourceInterface
}

func tsAccepts(env Env, p Param, actual string) bool {
	declared := strings.TrimSpace(p.Typing)
	if !IsUntyped(actual) {
		return declared == strings.TrimSpace(actual)
	}
	switch actual {
	case UntypedInt, UntypedFloat:
		return declared == "number" || declared == "bigint"
	case UntypedString:
		return declared == "string"
	case UntypedBool:
		return declared == "boolean"
	case UntypedNil:
		return declared == "null" || declared == "undefined"
	case UntypedRune:
		return declared == "string"
	}
	return false
}

// jsMatcher answers Unknown for every call, always.
//
// Not an omission -- a finding. JavaScript arity is not binding: f(1) against
// function f(a, b) is legal and leaves b undefined, and f(1,2,3) against function f(a) is
// legal too. There is no compiler to disagree with and no runtime error to predict, so any
// arity rule here would be a style opinion dressed up as a correctness warning. Since these
// warnings are printed to an agent after every edit, that is precisely the false positive
// this whole mechanism exists to remove, and it would land hardest on the language with the
// most files in most repositories.
//
// JavaScript therefore keeps the older heuristic -- the callee went back to the signature
// its callers were written against -- which is weaker but never claims more than it knows.
// The records are still written, so a file that gains annotations, or a .d.ts, starts being
// judged with no rescan.
type jsMatcher struct{}

func (jsMatcher) Language() string { return "javascript" }

func (jsMatcher) Match(Env, domain.Resource, CallSite) (Verdict, string) {
	return Unknown, ""
}

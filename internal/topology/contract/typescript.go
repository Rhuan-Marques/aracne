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
	if site.N < 0 || site.Variadic {
		return Unknown, "" // a spread call supplies an unknown number of arguments
	}
	// A call has to fit ONE of the signatures the callee declares, so a mismatch is only a
	// mismatch when every one of them says so.
	var why string
	for _, params := range tsSignatures(callee) {
		v, w := tsMatchOne(env, callee.Name, params, site)
		if v != Mismatch {
			return v, ""
		}
		if why == "" {
			why = w // the first signature's complaint, which is the one in source order
		}
	}
	return Mismatch, why
}

// tsSignatures lists the signatures a caller may be written against, in source order.
//
// For an overload set that is the overloads and ONLY the overloads: TypeScript does not let
// anyone call the implementation signature, and rejects a call that fits it while fitting no
// overload -- the usual `function convert(x: any)` under two typed overloads accepts
// everything, so admitting it here would answer Match for every call in the set.
func tsSignatures(callee domain.Resource) [][]Param {
	if over := tsOverloadParams(callee); len(over) > 0 {
		return over
	}
	return [][]Param{tsParams(callee)}
}

func tsMatchOne(env Env, name string, params []Param, site CallSite) (Verdict, string) {
	if len(params) == 0 && site.N <= 0 {
		return Unknown, ""
	}
	if a := PositionalArity(params); !a.Accepts(site.N) {
		return Mismatch, arityMessage(name, a, site.N)
	}
	if v, why := matchTypes(env, params, site, tsTypeOpaque, tsAccepts); v == Mismatch {
		return Mismatch, why
	}
	return Match, ""
}

// tsOverloadParams is tsParams for each declared overload signature.
func tsOverloadParams(callee domain.Resource) [][]Param {
	over := OverloadParams(callee)
	for i := range over {
		over[i] = tsRestMarkers(over[i])
	}
	return over
}

// tsParams recovers the rest marker, which the parser spells in the name ("...rest").
func tsParams(callee domain.Resource) []Param {
	return tsRestMarkers(Params(callee))
}

func tsRestMarkers(params []Param) []Param {
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
	// `Object` is the wrapper type: every non-null value is assignable to it.
	case "", "any", "unknown", "object", "Object":
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
	switch env.kindOf(p.TypingID) {
	case domain.ResourceInterface:
		return true
	case domain.ResourceNamedType:
		// A type alias or an enum. What it accepts is written in its right-hand side,
		// which this package does not parse: `enum Kind {A}` takes the number 0, and
		// `type Id = string | number` takes both -- neither argument's text ever equals
		// the name the parameter is declared with, so comparing them warns on correct
		// code. numEnum(0) was exactly that false positive, and being a call that fits,
		// no edit to the caller could ever retire the warning it raised.
		return true
	}
	return false
}

func tsAccepts(env Env, p Param, actual string) bool {
	declared := strings.TrimSpace(p.Typing)
	if !IsUntyped(actual) {
		return declared == strings.TrimSpace(actual)
	}
	// Number, String and Boolean are the wrapper types of the primitives, and a primitive
	// is assignable to its wrapper -- only the reverse is the error TypeScript reports.
	switch actual {
	case UntypedInt, UntypedFloat:
		return declared == "number" || declared == "bigint" || declared == "Number"
	case UntypedString:
		return declared == "string" || declared == "String"
	case UntypedBool:
		return declared == "boolean" || declared == "Boolean"
	case UntypedNil:
		// An optional parameter IS `T | undefined`, so passing `undefined` explicitly is
		// the thing it was declared for. null and undefined share one literal class --
		// both spell "absent" -- so the optional case takes either rather than guessing
		// which one the caller wrote and warning about a call that compiles.
		return p.Optional || declared == "null" || declared == "undefined"
	case UntypedRune:
		return declared == "string" || declared == "String"
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

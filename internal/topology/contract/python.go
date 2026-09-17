package contract

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func init() { register(pythonMatcher{}) }

// pythonMatcher applies Python's rules.
//
// Python is strict about arity, which is easy to get wrong from a distance: passing the
// wrong number of arguments raises TypeError at the call, exactly like a compiled language
// rejecting it, and unlike JavaScript where the missing parameter is merely undefined. What
// makes it fiddly is the number of ways a parameter may legitimately be omitted -- defaults,
// *args, **kwargs, keyword-only -- all of which the parser now reports, and one way an
// argument may arrive out of position: by keyword.
type pythonMatcher struct{}

func (pythonMatcher) Language() string { return "python" }

func (m pythonMatcher) Match(env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	params := Params(callee)
	if len(params) == 0 && site.N <= 0 {
		return Unknown, ""
	}
	// The receiver is declared but never passed -- and it is the SCANNER that drops it,
	// because only the scanner can tell a receiver from a real parameter. It knows the
	// position (a receiver is always first) and the decorators (a @staticmethod has none).
	// A name test here could not: it ate the second parameter of `register(self, cls)` and
	// the first of a staticmethod, hiding calls the interpreter rejects.

	// A starred call -- f(*xs) or f(**kw) -- supplies an unknown number of arguments under
	// unknown names, so neither the count nor the keywords can be judged.
	if site.N < 0 || site.Variadic {
		return Unknown, ""
	}

	byName := make(map[string]Param, len(params))
	acceptsExtraKwargs := false
	for _, p := range params {
		if p.Variadic && p.KeyOnly {
			acceptsExtraKwargs = true // **kwargs swallows any name
			continue
		}
		if p.Variadic {
			continue
		}
		byName[p.Name] = p
	}

	// A keyword naming no parameter is a TypeError, and nothing else here can catch it.
	if !acceptsExtraKwargs {
		for _, kw := range site.Kwargs {
			if _, ok := byName[kw]; !ok {
				return Mismatch, callee.Name + " has no parameter named " + kw +
					", but this call passes it by keyword"
			}
		}
	}

	a := PositionalArity(params)
	if a.Max >= 0 && site.N > a.Max {
		return Mismatch, arityMessage(callee.Name, a, site.N)
	}
	// Keywords can satisfy required parameters the positional arguments did not reach, so
	// the minimum is only violated when neither route covers them.
	if missing := requiredNotSupplied(params, site); missing != "" {
		return Mismatch, callee.Name + " requires " + missing +
			", which this call does not pass"
	}

	switch v, why := matchTypes(env, params, site, pythonTypeOpaque, pythonAccepts); v {
	case Mismatch:
		return Mismatch, why
	case Unverified:
		return Unverified, why
	}
	return Match, ""
}

// requiredNotSupplied names the first required parameter no argument reaches, positionally
// or by keyword, or "" when every one is covered.
func requiredNotSupplied(params []Param, site CallSite) string {
	given := make(map[string]bool, len(site.Kwargs))
	for _, kw := range site.Kwargs {
		given[kw] = true
	}
	pos := 0
	for _, p := range params {
		if p.Variadic {
			continue
		}
		covered := given[p.Name]
		if !p.KeyOnly {
			if pos < site.N {
				covered = true
			}
			pos++
		}
		if !covered && !p.Optional {
			return p.Name
		}
	}
	return ""
}

// pythonScalarBuiltins are the only annotations a recorded argument can be compared with.
//
// A call site records a literal as the CLASS of that literal -- #int, #float, #string,
// #bool, #nil -- and nothing else, so the comparison is only meaningful against a builtin
// scalar. Every other annotation needs real type resolution to judge: an alias or a NewType
// stands for something else, a generic or a union admits several things, a Protocol admits
// anything shaped right, and a project class may define __init__ conversions. Guessing at
// any of them produces a warning about correct code, which is the failure this package
// exists to avoid.
var pythonScalarBuiltins = map[string]bool{
	"int": true, "float": true, "complex": true,
	"str": true, "bool": true, "bytes": true,
	"None": true, "NoneType": true,
}

// pythonTypeOpaque declines any position the annotation does not pin down to a builtin
// scalar. An unannotated parameter says nothing at all, and Any is an explicit statement
// that it says nothing.
func pythonTypeOpaque(env Env, p Param) bool {
	return !pythonScalarBuiltins[strings.TrimSpace(p.Typing)]
}

// pythonAccepts compares a literal against an annotation. Only literals reach here.
func pythonAccepts(env Env, p Param, actual string) bool {
	declared := strings.TrimSpace(p.Typing)
	if !IsUntyped(actual) {
		return declared == strings.TrimSpace(actual)
	}
	switch actual {
	case UntypedInt:
		// bool is a subclass of int, and an int is accepted where a float is annotated
		// (PEP 484's numeric tower), so both are legal and must not be reported.
		return declared == "int" || declared == "float" || declared == "complex"
	case UntypedFloat:
		return declared == "float" || declared == "complex"
	case UntypedString:
		return declared == "str"
	case UntypedBool:
		return declared == "bool" || declared == "int"
	case UntypedNil:
		// None fits only an Optional or a None annotation, and pythonTypeOpaque has
		// already declined every Optional -- so reaching here with a concrete annotation
		// means None really does not fit.
		return declared == "None" || declared == "NoneType"
	}
	return false
}

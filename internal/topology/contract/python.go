package contract

import (
	"strings"

	"aracne/internal/topology/domain"
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
	// The receiver is declared but never passed. Getting this wrong shifts every method
	// call in the repository by one, so it is checked by name rather than by position:
	// a module-level function whose first parameter happens to be called self is not a
	// method, and a method always declares one.
	if isMethodResource(callee) && len(params) > 0 && isReceiverName(params[0].Name) {
		params = params[1:]
	}

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

	if v, why := matchTypes(env, params, site, pythonTypeOpaque, pythonAccepts); v == Mismatch {
		return Mismatch, why
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

// isMethodResource reports whether a callee is declared inside a class.
func isMethodResource(res domain.Resource) bool {
	if res.Kind == domain.ResourceMethod {
		return true
	}
	if res.Properties == nil {
		return false
	}
	if from, ok := res.Properties["method_from"].(string); ok && from != "" {
		return true
	}
	return false
}

func isReceiverName(name string) bool { return name == "self" || name == "cls" }

// pythonTypeOpaque declines any position the annotation does not pin down. An unannotated
// parameter says nothing at all, and Any is an explicit statement that it says nothing.
func pythonTypeOpaque(env Env, p Param) bool {
	t := strings.TrimSpace(p.Typing)
	switch t {
	case "", "Any", "typing.Any", "object":
		return true
	}
	if isLikelyTypeParam(t) {
		return true
	}
	// Optional[X] and X | None accept None as well as X; unions accept several types.
	// Judging those needs real type resolution, so decline rather than guess.
	if strings.ContainsAny(t, "|[") || strings.HasPrefix(t, "Optional") ||
		strings.HasPrefix(t, "Union") {
		return true
	}
	return env.kindOf(p.TypingID) == domain.ResourceInterface
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

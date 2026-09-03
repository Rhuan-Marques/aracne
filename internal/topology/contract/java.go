package contract

import (
	"strings"

	"aracne/internal/topology/domain"
)

func init() { register(javaMatcher{}) }

// javaMatcher applies Java's rules.
//
// Java is the odd one here, and gains the least. Its method IDs encode the parameter list
// ("com.x.C.m(int,int)"), so changing a parameter is an IDENTITY change rather than a
// signature change -- the old method appears deleted and a new one appears. What contract
// matching still buys is the varargs case and, more importantly, not warning on the
// overload fallback: when no overload of the right arity exists, the resolver connects the
// call to every same-named one, and checking arity against those would report a mismatch
// on each. That is what CallSite.Dyn suppresses.
type javaMatcher struct{}

func (javaMatcher) Language() string { return "java" }

func (m javaMatcher) Match(env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	if site.N < 0 {
		return Unknown, "" // a method reference; the call has no argument list of its own
	}
	params := javaParams(callee)
	a := PositionalArity(params)
	if !a.Accepts(site.N) {
		if site.Dyn {
			// The edge itself is a guess about which overload is meant, so a mismatch
			// against it says nothing about the code.
			return Unknown, ""
		}
		return Mismatch, arityMessage(callee.Name, a, site.N)
	}
	if v, why := matchTypes(env, params, site, javaTypeOpaque, javaAccepts); v == Mismatch && !site.Dyn {
		return Mismatch, why
	}
	return Match, ""
}

// javaParams recovers the varargs marker from the declared type text: the parser spells a
// varargs parameter "T..." (and the method ID normalises it to "T[]").
func javaParams(callee domain.Resource) []Param {
	params := Params(callee)
	for i, p := range params {
		if strings.HasSuffix(p.Typing, "...") {
			params[i].Variadic = true
			params[i].Typing = strings.TrimSuffix(p.Typing, "...")
		}
	}
	return params
}

func javaTypeOpaque(env Env, p Param) bool {
	t := strings.TrimSpace(p.Typing)
	switch t {
	case "", "Object", "var":
		return true
	}
	if isLikelyTypeParam(t) {
		return true
	}
	// An interface parameter takes any implementer, whose type text will not equal it.
	return env.kindOf(p.TypingID) == domain.ResourceInterface
}

// javaAccepts compares a literal against a declared type, allowing the widening
// conversions and the boxing that the language performs silently.
func javaAccepts(env Env, p Param, actual string) bool {
	declared := strings.TrimSpace(p.Typing)
	if !IsUntyped(actual) {
		return declared == strings.TrimSpace(actual)
	}
	switch actual {
	case UntypedInt, UntypedRune:
		return javaNumeric[declared] || declared == "Integer" || declared == "Long" ||
			declared == "Short" || declared == "Byte" || declared == "Character" ||
			declared == "Double" || declared == "Float"
	case UntypedFloat:
		return declared == "double" || declared == "float" ||
			declared == "Double" || declared == "Float"
	case UntypedString:
		return declared == "String" || declared == "CharSequence"
	case UntypedBool:
		return declared == "boolean" || declared == "Boolean"
	case UntypedNil:
		// null fits any reference type. A lowercase name is a primitive and cannot take it;
		// everything else can, and distinguishing further would need real type resolution.
		return declared != "" && !javaPrimitive[declared]
	}
	return false
}

var javaNumeric = map[string]bool{
	"int": true, "long": true, "short": true, "byte": true,
	"char": true, "float": true, "double": true,
}

var javaPrimitive = map[string]bool{
	"int": true, "long": true, "short": true, "byte": true, "char": true,
	"float": true, "double": true, "boolean": true, "void": true,
}

package contract

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func init() { register(goMatcher{}) }

// goMatcher applies Go's rules: arity is binding, and so are argument types wherever both
// sides are known concretely.
type goMatcher struct{}

func (goMatcher) Language() string { return "go" }

func (m goMatcher) Match(env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	params := goParams(callee)
	if params == nil && site.N <= 0 {
		return Unknown, ""
	}

	// A spread call -- f(xs...) -- passes however many elements xs holds, which is not
	// knowable from the syntax. The fixed arguments before the spread still have to fit,
	// but the count cannot be judged, so arity is skipped rather than guessed.
	if !site.Variadic && site.N >= 0 {
		if a := PositionalArity(params); !a.Accepts(site.N) {
			return Mismatch, arityMessage(callee.Name, a, site.N)
		}
	}

	if v, why := matchTypes(env, params, site, goTypeOpaque, goAccepts); v == Mismatch {
		return Mismatch, why
	}
	return Match, ""
}

// goParams reads the declared parameters and recovers the variadic marker from the type
// text. goscanner spells a variadic parameter "...T" in Typing (verified against a scanned
// fixture), so Go needs no scanner change to answer arity.
func goParams(callee domain.Resource) []Param {
	params := Params(callee)
	for i, p := range params {
		if strings.HasPrefix(p.Typing, "...") {
			params[i].Variadic = true
			params[i].Typing = strings.TrimPrefix(p.Typing, "...")
		}
	}
	return params
}

// goTypeOpaque reports whether a declared parameter type accepts values whose type text
// will not equal it, so comparing the two would be wrong rather than merely unhelpful.
//
// Interfaces are the important case and the reason Env exists at all: passing *os.File to
// an io.Writer parameter is correct code with mismatched type text. Deciding whether a
// concrete type satisfies an interface is real type checking, which this package does not
// do and should not pretend to.
func goTypeOpaque(env Env, p Param) bool {
	switch strings.TrimSpace(p.Typing) {
	case "", "any", "interface{}", "error":
		return true
	}
	if env.kindOf(p.TypingID) == domain.ResourceInterface {
		return true
	}
	// A single upper-case letter, or a short all-caps word, is overwhelmingly a type
	// parameter rather than a named type: `func Map[T any](xs []T)`. Judging an argument
	// against T would compare a concrete type to a placeholder.
	if isLikelyTypeParam(p.Typing) {
		return true
	}
	return false
}

func isLikelyTypeParam(t string) bool {
	if len(t) == 0 || len(t) > 2 {
		return false
	}
	for _, r := range t {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// goAccepts reports whether a parameter of type `declared` accepts an argument recorded as
// `actual`.
//
// Assignability, not equality. An untyped constant is the reason: Go's `5` is an untyped
// integer constant and is legal wherever any numeric type is wanted, so `f(5)` against
// `f(x float64)` must be a Match. Comparing type text would call that a mismatch and warn
// on a large fraction of every Go repository -- a worse failure than the bug this whole
// mechanism exists to fix. Named types are followed to their underlying type for the same
// reason: `type Celsius float64` takes `5` too.
func goAccepts(env Env, p Param, actual string) bool {
	d, a := normalizeGoType(p.Typing), normalizeGoType(actual)
	if !IsUntyped(a) {
		return d == a
	}
	return goAcceptsUntyped(env, p, d, a)
}

func goAcceptsUntyped(env Env, p Param, declared, token string) bool {
	// Follow a named type to what it is built on -- `type Celsius float64` takes 5 -- using
	// the id the scanner already resolved. An unresolvable name is judged on its own text,
	// which simply will not match, so the position yields no verdict rather than a wrong one.
	target := declared
	if u := env.underlyingOf(p.TypingID); u != "" {
		target = normalizeGoType(u)
	}
	switch token {
	case UntypedInt, UntypedRune:
		// A rune literal is an untyped rune constant, assignable to every integer type.
		return goNumeric[target]
	case UntypedFloat:
		return goFloat[target]
	case UntypedString:
		return target == "string"
	case UntypedBool:
		return target == "bool"
	case UntypedNil:
		// nil is assignable to exactly the nilable kinds. Composite type text is enough to
		// recognise all of them without resolving anything.
		return strings.HasPrefix(target, "*") || strings.HasPrefix(target, "[]") ||
			strings.HasPrefix(target, "map[") || strings.HasPrefix(target, "chan") ||
			strings.HasPrefix(target, "func(") || target == "any" || target == "interface{}"
	}
	return false
}

var goNumeric = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "byte": true, "rune": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
}

var goFloat = map[string]bool{
	"float32": true, "float64": true, "complex64": true, "complex128": true,
}

func normalizeGoType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.ReplaceAll(t, " ", "")
	return t
}

// matchTypes walks positions common to both sides, skipping anything either side declines
// to judge. Shared by every language; only the two predicates differ.
func matchTypes(
	env Env,
	params []Param,
	site CallSite,
	opaque func(Env, Param) bool,
	accepts func(Env, Param, string) bool,
) (Verdict, string) {
	if len(site.Types) == 0 {
		return Unknown, ""
	}
	judged := false
	for i, param := range positionalParams(params) {
		if i >= len(site.Types) {
			break
		}
		actual := site.Types[i]
		if actual == nil || *actual == "" {
			continue // the call site could not say; that position carries no information
		}
		if opaque(env, param) {
			continue
		}
		// A variadic parameter absorbs every remaining argument, all of which must be the
		// element type. Comparing only position i would miss the rest.
		if param.Variadic {
			for j := i; j < len(site.Types); j++ {
				a := site.Types[j]
				if a == nil || *a == "" {
					continue
				}
				judged = true
				if !accepts(env, param, *a) {
					return Mismatch, typeMessage(param, j, *a)
				}
			}
			break
		}
		judged = true
		if !accepts(env, param, *actual) {
			return Mismatch, typeMessage(param, i, *actual)
		}
	}
	if !judged {
		return Unknown, ""
	}
	return Match, ""
}

// positionalParams drops keyword-only parameters, which never line up with an argument
// index.
func positionalParams(params []Param) []Param {
	out := make([]Param, 0, len(params))
	for _, p := range params {
		if p.KeyOnly {
			continue
		}
		out = append(out, p)
	}
	return out
}

func arityMessage(name string, a Arity, got int) string {
	var want string
	switch {
	case a.Max < 0:
		want = fmt.Sprintf("at least %d", a.Min)
	case a.Min == a.Max:
		want = fmt.Sprintf("%d", a.Min)
	default:
		want = fmt.Sprintf("%d to %d", a.Min, a.Max)
	}
	return fmt.Sprintf("%s takes %s argument(s), this call passes %d", name, want, got)
}

func typeMessage(p Param, pos int, actual string) string {
	label := p.Name
	if label == "" {
		label = fmt.Sprintf("argument %d", pos+1)
	}
	return fmt.Sprintf("%s is declared %s, this call passes %s", label, p.Typing, actual)
}

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
	// No `params == nil` escape here. A Go function always has a parameter list, and "none"
	// arrives in two shapes: an empty list from a fresh parse, and no "input" key at all from
	// the same resource read back from SQLite. Treating the second as "cannot say" made
	// f() against a callee reduced to zero parameters a Match on the full path and Unknown on
	// the partial one -- so the warning an agent cleared by fixing the call stayed standing.
	params := goParams(callee)

	// A spread call -- f(xs...) -- passes however many elements xs holds, which is not
	// knowable from the syntax. The fixed arguments before the spread still have to fit,
	// but the count cannot be judged, so arity is skipped rather than guessed.
	if !site.Variadic && site.N >= 0 {
		if a := PositionalArity(params); !a.Accepts(site.N) {
			return Mismatch, arityMessage(callee.Name, a, site.N)
		}
	}

	switch v, why := matchTypes(env, params, site, goTypeOpaque, goAccepts); v {
	case Mismatch:
		return Mismatch, why
	case Unverified:
		return Unverified, why
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
		// SameGoTypeText, not ==: a call site recorded by an older build spells a forwarded
		// func- or struct-typed parameter with that build's placeholder, and judging it
		// against the full rendering would warn about a call that never changed.
		return SameGoTypeText(d, a)
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
//
// THREE OUTCOMES, NOT TWO, and the third is the point. "Every position I judged fits" and
// "I judged nothing, because the call site never recorded what it passes" used to both
// leave here as Match -- and Match is what withdraws a warning, so the most common breaking
// edit in a typed language reported nothing at all. A position that MATTERS and cannot be
// read now returns Unverified, which the caller reports and does not store.
//
// Mattering is decided by env.OldParams: a position whose declared type did not move cannot
// have broken a caller that already fit, so it is skipped before anything else.
func matchTypes(
	env Env,
	params []Param,
	site CallSite,
	opaque func(Env, Param) bool,
	accepts func(Env, Param, string) bool,
) (Verdict, string) {
	judged := false
	unverified := ""
	// UNVERIFIED NEEDS EVIDENCE, and the baseline is the only thing that carries it. Without
	// OldParams nothing here knows which positions the edit moved, so calling an unreadable
	// argument "unverified" would be doubt manufactured from nothing -- every arity-only call
	// in a language that records only literals would raise one, forever, on every edit.
	// No baseline therefore means the older behaviour exactly: skip what cannot be read.
	note := func(i int, p Param) {
		if env.OldParams != nil && unverified == "" {
			unverified = unverifiedMessage(p, i)
		}
	}
	for i, param := range positionalParams(params) {
		if env.unchangedAt(i, param) {
			continue
		}
		// OPACITY IS ASKED FIRST, and the order is the whole of `string -> any`. The
		// declared type here accepts values whose text will not equal it -- `any`, an
		// interface, a type parameter -- so what the call passes cannot contradict it and
		// does not need reading. Asked after the two checks below, as it used to be when
		// all three merely skipped, a widened parameter reported doubt about every caller
		// whose argument was unreadable: the exact opposite of the intended answer.
		if opaque(env, param) {
			continue
		}
		if i >= len(site.Types) {
			// The call site recorded fewer positions than the callee declares -- an older
			// record, or one that could name nothing at all. Arity has already had its say;
			// what this position now holds is exactly what cannot be known.
			note(i, param)
			continue
		}
		actual := site.Types[i]
		if actual == nil || *actual == "" {
			// The call site could not say. That is not "no information" when the position
			// is one the edit moved -- it is the information being missing.
			note(i, param)
			continue
		}
		// A variadic parameter absorbs every remaining argument, all of which must be the
		// element type. Comparing only position i would miss the rest.
		if param.Variadic {
			for j := i; j < len(site.Types); j++ {
				a := site.Types[j]
				if a == nil || *a == "" {
					note(j, param)
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
	// Order matters: an unreadable position is only news once nothing outright contradicted
	// the signature, and "nothing was judged AND nothing needed judging" is the honest
	// Unknown the older heuristic falls back on.
	if unverified != "" {
		return Unverified, unverified
	}
	if !judged {
		return Unknown, ""
	}
	return Match, ""
}

// unchangedAt reports whether position i holds the same declared type the caller was
// written against, so nothing about it can have broken.
//
// A nil OldParams judges everything -- that is the honest reading of "no baseline", and it
// costs extra Unverified reports rather than missed ones. A position past the end of the
// old list is new, and therefore changed.
func (e Env) unchangedAt(i int, param Param) bool {
	if e.OldParams == nil {
		return false
	}
	old := positionalParams(e.OldParams)
	if i >= len(old) {
		return false
	}
	return normalizeGoType(old[i].Typing) == normalizeGoType(param.Typing)
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

// unverifiedMessage explains a position the rules could not judge: the parameter moved and
// the call site never recorded what it passes there.
//
// It names the type the argument now has to satisfy, because that is the question the reader
// has to answer by eye -- the one thing the graph cannot answer for them.
func unverifiedMessage(p Param, pos int) string {
	label := p.Name
	if label == "" {
		label = fmt.Sprintf("argument %d", pos+1)
	}
	if strings.TrimSpace(p.Typing) == "" {
		return fmt.Sprintf("%s changed type and this call's argument could not be read", label)
	}
	return fmt.Sprintf("%s is now %s and this call's argument could not be read", label, p.Typing)
}

// SameGoTypeText reports whether two rendered Go type texts name the same type.
//
// It exists for ONE reason: aracne used to render a type whose shape it could not write out
// as a placeholder -- every func type as "func(...)", every struct literal as "struct{...}",
// every interface literal as "interface{}", and a directional channel as the bidirectional
// "chan T". Those strings are in every database written by an older build, in stored
// signatures and in stored call sites alike, and they are compared against the full rendering
// a current scan produces. A plain string compare would call every one of them a change, so
// the first scan after the upgrade would warn about every function that takes a callback.
//
// A placeholder cannot say whether the shape changed, so it does not get to claim one did:
// when one side is a string the collapse leaves alone -- which is what an older build wrote --
// and the other collapses to exactly it, the two are treated as the same type. Rich text on
// both sides is compared literally, which is the whole point of rendering it.
//
// The cost is two forms that are their own collapse and so stay ambiguous for good: a
// bidirectional `chan T` gaining a direction, and a bare `interface{}` gaining a method. Both
// were entirely invisible before, so neither is a regression.
func SameGoTypeText(a, b string) bool {
	if a == b {
		return true
	}
	ca, cb := collapseGoShapes(a), collapseGoShapes(b)
	return (ca == a || cb == b) && ca == cb
}

// collapseGoShapes rewrites a type text the way the older renderer would have written it:
// func types, struct literals and interface literals become their placeholders, and a
// channel loses its direction. Anything else is copied through.
func collapseGoShapes(t string) string {
	var out strings.Builder
	for i := 0; i < len(t); {
		switch {
		case strings.HasPrefix(t[i:], "<-chan "), strings.HasPrefix(t[i:], "chan<- "):
			out.WriteString("chan ")
			i += len("<-chan ")
		case strings.HasPrefix(t[i:], "struct{"):
			out.WriteString("struct{...}")
			i = closeBrace(t, i+len("struct{"))
		case strings.HasPrefix(t[i:], "interface{"):
			out.WriteString("interface{}")
			i = closeBrace(t, i+len("interface{"))
		case strings.HasPrefix(t[i:], "func("):
			out.WriteString("func(...)")
			i = endOfFuncType(t, i+len("func"))
		default:
			out.WriteByte(t[i])
			i++
		}
	}
	return out.String()
}

// closeBrace returns the index just past the '}' that closes the brace opened before `i`.
func closeBrace(t string, i int) int {
	depth := 1
	for ; i < len(t); i++ {
		switch t[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(t)
}

// endOfFuncType returns the index just past a func type whose parameter list opens at `i`,
// results included. A single unnamed result is written bare, so it ends where the enclosing
// list does: at a top-level separator or closer.
func endOfFuncType(t string, i int) int {
	i = closeParen(t, i+1)
	if i >= len(t) || t[i] != ' ' {
		return i
	}
	if i+1 < len(t) && t[i+1] == '(' {
		return closeParen(t, i+2)
	}
	depth := 0
	for j := i + 1; j < len(t); j++ {
		switch t[j] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return j
			}
			depth--
		case ',', ';':
			if depth == 0 {
				return j
			}
		}
	}
	return len(t)
}

// closeParen returns the index just past the ')' that closes the paren opened before `i`.
func closeParen(t string, i int) int {
	depth := 1
	for ; i < len(t); i++ {
		switch t[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(t)
}

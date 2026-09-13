package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Signature is a method's declared shape, from either a method resource or an interface's
// method list.
type Signature struct {
	Name       string
	Input      []Param
	Output     []Param
	HasDefault bool // the interface supplies a body, so an implementer need not
}

// InterfaceMethods reads the methods an interface requires.
//
// Rust traits, TypeScript interfaces and Java interfaces all publish them the same way, in
// Properties["methods"], which is why one reader serves all three. Python is the exception
// and is handled by the caller: an ABC is an ordinary class whose requirements are its
// own methods carrying the abstractmethod decorator, not a published list.
func InterfaceMethods(iface domain.Resource) []Signature {
	raw, ok := iface.Properties["methods"]
	if !ok || raw == nil {
		return nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var wire []struct {
		Name       string      `json:"Name"`
		Input      []paramWire `json:"Input"`
		Output     []paramWire `json:"Output"`
		HasDefault bool        `json:"HasDefault"`
		// TypeScript's optional method (`maybe?(): void`): like a default, nothing an
		// implementer has to provide.
		Optional bool `json:"Optional"`
		// Java's `static` interface method (`static <T> Repo<T> empty() {...}`, and the
		// private static helper beside it). It is called on the interface itself, is never
		// inherited and can never be overridden, so it is not a requirement at all --
		// requiring it put a conflict on every implementer the moment someone added a
		// static factory. Only Java publishes this flag; Rust's trait summaries and
		// TypeScript's interface members have no field of the name, so it stays false
		// there and an associated function in a Rust trait is still required.
		IsStatic bool `json:"IsStatic"`
	}
	if err := json.Unmarshal(blob, &wire); err != nil {
		return nil
	}
	out := make([]Signature, 0, len(wire))
	for _, w := range wire {
		if w.IsStatic {
			continue
		}
		out = append(out, Signature{
			Name:       w.Name,
			Input:      fromWire(w.Input),
			Output:     fromWire(w.Output),
			HasDefault: w.HasDefault || w.Optional,
		})
	}
	return out
}

// SignatureOf reads a method resource's own declared shape.
func SignatureOf(method domain.Resource) Signature {
	return Signature{
		Name:   method.Name,
		Input:  Params(method),
		Output: decodeParams(method.Properties["output"]),
	}
}

func fromWire(w []paramWire) []Param {
	out := make([]Param, 0, len(w))
	for _, p := range w {
		// paramWire is Param plus JSON tags, field for field: a conversion says that in one
		// place, and stops a new Param field from being silently dropped here.
		out = append(out, Param(p))
	}
	return out
}

// ChecksConformance reports whether a language's declared "implements" relationship is a
// CLAIM that can be wrong, and therefore worth verifying.
//
// Only where all three hold: conformance is written in the source, the edge survives a
// broken implementation, and the interface publishes its method signatures. Verified by
// scanning a deliberately broken fixture in each language and reading the database back.
//
// Go is excluded because its conformance is structural and its edge is DERIVED from
// satisfaction -- matchStructsToInterfaces only creates it when implements() already
// returns true, so a broken type simply has no edge and there is no claim to check. The
// same is true of Python's Protocol. Warning about those would mean guessing which types
// were "meant" to implement which interfaces, and a guess here is a false warning about
// correct code.
func ChecksConformance(lang string) bool {
	switch lang {
	case "rust", "java", "typescript", "python":
		return true
	}
	return false
}

// Satisfies reports whether `provided` can stand in for `required`.
//
// The languages disagree sharply about what counts, and getting this wrong in either
// direction is expensive, so each rule below is the language's actual rule rather than a
// shared approximation:
//
//   - Rust and Java demand an exact match. A differing signature is not a loose override,
//     it is a missing one: the trait impl does not compile, and the Java method is an
//     overload that leaves the interface method unimplemented.
//   - TypeScript accepts an implementation with FEWER parameters. That is ordinary,
//     idiomatic TypeScript -- a handler that ignores its second argument declares one
//     parameter -- so demanding equal arity would warn about correct code. Only an
//     implementation that requires MORE than the interface supplies is broken.
//   - Python enforces nothing at all: an ABC only requires that the name be defined, and
//     an override may take any arguments it likes. So a differing signature is legal and
//     is not reported here. The real breakage -- a caller passing what the override no
//     longer accepts -- is caught by the call-site check instead, which is the right
//     place for it.
func Satisfies(lang string, required, provided Signature) (Verdict, string) {
	return SatisfiesWithTypeParams(lang, required, provided, nil)
}

// ConformanceCtx is what the comparison needs from the graph it cannot see from two
// signatures alone.
//
// TypeParams are the interface's own type-parameter names; see SatisfiesWithTypeParams.
// Related answers whether two topology type ids stand in an inheritance relationship in
// EITHER direction -- this package has no resolver, so the caller, which holds the topology,
// supplies it. It may be nil, and then every relationship question answers "unknown".
type ConformanceCtx struct {
	TypeParams []string
	Related    func(aID, bID string) bool
}

func (c ConformanceCtx) related(a, b string) bool {
	if c.Related == nil || a == "" || b == "" {
		return false
	}
	return c.Related(a, b)
}

// SatisfiesWithTypeParams is Satisfies told which names in `required` are the interface's own
// TYPE PARAMETERS.
//
// An interface writes its methods against its parameters -- `void save(T item)` -- and an
// implementer writes them against the argument it chose -- `save(User item)` for
// `implements Repo<User>`. Comparing those as text made every implementer of a generic
// interface a mismatch, which is most of the generic code in Java and TypeScript. A position
// whose declared type mentions a type parameter therefore yields no verdict: substituting the
// implements clause's type arguments needs a resolver this package does not have, and the
// honest answer about `T` is that it says nothing about the concrete type. Everything a type
// parameter does not explain -- arity, and a concrete type in a non-generic position -- is
// still compared.
func SatisfiesWithTypeParams(lang string, required, provided Signature, typeParams []string) (Verdict, string) {
	return SatisfiesIn(lang, required, provided, ConformanceCtx{TypeParams: typeParams})
}

// SatisfiesIn is SatisfiesWithTypeParams given everything the graph knows: the interface's
// type parameters and, for the languages whose rule needs it, a way to ask whether two types
// are related by inheritance.
func SatisfiesIn(lang string, required, provided Signature, ctx ConformanceCtx) (Verdict, string) {
	typeParams := ctx.TypeParams
	switch lang {
	case "rust":
		// Rust reports a MISSING method and nothing else. Comparing its signatures as text
		// does not work, and the failure is not marginal: measured on two real crates it
		// produced 39 warnings and every one was wrong, in four distinct ways.
		//
		//   crate::peniko::Gradient  vs  Gradient       the same type, qualified or imported
		//   u32                      vs  Self::SourceValue   an associated type the impl is
		//                                                    SUPPOSED to concretize
		//   &mut RenderContext       vs  &mut Scene     and the exact reverse, for the same
		//                                               type -- two traits on one id
		//   7 parameters             vs  5              four impls of one name in one file
		//
		// Deciding any of those needs a resolver: `use` context, associated-type bindings,
		// lifetimes, generics, and per-impl identity. This package has none of them, and a
		// warning it cannot stand behind is worse than no warning -- it is printed to an
		// agent after an edit, and a channel that cries wolf gets skimmed past. What survives
		// is the claim names alone can support: the impl does not provide this method at all.
		return Match, ""
	case "java":
		return exactlySatisfies(required, provided, typeParams)
	case "typescript":
		return tsSatisfies(required, provided, ctx)
	case "python":
		// Reaching here means the name exists, which is all Python asks.
		return Match, ""
	}
	return Unknown, ""
}

func exactlySatisfies(required, provided Signature, typeParams []string) (Verdict, string) {
	if len(provided.Input) != len(required.Input) {
		return Mismatch, fmt.Sprintf(
			"%s takes %d parameter(s) but the interface declares %d",
			provided.Name, len(provided.Input), len(required.Input))
	}
	for i := range required.Input {
		if mentionsTypeParam(required.Input[i].Typing, typeParams) {
			continue
		}
		if !sameTypeText(required.Input[i].Typing, provided.Input[i].Typing) {
			return Mismatch, fmt.Sprintf(
				"parameter %d of %s is %s but the interface declares %s",
				i+1, provided.Name, orUntyped(provided.Input[i].Typing), orUntyped(required.Input[i].Typing))
		}
	}
	if len(provided.Output) != len(required.Output) {
		return Mismatch, fmt.Sprintf(
			"%s returns %d value(s) but the interface declares %d",
			provided.Name, len(provided.Output), len(required.Output))
	}
	for i := range required.Output {
		if mentionsTypeParam(required.Output[i].Typing, typeParams) {
			continue
		}
		if !sameTypeText(required.Output[i].Typing, provided.Output[i].Typing) {
			return Mismatch, fmt.Sprintf(
				"%s returns %s but the interface declares %s",
				provided.Name, orUntyped(provided.Output[i].Typing), orUntyped(required.Output[i].Typing))
		}
	}
	return Match, ""
}

// tsSatisfies applies TypeScript's assignability direction: an implementation may ignore
// trailing parameters, but may not demand ones the interface does not supply.
//
// Parameter TYPES are a separate matter, and TypeScript is deliberately unsound about them:
// a member written in method syntax -- which is what an interface member and a class method
// both are -- is BIVARIANT in its parameters even under strictFunctionTypes. `handle(e:
// MouseEv)` against `handle(e: BaseEv)` is accepted by tsc, and narrowing like that is how
// handler hierarchies are written, so comparing the text reported working code. A position
// is therefore a mismatch only when the two types are known to be UNRELATED: both resolve to
// topology types with no inheritance path between them, or both are primitives. Anything the
// graph cannot decide -- an external type, a type the scanner could not resolve -- yields no
// verdict rather than a guess.
func tsSatisfies(required, provided Signature, ctx ConformanceCtx) (Verdict, string) {
	typeParams := ctx.TypeParams
	req := 0
	for _, p := range provided.Input {
		if !p.Optional && !p.Variadic {
			req++
		}
	}
	if req > len(required.Input) {
		return Mismatch, fmt.Sprintf(
			"%s requires %d parameter(s) but the interface supplies %d",
			provided.Name, req, len(required.Input))
	}
	for i := range provided.Input {
		if i >= len(required.Input) {
			break
		}
		// `any`, `unknown` and `object` accept whatever the interface passes, and `any` is
		// assignable to whatever it demands, so text that differs from them is no evidence.
		if tsTopType(required.Input[i].Typing) || tsTopType(provided.Input[i].Typing) {
			continue
		}
		if mentionsTypeParam(required.Input[i].Typing, typeParams) {
			continue
		}
		if sameTypeText(required.Input[i].Typing, provided.Input[i].Typing) {
			continue
		}
		if !tsUnrelated(required.Input[i], provided.Input[i], ctx) {
			continue
		}
		return Mismatch, fmt.Sprintf(
			"parameter %d of %s is %s but the interface declares %s",
			i+1, provided.Name, orUntyped(provided.Input[i].Typing),
			orUntyped(required.Input[i].Typing))
	}
	return Match, ""
}

// tsUnrelated reports whether two differing TypeScript parameter types are known to have no
// relationship at all -- the only case bivariance does not excuse.
//
// Two ids the topology holds decide it outright: related by inheritance either way, the
// narrowing is legal; unrelated, it is a real error and stays reported. Two primitives decide
// it too, since `number` is not reachable from `string`. Everything else is a type the graph
// does not hold, and silence is the honest answer there.
func tsUnrelated(required, provided Param, ctx ConformanceCtx) bool {
	if required.TypingID != "" && provided.TypingID != "" {
		return !ctx.related(required.TypingID, provided.TypingID)
	}
	return tsPrimitive(required.Typing) && tsPrimitive(provided.Typing)
}

// tsPrimitive reports whether a TypeScript type is one of the built-in scalars, which no
// inheritance path can connect.
func tsPrimitive(t string) bool {
	switch strings.TrimSpace(t) {
	case "string", "number", "boolean", "bigint", "symbol", "void", "null", "undefined", "never":
		return true
	}
	return false
}

// tsTopType reports whether a TypeScript parameter type accepts every argument.
func tsTopType(t string) bool {
	switch strings.TrimSpace(t) {
	case "any", "unknown", "object":
		return true
	}
	return false
}

// mentionsTypeParam reports whether a declared type names one of the interface's type
// parameters -- `T`, `Map<String, T>`, `T[]`. Such a position is compared to nothing: see
// SatisfiesWithTypeParams.
func mentionsTypeParam(typing string, typeParams []string) bool {
	if len(typeParams) == 0 || strings.TrimSpace(typing) == "" {
		return false
	}
	for _, name := range qualifiedName.FindAllString(typing, -1) {
		for _, param := range typeParams {
			if name == param {
				return true
			}
		}
	}
	return false
}

// sameTypeText compares declared types, treating an unreadable one as agreeing. A scanner
// that could not name a type must not be the reason an implementer is reported broken.
//
// It compares the LAST path segment, because the same type is routinely written two ways
// across a trait and its impl -- `crate::peniko::Gradient` where the trait, having imported
// it, simply says `Gradient`. Reporting those as different is reporting an import style as a
// bug. The cost is that two genuinely different types sharing a final segment (`a::Config`
// and `b::Config`) compare equal; that is the right side to err on, since a missed warning is
// silence and a false one is an agent sent to fix working code.
func sameTypeText(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return true
	}
	if a == b {
		return true
	}
	// An associated type is a placeholder the implementer fills in; comparing it to the
	// concrete type it was filled with is comparing a question to its answer.
	if isAssociatedType(a) || isAssociatedType(b) {
		return true
	}
	return canonicalTypeText(a) == canonicalTypeText(b)
}

// qualifiedName matches one dotted or `::`-separated name inside a type: `java.util.Map`,
// `crate::peniko::Gradient`, or a bare `Integer`.
var qualifiedName = regexp.MustCompile(`[\p{L}_$][\p{L}\p{N}_$]*(?:(?:\.|::)[\p{L}_$][\p{L}\p{N}_$]*)*`)

// canonicalTypeText is lastPathSegment applied to EVERY name in a type rather than to the
// type as a whole, with insignificant whitespace dropped.
//
// A type argument is a type in its own right and is spelled as freely as the outer one: the
// interface says `Map<String, Integer>` and the implementer `Map<String,Integer>`, or one side
// writes `java.lang.Integer` inside the brackets. Cutting the whole text at its last dot turned
// the second into `Integer>`, and neither pair compared equal -- so a Java implementer whose
// only difference was formatting was reported as not delivering the interface.
//
// Whitespace survives only between two word characters, where it separates tokens
// (`&mut Scene`, `? extends Number`); everywhere else -- after a comma, inside brackets --
// it carries nothing.
func canonicalTypeText(t string) string {
	t = qualifiedName.ReplaceAllStringFunc(t, lastPathSegment)
	var b strings.Builder
	pendingSpace := false
	var prev rune
	for _, r := range t {
		if unicode.IsSpace(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace && isTypeWordRune(prev) && isTypeWordRune(r) {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isTypeWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isAssociatedType reports whether a type name is a trait's own placeholder.
func isAssociatedType(t string) bool {
	return strings.HasPrefix(t, "Self::") || strings.Contains(t, "::Self::")
}

// lastPathSegment strips module qualification, keeping any leading reference or mutability
// markers so `&mut Scene` and `&mut RenderContext` still differ.
func lastPathSegment(t string) string {
	prefix := ""
	for _, p := range []string{"&mut ", "&", "*mut ", "*const ", "*"} {
		if strings.HasPrefix(t, p) {
			prefix, t = p, strings.TrimPrefix(t, p)
			break
		}
	}
	if i := strings.LastIndex(t, "::"); i >= 0 {
		t = t[i+2:]
	}
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return prefix + t
}

func orUntyped(t string) string {
	if t == "" {
		return "untyped"
	}
	return t
}

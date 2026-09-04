package contract

import (
	"encoding/json"
	"fmt"
	"strings"

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
	}
	if err := json.Unmarshal(blob, &wire); err != nil {
		return nil
	}
	out := make([]Signature, 0, len(wire))
	for _, w := range wire {
		out = append(out, Signature{
			Name:       w.Name,
			Input:      fromWire(w.Input),
			Output:     fromWire(w.Output),
			HasDefault: w.HasDefault,
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
		out = append(out, Param{
			Name: p.Name, Typing: p.Typing, TypingID: p.TypingID,
			Optional: p.Optional, Variadic: p.Variadic, KeyOnly: p.KeyOnly,
		})
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
		return exactlySatisfies(required, provided)
	case "typescript":
		return tsSatisfies(required, provided)
	case "python":
		// Reaching here means the name exists, which is all Python asks.
		return Match, ""
	}
	return Unknown, ""
}

func exactlySatisfies(required, provided Signature) (Verdict, string) {
	if len(provided.Input) != len(required.Input) {
		return Mismatch, fmt.Sprintf(
			"%s takes %d parameter(s) but the interface declares %d",
			provided.Name, len(provided.Input), len(required.Input))
	}
	for i := range required.Input {
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
func tsSatisfies(required, provided Signature) (Verdict, string) {
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
		if !sameTypeText(required.Input[i].Typing, provided.Input[i].Typing) {
			return Mismatch, fmt.Sprintf(
				"parameter %d of %s is %s but the interface declares %s",
				i+1, provided.Name, orUntyped(provided.Input[i].Typing),
				orUntyped(required.Input[i].Typing))
		}
	}
	return Match, ""
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
	return lastPathSegment(a) == lastPathSegment(b)
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

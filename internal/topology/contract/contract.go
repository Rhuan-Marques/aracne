// Package contract decides whether a recorded call site still satisfies the signature of
// the function it calls.
//
// WHY THIS EXISTS. A signature_changed warning used to be an EVENT that later had to be
// "cleared", and every clearing rule was a proxy for the question nobody could ask. The
// callee's previous signature stood in for "what the caller expects", and the caller's
// file being re-parsed stood in for "the caller was fixed". Both proxies are wrong in the
// same ordinary situation:
//
//	fun1(x)                fun2 calls fun1(5)     -> agree
//	fun1 -> (x, y)                                -> warn, correctly
//	fun2 -> fun1(5, 6, 7)                         -> cleared, because fun2's FILE was
//	                                                 re-parsed; the call was never checked
//	fun1 -> (x, y, z)                             -> warned AGAIN
//
// The code compiles at that last step and aracne warns anyway. Warnings go straight to the
// model after every edit, so a false one is an instruction to go re-verify correct code.
//
// Asking the real question -- does what the caller passes still fit what the callee
// declares -- makes the warning a property of the CURRENT state, so a revert, a fix and an
// unrelated edit all fall out of one comparison and none of them needs a rule.
package contract

import (
	"encoding/json"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Verdict is the answer to "does this call still fit this signature".
type Verdict int

const (
	// Unknown means this language, or this particular call, cannot say. It is a
	// first-class answer rather than an error: JavaScript arity is genuinely not
	// binding, and an argument whose type cannot be inferred genuinely carries no
	// information. Callers fall back to the older heuristic on Unknown instead of
	// inventing a verdict.
	Unknown Verdict = iota
	Match
	Mismatch
)

func (v Verdict) String() string {
	switch v {
	case Match:
		return "match"
	case Mismatch:
		return "mismatch"
	}
	return "unknown"
}

// CallSite is what a caller was observed to pass, recorded when its body was parsed.
//
// Types is per-position and nil-able so that "we could not tell" is expressible for ONE
// argument without discarding the rest: a call like f(x, someUnresolvedCall()) still
// carries a usable type for the first argument. An all-nil Types is normal in a language
// with no annotations and simply means only arity can be judged.
type CallSite struct {
	CalleeID string    `json:"c"`
	N        int       `json:"n"`           // arguments passed; -1 when even the count is unknown
	Types    []*string `json:"t,omitempty"` // per position; nil entry = not determinable
	Variadic bool      `json:"v,omitempty"` // f(xs...) / f(*args) / f(...a) AT THE CALL SITE
	Kwargs   []string  `json:"k,omitempty"` // Python keyword-argument names
	// Dyn marks a callee that was resolved heuristically rather than exactly -- Java's
	// overload fallback, a duck-typed method call. The edge is a guess about WHICH function
	// is called, so a mismatch against it is a guess too, and gets downgraded to Unknown.
	// Without this, Java's habit of connecting every same-named overload when no arity fits
	// would make an arity check report a mismatch against overloads the call never meant.
	Dyn bool `json:"d,omitempty"`
}

// Param is one declared parameter, read from Properties["input"].
//
// The five languages share an identical VariableDefinition{Name, Typing, TypingID}, so one
// reader serves all of them. The three booleans are what that shared shape cannot express
// and each scanner must supply: Python needs defaults and */**, TypeScript needs `?`.
type Param struct {
	Name     string
	Typing   string
	TypingID string // resolved resource id for Typing, "" for builtins and unresolved
	Optional bool   // has a default, or is marked `?`
	Variadic bool   // ...T / *args / T...
	KeyOnly  bool   // Python keyword-only (declared after a bare * or *args)
}

// Env is the small amount of graph a matcher needs beyond the two endpoints.
//
// Only a kind lookup, and only because of one false positive that would otherwise be
// unavoidable: passing *os.File to an io.Writer parameter is correct code whose argument
// type does not equal its parameter type. A matcher that compared those as strings would
// warn on every interface parameter in the repository. KindOf lets a matcher recognise the
// parameter as an interface and decline to judge, which is the honest answer -- deciding
// whether a concrete type satisfies an interface is the scanner's job, not this package's.
type Env struct {
	// Lookup resolves a type id to its resource. A nil Lookup, or an id that is empty or
	// outside the graph, means "cannot say" -- which every matcher must treat as a reason
	// to decline judgement rather than a reason to warn.
	Lookup func(typeID string) (domain.Resource, bool)
}

func (e Env) lookup(typeID string) (domain.Resource, bool) {
	if e.Lookup == nil || typeID == "" {
		return domain.Resource{}, false
	}
	return e.Lookup(typeID)
}

func (e Env) kindOf(typeID string) domain.ResourceKind {
	res, ok := e.lookup(typeID)
	if !ok {
		return ""
	}
	return res.Kind
}

// underlyingOf returns a named type's underlying type text, which is how a matcher tells
// that `type Celsius float64` accepts an untyped numeric constant. Scanners already store
// it, and resourceSignatureKey already reads it.
func (e Env) underlyingOf(typeID string) string {
	res, ok := e.lookup(typeID)
	if !ok || res.Properties == nil {
		return ""
	}
	u, _ := res.Properties["underlying"].(string)
	return u
}

// Untyped constant tokens.
//
// A call site records what it PASSES, and in several languages a literal has no single
// type: Go's 5 is an untyped integer constant, assignable to int, uint8, float64,
// complex128 and any named type built on those. Recording it as "int" and comparing for
// equality would report a mismatch on f(5) against f(x float64) -- legal, extremely
// common code. These tokens say "a literal of this class" so the matcher can apply the
// language's assignability rule instead of string equality.
const (
	UntypedInt    = "#int"
	UntypedFloat  = "#float"
	UntypedString = "#string"
	UntypedRune   = "#rune"
	UntypedBool   = "#bool"
	UntypedNil    = "#nil"
)

// IsUntyped reports whether a recorded argument token is an untyped-constant class.
func IsUntyped(token string) bool {
	return strings.HasPrefix(token, "#")
}

// Matcher is one language's rules. Implementations must be pure.
type Matcher interface {
	// Match reports whether site still fits callee. The string explains a Mismatch and is
	// shown to the agent, so it names what actually differs.
	Match(env Env, callee domain.Resource, site CallSite) (Verdict, string)
	// Language is the domain.Resource.Language value this matcher serves.
	Language() string
}

// registry is populated by each language's init. A language with no entry yields nil, and
// the caller treats that as Unknown -- adding a language must not silently start warning.
var registry = map[string]Matcher{}

func register(m Matcher) { registry[m.Language()] = m }

// For returns the matcher for a language, or nil when that language has no rules.
func For(language string) Matcher { return registry[language] }

// Params reads a resource's declared parameters.
//
// It tolerates both shapes the same resource arrives in -- typed structs straight from a
// scanner, and the JSON round-trip of those structs read back from SQLite -- by going
// through json.Marshal in both cases. Getting this wrong is not hypothetical: the same
// mismatch silently disabled an earlier version of the signature-warning fix, because a
// value stored from one shape never compared equal to the other.
func Params(res domain.Resource) []Param {
	return decodeParams(res.Properties["input"])
}

// paramWire mirrors VariableDefinition plus the markers scanners add. Field names are
// matched case-insensitively by encoding/json, so "Typing" and "typing" both land.
type paramWire struct {
	Name     string `json:"Name"`
	Typing   string `json:"Typing"`
	TypingID string `json:"TypingID"`
	Optional bool   `json:"Optional"`
	Variadic bool   `json:"Variadic"`
	KeyOnly  bool   `json:"KeyOnly"`
}

// OverloadParams reads the additional signatures a resource declares, as parameter lists.
//
// Only TypeScript writes them today (an overload set: N bodiless signatures plus one
// implementation, all under one id), but the shape is the language-neutral one every
// scanner already stores parameters in, so nothing here is TypeScript-specific. An absent
// or unreadable property yields nothing, and the caller then judges the resource's own
// parameters as before.
func OverloadParams(res domain.Resource) [][]Param {
	raw, err := json.Marshal(res.Properties["overloads"])
	if err != nil {
		return nil
	}
	var wire []struct {
		Input []paramWire `json:"Input"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}
	out := make([][]Param, 0, len(wire))
	for _, w := range wire {
		out = append(out, paramsFromWire(w.Input))
	}
	return out
}

func decodeParams(v any) []Param {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var wire []paramWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}
	return paramsFromWire(wire)
}

func paramsFromWire(wire []paramWire) []Param {
	out := make([]Param, 0, len(wire))
	for _, w := range wire {
		// paramWire is Param plus JSON tags, field for field: a conversion says that in one
		// place, and stops a new Param field from being silently dropped here.
		out = append(out, Param(w))
	}
	return out
}

// Arity is the number of arguments a signature accepts, as an inclusive range. max is -1
// for "no upper bound", which is what a variadic tail means.
type Arity struct {
	Min int
	Max int
}

// Accepts reports whether n arguments fit.
func (a Arity) Accepts(n int) bool {
	if n < a.Min {
		return false
	}
	return a.Max < 0 || n <= a.Max
}

// PositionalArity computes the accepted range from declared parameters, counting only
// positional ones. A variadic tail removes the upper bound; an optional parameter lowers
// the minimum without lowering the maximum.
func PositionalArity(params []Param) Arity {
	a := Arity{}
	for _, p := range params {
		if p.KeyOnly {
			continue // never passed positionally
		}
		if p.Variadic {
			a.Max = -1
			continue // and contributes nothing to the minimum
		}
		if a.Max >= 0 {
			a.Max++
		}
		if !p.Optional {
			a.Min++
		}
	}
	return a
}

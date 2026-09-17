package contract

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func init() { register(rustMatcher{}) }

// rustMatcher applies Rust's rules, which are the strictest of any language here: no
// default arguments, no overloads, no varargs, and `self` already excluded from the
// declared parameters by the parser. So an argument count that differs from the parameter
// count is always wrong, with no escape hatch to account for.
type rustMatcher struct{}

func (rustMatcher) Language() string { return "rust" }

func (m rustMatcher) Match(env Env, callee domain.Resource, site CallSite) (Verdict, string) {
	params := Params(callee)
	if len(params) == 0 && site.N <= 0 {
		return Unknown, ""
	}
	if site.N >= 0 && !site.Variadic {
		if a := (Arity{Min: len(params), Max: len(params)}); !a.Accepts(site.N) {
			return Mismatch, arityMessage(callee.Name, a, site.N)
		}
	}
	switch v, why := matchTypes(env, params, site, rustTypeOpaque, rustAccepts); v {
	case Mismatch:
		return Mismatch, why
	case Unverified:
		return Unverified, why
	}
	return Match, ""
}

// rustTypeOpaque declines the positions where comparing type text would be wrong rather
// than merely unhelpful: a generic parameter, an `impl Trait` position and a trait object
// all accept values whose written type is not the parameter's.
func rustTypeOpaque(env Env, p Param) bool {
	t := strings.TrimSpace(p.Typing)
	if t == "" {
		return true
	}
	if strings.HasPrefix(t, "impl ") || strings.HasPrefix(t, "dyn ") || strings.Contains(t, "dyn ") {
		return true
	}
	if isLikelyTypeParam(t) {
		return true
	}
	if env.kindOf(p.TypingID) == domain.ResourceInterface {
		return true // a trait, reached by name
	}
	return false
}

// rustAccepts compares one argument against one parameter. Only literals reach here with a
// token, so the question is narrow: does this literal class fit this declared type.
func rustAccepts(env Env, p Param, actual string) bool {
	declared := normalizeRustType(p.Typing)
	if !IsUntyped(actual) {
		return declared == normalizeRustType(actual)
	}
	switch actual {
	case UntypedInt:
		return rustNumeric[declared]
	case UntypedFloat:
		return declared == "f32" || declared == "f64"
	case UntypedString:
		// A string literal is &str; String is a different type and needs a conversion, so
		// treating both as acceptable would miss a real mismatch.
		return declared == "&str" || declared == "str"
	case UntypedRune:
		return declared == "char"
	case UntypedBool:
		return declared == "bool"
	case UntypedNil:
		return false // Rust has no null literal; nothing should produce this
	}
	return false
}

func normalizeRustType(t string) string {
	return strings.ReplaceAll(strings.TrimSpace(t), " ", "")
}

var rustNumeric = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "i128": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "u128": true, "usize": true,
	"f32": true, "f64": true,
}

package idresolve

import (
	"testing"
)

// RD-01. The suffix tier prefers the SHORTEST matching id whenever it is strictly shorter than
// the runner-up. That rule was written for genuine nesting -- a caller who writes `a.b.Foo`
// means `a.b.Foo` rather than `x.y.z.a.b.Foo` -- but it also fired for resources that merely
// share a name in different files and languages, so a one-token query returned whichever
// resource happened to have the shortest id, with no sign that anything else matched. A model
// that asks for `area` and is handed another language's function reads, reasons about and
// edits the wrong code.

// A bare name shared by unrelated resources is ambiguous, not "the shortest one".
func TestRD01_ASharedBareNameIsAmbiguous(t *testing.T) {
	topo := topoOf(
		fn("a/shapes.area", "area"),
		fn("b/geometry.area", "area"),
		fn("c.area", "area"),
	)
	got := Resolve(topo, "area", Options{})
	if got.Found() {
		t.Fatalf("Resolve(\"area\") silently returned %q; three unrelated resources are named area", got.ID)
	}
	if got.Tier != TierAmbiguous {
		t.Fatalf("tier = %s, want %s", got.Tier, TierAmbiguous)
	}
	if len(got.Candidates) != 3 {
		t.Fatalf("candidates = %v, want all three", got.Candidates)
	}
}

// The same shape one level up: a shared trailing pair is still ambiguous.
func TestRD01_ASharedTrailingPairIsAmbiguous(t *testing.T) {
	topo := topoOf(
		method("python/shapes.Circle.area", "area"),
		method("jsfamily/shapes.Circle.area", "area"),
	)
	got := Resolve(topo, "Circle.area", Options{})
	if got.Found() {
		t.Fatalf("Resolve(\"Circle.area\") silently returned %q", got.ID)
	}
	if got.Tier != TierAmbiguous {
		t.Fatalf("tier = %s, want %s", got.Tier, TierAmbiguous)
	}
}

// What the shortest-id rule is FOR must keep working: a query that names a resource exactly,
// while a longer id merely ends with those same tokens, resolves to the exact one.
func TestRD01_TrueNestingStillPrefersTheShorterID(t *testing.T) {
	topo := topoOf(
		fn("a/b.Foo", "Foo"),
		fn("x/y/a/b.Foo", "Foo"),
	)
	mustResolve(t, topo, "a.b.Foo", "a/b.Foo", TierSuffix)
	mustResolve(t, topo, "a/b.Foo", "a/b.Foo", TierExact)
}

// And an unambiguous unique suffix still resolves.
func TestRD01_AUniqueSuffixStillResolves(t *testing.T) {
	topo := topoOf(
		fn("a/shapes.area", "area"),
		fn("b/geometry.perimeter", "perimeter"),
	)
	mustResolve(t, topo, "perimeter", "b/geometry.perimeter", TierSuffix)
}

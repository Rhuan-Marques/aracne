package idresolve

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The point of this package is that a caller's REASONABLE guess at a resource ID resolves.
// These tests are written from the guesses a model actually makes: the form implied by the
// source it just read, with the project-directory prefix and the separator convention it
// has no way to know.

func topoOf(res ...domain.Resource) *domain.Topology {
	t := &domain.Topology{Resources: map[string]domain.Resource{}}
	for _, r := range res {
		t.Resources[r.ID] = r
	}
	return t
}

func fn(id, name string) domain.Resource {
	return domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: name}
}

func method(id, name string) domain.Resource {
	return domain.Resource{ID: id, Kind: domain.ResourceMethod, Name: name}
}

func strct(id, name string) domain.Resource {
	return domain.Resource{ID: id, Kind: domain.ResourceStruct, Name: name}
}

func mustResolve(t *testing.T, topo *domain.Topology, raw, wantID, wantTier string) {
	t.Helper()
	got := Resolve(topo, raw, Options{})
	if !got.Found() {
		t.Fatalf("Resolve(%q): not found (tier %s, candidates %v)", raw, got.Tier, got.Candidates)
	}
	if got.ID != wantID {
		t.Fatalf("Resolve(%q) = %q, want %q", raw, got.ID, wantID)
	}
	if got.Tier != wantTier {
		t.Fatalf("Resolve(%q) tier = %q, want %q", raw, got.Tier, wantTier)
	}
}

// This is the exact failure that motivated the package: the flask fixture stores IDs under
// the fixture DIRECTORY name with "/" separators, so neither form a reader would try
// resolved, and every lookup fell through to an ambiguous name scan.
func TestResolvesTheFlaskIDsThatUsedToMiss(t *testing.T) {
	const real = "worktree/src/flask/app.Flask.register_blueprint"
	topo := topoOf(
		method(real, "register_blueprint"),
		method("worktree/src/flask/app.Flask.add_url_rule", "add_url_rule"),
		strct("worktree/src/flask/app.Flask", "Flask"),
	)
	for _, guess := range []string{
		"src/flask/app.Flask.register_blueprint", // repo-relative, as a reader would type
		"flask.app.Flask.register_blueprint",     // the importable module path
		"app.Flask.register_blueprint",           // module-local
		"Flask.register_blueprint",               // type-qualified
		"flask/app.Flask.register_blueprint",     // mixed separators
		real,                                     // and the real one still works
	} {
		res := Resolve(topo, guess, Options{})
		if !res.Found() || res.ID != real {
			t.Fatalf("guess %q -> %q (tier %s); want %q",
				guess, res.ID, res.Tier, real)
		}
	}
}

func TestExactIDWinsOverEverything(t *testing.T) {
	topo := topoOf(fn("a.b.run", "run"), fn("run", "run"))
	mustResolve(t, topo, "run", "run", TierExact)
}

func TestSuffixPrefersTheShortestID(t *testing.T) {
	// A caller who wrote "b.Foo" meant the shallower one, not a deeply nested namesake.
	// (The query must NOT be an exact ID, or the exact tier would answer it.)
	topo := topoOf(strct("a.b.Foo", "Foo"), strct("x.y.z.a.b.Foo", "Foo"))
	mustResolve(t, topo, "b.Foo", "a.b.Foo", TierSuffix)
}

func TestAmbiguousSuffixReturnsCandidatesNotAGuess(t *testing.T) {
	// Two equally-deep matches: guessing would attach the caller to the wrong resource.
	topo := topoOf(strct("one.pkg.Foo", "Foo"), strct("two.pkg.Foo", "Foo"))
	got := Resolve(topo, "pkg.Foo", Options{})
	if got.Found() {
		t.Fatalf("want ambiguous, resolved to %q", got.ID)
	}
	if got.Tier != TierAmbiguous || len(got.Candidates) != 2 {
		t.Fatalf("tier %s with %d candidates; want ambiguous with 2", got.Tier, len(got.Candidates))
	}
}

func TestKindFilterDisambiguates(t *testing.T) {
	// A class and a same-named factory function coexist; asking for a struct is enough.
	topo := topoOf(strct("m.Circle", "Circle"), fn("m.make_circle", "Circle"))
	got := Resolve(topo, "Circle", Options{Kinds: []domain.ResourceKind{domain.ResourceStruct}})
	if !got.Found() || got.ID != "m.Circle" {
		t.Fatalf("got %q tier %s; want m.Circle", got.ID, got.Tier)
	}
}

func TestAliasTierResolvesLegacyIDsBeforeAnyGuess(t *testing.T) {
	// After an id-scheme migration the alias table holds the AUTHORITATIVE mapping; it must
	// be consulted before the fuzzy tiers so a migrated database never guesses.
	topo := topoOf(strct("flask.app.Flask", "Flask"), strct("other.app.Flask", "Flask"))
	alias := func(old string) (string, bool) {
		if old == "worktree/src/flask/app.Flask" {
			return "flask.app.Flask", true
		}
		return "", false
	}
	got := Resolve(topo, "worktree/src/flask/app.Flask", Options{Alias: alias})
	if got.Tier != TierAlias || got.ID != "flask.app.Flask" {
		t.Fatalf("got %q tier %s; want flask.app.Flask via alias", got.ID, got.Tier)
	}
}

func TestAliasPointingAtAMissingResourceFallsThrough(t *testing.T) {
	// A stale alias must not dead-end the lookup.
	topo := topoOf(strct("flask.app.Flask", "Flask"))
	alias := func(string) (string, bool) { return "gone.Flask", true }
	got := Resolve(topo, "src/flask/app.Flask", Options{Alias: alias})
	if !got.Found() || got.ID != "flask.app.Flask" {
		t.Fatalf("got %q tier %s; want the suffix match to still win", got.ID, got.Tier)
	}
}

func TestGoReceiverAndRustPathForms(t *testing.T) {
	topo := topoOf(
		method("mod/pkg1.(Stu).Method", "Method"),
		method("serde::de::impls::BytesVisitor::visit_bool", "visit_bool"),
	)
	mustResolve(t, topo, "pkg1.Stu.Method", "mod/pkg1.(Stu).Method", TierSuffix)
	mustResolve(t, topo, "mod/pkg1.(Stu).Method", "mod/pkg1.(Stu).Method", TierExact)
	mustResolve(t, topo, "impls.BytesVisitor.visit_bool",
		"serde::de::impls::BytesVisitor::visit_bool", TierSuffix)
	mustResolve(t, topo, "BytesVisitor::visit_bool",
		"serde::de::impls::BytesVisitor::visit_bool", TierSuffix)
}

func TestJavaSignatureAndNestedForms(t *testing.T) {
	topo := topoOf(
		method("com.t.shapes.Circle.<init>(double)", "<init>"),
		strct("com.t.nested.Outer$Helper", "Helper"),
	)
	// The signature is dropped for matching, so the un-signed form resolves...
	mustResolve(t, topo, "Circle.<init>", "com.t.shapes.Circle.<init>(double)", TierSuffix)
	// ...and the exact form still wins outright.
	mustResolve(t, topo, "com.t.shapes.Circle.<init>(double)",
		"com.t.shapes.Circle.<init>(double)", TierExact)
	// `$` for a nested type is decoration; `.` must work too.
	mustResolve(t, topo, "Outer.Helper", "com.t.nested.Outer$Helper", TierSuffix)
}

func TestPartialIdentifierDoesNotMatch(t *testing.T) {
	// "blueprint" must NOT resolve "register_blueprint": a suffix that ends mid-identifier
	// would attach callers to arbitrary resources.
	topo := topoOf(method("a.Flask.register_blueprint", "register_blueprint"))
	got := Resolve(topo, "blueprint", Options{})
	if got.Found() {
		t.Fatalf("partial identifier resolved to %q", got.ID)
	}
}

func TestMissReturnsRankedCandidates(t *testing.T) {
	// A miss must cost zero extra turns: the caller gets something to pick from.
	topo := topoOf(
		method("a.Flask.register_blueprint", "register_blueprint"),
		method("b.Flask.register_error_handler", "register_error_handler"),
		fn("c.unrelated", "unrelated"),
	)
	got := Resolve(topo, "Flask.register_blueprints", Options{}) // note the typo'd plural
	if got.Found() {
		t.Fatalf("unexpected match %q", got.ID)
	}
	if len(got.Candidates) == 0 {
		t.Fatal("a miss must return candidates")
	}
	if got.Candidates[0].ID != "a.Flask.register_blueprint" {
		t.Fatalf("best candidate = %q, want a.Flask.register_blueprint",
			got.Candidates[0].ID)
	}
}

func TestCandidatesAreCapped(t *testing.T) {
	topo := &domain.Topology{Resources: map[string]domain.Resource{}}
	for _, p := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		topo.Resources[p+".handler"] = fn(p+".handler", "handler")
	}
	got := Resolve(topo, "handlers", Options{})
	if len(got.Candidates) > MaxCandidates {
		t.Fatalf("got %d candidates, want at most %d", len(got.Candidates), MaxCandidates)
	}
}

func TestEmptyAndNilInputsAreSafe(t *testing.T) {
	if Resolve(nil, "x", Options{}).Found() {
		t.Fatal("nil topology must not resolve")
	}
	if Resolve(topoOf(fn("a", "a")), "   ", Options{}).Found() {
		t.Fatal("blank query must not resolve")
	}
}

func TestFormatCandidates(t *testing.T) {
	out := FormatCandidates("x", []Candidate{{ID: "a.B", Kind: domain.ResourceStruct, Name: "B"}})
	if out == "" || !contains(out, "a.B") || !contains(out, "struct") {
		t.Fatalf("unhelpful rendering: %q", out)
	}
	if FormatCandidates("x", nil) != "" {
		t.Fatal("no candidates must render nothing")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

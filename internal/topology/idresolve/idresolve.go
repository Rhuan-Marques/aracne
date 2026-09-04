// Package idresolve turns whatever a caller *thinks* a resource ID is into the ID the
// topology actually holds.
//
// WHY THIS EXISTS. Resource IDs are the only handle an agent has on the graph, and they
// are not uniformly guessable. Go roots them at the go.mod module path and Rust at the
// crate name — both of which appear verbatim in the source the agent is already reading —
// but Python and JS/TS root them at the PROJECT DIRECTORY'S BASENAME with "/" separators,
// which appears nowhere in the code:
//
//	worktree/src/flask/app.Flask.register_blueprint      <- what the topology stores
//	flask.app.Flask.register_blueprint                   <- what the source implies
//	src/flask/app.Flask.register_blueprint               <- what a reader would try
//
// Every lookup in those languages therefore missed, fell through to a bare name scan, and
// on a 28-47% name-ambiguity rate came back as a disambiguation list rather than an answer
// — an extra turn per lookup, which is the mechanism behind the +36%/+46% turn counts
// measured for Python/TypeScript.
//
// Resolution walks progressively weaker tiers and STOPS at the first unambiguous hit. When
// nothing resolves it returns ranked candidates instead of a bare error, so a wrong guess
// costs zero extra turns: the caller can render "did you mean" and the model picks.
package idresolve

import (
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Tier names, most to least trustworthy. Reported on every result so callers (and tests)
// can tell a real hit from a lucky one.
const (
	TierExact     = "exact"     // the ID is stored verbatim
	TierAlias     = "alias"     // a previous id-scheme's ID, via resource_alias
	TierSuffix    = "suffix"    // a unique trailing-segment match ("app.Flask.method")
	TierName      = "name"      // a unique (kind, name) match
	TierNone      = "none"      // nothing matched; Candidates holds suggestions
	TierAmbiguous = "ambiguous" // several equally good matches; Candidates holds them
)

// AliasFunc maps a legacy ID to its current one. helper.ResolveAlias satisfies it.
// A nil AliasFunc simply skips the alias tier.
type AliasFunc func(oldID string) (string, bool)

// Candidate is one suggestion for an unresolved lookup.
type Candidate struct {
	ID   string
	Kind domain.ResourceKind
	Name string
}

// Result is the outcome of one resolution.
type Result struct {
	ID       string
	Resource domain.Resource
	Tier     string
	// Candidates is populated only when Tier is TierNone or TierAmbiguous, ranked best
	// first and capped by MaxCandidates.
	Candidates []Candidate
}

// Found reports whether the lookup resolved to exactly one resource.
func (r Result) Found() bool { return r.Tier != TierNone && r.Tier != TierAmbiguous }

// MaxCandidates bounds the suggestion list. Suggestions are a nudge, not a catalogue: a
// long list costs the tokens the miss was supposed to save.
const MaxCandidates = 5

// Options tunes one resolution.
type Options struct {
	// Kinds restricts matching to these resource kinds. Empty means any kind.
	Kinds []domain.ResourceKind
	// Filter is an arbitrary additional predicate, ANDed with Kinds. Callers that already
	// hold a match predicate pass it here instead of flattening it to a kind list.
	Filter func(domain.Resource) bool
	// Alias resolves legacy IDs. Optional.
	Alias AliasFunc
}

// Resolve finds the resource `raw` refers to.
//
// The tiers, in order:
//
//	exact   topo.Resources[raw]
//	alias   resource_alias[raw] -> current ID
//	suffix  raw's identifier tokens are a unique trailing run of some resource's tokens
//	name    a unique (kind, name) match
//
// Anything else returns TierNone (or TierAmbiguous) with ranked candidates.
func Resolve(topo *domain.Topology, raw string, opt Options) Result {
	raw = strings.TrimSpace(raw)
	if topo == nil || raw == "" {
		return Result{Tier: TierNone}
	}
	allow := allowFunc(opt)

	// 1. exact
	if res, ok := topo.Resources[raw]; ok && allow(res) {
		return Result{ID: raw, Resource: res, Tier: TierExact}
	}

	// 2. alias — an ID minted under a previous scheme. Checked before any fuzzy tier so a
	// migrated database gives the *authoritative* answer rather than a guess.
	if opt.Alias != nil {
		if newID, ok := opt.Alias(raw); ok {
			if res, ok := topo.Resources[newID]; ok && allow(res) {
				return Result{ID: newID, Resource: res, Tier: TierAlias}
			}
		}
	}

	// 3. suffix over identifier tokens. This is the tier that absorbs the whole class of
	// "the root prefix and the separators are not what I guessed" mistakes, because it
	// compares the trailing identifiers and ignores both.
	want := tokenize(raw)
	if len(want) > 0 {
		var hits []string
		for id, res := range topo.Resources {
			if !allow(res) {
				continue
			}
			if hasTokenSuffix(tokenize(id), want) {
				hits = append(hits, id)
			}
		}
		if len(hits) == 1 {
			return Result{ID: hits[0], Resource: topo.Resources[hits[0]], Tier: TierSuffix}
		}
		if len(hits) > 1 {
			// Prefer the shortest ID: among `a.b.Foo` and `x.y.z.a.b.Foo`, a caller who
			// wrote `a.b.Foo` almost always meant the former.
			//
			// Token counts are computed once rather than inside the comparator, which
			// would re-tokenize O(n log n) times per lookup.
			depth := make(map[string]int, len(hits))
			for _, h := range hits {
				depth[h] = len(tokenize(h))
			}
			sort.Slice(hits, func(i, j int) bool {
				if depth[hits[i]] != depth[hits[j]] {
					return depth[hits[i]] < depth[hits[j]]
				}
				return hits[i] < hits[j]
			})
			if depth[hits[0]] < depth[hits[1]] {
				return Result{ID: hits[0], Resource: topo.Resources[hits[0]], Tier: TierSuffix}
			}
			return Result{Tier: TierAmbiguous, Candidates: candidatesFor(topo, hits)}
		}
	}

	// 4. bare (kind, name)
	last := ""
	if len(want) > 0 {
		last = want[len(want)-1]
	}
	if last != "" {
		var hits []string
		for id, res := range topo.Resources {
			if allow(res) && res.Name == last {
				hits = append(hits, id)
			}
		}
		if len(hits) == 1 {
			return Result{ID: hits[0], Resource: topo.Resources[hits[0]], Tier: TierName}
		}
		if len(hits) > 1 {
			sort.Strings(hits)
			return Result{Tier: TierAmbiguous, Candidates: candidatesFor(topo, hits)}
		}
	}

	return Result{Tier: TierNone, Candidates: suggest(topo, want, allow)}
}

// --------------------------------------------------------------------------- //
// tokenization
// --------------------------------------------------------------------------- //

// tokenize reduces an ID to its identifier tokens, discarding every separator the
// languages disagree on and every decoration that is not part of the name:
//
//	worktree/src/flask/app.Flask.register_blueprint -> [worktree src flask app Flask register_blueprint]
//	flask.app.Flask.register_blueprint              -> [flask app Flask register_blueprint]
//	mod/pkg1.(Stu).Method                           -> [mod pkg1 Stu Method]
//	serde::de::impls::BytesVisitor                  -> [serde de impls BytesVisitor]
//	com.t.shapes.Circle.<init>(double)              -> [com t shapes Circle <init>]
//
// A Java signature is dropped before splitting so `Circle.area(int)` and `Circle.area`
// tokenize alike; the exact tier still distinguishes overloads when the caller is precise.
func tokenize(id string) []string {
	if id == "" {
		return nil
	}
	// Drop a trailing parameter signature: "(int,String)" at the very end.
	if i := strings.LastIndex(id, "("); i > 0 && strings.HasSuffix(id, ")") {
		id = id[:i]
	}
	id = strings.ReplaceAll(id, "::", "/")
	id = strings.ReplaceAll(id, "\\", "/")
	fields := strings.FieldsFunc(id, func(r rune) bool {
		return r == '/' || r == '.' || r == '#' || r == '$'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		// Go receivers arrive as "(Stu)"; the parens are decoration, not identity.
		f = strings.Trim(f, "()")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// hasTokenSuffix reports whether `want` is a trailing run of `have`. The final token must
// match exactly — a suffix that ends mid-identifier would match unrelated resources.
func hasTokenSuffix(have, want []string) bool {
	if len(want) == 0 || len(want) > len(have) {
		return false
	}
	off := len(have) - len(want)
	for i := range want {
		if have[off+i] != want[i] {
			return false
		}
	}
	return true
}

// --------------------------------------------------------------------------- //
// suggestions
// --------------------------------------------------------------------------- //

// suggest ranks resources by how much of their trailing token run the query shares, so a
// miss returns something actionable rather than a dead end.
func suggest(topo *domain.Topology, want []string, allow func(domain.Resource) bool) []Candidate {
	if len(want) == 0 {
		return nil
	}
	last := want[len(want)-1]
	lastLower := strings.ToLower(last)

	type scored struct {
		id    string
		score int
	}
	var ranked []scored
	for id, res := range topo.Resources {
		if !allow(res) {
			continue
		}
		score := 0
		switch {
		case res.Name == last:
			score = 100
		case strings.EqualFold(res.Name, last):
			score = 80
		case strings.Contains(strings.ToLower(res.Name), lastLower):
			score = 40
		case strings.Contains(lastLower, strings.ToLower(res.Name)) && len(res.Name) > 2:
			score = 20
		default:
			continue
		}
		// Shared leading context breaks ties between same-named resources in different
		// packages: the one nearest the caller's guess ranks first.
		score += 2 * sharedTokens(tokenize(id), want)
		ranked = append(ranked, scored{id, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	if len(ranked) > MaxCandidates {
		ranked = ranked[:MaxCandidates]
	}
	ids := make([]string, 0, len(ranked))
	for _, r := range ranked {
		ids = append(ids, r.id)
	}
	return candidatesFor(topo, ids)
}

// sharedTokens counts how many tokens the two IDs have in common, ignoring position.
func sharedTokens(have, want []string) int {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	n := 0
	for _, w := range want {
		if set[w] {
			n++
		}
	}
	return n
}

func candidatesFor(topo *domain.Topology, ids []string) []Candidate {
	if len(ids) > MaxCandidates {
		ids = ids[:MaxCandidates]
	}
	out := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		res := topo.Resources[id]
		out = append(out, Candidate{ID: id, Kind: res.Kind, Name: res.Name})
	}
	return out
}

// allowFunc combines the Kinds list and the Filter predicate into one test over a
// resource. An empty Kinds list and a nil Filter allow everything.
func allowFunc(opt Options) func(domain.Resource) bool {
	var set map[domain.ResourceKind]bool
	if len(opt.Kinds) > 0 {
		set = make(map[domain.ResourceKind]bool, len(opt.Kinds))
		for _, k := range opt.Kinds {
			set[k] = true
		}
	}
	return func(res domain.Resource) bool {
		if set != nil && !set[res.Kind] {
			return false
		}
		if opt.Filter != nil && !opt.Filter(res) {
			return false
		}
		return true
	}
}

// FormatCandidates renders suggestions for a tool result. Kept here so every call site
// phrases a miss the same way.
func FormatCandidates(query string, cands []Candidate) string {
	if len(cands) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Did you mean one of these? Pass the full ID:\n")
	for _, c := range cands {
		b.WriteString("- ")
		b.WriteString(c.ID)
		b.WriteString(" (")
		b.WriteString(string(c.Kind))
		b.WriteString(")\n")
	}
	return b.String()
}

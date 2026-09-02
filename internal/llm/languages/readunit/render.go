package readunit

import (
	"regexp"
	"sort"
	"strings"

	"aracne/internal/llm/languages/renderstate"
)

// Options tunes a batch render.
type Options struct {
	// IncludeIncoming emits the "# USED BY:" section (read.context_filter.include_incoming).
	IncludeIncoming bool
	// Unlimited disables the entry/byte budget. Used by tests and exports.
	Unlimited bool
	// State, when set, is the ledger this render shares with whoever BUILT the units.
	//
	// Bodies are assembled one unit at a time, before Render is ever called, so a fact only
	// Render knows -- "this enclosing type was already inlined" -- reaches body construction
	// too late unless the same ledger is threaded through both phases. Reading five methods
	// of one struct emitted the struct five times for exactly that reason.
	//
	// Pass a State that a previous, unrelated render already used and its import ledger will
	// suppress imports this response never showed; construct a fresh one per batch.
	State *renderstate.State
	// Locate turns a resource id into "path:start-end", or "" when it cannot. Non-nil
	// switches the context sections to line-range identification.
	//
	// WHY THIS IS A POST-PROCESS AND NOT FORTY EDITS. Context entries are written by ~40
	// call sites spread over five per-language packages, each formatting its own line. A
	// mode that had to be threaded into every one of them would be forty chances to miss
	// one, and the misses would be silent -- an entry that kept the old form reads as
	// perfectly normal output. Every entry passes through this function on its way out, and
	// its grammar is already pinned by tests/context_dedup_test.go, so rewriting here covers
	// all of them at one point that a test can hold still.
	Locate func(id string) string
}

// ctxEntry matches one rendered context entry: an id, an optional parenthesised qualifier,
// then the separator. Deliberately the same shape tests/context_dedup_test.go asserts.
// The separator may be absent: an external variable with no value renders as a bare
// "## <id>", and it deserves a span like every other entry.
var ctxEntry = regexp.MustCompile(`^((?:##\s+|\t+))(\S+?)((?:\s+\(([^)]*)\))?)(\s*[:=]|\s*$)`)

// withLocations rewrites each entry to carry the span the reader can act on.
//
// It NEVER rewrites a line whose token does not resolve. That is what keeps section headings
// ("## Implemented By", "## Used By", "## file <path>") intact: they are not resource ids, so
// Locate returns "" and the line is passed through untouched.
//
// The id is kept alongside the span rather than replaced. In a context block the id is what
// ties the entry to the symbol the reader just saw in the code above it; the span is what
// tells them how to fetch it. Both facts are load-bearing, and together they cost one short
// parenthesis.
func withLocations(section string, locate func(string) string) string {
	if locate == nil || section == "" {
		return section
	}
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		m := ctxEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		loc := locate(m[2])
		if loc == "" {
			continue
		}
		qualifier := loc
		if m[4] != "" {
			qualifier = m[4] + ", " + loc
		}
		lines[i] = m[1] + m[2] + " (" + qualifier + ")" + m[5] + line[len(m[0]):]
	}
	return strings.Join(lines, "\n")
}

// Render turns resolved units into the single document a `read` call returns.
//
// The order is load-bearing. Every body is written before any context is, so that by the time
// a neighbour is considered the exclusion ledger already knows about every resource the
// response shows as source -- including the ones a whole-file read pulled in implicitly. That
// ordering is the entire fix for the duplication this rework targets: previously each resource
// rendered its own context immediately, so a sibling in the same file was listed under
// "# CONTEXT:" before anything knew it would also be printed in full further down.
func Render(units []Unit, opt Options) string {
	units = dropCovered(units)
	if len(units) == 0 {
		return ""
	}

	st := opt.State
	if st == nil {
		st = renderstate.New()
	}
	if opt.Unlimited {
		st.MaxEntries, st.MaxBytes = 0, 0
	}
	for _, u := range units {
		st.ExcludeID(u.ID)
		for _, id := range u.Covers {
			st.ExcludeID(id)
		}
	}

	var b strings.Builder
	for _, g := range group(units) {
		writeGroup(&b, st, g)
	}

	writeContext(&b, st, units, opt)
	writeIncoming(&b, st, units, opt)
	return b.String()
}

// dropCovered removes a unit whose source is already inside another unit's body. Asking for a
// file and a function in it is a reasonable thing for a model to do -- the file simply wins,
// rather than printing the function twice.
func dropCovered(units []Unit) []Unit {
	if len(units) < 2 {
		return units
	}
	covered := map[string]bool{}
	for _, u := range units {
		for _, id := range u.Covers {
			if id != u.ID {
				covered[id] = true
			}
		}
	}
	if len(covered) == 0 {
		return units
	}
	out := units[:0:0]
	for _, u := range units {
		if covered[u.ID] {
			continue
		}
		out = append(out, u)
	}
	return out
}

// fileGroup is the units declared in one file, in source order.
type fileGroup struct {
	path  string
	label string
	fence string
	units []Unit
}

// importBlock returns the group's import renderer, or nil when the language has none.
func (g fileGroup) importBlock() func(imports, deps []string) string {
	for _, u := range g.units {
		if u.ImportBlock != nil {
			return u.ImportBlock
		}
	}
	return nil
}

// group buckets units by declaring file, keeping the paths in the order the caller asked for
// them and sorting within a bucket by line so a group reads top-to-bottom like the file does.
func group(units []Unit) []fileGroup {
	var order []string
	byPath := map[string][]Unit{}
	for _, u := range units {
		if _, seen := byPath[u.Path]; !seen {
			order = append(order, u.Path)
		}
		byPath[u.Path] = append(byPath[u.Path], u)
	}
	groups := make([]fileGroup, 0, len(order))
	for _, p := range order {
		us := byPath[p]
		sort.SliceStable(us, func(i, j int) bool { return us[i].Line < us[j].Line })
		label := us[0].Label
		if label == "" {
			label = p
		}
		groups = append(groups, fileGroup{path: p, label: label, fence: us[0].Fence, units: us})
	}
	return groups
}

// writeGroup emits one file's fence: the pooled import block, then each body separated by a
// blank line.
//
// Imports are deduped per GROUP, not per response. Two files that both import "os" must each
// say so; suppressing the second would make that group read as though it imported nothing.
func writeGroup(b *strings.Builder, st *renderstate.State, g fileGroup) {
	st.ResetImports()

	var imports, deps []string
	for _, u := range g.units {
		imports = append(imports, u.Imports...)
		deps = append(deps, u.Deps...)
	}

	b.WriteString("```")
	b.WriteString(g.label)
	b.WriteString("\n")

	if render := g.importBlock(); render != nil {
		b.WriteString(render(st.NewImports(imports), st.NewImports(deps)))
	}

	for i, u := range g.units {
		// One blank line between siblings, matching how they are spaced in the file itself.
		if i > 0 {
			b.WriteString("\n")
		}
		st.MarkRendered(u.Body)
		b.WriteString(strings.TrimRight(u.Body, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")
}

// writeContext emits the one "# CONTEXT:" section for the whole query, or nothing when every
// neighbour turned out to be something the response already showed in full.
func writeContext(b *strings.Builder, st *renderstate.State, units []Unit, opt Options) {
	var inner strings.Builder
	for _, u := range units {
		if u.Context != nil {
			u.Context(&inner, st)
		}
	}
	if inner.Len() == 0 {
		return
	}
	b.WriteString("# CONTEXT:\n")
	b.WriteString(withLocations(inner.String(), opt.Locate))
	b.WriteString(st.Trailer())
	b.WriteString("\n")
}

// writeIncoming emits the single "# USED BY:" section, deduped across every unit in the batch.
func writeIncoming(b *strings.Builder, st *renderstate.State, units []Unit, opt Options) {
	if !opt.IncludeIncoming {
		return
	}
	used := renderstate.New()
	used.MaxEntries, used.MaxBytes = st.MaxEntries, st.MaxBytes
	// Deliberately NOT subject to st's RestrictTo filter. That filter keeps a windowed read's
	// OUTGOING context to what the shown lines mention; incoming callers are a different
	// relation, and a caller's name almost never appears inside the code it calls -- applying
	// the same test here would silently delete the whole section.

	var inner strings.Builder
	for _, u := range units {
		for _, ref := range u.Incoming {
			if st.IsExcluded(string(ref.ID)) || !used.FirstTime(string(ref.ID)) {
				continue
			}
			line := "## " + ref.ID + " (" + string(ref.Kind) + "): " + describe(ref.Description) + "\n"
			if !used.Allow(len(line)) {
				continue
			}
			inner.WriteString(line)
		}
	}
	if inner.Len() == 0 {
		return
	}
	b.WriteString("# USED BY:\n")
	b.WriteString(withLocations(inner.String(), opt.Locate))
	b.WriteString(used.Trailer())
}

// describe is the shared "no description" fallback, kept here so the batch sections read the
// same as the per-language ones.
func describe(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

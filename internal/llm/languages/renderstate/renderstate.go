// Package renderstate carries the per-response bookkeeping a topology read needs to stop
// paying for the same bytes twice.
//
// WHY. A read renders the target resource and then a `# CONTEXT:` section of its
// neighbours. Every Full-visibility neighbour used to be rendered as a self-contained code
// block: its own fence, its own import list, and — for a method — its enclosing type's
// entire declaration. Nothing was shared between entries, so reading one method of a
// struct with N Full-visibility siblings emitted the struct declaration and the import
// list N+1 times. Measured against the raw source span of the resource being read, output
// ran 1.5x-10.1x, median ~3.3x.
//
// Nothing here changes what the model is told. Imports and enclosing types are still
// shown — once — and a repeat is replaced by a short back-reference. What it removes is
// the duplication, plus an unbounded tail: CONTEXT and USED BY had no cap of any kind, and
// `# USED BY` in particular is a full scan of the topology rendered in full.
package renderstate

import (
	"fmt"
	"strings"
)

// Defaults chosen so an ordinary read is unaffected and only a pathological one is cut.
const (
	// DefaultMaxEntries bounds a single section (CONTEXT or USED BY).
	DefaultMaxEntries = 40
	// DefaultMaxBytes bounds the rendered neighbourhood. A read that needs more than this
	// is not answering a question, it is dumping a subgraph.
	DefaultMaxBytes = 24 * 1024
)

// State is per-render. It must not be shared between concurrent renders.
type State struct {
	imports  map[string]bool
	parents  map[string]bool
	entries  map[string]bool
	excluded map[string]bool
	// allowed, when non-nil, is the ONLY set of ids the context section may render. See
	// RestrictTo.
	allowed map[string]bool

	bytes   int
	shown   int
	omitted int

	MaxEntries int
	MaxBytes   int
}

// New returns a State with the default budget.
func New() *State {
	return &State{
		imports:    map[string]bool{},
		parents:    map[string]bool{},
		entries:    map[string]bool{},
		excluded:   map[string]bool{},
		MaxEntries: DefaultMaxEntries,
		MaxBytes:   DefaultMaxBytes,
	}
}

// Unlimited returns a State that dedups but never truncates. Used by callers that must
// render everything (tests, exports).
func Unlimited() *State {
	s := New()
	s.MaxEntries, s.MaxBytes = 0, 0
	return s
}

// NewImports returns the import lines not yet emitted in this response, marking them
// emitted. The first block a reader sees carries the bulk; later blocks shrink to the
// imports they alone introduce.
func (s *State) NewImports(lines []string) []string {
	if s == nil {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" || s.imports[l] {
			continue
		}
		s.imports[l] = true
		out = append(out, l)
	}
	return out
}

// ResetImports forgets which import lines have been emitted, without touching the entry,
// parent or exclusion sets.
//
// A batched read groups its results by containing file and gives each group its own import
// block. Those blocks are independent: two files that both import `os` must each say so, or
// the second group reads as though it had no imports at all. Everything else the State
// tracks stays response-wide, which is the whole point -- only the import ledger is
// per-group.
func (s *State) ResetImports() {
	if s == nil {
		return
	}
	s.imports = map[string]bool{}
}

// ExcludeID marks a resource as rendered in full somewhere in this response, so the CONTEXT
// section never repeats it.
//
// This is the invariant the batched read is built on: a resource the caller asked for -- and
// every resource that lives inside a file the caller asked for -- is already present as
// source. Listing it again under "# CONTEXT:" as an ID plus a description is pure
// duplication, and for a whole-file read it was an index of the fence directly above it.
//
// Distinct from FirstTime: FirstTime dedups WITHIN the context section, ExcludeID keeps the
// body's contents OUT of it.
func (s *State) ExcludeID(id string) {
	if s == nil || id == "" {
		return
	}
	s.excluded[id] = true
}

// IsExcluded reports whether `id` was rendered in full in the body of this response.
func (s *State) IsExcluded(id string) bool {
	if s == nil || id == "" {
		return false
	}
	return s.excluded[id]
}

// RestrictTo narrows the CONTEXT section to a specific set of resource ids. Passing nil (the
// default) restores the unrestricted behaviour.
//
// WHY. A windowed read shows a slice of a function, not the function. Its neighbours are the
// neighbours of the WHOLE declaration, so rendering all of them answers a question the caller
// did not ask -- `tail -2` on a 200-line function would come back with the context of all 200.
// The caller computes which resources the shown lines actually mention and passes them here.
//
// It is enforced inside Renderable rather than at each call site on purpose: Renderable is
// already the one check every language's renderer makes, so one condition here filters Go,
// Python, JS/TS, Rust and Java at once, and a language added later inherits it for free.
func (s *State) RestrictTo(ids map[string]bool) {
	if s == nil {
		return
	}
	s.allowed = ids
}

// permitted reports whether an id survives the RestrictTo filter.
func (s *State) permitted(id string) bool {
	if s == nil || s.allowed == nil {
		return true
	}
	return s.allowed[id]
}

// Renderable reports whether a neighbour with this ID should be rendered in the CONTEXT
// section: within any active restriction, not already shown as source in the body, and not
// already listed here. It is the single check every render* helper makes, so the rules cannot
// drift apart.
//
// The restriction is tested BEFORE FirstTime, which has a side effect: marking an id rendered
// when it was about to be filtered out would suppress it from a later, unrestricted section of
// the same response.
func (s *State) Renderable(id string) bool {
	if s == nil {
		return true
	}
	if !s.permitted(id) {
		return false
	}
	return !s.IsExcluded(id) && s.FirstTime(id)
}

// MarkRendered records a code cut as already shown, so a later entry that would repeat it
// as its enclosing type renders a back-reference instead.
//
// Needed because a type can appear BOTH as an entry in its own right and as another
// entry's parent: reading a function that touches `AuthConfig` and `AuthConfig.Token`
// rendered the struct declaration twice — once as the struct's own cut, once as the
// method's ParentCut — and comparing parents only to other parents never caught it.
func (s *State) MarkRendered(cut string) {
	if s == nil || cut == "" {
		return
	}
	s.parents[cut] = true
}

// ParentSeen reports whether this enclosing-type declaration has already been rendered,
// marking it seen. A repeat is what turned one struct into N+1 copies of itself.
func (s *State) ParentSeen(cut string) bool {
	if s == nil || cut == "" {
		return false
	}
	if s.parents[cut] {
		return true
	}
	s.parents[cut] = true
	return false
}

// FirstTime reports whether `id` has not been rendered yet in this response, marking it
// rendered. A neighbour reachable by two edges (a struct in StructsUsed and its method in
// CalledFunctions) used to render twice.
func (s *State) FirstTime(id string) bool {
	if s == nil || id == "" {
		return true
	}
	if s.entries[id] {
		return false
	}
	s.entries[id] = true
	return true
}

// Allow reports whether one more entry of roughly `size` bytes fits the budget. When it
// does not, the entry is counted as omitted so Trailer can report it.
func (s *State) Allow(size int) bool {
	if s == nil {
		return true
	}
	overEntries := s.MaxEntries > 0 && s.shown >= s.MaxEntries
	overBytes := s.MaxBytes > 0 && s.bytes+size > s.MaxBytes
	if overEntries || overBytes {
		s.omitted++
		return false
	}
	s.shown++
	s.bytes += size
	return true
}

// Omitted is how many entries the budget suppressed.
func (s *State) Omitted() int {
	if s == nil {
		return 0
	}
	return s.omitted
}

// Trailer renders the "not shown" note, or "" when nothing was omitted. Always say what
// was withheld: silent truncation reads as "that is all there is".
func (s *State) Trailer() string {
	if s == nil || s.omitted == 0 {
		return ""
	}
	return fmt.Sprintf("… %d more related resource(s) not shown (read them directly by ID).\n",
		s.omitted)
}

// SectionGuard bounds one rendered section by watching a builder's growth.
//
// The renderers write straight into a strings.Builder, so an entry's size is not known
// before it is written. Measuring the builder before and after each entry gives the same
// protection without restructuring them: once the section exceeds its byte budget the rest
// are counted and skipped, and Trailer says how many.
type SectionGuard struct {
	st    *State
	b     *strings.Builder
	start int
}

// Guard begins bounding a section at the builder's current length.
func (s *State) Guard(b *strings.Builder) *SectionGuard {
	return &SectionGuard{st: s, b: b, start: b.Len()}
}

// More reports whether another entry may be rendered. Call it before each entry.
func (g *SectionGuard) More() bool {
	if g == nil || g.st == nil {
		return true
	}
	if g.st.MaxBytes > 0 && g.b.Len()-g.start >= g.st.MaxBytes {
		g.st.omitted++
		return false
	}
	if g.st.MaxEntries > 0 && g.st.shown >= g.st.MaxEntries {
		g.st.omitted++
		return false
	}
	g.st.shown++
	return true
}

// BackRef is the one-line stand-in for an enclosing type already shown above.
func BackRef(b *strings.Builder, comment string) {
	b.WriteString(comment)
	b.WriteString(" (shown above)\n\n")
}

// ElisionMarker is the one-line stand-in for source a body chose not to inline verbatim.
//
// Two rules make this text load-bearing rather than cosmetic:
//
// It must never read as source. `edit` matches old_string against the bytes on disk, so a
// marker styled like a plausible comment invites a model to build old_string from text that
// was never in the file and get a confusing "not found". The leading U+22EF opens no comment
// in any language aracne scans, which is exactly why it was chosen.
//
// It must not claim a position. Bodies are built in the caller's request order but rendered
// sorted by (path, line), so "shown above" — which BackRef can say safely inside the single
// pass that builds the CONTEXT section — would be a lie here often enough to matter. Naming
// the ID tells the model what to do next without asserting where anything sits.
func ElisionMarker(id, reason string) string {
	return fmt.Sprintf("⋯ %s not shown here (%s) — read %q for its source ⋯\n\n", id, reason, id)
}

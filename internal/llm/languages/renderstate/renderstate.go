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
	imports map[string]bool
	parents map[string]bool
	entries map[string]bool

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

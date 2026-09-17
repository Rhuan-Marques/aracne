// Package readunit is the language-neutral shape a single resolved resource takes on its way
// into a read response.
//
// WHY. `read` takes a LIST of ids and answers with one document: resources grouped under the
// file that declares them, each group carrying its own import block, and a single
// "# CONTEXT:" section for the whole query. Producing that needs two things the old
// per-resource formatters could not give:
//
//   - the body and the context have to be written at different TIMES (every body first, then
//     one context), where each Format*Context wrote both in a single pass; and
//   - imports have to be poolable across the resources sharing a file, where each formatter
//     emitted its own import block inline.
//
// A Unit separates those parts without moving any language-specific rendering out of the
// language packages: Body is a plain string, Imports/Deps are plain lines, and Context is a
// closure the owning package supplies. The batch renderer stays free of any knowledge of Go,
// Python, JS, Rust or Java.
package readunit

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Unit is one resolved resource, decomposed into the pieces the batch renderer schedules
// independently.
type Unit struct {
	// ID is the canonical resource ID, used for ordering and for the exclusion ledger.
	ID string
	// Kind is what the resolver decided this is.
	Kind domain.ResourceKind
	// Path is the file that declares the resource; it is the grouping key.
	Path string
	// Label is how that file is written in the group header. It is the path relative to the
	// topology root where one is known, because Path is an absolute file ID and repeating a
	// long absolute prefix on every group is pure overhead -- measurably so on small
	// resources, where the header rivalled the source it introduced. Empty falls back to Path.
	Label string
	// Line is the resource's first line, the sort key inside a group.
	Line int
	// Fence is the info string for the group's code fence ("go", "python", ...).
	Fence string
	// Imports and Deps are the raw import tokens this resource needs -- package paths for
	// Go, module names for Python, and so on. They are pooled across the group and deduped
	// per group, not per response.
	Imports []string
	Deps    []string
	// ImportBlock renders the group's merged, deduped tokens into that language's import
	// syntax. Go needs an `import ( ... )` wrapper around its lines, Python a comment suffix
	// per line; keeping the syntax here is what lets the renderer pool imports across a group
	// without learning any of it. Taken from the first unit in a group, since a file's units
	// all share a language. Nil means the language emits no import block.
	ImportBlock func(imports, deps []string) string
	// Body is the source to place inside the group's fence, with no fence or imports of its
	// own.
	Body string
	// Context writes this resource's neighbours into the shared "# CONTEXT:" section. It is
	// called after every body in the response has been written, so the exclusion ledger is
	// complete by then and nothing already shown as source can leak back in. Nil when the
	// resource has no neighbours.
	Context func(b *strings.Builder, st *renderstate.State)
	// Incoming feeds the single "# USED BY:" section when read.context_filter turns it on.
	Incoming []domain.ResourceRef
	// Annotations are inline notes the caller asked for on specific lines of Body.
	//
	// APPLIED AT RENDER TIME, in writeGroup, deliberately NOT baked into Body. Body's exact
	// text is load-bearing in four places -- dropRepeatedSource's Contains test, bodyBytes'
	// over-serve denominator, renderstate.MarkRendered, and the abridger -- and every one of
	// them wants the source as the file has it. Decorating it on the way out keeps all four
	// seeing the bytes they have always seen.
	Annotations []Annotation
	// Covers lists resources this unit renders in full beyond ID itself. A file read covers
	// every declaration inside it -- that is what keeps a file's own functions out of the
	// context section.
	Covers []string
}

// HasContext reports whether this unit contributes anything to the context section.
func (u Unit) HasContext() bool { return u.Context != nil }

package readunit

import (
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

	st := renderstate.New()
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

	writeContext(&b, st, units)
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
func writeContext(b *strings.Builder, st *renderstate.State, units []Unit) {
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
	b.WriteString(inner.String())
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
	b.WriteString(inner.String())
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

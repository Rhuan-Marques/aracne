package pythontools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// wantVis reports whether an item of the given visibility belongs in the
// current render pass: the `full` pass takes Full items, the other takes the
// rest (Normal). Used to render Full entries before Normal ones.
func wantVis(v domain.Visibility, full bool) bool {
	return (v == domain.VisibilityFull) == full
}

// Removes duplicate strings from a slice while preserving order.
func dedupStr(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Writes internal and external import statements to a string builder with source annotations.
func writePyImports(b *strings.Builder, modules []python.PackagePath, deps []python.DependancyPath) {
	if len(modules) == 0 && len(deps) == 0 {
		return
	}
	for _, p := range modules {
		b.WriteString(fmt.Sprintf("import %s  # internal\n", p))
	}
	for _, d := range deps {
		b.WriteString(fmt.Sprintf("import %s  # external\n", d))
	}
	b.WriteString("\n")
}

// writeFullBlock renders a neighbor's full source cut (imports, optional parent
// class, then the resource's own cut) as a fenced code block.
// Imports and the enclosing type are emitted only the FIRST time they appear in a
// response; a repeat becomes a one-line back-reference. See renderstate.
func writeFullBlock(b *strings.Builder, st *renderstate.State, full *python.FullBlock) {
	if full == nil {
		return
	}
	b.WriteString("```python\n")
	writePyImports(b, st.NewImports(dedupStr(full.Imports)), st.NewImports(dedupStr(full.Deps)))
	if full.ParentCut != "" {
		if st.ParentSeen(full.ParentCut) {
			renderstate.BackRef(b, "// enclosing type")
		} else {
			b.WriteString(full.ParentCut)
			b.WriteString("\n\n")
		}
	}
	// Guarded the same way ParentCut is. A neighbour reachable by two edges -- a struct in
	// StructsUsed and again as an interface implementation, say -- reached here twice and
	// printed its whole body both times, because only ParentCut was ever compared.
	if st.ParentSeen(full.Cut) {
		renderstate.BackRef(b, "// source")
	} else {
		b.WriteString(full.Cut)
		if !strings.HasSuffix(full.Cut, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("```\n")
}

// Formats a Python function into markdown with ID, description, and full source block if visibility is full.
func renderFunc(b *strings.Builder, st *renderstate.State, prefix string, fn python.SimplifiedFunction) {
	if fn.Visibility == domain.VisibilityFull && fn.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", fn.ID, desc(fn.Description)))
		writeFullBlock(b, st, fn.Full)
		return
	}
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, fn.ID, desc(fn.Description)))
}

// Renders a Python class usage entry with description, full code block if visible, and methods.
func renderClassUsage(b *strings.Builder, st *renderstate.State, cu python.ClassUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", cu.ID, desc(cu.Description)))
	if cu.Visibility == domain.VisibilityFull && cu.Full != nil {
		writeFullBlock(b, st, cu.Full)
	}
	for _, m := range cu.Methods {
		renderFunc(b, st, "\t", m)
	}
}

// Renders an external variable entry with full code block if visible, or a compact format with optional value.
func renderExtVar(b *strings.Builder, st *renderstate.State, ev python.SimplifiedExtVar) {
	if ev.Visibility == domain.VisibilityFull && ev.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", ev.ID, desc(ev.Description)))
		writeFullBlock(b, st, ev.Full)
		return
	}
	valStr := ""
	if ev.Value != "" {
		valStr = fmt.Sprintf(" = %s", ev.Value)
	}
	b.WriteString(fmt.Sprintf("## %s%s\n", ev.ID, valStr))
}

// Writes a markdown section listing resources that use the current dependency.
// Capped: built by scanning every function/class in the topology and previously
// rendered in full with no limit.
func writeUsedBy(b *strings.Builder, st *renderstate.State, incoming []domain.ResourceRef) {
	if len(incoming) == 0 {
		return
	}
	used := renderstate.New()
	if st != nil {
		used.MaxEntries, used.MaxBytes = st.MaxEntries, st.MaxBytes
	}
	b.WriteString("# USED BY:\n")
	for _, ref := range incoming {
		line := fmt.Sprintf("## %s (%s): %s\n", ref.ID, string(ref.Kind), desc(ref.Description))
		if !used.Allow(len(line)) {
			continue
		}
		b.WriteString(line)
	}
	b.WriteString(used.Trailer())
}

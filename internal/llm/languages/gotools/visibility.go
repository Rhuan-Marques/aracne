package gotools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/renderstate"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
)

// wantVis reports whether an item of the given visibility belongs in the
// current render pass: the `full` pass takes Full items, the other takes the
// rest (Normal). Used to render Full entries before Normal ones.
func wantVis(v domain.Visibility, full bool) bool {
	return (v == domain.VisibilityFull) == full
}

// Removes duplicate strings from a slice while preserving order
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

// writeFullBlock renders a neighbor's full source cut (imports, optional parent
// struct, then the resource's own cut) as a fenced Go code block.
//
// Imports and the enclosing type are emitted only the FIRST time they appear in a
// response. Previously every Full neighbour repeated both, so a method on a struct with N
// Full siblings emitted that struct and its imports N+1 times.
func writeFullBlock(b *strings.Builder, st *renderstate.State, full *golang.FullBlock) {
	if full == nil {
		return
	}
	b.WriteString("```go\n")
	writeImports(b, st.NewImports(dedupStr(full.Imports)), st.NewImports(dedupStr(full.Deps)))
	if full.ParentCut != "" {
		if st.ParentSeen(full.ParentCut) {
			renderstate.BackRef(b, "// enclosing type")
		} else {
			b.WriteString(full.ParentCut)
			b.WriteString("\n\n")
		}
	}
	b.WriteString(full.Cut)
	// Register the cut so a later entry that encloses this type back-references it.
	st.MarkRendered(full.Cut)
	if !strings.HasSuffix(full.Cut, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
}

// renderFunc renders a function/method neighbor. Full neighbors get a "## ID"
// header followed by a code block; others use the given prefix ("## " for
// top-level, "\t"/"\t\t" for nested normal entries).
func renderFunc(b *strings.Builder, st *renderstate.State, prefix string, fn golang.SimplifiedFunction) {
	if fn.Visibility == domain.VisibilityFull && fn.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", fn.ID, desc(fn.Description)))
		writeFullBlock(b, st, fn.Full)
		return
	}
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, fn.ID, desc(fn.Description)))
}

// Formats a struct definition with its methods and optional full source block.
func renderStructUsage(b *strings.Builder, st *renderstate.State, su golang.StructUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", su.ID, desc(su.Description)))
	if su.Visibility == domain.VisibilityFull && su.Full != nil {
		writeFullBlock(b, st, su.Full)
	}
	for _, m := range su.Methods {
		renderFunc(b, st, "\t", m)
	}
}

// Formats a struct's interface implementation with its methods and optional full source block.
func renderImpl(b *strings.Builder, st *renderstate.State, prefix string, impl golang.InterfaceImplementation) {
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, impl.StructID, desc(impl.Description)))
	if impl.Visibility == domain.VisibilityFull && impl.Full != nil {
		writeFullBlock(b, st, impl.Full)
	}
	for _, m := range impl.Methods {
		renderFunc(b, st, prefix+"\t", m)
	}
}

// Formats an interface definition with all its implementations and their methods.
func renderInterfaceUsage(b *strings.Builder, st *renderstate.State, iu golang.InterfaceUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", iu.ID, desc(iu.Description)))
	if iu.Visibility == domain.VisibilityFull && iu.Full != nil {
		writeFullBlock(b, st, iu.Full)
	}
	for _, impl := range iu.Implementations {
		renderImpl(b, st, "\t", impl)
	}
}

// Formats an external variable reference with optional full source block and value assignment.
func renderExtVar(b *strings.Builder, st *renderstate.State, ev golang.SimplifiedExtVar) {
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

// writeUsedBy renders the incoming-connections section (always Normal).
//
// Capped: this list is built by scanning every function, struct and interface in the
// topology, and was rendered in full with no limit.
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
		line := fmt.Sprintf("## %s (%s): %s\n", ref.ID, resourceKindLabel(ref.Kind), desc(ref.Description))
		if !used.Allow(len(line)) {
			continue
		}
		b.WriteString(line)
	}
	b.WriteString(used.Trailer())
}

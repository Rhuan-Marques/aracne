package javatools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// wantVis reports whether an item of the given read.context_filter visibility belongs in the
// current render pass: the `full` pass takes Full items, the other takes the rest (Normal).
// Used to render Full entries before Normal ones. (Not the Java access modifier.)
func wantVis(v domain.Visibility, full bool) bool {
	return (v == domain.VisibilityFull) == full
}

// dedupStr removes duplicate strings from a slice while preserving order.
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

// writeFullBlock renders a neighbour's full source cut (dependencies, optional enclosing
// type, then the resource's own cut) as a fenced code block. Dependencies and the enclosing
// type are emitted only the FIRST time they appear in a response; a repeat becomes a one-line
// back-reference. See renderstate.
func writeFullBlock(b *strings.Builder, st *renderstate.State, full *java.FullBlock) {
	if full == nil {
		return
	}
	b.WriteString("```java\n")
	b.WriteString(javaImportBlock(nil, st.NewImports(dedupStr(importTokens(full.Deps)))))
	if full.ParentCut != "" {
		if st.ParentSeen(full.ParentCut) {
			renderstate.BackRef(b, "// enclosing type")
		} else {
			b.WriteString(full.ParentCut)
			b.WriteString("\n\n")
		}
	}
	if st.ParentSeen(full.Cut) {
		renderstate.BackRef(b, "// source")
	} else {
		writeCut(b, full.Cut)
	}
	b.WriteString("```\n")
}

// renderFunc renders a method neighbour. A Full one gets a "## ID" header followed by its
// source block; the others use the given prefix ("## " top-level, "\t" nested).
func renderFunc(b *strings.Builder, st *renderstate.State, prefix string, fn java.SimplifiedFunction) {
	if fn.Visibility == domain.VisibilityFull && fn.Full != nil {
		fmt.Fprintf(b, "## %s: %s\n", fn.ID, desc(fn.Description))
		writeFullBlock(b, st, fn.Full)
		return
	}
	fmt.Fprintf(b, "%s%s: %s\n", prefix, fn.ID, desc(fn.Description))
}

// renderStructUsage renders a used class, its source block when Full, and its listed methods.
func renderStructUsage(b *strings.Builder, st *renderstate.State, su java.StructUsage) {
	fmt.Fprintf(b, "## %s: %s\n", su.ID, desc(su.Description))
	if su.Visibility == domain.VisibilityFull && su.Full != nil {
		writeFullBlock(b, st, su.Full)
	}
	for _, m := range su.Methods {
		if st.Renderable(string(m.ID)) {
			renderFunc(b, st, "\t", m)
		}
	}
}

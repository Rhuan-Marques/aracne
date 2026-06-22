package gotools

import (
	"fmt"
	"strings"

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
func writeFullBlock(b *strings.Builder, full *golang.FullBlock) {
	if full == nil {
		return
	}
	b.WriteString("```go\n")
	writeImports(b, dedupStr(full.Imports), dedupStr(full.Deps))
	if full.ParentCut != "" {
		b.WriteString(full.ParentCut)
		b.WriteString("\n\n")
	}
	b.WriteString(full.Cut)
	if !strings.HasSuffix(full.Cut, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
}

// renderFunc renders a function/method neighbor. Full neighbors get a "## ID"
// header followed by a code block; others use the given prefix ("## " for
// top-level, "\t"/"\t\t" for nested normal entries).
func renderFunc(b *strings.Builder, prefix string, fn golang.SimplifiedFunction) {
	if fn.Visibility == domain.VisibilityFull && fn.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", fn.ID, desc(fn.Description)))
		writeFullBlock(b, fn.Full)
		return
	}
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, fn.ID, desc(fn.Description)))
}

// Formats a struct definition with its methods and optional full source block.
func renderStructUsage(b *strings.Builder, su golang.StructUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", su.ID, desc(su.Description)))
	if su.Visibility == domain.VisibilityFull && su.Full != nil {
		writeFullBlock(b, su.Full)
	}
	for _, m := range su.Methods {
		renderFunc(b, "\t", m)
	}
}

// Formats a struct's interface implementation with its methods and optional full source block.
func renderImpl(b *strings.Builder, prefix string, impl golang.InterfaceImplementation) {
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, impl.StructID, desc(impl.Description)))
	if impl.Visibility == domain.VisibilityFull && impl.Full != nil {
		writeFullBlock(b, impl.Full)
	}
	for _, m := range impl.Methods {
		renderFunc(b, prefix+"\t", m)
	}
}

// Formats an interface definition with all its implementations and their methods.
func renderInterfaceUsage(b *strings.Builder, iu golang.InterfaceUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", iu.ID, desc(iu.Description)))
	if iu.Visibility == domain.VisibilityFull && iu.Full != nil {
		writeFullBlock(b, iu.Full)
	}
	for _, impl := range iu.Implementations {
		renderImpl(b, "\t", impl)
	}
}

// Formats an external variable reference with optional full source block and value assignment.
func renderExtVar(b *strings.Builder, ev golang.SimplifiedExtVar) {
	if ev.Visibility == domain.VisibilityFull && ev.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", ev.ID, desc(ev.Description)))
		writeFullBlock(b, ev.Full)
		return
	}
	valStr := ""
	if ev.Value != "" {
		valStr = fmt.Sprintf(" = %s", ev.Value)
	}
	b.WriteString(fmt.Sprintf("## %s%s\n", ev.ID, valStr))
}

// writeUsedBy renders the incoming-connections section (always Normal).
func writeUsedBy(b *strings.Builder, incoming []domain.ResourceRef) {
	if len(incoming) == 0 {
		return
	}
	b.WriteString("# USED BY:\n")
	for _, ref := range incoming {
		b.WriteString(fmt.Sprintf("## %s (%s): %s\n", ref.ID, resourceKindLabel(ref.Kind), desc(ref.Description)))
	}
}

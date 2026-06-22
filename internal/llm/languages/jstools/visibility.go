package jstools

import (
	"fmt"
	"strings"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/javascript"
)

// wantVis reports whether an item of the given visibility belongs in the
// current render pass: the `full` pass takes Full items, the other takes the
// rest (Normal). Used to render Full entries before Normal ones.
func wantVis(v domain.Visibility, full bool) bool {
	return (v == domain.VisibilityFull) == full
}

// Deduplicates string slices, preserving order and returning the original if empty.
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

// Writes import comments for internal modules and external dependencies to a string builder.
func writeJSImports(b *strings.Builder, modules []javascript.PackagePath, deps []javascript.DependancyPath) {
	if len(modules) == 0 && len(deps) == 0 {
		return
	}
	for _, p := range modules {
		b.WriteString(fmt.Sprintf("// import from %s (internal)\n", p))
	}
	for _, d := range deps {
		b.WriteString(fmt.Sprintf("// import from %s (external)\n", d))
	}
	b.WriteString("\n")
}

// writeFullBlock renders a neighbor's full source cut (imports, optional parent
// class, then the resource's own cut) as a fenced code block.
func writeFullBlock(b *strings.Builder, full *javascript.FullBlock) {
	if full == nil {
		return
	}
	b.WriteString("```javascript\n")
	writeJSImports(b, dedupStr(full.Imports), dedupStr(full.Deps))
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

// Renders a JavaScript function entry with ID, description, and full source block if visibility is full.
func renderFunc(b *strings.Builder, prefix string, fn javascript.SimplifiedFunction) {
	if fn.Visibility == domain.VisibilityFull && fn.Full != nil {
		b.WriteString(fmt.Sprintf("## %s: %s\n", fn.ID, desc(fn.Description)))
		writeFullBlock(b, fn.Full)
		return
	}
	b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, fn.ID, desc(fn.Description)))
}

// Renders a JavaScript class usage entry with description, full source block if visibility is full, and all methods.
func renderClassUsage(b *strings.Builder, cu javascript.ClassUsage) {
	b.WriteString(fmt.Sprintf("## %s: %s\n", cu.ID, desc(cu.Description)))
	if cu.Visibility == domain.VisibilityFull && cu.Full != nil {
		writeFullBlock(b, cu.Full)
	}
	for _, m := range cu.Methods {
		renderFunc(b, "\t", m)
	}
}

// Renders an external variable entry showing ID, optional value, description, and full source block if visibility is full.
func renderExtVar(b *strings.Builder, ev javascript.SimplifiedExtVar) {
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

// Formats incoming resource references into a "USED BY" section for display.
func writeUsedBy(b *strings.Builder, incoming []domain.ResourceRef) {
	if len(incoming) == 0 {
		return
	}
	b.WriteString("# USED BY:\n")
	for _, ref := range incoming {
		b.WriteString(fmt.Sprintf("## %s (%s): %s\n", ref.ID, string(ref.Kind), desc(ref.Description)))
	}
}

package javatools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// javaImportBlock renders a group's pooled dependencies as import comments.
func javaImportBlock(imports, deps []string) string {
	if len(imports) == 0 && len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range append(append([]string{}, imports...), deps...) {
		fmt.Fprintf(&b, "// import %s (external dependency)\n", d)
	}
	b.WriteString("\n")
	return b.String()
}

// importTokens converts typed path slices into the plain tokens a Unit pools.
func importTokens[T ~string](in []T) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, string(v))
	}
	return out
}

// line renders one "## id: description" context entry, honouring the render ledger.
func line(b *strings.Builder, st *renderstate.State, id, suffix, description string) {
	if !st.Renderable(id) {
		return
	}
	fmt.Fprintf(b, "## %s%s: %s\n", id, suffix, desc(description))
}

// FunctionUnit decomposes a Java method or constructor read.
func FunctionUnit(ctx *java.JavaFunctionContext, st *renderstate.State) readunit.Unit {
	u := readunit.Unit{
		Kind:        domain.ResourceFunction,
		Fence:       "java",
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: javaImportBlock,
	}
	var body strings.Builder
	inlinedParent := ctx.ParentStruct != nil
	// A batch that asks for several members of one type used to inline the type once per
	// member. st is nil for single-unit callers, where ParentSeen reports false.
	elidedParent := inlinedParent && st.ParentSeen(ctx.ParentStruct.Cut)
	if inlinedParent {
		if elidedParent {
			body.WriteString(renderstate.ElisionMarker(string(ctx.ParentStruct.ID),
				"enclosing type already shown in this response"))
		} else {
			body.WriteString(ctx.ParentStruct.Cut)
		}
	}
	if ctx.Function != nil {
		u.ID = string(ctx.Function.ID)
		u.Path = ctx.Function.Loc.Path
		u.Line = ctx.Function.Loc.StartsAt
		// A Java class cut is the whole class, so an inlined parent already contains the
		// method; appending it again printed it twice.
		if !inlinedParent || elidedParent || !ctx.ParentStruct.Loc.Contains(ctx.Function.Loc) {
			if inlinedParent && !elidedParent {
				body.WriteString("\n\n")
			}
			body.WriteString(ctx.Function.Cut)
		}
	}
	u.Body = body.String()

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.ParentStruct != nil {
			st.MarkRendered(ctx.ParentStruct.Cut)
		}
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing class): %s\n", p.ID, desc(p.Description))
		}
		g := st.Guard(b)
		for _, su := range ctx.StructsUsed {
			if !st.Renderable(string(su.ID)) || !g.More() {
				continue
			}
			fmt.Fprintf(b, "## %s: %s\n", su.ID, desc(su.Description))
			for _, m := range su.Methods {
				if st.Renderable(string(m.ID)) {
					fmt.Fprintf(b, "\t%s: %s\n", m.ID, desc(m.Description))
				}
			}
		}
		for _, iu := range ctx.InterfacesUsed {
			if g.More() {
				line(b, st, string(iu.ID), " (interface)", iu.Description)
			}
		}
		for _, cf := range ctx.CalledFunctions {
			if g.More() {
				line(b, st, string(cf.ID), "", cf.Description)
			}
		}
	}
	return u
}

// StructUnit decomposes a Java class/enum/record read.
func StructUnit(ctx *java.JavaStructContext) readunit.Unit {
	u := readunit.Unit{
		Kind:        domain.ResourceStruct,
		Fence:       "java",
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: javaImportBlock,
	}
	if ctx.Struct != nil {
		u.ID = string(ctx.Struct.ID)
		u.Path = ctx.Struct.Loc.Path
		u.Line = ctx.Struct.Loc.StartsAt
		u.Body = ctx.Struct.Cut
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.Struct != nil {
			st.MarkRendered(ctx.Struct.Cut)
		}
		g := st.Guard(b)
		if ctx.IsEnum && len(ctx.Variants) > 0 {
			fmt.Fprintf(b, "## variants: %s\n", strings.Join(ctx.Variants, ", "))
		}
		if ctx.IsRecord && len(ctx.Components) > 0 {
			fmt.Fprintf(b, "## components: %s\n", joinComponents(ctx.Components))
		}
		if ctx.Constructor != nil {
			line(b, st, string(ctx.Constructor.ID), " (constructor)", ctx.Constructor.Description)
		}
		for _, s := range ctx.Inherits {
			if g.More() {
				line(b, st, string(s.ID), " (extends)", s.Description)
			}
		}
		for _, t := range ctx.Implements {
			if g.More() {
				line(b, st, string(t.ID), " (implements)", t.Description)
			}
		}
		for _, m := range ctx.Methods {
			if g.More() {
				line(b, st, string(m.ID), "", m.Description)
			}
		}
		for _, su := range ctx.StructsUsed {
			if !st.Renderable(string(su.ID)) || !g.More() {
				continue
			}
			fmt.Fprintf(b, "## %s: %s\n", su.ID, desc(su.Description))
			for _, mm := range su.Methods {
				if st.Renderable(string(mm.ID)) {
					fmt.Fprintf(b, "\t%s: %s\n", mm.ID, desc(mm.Description))
				}
			}
		}
	}
	return u
}

// InterfaceUnit decomposes a Java interface/annotation read.
func InterfaceUnit(ctx *java.JavaInterfaceContext) readunit.Unit {
	u := readunit.Unit{Kind: domain.ResourceInterface, Fence: "java"}
	if ctx.Interface != nil {
		u.ID = string(ctx.Interface.ID)
		u.Path = ctx.Interface.Loc.Path
		u.Line = ctx.Interface.Loc.StartsAt
		u.Body = ctx.Interface.Cut
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.Interface != nil {
			st.MarkRendered(ctx.Interface.Cut)
		}
		if ctx.IsAnnotation {
			b.WriteString("## (annotation type)\n")
		}
		g := st.Guard(b)
		for _, sup := range ctx.Supertypes {
			if g.More() {
				line(b, st, string(sup.ID), " (supertype)", sup.Description)
			}
		}
		if ctx.Interface != nil {
			for _, m := range ctx.Interface.Methods {
				if !m.HasDefault && !m.IsStatic {
					continue
				}
				kind := "default"
				if m.IsStatic {
					kind = "static"
				}
				fmt.Fprintf(b, "## %s %s(%s)\n", kind, m.Name, joinTypes(m.Input))
			}
		}
		var inner strings.Builder
		for _, impl := range ctx.ImplementedBy {
			if !st.Renderable(string(impl.ID)) || !g.More() {
				continue
			}
			fmt.Fprintf(&inner, "\t%s: %s\n", impl.ID, desc(impl.Description))
		}
		if inner.Len() > 0 {
			b.WriteString("## Implemented By\n")
			b.WriteString(inner.String())
		}
	}
	return u
}

// DependencyUnit decomposes a Java dependency read.
func DependencyUnit(ctx *java.JavaDependencyContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Dependency),
		Kind:  domain.ResourceDependency,
		Path:  string(ctx.Dependency),
		Fence: "java",
		Body:  fmt.Sprintf("// dependency %s", ctx.Dependency),
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if len(ctx.UsedBy) == 0 {
			return
		}
		var inner strings.Builder
		g := st.Guard(&inner)
		for _, usage := range ctx.UsedBy {
			if !st.Renderable(usage.ID) || !g.More() {
				continue
			}
			fmt.Fprintf(&inner, "\t%s (%s): %s\n", usage.ID, string(usage.Kind), desc(usage.Description))
		}
		if inner.Len() == 0 {
			return
		}
		b.WriteString("## Used By\n")
		b.WriteString(inner.String())
	}
	return u
}

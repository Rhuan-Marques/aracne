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
	// A Java class cut is the whole class, so it contains the member. When that class is
	// already in this response -- inlined by a sibling member, or read in its own right --
	// the member's source is already there too, and this unit has nothing left to print:
	// writing the member again after an elision marker is how batching two methods of one
	// class printed the second one twice.
	contained := inlinedParent && ctx.Function != nil && ctx.ParentStruct.Loc.Contains(ctx.Function.Loc)
	if inlinedParent {
		if !elidedParent {
			body.WriteString(ctx.ParentStruct.Cut)
		} else if !contained {
			body.WriteString(renderstate.ElisionMarker(string(ctx.ParentStruct.ID),
				"enclosing type already shown in this response"))
		}
	}
	if ctx.Function != nil {
		u.ID = string(ctx.Function.ID)
		u.Path = ctx.Function.Loc.Path
		u.Line = ctx.Function.Loc.StartsAt
		// A Java class cut is the whole class, so an inlined parent already contains the
		// method; appending it again printed it twice.
		if !contained {
			if inlinedParent && !elidedParent {
				body.WriteString("\n\n")
			}
			body.WriteString(ctx.Function.Cut)
		}
	}
	u.Body = body.String()
	u.Incoming = ctx.Incoming

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.ParentStruct != nil {
			st.MarkRendered(ctx.ParentStruct.Cut)
		}
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing class): %s\n", p.ID, desc(p.Description))
		}
		// The manager already applied read.context_filter: hidden neighbours are gone, and each
		// remaining one carries Normal or Full. Full entries render first, as source cuts.
		g := st.Guard(b)
		for _, full := range []bool{true, false} {
			for _, su := range ctx.StructsUsed {
				if wantVis(su.Visibility, full) && st.Renderable(string(su.ID)) && g.More() {
					renderStructUsage(b, st, su)
				}
			}
			for _, iu := range ctx.InterfacesUsed {
				if wantVis(iu.Visibility, full) && st.Renderable(string(iu.ID)) && g.More() {
					fmt.Fprintf(b, "## %s (interface): %s\n", iu.ID, desc(iu.Description))
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) && st.Renderable(string(cf.ID)) && g.More() {
					renderFunc(b, st, "## ", cf)
				}
			}
		}
	}
	return u
}

// StructUnit decomposes a Java class/enum/record read. st is the batch's render state, shared
// with the member units (nil for a single-unit caller).
func StructUnit(ctx *java.JavaStructContext, st *renderstate.State) readunit.Unit {
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
		// Registered with the batch's ledger at BUILD time, so a member of this class later in
		// the batch elides it rather than inlining it again; a member earlier in the batch that
		// already inlined it leaves nothing of it to print here.
		if !st.ParentSeen(ctx.Struct.Cut) {
			u.Body = ctx.Struct.Cut
		}
	}
	u.Incoming = ctx.Incoming
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.Struct != nil {
			st.MarkRendered(ctx.Struct.Cut)
		}
		g := st.Guard(b)
		// The relationship lines -- variants, components, constructor, extends, implements --
		// carry structure and render whatever read.context_filter says, as a Python base class
		// or a Go struct's interfaces do. The constructor goes first so that, listed again
		// among the methods, it keeps its "(constructor)" label rather than a Full cut.
		if ctx.IsEnum && len(ctx.Variants) > 0 {
			fmt.Fprintf(b, "## variants: %s\n", strings.Join(ctx.Variants, ", "))
		}
		if ctx.IsRecord && len(ctx.Components) > 0 {
			fmt.Fprintf(b, "## components: %s\n", joinComponents(ctx.Components))
		}
		if ctx.Constructor != nil {
			line(b, st, string(ctx.Constructor.ID), " (constructor)", ctx.Constructor.Description)
		}
		// Members and used classes went through the filter in the manager: Full ones render
		// first, as source cuts; hidden ones are already gone.
		for _, full := range []bool{true, false} {
			if !full {
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
			}
			for _, m := range ctx.Methods {
				if wantVis(m.Visibility, full) && st.Renderable(string(m.ID)) && g.More() {
					renderFunc(b, st, "## ", m)
				}
			}
			for _, su := range ctx.StructsUsed {
				if wantVis(su.Visibility, full) && st.Renderable(string(su.ID)) && g.More() {
					renderStructUsage(b, st, su)
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
	u.Incoming = ctx.Incoming
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

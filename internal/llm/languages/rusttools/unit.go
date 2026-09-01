package rusttools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/readunit"
	"aracne/internal/llm/languages/renderstate"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/rust"
)

// rustImportBlock renders a group's pooled crate dependencies as `use` comments. Rust has no
// internal-module import list on a read context, so only deps arrive here.
func rustImportBlock(imports, deps []string) string {
	if len(imports) == 0 && len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range append(append([]string{}, imports...), deps...) {
		fmt.Fprintf(&b, "// use %s (external crate)\n", d)
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

// FunctionUnit decomposes a Rust function or method read.
func FunctionUnit(ctx *rust.RustFunctionContext, st *renderstate.State) readunit.Unit {
	u := readunit.Unit{
		ID:          string(ctx.Function.ID),
		Kind:        domain.ResourceFunction,
		Path:        ctx.Function.Loc.Path,
		Line:        ctx.Function.Loc.StartsAt,
		Fence:       "rust",
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: rustImportBlock,
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
	// Only append the member when the inlined parent does not already contain it. A Go/Rust
	// type declaration does not; a Python/JS/Java class cut is the whole class and does, and
	// appending anyway printed the method twice.
	if !inlinedParent || elidedParent || !ctx.ParentStruct.Loc.Contains(ctx.Function.Loc) {
		if inlinedParent && !elidedParent {
			body.WriteString("\n\n")
		}
		body.WriteString(ctx.Function.Cut)
	}
	u.Body = body.String()

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.ParentStruct != nil {
			st.MarkRendered(ctx.ParentStruct.Cut)
		}
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing type): %s\n", p.ID, desc(p.Description))
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
		for _, tu := range ctx.TraitsUsed {
			if g.More() {
				line(b, st, string(tu.ID), " (trait)", tu.Description)
			}
		}
		for _, nt := range ctx.NamedTypesUsed {
			if g.More() {
				line(b, st, string(nt.ID), " (type)", nt.Description)
			}
		}
		for _, cf := range ctx.CalledFunctions {
			if g.More() {
				line(b, st, string(cf.ID), "", cf.Description)
			}
		}
		for _, v := range ctx.VarsUsed {
			if !st.Renderable(string(v.ID)) || !g.More() {
				continue
			}
			valStr := ""
			if v.Value != "" {
				valStr = fmt.Sprintf(" = %s", v.Value)
			}
			fmt.Fprintf(b, "## %s%s: %s\n", v.ID, valStr, desc(v.Description))
		}
	}
	return u
}

// StructUnit decomposes a Rust struct/enum/union read.
func StructUnit(ctx *rust.RustStructContext) readunit.Unit {
	u := readunit.Unit{
		ID:          string(ctx.Struct.ID),
		Kind:        domain.ResourceStruct,
		Path:        ctx.Struct.Loc.Path,
		Line:        ctx.Struct.Loc.StartsAt,
		Fence:       "rust",
		Body:        ctx.Struct.Cut,
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: rustImportBlock,
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.Struct.Cut)
		g := st.Guard(b)
		if ctx.IsEnum && len(ctx.Variants) > 0 {
			fmt.Fprintf(b, "## variants: %s\n", strings.Join(ctx.Variants, ", "))
		}
		if ctx.Constructor != nil {
			line(b, st, string(ctx.Constructor.ID), " (constructor)", ctx.Constructor.Description)
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
		for _, nt := range ctx.NamedTypesUsed {
			if g.More() {
				line(b, st, string(nt.ID), " (type)", nt.Description)
			}
		}
	}
	return u
}

// InterfaceUnit decomposes a Rust trait read.
func InterfaceUnit(ctx *rust.RustInterfaceContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Trait.ID),
		Kind:  domain.ResourceInterface,
		Path:  ctx.Trait.Loc.Path,
		Line:  ctx.Trait.Loc.StartsAt,
		Fence: "rust",
		Body:  ctx.Trait.Cut,
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.Trait.Cut)
		g := st.Guard(b)
		for _, s := range ctx.Supertraits {
			if g.More() {
				line(b, st, string(s.ID), " (supertrait)", s.Description)
			}
		}
		for _, impl := range ctx.Implementors {
			if g.More() {
				line(b, st, string(impl.ID), " (implements)", impl.Description)
			}
		}
	}
	return u
}

// NamedTypeUnit decomposes a Rust type-alias read.
func NamedTypeUnit(ctx *rust.RustNamedTypeContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.NamedType.ID),
		Kind:  domain.ResourceNamedType,
		Path:  ctx.NamedType.Loc.Path,
		Line:  ctx.NamedType.Loc.StartsAt,
		Fence: "rust",
		Body:  ctx.NamedType.Cut,
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.NamedType.Cut)
		writeUsageList(b, st, ctx.UsedBy)
	}
	return u
}

// DependencyUnit decomposes a Rust crate-dependency read.
func DependencyUnit(ctx *rust.RustDependencyContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Dependency),
		Kind:  domain.ResourceDependency,
		Path:  string(ctx.Dependency),
		Fence: "rust",
		Body:  fmt.Sprintf("// crate %s", ctx.Dependency),
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		writeUsageList(b, st, ctx.UsedBy)
	}
	return u
}

// writeUsageList renders a flat "Used By" list, skipping anything already shown in full.
func writeUsageList(b *strings.Builder, st *renderstate.State, usages []rust.ResourceUsage) {
	if len(usages) == 0 {
		return
	}
	var inner strings.Builder
	g := st.Guard(&inner)
	for _, u := range usages {
		if !st.Renderable(u.ID) || !g.More() {
			continue
		}
		fmt.Fprintf(&inner, "\t%s (%s): %s\n", u.ID, string(u.Kind), desc(u.Description))
	}
	if inner.Len() == 0 {
		return
	}
	b.WriteString("## Used By\n")
	b.WriteString(inner.String())
}

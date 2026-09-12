package rusttools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/rust"
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
	u.Incoming = ctx.Incoming

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.ParentStruct != nil {
			st.MarkRendered(ctx.ParentStruct.Cut)
		}
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing type): %s\n", p.ID, desc(p.Description))
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
			for _, tu := range ctx.TraitsUsed {
				if wantVis(tu.Visibility, full) && st.Renderable(string(tu.ID)) && g.More() {
					fmt.Fprintf(b, "## %s (trait): %s\n", tu.ID, desc(tu.Description))
				}
			}
			for _, nt := range ctx.NamedTypesUsed {
				if wantVis(nt.Visibility, full) && st.Renderable(string(nt.ID)) && g.More() {
					fmt.Fprintf(b, "## %s (type): %s\n", nt.ID, desc(nt.Description))
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) && st.Renderable(string(cf.ID)) && g.More() {
					renderFunc(b, st, "## ", cf)
				}
			}
			for _, v := range ctx.VarsUsed {
				if wantVis(v.Visibility, full) && st.Renderable(string(v.ID)) && g.More() {
					renderVar(b, st, v)
				}
			}
		}
	}
	return u
}

// StructUnit decomposes a Rust struct/enum/union read. st is the batch's render state, shared
// with the member units (nil for a single-unit caller).
func StructUnit(ctx *rust.RustStructContext, st *renderstate.State) readunit.Unit {
	u := readunit.Unit{
		ID:          string(ctx.Struct.ID),
		Kind:        domain.ResourceStruct,
		Path:        ctx.Struct.Loc.Path,
		Line:        ctx.Struct.Loc.StartsAt,
		Fence:       "rust",
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: rustImportBlock,
	}
	// Registered with the batch's ledger at BUILD time, so a method of this type later in the
	// batch elides the declaration rather than inlining it a second time; a method earlier in
	// the batch that already inlined it leaves nothing of it to print here.
	if !st.ParentSeen(ctx.Struct.Cut) {
		u.Body = ctx.Struct.Cut
	}
	u.Incoming = ctx.Incoming
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.Struct.Cut)
		g := st.Guard(b)
		// The relationship lines -- variants, constructor, implemented traits -- carry structure
		// and render whatever read.context_filter says, as a Python base class or a Go struct's
		// interfaces do. The constructor goes first so that, listed again among the methods, it
		// keeps its "(constructor)" label rather than a Full cut.
		if ctx.IsEnum && len(ctx.Variants) > 0 {
			fmt.Fprintf(b, "## variants: %s\n", strings.Join(ctx.Variants, ", "))
		}
		if ctx.Constructor != nil {
			line(b, st, string(ctx.Constructor.ID), " (constructor)", ctx.Constructor.Description)
		}
		// Methods and used types went through the filter in the manager: Full ones render
		// first, as source cuts; hidden ones are already gone.
		for _, full := range []bool{true, false} {
			if !full {
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
			for _, nt := range ctx.NamedTypesUsed {
				if wantVis(nt.Visibility, full) && st.Renderable(string(nt.ID)) && g.More() {
					fmt.Fprintf(b, "## %s (type): %s\n", nt.ID, desc(nt.Description))
				}
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
	u.Incoming = ctx.Incoming
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

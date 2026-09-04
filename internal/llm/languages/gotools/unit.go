package gotools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

// goImportBlock renders a group's pooled import tokens as one Go import statement.
func goImportBlock(imports, deps []string) string {
	if len(imports) == 0 && len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("import (\n")
	for _, p := range imports {
		fmt.Fprintf(&b, "\t%q\n", p)
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "\t%q\n", d)
	}
	b.WriteString(")\n\n")
	return b.String()
}

// importTokens converts the typed path slices into the plain tokens a Unit pools.
func importTokens[T ~string](in []T) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, string(v))
	}
	return out
}

// baseUnit fills the fields every Go unit shares.
func baseUnit(id string, kind domain.ResourceKind, loc domain.Location, imports, deps []string) readunit.Unit {
	return readunit.Unit{
		ID:          id,
		Kind:        kind,
		Path:        loc.Path,
		Line:        loc.StartsAt,
		Fence:       "go",
		Imports:     imports,
		Deps:        deps,
		ImportBlock: goImportBlock,
	}
}

// FunctionUnit decomposes a function/method read into its body, its pooled imports and a
// closure that writes its neighbours into the shared context section.
//
// The parent struct is part of the BODY, not the context: a method without its receiver type
// is hard to read. It is registered with the render state so the context section does not
// print it again.
func FunctionUnit(ctx *golang.GoFunctionContext, st *renderstate.State) readunit.Unit {
	u := baseUnit(string(ctx.Function.ID), domain.ResourceFunction, ctx.Function.Loc,
		importTokens(ctx.PackagesUsed), importTokens(ctx.Dependencies))

	var body strings.Builder
	inlinedParent := ctx.ParentStruct != nil
	// A batch that asks for five methods of one struct used to inline the struct five times.
	// st is nil for single-unit callers, where ParentSeen reports false and nothing changes.
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
	// appending anyway printed the method twice. An ELIDED parent contains nothing, so the
	// member always has to be written -- otherwise the body would be a marker and no code.
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
		// The enclosing type led the context when it was too big to inline: it is the single
		// most relevant neighbour of a method, and the model needs its ID to read it.
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing type): %s\n", p.ID, desc(p.Description))
		}
		g := st.Guard(b)
		for _, full := range []bool{true, false} {
			for _, iu := range ctx.InterfacesUsed {
				if wantVis(iu.Visibility, full) && st.Renderable(string(iu.ID)) && g.More() {
					renderInterfaceUsage(b, st, iu)
				}
			}
			for _, su := range ctx.StructsUsed {
				if wantVis(su.Visibility, full) && st.Renderable(string(su.ID)) && g.More() {
					renderStructUsage(b, st, su)
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) && st.Renderable(string(cf.ID)) && g.More() {
					renderFunc(b, st, "## ", cf)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) && st.Renderable(string(ev.ID)) && g.More() {
					renderExtVar(b, st, ev)
				}
			}
		}
	}
	return u
}

// StructUnit decomposes a struct read. The constructor rides along in the body for the same
// reason a method's receiver does: it is the type's front door.
func StructUnit(ctx *golang.GoStructContext) readunit.Unit {
	u := baseUnit(string(ctx.Struct.ID), domain.ResourceStruct, ctx.Struct.Loc,
		importTokens(ctx.PackagesUsed), importTokens(ctx.Dependencies))

	var body strings.Builder
	body.WriteString(ctx.Struct.Cut)
	if ctx.Constructor != nil {
		body.WriteString("\n\n")
		body.WriteString(ctx.Constructor.Cut)
	}
	u.Body = body.String()
	u.Incoming = ctx.Incoming
	if ctx.Constructor != nil {
		u.Covers = append(u.Covers, string(ctx.Constructor.ID))
	}

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		// Register both cuts before any neighbour renders. Without this the first
		// Full-visibility method re-emitted the whole struct declaration as its ParentCut.
		st.MarkRendered(ctx.Struct.Cut)
		if ctx.Constructor != nil {
			st.MarkRendered(ctx.Constructor.Cut)
		}
		g := st.Guard(b)
		for _, full := range []bool{true, false} {
			if !full {
				for _, iface := range ctx.Interfaces {
					if !st.Renderable(string(iface.ID)) {
						continue
					}
					need := ""
					if iface.NeedToImplement {
						need = " [NEED TO IMPLEMENT]"
					}
					fmt.Fprintf(b, "## %s: %s%s\n", iface.ID, desc(iface.Description), need)
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
			for _, iu := range ctx.InterfacesUsed {
				if wantVis(iu.Visibility, full) && st.Renderable(string(iu.ID)) && g.More() {
					renderInterfaceUsage(b, st, iu)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) && st.Renderable(string(ev.ID)) && g.More() {
					renderExtVar(b, st, ev)
				}
			}
		}
	}
	return u
}

// InterfaceUnit decomposes an interface read; its context is the implementations.
func InterfaceUnit(ctx *golang.GoInterfaceContext) readunit.Unit {
	u := baseUnit(string(ctx.Interface.ID), domain.ResourceInterface, ctx.Interface.Loc,
		importTokens(ctx.PackagesUsed), importTokens(ctx.Dependencies))
	u.Body = ctx.Interface.Cut
	u.Incoming = ctx.Incoming

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.Interface.Cut)
		if len(ctx.Implementations) == 0 {
			return
		}
		var inner strings.Builder
		g := st.Guard(&inner)
		for _, full := range []bool{true, false} {
			for _, impl := range ctx.Implementations {
				if wantVis(impl.Visibility, full) && st.Renderable(string(impl.StructID)) && g.More() {
					renderImpl(&inner, st, "\t", impl)
				}
			}
		}
		if inner.Len() == 0 {
			return
		}
		b.WriteString("## Implemented By\n")
		b.WriteString(inner.String())
	}
	return u
}

// NamedTypeUnit decomposes a named-type read; its context is the resources that use it.
func NamedTypeUnit(ctx *golang.GoNamedTypeContext) readunit.Unit {
	u := baseUnit(string(ctx.NamedType.ID), domain.ResourceNamedType, ctx.NamedType.Loc,
		importTokens(ctx.PackagesUsed), importTokens(ctx.Dependencies))
	u.Body = ctx.NamedType.Cut

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.NamedType.Cut)
		writeUsageList(b, st, "## Used By", ctx.UsedBy)
	}
	return u
}

// PackageUnit decomposes a package read. A package has no source of its own, so its body is a
// one-line header and its context is what it contains.
func PackageUnit(ctx *golang.GoPackageContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Package.Path),
		Kind:  domain.ResourcePackage,
		Path:  string(ctx.Package.Path),
		Fence: "go",
		Body:  fmt.Sprintf("// package %s", ctx.Package.Path),
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		g := st.Guard(b)
		for _, f := range ctx.Files {
			if st.Renderable(string(f)) && g.More() {
				fmt.Fprintf(b, "## file %s\n", f)
			}
		}
		for _, fn := range ctx.Functions {
			if st.Renderable(string(fn.ID)) && g.More() {
				fmt.Fprintf(b, "## func %s: %s\n", fn.ID, desc(fn.Description))
			}
		}
		for _, s := range ctx.Structs {
			if !st.Renderable(string(s.ID)) || !g.More() {
				continue
			}
			fmt.Fprintf(b, "## struct %s: %s\n", s.ID, desc(s.Description))
			for _, m := range s.Methods {
				if st.Renderable(string(m.ID)) {
					fmt.Fprintf(b, "\t%s: %s\n", m.ID, desc(m.Description))
				}
			}
		}
		for _, iface := range ctx.Interfaces {
			if st.Renderable(string(iface.ID)) && g.More() {
				fmt.Fprintf(b, "## interface %s: %s\n", iface.ID, desc(iface.Description))
			}
		}
		for _, nt := range ctx.NamedTypes {
			if st.Renderable(nt.ID) && g.More() {
				fmt.Fprintf(b, "## type %s: %s\n", nt.ID, desc(nt.Description))
			}
		}
		for _, ev := range ctx.ExtVars {
			if st.Renderable(string(ev.ID)) && g.More() {
				fmt.Fprintf(b, "## var %s: %s\n", ev.ID, desc(ev.Description))
			}
		}
		for _, d := range ctx.Dependencies {
			fmt.Fprintf(b, "## dep %q\n", d)
		}
	}
	return u
}

// DependencyUnit decomposes a dependency read: no source at all, just reverse usage.
func DependencyUnit(ctx *golang.GoDependencyContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Dependency),
		Kind:  domain.ResourceDependency,
		Path:  string(ctx.Dependency),
		Fence: "go",
		Body:  fmt.Sprintf("// dependency %s", ctx.Dependency),
	}
	u.Context = func(b *strings.Builder, st *renderstate.State) {
		writeUsageList(b, st, "## Used By", ctx.UsedBy)
	}
	return u
}

// writeUsageList renders a flat "used by" list under a heading, skipping anything the response
// already shows in full.
func writeUsageList(b *strings.Builder, st *renderstate.State, heading string, usages []golang.ResourceUsage) {
	if len(usages) == 0 {
		return
	}
	var inner strings.Builder
	g := st.Guard(&inner)
	for _, usage := range usages {
		if !st.Renderable(usage.ID) || !g.More() {
			continue
		}
		fmt.Fprintf(&inner, "\t%s (%s): %s\n", usage.ID, resourceKindLabel(usage.Kind), desc(usage.Description))
	}
	if inner.Len() == 0 {
		return
	}
	b.WriteString(heading)
	b.WriteString("\n")
	b.WriteString(inner.String())
}

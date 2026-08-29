package pythontools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/readunit"
	"aracne/internal/llm/languages/renderstate"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/python"
)

// pyImportBlock renders a group's pooled import tokens as Python import lines.
func pyImportBlock(imports, deps []string) string {
	if len(imports) == 0 && len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range imports {
		fmt.Fprintf(&b, "import %s  # internal\n", p)
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "import %s  # external\n", d)
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

// FunctionUnit decomposes a Python function or method read.
func FunctionUnit(ctx *python.PythonFunctionContext) readunit.Unit {
	u := readunit.Unit{
		ID:          string(ctx.Function.ID),
		Kind:        domain.ResourceFunction,
		Path:        ctx.Function.Loc.Path,
		Line:        ctx.Function.Loc.StartsAt,
		Fence:       "python",
		Imports:     importTokens(ctx.ModulesUsed),
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: pyImportBlock,
		Incoming:    ctx.Incoming,
	}

	var body strings.Builder
	inlinedParent := ctx.ParentClass != nil
	if inlinedParent {
		body.WriteString(ctx.ParentClass.Cut)
	}
	// Only append the member when the inlined parent does not already contain it. A Go/Rust
	// type declaration does not; a Python/JS/Java class cut is the whole class and does, and
	// appending anyway printed the method twice.
	if !inlinedParent || !ctx.ParentClass.Loc.Contains(ctx.Function.Loc) {
		if inlinedParent {
			body.WriteString("\n\n")
		}
		body.WriteString(ctx.Function.Cut)
	}
	u.Body = body.String()

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		if ctx.ParentClass != nil {
			st.MarkRendered(ctx.ParentClass.Cut)
		}
		if p := ctx.OversizedParent; p != nil && st.Renderable(string(p.ID)) {
			fmt.Fprintf(b, "## %s (enclosing class): %s\n", p.ID, desc(p.Description))
		}
		g := st.Guard(b)
		for _, full := range []bool{true, false} {
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) && st.Renderable(string(cu.ID)) && g.More() {
					renderClassUsage(b, st, cu)
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

// ClassUnit decomposes a Python class read.
func ClassUnit(ctx *python.PythonClassContext) readunit.Unit {
	u := readunit.Unit{
		ID:          string(ctx.Class.ID),
		Kind:        domain.ResourceStruct,
		Path:        ctx.Class.Loc.Path,
		Line:        ctx.Class.Loc.StartsAt,
		Fence:       "python",
		Imports:     importTokens(ctx.ModulesUsed),
		Deps:        importTokens(ctx.Dependencies),
		ImportBlock: pyImportBlock,
		Incoming:    ctx.Incoming,
	}

	var body strings.Builder
	body.WriteString(ctx.Class.Cut)
	if ctx.Constructor != nil {
		body.WriteString("\n\n")
		body.WriteString(ctx.Constructor.Cut)
		u.Covers = append(u.Covers, string(ctx.Constructor.ID))
	}
	u.Body = body.String()

	u.Context = func(b *strings.Builder, st *renderstate.State) {
		st.MarkRendered(ctx.Class.Cut)
		if ctx.Constructor != nil {
			st.MarkRendered(ctx.Constructor.Cut)
		}
		g := st.Guard(b)
		for _, full := range []bool{true, false} {
			if !full {
				for _, base := range ctx.BaseClasses {
					if !st.Renderable(string(base.ID)) {
						continue
					}
					need := ""
					if base.NeedToImplement {
						need = fmt.Sprintf(" [NEED TO IMPLEMENT: %s]", strings.Join(base.NeedToImplementMethods, ", "))
					}
					fmt.Fprintf(b, "## %s (base class): %s%s\n", base.ID, desc(base.Description), need)
				}
			}
			for _, m := range ctx.Methods {
				if wantVis(m.Visibility, full) && st.Renderable(string(m.ID)) && g.More() {
					renderFunc(b, st, "## ", m)
				}
			}
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) && st.Renderable(string(cu.ID)) && g.More() {
					renderClassUsage(b, st, cu)
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

// DependencyUnit decomposes a Python dependency read: no source, just reverse usage.
func DependencyUnit(ctx *python.PythonDependencyContext) readunit.Unit {
	u := readunit.Unit{
		ID:    string(ctx.Dependency),
		Kind:  domain.ResourceDependency,
		Path:  string(ctx.Dependency),
		Fence: "python",
		Body:  fmt.Sprintf("# dependency %s", ctx.Dependency),
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

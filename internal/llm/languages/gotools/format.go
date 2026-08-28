package gotools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/renderstate"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
)

// Returns the input string or "no description" if empty
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Formats a GoFunctionContext into a human-readable string with code blocks, import statements, parent struct, function cut, and a hierarchical CONTEXT section listing interfaces, structs, called functions, and external variables.
func FormatGoFunctionContext(ctx *golang.GoFunctionContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```go\n")

	// Registered with the state, so the CONTEXT blocks below do not repeat what this
	// header already showed.
	writeImports(&b, st.NewImports(ctx.PackagesUsed), st.NewImports(ctx.Dependencies))

	if ctx.ParentStruct != nil {
		b.WriteString(ctx.ParentStruct.Cut)
		st.MarkRendered(ctx.ParentStruct.Cut)
		b.WriteString("\n\n")
	}

	b.WriteString(ctx.Function.Cut)
	b.WriteString("\n```\n\n")

	hasContext := len(ctx.InterfacesUsed) > 0 || len(ctx.StructsUsed) > 0 ||
		len(ctx.CalledFunctions) > 0 || len(ctx.ExtVarsUsed) > 0
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		// Bounded: CONTEXT had no cap of any kind, so a resource with a large neighbourhood
		// returned a subgraph dump rather than an answer.
		g := st.Guard(&b)
		for _, full := range []bool{true, false} {
			for _, iu := range ctx.InterfacesUsed {
				if wantVis(iu.Visibility, full) && g.More() {
					renderInterfaceUsage(&b, st, iu)
				}
			}
			for _, su := range ctx.StructsUsed {
				if wantVis(su.Visibility, full) && g.More() {
					renderStructUsage(&b, st, su)
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) && g.More() {
					renderFunc(&b, st, "## ", cf)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) && g.More() {
					renderExtVar(&b, st, ev)
				}
			}
		}
	}

	b.WriteString(st.Trailer())
	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

// Formats a GoStructContext into a human-readable string with a code block (imports, struct cut, constructor) and a CONTEXT section listing interfaces, methods, structs used, and external variables with their descriptions.
func FormatGoStructContext(ctx *golang.GoStructContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```go\n")

	// Registered with the state, so the CONTEXT blocks below do not repeat what this
	// header already showed.
	writeImports(&b, st.NewImports(ctx.PackagesUsed), st.NewImports(ctx.Dependencies))

	b.WriteString(ctx.Struct.Cut)
	b.WriteString("\n\n")

	if ctx.Constructor != nil {
		b.WriteString(ctx.Constructor.Cut)
		b.WriteString("\n")
	}

	b.WriteString("```\n\n")

	hasContext := len(ctx.Interfaces) > 0 || len(ctx.Methods) > 0 ||
		len(ctx.StructsUsed) > 0 || len(ctx.InterfacesUsed) > 0 || len(ctx.ExtVarsUsed) > 0
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		// Bounded: CONTEXT had no cap of any kind, so a resource with a large neighbourhood
		// returned a subgraph dump rather than an answer.
		g := st.Guard(&b)
		for _, full := range []bool{true, false} {
			if !full {
				for _, iface := range ctx.Interfaces {
					need := ""
					if iface.NeedToImplement {
						need = " [NEED TO IMPLEMENT]"
					}
					b.WriteString(fmt.Sprintf("## %s: %s%s\n", iface.ID, desc(iface.Description), need))
				}
			}
			for _, m := range ctx.Methods {
				if wantVis(m.Visibility, full) && g.More() {
					renderFunc(&b, st, "## ", m)
				}
			}
			for _, su := range ctx.StructsUsed {
				if wantVis(su.Visibility, full) && g.More() {
					renderStructUsage(&b, st, su)
				}
			}
			for _, iu := range ctx.InterfacesUsed {
				if wantVis(iu.Visibility, full) && g.More() {
					renderInterfaceUsage(&b, st, iu)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) && g.More() {
					renderExtVar(&b, st, ev)
				}
			}
		}
	}

	b.WriteString(st.Trailer())
	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

// Formats a GoInterfaceContext into a human-readable string with interface source and implementing structs/methods.
func FormatGoInterfaceContext(ctx *golang.GoInterfaceContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```go\n")
	writeImports(&b, ctx.PackagesUsed, ctx.Dependencies)
	b.WriteString(ctx.Interface.Cut)
	b.WriteString("\n```\n\n")

	if len(ctx.Implementations) > 0 {
		b.WriteString("# CONTEXT:\n")
		b.WriteString("## Implemented By\n")
		g := st.Guard(&b)
		for _, full := range []bool{true, false} {
			for _, impl := range ctx.Implementations {
				if wantVis(impl.Visibility, full) && g.More() {
					renderImpl(&b, st, "\t", impl)
				}
			}
		}
	}

	b.WriteString(st.Trailer())
	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

// Formats a GoNamedTypeContext into a human-readable string with named type source and resources that use it.
func FormatGoNamedTypeContext(ctx *golang.GoNamedTypeContext) string {
	var b strings.Builder

	b.WriteString("```go\n")
	writeImports(&b, ctx.PackagesUsed, ctx.Dependencies)
	b.WriteString(ctx.NamedType.Cut)
	b.WriteString("\n```\n\n")

	if len(ctx.UsedBy) == 0 {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	b.WriteString("## Used By\n")
	for _, usage := range ctx.UsedBy {
		b.WriteString(fmt.Sprintf("\t%s (%s): %s\n", usage.ID, resourceKindLabel(usage.Kind), desc(usage.Description)))
	}

	return b.String()
}

// Writes a formatted Go import block containing packages and dependencies to a strings.Builder.
func writeImports(b *strings.Builder, packages []golang.PackagePath, dependencies []golang.DependancyPath) {
	if len(packages) == 0 && len(dependencies) == 0 {
		return
	}
	b.WriteString("import (\n")
	for _, p := range packages {
		b.WriteString(fmt.Sprintf("\t%q\n", p))
	}
	for _, d := range dependencies {
		b.WriteString(fmt.Sprintf("\t%q\n", d))
	}
	b.WriteString(")\n\n")
}

// Formats a GoFileContext into a human-readable string with file source and a CONTEXT section listing functions, structs, interfaces, and imports.
func FormatGoFileContext(ctx *golang.GoFileContext) string {
	var b strings.Builder

	b.WriteString("```go\n")
	b.WriteString(ctx.File.Cut)
	if b.Len() > 0 && ctx.File.Cut != "" && ctx.File.Cut[len(ctx.File.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	hasContext := len(ctx.Functions) > 0 || len(ctx.Structs) > 0 ||
		len(ctx.Interfaces) > 0 || len(ctx.NamedTypes) > 0 || len(ctx.ExtVars) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## func %s: %s\n", fn.ID, desc(fn.Description)))
	}
	for _, s := range ctx.Structs {
		b.WriteString(fmt.Sprintf("## struct %s: %s\n", s.ID, desc(s.Description)))
		for _, m := range s.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, iface := range ctx.Interfaces {
		b.WriteString(fmt.Sprintf("## interface %s: %s\n", iface.ID, desc(iface.Description)))
	}
	for _, nt := range ctx.NamedTypes {
		b.WriteString(fmt.Sprintf("## type %s: %s\n", nt.ID, desc(nt.Description)))
	}
	for _, ev := range ctx.ExtVars {
		b.WriteString(fmt.Sprintf("## var %s: %s\n", ev.ID, desc(ev.Description)))
	}

	return b.String()
}

// Formats a GoPackageContext into a human-readable string with a CONTEXT section listing files, functions, structs, interfaces, and dependencies.
func FormatGoPackageContext(ctx *golang.GoPackageContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Package: %s\n\n", ctx.Package.Path))

	hasContext := len(ctx.Files) > 0 || len(ctx.Functions) > 0 || len(ctx.Structs) > 0 ||
		len(ctx.Interfaces) > 0 || len(ctx.NamedTypes) > 0 || len(ctx.ExtVars) > 0 ||
		len(ctx.Dependencies) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, f := range ctx.Files {
		b.WriteString(fmt.Sprintf("## file %s\n", f))
	}
	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## func %s: %s\n", fn.ID, desc(fn.Description)))
	}
	for _, s := range ctx.Structs {
		b.WriteString(fmt.Sprintf("## struct %s: %s\n", s.ID, desc(s.Description)))
		for _, m := range s.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, iface := range ctx.Interfaces {
		b.WriteString(fmt.Sprintf("## interface %s: %s\n", iface.ID, desc(iface.Description)))
	}
	for _, nt := range ctx.NamedTypes {
		b.WriteString(fmt.Sprintf("## type %s: %s\n", nt.ID, desc(nt.Description)))
	}
	for _, ev := range ctx.ExtVars {
		b.WriteString(fmt.Sprintf("## var %s: %s\n", ev.ID, desc(ev.Description)))
	}
	for _, d := range ctx.Dependencies {
		b.WriteString(fmt.Sprintf("## dep %q\n", d))
	}

	return b.String()
}

// Formats a GoDependencyContext into a human-readable string listing all resources that use the dependency.
func FormatGoDependencyContext(ctx *golang.GoDependencyContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Dependency: %s\n\n", ctx.Dependency))

	if len(ctx.UsedBy) == 0 {
		b.WriteString("No resources reference this dependency.\n")
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	b.WriteString("## Used By\n")
	for _, usage := range ctx.UsedBy {
		b.WriteString(fmt.Sprintf("\t%s (%s): %s\n", usage.ID, resourceKindLabel(usage.Kind), desc(usage.Description)))
	}

	return b.String()
}

// Maps a ResourceKind enum to its string label (function, method, type, interface, etc.).
func resourceKindLabel(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceFunction:
		return "function"
	case domain.ResourceMethod:
		return "method"
	case domain.ResourceStruct:
		return "struct"
	case domain.ResourceNamedType:
		return "named_type"
	case domain.ResourceInterface:
		return "interface"
	default:
		return string(kind)
	}
}

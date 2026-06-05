package gotools

import (
	"fmt"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
)

func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Formats a GoFunctionContext into a human-readable string with code blocks, import statements, parent struct, function cut, and a hierarchical CONTEXT section listing interfaces, structs, called functions, and external variables.
func FormatGoFunctionContext(ctx *golang.GoFunctionContext) string {
	var b strings.Builder

	b.WriteString("```go\n")

	if len(ctx.PackagesUsed) > 0 || len(ctx.Dependencies) > 0 {
		b.WriteString("import (\n")
		for _, p := range ctx.PackagesUsed {
			b.WriteString(fmt.Sprintf("\t%q\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("\t%q\n", d))
		}
		b.WriteString(")\n\n")
	}

	if ctx.ParentStruct != nil {
		b.WriteString(ctx.ParentStruct.Cut)
		b.WriteString("\n\n")
	}

	b.WriteString(ctx.Function.Cut)
	b.WriteString("\n```\n\n")

	hasContext := len(ctx.InterfacesUsed) > 0 || len(ctx.StructsUsed) > 0 ||
		len(ctx.CalledFunctions) > 0 || len(ctx.ExtVarsUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, iu := range ctx.InterfacesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", iu.Name, desc(iu.Description)))
		for _, impl := range iu.Implementations {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.Name, desc(impl.Description)))
			for _, m := range impl.Methods {
				b.WriteString(fmt.Sprintf("\t\t%s.%s: %s\n", impl.Name, m.Name, desc(m.Description)))
			}
		}
	}

	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.Name, desc(su.Description)))
		for _, m := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", su.Name, m.Name, desc(m.Description)))
		}
	}

	for _, cf := range ctx.CalledFunctions {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cf.Name, desc(cf.Description)))
	}

	for _, ev := range ctx.ExtVarsUsed {
		valStr := ""
		if ev.Value != "" {
			valStr = fmt.Sprintf(" = %s", ev.Value)
		}
		b.WriteString(fmt.Sprintf("## %s%s\n", ev.Name, valStr))
	}

	return b.String()
}

// Formats a GoStructContext into a human-readable string with a code block (imports, struct cut, constructor) and a CONTEXT section listing interfaces, methods, structs used, and external variables with their descriptions.
func FormatGoStructContext(ctx *golang.GoStructContext) string {
	var b strings.Builder

	b.WriteString("```go\n")

	if len(ctx.PackagesUsed) > 0 || len(ctx.Dependencies) > 0 {
		b.WriteString("import (\n")
		for _, p := range ctx.PackagesUsed {
			b.WriteString(fmt.Sprintf("\t%q\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("\t%q\n", d))
		}
		b.WriteString(")\n\n")
	}

	b.WriteString(ctx.Struct.Cut)
	b.WriteString("\n\n")

	if ctx.Constructor != nil {
		b.WriteString(ctx.Constructor.Cut)
		b.WriteString("\n")
	}

	b.WriteString("```\n\n")

	hasContext := len(ctx.Interfaces) > 0 || len(ctx.Methods) > 0 ||
		len(ctx.StructsUsed) > 0 || len(ctx.InterfacesUsed) > 0 || len(ctx.ExtVarsUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, iface := range ctx.Interfaces {
		need := ""
		if iface.NeedToImplement {
			need = " [NEED TO IMPLEMENT]"
		}
		b.WriteString(fmt.Sprintf("## %s: %s%s\n", iface.Name, desc(iface.Description), need))
	}

	for _, m := range ctx.Methods {
		b.WriteString(fmt.Sprintf("## %s: %s\n", m.Name, desc(m.Description)))
	}

	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.Name, desc(su.Description)))
		for _, m := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", su.Name, m.Name, desc(m.Description)))
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", iu.Name, desc(iu.Description)))
		for _, impl := range iu.Implementations {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.Name, desc(impl.Description)))
			for _, m := range impl.Methods {
				b.WriteString(fmt.Sprintf("\t\t%s.%s: %s\n", impl.Name, m.Name, desc(m.Description)))
			}
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		valStr := ""
		if ev.Value != "" {
			valStr = fmt.Sprintf(" = %s", ev.Value)
		}
		b.WriteString(fmt.Sprintf("## %s%s\n", ev.Name, valStr))
	}

	return b.String()
}

// Formats a GoInterfaceContext into a human-readable string with interface source and implementing structs/methods.
func FormatGoInterfaceContext(ctx *golang.GoInterfaceContext) string {
	var b strings.Builder

	b.WriteString("```go\n")
	writeImports(&b, ctx.PackagesUsed, ctx.Dependencies)
	b.WriteString(ctx.Interface.Cut)
	b.WriteString("\n```\n\n")

	if len(ctx.Implementations) == 0 {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	b.WriteString("## Implemented By\n")
	for _, impl := range ctx.Implementations {
		b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.Name, desc(impl.Description)))
		for _, m := range impl.Methods {
			b.WriteString(fmt.Sprintf("\t\t%s.%s: %s\n", impl.Name, m.Name, desc(m.Description)))
		}
	}

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

func resourceKindLabel(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceFunction:
		return "function"
	case domain.ResourceMethod:
		return "method"
	case domain.ResourceType:
		return "type"
	case domain.ResourceNamedType:
		return "named_type"
	case domain.ResourceInterface:
		return "interface"
	default:
		return string(kind)
	}
}

package tools

import (
	"fmt"
	"strings"

	"llm-topology/internal/topology/domain"
)

func formatFunctionContext(ctx *domain.FunctionContext) string {
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
		b.WriteString(fmt.Sprintf("## %s: %s\n", iu.Name, iu.Description))
		for _, impl := range iu.Implementations {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.Name, impl.Description))
			for _, m := range impl.Methods {
				b.WriteString(fmt.Sprintf("\t\t%s.%s: %s\n", impl.Name, m.Name, m.Description))
			}
		}
	}

	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.Name, su.Description))
		for _, m := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", su.Name, m.Name, m.Description))
		}
	}

	for _, cf := range ctx.CalledFunctions {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cf.Name, cf.Description))
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

func formatStructContext(ctx *domain.StructContext) string {
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
		b.WriteString(fmt.Sprintf("## %s: %s%s\n", iface.Name, iface.Description, need))
	}

	for _, m := range ctx.Methods {
		b.WriteString(fmt.Sprintf("## %s: %s\n", m.Name, m.Description))
	}

	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.Name, su.Description))
		for _, m := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", su.Name, m.Name, m.Description))
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", iu.Name, iu.Description))
		for _, impl := range iu.Implementations {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.Name, impl.Description))
			for _, m := range impl.Methods {
				b.WriteString(fmt.Sprintf("\t\t%s.%s: %s\n", impl.Name, m.Name, m.Description))
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

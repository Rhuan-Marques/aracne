package pythontools

import (
	"fmt"
	"strings"

	"llm-topology/internal/topology/python"
)

func FormatPythonFunctionContext(ctx *python.PythonFunctionContext) string {
	var b strings.Builder

	b.WriteString("```python\n")

	if len(ctx.ModulesUsed) > 0 || len(ctx.Dependencies) > 0 {
		for _, p := range ctx.ModulesUsed {
			b.WriteString(fmt.Sprintf("import %s  # internal\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("import %s  # external\n", d))
		}
		b.WriteString("\n")
	}

	if ctx.ParentClass != nil {
		b.WriteString(ctx.ParentClass.Cut)
		b.WriteString("\n\n")
	}

	b.WriteString(ctx.Function.Cut)
	b.WriteString("\n```\n\n")

	hasContext := len(ctx.ClassesUsed) > 0 ||
		len(ctx.CalledFunctions) > 0 || len(ctx.ExtVarsUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, cu := range ctx.ClassesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cu.Name, cu.Description))
		for _, m := range cu.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", cu.Name, m.Name, m.Description))
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

func FormatPythonClassContext(ctx *python.PythonClassContext) string {
	var b strings.Builder

	b.WriteString("```python\n")

	if len(ctx.ModulesUsed) > 0 || len(ctx.Dependencies) > 0 {
		for _, p := range ctx.ModulesUsed {
			b.WriteString(fmt.Sprintf("import %s  # internal\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("import %s  # external\n", d))
		}
		b.WriteString("\n")
	}

	b.WriteString(ctx.Class.Cut)
	b.WriteString("\n\n")

	if ctx.Constructor != nil {
		b.WriteString(ctx.Constructor.Cut)
		b.WriteString("\n")
	}

	b.WriteString("```\n\n")

	hasContext := len(ctx.BaseClasses) > 0 || len(ctx.Methods) > 0 ||
		len(ctx.ClassesUsed) > 0 || len(ctx.ExtVarsUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, base := range ctx.BaseClasses {
		need := ""
		if base.NeedToImplement {
			methods := strings.Join(base.NeedToImplementMethods, ", ")
			need = fmt.Sprintf(" [NEED TO IMPLEMENT: %s]", methods)
		}
		b.WriteString(fmt.Sprintf("## %s (base class): %s%s\n", base.Name, base.Description, need))
	}

	for _, m := range ctx.Methods {
		b.WriteString(fmt.Sprintf("## %s: %s\n", m.Name, m.Description))
	}

	for _, cu := range ctx.ClassesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cu.Name, cu.Description))
		for _, m := range cu.Methods {
			b.WriteString(fmt.Sprintf("\t%s.%s: %s\n", cu.Name, m.Name, m.Description))
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

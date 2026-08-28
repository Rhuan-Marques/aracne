package jstools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/renderstate"

	"aracne/internal/topology/javascript"
)

// Returns "no description" for empty strings, otherwise returns the input string unchanged.
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Formats a JavaScript module with its functions, classes, and external variables as a hierarchical context block for LLM consumption.
func FormatJavaScriptModuleContext(ctx *javascript.JavaScriptModuleContext) string {
	var b strings.Builder

	b.WriteString("```javascript\n")
	b.WriteString(ctx.Module.Cut)
	if ctx.Module.Cut != "" && ctx.Module.Cut[len(ctx.Module.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	hasContext := len(ctx.Functions) > 0 || len(ctx.Classes) > 0 || len(ctx.ExtVars) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## function %s: %s\n", fn.ID, desc(fn.Description)))
	}
	for _, c := range ctx.Classes {
		b.WriteString(fmt.Sprintf("## class %s: %s\n", c.ID, desc(c.Description)))
		for _, m := range c.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, ev := range ctx.ExtVars {
		b.WriteString(fmt.Sprintf("## var %s: %s\n", ev.ID, desc(ev.Description)))
	}

	return b.String()
}

// Formats a JavaScript dependency as markdown showing which resources use it.
func FormatJavaScriptDependencyContext(ctx *javascript.JavaScriptDependencyContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Dependency: %s\n\n", ctx.Dependency))

	if len(ctx.UsedBy) == 0 {
		b.WriteString("No resources reference this dependency.\n")
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	b.WriteString("## Used By\n")
	for _, usage := range ctx.UsedBy {
		b.WriteString(fmt.Sprintf("\t%s (%s): %s\n", usage.ID, string(usage.Kind), desc(usage.Description)))
	}

	return b.String()
}

// Formats a JavaScript function context as markdown with imports, function definition, parent class, and CONTEXT section listing called functions and dependencies.
func FormatJavaScriptFunctionContext(ctx *javascript.JavaScriptFunctionContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```javascript\n")

	if len(ctx.ModulesUsed) > 0 || len(ctx.Dependencies) > 0 {
		for _, p := range ctx.ModulesUsed {
			b.WriteString(fmt.Sprintf("// import from %s (internal)\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// import from %s (external)\n", d))
		}
		b.WriteString("\n")
	}

	if ctx.ParentClass != nil {
		b.WriteString(ctx.ParentClass.Cut)
		b.WriteString("\n\n")
	}

	b.WriteString(ctx.Function.Cut)
	b.WriteString("\n```\n\n")

	hasContext := len(ctx.ClassesUsed) > 0 || len(ctx.InterfacesUsed) > 0 ||
		len(ctx.NamedTypesUsed) > 0 || len(ctx.CalledFunctions) > 0 || len(ctx.ExtVarsUsed) > 0
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		for _, full := range []bool{true, false} {
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) {
					renderClassUsage(&b, st, cu)
				}
			}
			if !full {
				for _, iu := range ctx.InterfacesUsed {
					b.WriteString(fmt.Sprintf("## %s (interface): %s\n", iu.ID, desc(iu.Description)))
				}
				for _, nt := range ctx.NamedTypesUsed {
					b.WriteString(fmt.Sprintf("## %s (type): %s\n", nt.ID, desc(nt.Description)))
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) {
					renderFunc(&b, st, "## ", cf)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) {
					renderExtVar(&b, st, ev)
				}
			}
		}
	}

	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

// Formats a TypeScript interface with its base interfaces and implementations as a context block for LLM consumption.
func FormatJavaScriptInterfaceContext(ctx *javascript.JavaScriptInterfaceContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```typescript\n")
	b.WriteString(ctx.Interface.Cut)
	if ctx.Interface.Cut != "" && ctx.Interface.Cut[len(ctx.Interface.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	if len(ctx.BaseInterfaces) > 0 || len(ctx.Implementations) > 0 {
		b.WriteString("# CONTEXT:\n")
		for _, base := range ctx.BaseInterfaces {
			b.WriteString(fmt.Sprintf("## %s (extends): %s\n", base.ID, desc(base.Description)))
		}
		if len(ctx.Implementations) > 0 {
			b.WriteString("## Implemented By\n")
			for _, impl := range ctx.Implementations {
				b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.ID, desc(impl.Description)))
			}
		}
	}

	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

// Formats a TypeScript named type with its usage locations as a context block for LLM consumption.
func FormatJavaScriptNamedTypeContext(ctx *javascript.JavaScriptNamedTypeContext) string {
	var b strings.Builder

	b.WriteString("```typescript\n")
	b.WriteString(ctx.NamedType.Cut)
	if ctx.NamedType.Cut != "" && ctx.NamedType.Cut[len(ctx.NamedType.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	if len(ctx.UsedBy) == 0 {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	b.WriteString("## Used By\n")
	for _, u := range ctx.UsedBy {
		b.WriteString(fmt.Sprintf("\t%s (%s): %s\n", u.ID, string(u.Kind), desc(u.Description)))
	}

	return b.String()
}

// Formats a JavaScript class context as markdown with imports, class definition, and CONTEXT section listing base classes, methods, and dependencies.
func FormatJavaScriptClassContext(ctx *javascript.JavaScriptClassContext) string {
	var b strings.Builder
	st := renderstate.New()

	b.WriteString("```javascript\n")

	if len(ctx.ModulesUsed) > 0 || len(ctx.Dependencies) > 0 {
		for _, p := range ctx.ModulesUsed {
			b.WriteString(fmt.Sprintf("// import from %s (internal)\n", p))
		}
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// import from %s (external)\n", d))
		}
		b.WriteString("\n")
	}

	b.WriteString(ctx.Class.Cut)
	b.WriteString("\n")

	b.WriteString("```\n\n")

	hasContext := len(ctx.BaseClasses) > 0 || len(ctx.Methods) > 0 ||
		len(ctx.ClassesUsed) > 0 || len(ctx.ExtVarsUsed) > 0
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		for _, full := range []bool{true, false} {
			if !full {
				for _, base := range ctx.BaseClasses {
					b.WriteString(fmt.Sprintf("## %s (base class): %s\n", base.ID, desc(base.Description)))
				}
			}
			for _, m := range ctx.Methods {
				if wantVis(m.Visibility, full) {
					renderFunc(&b, st, "## ", m)
				}
			}
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) {
					renderClassUsage(&b, st, cu)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) {
					renderExtVar(&b, st, ev)
				}
			}
		}
	}

	writeUsedBy(&b, st, ctx.Incoming)

	return b.String()
}

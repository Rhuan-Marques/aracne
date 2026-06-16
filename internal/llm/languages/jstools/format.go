package jstools

import (
	"fmt"
	"strings"

	"aracne/internal/topology/javascript"
)

func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

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
	b.WriteString(fmt.Sprintf("## Package: %s\n", ctx.FromPackage))

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
	for _, imp := range ctx.Imports {
		b.WriteString(fmt.Sprintf("## import %q\n", imp))
	}

	return b.String()
}

func FormatJavaScriptPackageContext(ctx *javascript.JavaScriptPackageContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Package: %s\n\n", ctx.Package.Path))

	hasContext := len(ctx.Files) > 0 || len(ctx.Functions) > 0 || len(ctx.Classes) > 0 ||
		len(ctx.ExtVars) > 0 || len(ctx.Dependencies) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, f := range ctx.Files {
		b.WriteString(fmt.Sprintf("## module %s\n", f))
	}
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
	for _, d := range ctx.Dependencies {
		b.WriteString(fmt.Sprintf("## dep %q\n", d))
	}

	return b.String()
}

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

func FormatJavaScriptFunctionContext(ctx *javascript.JavaScriptFunctionContext) string {
	var b strings.Builder

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
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, cu := range ctx.ClassesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cu.ID, desc(cu.Description)))
		for _, m := range cu.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		b.WriteString(fmt.Sprintf("## %s (interface): %s\n", iu.ID, desc(iu.Description)))
	}

	for _, nt := range ctx.NamedTypesUsed {
		b.WriteString(fmt.Sprintf("## %s (type): %s\n", nt.ID, desc(nt.Description)))
	}

	for _, cf := range ctx.CalledFunctions {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cf.ID, desc(cf.Description)))
	}

	for _, ev := range ctx.ExtVarsUsed {
		valStr := ""
		if ev.Value != "" {
			valStr = fmt.Sprintf(" = %s", ev.Value)
		}
		b.WriteString(fmt.Sprintf("## %s%s\n", ev.ID, valStr))
	}

	return b.String()
}

func FormatJavaScriptInterfaceContext(ctx *javascript.JavaScriptInterfaceContext) string {
	var b strings.Builder

	b.WriteString("```typescript\n")
	b.WriteString(ctx.Interface.Cut)
	if ctx.Interface.Cut != "" && ctx.Interface.Cut[len(ctx.Interface.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	if len(ctx.BaseInterfaces) == 0 && len(ctx.Implementations) == 0 {
		return b.String()
	}

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

	return b.String()
}

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

func FormatJavaScriptClassContext(ctx *javascript.JavaScriptClassContext) string {
	var b strings.Builder

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
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, base := range ctx.BaseClasses {
		b.WriteString(fmt.Sprintf("## %s (base class): %s\n", base.ID, desc(base.Description)))
	}

	for _, m := range ctx.Methods {
		b.WriteString(fmt.Sprintf("## %s: %s\n", m.ID, desc(m.Description)))
	}

	for _, cu := range ctx.ClassesUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cu.ID, desc(cu.Description)))
		for _, m := range cu.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		valStr := ""
		if ev.Value != "" {
			valStr = fmt.Sprintf(" = %s", ev.Value)
		}
		b.WriteString(fmt.Sprintf("## %s%s\n", ev.ID, valStr))
	}

	return b.String()
}

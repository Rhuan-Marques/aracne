package pythontools

import (
	"fmt"
	"strings"

	"aracne/internal/topology/python"
)

func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

func FormatPythonModuleContext(ctx *python.PythonModuleContext) string {
	var b strings.Builder

	b.WriteString("```python\n")
	b.WriteString(ctx.Module.Cut)
	if b.Len() > 0 && ctx.Module.Cut != "" && ctx.Module.Cut[len(ctx.Module.Cut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	hasContext := len(ctx.Functions) > 0 || len(ctx.Classes) > 0 || len(ctx.ExtVars) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")

	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## def %s: %s\n", fn.ID, desc(fn.Description)))
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

func FormatPythonPackageContext(ctx *python.PythonPackageContext) string {
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
		b.WriteString(fmt.Sprintf("## def %s: %s\n", fn.ID, desc(fn.Description)))
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

func FormatPythonDependencyContext(ctx *python.PythonDependencyContext) string {
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
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		for _, full := range []bool{true, false} {
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) {
					renderClassUsage(&b, cu)
				}
			}
			for _, cf := range ctx.CalledFunctions {
				if wantVis(cf.Visibility, full) {
					renderFunc(&b, "## ", cf)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) {
					renderExtVar(&b, ev)
				}
			}
		}
	}

	writeUsedBy(&b, ctx.Incoming)

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
	if hasContext {
		b.WriteString("# CONTEXT:\n")
		for _, full := range []bool{true, false} {
			if !full {
				for _, base := range ctx.BaseClasses {
					need := ""
					if base.NeedToImplement {
						methods := strings.Join(base.NeedToImplementMethods, ", ")
						need = fmt.Sprintf(" [NEED TO IMPLEMENT: %s]", methods)
					}
					b.WriteString(fmt.Sprintf("## %s (base class): %s%s\n", base.ID, desc(base.Description), need))
				}
			}
			for _, m := range ctx.Methods {
				if wantVis(m.Visibility, full) {
					renderFunc(&b, "## ", m)
				}
			}
			for _, cu := range ctx.ClassesUsed {
				if wantVis(cu.Visibility, full) {
					renderClassUsage(&b, cu)
				}
			}
			for _, ev := range ctx.ExtVarsUsed {
				if wantVis(ev.Visibility, full) {
					renderExtVar(&b, ev)
				}
			}
		}
	}

	writeUsedBy(&b, ctx.Incoming)

	return b.String()
}

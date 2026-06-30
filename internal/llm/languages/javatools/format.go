package javatools

import (
	"fmt"
	"strings"

	"aracne/internal/topology/java"
)

// desc returns "no description" for empty strings, otherwise the input unchanged.
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// writeCut writes a code cut, ensuring it ends with a newline.
func writeCut(b *strings.Builder, cut string) {
	b.WriteString(cut)
	if cut != "" && cut[len(cut)-1] != '\n' {
		b.WriteString("\n")
	}
}

// joinTypes comma-joins the type text of a list of variable definitions, e.g.
// the parameter types of a method signature.
func joinTypes(vars []java.VariableDefinition) string {
	parts := make([]string, len(vars))
	for i, v := range vars {
		parts[i] = v.Typing
	}
	return strings.Join(parts, ", ")
}

// joinComponents comma-joins record components / fields as "Type name" pairs.
func joinComponents(vars []java.VariableDefinition) string {
	parts := make([]string, len(vars))
	for i, v := range vars {
		if v.Typing != "" {
			parts[i] = fmt.Sprintf("%s %s", v.Typing, v.Name)
		} else {
			parts[i] = v.Name
		}
	}
	return strings.Join(parts, ", ")
}

// FormatJavaFunctionContext formats a Java method/constructor context as markdown
// with a fenced java code block and a CONTEXT section listing the parent class,
// called methods, used classes/interfaces, and external dependencies.
func FormatJavaFunctionContext(ctx *java.JavaFunctionContext) string {
	var b strings.Builder

	b.WriteString("```java\n")
	if len(ctx.Dependencies) > 0 {
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// import %s (external dependency)\n", d))
		}
		b.WriteString("\n")
	}
	if ctx.ParentStruct != nil {
		b.WriteString(ctx.ParentStruct.Cut)
		b.WriteString("\n\n")
	}
	if ctx.Function != nil {
		writeCut(&b, ctx.Function.Cut)
	}
	b.WriteString("```\n\n")

	hasContext := len(ctx.StructsUsed) > 0 || len(ctx.InterfacesUsed) > 0 ||
		len(ctx.CalledFunctions) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.ID, desc(su.Description)))
		for _, m := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, iu := range ctx.InterfacesUsed {
		b.WriteString(fmt.Sprintf("## %s (interface): %s\n", iu.ID, desc(iu.Description)))
	}
	for _, cf := range ctx.CalledFunctions {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cf.ID, desc(cf.Description)))
	}

	return b.String()
}

// FormatJavaStructContext formats a Java class/enum/record context as markdown
// with a fenced java code block and a CONTEXT section listing enum variants,
// record components, the constructor, superclasses, implemented interfaces,
// methods, used classes, and external dependencies.
func FormatJavaStructContext(ctx *java.JavaStructContext) string {
	var b strings.Builder

	b.WriteString("```java\n")
	if len(ctx.Dependencies) > 0 {
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// import %s (external dependency)\n", d))
		}
		b.WriteString("\n")
	}
	if ctx.Struct != nil {
		writeCut(&b, ctx.Struct.Cut)
	}
	b.WriteString("```\n\n")

	hasContext := ctx.Constructor != nil || (ctx.IsEnum && len(ctx.Variants) > 0) ||
		(ctx.IsRecord && len(ctx.Components) > 0) || len(ctx.Methods) > 0 ||
		len(ctx.Implements) > 0 || len(ctx.Inherits) > 0 || len(ctx.StructsUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	if ctx.IsEnum && len(ctx.Variants) > 0 {
		b.WriteString(fmt.Sprintf("## variants: %s\n", strings.Join(ctx.Variants, ", ")))
	}
	if ctx.IsRecord && len(ctx.Components) > 0 {
		b.WriteString(fmt.Sprintf("## components: %s\n", joinComponents(ctx.Components)))
	}
	if ctx.Constructor != nil {
		b.WriteString(fmt.Sprintf("## %s (constructor): %s\n", ctx.Constructor.ID, desc(ctx.Constructor.Description)))
	}
	for _, s := range ctx.Inherits {
		b.WriteString(fmt.Sprintf("## %s (extends): %s\n", s.ID, desc(s.Description)))
	}
	for _, t := range ctx.Implements {
		b.WriteString(fmt.Sprintf("## %s (implements): %s\n", t.ID, desc(t.Description)))
	}
	for _, m := range ctx.Methods {
		b.WriteString(fmt.Sprintf("## %s: %s\n", m.ID, desc(m.Description)))
	}
	for _, su := range ctx.StructsUsed {
		b.WriteString(fmt.Sprintf("## %s: %s\n", su.ID, desc(su.Description)))
		for _, mm := range su.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", mm.ID, desc(mm.Description)))
		}
	}

	return b.String()
}

// FormatJavaInterfaceContext formats a Java interface/annotation context as
// markdown with a fenced java code block and a CONTEXT section listing the
// supertypes it extends, the classes/enums/records that implement it, and any
// default/static method summaries it declares.
func FormatJavaInterfaceContext(ctx *java.JavaInterfaceContext) string {
	var b strings.Builder

	b.WriteString("```java\n")
	if ctx.Interface != nil {
		writeCut(&b, ctx.Interface.Cut)
	}
	b.WriteString("```\n\n")

	var defaultStatic []java.FunctionDefinition
	if ctx.Interface != nil {
		for _, m := range ctx.Interface.Methods {
			if m.HasDefault || m.IsStatic {
				defaultStatic = append(defaultStatic, m)
			}
		}
	}

	hasContext := len(ctx.Supertypes) > 0 || len(ctx.ImplementedBy) > 0 ||
		len(defaultStatic) > 0 || ctx.IsAnnotation
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	if ctx.IsAnnotation {
		b.WriteString("## (annotation type)\n")
	}
	for _, st := range ctx.Supertypes {
		b.WriteString(fmt.Sprintf("## %s (supertype): %s\n", st.ID, desc(st.Description)))
	}
	for _, m := range defaultStatic {
		kind := "default"
		if m.IsStatic {
			kind = "static"
		}
		b.WriteString(fmt.Sprintf("## %s %s(%s)\n", kind, m.Name, joinTypes(m.Input)))
	}
	if len(ctx.ImplementedBy) > 0 {
		b.WriteString("## Implemented By\n")
		for _, impl := range ctx.ImplementedBy {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.ID, desc(impl.Description)))
		}
	}

	return b.String()
}

// FormatJavaModuleContext formats a Java module (file) context as markdown with a
// fenced java code block and a CONTEXT section listing the methods, classes, and
// interfaces it defines plus the modules and dependencies it imports.
func FormatJavaModuleContext(ctx *java.JavaModuleContext) string {
	var b strings.Builder

	b.WriteString("```java\n")
	if ctx.Module != nil {
		writeCut(&b, ctx.Module.Cut)
	}
	b.WriteString("```\n\n")

	hasContext := len(ctx.Functions) > 0 || len(ctx.Structs) > 0 ||
		len(ctx.Interfaces) > 0 || len(ctx.Imports) > 0 || len(ctx.Dependencies) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	if ctx.Package != "" {
		b.WriteString(fmt.Sprintf("## package %s\n", ctx.Package))
	}
	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## function %s: %s\n", fn.ID, desc(fn.Description)))
	}
	for _, s := range ctx.Structs {
		b.WriteString(fmt.Sprintf("## struct %s: %s\n", s.ID, desc(s.Description)))
		for _, m := range s.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, t := range ctx.Interfaces {
		b.WriteString(fmt.Sprintf("## interface %s: %s\n", t.ID, desc(t.Description)))
	}
	for _, imp := range ctx.Imports {
		b.WriteString(fmt.Sprintf("## imports %s\n", imp))
	}
	for _, d := range ctx.Dependencies {
		b.WriteString(fmt.Sprintf("## dependency %s\n", d))
	}

	return b.String()
}

// FormatJavaDependencyContext formats a Java external dependency as markdown
// showing which resources reference it.
func FormatJavaDependencyContext(ctx *java.JavaDependencyContext) string {
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

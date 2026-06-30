package rusttools

import (
	"fmt"
	"strings"

	"aracne/internal/topology/rust"
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

// FormatRustFunctionContext formats a Rust function/method context as markdown
// with a fenced rust code block and a CONTEXT section listing the parent struct,
// called functions, used structs/traits/named-types/variables, and external
// crate dependencies.
func FormatRustFunctionContext(ctx *rust.RustFunctionContext) string {
	var b strings.Builder

	b.WriteString("```rust\n")
	if len(ctx.Dependencies) > 0 {
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// use %s (external crate)\n", d))
		}
		b.WriteString("\n")
	}
	if ctx.ParentStruct != nil {
		b.WriteString(ctx.ParentStruct.Cut)
		b.WriteString("\n\n")
	}
	writeCut(&b, ctx.Function.Cut)
	b.WriteString("```\n\n")

	hasContext := len(ctx.StructsUsed) > 0 || len(ctx.TraitsUsed) > 0 ||
		len(ctx.NamedTypesUsed) > 0 || len(ctx.CalledFunctions) > 0 || len(ctx.VarsUsed) > 0
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
	for _, tu := range ctx.TraitsUsed {
		b.WriteString(fmt.Sprintf("## %s (trait): %s\n", tu.ID, desc(tu.Description)))
	}
	for _, nt := range ctx.NamedTypesUsed {
		b.WriteString(fmt.Sprintf("## %s (type): %s\n", nt.ID, desc(nt.Description)))
	}
	for _, cf := range ctx.CalledFunctions {
		b.WriteString(fmt.Sprintf("## %s: %s\n", cf.ID, desc(cf.Description)))
	}
	for _, v := range ctx.VarsUsed {
		valStr := ""
		if v.Value != "" {
			valStr = fmt.Sprintf(" = %s", v.Value)
		}
		b.WriteString(fmt.Sprintf("## %s%s: %s\n", v.ID, valStr, desc(v.Description)))
	}

	return b.String()
}

// FormatRustStructContext formats a Rust struct/enum/union context as markdown
// with a fenced rust code block and a CONTEXT section listing the constructor,
// enum variants, implemented traits, impl methods, used structs/named-types, and
// external crate dependencies.
func FormatRustStructContext(ctx *rust.RustStructContext) string {
	var b strings.Builder

	b.WriteString("```rust\n")
	if len(ctx.Dependencies) > 0 {
		for _, d := range ctx.Dependencies {
			b.WriteString(fmt.Sprintf("// use %s (external crate)\n", d))
		}
		b.WriteString("\n")
	}
	writeCut(&b, ctx.Struct.Cut)
	b.WriteString("```\n\n")

	hasContext := ctx.Constructor != nil || (ctx.IsEnum && len(ctx.Variants) > 0) ||
		len(ctx.Methods) > 0 || len(ctx.Implements) > 0 ||
		len(ctx.StructsUsed) > 0 || len(ctx.NamedTypesUsed) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	if ctx.IsEnum && len(ctx.Variants) > 0 {
		b.WriteString(fmt.Sprintf("## variants: %s\n", strings.Join(ctx.Variants, ", ")))
	}
	if ctx.Constructor != nil {
		b.WriteString(fmt.Sprintf("## %s (constructor): %s\n", ctx.Constructor.ID, desc(ctx.Constructor.Description)))
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
	for _, nt := range ctx.NamedTypesUsed {
		b.WriteString(fmt.Sprintf("## %s (type): %s\n", nt.ID, desc(nt.Description)))
	}

	return b.String()
}

// FormatRustInterfaceContext formats a Rust trait context as markdown with a
// fenced rust code block and a CONTEXT section listing the supertraits it
// inherits and the structs/enums that implement it.
func FormatRustInterfaceContext(ctx *rust.RustInterfaceContext) string {
	var b strings.Builder

	b.WriteString("```rust\n")
	writeCut(&b, ctx.Trait.Cut)
	b.WriteString("```\n\n")

	if len(ctx.Supertraits) == 0 && len(ctx.Implementors) == 0 {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	for _, st := range ctx.Supertraits {
		b.WriteString(fmt.Sprintf("## %s (supertrait): %s\n", st.ID, desc(st.Description)))
	}
	if len(ctx.Implementors) > 0 {
		b.WriteString("## Implemented By\n")
		for _, impl := range ctx.Implementors {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", impl.ID, desc(impl.Description)))
		}
	}

	return b.String()
}

// FormatRustNamedTypeContext formats a Rust type alias context as markdown with a
// fenced rust code block and a CONTEXT section listing the resources that use it.
func FormatRustNamedTypeContext(ctx *rust.RustNamedTypeContext) string {
	var b strings.Builder

	b.WriteString("```rust\n")
	writeCut(&b, ctx.NamedType.Cut)
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

// FormatRustModuleContext formats a Rust module (file) context as markdown with a
// fenced rust code block and a CONTEXT section listing the functions, structs,
// traits, named types, and variables it defines.
func FormatRustModuleContext(ctx *rust.RustModuleContext) string {
	var b strings.Builder

	b.WriteString("```rust\n")
	writeCut(&b, ctx.Module.Cut)
	b.WriteString("```\n\n")

	hasContext := len(ctx.Functions) > 0 || len(ctx.Structs) > 0 || len(ctx.Traits) > 0 ||
		len(ctx.NamedTypes) > 0 || len(ctx.Variables) > 0
	if !hasContext {
		return b.String()
	}

	b.WriteString("# CONTEXT:\n")
	for _, fn := range ctx.Functions {
		b.WriteString(fmt.Sprintf("## function %s: %s\n", fn.ID, desc(fn.Description)))
	}
	for _, s := range ctx.Structs {
		b.WriteString(fmt.Sprintf("## struct %s: %s\n", s.ID, desc(s.Description)))
		for _, m := range s.Methods {
			b.WriteString(fmt.Sprintf("\t%s: %s\n", m.ID, desc(m.Description)))
		}
	}
	for _, t := range ctx.Traits {
		b.WriteString(fmt.Sprintf("## trait %s: %s\n", t.ID, desc(t.Description)))
	}
	for _, nt := range ctx.NamedTypes {
		b.WriteString(fmt.Sprintf("## type %s: %s\n", nt.ID, desc(nt.Description)))
	}
	for _, v := range ctx.Variables {
		b.WriteString(fmt.Sprintf("## var %s: %s\n", v.ID, desc(v.Description)))
	}

	return b.String()
}

// FormatRustDependencyContext formats a Rust external crate dependency as
// markdown showing which resources use it.
func FormatRustDependencyContext(ctx *rust.RustDependencyContext) string {
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

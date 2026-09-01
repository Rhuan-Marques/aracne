package rusttools

import (
	"strings"

	"aracne/internal/llm/languages/readunit"

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

// Each Format* function renders ONE resource through the same batch renderer a multi-id
// `read` uses, so single and batched reads produce identical output. The body/context split
// lives in unit.go. Whole-file reads are language-neutral and no longer pass through here.

// Formats a Rust function or method read.
func FormatRustFunctionContext(ctx *rust.RustFunctionContext) string {
	return renderOne(FunctionUnit(ctx, nil))
}

// Formats a Rust struct/enum/union read.
func FormatRustStructContext(ctx *rust.RustStructContext) string {
	return renderOne(StructUnit(ctx))
}

// Formats a Rust trait read.
func FormatRustInterfaceContext(ctx *rust.RustInterfaceContext) string {
	return renderOne(InterfaceUnit(ctx))
}

// Formats a Rust type-alias read.
func FormatRustNamedTypeContext(ctx *rust.RustNamedTypeContext) string {
	return renderOne(NamedTypeUnit(ctx))
}

// Formats a Rust crate-dependency read.
func FormatRustDependencyContext(ctx *rust.RustDependencyContext) string {
	return renderOne(DependencyUnit(ctx))
}

// renderOne is the single-resource entry point into the batch renderer.
func renderOne(u readunit.Unit) string {
	return readunit.Render([]readunit.Unit{u}, readunit.Options{IncludeIncoming: true})
}

package jstools

import (
	"aracne/internal/llm/languages/readunit"

	"aracne/internal/topology/javascript"
)

// Returns "no description" for empty strings, otherwise returns the input string unchanged.
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Each Format* function renders ONE resource through the same batch renderer a multi-id
// `read` uses, so single and batched reads produce identical output. The body/context split
// lives in unit.go. Whole-file reads are language-neutral and no longer pass through here.

// Formats a JS/TS dependency read.
func FormatJavaScriptDependencyContext(ctx *javascript.JavaScriptDependencyContext) string {
	return renderOne(DependencyUnit(ctx))
}

// Formats a JS/TS function or method read.
func FormatJavaScriptFunctionContext(ctx *javascript.JavaScriptFunctionContext) string {
	return renderOne(FunctionUnit(ctx))
}

// Formats a TypeScript interface read.
func FormatJavaScriptInterfaceContext(ctx *javascript.JavaScriptInterfaceContext) string {
	return renderOne(InterfaceUnit(ctx))
}

// Formats a TypeScript named type read.
func FormatJavaScriptNamedTypeContext(ctx *javascript.JavaScriptNamedTypeContext) string {
	return renderOne(NamedTypeUnit(ctx))
}

// Formats a JS/TS class read.
func FormatJavaScriptClassContext(ctx *javascript.JavaScriptClassContext) string {
	return renderOne(ClassUnit(ctx))
}

// renderOne is the single-resource entry point into the batch renderer.
func renderOne(u readunit.Unit) string {
	return readunit.Render([]readunit.Unit{u}, readunit.Options{IncludeIncoming: true})
}

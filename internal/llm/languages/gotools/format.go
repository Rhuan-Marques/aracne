package gotools

import (
	"fmt"
	"strings"

	"aracne/internal/llm/languages/readunit"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
)

// Returns the input string or "no description" if empty
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Each Format* function below renders ONE resource through the same batch renderer that a
// multi-id `read` uses, so a single read and a batched one produce identical output. The
// per-resource body/context split lives in unit.go.

// Formats a Go function or method read.
func FormatGoFunctionContext(ctx *golang.GoFunctionContext) string {
	return renderOne(FunctionUnit(ctx))
}

// Formats a Go struct read.
func FormatGoStructContext(ctx *golang.GoStructContext) string {
	return renderOne(StructUnit(ctx))
}

// Formats a Go interface read.
func FormatGoInterfaceContext(ctx *golang.GoInterfaceContext) string {
	return renderOne(InterfaceUnit(ctx))
}

// Formats a Go named type read.
func FormatGoNamedTypeContext(ctx *golang.GoNamedTypeContext) string {
	return renderOne(NamedTypeUnit(ctx))
}

// Formats a Go package read.
func FormatGoPackageContext(ctx *golang.GoPackageContext) string {
	return renderOne(PackageUnit(ctx))
}

// Formats a Go dependency read.
func FormatGoDependencyContext(ctx *golang.GoDependencyContext) string {
	return renderOne(DependencyUnit(ctx))
}

// renderOne is the single-resource entry point into the batch renderer.
func renderOne(u readunit.Unit) string {
	return readunit.Render([]readunit.Unit{u}, readunit.Options{IncludeIncoming: true})
}

// Writes a formatted Go import block containing packages and dependencies to a strings.Builder.
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

// Maps a ResourceKind enum to its string label (function, method, type, interface, etc.).
func resourceKindLabel(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceFunction:
		return "function"
	case domain.ResourceMethod:
		return "method"
	case domain.ResourceStruct:
		return "struct"
	case domain.ResourceNamedType:
		return "named_type"
	case domain.ResourceInterface:
		return "interface"
	default:
		return string(kind)
	}
}

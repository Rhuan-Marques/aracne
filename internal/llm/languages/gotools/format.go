package gotools

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
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

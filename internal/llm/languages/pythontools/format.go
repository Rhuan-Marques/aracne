package pythontools

import (
	"aracne/internal/llm/languages/readunit"

	"aracne/internal/topology/python"
)

// Returns a description string or "no description" if empty.
func desc(s string) string {
	if s == "" {
		return "no description"
	}
	return s
}

// Each Format* function renders ONE resource through the same batch renderer a multi-id
// `read` uses, so single and batched reads produce identical output. The body/context split
// lives in unit.go. Whole-file reads are language-neutral and no longer pass through here.

// Formats a Python dependency read.
func FormatPythonDependencyContext(ctx *python.PythonDependencyContext) string {
	return renderOne(DependencyUnit(ctx))
}

// Formats a Python function or method read.
func FormatPythonFunctionContext(ctx *python.PythonFunctionContext) string {
	return renderOne(FunctionUnit(ctx, nil))
}

// Formats a Python class read.
func FormatPythonClassContext(ctx *python.PythonClassContext) string {
	return renderOne(ClassUnit(ctx))
}

// renderOne is the single-resource entry point into the batch renderer.
func renderOne(u readunit.Unit) string {
	return readunit.Render([]readunit.Unit{u}, readunit.Options{IncludeIncoming: true})
}

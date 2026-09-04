package jstools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
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

// renderOne is the single-resource entry point into the batch renderer.
func renderOne(u readunit.Unit) string {
	return readunit.Render([]readunit.Unit{u}, readunit.Options{IncludeIncoming: true})
}

package pythontools

import (
	"strings"
)

// desc renders a stored description as one line, or "no description" when it is empty.
func desc(s string) string {
	// Collapsed to one line, because it is printed inside a "## id: description" line: a stored
	// newline -- from a row written before the write path collapsed them -- would end that line
	// and let the rest of the description pose as CONTEXT structure of its own.
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "no description"
	}
	return s
}

// Each Format* function renders ONE resource through the same batch renderer a multi-id
// `read` uses, so single and batched reads produce identical output. The body/context split
// lives in unit.go. Whole-file reads are language-neutral and no longer pass through here.

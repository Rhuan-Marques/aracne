package prompts

import (
	"strings"

	"aracne/internal/toolspec"
)

// WithToolsListing inserts a "## Tools" section — built from the tool catalog
// (toolspec) for the given tool names — into an agent prompt body, right after
// the opening paragraph. This keeps the per-tool descriptions in one place and
// makes the listing reflect the agent's actually-configured tools.
//
// toolNames are catalog names; nativeReadAvailable decides which runtime name the read
// tool is listed under (see toolspec.ResolveToolNames).
func WithToolsListing(prompt string, toolNames []string, nativeReadAvailable bool) string {
	section := toolspec.ToolsSection(toolNames, nativeReadAvailable)
	if section == "" {
		return prompt
	}
	trimmed := strings.TrimRight(prompt, "\n")
	if idx := strings.Index(trimmed, "\n\n"); idx >= 0 {
		return trimmed[:idx+2] + section + "\n" + trimmed[idx+2:] + "\n"
	}
	return section + "\n\n" + trimmed + "\n"
}

package agent

import "llm-topology/internal/llm/languages/gotools"

// Builds the system prompt string for the AI agent based on the target programming language. Takes a language string parameter. Dispatches to the language-specific prompt builder (currently always Go). Returns the assembled system prompt text.
func BuildPrompt(language string) string {
	switch language {
	case "go":
		return gotools.BuildGoSystemPrompt()
	default:
		return gotools.BuildGoSystemPrompt()
	}
}

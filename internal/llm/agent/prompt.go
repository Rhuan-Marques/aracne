package agent

import "llm-topology/internal/llm/languages/gotools"

func BuildPrompt(language string) string {
	switch language {
	case "go":
		return gotools.GoSystemPrompt
	default:
		return gotools.GoSystemPrompt
	}
}

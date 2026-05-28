package agent

import (
	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
)

func BuildPrompt(language string) string {
	switch language {
	case "go":
		return gotools.BuildGoSystemPrompt()
	case "python":
		return pythontools.BuildPythonSystemPrompt()
	default:
		return gotools.BuildGoSystemPrompt()
	}
}

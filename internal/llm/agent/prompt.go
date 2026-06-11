package agent

import (
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/pythontools"
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

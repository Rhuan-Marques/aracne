package agent

import (
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
)

// Constructs language-specific system prompts for Go, Python, JavaScript, TypeScript, or multi-language.
func BuildPrompt(language string) string {
	switch language {
	case "go":
		return gotools.BuildGoSystemPrompt()
	case "python":
		return pythontools.BuildPythonSystemPrompt()
	case "javascript":
		return jstools.BuildJavaScriptSystemPrompt()
	case "typescript":
		return jstools.BuildTypeScriptSystemPrompt()
	case "multi":
		return gotools.BuildGoSystemPrompt() + "\n\n" + pythontools.BuildPythonSystemPrompt() + "\n\n" + jstools.BuildJavaScriptSystemPrompt() + "\n\n" + jstools.BuildTypeScriptSystemPrompt()
	default:
		return gotools.BuildGoSystemPrompt()
	}
}

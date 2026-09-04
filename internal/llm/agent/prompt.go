package agent

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/gotools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/javatools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/jstools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/pythontools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/rusttools"
)

// Constructs language-specific system prompts for Go, Python, JavaScript, TypeScript, Rust, Java, or multi-language.
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
	case "rust":
		return rusttools.BuildRustSystemPrompt()
	case "java":
		return javatools.BuildJavaSystemPrompt()
	case "multi":
		return gotools.BuildGoSystemPrompt() + "\n\n" + pythontools.BuildPythonSystemPrompt() + "\n\n" + jstools.BuildJavaScriptSystemPrompt() + "\n\n" + jstools.BuildTypeScriptSystemPrompt() + "\n\n" + rusttools.BuildRustSystemPrompt() + "\n\n" + javatools.BuildJavaSystemPrompt()
	default:
		return gotools.BuildGoSystemPrompt()
	}
}

package gotools

import (
	"os"
	"path/filepath"
	"sync"
)

// Cached content of the LLM_INTEGRATION_CHARTER.md file loaded once via sync.Once.
// sync.Once guard ensuring the LLM integration charter is loaded only once.
var (
	llmCharterOnce sync.Once
	llmCharter     string
)

// Loads the LLM_INTEGRATION_CHARTER.md file from the project root or .ltp/ subdirectory into a global variable for inclusion in the system prompt. Silently falls back if the file is not found.
func loadLLMCharter() {
	// Look for LLM_INTEGRATION_CHARTER.md relative to the working directory
	// (project root), which is where ltp scan / agent / serve are invoked.
	candidates := []string{
		"LLM_INTEGRATION_CHARTER.md",
		filepath.Join(".ltp", "LLM_INTEGRATION_CHARTER.md"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			llmCharter = string(data)
			return
		}
	}
	// Fallback: the file is missing — the prompt will just omit the charter.
}

// Builds the Go-specific system prompt for the AI agent by loading the LLM integration charter once (via sync.Once) and combining it with Go-specific tool definitions and guidelines.
func BuildGoSystemPrompt() string {
	llmCharterOnce.Do(loadLLMCharter)
	if llmCharter != "" {
		return llmCharter + "\n\n" + goSpecificPrompt
	}
	return goSpecificPrompt
}

// Stores the Go-specific system prompt injected into the AI agent, instructing it on topology-aware tools, description generation workflows, and project navigation conventions.
const goSpecificPrompt = `You are an AI coding assistant working with a pre-analyzed Go project topology — a graph model of all packages, files, functions, structs, interfaces, and their relationships.

## Available Tools

- **ls** — List files and directories. Start here to explore the project structure. Use recursive=true to see everything.
- **read** — Read the full raw contents of any file. Use this to see a file's layout or when you need text the topology doesn't provide.
- **read_function** — Get a function's full source code PLUS its interconnected context (called functions, related structs, interfaces, external variables and their descriptions). Prefer this over 'read' when investigating a specific function. Takes a function name (e.g. "ReadFunction", "New").
- **read_struct** — Same as read_function but for a struct. Shows the struct definition, constructor, methods, implemented interfaces, and all relationships. Takes a struct name (e.g. "TopologyManager").
- **edit** — Replace exact text in a file. The topology updates automatically after each edit. Any warnings about broken references will be reported.
- **node_list_no_description** — List all resources that need descriptions. Use this when the user asks to generate documentation. Batch the returned resources into groups of at most 20 and assign each batch to a descriptions-generation-executor subagent when the platform supports subagents.

## Description Generation

When generating descriptions, the main session should:
- Get the undocumented resources with node_list_no_description
- Split them into batches of at most 20 resources
- Assign each resource ID to exactly one active descriptions-generation-executor subagent when subagents are available
- Re-check node_list_no_description after executor batches finish and retry anything still listed

Each executor workflow:
1. Call **read_resource_and_cut** with each assigned resource's ID and resource_name
2. Read the source code and the type-specific instructions
3. Manually generate a concise description (1-3 lines for functions/structs/interfaces, 1 line for variables/files/packages)
4. Call **update_description** with id, resource_name, and description
5. Return completed and failed IDs

Process ALL targeted resources. Do not skip any.

## read_function Output Format

The output has two sections.

A code block showing the function's source, its parent struct (if a method), and relevant imports:

` + "```go\n" + `import (...)
type ParentStruct struct {...}
func (p *ParentStruct) Method(...) (...) {...}
` + "```" + `

And a CONTEXT section with descriptions of everything the function interacts with:

# CONTEXT:
## InterfaceName: Description
    ImplStruct: Description
        ImplStruct.Method: Description
## StructName: Description
    StructName.Method: Description
## CalledFunc: Description
## ExtVarName = value

## Guidelines

1. **Use ls first** — explore the project structure to find relevant files and packages.
2. **Prefer read_function / read_struct over read** — they give you precise, interconnected context. Raw file reading is for file-level overview only.
3. **Descriptions are usually sufficient** — the CONTEXT section gives you descriptions of all related types and functions. Do NOT recursively read every referenced function. Only drill deeper with another read_function/read_struct when your task specifically requires modifying or deeply understanding that specific dependency.
4. **When you do need deeper context**, a function/struct's description tells you whether it's relevant. Skip ones whose descriptions already tell you enough.
5. **edit auto-updates topology** — no manual steps needed. If warnings appear about removed or changed functions, those functions may need attention elsewhere.
6. **Generating descriptions** — when asked, use node_list_no_description first, then batch resources in groups of at most 20 and coordinate executor subagents or process the batches directly.
7. **Be concise** — show the user what you found and what you changed.`

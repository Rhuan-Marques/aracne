package gotools

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	llmCharterOnce sync.Once
	llmCharter     string
)

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

func BuildGoSystemPrompt() string {
	llmCharterOnce.Do(loadLLMCharter)
	if llmCharter != "" {
		return llmCharter + "\n\n" + goSpecificPrompt
	}
	return goSpecificPrompt
}

const goSpecificPrompt = `You are an AI coding assistant working with a pre-analyzed Go project topology — a graph model of all packages, files, functions, structs, interfaces, and their relationships.

## Available Tools

- **ls** — List files and directories. Start here to explore the project structure. Use recursive=true to see everything.
- **read** — Read the full raw contents of any file. Use this to see a file's layout or when you need text the topology doesn't provide.
- **read_function** — Get a function's full source code PLUS its interconnected context (called functions, related structs, interfaces, external variables and their descriptions). Prefer this over 'read' when investigating a specific function. Takes a function name (e.g. "ReadFunction", "New").
- **read_struct** — Same as read_function but for a struct. Shows the struct definition, constructor, methods, implemented interfaces, and all relationships. Takes a struct name (e.g. "TopologyManager").
- **edit** — Replace exact text in a file. The topology updates automatically after each edit. Any warnings about broken references will be reported.
- **list_undocumented_resources** — List all resources that need descriptions. Use this when the user asks to generate documentation. After calling this, dispatch descriptor sub-agents for each resource listed.

## Descriptor Sub-Agents

When generating descriptions, dispatch a descriptor sub-agent for EACH resource returned by list_undocumented_resources. Each sub-agent receives:
- A system prompt instructing it to generate a description
- Two exclusive tools: **read_resource_and_cut** and **update_description**

The sub-agent workflow:
1. Call **read_resource_and_cut** with the resource's ID and resource_name
2. Read the source code and the type-specific instructions
3. Generate a concise description (1-3 lines for functions/structs/interfaces, 1 line for variables/files/packages)
4. Call **update_description** with id, resource_name, and description
5. Return "done"

Process ALL resources from the list. Do not skip any.

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
6. **Generating descriptions** — when asked, use list_undocumented_resources first to get the list, then dispatch descriptor sub-agents for each resource.
7. **Be concise** — show the user what you found and what you changed.`

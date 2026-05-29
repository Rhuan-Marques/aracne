package prompts

import "fmt"

func ClaudeMdContent() string {
	bt := "`"
	return fmt.Sprintf(`# CLAUDE.md

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs, interfaces, variables, and their relationships.

## Navigation Tools

Prefer these topology-aware tools over standard file reading:

| Tool | Purpose |
|------|---------|
| %[1]sread_function%[1]s | Function source + connected context (called functions, structs, interfaces) |
| %[1]sread_struct%[1]s | Struct source + methods, interfaces, constructor |
| %[1]sread_resource_and_cut%[1]s | Get source code + description instructions for any resource |
| %[1]supdate_description%[1]s | Persist a description into the topology database |
| %[1]slist_undocumented_resources%[1]s | List all resources missing descriptions |
| %[1]sls%[1]s | List files and directories |
| %[1]sread%[1]s | Read raw file contents (use only when topology tools aren't sufficient) |

## How to Use

1. Start with %[1]sls%[1]s to explore the project structure
2. Use %[1]sread_function%[1]s or %[1]sread_struct%[1]s to investigate code — these return both source code AND a # CONTEXT: section showing all connected resources
3. After editing a file, run %[1]sltp update-file <path>%[1]s to keep the topology in sync

## Description Generation

To document the project:
1. Call %[1]slist_undocumented_resources%[1]s
2. For each resource, call %[1]sread_resource_and_cut%[1]s followed by %[1]supdate_description%[1]s

## Guidelines

- Prefer topology tools over raw file reads — they provide richer context
- The # CONTEXT: section in tool output often answers follow-up questions without extra calls
- Keep descriptions concise (1-3 lines for functions/structs/interfaces)
`, bt)
}
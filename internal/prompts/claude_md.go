package prompts

import (
	"fmt"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func ClaudeMdContent() string {
	return ClaudeMdContentForModes(helper.DefaultToolModes(), nil)
}

func AgentsMdContent() string {
	return AgentsMdContentForModes(helper.DefaultToolModes(), nil)
}

func ClaudeMdContentForModes(modes helper.ToolModes, readSplit map[domain.ResourceKind]bool) string {
	return agentInstructionsContent(modes, "mcp__aracne__", readSplit)
}

func AgentsMdContentForModes(modes helper.ToolModes, readSplit map[domain.ResourceKind]bool) string {
	return agentInstructionsContent(modes, "aracne_", readSplit)
}

func bt(s string) string {
	return "`" + s + "`"
}

func agentInstructionsContent(modes helper.ToolModes, mcpToolPrefix string, readSplit map[domain.ResourceKind]bool) string {
	var b strings.Builder

	b.WriteString(introductionSection())
	b.WriteString(navigationModelSection(modes))
	b.WriteString(lookupToolsSection(modes, mcpToolPrefix, readSplit))
	b.WriteString(grepSection(modes, mcpToolPrefix))
	b.WriteString(resourceContextSection(modes))
	b.WriteString(editWriteSection(modes, mcpToolPrefix))
	b.WriteString(otherSection(modes, mcpToolPrefix))
	b.WriteString(howToNavigateSection())
	b.WriteString(behavioralRulesSection())
	b.WriteString(endingSection())

	return b.String()
}

func introductionSection() string {
	return `# Aracne Project Integration

This project uses **aracne** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

`
}

func navigationModelSection(modes helper.ToolModes) string {
	var step2 string
	if modes.Other == helper.OtherModeMCP {
		step2 = "Use lookup MCP tools"
	} else {
		step2 = "Use `arac read` commands in bash"
	}

	b := &strings.Builder{}
	fmt.Fprintf(b, "## Navigation Model\n\n")
	fmt.Fprintf(b, "The topology is a directed graph can enhance your information about the repository you're using if you use it correctly.\n\n")
	fmt.Fprintf(b, "**Navigation Flow:**\n")
	fmt.Fprintf(b, "1. Use `ls` to understand the project file layout\n")
	fmt.Fprintf(b, "2. Use %s to get a resource's full context with interconnected relationships\n", step2)

	if modes.Read == helper.ReadModeNative {
		fmt.Fprintf(b, "3. Use your `read` tool to get files when %s is not relevant\n\n", step2)
	} else {
		var note string
		if modes.Other == helper.OtherModeMCP {
			note = "use MCP lookups"
		} else {
			note = "use `arac read` commands"
		}
		fmt.Fprintf(b, "\n**Note: Never try to use `read` native tool, %s instead**\n\n", note)
	}

	return b.String()
}

func lookupToolsSection(modes helper.ToolModes, mcpToolPrefix string, readSplit map[domain.ResourceKind]bool) string {
	useSplit := len(readSplit) > 0

	b := &strings.Builder{}

	if modes.Other == helper.OtherModeMCP {
		if useSplit {
			b.WriteString("## MCP Lookup tools:\n")
			for kind := range readSplit {
				switch kind {
				case domain.ResourceFunction, domain.ResourceMethod:
					fmt.Fprintf(b, "- %s: Reads the function and context for resources it uses, receives a function ID.\n", bt(mcpToolPrefix+"read_function"))
				case domain.ResourceType:
					fmt.Fprintf(b, "- %s: Reads the struct and context for resources it uses, receives a struct ID.\n", bt(mcpToolPrefix+"read_struct"))
				case domain.ResourceInterface:
					fmt.Fprintf(b, "- %s: Reads the interface and context for which resources it is implemented by, receives an interface ID.\n", bt(mcpToolPrefix+"read_interface"))
				case domain.ResourceNamedType:
					fmt.Fprintf(b, "- %s: Reads the named type and context for which resources it is used by, receives a named type ID.\n", bt(mcpToolPrefix+"read_named_type"))
				case domain.ResourceFile:
					fmt.Fprintf(b, "- %s: Reads the content of a file, receives the file path.\n", bt(mcpToolPrefix+"read_file"))
				case domain.ResourcePackage:
					fmt.Fprintf(b, "- %s: Reads the package and context for which resources it is used by, receives a package ID.\n", bt(mcpToolPrefix+"read_package"))
				case domain.ResourceDependency:
					fmt.Fprintf(b, "- %s: Reads the dependency and context for which resources it is used by, receives a dependency ID.\n", bt(mcpToolPrefix+"read_dependency"))
				}
			}
		} else {
			b.WriteString("## MCP Lookup tools:\n")
			fmt.Fprintf(b, "- %s: This command will give you the code and full context for any resource you want. These include: Files, Functions, Structs, etc. The tool receives a Resource ID, which can be the file's path or the ID of any resource.\n", bt(mcpToolPrefix+"read"))
		}
		b.WriteString("\n")
	} else if modes.Other == helper.OtherModeTerminal {
		if useSplit {
			b.WriteString("## arac read Terminal command:\n")
			b.WriteString("To navigate, you should always use your terminal tool to use `arac read {resource ID}` commands. The available resources are the following:\n")
			for kind := range readSplit {
				switch kind {
				case domain.ResourceFunction, domain.ResourceMethod:
					fmt.Fprintf(b, "- %s: Reads the function and context for resources it uses\n", bt("arac read {function_id}"))
				case domain.ResourceType:
					fmt.Fprintf(b, "- %s: Reads the struct and context for resources it uses\n", bt("arac read {struct_id}"))
				case domain.ResourceInterface:
					fmt.Fprintf(b, "- %s: Reads the interface and context for which resources it is implemented by\n", bt("arac read {interface_id}"))
				case domain.ResourceNamedType:
					fmt.Fprintf(b, "- %s: Reads the named type and context for which resources it is used by\n", bt("arac read {named_type_id}"))
				case domain.ResourceFile:
					fmt.Fprintf(b, "- %s: Reads the content of a file\n", bt("arac read {file_path}"))
				case domain.ResourcePackage:
					fmt.Fprintf(b, "- %s: Reads the package and context for which resources it is used by\n", bt("arac read {package_id}"))
				case domain.ResourceDependency:
					fmt.Fprintf(b, "- %s: Reads the dependency and context for which resources it is used by\n", bt("arac read {dependency_id}"))
				}
			}
		} else {
			b.WriteString("## arac read Terminal command:\n")
			fmt.Fprintf(b, "- %s: This command will give you the code and full context for any resource you want. These include: Files, Functions, Structs, etc. The resource_id can be a file's path or the ID of any other resource.\n", bt("arac read {resource_id}"))
		}
		b.WriteString("\n")
	}

	b.WriteString("Note: Do *not* use \"cat\", \"Get-Content\" or any other OS command to read files")

	return b.String()
}

func grepSection(modes helper.ToolModes, mcpToolPrefix string) string {
	switch modes.Grep {
	case helper.GrepModeNative:
		return "## Grep/Search\n\nUse your native `grep`/`Grep` search tool for content search. When you need topology metadata in results, use `arac grep <pattern> [path]`; it returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.\n\n"
	case helper.GrepModeMCP:
		return fmt.Sprintf("## Grep/Search\n\nUse the MCP tool %s for content search. It returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.\n\nDo *not* use your native `grep` tool.\nDo not use `grep`, `Select-String` or `rg` in the terminal", bt(mcpToolPrefix+"grep"))
	case helper.GrepModeTerminal:
		return "## Grep/Search\n\nUse `arac grep <pattern> [path]` for content search. It returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.\n\nDo *not* use your native `grep` tool.\nDo not use `grep`, `Select-String` or `rg` in the terminal"
	default:
		return ""
	}
}

func resourceContextSection(modes helper.ToolModes) string {
	var toolRef string
	if modes.Other == helper.OtherModeMCP {
		toolRef = "MCP Lookup Tool"
	} else {
		toolRef = "`arac read` command"
	}

	b := &strings.Builder{}
	fmt.Fprintf(b, "## Resource Context\n\n")
	fmt.Fprintf(b, "When you call a %s, the output has two sections:\n\n", toolRef)
	fmt.Fprintf(b, "**Code Block:** The resource's full source code, plus relevant imports and enclosing type (for methods).\n\n")
	fmt.Fprintf(b, "**%s Section:** A structured hierarchical listing of everything the resource touches:\n\n", bt("# CONTEXT:"))
	fmt.Fprintf(b, "```\n")
	fmt.Fprintf(b, "# CONTEXT:\n")
	fmt.Fprintf(b, "## InterfaceName: Description\n")
	fmt.Fprintf(b, "    ImplStruct: Description\n")
	fmt.Fprintf(b, "        ImplStruct.Method: Description\n")
	fmt.Fprintf(b, "## OtherStruct: Description\n")
	fmt.Fprintf(b, "    OtherStruct.Method: Description\n")
	fmt.Fprintf(b, "## CalledFunction: Description\n")
	fmt.Fprintf(b, "## ExtVarName = value\n")
	fmt.Fprintf(b, "```\n\n")
	fmt.Fprintf(b, "Use the CONTEXT section to understand relationships **without making additional tool calls**.\n\n")
	return b.String()
}

func editWriteSection(modes helper.ToolModes, mcpToolPrefix string) string {
	switch modes.Edit {
	case helper.EditModeNative:
		return "## Edit and Write:\n\nYou can edit files using your native `edit` tool.\nYou can write files using your native `write` tool.\nAfter editing or writing, the context for the topology will be automatically updated to reflect your actions.\n\n"
	case helper.EditModeMCP:
		return fmt.Sprintf("## Edit and Write:\n\nYou can edit files using the MCP tool %s.\nYou can write files using the MCP tool %s.\nAfter editing or writing, the context for the topology will be automatically updated to reflect your actions.\n\n**Note: NEVER try to edit or write using your native tools**\n\n", bt(mcpToolPrefix+"edit"), bt(mcpToolPrefix+"write"))
	case helper.EditModeTerminal:
		return "## Edit:\n\nYou can edit files using `arac edit {file_path} {old_string} {new_string}` in your terminal. The old string should only have one match in the file, make the string longer if there's any conflict.\nYou can write files using `arac write {file_path} {content}` in your terminal.\nAfter editing or writing, the context for the topology will be automatically updated to reflect your actions.\n\n**Note: NEVER try to edit or write using your native tools**\n\n"
	}
	return ""
}

func otherSection(modes helper.ToolModes, mcpToolPrefix string) string {
	b := &strings.Builder{}

	if modes.Other == helper.OtherModeMCP {
		b.WriteString("## Other:\n\n")
		fmt.Fprintf(b, "- If you find a bug that is not relevant to your task, *do not fix it*. Instead, report it using %s\n", bt(mcpToolPrefix+"bug_report"))
		fmt.Fprintf(b, "- If you want to check for any topology warnings, you can do it using %s\n\n", bt(mcpToolPrefix+"warnings_list"))
	} else if modes.Other == helper.OtherModeTerminal {
		b.WriteString("## Other:\n\n")
		fmt.Fprintf(b, "- If you find a bug that is not relevant to your task, *do not fix it*. Instead, report it using %s\n", bt("arac bug report --resource <id> --description <text>"))
		fmt.Fprintf(b, "- If you want to check for any topology warnings, you can do it using %s\n\n", bt("arac warnings list"))
	}

	return b.String()
}

func howToNavigateSection() string {
	return `## How to Navigate:

### 1: Explore Topology, NOT Files
Use the topology manager to your advantage, only read entire files when:
    - They are NOT supported language files (.go and .py)
    - Your tasks requires you to know all the information from the entire file
    - You don't know the other resources IDs yet

### 2: Let Descriptions Guide You
- A resource's description can tell you whether it is relevant to your task
- If the resource's description makes it look irrelevant to your task, skip it
- If you only need to understand what a resource does / is, descriptions can be *enough*. You don't need to read resources when the descriptions already gave you the necessary context

### 3: Go Deeper with Intent
- When exploring, ask yourself *what* you need to discover and understand fully.
- Which resources do you need to **know the code** of.
- These questions should guide you to navigate deeper in the topology to do your task to its best

### 4: Beware of TopologyWarnings
- When **editing**, you'll usually receive helpful warnings on resources that might have been affected by your changes. Keep those in mind and solve them as they come up

`
}

func behavioralRulesSection() string {
	return `## Behavioral Rules

1. **Be concise** — Prefer short answers. Show what you found and what you changed, not how you did it.
2. **Do not parse code yourself** — Always use topology tools. The database is the source of truth.
3. **Do not guess** — If a tool returns no results or an error, report it accurately. Do not fabricate code or relationships.
4. **One level deep** — Read the CONTEXT section and only drill deeper when essential. Descriptions are designed to answer most questions at the surface level.
5. **Topology is always current** — After any ` + "`edit`" + `, the topology updates automatically. You never need to request a re-scan.
6. **Avoid circular exploration** — If you already read a resource, do not re-read it in the same session. Trust your context.

`
}

func endingSection() string {
	return "Good Luck in your task.\n"
}

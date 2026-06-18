package prompts

import (
	"fmt"
	"strings"

	"aracne/internal/helper"
)

func ClaudeMdContent() string {
	return ClaudeMdContentForAgent(helper.DefaultConfig().EffectiveAgent("claude_code", "main"))
}

func AgentsMdContent() string {
	return AgentsMdContentForAgent(helper.DefaultConfig().EffectiveAgent("opencode", "main"))
}

// ClaudeMdContentForAgent renders the CLAUDE.md guidance for a resolved agent
// config, describing each capability as an MCP tool or a native tool depending
// on the agent's mcp_tools / blocked_tools.
func ClaudeMdContentForAgent(eff helper.AgentConfig) string {
	return agentInstructionsContent(eff, "mcp__aracne__")
}

// AgentsMdContentForAgent renders the OpenCode AGENTS.md guidance.
func AgentsMdContentForAgent(eff helper.AgentConfig) string {
	return agentInstructionsContent(eff, "aracne_")
}

func bt(s string) string {
	return "`" + s + "`"
}

// --- tool-membership predicates -------------------------------------------

var splitReadTools = []string{"read_function", "read_struct", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency"}

func inList(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func hasMCPTool(eff helper.AgentConfig, name string) bool { return inList(eff.MCPTools, name) }

func nativeAllowed(eff helper.AgentConfig, key string) bool { return !inList(eff.BlockedTools, key) }

func presentSplitReads(eff helper.AgentConfig) []string {
	var out []string
	for _, n := range splitReadTools {
		if hasMCPTool(eff, n) {
			out = append(out, n)
		}
	}
	return out
}

func usesMCPRead(eff helper.AgentConfig) bool {
	return hasMCPTool(eff, "read") || len(presentSplitReads(eff)) > 0
}

// --- sections --------------------------------------------------------------

func agentInstructionsContent(eff helper.AgentConfig, mcpToolPrefix string) string {
	var b strings.Builder

	b.WriteString(introductionSection())
	b.WriteString(navigationModelSection(eff))
	b.WriteString(lookupToolsSection(eff, mcpToolPrefix))
	b.WriteString(grepSection(eff, mcpToolPrefix))
	b.WriteString(resourceContextSection(eff))
	b.WriteString(editWriteSection(eff, mcpToolPrefix))
	b.WriteString(otherSection(eff, mcpToolPrefix))
	// The guard hook is a Claude Code feature (PreToolUse/PostToolUse);
	// OpenCode enforces the same intent through its permission block instead.
	if mcpToolPrefix == "mcp__aracne__" {
		b.WriteString(guardNoteSection())
	}
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

func navigationModelSection(eff helper.AgentConfig) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "## Navigation Model\n\n")
	fmt.Fprintf(b, "The topology is a directed graph can enhance your information about the repository you're using if you use it correctly.\n\n")
	fmt.Fprintf(b, "**Navigation Flow:**\n")
	fmt.Fprintf(b, "1. Use `ls` to understand the project file layout\n")

	if !usesMCPRead(eff) {
		fmt.Fprintf(b, "2. Use your `read` tool to read resources and files\n\n")
		return b.String()
	}

	fmt.Fprintf(b, "2. Use lookup MCP tools to get a resource's full context with interconnected relationships\n")
	fmt.Fprintf(b, "\n**Note: Never try to use `read` native tool, use MCP lookups instead**\n\n")

	return b.String()
}

func lookupToolsSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	b := &strings.Builder{}

	if usesMCPRead(eff) {
		b.WriteString("## MCP Lookup tools:\n")
		splits := presentSplitReads(eff)
		if len(splits) > 0 {
			for _, name := range splits {
				fmt.Fprintf(b, "- %s: %s\n", bt(mcpToolPrefix+name), splitReadDescription(name))
			}
		} else {
			fmt.Fprintf(b, "- %s: This command will give you the code and full context for any resource you want. These include: Files, Functions, Structs, etc. The tool receives a Resource ID, which can be the file's path or the ID of any resource.\n", bt(mcpToolPrefix+"read"))
		}
		b.WriteString("\n")
	}

	b.WriteString("Note: Do *not* use \"cat\", \"Get-Content\" or any other OS command to read files")

	return b.String()
}

func splitReadDescription(name string) string {
	switch name {
	case "read_function":
		return "Reads the function and context for resources it uses, receives a function ID."
	case "read_struct":
		return "Reads the struct and context for resources it uses, receives a struct ID."
	case "read_interface":
		return "Reads the interface and context for which resources it is implemented by, receives an interface ID."
	case "read_named_type":
		return "Reads the named type and context for which resources it is used by, receives a named type ID."
	case "read_file":
		return "Reads the content of a file, receives the file path."
	case "read_package":
		return "Reads the package and context for which resources it is used by, receives a package ID."
	case "read_dependency":
		return "Reads the dependency and context for which resources it is used by, receives a dependency ID."
	default:
		return "Reads the resource and its context."
	}
}

func grepSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	if hasMCPTool(eff, "grep") {
		return fmt.Sprintf("## Grep/Search\n\nUse the MCP tool %s for content search. It returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.\n\nDo *not* use your native `grep` tool.\nDo not use `grep`, `Select-String` or `rg` in the terminal", bt(mcpToolPrefix+"grep"))
	}
	if nativeAllowed(eff, "grep") {
		return "## Grep/Search\n\nUse your native `grep`/`Grep` search tool for content search. When you need topology metadata in results, use `arac grep <pattern> [path]`; it returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.\n\n"
	}
	return ""
}

func resourceContextSection(eff helper.AgentConfig) string {
	// The "# CONTEXT:" output is produced only by the MCP read tools.
	if !usesMCPRead(eff) {
		return ""
	}

	b := &strings.Builder{}
	fmt.Fprintf(b, "## Resource Context\n\n")
	fmt.Fprintf(b, "When you call a MCP Lookup Tool, the output has two sections:\n\n")
	fmt.Fprintf(b, "**Code Block:** The resource's full source code, plus relevant imports and enclosing type (for methods).\n\n")
	fmt.Fprintf(b, "**%s Section:** A structured hierarchical listing of everything the resource touches. Each entry is keyed by the resource's full ID, which you can pass directly to a lookup tool to drill deeper:\n\n", bt("# CONTEXT:"))
	fmt.Fprintf(b, "```\n")
	fmt.Fprintf(b, "# CONTEXT:\n")
	fmt.Fprintf(b, "## pkg.InterfaceName: Description\n")
	fmt.Fprintf(b, "    pkg.ImplStruct: Description\n")
	fmt.Fprintf(b, "        pkg.(ImplStruct).Method: Description\n")
	fmt.Fprintf(b, "## pkg.OtherStruct: Description\n")
	fmt.Fprintf(b, "    pkg.(OtherStruct).Method: Description\n")
	fmt.Fprintf(b, "## pkg.CalledFunction: Description\n")
	fmt.Fprintf(b, "## pkg.ExtVarName = value\n")
	fmt.Fprintf(b, "```\n\n")
	fmt.Fprintf(b, "Use the CONTEXT section to understand relationships **without making additional tool calls**.\n\n")
	return b.String()
}

func editWriteSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	if hasMCPTool(eff, "edit") || hasMCPTool(eff, "write") {
		return fmt.Sprintf("## Edit and Write:\n\nYou can edit files using the MCP tool %s.\nYou can write files using the MCP tool %s.\nAfter editing or writing, the context for the topology will be automatically updated to reflect your actions.\n\n**Note: NEVER try to edit or write using your native tools**\n\n", bt(mcpToolPrefix+"edit"), bt(mcpToolPrefix+"write"))
	}
	if nativeAllowed(eff, "edit") {
		return "## Edit and Write:\n\nYou can edit files using your native `edit` tool.\nYou can write files using your native `write` tool.\nAfter editing or writing, the context for the topology will be automatically updated to reflect your actions.\n\n"
	}
	return ""
}

func otherSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	hasBugReport := hasMCPTool(eff, "bug_report")
	hasWarnings := hasMCPTool(eff, "warnings_list")
	if !hasBugReport && !hasWarnings {
		return ""
	}

	b := &strings.Builder{}
	b.WriteString("## Other:\n\n")
	if hasBugReport {
		fmt.Fprintf(b, "- If you find a bug that is not relevant to your task, *do not fix it*. Instead, report it using %s\n", bt(mcpToolPrefix+"bug_report"))
	}
	if hasWarnings {
		fmt.Fprintf(b, "- If you want to check for any topology warnings, you can do it using %s\n", bt(mcpToolPrefix+"warnings_list"))
	}
	b.WriteString("\n")
	return b.String()
}

func guardNoteSection() string {
	return "## Tool Guard\n\n" +
		"An `arac guard` hook watches your tool calls. Whenever you use a native tool " +
		"(`Read`/`Grep`/`Edit`/`Write`) or a shell equivalent (`cat`/`head`/`tail`/`less`/`grep`/`rg`/`sed`/`awk`, " +
		"or PowerShell `Get-Content`/`Select-String`), you are reminded to use the matching aracne MCP tool instead.\n\n" +
		"Any tool listed in `blocked_tools` for the `claude_code` harness is blocked outright. Blocking `grep` also " +
		"blocks `grep`/`rg`/`Select-String` run through the Bash tool; blocking `bash` blocks the Bash tool entirely. " +
		"The guard uses the main agent's `blocked_tools`; per-sub-agent blocking is enforced by each sub-agent's tool " +
		"allow-list, not by this hook.\n\n"
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

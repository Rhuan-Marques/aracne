package prompts

import (
	"fmt"
	"strings"

	"ltp/internal/helper"
)

func ClaudeMdContent() string {
	return ClaudeMdContentForModes(helper.DefaultToolModes())
}

func AgentsMdContent() string {
	return AgentsMdContentForModes(helper.DefaultToolModes())
}

func ClaudeMdContentForModes(modes helper.ToolModes) string {
	return agentInstructionsContent(modes, "mcp__llm-topology__")
}

func AgentsMdContentForModes(modes helper.ToolModes) string {
	return agentInstructionsContent(modes, "llm-topology_")
}

func agentInstructionsContent(modes helper.ToolModes, mcpToolPrefix string) string {
	bt := "`"
	var mcpTools []string
	var terminalCommands []string
	if modes.Read == helper.ReadModeMCP {
		mcpTools = append(mcpTools, fmt.Sprintf("| %[1]s%[2]sread_file%[1]s | Read raw file contents |", bt, mcpToolPrefix))
	} else if modes.Read == helper.ReadModeTerminal {
		terminalCommands = append(terminalCommands, fmt.Sprintf("| %[1]sltp read_file <path>%[1]s | Read raw file contents |", bt))
	}
	if modes.Edit == helper.EditModeMCP {
		mcpTools = append(mcpTools,
			fmt.Sprintf("| %[1]s%[2]sedit%[1]s | Edit files and update topology automatically |", bt, mcpToolPrefix),
			fmt.Sprintf("| %[1]s%[2]swrite%[1]s | Write files and update topology automatically |", bt, mcpToolPrefix),
		)
	} else if modes.Edit == helper.EditModeTerminal {
		terminalCommands = append(terminalCommands,
			fmt.Sprintf("| %[1]sltp edit%[1]s | Edit files through terminal commands |", bt),
			fmt.Sprintf("| %[1]sltp write%[1]s | Write files through terminal commands |", bt),
		)
	}
	if modes.Other == helper.OtherModeMCP {
		mcpTools = append(mcpTools,
			fmt.Sprintf("| %[1]s%[2]sread_function%[1]s | Function source + connected context |", bt, mcpToolPrefix),
			fmt.Sprintf("| %[1]s%[2]sread_struct%[1]s | Struct source + methods/interfaces/context |", bt, mcpToolPrefix),
			fmt.Sprintf("| %[1]s%[2]swarnings_list%[1]s | List topology warnings |", bt, mcpToolPrefix),
			fmt.Sprintf("| %[1]s%[2]sbug_report%[1]s | Report a confirmed bug on a resource node |", bt, mcpToolPrefix),
		)
	} else {
		terminalCommands = append(terminalCommands,
			fmt.Sprintf("| %[1]sltp read_function <name>%[1]s | Function source + connected context |", bt),
			fmt.Sprintf("| %[1]sltp read_struct <name>%[1]s | Struct source + methods/interfaces/context |", bt),
			fmt.Sprintf("| %[1]sltp warnings list%[1]s | List topology warnings |", bt),
			fmt.Sprintf("| %[1]sltp bug report --node <id> --description <text>%[1]s | Report a confirmed bug on a resource node |", bt),
		)
	}

	sections := toolSection("MCP Tools", "Use these llm-topology MCP tools for MCP-mode topology operations:", "Tool", mcpTools) +
		toolSection("Terminal Commands", "Use these ltp commands for terminal-mode topology operations:", "Command", terminalCommands)

	return fmt.Sprintf(`# LTP Integration

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

%[1]s

This is it for ltp integration

`, sections)
}

func toolSection(title, lead, column string, rows []string) string {
	if len(rows) == 0 {
		return ""
	}
	return fmt.Sprintf("## %s\n\n%s\n\n| %s | Purpose |\n|------|---------|\n%s\n\n", title, lead, column, strings.Join(rows, "\n"))
}

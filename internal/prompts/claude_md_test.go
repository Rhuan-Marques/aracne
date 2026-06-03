package prompts

import (
	"strings"
	"testing"

	"ltp/internal/helper"
)

func TestAgentInstructionsOmitNativeReadEditTools(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := AgentsMdContentForModes(modes)

	for _, forbidden := range []string{
		"## Agent Workflows",
		"## Guidelines",
		"## Terminal Commands",
		"`read`",
		"`edit / write`",
		"llm-topology_read_file",
		"llm-topology_edit",
		"llm-topology_write",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated AGENTS.md contains %q:\n%s", forbidden, got)
		}
	}
	for _, required := range []string{
		"## MCP Tools",
		"`llm-topology_read_function`",
		"`llm-topology_read_struct`",
		"`llm-topology_warnings_list`",
		"`llm-topology_bug_report`",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated AGENTS.md missing %q:\n%s", required, got)
		}
	}
}

func TestAgentInstructionsOmitMCPSectionWhenNoMCPModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeTerminal,
		Edit:  helper.EditModeTerminal,
		Other: helper.OtherModeTerminal,
	}

	got := ClaudeMdContentForModes(modes)

	if strings.Contains(got, "## MCP Tools") {
		t.Fatalf("generated CLAUDE.md contains MCP section:\n%s", got)
	}
	for _, required := range []string{
		"## Terminal Commands",
		"`ltp read_file <path>`",
		"`ltp edit`",
		"`ltp write`",
		"`ltp read_function <name>`",
		"`ltp read_struct <name>`",
		"`ltp warnings list`",
		"`ltp bug report --node <id> --description <text>`",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", required, got)
		}
	}
}

func TestAgentInstructionsOmitTerminalSectionWhenNoTerminalModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeMCP,
		Other: helper.OtherModeMCP,
	}

	got := ClaudeMdContentForModes(modes)

	if strings.Contains(got, "## Terminal Commands") {
		t.Fatalf("generated CLAUDE.md contains terminal section:\n%s", got)
	}
	for _, required := range []string{
		"## MCP Tools",
		"`mcp__llm-topology__read_file`",
		"`mcp__llm-topology__edit`",
		"`mcp__llm-topology__write`",
		"`mcp__llm-topology__read_function`",
		"`mcp__llm-topology__read_struct`",
		"`mcp__llm-topology__warnings_list`",
		"`mcp__llm-topology__bug_report`",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", required, got)
		}
	}
}

package prompts

import (
	"strings"
	"testing"

	"ltp/internal/helper"
	"ltp/internal/topology/domain"
)

func TestAgentInstructionsOmitNativeReadEditTools(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := AgentsMdContentForModes(modes, nil)

	for _, forbidden := range []string{
		"## ltp read Terminal command:",
		"llm-topology_edit",
		"llm-topology_write",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated AGENTS.md contains %q:\n%s", forbidden, got)
		}
	}
	for _, required := range []string{
		"## MCP Lookup tools:",
		"`llm-topology_read`",
		"## Navigation Model",
		"## Resource Context",
		"## How to Navigate:",
		"## Behavioral Rules",
		"`llm-topology_warnings_list`",
		"`llm-topology_bug_report`",
		"## Edit and Write:",
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

	got := ClaudeMdContentForModes(modes, nil)

	if strings.Contains(got, "## MCP Lookup tools:") {
		t.Fatalf("generated CLAUDE.md contains MCP Lookup section:\n%s", got)
	}
	for _, required := range []string{
		"## ltp read Terminal command:",
		"## Navigation Model",
		"## How to Navigate:",
		"## Behavioral Rules",
		"ltp warnings list",
		"ltp bug report --resource <id> --description <text>",
		"## Edit:",
		"ltp edit {file_path}",
		"ltp write {file_path}",
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

	got := ClaudeMdContentForModes(modes, nil)

	if strings.Contains(got, "## ltp read Terminal command:") {
		t.Fatalf("generated CLAUDE.md contains terminal section:\n%s", got)
	}
	for _, required := range []string{
		"## MCP Lookup tools:",
		"## Navigation Model",
		"## How to Navigate:",
		"## Behavioral Rules",
		"`mcp__llm-topology__read`",
		"`mcp__llm-topology__edit`",
		"`mcp__llm-topology__write`",
		"`mcp__llm-topology__warnings_list`",
		"`mcp__llm-topology__bug_report`",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", required, got)
		}
	}
}

func TestAgentInstructionsWithMCPReadSplits(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	splits := map[domain.ResourceKind]bool{
		domain.ResourceFunction:   true,
		domain.ResourceType:       true,
		domain.ResourceInterface:  true,
		domain.ResourceFile:       true,
		domain.ResourcePackage:    true,
		domain.ResourceDependency: true,
	}

	got := ClaudeMdContentForModes(modes, splits)

	for _, forbidden := range []string{
		"`mcp__llm-topology__read`",
		"`mcp__llm-topology__edit`",
		"`mcp__llm-topology__write`",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated CLAUDE.md contains %q when splits active:\n%s", forbidden, got)
		}
	}
	for _, required := range []string{
		"## MCP Lookup tools:",
		"`mcp__llm-topology__read_function`",
		"`mcp__llm-topology__read_struct`",
		"`mcp__llm-topology__read_interface`",
		"`mcp__llm-topology__read_file`",
		"`mcp__llm-topology__read_package`",
		"`mcp__llm-topology__read_dependency`",
		"`mcp__llm-topology__warnings_list`",
		"`mcp__llm-topology__bug_report`",
		"## Navigation Model",
		"## Resource Context",
		"## How to Navigate:",
		"## Behavioral Rules",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q with splits:\n%s", required, got)
		}
	}
}

func TestAgentInstructionsWithPartialMCPReadSplits(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	splits := map[domain.ResourceKind]bool{
		domain.ResourceFunction: true,
		domain.ResourceFile:     true,
	}

	got := ClaudeMdContentForModes(modes, splits)

	if strings.Contains(got, "`mcp__llm-topology__read_struct`") {
		t.Fatalf("generated CLAUDE.md contains read_struct when not in splits:\n%s", got)
	}
	if strings.Contains(got, "`mcp__llm-topology__read_interface`") {
		t.Fatalf("generated CLAUDE.md contains read_interface when not in splits:\n%s", got)
	}
	for _, required := range []string{
		"`mcp__llm-topology__read_function`",
		"`mcp__llm-topology__read_file`",
		"`mcp__llm-topology__warnings_list`",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", required, got)
		}
	}
}

func TestAgentInstructionsEnding(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := ClaudeMdContentForModes(modes, nil)

	if !strings.Contains(got, "Good Luck in your task.") {
		t.Fatalf("generated CLAUDE.md missing ending:\n%s", got)
	}
	if strings.Contains(got, "This is it for ltp integration") {
		t.Fatalf("generated CLAUDE.md contains old ending:\n%s", got)
	}
}

func TestAgentInstructionsNoPrioritiesSection(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := ClaudeMdContentForModes(modes, nil)

	if strings.Contains(got, "## Priorities") {
		t.Fatalf("generated CLAUDE.md contains old Priorities section:\n%s", got)
	}
	if strings.Contains(got, "## Description Generation") {
		t.Fatalf("generated CLAUDE.md contains old Description Generation section:\n%s", got)
	}
}

func TestAgentInstructionsNewSectionsPresent(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := ClaudeMdContentForModes(modes, nil)

	for _, section := range []string{
		"## How to Navigate:",
		"## Edit and Write:",
		"## Other:",
	} {
		if !strings.Contains(got, section) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", section, got)
		}
	}
}

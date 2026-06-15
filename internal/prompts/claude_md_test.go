package prompts

import (
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func TestAgentInstructionsOmitNativeReadEditTools(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
	}

	got := AgentsMdContentForModes(modes, nil)

	// With Read=Native there are no MCP/terminal read tools, so the read/lookup
	// sections (and the CONTEXT explanation they produce) must be absent — the
	// agent reads natively. Other-mode tools (warnings/bug) remain via MCP.
	for _, forbidden := range []string{
		"## arac read Terminal command:",
		"## MCP Lookup tools:",
		"## Resource Context",
		"`aracne_read`",
		"aracne_edit",
		"aracne_write",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated AGENTS.md contains %q:\n%s", forbidden, got)
		}
	}
	for _, required := range []string{
		"## Navigation Model",
		"Use your `read` tool",
		"## How to Navigate:",
		"## Behavioral Rules",
		"`aracne_warnings_list`",
		"`aracne_bug_report`",
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
		"## arac read Terminal command:",
		"## Navigation Model",
		"## How to Navigate:",
		"## Behavioral Rules",
		"arac warnings list",
		"arac bug report --resource <id> --description <text>",
		"## Edit:",
		"arac edit {file_path}",
		"arac write {file_path}",
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

	if strings.Contains(got, "## arac read Terminal command:") {
		t.Fatalf("generated CLAUDE.md contains terminal section:\n%s", got)
	}
	for _, required := range []string{
		"## MCP Lookup tools:",
		"## Navigation Model",
		"## How to Navigate:",
		"## Behavioral Rules",
		"`mcp__aracne__read`",
		"`mcp__aracne__edit`",
		"`mcp__aracne__write`",
		"`mcp__aracne__warnings_list`",
		"`mcp__aracne__bug_report`",
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
		"`mcp__aracne__read`",
		"`mcp__aracne__edit`",
		"`mcp__aracne__write`",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated CLAUDE.md contains %q when splits active:\n%s", forbidden, got)
		}
	}
	for _, required := range []string{
		"## MCP Lookup tools:",
		"`mcp__aracne__read_function`",
		"`mcp__aracne__read_struct`",
		"`mcp__aracne__read_interface`",
		"`mcp__aracne__read_file`",
		"`mcp__aracne__read_package`",
		"`mcp__aracne__read_dependency`",
		"`mcp__aracne__warnings_list`",
		"`mcp__aracne__bug_report`",
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

	if strings.Contains(got, "`mcp__aracne__read_struct`") {
		t.Fatalf("generated CLAUDE.md contains read_struct when not in splits:\n%s", got)
	}
	if strings.Contains(got, "`mcp__aracne__read_interface`") {
		t.Fatalf("generated CLAUDE.md contains read_interface when not in splits:\n%s", got)
	}
	for _, required := range []string{
		"`mcp__aracne__read_function`",
		"`mcp__aracne__read_file`",
		"`mcp__aracne__warnings_list`",
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
	if strings.Contains(got, "This is it for Aracne Project Integration") {
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

func TestAgentInstructionsReadModeGovernsLookupDocs(t *testing.T) {
	// Read=MCP while Other=Terminal: read/lookup docs must follow Read (MCP);
	// meta-tool docs follow Other (terminal). Previously the lookup section was
	// (incorrectly) gated by the Other mode.
	mcpRead := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeTerminal,
		Grep:  helper.GrepModeNative,
	}
	got := ClaudeMdContentForModes(mcpRead, nil)
	if !strings.Contains(got, "## MCP Lookup tools:") || !strings.Contains(got, "`mcp__aracne__read`") {
		t.Fatalf("Read=MCP should produce MCP lookup docs regardless of Other mode:\n%s", got)
	}
	if strings.Contains(got, "## arac read Terminal command:") {
		t.Fatalf("Read=MCP should not produce terminal read docs:\n%s", got)
	}
	if !strings.Contains(got, "arac warnings list") {
		t.Fatalf("Other=Terminal should produce terminal meta-tool docs:\n%s", got)
	}

	// Read=Terminal while Other=MCP: the inverse.
	termRead := helper.ToolModes{
		Read:  helper.ReadModeTerminal,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	got2 := ClaudeMdContentForModes(termRead, nil)
	if !strings.Contains(got2, "## arac read Terminal command:") {
		t.Fatalf("Read=Terminal should produce terminal read docs regardless of Other mode:\n%s", got2)
	}
	if strings.Contains(got2, "## MCP Lookup tools:") {
		t.Fatalf("Read=Terminal should not produce MCP lookup docs:\n%s", got2)
	}
	if !strings.Contains(got2, "`mcp__aracne__warnings_list`") {
		t.Fatalf("Other=MCP should produce MCP meta-tool docs:\n%s", got2)
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

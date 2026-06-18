package prompts

import (
	"strings"
	"testing"

	"aracne/internal/helper"
)

// nativeReadAgent reads natively (no MCP read), edits natively, and keeps the
// MCP meta tools (warnings/bug_report).
func nativeReadAgent() helper.AgentConfig {
	return helper.AgentConfig{
		MCPTools:     []string{"warnings_list", "bug_report"},
		BlockedTools: []string{"grep"},
	}
}

func TestAgentInstructionsOmitNativeReadEditTools(t *testing.T) {
	got := AgentsMdContentForAgent(nativeReadAgent())

	// With native read there are no MCP read tools, so the lookup/CONTEXT
	// sections must be absent. The MCP meta tools (warnings/bug) remain.
	for _, forbidden := range []string{
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

func TestAgentInstructionsDefaultMainAgentUsesMCP(t *testing.T) {
	eff := helper.DefaultConfig().EffectiveAgent("claude_code", "main")
	got := ClaudeMdContentForAgent(eff)

	for _, required := range []string{
		"## MCP Lookup tools:",
		"## Navigation Model",
		"## Resource Context",
		"## How to Navigate:",
		"## Behavioral Rules",
		"`mcp__aracne__read_function`",
		"`mcp__aracne__read_file`",
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
	eff := helper.AgentConfig{
		MCPTools: []string{
			"read_function", "read_struct", "read_interface", "read_file",
			"read_package", "read_dependency", "warnings_list", "bug_report",
		},
		BlockedTools: []string{"read", "grep", "edit", "write"},
	}

	got := ClaudeMdContentForAgent(eff)

	if strings.Contains(got, "`mcp__aracne__read`:") {
		t.Fatalf("generated CLAUDE.md contains unsplit read when splits active:\n%s", got)
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
	eff := helper.AgentConfig{
		MCPTools:     []string{"read_function", "read_file", "warnings_list"},
		BlockedTools: []string{"read", "grep", "edit", "write"},
	}

	got := ClaudeMdContentForAgent(eff)

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
	got := ClaudeMdContentForAgent(helper.DefaultConfig().EffectiveAgent("claude_code", "main"))

	if !strings.Contains(got, "Good Luck in your task.") {
		t.Fatalf("generated CLAUDE.md missing ending:\n%s", got)
	}
}

func TestAgentInstructionsNoTerminalSections(t *testing.T) {
	got := ClaudeMdContentForAgent(helper.DefaultConfig().EffectiveAgent("claude_code", "main"))

	for _, forbidden := range []string{
		"## arac read Terminal command:",
		"## Priorities",
		"## Description Generation",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated CLAUDE.md contains removed section %q:\n%s", forbidden, got)
		}
	}
}

func TestAgentInstructionsNewSectionsPresent(t *testing.T) {
	got := ClaudeMdContentForAgent(helper.DefaultConfig().EffectiveAgent("claude_code", "main"))

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

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
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated CLAUDE.md missing %q:\n%s", required, got)
		}
	}
	// The bug pipeline is a v2 feature and is not in the default profile, so the contract
	// must not advertise it — documenting a tool the agent has not been granted just costs
	// tokens and invites a call that will be refused.
	if strings.Contains(got, "mcp__aracne__bug_report") {
		t.Fatalf("default CLAUDE.md should not advertise the v2 bug tools:\n%s", got)
	}
	// Warn-only: the contract describes when each tool wins rather than forbidding the
	// native ones. A blanket "never use read" is what drove redundant read turns.
	for _, forbidden := range []string{"Never try to use", "NEVER try to edit", "Do *not* use your native"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("contract still forbids native tools (%q):\n%s", forbidden, got)
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

// TestContractMatchesBlockedTools pins that the contract tells the truth about which
// native tools are usable.
//
// A rewrite made the guidance unconditional ("your native grep still works; use whichever
// fits") even when blocked_tools denied it. That combination is the worst of both: the
// agent is told to use a tool the guard will deny, so every attempt costs a turn — which is
// the exact failure the rewrite was meant to remove.
func TestContractMatchesBlockedTools(t *testing.T) {
	warn := helper.DefaultConfig().EffectiveAgent("claude_code", "main")
	warnMd := ClaudeMdContentForAgent(warn)
	if !strings.Contains(warnMd, "native grep also works") {
		t.Fatalf("warn-only contract must say the native tools remain usable:\n%s", warnMd)
	}
	if strings.Contains(warnMd, "is blocked in this project") {
		t.Fatalf("warn-only contract must not claim anything is blocked:\n%s", warnMd)
	}

	blocked := warn
	blocked.BlockedTools = []string{"read", "grep", "edit", "write"}
	blockedMd := ClaudeMdContentForAgent(blocked)
	for _, want := range []string{
		"The native read tool is blocked",
		"The native grep tool is blocked",
		"The native edit/write tools are blocked",
	} {
		if !strings.Contains(blockedMd, want) {
			t.Fatalf("blocked contract missing %q:\n%s", want, blockedMd)
		}
	}
	if strings.Contains(blockedMd, "native grep also works") {
		t.Fatalf("blocked contract must not offer the native grep:\n%s", blockedMd)
	}
}

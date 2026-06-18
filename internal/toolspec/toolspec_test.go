package toolspec

import (
	"strings"
	"testing"
)

func TestSurfaceClassification(t *testing.T) {
	if !IsMCPTool("read_function") || !IsChatTool("read_function") {
		t.Fatal("read_function should be both MCP and chat")
	}
	if IsMCPTool("ls") {
		t.Fatal("ls should not be an MCP tool")
	}
	if !IsChatTool("ls") {
		t.Fatal("ls should be a chat tool")
	}
	if IsMCPTool("nope") || IsChatTool("nope") {
		t.Fatal("unknown tool should not be valid on any surface")
	}
	if !IsNativeTool("bash") || IsNativeTool("read_function") {
		t.Fatal("native-blockable set mismatch")
	}
}

func TestValidateMCPTools(t *testing.T) {
	if err := ValidateMCPTools([]string{"read", "grep", "edit"}); err != nil {
		t.Fatalf("valid tools should pass: %v", err)
	}
	err := ValidateMCPTools([]string{"read", "read_inferface", "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown tools")
	}
	if !strings.Contains(err.Error(), "read_inferface") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error should name the unknown tools: %v", err)
	}
	// A chat-only tool is not a valid MCP tool.
	if ValidateMCPTools([]string{"ls"}) == nil {
		t.Fatal("ls should not validate as an MCP tool")
	}
}

func TestValidateChatTools(t *testing.T) {
	if err := ValidateChatTools([]string{"ls", "bash", "CreateTasks", "read"}); err != nil {
		t.Fatalf("valid chat tools should pass: %v", err)
	}
	if ValidateChatTools([]string{"definitely_not_a_tool"}) == nil {
		t.Fatal("expected error for unknown chat tool")
	}
}

func TestNativeToolKey(t *testing.T) {
	cases := map[string]string{"Read": "read", "Grep": "grep", "Edit": "edit", "Write": "write", "Bash": "bash"}
	for tool, want := range cases {
		got, ok := NativeToolKey(tool)
		if !ok || got != want {
			t.Fatalf("NativeToolKey(%q) = (%q, %v), want (%q, true)", tool, got, ok, want)
		}
	}
	for _, tool := range []string{"MultiEdit", "Glob", "read", "nope"} {
		if _, ok := NativeToolKey(tool); ok {
			t.Fatalf("NativeToolKey(%q) should report false", tool)
		}
	}
}

func TestShellCommandKey(t *testing.T) {
	cases := map[string]string{
		"cat": "read", "head": "read", "tail": "read", "less": "read",
		"grep": "grep", "rg": "grep",
		"sed": "edit", "awk": "edit",
		"Get-Content": "read", "get-content": "read",
		"Select-String": "grep", "SELECT-STRING": "grep",
	}
	for cmd, want := range cases {
		got, ok := ShellCommandKey(cmd)
		if !ok || got != want {
			t.Fatalf("ShellCommandKey(%q) = (%q, %v), want (%q, true)", cmd, got, ok, want)
		}
	}
	for _, cmd := range []string{"git", "npm", "echo", "gc", "sls", "ls", ""} {
		if _, ok := ShellCommandKey(cmd); ok {
			t.Fatalf("ShellCommandKey(%q) should report false", cmd)
		}
	}
}

func TestWarningFor(t *testing.T) {
	for _, key := range []string{"read", "grep", "edit", "write"} {
		if WarningFor(key) == "" {
			t.Fatalf("WarningFor(%q) should be non-empty", key)
		}
	}
	if !strings.Contains(WarningFor("grep"), "mcp__aracne__grep") {
		t.Fatalf("grep warning should reference the MCP tool: %q", WarningFor("grep"))
	}
	for _, key := range []string{"bash", "nope", ""} {
		if WarningFor(key) != "" {
			t.Fatalf("WarningFor(%q) should be empty", key)
		}
	}
}

func TestToolsSection(t *testing.T) {
	out := ToolsSection([]string{"read_function", "bug_report"})
	if !strings.HasPrefix(out, "## Tools\n") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "- `read_function` -- "+Description("read_function")) {
		t.Fatalf("missing read_function listing:\n%s", out)
	}
	if !strings.Contains(out, "- `bug_report` -- ") {
		t.Fatalf("missing bug_report listing:\n%s", out)
	}
	if ToolsSection(nil) != "" {
		t.Fatal("empty input should yield empty section")
	}
}

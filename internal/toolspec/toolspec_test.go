package toolspec

import (
	"strings"
	"testing"
)

func TestSurfaceClassification(t *testing.T) {
	if !IsMCPTool("read") || !IsChatTool("read") {
		t.Fatal("read should be both MCP and chat")
	}
	// The per-kind read tools were collapsed into "read"; naming one in a config is now an
	// error rather than a silently different tool set.
	for _, gone := range []string{"read_function", "read_struct", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency"} {
		if IsMCPTool(gone) || IsChatTool(gone) {
			t.Fatalf("%s should no longer be a valid tool name", gone)
		}
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
	if !IsNativeTool("bash") || IsNativeTool("read_resource") {
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
		for _, nativeRead := range []bool{true, false} {
			if WarningFor(key, nativeRead) == "" {
				t.Fatalf("WarningFor(%q, %v) should be non-empty", key, nativeRead)
			}
		}
	}
	if !strings.Contains(WarningFor("grep", true), "mcp__aracne__grep") {
		t.Fatalf("grep warning should reference the MCP tool: %q", WarningFor("grep", true))
	}
	for _, key := range []string{"bash", "nope", ""} {
		if WarningFor(key, true) != "" {
			t.Fatalf("WarningFor(%q) should be empty", key)
		}
	}
}

// The read tool answers to two names, and the guidance has to name the one the agent was
// actually given -- naming the other sends the model to a tool it does not have.
func TestWarningForRead_NamesTheToolTheAgentHas(t *testing.T) {
	withNative := WarningFor("read", true)
	if !strings.Contains(withNative, "mcp__aracne__"+ReadResourceToolName) {
		t.Fatalf("native read allowed: warning should name %q: %q", ReadResourceToolName, withNative)
	}
	blockedNative := WarningFor("read", false)
	if !strings.Contains(blockedNative, "mcp__aracne__"+ReadToolName+"`") {
		t.Fatalf("native read blocked: warning should name %q: %q", ReadToolName, blockedNative)
	}
	if strings.Contains(blockedNative, ReadResourceToolName) {
		t.Fatalf("native read blocked: warning must not mention %q: %q", ReadResourceToolName, blockedNative)
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

// TestShellCommandKeyForArgs pins the sed/awk classification.
//
// Mapping them to `edit` on the name alone meant the guard refused
// `git log --oneline | sed -n '30,60p'` — a read of command output with no file
// operand and no -i — and billed the agent a turn to be told no. They are
// stream editors: they stand in for `edit` only when they write.
func TestShellCommandKeyForArgs(t *testing.T) {
	tests := []struct {
		name     string
		word     string
		args     []string
		redirect bool
		want     string
		wantOK   bool
	}{
		// unaffected commands keep their static mapping
		{"cat is still a read", "cat", []string{"f"}, false, "read", true},
		{"grep is still grep", "grep", []string{"x", "f"}, false, "grep", true},
		{"unknown command", "npm", []string{"test"}, false, "", false},
		{"redirect does not promote a reader", "cat", []string{"f"}, true, "read", true},

		// sed: reading forms
		{"sed print range", "sed", []string{"-n", "1,10p"}, false, "read", true},
		{"sed substitute to stdout", "sed", []string{"s/a/b/"}, false, "read", true},
		{"sed extended regexp", "sed", []string{"-E", "s/a/b/"}, false, "read", true},
		{"sed expression flag", "sed", []string{"-e", "s/i/x/"}, false, "read", true},
		{"sed no args", "sed", nil, false, "read", true},

		// sed: writing forms
		{"sed in place", "sed", []string{"-i", "s/a/b/", "f"}, false, "edit", true},
		{"sed in place long", "sed", []string{"--in-place", "s/a/b/", "f"}, false, "edit", true},
		{"sed in place long with suffix", "sed", []string{"--in-place=.bak", "s/a/b/", "f"}, false, "edit", true},
		{"sed in place with backup suffix", "sed", []string{"-i.bak", "s/a/b/", "f"}, false, "edit", true},
		{"sed in place in a cluster", "sed", []string{"-ni", "s/a/b/", "f"}, false, "edit", true},
		{"sed in place in a cluster after E", "sed", []string{"-Ei", "s/a/b/", "f"}, false, "edit", true},
		{"sed redirecting output", "sed", []string{"s/a/b/", "in"}, true, "edit", true},

		// awk
		{"awk filtering", "awk", []string{"{print $2}"}, false, "read", true},
		{"awk field flag", "awk", []string{"-F,", "{print $1}"}, false, "read", true},
		{"awk gawk inplace", "awk", []string{"-i", "inplace", "{print}"}, false, "edit", true},
		{"awk include inplace", "awk", []string{"--include=inplace", "{print}"}, false, "edit", true},
		{"awk redirecting output", "awk", []string{"{print}"}, true, "edit", true},

		// path- and extension-qualified names still resolve
		{"absolute path sed", "/usr/bin/sed", []string{"-n", "1p"}, false, "read", true},
		{"windows sed", "sed.exe", []string{"-i", "s/a/b/", "f"}, false, "edit", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ShellCommandKeyForArgs(tt.word, tt.args, tt.redirect)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ShellCommandKeyForArgs(%q, %v, redirect=%v) = (%q, %v), want (%q, %v)",
					tt.word, tt.args, tt.redirect, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// The name-only mapping is unchanged: it is the pessimistic default, and other
// callers still rely on it.
func TestShellCommandKeyUnchangedForStreamEditors(t *testing.T) {
	for _, word := range []string{"sed", "awk"} {
		if got, ok := ShellCommandKey(word); !ok || got != "edit" {
			t.Errorf("ShellCommandKey(%q) = (%q, %v), want (edit, true)", word, got, ok)
		}
	}
}

func TestResolveReadToolName(t *testing.T) {
	// With a native read still available the aracne tool takes the longer name, so the two are
	// never confusable in one session.
	if got := ResolveReadToolName(true); got != ReadResourceToolName {
		t.Fatalf("ResolveReadToolName(true) = %q, want %q", got, ReadResourceToolName)
	}
	if got := ResolveReadToolName(false); got != ReadToolName {
		t.Fatalf("ResolveReadToolName(false) = %q, want %q", got, ReadToolName)
	}
	for _, name := range []string{ReadToolName, ReadResourceToolName} {
		if !IsReadToolName(name) {
			t.Fatalf("IsReadToolName(%q) = false", name)
		}
	}
	if IsReadToolName("grep") {
		t.Fatal("IsReadToolName(grep) should be false")
	}
}

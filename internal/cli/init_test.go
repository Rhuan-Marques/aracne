package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func TestParseReadToolMode_Valid(t *testing.T) {
	for _, mode := range []helper.ReadToolMode{helper.ReadModeNative, helper.ReadModeMCP, helper.ReadModeTerminal} {
		got, err := parseReadToolMode(string(mode))
		if err != nil {
			t.Fatalf("parseReadToolMode(%q): %v", mode, err)
		}
		if got != mode {
			t.Fatalf("parseReadToolMode(%q) = %q, want %q", mode, got, mode)
		}
	}
}

func TestParseReadToolMode_Invalid(t *testing.T) {
	_, err := parseReadToolMode("bogus")
	if err == nil {
		t.Fatal("expected error for bogus read mode")
	}
	if !strings.Contains(err.Error(), "invalid --read-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseEditToolMode_Valid(t *testing.T) {
	for _, mode := range []helper.EditToolMode{helper.EditModeNative, helper.EditModeMCP, helper.EditModeTerminal} {
		got, err := parseEditToolMode(string(mode))
		if err != nil {
			t.Fatalf("parseEditToolMode(%q): %v", mode, err)
		}
		if got != mode {
			t.Fatalf("parseEditToolMode(%q) = %q, want %q", mode, got, mode)
		}
	}
}

func TestParseEditToolMode_Invalid(t *testing.T) {
	_, err := parseEditToolMode("bogus")
	if err == nil {
		t.Fatal("expected error for bogus edit mode")
	}
	if !strings.Contains(err.Error(), "invalid --edit-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseOtherToolMode_Valid(t *testing.T) {
	for _, mode := range []helper.OtherToolMode{helper.OtherModeMCP, helper.OtherModeTerminal} {
		got, err := parseOtherToolMode(string(mode))
		if err != nil {
			t.Fatalf("parseOtherToolMode(%q): %v", mode, err)
		}
		if got != mode {
			t.Fatalf("parseOtherToolMode(%q) = %q, want %q", mode, got, mode)
		}
	}
}

func TestParseOtherToolMode_Invalid(t *testing.T) {
	_, err := parseOtherToolMode("bogus")
	if err == nil {
		t.Fatal("expected error for bogus other mode")
	}
	if !strings.Contains(err.Error(), "invalid --other-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseGrepToolMode_Valid(t *testing.T) {
	for _, mode := range []helper.GrepToolMode{helper.GrepModeNative, helper.GrepModeMCP, helper.GrepModeTerminal} {
		got, err := parseGrepToolMode(string(mode))
		if err != nil {
			t.Fatalf("parseGrepToolMode(%q): %v", mode, err)
		}
		if got != mode {
			t.Fatalf("parseGrepToolMode(%q) = %q, want %q", mode, got, mode)
		}
	}
}

func TestParseGrepToolMode_Invalid(t *testing.T) {
	_, err := parseGrepToolMode("bogus")
	if err == nil {
		t.Fatal("expected error for bogus grep mode")
	}
	if !strings.Contains(err.Error(), "invalid --grep-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnyMCPMode_AllMCP(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeMCP,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeMCP,
	}
	if !anyMCPMode(modes) {
		t.Fatal("all MCP modes should return true")
	}
}

func TestAnyMCPMode_None(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeTerminal,
		Grep:  helper.GrepModeNative,
	}
	if anyMCPMode(modes) {
		t.Fatal("no MCP modes should return false")
	}
}

func TestAnyMCPMode_Partial(t *testing.T) {
	cases := []helper.ToolModes{
		{Read: helper.ReadModeMCP, Edit: helper.EditModeNative, Other: helper.OtherModeTerminal, Grep: helper.GrepModeNative},
		{Read: helper.ReadModeNative, Edit: helper.EditModeMCP, Other: helper.OtherModeTerminal, Grep: helper.GrepModeNative},
		{Read: helper.ReadModeNative, Edit: helper.EditModeNative, Other: helper.OtherModeMCP, Grep: helper.GrepModeNative},
		{Read: helper.ReadModeNative, Edit: helper.EditModeNative, Other: helper.OtherModeTerminal, Grep: helper.GrepModeMCP},
	}
	for i, modes := range cases {
		if !anyMCPMode(modes) {
			t.Fatalf("case %d: expected true for at least one MCP mode: %+v", i, modes)
		}
	}
}

func TestNativePermission(t *testing.T) {
	if nativePermission(true) != "allow" {
		t.Fatalf("nativePermission(true) = %q, want allow", nativePermission(true))
	}
	if nativePermission(false) != "deny" {
		t.Fatalf("nativePermission(false) = %q, want deny", nativePermission(false))
	}
}

func TestOpenCodePaths_Local(t *testing.T) {
	configPath, configDir, agentsMdPath := opencodePaths(false)
	if configPath != ".opencode/opencode.json" {
		t.Fatalf("configPath = %q, want .opencode/opencode.json", configPath)
	}
	if configDir != ".opencode" {
		t.Fatalf("configDir = %q, want .opencode", configDir)
	}
	if agentsMdPath != "AGENTS.md" {
		t.Fatalf("agentsMdPath = %q, want AGENTS.md", agentsMdPath)
	}
}

func TestOpenCodePaths_Global(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath, configDir, agentsMdPath := opencodePaths(true)
	wantConfigPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	wantConfigDir := filepath.Join(home, ".config", "opencode")
	wantAgentsMd := filepath.Join(home, ".config", "opencode", "AGENTS.md")

	if configPath != wantConfigPath {
		t.Fatalf("configPath = %q, want %q", configPath, wantConfigPath)
	}
	if configDir != wantConfigDir {
		t.Fatalf("configDir = %q, want %q", configDir, wantConfigDir)
	}
	if agentsMdPath != wantAgentsMd {
		t.Fatalf("agentsMdPath = %q, want %q", agentsMdPath, wantAgentsMd)
	}
}

func TestClaudePaths_Local(t *testing.T) {
	mcpPath, commandsDir, agentsDir, claudeMdPath := claudePaths(false)
	if mcpPath != ".mcp.json" {
		t.Fatalf("mcpPath = %q, want .mcp.json", mcpPath)
	}
	if commandsDir != ".claude/commands" {
		t.Fatalf("commandsDir = %q, want .claude/commands", commandsDir)
	}
	if agentsDir != ".claude/agents" {
		t.Fatalf("agentsDir = %q, want .claude/agents", agentsDir)
	}
	if claudeMdPath != "CLAUDE.md" {
		t.Fatalf("claudeMdPath = %q, want CLAUDE.md", claudeMdPath)
	}
}

func TestClaudePaths_Global(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mcpPath, commandsDir, agentsDir, claudeMdPath := claudePaths(true)
	wantMcpPath := filepath.Join(home, ".claude.json")
	wantCommandsDir := filepath.Join(home, ".claude", "commands")
	wantAgentsDir := filepath.Join(home, ".claude", "agents")
	wantClaudeMd := filepath.Join(home, ".claude", "CLAUDE.md")

	if mcpPath != wantMcpPath {
		t.Fatalf("mcpPath = %q, want %q", mcpPath, wantMcpPath)
	}
	if commandsDir != wantCommandsDir {
		t.Fatalf("commandsDir = %q, want %q", commandsDir, wantCommandsDir)
	}
	if agentsDir != wantAgentsDir {
		t.Fatalf("agentsDir = %q, want %q", agentsDir, wantAgentsDir)
	}
	if claudeMdPath != wantClaudeMd {
		t.Fatalf("claudeMdPath = %q, want %q", claudeMdPath, wantClaudeMd)
	}
}

func TestReadJSONConfig_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	config := readJSONConfig(path)
	if config == nil {
		t.Fatal("expected non-nil config for empty file")
	}
	if len(config) != 0 {
		t.Fatalf("expected empty config, got %v", config)
	}
}

func TestReadJSONConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"key": "value", "num": 42}`), 0644)

	config := readJSONConfig(path)
	if config["key"] != "value" {
		t.Fatalf("key = %q, want value", config["key"])
	}
	if config["num"] != float64(42) {
		t.Fatalf("num = %v, want 42", config["num"])
	}
}

func TestWriteJSONConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")
	config := map[string]interface{}{
		"name": "test",
		"nested": map[string]interface{}{
			"enabled": true,
		},
	}
	writeJSONConfig(path, config)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"name": "test"`) {
		t.Fatalf("missing name field:\n%s", content)
	}
	if !strings.Contains(content, `"enabled": true`) {
		t.Fatalf("missing nested enabled field:\n%s", content)
	}
	if !strings.HasSuffix(content, "\n") {
		t.Fatal("expected trailing newline")
	}
}

func TestShouldWriteConfig_KeyAbsent(t *testing.T) {
	cfg := map[string]interface{}{}
	result := shouldWriteConfig(cfg, "mcpServers", "/tmp/test.json", "Test", true)
	if !result {
		t.Fatal("should return true when key is absent")
	}
}

func TestShouldWriteConfig_KeyExistsAutoYes(t *testing.T) {
	cfg := map[string]interface{}{"mcp": "existing"}
	result := shouldWriteConfig(cfg, "mcp", "/tmp/test.json", "Test", true)
	if !result {
		t.Fatal("should return true when key exists and autoYes is true")
	}
}

func TestBugHunterCommandForAgent(t *testing.T) {
	cmd := bugHunterCommandForAgent("bug-hunter")
	if !strings.Contains(cmd, "bug-hunter") {
		t.Fatalf("missing agent reference in: %s", cmd)
	}
	if !strings.Contains(cmd, "bug_report") {
		t.Fatalf("missing bug_report reference in: %s", cmd)
	}
}

func TestBugJudgeCommandForAgent(t *testing.T) {
	cmd := bugJudgeCommandForAgent("bug-judge")
	if !strings.Contains(cmd, "bug-judge") {
		t.Fatalf("missing agent reference in: %s", cmd)
	}
}

func TestBugSolverCommandForAgent(t *testing.T) {
	cmd := bugSolverCommandForAgent("bug-solver")
	if !strings.Contains(cmd, "bug-solver") {
		t.Fatalf("missing agent reference in: %s", cmd)
	}
}

func TestProfileAllows(t *testing.T) {
	if !profileAllows(ToolProfileDefault, "read") {
		t.Fatal("default profile should allow read")
	}
	if !profileAllows(ToolProfileBugSolver, "edit") {
		t.Fatal("bug-solver profile should allow edit")
	}
	if profileAllows(ToolProfileDescriptionsExecutor, "edit") {
		t.Fatal("descriptions-executor should NOT allow edit")
	}
}

func TestNeedsTerminal_AllNative(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	if needsTerminal(modes) {
		t.Fatal("no terminal modes should return false")
	}
}

func TestNeedsTerminal_ReadTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeTerminal,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	if !needsTerminal(modes) {
		t.Fatal("read=terminal should return true")
	}
}

func TestNeedsTerminal_EditTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeTerminal,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	if !needsTerminal(modes) {
		t.Fatal("edit=terminal should return true")
	}
}

func TestNeedsTerminal_OtherTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeTerminal,
		Grep:  helper.GrepModeNative,
	}
	if !needsTerminal(modes) {
		t.Fatal("other=terminal should return true")
	}
}

func TestNeedsTerminal_GrepTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeTerminal,
	}
	if !needsTerminal(modes) {
		t.Fatal("grep=terminal should return true")
	}
}

func TestTerminalCommandForTool(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"read_function", "arac read <resource-id>"},
		{"read_struct", "arac read <resource-id>"},
		{"read", "arac read <resource-id>"},
		{"grep", "arac grep <pattern> [path]"},
		{"warnings_list", "arac warnings list"},
		{"bug_report", "arac bug report --node <id> --description <text>"},
		{"bug_list", "arac bug list [--node <id>] [--state <state>]"},
		{"bug_acknowledge", "arac bug acknowledge <bugID>"},
		{"bug_dismiss", "arac bug dismiss <bugID>"},
		{"bug_delete", "arac bug delete <bugID>"},
		{"node_list_no_description", "arac resource list --no-description"},
		{"update_description", "arac update-description <id> <kind> <desc>"},
		{"unknown_tool", "Aracne unknown_tool"},
	}
	for _, tt := range tests {
		got := terminalCommandForTool(tt.tool)
		if got != tt.want {
			t.Fatalf("terminalCommandForTool(%q) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}

func TestClaudeAgentContent_ContainsFrontmatterAndPrompt(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	content := claudeAgentContent("test-agent", "Test agent", ToolProfileDefault, modes, nil, "This is the prompt")

	if !strings.Contains(content, "name: test-agent") {
		t.Fatal("missing name in frontmatter")
	}
	if !strings.Contains(content, "description: Test agent") {
		t.Fatal("missing description in frontmatter")
	}
	if !strings.Contains(content, "mcpServers:") {
		t.Fatal("missing mcpServers section")
	}
	if !strings.Contains(content, "This is the prompt") {
		t.Fatal("missing prompt section")
	}
}

func TestOpenCodeAgentContent_ContainsPermissionsAndPrompt(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	content := openCodeAgentContent("test-agent", "Test agent", ToolProfileDefault, modes, nil, "This is the prompt")

	if !strings.Contains(content, "description: Test agent") {
		t.Fatal("missing description in frontmatter")
	}
	if !strings.Contains(content, "mode: subagent") {
		t.Fatal("missing mode: subagent")
	}
	if !strings.Contains(content, "permission:") {
		t.Fatal("missing permission section")
	}
	if !strings.Contains(content, "This is the prompt") {
		t.Fatal("missing prompt section")
	}
	if !strings.Contains(content, "read: allow") {
		t.Fatal("missing read: allow (native mode)")
	}
	if !strings.Contains(content, "edit: allow") {
		t.Fatal("missing edit: allow (native mode)")
	}
}

func TestClaudeMCPServersFrontmatter_WithMCP(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	fm := claudeMCPServersFrontmatter(modes, ToolProfileBugHunter)
	if !strings.Contains(fm, "mcpServers:") {
		t.Fatal("expected mcpServers section")
	}
	if !strings.Contains(fm, "tool-profile") {
		t.Fatal("expected tool-profile in args")
	}
	if !strings.Contains(fm, string(ToolProfileBugHunter)) {
		t.Fatalf("expected %s in args", ToolProfileBugHunter)
	}
}

func TestClaudeMCPServersFrontmatter_WithoutMCP(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeTerminal,
		Grep:  helper.GrepModeNative,
	}
	fm := claudeMCPServersFrontmatter(modes, ToolProfileDefault)
	if fm != "" {
		t.Fatalf("expected empty string, got: %s", fm)
	}
}

func TestTerminalGuidance_EmptyWhenNoTerminalModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	guidance := terminalGuidance(modes, ToolProfileDefault, nil)
	if guidance != "" {
		t.Fatalf("expected empty for no terminal modes, got: %s", guidance)
	}
}

func TestTerminalGuidance_ReadTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeTerminal,
		Grep:  helper.GrepModeNative,
	}
	guidance := terminalGuidance(modes, ToolProfileDefault, nil)
	if !strings.Contains(guidance, "arac warnings list") {
		t.Fatalf("expected terminal commands for other tools:\n%s", guidance)
	}
}

func TestAllowedMCPToolNames_DefaultModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	names := allowedMCPToolNames(modes, ToolProfileDefault, nil)
	hasRead := false
	hasWarnings := false
	for _, n := range names {
		if n == "read" {
			hasRead = true
		}
		if n == "warnings_list" {
			hasWarnings = true
		}
	}
	if !hasRead {
		t.Fatalf("expected 'read' in allowed names: %v", names)
	}
	if !hasWarnings {
		t.Fatalf("expected 'warnings_list' in allowed names: %v", names)
	}
	for _, n := range names {
		if n == "edit" || n == "write" || n == "grep" {
			t.Fatalf("should not include native-mode tool %q in MCP names: %v", n, names)
		}
	}
}

func TestAllowedMCPToolNames_WithMCPGrep(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeMCP,
	}
	names := allowedMCPToolNames(modes, ToolProfileDefault, nil)
	hasGrep := false
	for _, n := range names {
		if n == "grep" {
			hasGrep = true
		}
	}
	if !hasGrep {
		t.Fatalf("expected 'grep' in allowed names when grep=mcp: %v", names)
	}
}

func TestClaudeToolsForProfile_DefaultModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	tools := claudeToolsForProfile(modes, ToolProfileDefault, nil)
	hasRead := false
	hasWarnings := false
	hasBash := false
	for _, tool := range tools {
		if tool == "Read" {
			hasRead = true
		}
		if strings.Contains(tool, "warnings_list") {
			hasWarnings = true
		}
		if tool == "Bash" {
			hasBash = true
		}
	}
	if !hasRead {
		t.Fatalf("expected 'Read' (native mode) in tools: %v", tools)
	}
	if !hasWarnings {
		t.Fatalf("expected mcp__aracne__warnings_list in tools: %v", tools)
	}
	if hasBash {
		t.Fatalf("should not have Bash when no terminal modes: %v", tools)
	}
}

func TestClaudeToolsForProfile_WithTerminalModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeTerminal,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	tools := claudeToolsForProfile(modes, ToolProfileDefault, nil)
	hasBash := false
	for _, tool := range tools {
		if tool == "Bash" {
			hasBash = true
		}
	}
	if !hasBash {
		t.Fatalf("expected 'Bash' when read=terminal: %v", tools)
	}
}

func TestWriteMarkdownFile_CreatesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	writeMarkdownFile(path, "test label", "content body", true)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "content body" {
		t.Fatalf("content = %q, want %q", string(data), "content body")
	}
}

func TestWriteMarkdownFile_SkipsExistingWithoutAutoYes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("original"), 0644)

	writeMarkdownFile(path, "test label", "new content", false)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("should have kept original content: %q", string(data))
	}
}

func TestWriteMarkdownFile_OverwritesWithAutoYes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("original"), 0644)

	writeMarkdownFile(path, "test label", "new content", true)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "new content" {
		t.Fatalf("content = %q, want %q", string(data), "new content")
	}
}

func TestWriteCommand_CreatesWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeCommand(dir, "test-cmd", "My test command", "command body", true)

	data, err := os.ReadFile(filepath.Join(dir, "test-cmd.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "description: My test command") {
		t.Fatalf("missing description frontmatter:\n%s", content)
	}
	if !strings.Contains(content, "command body") {
		t.Fatalf("missing command body:\n%s", content)
	}
}

func TestWriteOpenCodePrimaryCommand_HasAgentFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodePrimaryCommand(dir, "primary-cmd", "Primary command", "build-agent", "command body", true)

	data, err := os.ReadFile(filepath.Join(dir, "primary-cmd.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "agent: build-agent") {
		t.Fatalf("missing agent frontmatter:\n%s", content)
	}
	if strings.Contains(content, "subtask: true") {
		t.Fatal("primary command should not have subtask: true")
	}
}

func TestWriteOpenCodeCommand_HasSubtask(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodeCommand(dir, "sub-cmd", "Sub command", "worker-agent", "command body", true)

	data, err := os.ReadFile(filepath.Join(dir, "sub-cmd.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "subtask: true") {
		t.Fatalf("missing subtask: true:\n%s", content)
	}
}

func TestWriteAgent_CreatesMarkdown(t *testing.T) {
	dir := t.TempDir()
	content := "---\nname: test\n---\nprompt body"
	writeAgent(dir, "test-agent", content, true)

	data, err := os.ReadFile(filepath.Join(dir, "test-agent.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
	}
}

func TestWriteMarkdownIntegrationFile_WritesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	segment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"

	writeMarkdownIntegrationFile(path, "AGENTS.md", segment)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != segment {
		t.Fatalf("content = %q, want %q", string(data), segment)
	}
}

func TestWriteMarkdownIntegrationFile_ReplacesExistingSegment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	existing := "# Before\n\n# Aracne Project Integration\n\nold\n\nGood Luck in your task.\n\n# After\n"
	os.WriteFile(path, []byte(existing), 0644)

	newSegment := "# Aracne Project Integration\n\nnew\n\nGood Luck in your task.\n"
	writeMarkdownIntegrationFile(path, "AGENTS.md", newSegment)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Before") {
		t.Fatal("missing content before segment")
	}
	if !strings.Contains(content, "# After") {
		t.Fatal("missing content after segment")
	}
	if !strings.Contains(content, "new") {
		t.Fatalf("missing new content:\n%s", content)
	}
	if strings.Contains(content, "old") {
		t.Fatal("old content should be replaced")
	}
}

func TestOpenCodePermissions_DefaultModes(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	perms := openCodePermissions(modes, ToolProfileDefault, nil)

	if !strings.Contains(perms, "read: allow") {
		t.Fatalf("missing read: allow:\n%s", perms)
	}
	if !strings.Contains(perms, "edit: allow") {
		t.Fatalf("missing edit: allow:\n%s", perms)
	}
	if !strings.Contains(perms, `"aracne_*": deny`) {
		t.Fatalf("missing deny all:\n%s", perms)
	}
	if strings.Contains(perms, "bash: allow") {
		t.Fatal("should not have bash: allow when no terminal modes")
	}
}

func TestOpenCodePermissions_WithTerminal(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeTerminal,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	perms := openCodePermissions(modes, ToolProfileDefault, nil)

	if !strings.Contains(perms, "bash: allow") {
		t.Fatalf("missing bash: allow when read=terminal:\n%s", perms)
	}
}

func TestOpenCodePermissions_ReadMCP(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	perms := openCodePermissions(modes, ToolProfileDefault, nil)

	if !strings.Contains(perms, "read: deny") {
		t.Fatalf("expected read: deny when read=MCP:\n%s", perms)
	}
	if !strings.Contains(perms, `"aracne_read": allow`) {
		t.Fatalf("expected aracne_read: allow:\n%s", perms)
	}
}

func TestClaudeToolsForProfile_WithMCPEdit(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeMCP,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	tools := claudeToolsForProfile(modes, ToolProfileDefault, nil)
	hasEdit := false
	hasWrite := false
	for _, tool := range tools {
		if strings.Contains(tool, "mcp__aracne__edit") {
			hasEdit = true
		}
		if strings.Contains(tool, "mcp__aracne__write") {
			hasWrite = true
		}
	}
	if !hasEdit {
		t.Fatalf("expected mcp__aracne__edit when edit=MCP: %v", tools)
	}
	if !hasWrite {
		t.Fatalf("expected mcp__aracne__write when edit=MCP: %v", tools)
	}
}

func TestClaudeToolsForProfile_NotBashForMCPRead(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	tools := claudeToolsForProfile(modes, ToolProfileDefault, nil)
	for _, tool := range tools {
		if tool == "Bash" {
			t.Fatalf("Bash should not be in tools when no terminal modes: %v", tools)
		}
	}
}

func TestAllowedMCPToolNames_WithReadSplit(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeMCP,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	readSplit := map[domain.ResourceKind]bool{
		domain.ResourceFunction: true,
		domain.ResourceType:     true,
	}

	names := allowedMCPToolNames(modes, ToolProfileDefault, readSplit)
	hasReadFunc := false
	hasReadStruct := false
	hasRead := false
	for _, n := range names {
		if n == "read_function" {
			hasReadFunc = true
		}
		if n == "read_struct" {
			hasReadStruct = true
		}
		if n == "read" {
			hasRead = true
		}
	}
	if !hasReadFunc {
		t.Fatalf("expected read_function in split: %v", names)
	}
	if !hasReadStruct {
		t.Fatalf("expected read_struct in split: %v", names)
	}
	if hasRead {
		t.Fatalf("should NOT have read when using splits: %v", names)
	}
}

func TestClaudeToolsForProfile_WithGrepNative(t *testing.T) {
	modes := helper.ToolModes{
		Read:  helper.ReadModeNative,
		Edit:  helper.EditModeNative,
		Other: helper.OtherModeMCP,
		Grep:  helper.GrepModeNative,
	}
	tools := claudeToolsForProfile(modes, ToolProfileDefault, nil)
	hasGrep := false
	for _, tool := range tools {
		if tool == "Grep" {
			hasGrep = true
		}
	}
	if !hasGrep {
		t.Fatalf("expected Grep (native) when grep=native: %v", tools)
	}
}

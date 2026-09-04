package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

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

// --- new config-driven generation -----------------------------------------

// mcpModeConfig is the shipped default put into ModeMCP. Every test below is about the names
// that reach a harness allow-list, and only ModeMCP has MCP tools to name -- in the other three
// EffectiveAgent correctly reports none, and each assertion would go vacuous rather than fail.
func mcpModeConfig() *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	return cfg
}

// grep, edit and write are answered by shell interception in every mode, so no allow-list may
// name an MCP tool for them: the harness would route a call to a tool the server never
// registered, and nothing would fail loudly.
func TestNoAllowListNamesAShellServedTool(t *testing.T) {
	cfg := mcpModeConfig()
	for _, agent := range []string{"main", "descriptions-generation-executor", "bug-solver"} {
		claude := strings.Join(claudeToolsForAgent(cfg.EffectiveAgent("claude_code", agent)), ",")
		opencode := openCodePermissionsForAgent(cfg.EffectiveAgent("opencode", agent))
		for _, key := range []string{"grep", "edit", "write"} {
			if strings.Contains(claude, "mcp__aracne__"+key) {
				t.Errorf("claude %s allow-list names mcp__aracne__%s: %s", agent, key, claude)
			}
			if strings.Contains(opencode, `"aracne_`+key+`"`) {
				t.Errorf("opencode %s permissions name aracne_%s:\n%s", agent, key, opencode)
			}
		}
	}
}

func TestClaudeToolsForAgent_DefaultMainAgent(t *testing.T) {
	eff := mcpModeConfig().EffectiveAgent("claude_code", "main")
	tools := claudeToolsForAgent(eff)

	// The read entry must carry the RUNTIME name. Claude Code gives each agent its own
	// server, and under the warn-only default that agent keeps its native Read -- so the
	// aracne tool registers as `read_resource`. Emitting the catalog name `read` here named
	// a tool the server never registers, which silently denied every generated sub-agent
	// the aracne read tool.
	want := map[string]bool{
		"mcp__aracne__" + toolspec.ReadResourceToolName: true,
		"mcp__aracne__warnings_list":                    true,
		"Bash":                                          true,
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("expected %q in tools: %v", name, tools)
		}
	}
	if got["mcp__aracne__"+toolspec.ReadToolName] {
		t.Fatalf("native read is available, so the short name is not what the server registers: %v", tools)
	}
	// The shipped default is WARN-ONLY: the guard names the matching aracne tool but
	// denies nothing, so the native tools stay available. Denying them cost a wasted turn
	// per denial and pushed the agent off a more capable native grep; the `arac update-file`
	// PostToolUse hook keeps the topology in sync after a native edit either way.
	for _, native := range []string{"Read", "Edit", "Write", "Grep"} {
		if !got[native] {
			t.Fatalf("native %q should be available under the warn-only default: %v", native, tools)
		}
	}
}

// TestClaudeToolsForAgent_BlockedNativeReadUsesShortName is the other half: when the agent's
// blocked_tools denies the harness's own read, the aracne tool takes the short name and the
// allow-list must follow it there too.
func TestClaudeToolsForAgent_BlockedNativeReadUsesShortName(t *testing.T) {
	eff := mcpModeConfig().EffectiveAgent("claude_code", "main")
	eff.BlockedTools = []string{"read"}
	tools := claudeToolsForAgent(eff)

	got := map[string]bool{}
	for _, tool := range tools {
		got[tool] = true
	}
	if !got["mcp__aracne__"+toolspec.ReadToolName] {
		t.Fatalf("native read blocked: expected the short name in tools: %v", tools)
	}
	if got["mcp__aracne__"+toolspec.ReadResourceToolName] {
		t.Fatalf("native read blocked: %q is not what the server registers: %v", toolspec.ReadResourceToolName, tools)
	}
	if got["Read"] {
		t.Fatalf("blocked native read must not appear in the allow-list: %v", tools)
	}
	// Blocking read must not collaterally block the other native tools.
	for _, native := range []string{"Edit", "Write", "Grep"} {
		if !got[native] {
			t.Fatalf("native %q should still be available: %v", native, tools)
		}
	}
}

func TestOpenCodePermissionsForAgent_DefaultMainAgent(t *testing.T) {
	eff := mcpModeConfig().EffectiveAgent("opencode", "main")
	perms := openCodePermissionsForAgent(eff)

	// Warn-only default: no NATIVE tool is denied and bash needs no glob-pattern map.
	// Projects opt into denial via llm.<harness>.main_agent.blocked_tools.
	for _, allowed := range []string{"read: allow", "edit: allow", "bash: allow"} {
		if !strings.Contains(perms, allowed) {
			t.Fatalf("expected %q under the warn-only default:\n%s", allowed, perms)
		}
	}
	if strings.Contains(perms, `"head *": deny`) || strings.Contains(perms, `"grep *": deny`) {
		t.Fatalf("warn-only default must not deny shell read/grep forms:\n%s", perms)
	}
	// The aracne_* wildcard deny stays: it withholds MCP tools the profile does not grant
	// (the v2 bug_* set), which is unrelated to native-tool blocking.
	if !strings.Contains(perms, `"aracne_*": deny`) {
		t.Fatalf("expected ungranted aracne tools to stay denied:\n%s", perms)
	}
	// Under warn-only, bash is a plain scalar allow: the glob-pattern map existed only to
	// deny the direct shell read/grep forms, and nothing is denied any more.
	if !strings.Contains(perms, `"aracne_read": allow`) {
		t.Fatalf("expected aracne_read: allow:\n%s", perms)
	}
}

// TestOpenCodePermissionsAlwaysUseShortReadName guards the INVERSE of the Claude drift.
//
// OpenCode runs one shared MCP server for every agent (--tool-profile all), and the "all"
// profile always registers the read tool under its short name regardless of any agent's
// blocked_tools. So `aracne_read` is right here even for an agent whose blocked_tools would
// yield `read_resource` on Claude Code. A "cleanup" that unified the two harnesses on one
// bool would silently deny OpenCode agents their read tool.
func TestOpenCodePermissionsAlwaysUseShortReadName(t *testing.T) {
	for _, blocked := range [][]string{nil, {"read"}, {"edit", "write"}} {
		eff := mcpModeConfig().EffectiveAgent("opencode", "main")
		eff.BlockedTools = blocked
		perms := openCodePermissionsForAgent(eff)
		if !strings.Contains(perms, `"aracne_`+toolspec.ReadToolName+`": allow`) {
			t.Fatalf("blocked_tools=%v: OpenCode must grant the short read name:\n%s", blocked, perms)
		}
		if strings.Contains(perms, toolspec.ReadResourceToolName) {
			t.Fatalf("blocked_tools=%v: the --tool-profile all server never registers %q:\n%s",
				blocked, toolspec.ReadResourceToolName, perms)
		}
	}
}

func TestOpenCodeBashPermission(t *testing.T) {
	set := func(keys ...string) map[string]bool {
		m := map[string]bool{}
		for _, k := range keys {
			m[k] = true
		}
		return m
	}
	if got := openCodeBashPermission(set("bash")); got != "deny" {
		t.Fatalf("whole bash blocked => deny, got %v", got)
	}
	if got := openCodeBashPermission(set()); got != "allow" {
		t.Fatalf("nothing blocked => allow, got %v", got)
	}
	if got := openCodeBashPermission(set("edit", "write")); got != "allow" {
		t.Fatalf("edit/write only => allow (no shell read/grep gating), got %v", got)
	}
	readRules, ok := openCodeBashPermission(set("read")).(map[string]interface{})
	if !ok {
		t.Fatalf("read blocked => pattern map, got %T", openCodeBashPermission(set("read")))
	}
	if readRules["*"] != "allow" || readRules["head *"] != "deny" {
		t.Fatalf("unexpected read rules: %v", readRules)
	}
	if _, present := readRules["grep *"]; present {
		t.Fatalf("grep pattern should be absent when only read blocked: %v", readRules)
	}
	grepRules := openCodeBashPermission(set("grep")).(map[string]interface{})
	if grepRules["grep *"] != "deny" || grepRules["rg *"] != "deny" {
		t.Fatalf("unexpected grep rules: %v", grepRules)
	}
	if _, present := grepRules["head *"]; present {
		t.Fatalf("read pattern should be absent when only grep blocked: %v", grepRules)
	}
}

func TestClaudeMCPServersFrontmatter(t *testing.T) {
	fm := claudeMCPServersFrontmatter("bug-hunter")
	if !strings.Contains(fm, "mcpServers:") {
		t.Fatal("expected mcpServers section")
	}
	if !strings.Contains(fm, "--tool-profile") {
		t.Fatal("expected --tool-profile in args")
	}
	if !strings.Contains(fm, "bug-hunter") {
		t.Fatalf("expected agent name in args: %s", fm)
	}
	if !strings.Contains(fm, "claude_code") {
		t.Fatalf("expected --harness claude_code in args: %s", fm)
	}
}

func TestClaudeAgentContent_ContainsFrontmatterAndPrompt(t *testing.T) {
	eff := helper.DefaultConfig().EffectiveAgent("claude_code", "bug-hunter")
	content := claudeAgentContent("bug-hunter", "Test agent", eff, "This is the prompt")

	if !strings.Contains(content, "name: bug-hunter") {
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
	eff := helper.DefaultConfig().EffectiveAgent("opencode", "bug-hunter")
	content := openCodeAgentContent("Test agent", eff, "This is the prompt")

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
}

func TestAgentModelFrontmatter(t *testing.T) {
	if got := agentModelFrontmatter(""); got != "" {
		t.Fatalf("empty model should yield no line, got %q", got)
	}
	if got := agentModelFrontmatter("<inherits>"); got != "" {
		t.Fatalf("inherited model should yield no line, got %q", got)
	}
	if got := agentModelFrontmatter("opus"); got != "model: opus\n" {
		t.Fatalf("model line = %q", got)
	}
}

func TestClaudeAgentContent_EmitsConfiguredModel(t *testing.T) {
	eff := helper.AgentConfig{Model: "opus", MCPTools: []string{"bug_list"}}
	if content := claudeAgentContent("bug-judge", "Test", eff, "Prompt body"); !strings.Contains(content, "\nmodel: opus\n") {
		t.Fatalf("expected model frontmatter, got:\n%s", content)
	}
	// A default (inherited) agent must not emit a model line.
	def := helper.DefaultConfig().EffectiveAgent("claude_code", "bug-judge")
	if strings.Contains(claudeAgentContent("bug-judge", "Test", def, "Prompt body"), "\nmodel:") {
		t.Fatal("inherited model should not emit a model line")
	}
}

func TestOpenCodeAgentContent_EmitsConfiguredModel(t *testing.T) {
	eff := helper.AgentConfig{Model: "anthropic/claude-opus-4-8", MCPTools: []string{"bug_list"}}
	if content := openCodeAgentContent("Test", eff, "Prompt body"); !strings.Contains(content, "\nmodel: anthropic/claude-opus-4-8\n") {
		t.Fatalf("expected model frontmatter, got:\n%s", content)
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

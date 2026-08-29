package tests_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/cli"
)

func TestInitDefault_CreatesBothAgents(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	// OpenCode files
	assertExists(t, dir, ".opencode/opencode.json")
	assertExists(t, dir, ".opencode/commands")
	assertExists(t, dir, ".opencode/agents")
	assertExists(t, dir, "AGENTS.md")

	entries := readDir(t, dir, ".opencode/commands")
	if len(entries) != 6 {
		t.Fatalf("expected 6 commands in .opencode/commands, got %d", len(entries))
	}
	entries = readDir(t, dir, ".opencode/agents")
	if len(entries) != 4 {
		t.Fatalf("expected 4 agents in .opencode/agents, got %d", len(entries))
	}

	// Claude files
	assertExists(t, dir, ".mcp.json")
	assertExists(t, dir, ".claude/commands")
	assertExists(t, dir, ".claude/agents")
	assertExists(t, dir, "CLAUDE.md")

	entries = readDir(t, dir, ".claude/commands")
	if len(entries) != 6 {
		t.Fatalf("expected 6 commands in .claude/commands, got %d", len(entries))
	}
	entries = readDir(t, dir, ".claude/agents")
	if len(entries) != 4 {
		t.Fatalf("expected 4 agents in .claude/agents, got %d", len(entries))
	}
}

func TestInitOpenCodeOnly(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y", "--opencode")

	assertExists(t, dir, ".opencode/opencode.json")
	assertExists(t, dir, "AGENTS.md")
	assertNotExists(t, dir, ".mcp.json")
	assertNotExists(t, dir, "CLAUDE.md")
}

func TestInitClaudeOnly(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y", "--claude")

	assertExists(t, dir, ".mcp.json")
	assertExists(t, dir, "CLAUDE.md")
	assertNotExists(t, dir, ".opencode/opencode.json")
	assertNotExists(t, dir, "AGENTS.md")
}

func TestInitGlobal_CreatesFilesInHome(t *testing.T) {
	homeDir := t.TempDir()
	dir := t.TempDir()

	cmd := bashCmd(t, dir, "init", "-y", "--global")
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("arac init --global failed: %v\n%s", err, out)
	}

	// OpenCode global paths
	assertExists(t, homeDir, ".config/opencode/opencode.json")
	assertExists(t, homeDir, ".config/opencode/AGENTS.md")

	// Claude global paths
	assertExists(t, homeDir, ".claude.json")
	assertExists(t, homeDir, ".claude/commands")
	assertExists(t, homeDir, ".claude/agents")
	assertExists(t, homeDir, ".claude/CLAUDE.md")

	// No local files should exist
	assertNotExists(t, dir, ".opencode/opencode.json")
	assertNotExists(t, dir, ".mcp.json")
}

func TestInitDefault_NoNativeHooks(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	// The default config gives the main agent no plugins, so the native edit
	// hook/plugin (which only matters for native edits) is not written.
	assertNotExists(t, dir, ".claude/hooks/arac-update-file.sh")
	assertNotExists(t, dir, ".opencode/plugins/arac-native-edit-sync.js")
}

func TestInitWithEditPlugin_CreatesNativeHooks(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a config that enables the edit-update-db plugin on the main agent.
	cfgDir := filepath.Join(dir, ".aracne")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .aracne: %v", err)
	}
	cfgJSON := `{"scan":{"mode":"default"},"llm":{"<any>":{"main_agent":{"mcp_tools":["read","edit","write"],"blocked_tools":["read","grep","edit","write"],"plugins":["edit-update-db-plugin"]}}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mustRun(t, dir, "init", "-y")

	assertExists(t, dir, ".claude/hooks/arac-update-file.sh")
	assertExists(t, dir, ".claude/settings.json")
	assertExists(t, dir, ".opencode/plugins/arac-native-edit-sync.js")
}

func TestInitRerun_NoErrors(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")
	mustRun(t, dir, "init", "-y")
}

func TestInitOpenCodeConfig_Structure(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	raw := readFile(t, dir, ".opencode/opencode.json")
	var cfg struct {
		MCP        map[string]interface{} `json:"mcp"`
		Permission map[string]interface{} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse opencode.json: %v\ncontent: %s", err, raw)
	}

	if cfg.MCP == nil {
		t.Fatal("opencode.json missing 'mcp' section")
	}
	aracEntry, ok := cfg.MCP["aracne"].(map[string]interface{})
	if !ok {
		t.Fatalf("opencode.json mcp section missing 'aracne' entry: %+v", cfg.MCP)
	}
	if aracEntry["type"] != "local" {
		t.Fatalf("arac MCP type = %q, want local", aracEntry["type"])
	}
	if aracEntry["enabled"] != true {
		t.Fatal("arac MCP entry not enabled")
	}

	if cfg.Permission == nil {
		t.Fatal("opencode.json missing 'permission' section")
	}
	// WARN-ONLY is the shipped default: the guard names the matching aracne tool but
	// denies nothing, so native read/edit stay allowed and bash needs no glob map. Denying
	// them cost a wasted turn per denial and pushed the agent off a more capable native
	// grep; the `arac update-file` PostToolUse hook keeps the topology in sync after a
	// native edit either way. Projects opt into denial via blocked_tools.
	if cfg.Permission["read"] != "allow" {
		t.Fatalf("permission.read = %q, want allow (warn-only default)", cfg.Permission["read"])
	}
	if cfg.Permission["edit"] != "allow" {
		t.Fatalf("permission.edit = %q, want allow (warn-only default)", cfg.Permission["edit"])
	}
	if cfg.Permission["bash"] != "allow" {
		t.Fatalf("permission.bash = %v, want a scalar allow (nothing is denied)", cfg.Permission["bash"])
	}
	// The aracne_* wildcard deny stays: it withholds MCP tools the profile does not grant,
	// which is unrelated to native-tool blocking.
	if cfg.Permission["aracne_*"] != "deny" {
		t.Fatalf("permission.aracne_* = %q, want deny", cfg.Permission["aracne_*"])
	}
	if cfg.Permission["aracne_warnings_list"] != "allow" {
		t.Fatalf("permission.aracne_warnings_list = %q, want allow", cfg.Permission["aracne_warnings_list"])
	}
	// The bug pipeline is a v2 feature and is not in the default profile, so it is left
	// to the aracne_* wildcard deny rather than granted.
	if cfg.Permission["aracne_bug_report"] == "allow" {
		t.Fatalf("aracne_bug_report should not be granted by the v1 default profile")
	}
}

func TestInitClaudeConfig_Structure(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	raw := readFile(t, dir, ".mcp.json")
	var cfg struct {
		MCPServers map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse .mcp.json: %v\ncontent: %s", err, raw)
	}

	if cfg.MCPServers == nil {
		t.Fatal(".mcp.json missing 'mcpServers' section")
	}
	aracEntry, ok := cfg.MCPServers["aracne"].(map[string]interface{})
	if !ok {
		t.Fatalf(".mcp.json mcpServers missing 'aracne' entry: %+v", cfg.MCPServers)
	}
	if aracEntry["command"] != "arac" {
		t.Fatalf("arac MCP command = %q, want arac", aracEntry["command"])
	}
}

func TestInitCommandsAndAgents_Content(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	// OpenCode commands should have agent frontmatter
	for _, name := range []string{"descriptions-generate", "descriptions-apply", "descriptions_clear", "bug-hunter", "bug-judge", "bug-solver"} {
		content := readFile(t, dir, ".opencode/commands/"+name+".md")
		if !strings.Contains(content, "agent:") {
			t.Fatalf("%s command missing 'agent:' frontmatter:\n%s", name, content)
		}
	}

	// OpenCode agents should have permission frontmatter
	for _, name := range []string{"descriptions-generation-executor", "bug-hunter", "bug-judge", "bug-solver"} {
		content := readFile(t, dir, ".opencode/agents/"+name+".md")
		if !strings.Contains(content, "mode: subagent") {
			t.Fatalf("%s agent missing 'mode: subagent' frontmatter:\n%s", name, content)
		}
		if !strings.Contains(content, "permission:") {
			t.Fatalf("%s agent missing 'permission:' frontmatter:\n%s", name, content)
		}
	}

	// Claude commands should have description frontmatter
	for _, name := range []string{"descriptions-generate", "descriptions-apply", "descriptions_clear", "bug-hunter", "bug-judge", "bug-solver"} {
		content := readFile(t, dir, ".claude/commands/"+name+".md")
		if !strings.Contains(content, "description:") {
			t.Fatalf("Claude %s command missing 'description:' frontmatter:\n%s", name, content)
		}
	}

	// Claude agents should have mcpServers frontmatter when modes include MCP
	for _, name := range []string{"descriptions-generation-executor", "bug-hunter", "bug-judge", "bug-solver"} {
		content := readFile(t, dir, ".claude/agents/"+name+".md")
		if !strings.Contains(content, "mcpServers:") {
			t.Fatalf("Claude %s agent missing 'mcpServers:' frontmatter:\n%s", name, content)
		}
	}
}

func TestInitMarkdownFiles_ContainIntegrationSection(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	agentsMd := readFile(t, dir, "AGENTS.md")
	if !strings.Contains(agentsMd, cli.AracIntegrationStart) {
		t.Fatal("AGENTS.md missing integration section")
	}
	if !strings.Contains(agentsMd, "Good Luck in your task.") {
		t.Fatal("AGENTS.md missing ending")
	}

	claudeMd := readFile(t, dir, "CLAUDE.md")
	if !strings.Contains(claudeMd, cli.AracIntegrationStart) {
		t.Fatal("CLAUDE.md missing integration section")
	}
	if !strings.Contains(claudeMd, "Good Luck in your task.") {
		t.Fatal("CLAUDE.md missing ending")
	}
}

// --- helpers ---

func assertExists(t *testing.T, base, rel string) {
	t.Helper()
	path := filepath.Join(base, rel)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected file/dir to exist: %s", rel)
	}
}

func assertNotExists(t *testing.T, base, rel string) {
	t.Helper()
	path := filepath.Join(base, rel)
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected file/dir to NOT exist: %s", rel)
	}
}

func readFile(t *testing.T, base, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func readDir(t *testing.T, base, rel string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(base, rel))
	if err != nil {
		t.Fatalf("read dir %s: %v", rel, err)
	}
	return entries
}

func bashCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(AracBin, args...)
	cmd.Dir = dir
	return cmd
}

package tests_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/cli"
	"aracne/internal/helper"
)

// enableBugManagement writes a project config with features.bug_management on, so a test can
// exercise the enabled half of the gate. It must be called BEFORE `arac init`.
func enableBugManagement(t *testing.T, dir string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".aracne")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .aracne: %v", err)
	}
	cfg := helper.DefaultConfig()
	cfg.Features.BugManagement = true
	if err := helper.SaveConfig(cfg, filepath.Join(cfgDir, "config.json")); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

// The bug pipeline ships behind features.bug_management, so the artifact counts differ by
// flag state: 3 commands + 1 agent with it off, 6 + 4 with it on.
func TestInitDefault_CreatesBothAgents(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	// OpenCode files
	assertExists(t, dir, ".opencode/opencode.json")
	assertExists(t, dir, ".opencode/commands")
	assertExists(t, dir, ".opencode/agents")
	assertExists(t, dir, "AGENTS.md")

	assertDirCount(t, dir, ".opencode/commands", 3)
	assertDirCount(t, dir, ".opencode/agents", 1)

	// Claude files. No .mcp.json: the default surface is terminal, where aracne reaches the
	// agent by answering the shell commands it already runs. Writing a server whose tools the
	// contract never mentions would cost their schemas on every request for nothing.
	assertNotExists(t, dir, ".mcp.json")
	assertExists(t, dir, ".claude/commands")
	assertExists(t, dir, ".claude/agents")
	assertExists(t, dir, "CLAUDE.md")

	assertDirCount(t, dir, ".claude/commands", 3)
	assertDirCount(t, dir, ".claude/agents", 1)

	for _, name := range []string{"bug-hunter.md", "bug-judge.md", "bug-solver.md"} {
		assertMissing(t, dir, filepath.Join(".claude/commands", name))
		assertMissing(t, dir, filepath.Join(".claude/agents", name))
		assertMissing(t, dir, filepath.Join(".opencode/commands", name))
		assertMissing(t, dir, filepath.Join(".opencode/agents", name))
	}
}

func TestInitWithBugManagement_WritesBugAgents(t *testing.T) {
	dir := t.TempDir()
	enableBugManagement(t, dir)
	mustRun(t, dir, "init", "-y")

	assertDirCount(t, dir, ".opencode/commands", 6)
	assertDirCount(t, dir, ".opencode/agents", 4)
	assertDirCount(t, dir, ".claude/commands", 6)
	assertDirCount(t, dir, ".claude/agents", 4)

	// bug-judge and bug-solver must be PRIMARY OpenCode commands: they fan out one sub-agent
	// per bug, and a subtask cannot spawn further subtasks. bug-hunter does its own scanning
	// and stays a subtask.
	for _, name := range []string{"bug-judge", "bug-solver"} {
		body := readFile(t, dir, filepath.Join(".opencode/commands", name+".md"))
		if !strings.Contains(body, "agent: build") {
			t.Errorf("%s must run as the primary build agent to fan out:\n%s", name, body)
		}
		if strings.Contains(body, "subtask: true") {
			t.Errorf("%s is a subtask and cannot spawn sub-agents:\n%s", name, body)
		}
	}
	hunter := readFile(t, dir, filepath.Join(".opencode/commands", "bug-hunter.md"))
	if !strings.Contains(hunter, "subtask: true") {
		t.Errorf("bug-hunter should stay a subtask command:\n%s", hunter)
	}
}

// TestInitBugManagementOffPrunesStaleArtifacts pins that turning the flag back off removes
// files a previous enabled init wrote. A left-behind agent file names bug_* tools the server
// no longer registers -- the same silent-denial drift the generator test guards against.
func TestInitBugManagementOffPrunesStaleArtifacts(t *testing.T) {
	dir := t.TempDir()
	enableBugManagement(t, dir)
	mustRun(t, dir, "init", "-y")
	assertExists(t, dir, ".claude/agents/bug-hunter.md")

	cfg := helper.DefaultConfig()
	cfg.Features.BugManagement = false
	if err := helper.SaveConfig(cfg, filepath.Join(dir, ".aracne", "config.json")); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	mustRun(t, dir, "init", "-y")

	for _, name := range []string{"bug-hunter.md", "bug-judge.md", "bug-solver.md"} {
		assertMissing(t, dir, filepath.Join(".claude/agents", name))
		assertMissing(t, dir, filepath.Join(".claude/commands", name))
		assertMissing(t, dir, filepath.Join(".opencode/agents", name))
		assertMissing(t, dir, filepath.Join(".opencode/commands", name))
	}
}

func assertDirCount(t *testing.T, dir, rel string, want int) {
	t.Helper()
	entries := readDir(t, dir, rel)
	if len(entries) != want {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected %d entries in %s, got %d: %v", want, rel, len(entries), names)
	}
}

func assertMissing(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
		t.Fatalf("expected %s to be absent", rel)
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

	assertNotExists(t, dir, ".mcp.json") // terminal surface; see TestInitWithMCP_WiresTheServer
	assertExists(t, dir, "CLAUDE.md")
	assertNotExists(t, dir, ".opencode/opencode.json")
	assertNotExists(t, dir, "AGENTS.md")
}

// --mcp is the opt-in, and it has to PERSIST: the guard and `arac serve` both read the mode
// from config at run time, so a flag that lived only for one init would wire a server the next
// plain `arac init` silently removes again.
func TestInitWithMCP_WiresTheServerAndPersistsTheMode(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y", "--claude", "--mcp")

	assertExists(t, dir, ".mcp.json")
	raw := readFile(t, dir, ".mcp.json")
	var mcpCfg struct {
		MCPServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &mcpCfg); err != nil {
		t.Fatalf("parse .mcp.json: %v\n%s", err, raw)
	}
	if entry, ok := mcpCfg.MCPServers["aracne"]; !ok || entry["command"] != "arac" {
		t.Fatalf(".mcp.json missing the aracne server: %s", raw)
	}

	cfg := helper.LoadConfig(filepath.Join(dir, ".aracne", "config.json"))
	if !cfg.MCPEnabled() {
		t.Fatalf("--mcp did not persist the mode: %q", cfg.EffectiveMode())
	}
	if cfg.Mode != helper.ModeMCP {
		t.Errorf("--mcp should stamp the new key, got %q", cfg.Mode)
	}

	// The contract says the capabilities arrive as tools and stops there: each tool's own
	// description says how to call it, and naming one in prose is how the contract and the
	// registered tool list drift apart.
	body := readFile(t, dir, "CLAUDE.md")
	if !strings.Contains(body, "arrive as MCP tools") {
		t.Errorf("CLAUDE.md is not the mcp contract:\n%s", body)
	}
	if strings.Contains(body, "mcp__aracne__") {
		t.Errorf("CLAUDE.md names an MCP tool by identifier:\n%s", body)
	}
}

// Switching back removes the server rather than leaving it running unmentioned.
func TestInitBackToTerminalRemovesTheServer(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y", "--claude", "--mcp")
	assertExists(t, dir, ".mcp.json")

	cfgPath := filepath.Join(dir, ".aracne", "config.json")
	cfg := helper.LoadConfig(cfgPath)
	cfg.Mode = helper.ModeLineRange
	if err := helper.SaveConfig(cfg, cfgPath); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "init", "-y", "--claude")

	raw := readFile(t, dir, ".mcp.json")
	if strings.Contains(raw, "aracne") {
		t.Fatalf("the aracne server survived the switch away from mcp: %s", raw)
	}
	body := readFile(t, dir, "CLAUDE.md")
	if strings.Contains(body, "arrive as MCP tools") {
		t.Errorf("CLAUDE.md is still the mcp contract:\n%s", body)
	}
	if !strings.Contains(body, "## Line ranges") {
		t.Errorf("CLAUDE.md was not rewritten for the new mode:\n%s", body)
	}
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

	// Claude global paths. ~/.claude.json is the global MCP config and is only written on a
	// surface that serves MCP; --global alone is still the terminal default.
	assertNotExists(t, homeDir, ".claude.json")
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

// The pre-tool scan is not a plugin: it ships on both surfaces by default, because
// scan.pre_tool -- not the presence of a file init happened to write -- is what decides
// whether the graph is re-synced before a tool call.
func TestInitDefault_PreToolScanOnBothSurfaces(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y")

	assertExists(t, dir, ".opencode/plugins/arac-pre-tool-scan.js")
	plugin, err := os.ReadFile(filepath.Join(dir, ".opencode/plugins/arac-pre-tool-scan.js"))
	if err != nil {
		t.Fatalf("read pre-tool scan plugin: %v", err)
	}
	for _, want := range []string{"tool.execute.before", "guard", "--pre-scan"} {
		if !strings.Contains(string(plugin), want) {
			t.Fatalf("pre-tool scan plugin missing %q:\n%s", want, plugin)
		}
	}

	// Claude Code gets the same thing through the guard hook, which is already registered
	// on PreToolUse.
	assertExists(t, dir, ".claude/hooks/arac-guard.sh")
	settings, err := os.ReadFile(filepath.Join(dir, ".claude/settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(settings), "PreToolUse") {
		t.Fatalf("settings.json has no PreToolUse hook:\n%s", settings)
	}
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

	// The permission block still belongs here on every surface; the MCP server does not.
	if _, present := cfg.MCP["aracne"]; present {
		t.Fatalf("terminal surface wrote an MCP server entry: %+v", cfg.MCP)
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
	// On the terminal surface NO aracne_* tool is granted: none is served, and an allow-list
	// naming tools that do not exist is the same drift in a different file.
	for key, value := range cfg.Permission {
		if strings.HasPrefix(key, "aracne_") && key != "aracne_*" && value == "allow" {
			t.Errorf("terminal surface granted %s, but no MCP tool is served", key)
		}
	}
}

// The same file on the MCP surface: the wildcard deny stays and the granted tools are named.
func TestInitOpenCodeConfig_MCPSurfaceGrantsItsTools(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-y", "--mcp")

	raw := readFile(t, dir, ".opencode/opencode.json")
	var cfg struct {
		MCP        map[string]map[string]interface{} `json:"mcp"`
		Permission map[string]interface{}            `json:"permission"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse opencode.json: %v\ncontent: %s", err, raw)
	}
	entry, ok := cfg.MCP["aracne"]
	if !ok {
		t.Fatalf("opencode.json mcp section missing the 'aracne' entry: %+v", cfg.MCP)
	}
	if entry["type"] != "local" || entry["enabled"] != true {
		t.Fatalf("aracne MCP entry = %+v, want a local enabled server", entry)
	}
	// The aracne_* wildcard deny withholds tools the profile does not grant; the granted ones
	// are listed individually.
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
	mustRun(t, dir, "init", "-y", "--mcp")

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
	// Enabled, so the bug artifacts exist to assert on; the disabled shape is covered by
	// TestInitDefault_CreatesBothAgents.
	enableBugManagement(t, dir)
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

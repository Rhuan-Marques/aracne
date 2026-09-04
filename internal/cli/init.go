package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// Prompts the user for confirmation before overwriting an existing file, returning true if they approve.
func promptReplace(path string) bool {
	fmt.Printf("File %s already exists. Replace? [y/N] ", path)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

// Prompts user to confirm overwriting an existing config file.
func promptReplaceConfigExists(path string) bool {
	fmt.Printf("Config %s already exists. Overwrite? [y/N] ", path)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

// Initializes Claude Code and/or OpenCode integrations with the topology database, with optional global installation and auto-confirm flags.
func RunInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Initialize Claude Code integration")
	opencode := fs.Bool("opencode", false, "Initialize OpenCode integration")
	global := fs.Bool("global", false, "Install globally")
	yes := fs.Bool("y", false, "Auto-confirm all replacement prompts")
	withMCP := fs.Bool("mcp", false, "Wire the MCP server (sets mode to \"mcp\")")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	configPath := helper.ConfigPath(".aracne/topology.db")
	cfg := helper.EnsureConfig(configPath)
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid .aracne/config.json: %v\n", err)
		os.Exit(1)
	}
	if *withMCP && !cfg.MCPEnabled() {
		// Persisted, not just applied for this run. The guard and `arac serve` both read the
		// mode from config at runtime, so a flag that only lived for one init would wire an
		// MCP server the next plain `arac init` silently removes again.
		//
		// It sets ModeMCP outright rather than adding MCP to what is already there. There is
		// no additive option any more, and that is the point: the mode a project is in has to
		// be one of the four, and "the tools AND the interception" was the combination that
		// made the model choose between two answers to the same question.
		cfg.Mode = helper.ModeMCP
		if err := helper.SaveConfig(cfg, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not persist mode: %v\n", err)
		}
	}
	announceMode(cfg)

	if *opencode {
		initOpenCode(*global, cfg, *yes)
	}
	if *claude {
		initClaudeCode(*global, cfg, *yes)
	}
	printBugManagementHint(cfg, configPath)
}

// Initializes OpenCode integration by configuring MCP servers, permissions, commands, agents, and plugins.
func initOpenCode(global bool, cfg *helper.Config, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)
	os.MkdirAll(configDir, 0755)
	mainEff := cfg.EffectiveAgent("opencode", "main")

	config := readJSONConfig(configPath)
	if cfg.MCPEnabled() {
		if shouldWriteConfig(config, "mcp", configPath, "OpenCode", autoYes) {
			mcpMap, _ := config["mcp"].(map[string]interface{})
			if mcpMap == nil {
				mcpMap = make(map[string]interface{})
			}
			mcpMap["aracne"] = map[string]interface{}{
				"type":    "local",
				"command": []string{"arac", "serve", "--tool-profile", "all", "--harness", "opencode"},
				"enabled": true,
			}
			config["mcp"] = mcpMap
		}
	} else if mcpMap, ok := config["mcp"].(map[string]interface{}); ok {
		delete(mcpMap, "aracne")
		if len(mcpMap) == 0 {
			delete(config, "mcp")
		} else {
			config["mcp"] = mcpMap
		}
	}

	permissionMap, _ := config["permission"].(map[string]interface{})
	if permissionMap == nil {
		permissionMap = make(map[string]interface{})
	}
	// blocked_tools bites in ModeMCP and ModeCLI (see Config.GuardBlocksNativeReads), and OpenCode's
	// permission block is the same decision spelled for a different harness. Denying a native
	// read here in a mode whose guard would never deny it is how the two enforcement paths
	// drift apart -- and the one the operator notices is this one, because it refuses silently.
	blocked := map[string]bool{}
	if cfg.GuardBlocksNativeReads() {
		blocked = toolNameSet(mainEff.BlockedTools)
	}
	permissionMap["read"] = nativePermission(!blocked["read"])
	permissionMap["edit"] = nativePermission(!blocked["edit"] && !blocked["write"])
	permissionMap["bash"] = openCodeBashPermission(blocked)
	delete(permissionMap, "write")
	permissionMap["aracne_*"] = "deny"
	if cfg.MCPEnabled() {
		for _, toolName := range toolspec.ResolveToolNames(mainEff.MCPTools, openCodeNativeRead) {
			permissionMap["aracne_"+toolName] = "allow"
		}
	}
	config["permission"] = permissionMap
	writeJSONConfig(configPath, config)
	fmt.Printf("[OpenCode] Config written to %s\n", configPath)

	commandsDir := filepath.Join(configDir, "commands")
	agentsDir := filepath.Join(configDir, "agents")
	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	batchSize := cfg.AgentParam("opencode", "descriptions-generation-executor", "max-batch-size", helper.DefaultDescriptionBatchSize)
	writeOpenCodePrimaryCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", "build", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", batchSize), autoYes)
	writeOpenCodeCommand(commandsDir, "descriptions-apply", "Write topology descriptions back into source files as doc comments", "build", prompts.DescriptionsApplyCommand(), autoYes)
	writeOpenCodeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", "build", prompts.DescriptionsClearCommand(), autoYes)
	writeAgent(agentsDir, "descriptions-generation-executor", openCodeAgentContent("Generates descriptions for one assigned batch of undocumented topology resources", cfg.EffectiveAgent("opencode", "descriptions-generation-executor"), prompts.DescriptionsGenerationExecutorPrompt()), autoYes)

	if cfg.BugManagementEnabled() {
		// bug-hunter stays a subtask command: it does its own scanning and needs no fan-out.
		// bug-judge and bug-solver are PRIMARY commands -- they must spawn one sub-agent per
		// bug, and an OpenCode subtask cannot spawn further subtasks (the same reason
		// descriptions-generate is primary).
		writeOpenCodeCommand(commandsDir, "bug-hunter", "Scan the whole codebase for bugs", "bug-hunter", bugHunterOpenCodeCommand(), autoYes)
		writeOpenCodePrimaryCommand(commandsDir, "bug-judge", "Triage every pending bug by fanning out Bug Judge sub-agents in parallel", "build", bugJudgeCommandForAgent("bug-judge"), autoYes)
		writeOpenCodePrimaryCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", "build", bugSolverCommandForAgent("bug-solver"), autoYes)

		writeAgent(agentsDir, "bug-hunter", openCodeAgentContent("Scans the entire project topology looking for bugs", cfg.EffectiveAgent("opencode", "bug-hunter"), prompts.BugHunterPrompt()), autoYes)
		writeAgent(agentsDir, "bug-judge", openCodeAgentContent("Triages pending bugs by comparing against dismissed bug patterns", cfg.EffectiveAgent("opencode", "bug-judge"), prompts.BugJudgePrompt()), autoYes)
		writeAgent(agentsDir, "bug-solver", openCodeAgentContent("Fixes acknowledged bugs in the codebase and removes them", cfg.EffectiveAgent("opencode", "bug-solver"), prompts.BugSolverPrompt()), autoYes)
	} else {
		pruneBugArtifacts(commandsDir, agentsDir, "OpenCode")
	}

	writeOpenCodePlugins(mainEff.Plugins, configDir, autoYes)
	// The pre-tool scan plugin is installed unconditionally (independent of plugins), the
	// same way Claude Code's guard hook is: it is what keeps the graph current for the call
	// that is about to read it, on whichever surface the project is on.
	writeOpenCodePreToolScanPlugin(filepath.Join(configDir, "plugins"), autoYes)
	writeMarkdownIntegrationFile(agentsMdPath, "OpenCode AGENTS.md", prompts.AgentsMdForConfig(cfg))
	fmt.Println("[OpenCode] Restart OpenCode to activate the topology workflow.")
}

// Initializes Claude Code integration by configuring MCP servers, commands, agents, plugins, and guard hooks.
func initClaudeCode(global bool, cfg *helper.Config, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)
	claudeBaseDir := filepath.Dir(commandsDir)
	mainEff := cfg.EffectiveAgent("claude_code", "main")

	if cfg.MCPEnabled() {
		claudeConfig := readJSONConfig(mcpConfigPath)
		if shouldWriteConfig(claudeConfig, "mcpServers", mcpConfigPath, "Claude Code", autoYes) {
			mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
			if mcpServers == nil {
				mcpServers = make(map[string]interface{})
			}
			mcpServers["aracne"] = map[string]interface{}{
				"command": "arac",
				"args":    []string{"serve", "--tool-profile", "main", "--harness", "claude_code"},
			}
			claudeConfig["mcpServers"] = mcpServers
			writeJSONConfig(mcpConfigPath, claudeConfig)
			fmt.Printf("[Claude Code] MCP server configured in %s\n", mcpConfigPath)
		}
	} else if dropAracneMCPServer(mcpConfigPath) {
		// Leaving a stale entry behind would start a server whose tools the contract no
		// longer mentions -- the model pays for their schemas on every request and is told
		// nothing about them. Init has to be able to move a project BETWEEN surfaces, not
		// only onto one.
		fmt.Printf("[Claude Code] Removed the aracne MCP server from %s (integration.mode: %s)\n",
			mcpConfigPath, cfg.EffectiveMode())
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	batchSize := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", helper.DefaultDescriptionBatchSize)
	writeCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", batchSize), autoYes)
	writeCommand(commandsDir, "descriptions-apply", "Write topology descriptions back into source files as doc comments", prompts.DescriptionsApplyCommand(), autoYes)
	writeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", prompts.DescriptionsClearCommand(), autoYes)
	writeAgent(agentsDir, "descriptions-generation-executor", claudeAgentContent("descriptions-generation-executor", "Generates descriptions for one assigned batch of undocumented topology resources", cfg.EffectiveAgent("claude_code", "descriptions-generation-executor"), prompts.DescriptionsGenerationExecutorPrompt()), autoYes)

	if cfg.BugManagementEnabled() {
		writeCommand(commandsDir, "bug-hunter", "Fan out Bug Hunter sub-agents to scan the codebase in parallel", bugHunterCommandForAgent(".claude/agents/bug-hunter.md"), autoYes)
		writeCommand(commandsDir, "bug-judge", "Triage every pending bug by fanning out Bug Judge sub-agents in parallel", bugJudgeCommandForAgent(".claude/agents/bug-judge.md"), autoYes)
		writeCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", bugSolverCommandForAgent(".claude/agents/bug-solver.md"), autoYes)

		writeAgent(agentsDir, "bug-hunter", claudeAgentContent("bug-hunter", "Scans the entire project topology looking for bugs", cfg.EffectiveAgent("claude_code", "bug-hunter"), prompts.BugHunterPrompt()), autoYes)
		writeAgent(agentsDir, "bug-judge", claudeAgentContent("bug-judge", "Triages pending bugs by comparing against dismissed bug patterns", cfg.EffectiveAgent("claude_code", "bug-judge"), prompts.BugJudgePrompt()), autoYes)
		writeAgent(agentsDir, "bug-solver", claudeAgentContent("bug-solver", "Fixes acknowledged bugs in the codebase and removes them", cfg.EffectiveAgent("claude_code", "bug-solver"), prompts.BugSolverPrompt()), autoYes)
	} else {
		pruneBugArtifacts(commandsDir, agentsDir, "Claude Code")
	}

	writeClaudePlugins(mainEff.Plugins, claudeBaseDir, autoYes)
	// The guard hook is installed unconditionally (independent of plugins): it
	// must always warn on native/shell tool usage and block per blocked_tools.
	writeClaudeGuardHook(filepath.Join(claudeBaseDir, "settings.json"), filepath.Join(claudeBaseDir, "hooks"), autoYes)
	// Pre-approve the aracne MCP tools so Claude Code does not prompt on every
	// lookup/edit call in modes that would otherwise ask. Nothing to pre-approve on the
	// terminal surface: the calls the agent makes there are ordinary Bash.
	if cfg.MCPEnabled() {
		writeClaudePermissions(filepath.Join(claudeBaseDir, "settings.json"), cfg)
	}
	writeMarkdownIntegrationFile(claudeMdPath, "Claude Code CLAUDE.md", prompts.ClaudeMdForConfig(cfg))
	fmt.Println("[Claude Code] Restart Claude Code to activate the topology workflow.")
}

// announceMode says which of the four modes this project is in, because the answer decides
// everything else `arac init` just wrote.
//
// It must never be silent. The default is ModeCLI, so a project that predates the mode
// key and set neither legacy key loses nothing but gains no interception either -- and an
// operator who wanted one of the intercepting modes would otherwise discover that as "aracne
// stopped answering my reads".
func announceMode(cfg *helper.Config) {
	switch cfg.EffectiveMode() {
	case helper.ModeMCP:
		fmt.Println("Mode: mcp — aracne serves a single `read` MCP tool; shell reads run as themselves.")
		fmt.Println("  `grep` and edits are still answered by aracne. blocked_tools applies in this mode only.")
	case helper.ModeCLI:
		fmt.Println("Mode: cli — no MCP tools; the contract points at `arac read <id>` for symbols.")
		fmt.Println("  Shell reads run as themselves; `grep` and edits are answered by aracne.")
		fmt.Println("  For intercepted reads, set \"mode\" to \"intercept_line_ranges\" or \"intercept_id\" in .aracne/config.json.")
	case helper.ModeInterceptID:
		fmt.Println("Mode: intercept_id — `cat`/`head`/`tail`/`sed -n` are answered from the topology")
		fmt.Println("  and take a resource ID where they take a path.")
	case helper.ModeInterceptLineRanges:
		fmt.Println("Mode: intercept_line_ranges — `cat`/`head`/`tail`/`sed -n` are answered from the topology,")
		fmt.Println("  and every declaration is named by the exact lines it spans.")
	}
}

// dropAracneMCPServer removes an aracne entry from a Claude Code MCP config, reporting whether
// anything changed. Mirrors the removal `arac disable` performs, so switching surfaces and
// disabling entirely leave the file in the same shape.
func dropAracneMCPServer(mcpConfigPath string) bool {
	config := readJSONConfig(mcpConfigPath)
	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		return false
	}
	removed := false
	for _, key := range []string{"aracne", "arac"} {
		if _, exists := servers[key]; exists {
			delete(servers, key)
			removed = true
		}
	}
	if !removed {
		return false
	}
	if len(servers) == 0 {
		delete(config, "mcpServers")
	} else {
		config["mcpServers"] = servers
	}
	writeJSONConfig(mcpConfigPath, config)
	return true
}

// bugArtifactFiles are the command and agent markdown files the bug pipeline owns. The two
// sets happen to share their names; both directories get the same list.
var bugArtifactFiles = []string{"bug-hunter.md", "bug-judge.md", "bug-solver.md"}

// pruneBugArtifacts removes the generated bug commands and agents when
// features.bug_management is off.
//
// Without this, `arac init` would not be idempotent with respect to the flag: a project that
// once had the feature on would keep agent files whose `tools:` frontmatter names bug_* tools
// the server no longer registers -- reintroducing exactly the silent-denial drift the
// generator/server test exists to catch. Removing them makes the flag reversible without a
// separate `arac disable`.
func pruneBugArtifacts(commandsDir, agentsDir, label string) {
	before := countExisting(commandsDir, bugArtifactFiles) + countExisting(agentsDir, bugArtifactFiles)
	removeFiles(commandsDir, bugArtifactFiles)
	removeFiles(agentsDir, bugArtifactFiles)
	if before > 0 {
		fmt.Printf("[%s] Removed %d stale bug-pipeline file(s); features.bug_management is off\n", label, before)
	}
}

// countExisting reports how many of names exist in dir.
func countExisting(dir string, names []string) int {
	n := 0
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			n++
		}
	}
	return n
}

// printBugManagementHint tells the user the pipeline exists and how to turn it on. A config
// flag nobody can discover is a feature nobody enables, and nothing rewrites an existing
// .aracne/config.json to reveal the key.
func printBugManagementHint(cfg *helper.Config, configPath string) {
	if cfg.BugManagementEnabled() {
		return
	}
	fmt.Printf("Bug pipeline (bug-hunter/judge/solver) not installed. To enable it, set "+
		"\"features\": {\"bug_management\": true} in %s and re-run arac init.\n", configPath)
}

// Converts a boolean permission flag to a string ("allow" or "deny").
func nativePermission(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

// Converts a string slice of tool names into a set (map) for fast membership testing.
func toolNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// Returns paths to opencode.json config, its directory, and agents doc (global or local).
func opencodePaths(global bool) (string, string, string) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		configDir := filepath.Join(home, ".config", "opencode")
		return filepath.Join(configDir, "opencode.json"), configDir, filepath.Join(configDir, "AGENTS.md")
	}
	return ".opencode/opencode.json", ".opencode", "AGENTS.md"
}

// Returns paths to Claude configuration files (.claude.json, commands, agents, CLAUDE.md) in either global home directory or local project directory
func claudePaths(global bool) (string, string, string, string) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		return filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "commands"), filepath.Join(home, ".claude", "agents"), filepath.Join(home, ".claude", "CLAUDE.md")
	}
	return ".mcp.json", ".claude/commands", ".claude/agents", "CLAUDE.md"
}

// Reads and parses a JSON configuration file, exiting on parse errors or returning an empty map if the file is missing.
func readJSONConfig(path string) map[string]interface{} {
	config := make(map[string]interface{})
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", path, err)
			os.Exit(1)
		}
	}
	return config
}

// Determines whether to write a config value, prompting on overwrite unless autoYes is set; skips if key already exists and not approved.
func shouldWriteConfig(config map[string]interface{}, key, path, label string, autoYes bool) bool {
	if _, exists := config[key]; exists {
		if autoYes || promptReplaceConfigExists(path) {
			fmt.Printf("[%s] Overwriting %s\n", label, path)
			return true
		}
		fmt.Printf("[%s] Skipping %s\n", label, path)
		return false
	}
	return true
}

// Marshals a config map to indented JSON and writes it to a file with directory creation.
func writeJSONConfig(path string, config map[string]interface{}) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
		os.Exit(1)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
		os.Exit(1)
	}
}

// bugHunterOpenCodeCommand is the OpenCode variant: the command runs as the
// single bug-hunter sub-agent (it cannot spawn parallel sub-agents and has no
// bug_list), so it scans the whole codebase itself rather than orchestrating a
// fan-out.
func bugHunterOpenCodeCommand() string {
	return strings.Join([]string{
		"Scan the whole codebase for confirmed correctness, reliability, and security bugs — you are the only hunter this run, so cover every package, not just one slice.",
		"",
		"1. Work through the source package by package, inspecting functions, methods, types, and interfaces with the read tools.",
		"2. Report every confirmed bug with bug_report on the root-cause node — the resource that must be fixed, not a downstream symptom (precise node_id + concrete scenario; no style issues or speculation).",
		"3. After a full pass, make another focused pass over anything you were unsure about; stop when a pass finds nothing new.",
		"4. Report how many bugs you reported.",
	}, "\n")
}

// Returns instructions for hunting bugs across the codebase by partitioning and fanning out agent runs until no new bugs are found.
func bugHunterCommandForAgent(agentRef string) string {
	return strings.Join([]string{
		"Hunt the whole codebase for bugs by fanning out the " + agentRef + " agent in parallel, then repeat until a round finds nothing new.",
		"",
		"1. Partition the source tree into areas (top-level packages or directories; use ls/grep to enumerate them).",
		"2. Launch the " + agentRef + " agent once per area, running as many concurrently as the platform allows. Each run reports every confirmed bug with bug_report on the root-cause node — the resource that must be fixed, not a downstream symptom (precise node_id + concrete scenario; no style issues or speculation).",
		"3. Run `arac bug list --json` in the shell to see what has already been reported, then run another parallel round telling each hunter not to re-report existing bugs — only new, distinct ones.",
		"4. Repeat step 3 until a round adds no new bugs, or after a small number of rounds.",
		"5. Report how many distinct bugs were reported in total.",
	}, "\n")
}

// Returns instructions for triaging pending bugs by fanning out agent runs to classify each as duplicate, false positive, or genuine.
func bugJudgeCommandForAgent(agentRef string) string {
	return strings.Join([]string{
		"Triage every pending bug by fanning out the " + agentRef + " agent — one run per pending bug, run in parallel.",
		"",
		"1. Run `arac bug list --state pending --json` in the shell to get the bugs to triage, and `arac bug list --state dismissed --json` to get the known false-positive patterns. If the shell is unavailable, ask the user to run both and paste the output.",
		"2. Group the pending bugs by node_id.",
		"3. Launch the " + agentRef + " agent once per pending bug, running as many concurrently as the platform allows (in a single batch). Give each run only its assigned bug plus, for context: the other live bugs on the same node (duplicate candidates) and the dismissed bug descriptions (false-positive patterns, same node first).",
		"4. Each run applies, in order: (1) matches a dismissed pattern -> bug_delete; (2) duplicates another live bug -> bug_delete that duplicate ONLY if the assigned bug's ID sorts before it, otherwise leave both (the judge holding the lower ID resolves the pair); (3) false positive, intended, or fully guarded on inspection -> bug_dismiss; (4) genuine -> bug_acknowledge. If genuinely unsure, leave the bug untouched.",
		"5. After all runs finish, report how many bugs were acknowledged, dismissed, deleted, and left undecided.",
	}, "\n")
}

// Returns instructions for fixing acknowledged bugs by fanning out agent runs to find root causes, make changes, and verify fixes.
func bugSolverCommandForAgent(agentRef string) string {
	return strings.Join([]string{
		"Fix every acknowledged bug by fanning out the " + agentRef + " agent — one run per acknowledged bug, run in parallel.",
		"",
		"1. Run `arac bug list --state acknowledged --json` in the shell to get the bugs to fix. If the shell is unavailable, ask the user to run it and paste the output.",
		"2. Launch the " + agentRef + " agent once per acknowledged bug, running as many concurrently as the platform allows. Give each run only its assigned bug.",
		"3. Each run finds the root cause and makes the minimal correct change, then verifies: build and/or test the affected scope with Bash and clear any new topology warnings. If an edit fails because another agent changed the file, re-read the resource and retry.",
		"4. Each run deletes its bug report with bug_delete once the fix is verified; if a bug cannot be fixed, it leaves the report in place and explains why.",
		"5. After all runs finish, report how many bugs were fixed, deferred (unfixable), and failed.",
	}, "\n")
}

// Generates Claude agent YAML frontmatter with tools and MCP server configuration.
func claudeAgentContent(name, description string, eff helper.AgentConfig, prompt string) string {
	body := prompts.WithToolsListing(prompt, eff.MCPTools, claudeNativeReadAvailable(eff))
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntools: %s\n%s%s---\n\n%s\n", name, description, strings.Join(claudeToolsForAgent(eff), ", "), agentModelFrontmatter(eff.Model), claudeMCPServersFrontmatter(name), body)
}

// Formats agent YAML frontmatter with description, model config, permissions, and tool listings.
func openCodeAgentContent(description string, eff helper.AgentConfig, prompt string) string {
	body := prompts.WithToolsListing(prompt, eff.MCPTools, openCodeNativeRead)
	return fmt.Sprintf("---\ndescription: %s\nmode: subagent\n%spermission:\n%s---\n\n%s\n", description, agentModelFrontmatter(eff.Model), openCodePermissionsForAgent(eff), body)
}

// agentModelFrontmatter renders a `model:` frontmatter line for a generated
// agent file when the resolved per-agent config pins a model. An empty or
// "<inherits>" model yields no line, so the harness falls back to its default
// (inherit the main agent's model).
func agentModelFrontmatter(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || model == "<inherits>" {
		return ""
	}
	return fmt.Sprintf("model: %s\n", model)
}

// Generates MCP server frontmatter YAML configuration for the arac serve tool with tool-profile and harness settings
func claudeMCPServersFrontmatter(agentName string) string {
	return fmt.Sprintf("mcpServers:\n  - aracne:\n      type: stdio\n      command: arac\n      args: [\"serve\", \"--tool-profile\", \"%s\", \"--harness\", \"claude_code\"]\n", agentName)
}

// serverToolProfile returns the --tool-profile value the generated harness config actually
// starts the MCP server with for a given agent. This is the invariant every generator
// depends on, so it lives in one named place rather than being re-derived:
//
//   - Claude Code gets a server per agent (see claudeMCPServersFrontmatter), so the profile
//     is the agent's own name.
//   - OpenCode gets ONE shared server for every agent (see initOpenCode), always "all".
func serverToolProfile(harness, agentName string) string {
	if harness == "opencode" {
		return "all"
	}
	return agentName
}

// claudeNativeReadAvailable and openCodeNativeRead report, per harness, whether the MCP
// server serving an agent still sees a native read tool -- which is what decides whether
// aracne's read registers as "read" or "read_resource" (toolspec.ResolveReadToolName).
// Every generated tool name goes through one of them; a generated name that does not match
// the registered one is not an error, it is a silent denial.
//
// Claude Code gives each agent its OWN server (--tool-profile <agent>, see
// claudeMCPServersFrontmatter), so the answer is that agent's own blocked_tools -- exactly
// what cli.NativeReadAvailable computes for the same pair.
func claudeNativeReadAvailable(eff helper.AgentConfig) bool {
	return !toolNameSet(eff.BlockedTools)["read"]
}

// OpenCode runs ONE server for every agent (--tool-profile all, see initOpenCode), and
// BuildToolRegistry forces nativeReadAvailable=false for the "all" profile regardless of
// any agent's blocked_tools. So the short name always wins on this harness.
const openCodeNativeRead = false

// claudeToolsForAgent builds the Claude `tools:` allow-list: the agent's MCP
// tools (prefixed) plus each native tool not present in blocked_tools.
func claudeToolsForAgent(eff helper.AgentConfig) []string {
	var result []string
	for _, name := range toolspec.ResolveToolNames(eff.MCPTools, claudeNativeReadAvailable(eff)) {
		result = append(result, "mcp__aracne__"+name)
	}
	blocked := toolNameSet(eff.BlockedTools)
	for _, n := range nativeToolNames() {
		if !blocked[n.key] {
			result = append(result, n.claude)
		}
	}
	return result
}

// openCodePermissionsForAgent builds the OpenCode permission block: native
// tools allowed unless blocked, all aracne tools denied except the agent's
// MCP tools. When read/grep are blocked, bash gets glob deny-patterns for the
// direct read/grep shell forms (see writeOpenCodeBashPermission).
func openCodePermissionsForAgent(eff helper.AgentConfig) string {
	blocked := toolNameSet(eff.BlockedTools)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  read: %s\n", nativePermission(!blocked["read"])))
	b.WriteString(fmt.Sprintf("  edit: %s\n", nativePermission(!blocked["edit"] && !blocked["write"])))
	writeOpenCodeBashPermission(&b, blocked)
	b.WriteString("  \"aracne_*\": deny\n")
	for _, toolName := range toolspec.ResolveToolNames(eff.MCPTools, openCodeNativeRead) {
		b.WriteString(fmt.Sprintf("  \"aracne_%s\": allow\n", toolName))
	}
	return b.String()
}

// openCodeReadDenyPatterns / openCodeGrepDenyPatterns are the direct (un-piped)
// shell forms denied when read/grep are blocked. OpenCode matches a bash
// permission pattern against the full command line, so an anchored `head *`
// denies `head foo.go` but not `cmd | head` — piped output viewing stays
// allowed, mirroring the Claude Code guard's pipe exemption.
var (
	openCodeReadDenyPatterns = []string{"cat *", "head *", "tail *", "less *"}
	openCodeGrepDenyPatterns = []string{"grep *", "rg *"}
)

// openCodeBashPermission builds the OpenCode `bash` permission value for the
// global JSON config: "deny" when the whole Bash tool is blocked, "allow" when
// neither read nor grep is blocked, otherwise a glob-pattern map allowing
// everything except the direct read/grep shell forms.
func openCodeBashPermission(blocked map[string]bool) interface{} {
	if blocked["bash"] {
		return "deny"
	}
	if !blocked["read"] && !blocked["grep"] {
		return "allow"
	}
	rules := map[string]interface{}{"*": "allow"}
	if blocked["read"] {
		for _, p := range openCodeReadDenyPatterns {
			rules[p] = "deny"
		}
	}
	if blocked["grep"] {
		for _, p := range openCodeGrepDenyPatterns {
			rules[p] = "deny"
		}
	}
	return rules
}

// writeOpenCodeBashPermission renders the same policy as openCodeBashPermission
// into an agent YAML permission block, in a deterministic order.
func writeOpenCodeBashPermission(b *strings.Builder, blocked map[string]bool) {
	if blocked["bash"] {
		b.WriteString("  bash: deny\n")
		return
	}
	if !blocked["read"] && !blocked["grep"] {
		b.WriteString("  bash: allow\n")
		return
	}
	b.WriteString("  bash:\n")
	b.WriteString("    \"*\": allow\n")
	if blocked["read"] {
		for _, p := range openCodeReadDenyPatterns {
			b.WriteString(fmt.Sprintf("    %q: deny\n", p))
		}
	}
	if blocked["grep"] {
		for _, p := range openCodeGrepDenyPatterns {
			b.WriteString(fmt.Sprintf("    %q: deny\n", p))
		}
	}
}

// nativeToolNames maps the aracne-relevant native tool keys to their Claude
// (capitalized) tool names.
func nativeToolNames() []struct{ key, claude string } {
	return []struct{ key, claude string }{
		{"read", "Read"},
		{"grep", "Grep"},
		{"edit", "Edit"},
		{"write", "Write"},
		{"bash", "Bash"},
	}
}

// Writes a command markdown file with description and template content.
func writeCommand(dir, name, description, template string, autoYes bool) {
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template), autoYes)
}

// Creates an OpenCode primary command markdown file with description, agent name, and template content.
func writeOpenCodePrimaryCommand(dir, name, description, agentName, template string, autoYes bool) {
	content := fmt.Sprintf("---\ndescription: %s\nagent: %s\n---\n\n%s\n", description, agentName, template)
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, content, autoYes)
}

// Creates an OpenCode command markdown file with description, agent, and template frontmatter.
func writeOpenCodeCommand(dir, name, description, agentName, template string, autoYes bool) {
	content := fmt.Sprintf("---\ndescription: %s\nagent: %s\nsubtask: true\n---\n\n%s\n", description, agentName, template)
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, content, autoYes)
}

// Writes an agent markdown file to disk with the given name and content.
func writeAgent(dir, name, content string, autoYes bool) {
	writeMarkdownFile(filepath.Join(dir, name+".md"), "agent "+name, content, autoYes)
}

// Writes markdown content to a file, prompting for confirmation if it already exists unless autoYes is set.
func writeMarkdownFile(path, label, content string, autoYes bool) {
	if _, err := os.Stat(path); err == nil {
		if autoYes || promptReplace(path) {
			fmt.Printf("Overwriting %s at %s\n", label, path)
		} else {
			fmt.Printf("%s already present at %s, skipping\n", label, path)
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("%s written to %s\n", label, path)
}

const (
	// AracIntegrationStart is the heading that opens the generated block, and the marker used
	// to find that block again so a re-run REPLACES it instead of appending a second copy.
	// It must track the heading prompts.introductionSection actually emits.
	AracIntegrationStart = "# Aracne"
	// AracIntegrationLegacyStart is the heading earlier versions wrote. It is still matched so
	// upgrading a project rewrites its existing block rather than stacking a new one under it,
	// and so `arac disable` can still remove a block written by an older binary.
	AracIntegrationLegacyStart = "# Aracne Project Integration"
	// AracIntegrationEnd is the closing line every contract but ModeCLI writes.
	AracIntegrationEnd = "Good Luck in your task."
)

// aracIntegrationEndMarkers are the lines that can close a generated block.
//
// Each is the actual LAST LINE of some contract, never a delimiter injected for the parser's
// benefit. An HTML comment would have been easier to match and is the wrong trade: the block
// is a prompt, it is re-sent on every request, and a marker the model can see but cannot use
// is noise in it. So the parser learns the real closing lines instead, and a contract's last
// sentence has to earn its place as writing rather than as punctuation.
var aracIntegrationEndMarkers = []string{
	AracIntegrationEnd,
	prompts.AracneReadClosingLine,
}

// findAracIntegrationEnd locates the generated block's closing line, whichever contract wrote
// it, searching from the block's opening heading. Returns the EARLIEST match and the marker
// that produced it, or -1 when none is present.
func findAracIntegrationEnd(content string, from int) (int, string) {
	best, bestMarker := -1, ""
	for _, marker := range aracIntegrationEndMarkers {
		if i := findMarkdownLine(content, marker, from); i >= 0 && (best < 0 || i < best) {
			best, bestMarker = i, marker
		}
	}
	return best, bestMarker
}

// findAracIntegrationStart locates the generated block's opening heading, current or legacy,
// returning -1 when the file has no aracne block.
func findAracIntegrationStart(content string) int {
	if i := findMarkdownLine(content, AracIntegrationStart, 0); i >= 0 {
		return i
	}
	return findMarkdownLine(content, AracIntegrationLegacyStart, 0)
}

// Updates or creates a markdown file by merging a new segment into an existing integration section.
func writeMarkdownIntegrationFile(path, label, segment string) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		os.Exit(1)
	}

	existing := ""
	if err == nil {
		existing = string(data)
	}
	updated := updateMarkdownIntegrationSegment(existing, segment)

	if updated == existing {
		fmt.Printf("%s already up to date at %s\n", label, path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("%s updated at %s\n", label, path)
}

// Replaces or inserts a markdown integration segment, preserving existing content and handling line endings.
func updateMarkdownIntegrationSegment(existing, segment string) string {
	lineEnding := markdownLineEnding(existing)
	segment = normalizeMarkdownSegment(segment, lineEnding)
	if strings.TrimSpace(existing) == "" {
		return segment
	}

	start := findAracIntegrationStart(existing)
	if start >= 0 {
		end, marker := findAracIntegrationEnd(existing, start)
		if end >= 0 {
			end += len(marker)
			if strings.HasPrefix(existing[end:], "\r\n") {
				end += 2
			} else if strings.HasPrefix(existing[end:], "\n") {
				end++
			}
			return existing[:start] + segment + existing[end:]
		}
	}

	insertAt := markdownIntegrationInsertionIndex(existing)
	prefix := existing[:insertAt]
	suffix := existing[insertAt:]
	if prefix != "" {
		if !strings.HasSuffix(prefix, "\n") {
			prefix += lineEnding
		}
		if !hasTrailingBlankLine(prefix) {
			prefix += lineEnding
		}
	}
	if suffix != "" && !strings.HasPrefix(suffix, "\n") && !strings.HasPrefix(suffix, "\r\n") {
		segment += lineEnding
	}
	return prefix + segment + suffix
}

// Normalizes markdown text by trimming whitespace and standardizing line endings to a specified format.
func normalizeMarkdownSegment(segment, lineEnding string) string {
	segment = strings.TrimSpace(segment)
	segment = strings.ReplaceAll(segment, "\r\n", "\n")
	segment = strings.ReplaceAll(segment, "\r", "\n")
	if lineEnding != "\n" {
		segment = strings.ReplaceAll(segment, "\n", lineEnding)
	}
	return segment + lineEnding
}

// Detects and returns the line ending style (CRLF or LF) used in the content.
func markdownLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// Checks whether content ends with a trailing blank line (double newline).
func hasTrailingBlankLine(content string) bool {
	return strings.HasSuffix(content, "\n\n") || strings.HasSuffix(content, "\r\n\r\n")
}

// Returns the index where markdown integration content should be inserted, after BOM, frontmatter, and leading headings.
func markdownIntegrationInsertionIndex(content string) int {
	pos := 0
	if strings.HasPrefix(content, "\ufeff") {
		pos = len("\ufeff")
	}
	pos = skipMarkdownFrontmatter(content, pos)
	afterBlanks := skipBlankMarkdownLines(content, pos)
	if !markdownLineIsHeading(lineAt(content, afterBlanks)) {
		return pos
	}

	pos = afterBlanks
	for pos < len(content) {
		line, next := nextMarkdownLine(content, pos)
		if !markdownLineIsHeading(line) {
			break
		}
		pos = skipBlankMarkdownLines(content, next)
	}
	return pos
}

// Skips YAML frontmatter (--- delimited block) from markdown content and returns position after it.
func skipMarkdownFrontmatter(content string, pos int) int {
	line, next := nextMarkdownLine(content, pos)
	if strings.TrimSpace(line) != "---" {
		return pos
	}
	for next < len(content) {
		line, after := nextMarkdownLine(content, next)
		if strings.TrimSpace(line) == "---" {
			return after
		}
		next = after
	}
	return pos
}

// Advances a position cursor past consecutive blank markdown lines, returning the offset of the next non-blank line.
func skipBlankMarkdownLines(content string, pos int) int {
	for pos < len(content) {
		line, next := nextMarkdownLine(content, pos)
		if strings.TrimSpace(line) != "" {
			break
		}
		pos = next
	}
	return pos
}

// Returns the markdown line containing the given position in the content.
func lineAt(content string, pos int) string {
	line, _ := nextMarkdownLine(content, pos)
	return line
}

// Extracts the next line from markdown content starting at a given position, returning the line and next position.
func nextMarkdownLine(content string, pos int) (string, int) {
	if pos >= len(content) {
		return "", len(content)
	}
	newline := strings.IndexByte(content[pos:], '\n')
	if newline < 0 {
		return content[pos:], len(content)
	}
	next := pos + newline + 1
	return content[pos:next], next
}

// Checks whether a markdown line is a heading by detecting leading hash symbols.
func markdownLineIsHeading(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
}

// Searches for a markdown line matching a marker starting from a given position.
func findMarkdownLine(content, marker string, from int) int {
	for from < len(content) {
		line, next := nextMarkdownLine(content, from)
		if strings.TrimSpace(line) == marker {
			return from
		}
		from = next
	}
	return -1
}

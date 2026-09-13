package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// RunSetup writes the harness integration this project's config describes: the commands, the
// agents, the guard hook, the MCP entry (or its removal), and the contract in CLAUDE.md /
// AGENTS.md.
//
// WHY THIS IS ITS OWN COMMAND. It used to be `arac init`, and `arac init` was two jobs in one
// name: the thing you run once to set a repository up, and the thing you run every time the
// config changes to re-render what the config decides. Those want opposite behaviour. Setting
// up should ask questions; re-rendering must ask none, because it runs after a hand edit, in a
// script, in a Makefile, on a machine with nobody at the keyboard. `arac init` is now the
// first job (see init.go) and calls this one at the end of it; this is the second.
//
// It READS the mode and never writes it. That is the whole contract of this command: the mode
// a project is in is a decision it has already made -- in the wizard, or by hand in
// .aracne/config.json -- and the artifacts are downstream of it. The `--mcp` flag that used to
// live here set cfg.Mode = ModeMCP and saved it, which meant the command that re-renders your
// integration could also silently change which integration you had. Mode is question two of
// `arac init` now, or one line of JSON.
func RunSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Write the Claude Code integration")
	opencode := fs.Bool("opencode", false, "Write the OpenCode integration")
	global := fs.Bool("global", false, "Install globally")
	yes := fs.Bool("y", false, "Auto-confirm all replacement prompts")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}
	runSetup(*claude, *opencode, *global, *yes)
}

// runSetup is the command without its flags, so `arac init` can finish its wizard by calling
// it directly rather than by assembling an argv and parsing it again.
func runSetup(claude, opencode, global, autoYes bool) {
	dbPath := setupDBPath()
	configPath := helper.ConfigPath(dbPath)
	// A --global install renders from the project it is run in when there is one -- that is
	// how `arac init --global` carries its answers to user level -- but it does not MAKE one:
	// EnsureConfig here left a stray .aracne/config.json in whatever directory the command
	// happened to run from. With no project, the defaults are the config.
	var cfg *helper.Config
	if global && !fileExists(configPath) {
		cfg = helper.DefaultConfig()
	} else {
		cfg = helper.EnsureConfig(configPath)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid .aracne/config.json: %v\n", err)
		os.Exit(1)
	}
	// A key nothing reads is not fatal -- an older binary should still run a newer config --
	// but it must not be silent. Validate() only ever saw the DECODED config, where a
	// misspelled key has already been dropped, so this is the one place it can be caught.
	if raw, err := os.ReadFile(configPath); err == nil {
		if warning := helper.ConfigKeyWarning(raw); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}
	}
	announceMode(cfg)

	// Read from the database rather than scanned for: setup must stay cheap and must work on a
	// fresh checkout. An empty result is the honest answer there, and the contract renders its
	// language-free form -- the next `arac setup` after a scan fills it in. (`arac init` scans
	// before it gets here, so the wizard's first contract already names the languages.)
	languages := TopologyLanguages(dbPath)
	if global {
		// A user-level contract is read in every project, so it cannot name this one's
		// languages ("a graph of this Go project") -- it gets the language-free form.
		languages = nil
	}

	if opencode {
		initOpenCode(global, cfg, autoYes, languages)
	}
	if claude {
		initClaudeCode(global, cfg, autoYes, languages)
	}
}

// Initializes OpenCode integration by configuring MCP servers, permissions, commands, agents, and plugins.
func initOpenCode(global bool, cfg *helper.Config, autoYes bool, languages []string) {
	configPath, configDir, agentsMdPath := opencodePaths(global)
	// Before the first write, so the record can tell a file this run creates from one the
	// operator already had -- which is what `arac disable` needs to know before it deletes one.
	state, _ := loadSetupState(configPath)
	noteConfigCreation(&state, configPath)
	noteIntegrationCreation(&state, configDir, openCodeIntegrationDirs, agentsMdPath)
	os.MkdirAll(configDir, 0755)
	mainEff := cfg.EffectiveAgent("opencode", "main")

	config := readJSONConfig(configPath)
	// The shared server is written in EVERY mode.
	//
	// OpenCode runs one server for every agent (--tool-profile all), and the generated
	// sub-agents need it: the descriptions executor exists to call update_description, which
	// has no shell equivalent, so gating the server on the MAIN agent's surface left that
	// agent with nothing to call in three of the four modes. What the mode still decides is
	// the permission block below -- outside ModeMCP the main agent is allowed none of these
	// tools, and only the sub-agents' own blocks open them.
	upsertMCPServer(config, "mcp", map[string]interface{}{
		"type":    "local",
		"command": []string{aracBinary(), "serve", "--tool-profile", "all", "--harness", "opencode"},
		"enabled": true,
	})

	permissionMap := openCodePermissionBlock(config, &state)
	// OpenCode's permission block is the guard's blocked_tools decision spelled for a different
	// harness, so it goes through the SAME filter the guard does (Config.BlockableInMode): nothing
	// in the intercepting modes, and no `grep` outside ModeMCP. Denying something here that the
	// guard would never deny is how the two enforcement paths drift apart -- and the one the
	// operator notices is this one, because it refuses silently. Gating on the mode alone missed
	// the second rule: `blocked_tools: ["grep"]` in ModeCLI was answered on Claude Code and
	// refused outright here, under an AGENTS.md that (correctly) mentioned no restriction.
	//
	// And it only ever TIGHTENS. These three keys are the operator's before they are aracne's:
	// they used to be assigned outright, so `"edit": "ask"` became "allow", a structured
	// `read` policy denying `*.env` became "allow", and a key that was absent became an explicit
	// "allow" that also overrode OpenCode's own `*.env` ask. Now a key aracne does not gate is
	// left exactly as found, and one it must deny is written with the user's value remembered
	// (applyOpenCodeNativePermissions), so `arac disable` puts back what was there.
	blocked := openCodeMainBlocked(cfg)
	applyOpenCodeNativePermissions(permissionMap, blocked, &state)
	delete(permissionMap, "write")
	// Every aracne_* rule is re-derived from the config, the way upsertAracneAllowRules does on
	// the Claude side: only adding them left a switch out of ModeMCP -- or a tool dropped from
	// mcp_tools -- still allowing tools the contract no longer mentions.
	for key := range permissionMap {
		if strings.HasPrefix(key, "aracne_") {
			delete(permissionMap, key)
		}
	}
	permissionMap["aracne_*"] = "deny"
	if cfg.MCPEnabled() {
		for _, toolName := range toolspec.ResolveToolNames(mainEff.MCPTools, openCodeNativeRead) {
			permissionMap["aracne_"+toolName] = "allow"
		}
	}
	config["permission"] = permissionMap
	writeJSONConfig(configPath, config)
	saveSetupState(configPath, state)
	fmt.Printf("[OpenCode] Config written to %s\n", configPath)

	commandsDir := filepath.Join(configDir, "commands")
	agentsDir := filepath.Join(configDir, "agents")
	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	batchSize := cfg.AgentParam("opencode", "descriptions-generation-executor", "max-batch-size", helper.DefaultDescriptionBatchSize)
	writeOpenCodePrimaryCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", "build", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", batchSize), autoYes)
	writeOpenCodeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", "build", prompts.DescriptionsClearCommand(), autoYes)
	writeAgent(agentsDir, "descriptions-generation-executor", openCodeAgentContent("Generates descriptions for one assigned batch of undocumented topology resources", cfg.EffectiveAgent("opencode", "descriptions-generation-executor"), openCodeModelProvider(cfg), prompts.DescriptionsGenerationExecutorPrompt()), autoYes)

	if cfg.BugManagementEnabled() {
		// bug-hunter stays a subtask command: it does its own scanning and needs no fan-out.
		// bug-judge and bug-solver are PRIMARY commands -- they must spawn one sub-agent per
		// bug, and an OpenCode subtask cannot spawn further subtasks (the same reason
		// descriptions-generate is primary).
		writeOpenCodeCommand(commandsDir, "bug-hunter", "Scan the whole codebase for bugs", "bug-hunter", bugHunterOpenCodeCommand(), autoYes)
		writeOpenCodePrimaryCommand(commandsDir, "bug-judge", "Triage every pending bug by fanning out Bug Judge sub-agents in parallel", "build", bugJudgeCommandForAgent("bug-judge"), autoYes)
		writeOpenCodePrimaryCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", "build", bugSolverCommandForAgent("bug-solver"), autoYes)

		writeAgent(agentsDir, "bug-hunter", openCodeAgentContent("Scans the entire project topology looking for bugs", cfg.EffectiveAgent("opencode", "bug-hunter"), "", prompts.BugHunterPrompt()), autoYes)
		writeAgent(agentsDir, "bug-judge", openCodeAgentContent("Triages pending bugs by comparing against dismissed bug patterns", cfg.EffectiveAgent("opencode", "bug-judge"), "", prompts.BugJudgePrompt()), autoYes)
		writeAgent(agentsDir, "bug-solver", openCodeAgentContent("Fixes acknowledged bugs in the codebase and removes them", cfg.EffectiveAgent("opencode", "bug-solver"), "", prompts.BugSolverPrompt()), autoYes)
	} else {
		pruneBugArtifacts(commandsDir, agentsDir, "OpenCode")
	}

	writeOpenCodePlugins(mainEff.Plugins, configDir, autoYes)
	// The pre-tool scan plugin is installed unconditionally (independent of plugins), the
	// same way Claude Code's guard hook is: it is what keeps the graph current for the call
	// that is about to read it, on whichever surface the project is on.
	writeOpenCodePreToolScanPlugin(filepath.Join(configDir, "plugins"), autoYes)
	writeMarkdownIntegrationFile(agentsMdPath, "OpenCode AGENTS.md", prompts.AgentsMdForConfig(cfg, languages))
	fmt.Println("[OpenCode] Restart OpenCode to activate the topology workflow.")
}

// Initializes Claude Code integration by configuring MCP servers, commands, agents, plugins, and guard hooks.
func initClaudeCode(global bool, cfg *helper.Config, autoYes bool, languages []string) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)
	claudeBaseDir := filepath.Dir(commandsDir)
	mainEff := cfg.EffectiveAgent("claude_code", "main")

	// settings.json is written by the three writers below, and the MCP config, the contract and
	// the directories by the rest. Whether each existed before this run is the one fact `arac
	// disable` needs before it may delete it -- so it is recorded before the first write.
	settingsPath := filepath.Join(claudeBaseDir, "settings.json")
	settingsState, _ := loadSetupState(settingsPath)
	noteConfigCreation(&settingsState, settingsPath)
	noteIntegrationCreation(&settingsState, claudeBaseDir, claudeIntegrationDirs, claudeMdPath)
	if cfg.MCPEnabled() && !pathExists(mcpConfigPath) {
		settingsState.MCPConfigCreated = true
	}
	os.MkdirAll(claudeBaseDir, 0755)
	saveSetupState(settingsPath, settingsState)

	if cfg.MCPEnabled() {
		claudeConfig := readJSONConfig(mcpConfigPath)
		if upsertMCPServer(claudeConfig, "mcpServers", map[string]interface{}{
			"command": aracBinary(),
			"args":    []string{"serve", "--tool-profile", "main", "--harness", "claude_code"},
		}) {
			writeJSONConfig(mcpConfigPath, claudeConfig)
			fmt.Printf("[Claude Code] MCP server configured in %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] MCP server already current in %s\n", mcpConfigPath)
		}
	} else if dropAracneMCPServer(mcpConfigPath) {
		// Leaving a stale entry behind would start a server whose tools the contract no
		// longer mentions -- the model pays for their schemas on every request and is told
		// nothing about them. Init has to be able to move a project BETWEEN surfaces, not
		// only onto one.
		fmt.Printf("[Claude Code] Removed the aracne MCP server from %s (mode: %s)\n",
			mcpConfigPath, cfg.EffectiveMode())
		// And a file that is now `{}` goes too, when this setup made it -- the same rule
		// `arac disable` applies, so switching surfaces and disabling leave the same shape.
		if removeIfOnlyAracneWrote(mcpConfigPath, readJSONConfig(mcpConfigPath), settingsState.MCPConfigCreated) {
			fmt.Printf("[Claude Code] Removed %s (aracne created it, and nothing is left in it)\n", mcpConfigPath)
		}
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	batchSize := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", helper.DefaultDescriptionBatchSize)
	writeCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", batchSize), autoYes)
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

	writeClaudePlugins(mainEff.Plugins, claudeBaseDir, global, autoYes)
	// The guard hook is installed unconditionally (independent of plugins): it
	// must always warn on native/shell tool usage and block per blocked_tools.
	//
	// `global` is threaded in because it decides how the settings entry NAMES the script it
	// just wrote: ${CLAUDE_PROJECT_DIR} for a project install, the absolute path for a
	// user-level one. See hookScriptRef.
	writeClaudeGuardHook(filepath.Join(claudeBaseDir, "settings.json"), filepath.Join(claudeBaseDir, "hooks"), global, autoYes)
	// Pre-approve the aracne MCP tools so Claude Code does not prompt on every call.
	//
	// In EVERY mode, not only ModeMCP. The main agent has no MCP tools on the terminal
	// surface, but the generated sub-agents each declare their own scoped server inline and
	// do -- so skipping this left an intercepting project prompting on every
	// `update_description` its descriptions executor made.
	writeClaudePermissions(filepath.Join(claudeBaseDir, "settings.json"), cfg)
	// And decide whether those tools arrive with their schemas or behind a lookup. Called in
	// EVERY mode, unlike the question that sets it: the answer is recorded once and this is
	// what withdraws it again when the project moves off ModeMCP.
	writeClaudeToolSearchEnv(filepath.Join(claudeBaseDir, "settings.json"), cfg)
	writeMarkdownIntegrationFile(claudeMdPath, "Claude Code CLAUDE.md", prompts.ClaudeMdForConfig(cfg, languages))
	fmt.Println("[Claude Code] Restart Claude Code to activate the topology workflow.")
}

// announceMode says which of the four modes this project is in, because the answer decides
// everything else `arac setup` just wrote.
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
	// Emptied, not deleted: Claude Code validates `.mcp.json` against a schema that requires
	// `mcpServers`, so a file left as `{}` breaks every session in the project. The caller's
	// removeIfOnlyAracneWrote still takes the whole file when aracne created it.
	config["mcpServers"] = servers
	writeJSONConfig(mcpConfigPath, config)
	return true
}

// bugArtifactFiles are the command and agent markdown files the bug pipeline owns. The two
// sets happen to share their names; both directories get the same list.
var bugArtifactFiles = []string{"bug-hunter.md", "bug-judge.md", "bug-solver.md"}

// pruneBugArtifacts removes the generated bug commands and agents when
// features.bug_management is off.
//
// Without this, `arac setup` would not be idempotent with respect to the flag: a project that
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

// openCodeMainBlocked is the OpenCode main agent's blocked_tools, narrowed to what this mode may
// refuse -- the exact set loadGuardConfig computes for the Claude Code guard.
func openCodeMainBlocked(cfg *helper.Config) map[string]bool {
	return cfg.BlockableInMode(toolNameSet(cfg.EffectiveAgent("opencode", "main").BlockedTools))
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
	root := localIntegrationRoot()
	return filepath.Join(root, ".opencode", "opencode.json"), filepath.Join(root, ".opencode"), filepath.Join(root, "AGENTS.md")
}

// setupDBPath is the database -- and so the config and the project root -- `arac setup` and
// `arac disable` work on: ProjectDBPath's upward walk, and failing that the nearest
// .aracne/config.json above the working directory. A committed config with no scan yet (the
// database is usually ignored) is still a project, and a setup run from inside it is for it.
func setupDBPath() string {
	dbPath := ProjectDBPath(DefaultDBRelative)
	if dbPath != DefaultDBRelative || fileExists(dbPath) {
		return dbPath
	}
	if wd, err := os.Getwd(); err == nil {
		if cfg, ok := findUpward(wd, helper.ConfigPath(DefaultDBRelative)); ok {
			return filepath.Join(filepath.Dir(cfg), filepath.Base(DefaultDBRelative))
		}
	}
	return dbPath
}

// localIntegrationRoot is the directory a project install writes into: the root of the project
// setupDBPath resolved, spelled "." when that is the working directory.
//
// The config and the database were already found by walking up, while every output path was
// relative to the working directory -- so `arac setup` from `proj/shapes` rendered proj's config
// into a second CLAUDE.md and .claude/ under shapes/, which a harness launched at the root never
// reads, and left the real integration un-rendered. `arac disable` from there removed nothing.
func localIntegrationRoot() string {
	root := ProjectRootFor(setupDBPath())
	if wd, err := os.Getwd(); err == nil {
		if abs, err := filepath.Abs(root); err == nil && abs == wd {
			return "."
		}
	}
	return root
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
	root := localIntegrationRoot()
	return filepath.Join(root, ".mcp.json"), filepath.Join(root, ".claude", "commands"), filepath.Join(root, ".claude", "agents"), filepath.Join(root, "CLAUDE.md")
}

// Reads and parses a JSON configuration file, exiting on parse errors or returning an empty map
// if the file is missing. Comments and trailing commas are tolerated -- see parseJSONConfig.
func readJSONConfig(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]interface{})
	}
	config, err := parseJSONConfig(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", path, err)
		os.Exit(1)
	}
	return config
}

// upsertMCPServer merges aracne's own server entry into a harness MCP config, reporting whether
// anything changed. `section` is the key that holds the servers ("mcpServers" for Claude Code,
// "mcp" for OpenCode); every other server in it, and every other key in the file, is untouched.
//
// IT GUARDS ON ARACNE'S ENTRY, NOT ON THE SECTION, and that is the whole of the fix. The
// predicate it replaces asked whether the file already had a `mcpServers` key at all and then
// prompted "Config .mcp.json already exists. Overwrite? [y/N]" -- a question that misdescribed
// what the code does (it merges; it has never overwritten) and whose "no" skipped aracne
// entirely. A project with any other MCP server, and every aracne project after its first
// setup, took that branch: on a pipe it skipped unprompted, printed "Restart Claude Code to
// activate the topology workflow", and wrote a CLAUDE.md promising MCP tools against a config
// with no aracne server in it.
func upsertMCPServer(config map[string]interface{}, section string, entry map[string]interface{}) bool {
	servers, _ := config[section].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}
	if jsonEqual(servers["aracne"], entry) {
		return false
	}
	servers["aracne"] = entry
	config[section] = servers
	return true
}

// jsonEqual compares two decoded JSON values by their encoding, which is stable: encoding/json
// sorts map keys. It is how the writers above tell "already current" from "needs rewriting"
// without re-deriving equality per value shape.
func jsonEqual(a, b interface{}) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(left, right)
}

// Writes a config map back to a file with directory creation, PATCHING the file that is there
// rather than re-encoding it: the operator's key order and comments are load-bearing in a file
// aracne only merges into. See jsonconfig.go.
func writeJSONConfig(path string, config map[string]interface{}) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	original, _ := os.ReadFile(path)
	out, err := encodeJSONConfig(original, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
		os.Exit(1)
	}
	// ATOMIC. This writes .claude/settings.json and .opencode/opencode.json, files aracne
	// does not own -- and readJSONConfig calls os.Exit(1) on a parse error, so a write torn
	// between truncate and fill makes every later `arac setup` and `arac disable` fail hard,
	// with a hand-edit as the only way out. AtomicWriteFile also preserves the destination's
	// existing mode, which os.WriteFile does not.
	if err := helper.AtomicWriteFile(path, out, 0644); err != nil {
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
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntools: %s\n%s%s---\n\n%s\n", name, description, strings.Join(claudeToolsForAgent(eff), ", "), agentModelFrontmatter(claudeAgentModel(eff.Model)), claudeMCPServersFrontmatter(name), body)
}

// Formats agent YAML frontmatter with description, model config, permissions, and tool listings.
// modelProvider qualifies a bare model id into OpenCode's provider/model form; "" leaves a bare
// id unrendered (see openCodeAgentModel).
func openCodeAgentContent(description string, eff helper.AgentConfig, modelProvider, prompt string) string {
	body := prompts.WithToolsListing(prompt, eff.MCPTools, openCodeNativeRead)
	return fmt.Sprintf("---\ndescription: %s\nmode: subagent\n%spermission:\n%s---\n\n%s\n", description, agentModelFrontmatter(openCodeAgentModel(eff.Model, modelProvider)), openCodePermissionsForAgent(eff), body)
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

// A MODEL CAN BE CONFIGURED AND STILL NOT BELONG IN A HARNESS'S FRONTMATTER.
//
// The descriptions executor's model is THE description model (EffectiveLazyDescriptions): `arac
// init` writes whatever the describer answer called for -- gpt-5.4-mini on the OpenAI branch,
// deepseek-v4-flash on DeepSeek's, `haiku` for the Claude CLI -- into llm.<any>, where every
// harness reads it. That is right for the lazy fill and `arac descriptions generate`, which call
// that provider themselves. Copied verbatim into a sub-agent's `model:` it named a model the
// harness cannot run: Claude Code was handed gpt-5.4-mini, and OpenCode, which splits the value
// on its first "/", resolved `gpt-5.4-mini` to provider "gpt-5.4-mini" with no model at all --
// so /descriptions-generate failed on the wizard's happy path in both. The field is therefore
// READ per harness rather than copied: what the harness can run is rendered, and anything else
// leaves the line out, so the agent inherits the main model.

// claudeAgentModelAliases are the names a Claude Code sub-agent's `model:` takes besides a full
// claude-* id.
var claudeAgentModelAliases = map[string]bool{"haiku": true, "sonnet": true, "opus": true, "inherit": true}

// claudeAgentModel is the configured model as a Claude Code sub-agent can name it, or "" when
// Claude Code cannot run it. An OpenCode-qualified `anthropic/claude-*` is unwrapped, since the
// qualification is the only thing Claude Code would not understand about it.
func claudeAgentModel(model string) string {
	model = strings.TrimSpace(model)
	if rest, ok := strings.CutPrefix(model, "anthropic/"); ok {
		model = rest
	}
	lower := strings.ToLower(model)
	if claudeAgentModelAliases[lower] || strings.HasPrefix(lower, "claude-") {
		return model
	}
	return ""
}

// openCodeAgentModel is the configured model as an OpenCode agent must name it, `provider/model`,
// or "" when there is no unambiguous way to spell it that way.
//
// A value that is already qualified is the operator's own and is kept. A bare id is qualified
// only with modelProvider (see openCodeModelProvider). A Claude Code alias never is: "haiku" is a
// Claude CLI name, not an id in any provider's catalogue.
func openCodeAgentModel(model, modelProvider string) string {
	model = strings.TrimSpace(model)
	switch {
	case model == "" || model == helper.InheritsModel:
		return ""
	case strings.Contains(model, "/"):
		return model
	case modelProvider == "" || claudeAgentModelAliases[strings.ToLower(model)]:
		return ""
	}
	return modelProvider + "/" + model
}

// openCodeModelProvider is the OpenCode provider a bare DESCRIPTIONS model belongs to, or "" when
// the configured transport does not say so unambiguously.
//
// The three API transports are named the way OpenCode names its providers, so an anthropic,
// openai or deepseek project qualifies to anthropic/…, openai/…, deepseek/…. Two cases do not.
// `cli`: the model is whatever that command calls it, and OpenCode may hold no credentials for
// the vendor behind it. And any transport with a base_url: there `openai` names a wire format
// some gateway speaks, and `openai/<model>` would send the agent to OpenAI itself.
//
// It is the descriptions transport, so it qualifies the descriptions executor only; another
// agent's bare model says nothing about which provider it came from.
func openCodeModelProvider(cfg *helper.Config) string {
	resolved := cfg.EffectiveLazyDescriptions("opencode")
	if resolved.BaseURL != "" || !helper.IsAPIProvider(resolved.Provider) {
		return ""
	}
	return resolved.Provider
}

// Generates MCP server frontmatter YAML configuration for the arac serve tool with tool-profile and harness settings.
//
// The command is the RESOLVED binary (aracBinary), quoted, for the same reason the hooks use
// it: a bare `arac` is a bet on the harness inheriting the PATH that ran `arac setup`, and an
// MCP server that fails to start is quiet in both harnesses -- the tool list is simply short.
// See aracBinary.
func claudeMCPServersFrontmatter(agentName string) string {
	return fmt.Sprintf("mcpServers:\n  - aracne:\n      type: stdio\n      command: %s\n      args: [\"serve\", \"--tool-profile\", \"%s\", \"--harness\", \"claude_code\"]\n",
		strconv.Quote(aracBinary()), agentName)
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

// openCodePermissionsForAgent builds a generated sub-agent's OpenCode permission block: every
// aracne tool denied except the agent's own, plus a denial for each native tool its blocked_tools
// refuses. When read/grep are blocked, bash gets glob deny-patterns for the direct read/grep
// shell forms (see writeOpenCodeBashPermission).
//
// IT ONLY EVER TIGHTENS, for the same reason applyOpenCodeNativePermissions does on the main
// agent's side. OpenCode appends an agent's frontmatter rules AFTER the ones its global config
// produced and resolves the list last-match-wins, so an unconditional `read: allow` is not a
// default that yields to the operator -- it is the final rule for that tool, overriding every
// rule they wrote and OpenCode's own `*.env` ask with it. The block shipped with `read: allow`
// and `bash: allow`, which is how the descriptions executor came to run any command and read any
// secret in a project whose owner had asked to confirm both. A tool aracne does not gate is left
// unwritten, so whatever the project decided still decides it.
func openCodePermissionsForAgent(eff helper.AgentConfig) string {
	blocked := toolNameSet(eff.BlockedTools)
	var b strings.Builder
	if blocked["read"] {
		b.WriteString("  read: deny\n")
	}
	if blocked["edit"] || blocked["write"] {
		b.WriteString("  edit: deny\n")
	}
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

// writeOpenCodeBashPermission renders openCodeBashPermission's policy into an agent YAML
// permission block, in a deterministic order -- and only its denials.
//
// The `"*": "allow"` the JSON form opens with is dropped here for the reason withBashDenyPatterns
// drops it on the main agent's side: written into an agent's frontmatter it is the LAST rule
// OpenCode resolves, so it turns every command the project wanted confirmed into one that runs
// unasked. The deny patterns alone are the whole of what this agent has to add; anything they do
// not name is still the operator's decision.
func writeOpenCodeBashPermission(b *strings.Builder, blocked map[string]bool) {
	if blocked["bash"] {
		b.WriteString("  bash: deny\n")
		return
	}
	if !blocked["read"] && !blocked["grep"] {
		return
	}
	b.WriteString("  bash:\n")
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

// writeMarkdownFile writes one of the files `arac setup` generates in full.
//
// IT DOES NOT ASK, and the prompt it used to carry was the largest single gap between what this
// command promises and what it does. Every byte of these files is a pure function of
// .aracne/config.json and the resolved binary path -- there is no user content in a generated
// hook script, agent or command to protect -- so the question had nothing to preserve and one
// bad answer: on a pipe, a Makefile or a CI step, ReadString returned EOF, that read as "no",
// and the file was skipped while settings.json, the MCP entry and CLAUDE.md were rewritten
// around it. The guard hook is the sharp edge, because it embeds the ABSOLUTE path of the
// binary that generated it: rebuild elsewhere and `arac setup` would not repoint it, leaving a
// hook that fails on every tool call. Generated agent markdown drifted the same way, which is
// exactly the silent generator/server divergence ServableMCPTools exists to prevent.
//
// autoYes is kept in the signature because the flag still means something to the caller and to
// every other writer; here there is nothing left for it to decide.
//
// A file already byte-identical is left alone and said to be up to date, so a re-run on an
// unchanged config is quiet rather than a wall of "Overwriting".
func writeMarkdownFile(path, label, content string, autoYes bool) {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == content {
			fmt.Printf("%s already up to date at %s\n", label, path)
			return
		}
		fmt.Printf("Overwriting %s at %s\n", label, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}
	if err := helper.AtomicWriteFile(path, []byte(content), 0644); err != nil {
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
//
// A `# Aracne` HEADING IS NOT PROOF THE BLOCK IS ARACNE'S. It is the obvious title for a team's
// own notes about aracne, and the first such line used to be taken as the start of the
// generated block: with no closing line after it the bounds ran to the next top-level heading,
// and `arac setup` replaced the team's section with the contract -- silently, since this file is
// rewritten without a prompt. So each candidate has to be recognisably ours (see
// isAracneBlockAt); a heading that is not is skipped, and a file holding only a foreign one
// gets the contract appended like any other first-time insertion.
func findAracIntegrationStart(content string) int {
	for from := 0; from < len(content); {
		i := findMarkdownLine(content, AracIntegrationStart, from)
		if i < 0 {
			break
		}
		if isAracneBlockAt(content, i) {
			return i
		}
		_, from = nextMarkdownLine(content, i)
	}
	return findMarkdownLine(content, AracIntegrationLegacyStart, 0)
}

// isAracneBlockAt reports whether the `# Aracne` heading at offset i opens a block aracne wrote.
//
// Two ways to know, because a block may predate the current wording:
//   - its first line of prose is the opening line some contract renders today (the case that
//     survives a reader deleting the closing line, which the fallback in aracIntegrationBounds
//     exists for), or
//   - one of the contract's closing lines follows it with no other `# Aracne` heading in
//     between -- a block written by an older binary, whose opening prose has since changed.
//
// A team's own section satisfies neither: its prose is its own, and the only closing line after
// it, if any, belongs to the real block further down.
func isAracneBlockAt(content string, i int) bool {
	_, body := nextMarkdownLine(content, i)
	for pos := body; pos < len(content); {
		line, next := nextMarkdownLine(content, pos)
		if text := strings.TrimSpace(line); text != "" {
			for _, opening := range contractOpenings() {
				if strings.HasPrefix(text, opening) {
					return true
				}
			}
			break
		}
		pos = next
	}
	end, _ := findAracIntegrationEnd(content, body)
	if end < 0 {
		return false
	}
	other := findMarkdownLine(content, AracIntegrationStart, body)
	return other < 0 || other > end
}

// contractOpeningLen is how much of a contract's opening line identifies it. Short enough to
// stop before the part that varies with the project's languages ("…a pre-analyzed graph of this
// Go project"), long enough that no one writes it by accident.
const contractOpeningLen = 32

// contractOpenings is the start of the first prose line of every contract this binary renders,
// in every mode at either verbosity. Rendered rather than restated, so a change to the contract's
// wording cannot leave the parser looking for the old one.
func contractOpenings() []string {
	seen := map[string]bool{}
	var out []string
	for _, mode := range []string{helper.ModeMCP, helper.ModeCLI, helper.ModeInterceptID, helper.ModeInterceptLineRanges} {
		for _, verbosity := range []string{helper.ContractVerbosityLow, helper.ContractVerbosityHigh} {
			cfg := helper.DefaultConfig()
			cfg.Mode, cfg.ContractVerbosity = mode, verbosity
			opening := firstProseLine(prompts.ContractContent(cfg, nil))
			if len(opening) > contractOpeningLen {
				opening = opening[:contractOpeningLen]
			}
			if opening != "" && !seen[opening] {
				seen[opening] = true
				out = append(out, opening)
			}
		}
	}
	return out
}

// firstProseLine is the first non-blank line after a contract's opening heading.
func firstProseLine(contract string) string {
	lines := strings.Split(contract, "\n")
	for _, line := range lines[1:] {
		if text := strings.TrimSpace(line); text != "" {
			return text
		}
	}
	return ""
}

// aracIntegrationBounds locates the generated block: the byte offsets of its opening heading
// and of the first byte after it, plus whether there is a block at all.
//
// ONE FUNCTION, BECAUSE THE WRITER AND THE REMOVER MUST NOT DISAGREE. `arac setup` had a
// fallback for a block whose closing line the reader had edited away -- it reads as ordinary
// prose in a file they are told is theirs -- and ran to the next top-level heading instead.
// `arac disable` did not: findAracIntegrationEnd returned -1, stripAracneIntegrationSegment
// returned the content unchanged, and the command printed "already clean" while leaving the
// whole contract in place. An uninstall that reports success and removes nothing is worse than
// one that fails, because nothing tells you to look. Both callers now ask the same question.
func aracIntegrationBounds(content string) (start, end int, ok bool) {
	start = findAracIntegrationStart(content)
	if start < 0 {
		return 0, 0, false
	}
	if at, marker := findAracIntegrationEnd(content, start); at >= 0 {
		end = at + len(marker)
		if strings.HasPrefix(content[end:], "\r\n") {
			end += 2
		} else if strings.HasPrefix(content[end:], "\n") {
			end++
		}
		return start, end, true
	}
	// The block opens and its closing line is gone. It runs to the next top-level heading, or
	// to the end of the file when there is none.
	return start, nextTopLevelHeading(content, start), true
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
	if err := helper.AtomicWriteFile(path, []byte(updated), 0644); err != nil {
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

	// A block already present is REPLACED, wherever it sits and whether or not its closing
	// line survived -- see aracIntegrationBounds. Falling through to the insertion path
	// prepended a SECOND contract above the orphaned one, and every later run added another.
	if start, end, ok := aracIntegrationBounds(existing); ok {
		return existing[:start] + segment + existing[end:]
	}

	// A FIRST-TIME insertion goes at the END of the document.
	//
	// It used to go after the leading headings, which for the overwhelmingly common shape --
	// an H1 title followed by prose -- dropped a second H1 (`# Aracne`) between the title and
	// the body it introduces. The author's own paragraphs then read as the body of aracne's
	// block and their `##` sections became subsections of it, in a file aracne is a guest in.
	// Anything that navigates by heading -- a docs site, an outline, a model reading the file
	// as a tree -- attributed the wrong content to the wrong section, and aracne's own
	// CLAUDE.md was living proof.
	//
	// Appending costs nothing: the block is found again by its heading
	// (aracIntegrationBounds), so its position is free, and the end of the file is the only
	// place that cannot orphan someone else's content.
	prefix := existing
	if !strings.HasSuffix(prefix, "\n") {
		prefix += lineEnding
	}
	if !hasTrailingBlankLine(prefix) {
		prefix += lineEnding
	}
	return prefix + segment
}

// nextTopLevelHeading returns the offset of the first "# " heading strictly after `from`, or
// len(content) when there is none. It bounds a generated block whose closing line the reader
// removed, so re-rendering replaces it instead of stacking a second copy above it.
//
// A `#` line inside a fenced code block is not a heading -- a shell comment, or the high
// contract's own `# CONTEXT:` example -- and taking one for a heading cut the block short at its
// first example, orphaning the rest of the contract below the replacement.
func nextTopLevelHeading(content string, from int) int {
	pos := from
	if _, next := nextMarkdownLine(content, pos); next > pos {
		pos = next // skip the opening heading itself
	}
	fence := "" // the open fence's marker, "" outside one
	for pos < len(content) {
		line, next := nextMarkdownLine(content, pos)
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case fence == "" && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")):
			fence = trimmed[:3]
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "# ") || strings.TrimRight(trimmed, "\r\n") == "#":
			return pos
		}
		pos = next
	}
	return len(content)
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

package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/prompts"
	"aracne/internal/topology/domain"
)

func promptReplace(path string) bool {
	fmt.Printf("File %s already exists. Replace? [y/N] ", path)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func promptReplaceConfigExists(path string) bool {
	fmt.Printf("Config %s already exists. Overwrite? [y/N] ", path)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func RunInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Initialize Claude Code integration")
	opencode := fs.Bool("opencode", false, "Initialize OpenCode integration")
	global := fs.Bool("global", false, "Install globally")
	readMode := fs.String("read-mode", "", "Read tool mode: native, mcp, or terminal")
	editMode := fs.String("edit-mode", "", "Edit/write tool mode: native, mcp, or terminal")
	otherMode := fs.String("other-mode", "", "Other topology tool mode: mcp or terminal")
	grepMode := fs.String("grep-mode", "", "Grep/search tool mode: native, mcp, or terminal")
	yes := fs.Bool("y", false, "Auto-confirm all replacement prompts")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	cfgPath := helper.ConfigPath(".aracne/topology.db")
	cfg := helper.EnsureConfig(cfgPath)
	changed := false
	if *readMode != "" {
		mode, err := parseReadToolMode(*readMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfg.ToolModes.Read = mode
		changed = true
	}
	if *editMode != "" {
		mode, err := parseEditToolMode(*editMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfg.ToolModes.Edit = mode
		changed = true
	}
	if *otherMode != "" {
		mode, err := parseOtherToolMode(*otherMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfg.ToolModes.Other = mode
		changed = true
	}
	if *grepMode != "" {
		mode, err := parseGrepToolMode(*grepMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfg.ToolModes.Grep = mode
		changed = true
	}
	if changed {
		if err := helper.SaveConfig(cfg, cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", cfgPath, err)
			os.Exit(1)
		}
	}

	if *opencode {
		initOpenCode(*global, cfg.ToolModes, cfg.ReadSplit, cfg.DescriptionBatchSize, *yes)
	}
	if *claude {
		initClaudeCode(*global, cfg.ToolModes, cfg.ReadSplit, cfg.DescriptionBatchSize, *yes)
	}
}

func parseReadToolMode(value string) (helper.ReadToolMode, error) {
	switch helper.ReadToolMode(value) {
	case helper.ReadModeNative, helper.ReadModeMCP, helper.ReadModeTerminal:
		return helper.ReadToolMode(value), nil
	default:
		return "", fmt.Errorf("invalid --read-mode: %s (must be native, mcp, or terminal)", value)
	}
}

func parseEditToolMode(value string) (helper.EditToolMode, error) {
	switch helper.EditToolMode(value) {
	case helper.EditModeNative, helper.EditModeMCP, helper.EditModeTerminal:
		return helper.EditToolMode(value), nil
	default:
		return "", fmt.Errorf("invalid --edit-mode: %s (must be native, mcp, or terminal)", value)
	}
}

func parseOtherToolMode(value string) (helper.OtherToolMode, error) {
	switch helper.OtherToolMode(value) {
	case helper.OtherModeMCP, helper.OtherModeTerminal:
		return helper.OtherToolMode(value), nil
	default:
		return "", fmt.Errorf("invalid --other-mode: %s (must be mcp or terminal)", value)
	}
}

func parseGrepToolMode(value string) (helper.GrepToolMode, error) {
	switch helper.GrepToolMode(value) {
	case helper.GrepModeNative, helper.GrepModeMCP, helper.GrepModeTerminal:
		return helper.GrepToolMode(value), nil
	default:
		return "", fmt.Errorf("invalid --grep-mode: %s (must be native, mcp, or terminal)", value)
	}
}

func initOpenCode(global bool, modes helper.ToolModes, readSplit map[domain.ResourceKind]bool, descriptionBatchSize int, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)
	os.MkdirAll(configDir, 0755)

	config := readJSONConfig(configPath)
	if anyMCPMode(modes) {
		if shouldWriteConfig(config, "mcp", configPath, "OpenCode", autoYes) {
			mcpMap, _ := config["mcp"].(map[string]interface{})
			if mcpMap == nil {
				mcpMap = make(map[string]interface{})
			}
			mcpMap["arac"] = map[string]interface{}{
				"type":    "local",
				"command": []string{"arac", "serve", "--tool-profile", "all"},
				"enabled": true,
			}
			config["mcp"] = mcpMap
		}
	} else {
		fmt.Println("[OpenCode] Terminal/native mode: no aracne MCP server needed")
	}

	permissionMap, _ := config["permission"].(map[string]interface{})
	if permissionMap == nil {
		permissionMap = make(map[string]interface{})
	}
	permissionMap["read"] = nativePermission(modes.Read == helper.ReadModeNative)
	permissionMap["edit"] = nativePermission(modes.Edit == helper.EditModeNative)
	delete(permissionMap, "write")
	permissionMap["aracne_*"] = "deny"
	for _, toolName := range allowedMCPToolNames(modes, ToolProfileDefault, readSplit) {
		permissionMap["aracne_"+toolName] = "allow"
	}
	config["permission"] = permissionMap
	writeJSONConfig(configPath, config)
	fmt.Printf("[OpenCode] Config written to %s\n", configPath)

	commandsDir := filepath.Join(configDir, "commands")
	agentsDir := filepath.Join(configDir, "agents")
	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	writeOpenCodePrimaryCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", "build", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", descriptionBatchSize), autoYes)
	writeOpenCodeCommand(commandsDir, "descriptions-apply", "Write topology descriptions back into source files as doc comments", "build", prompts.DescriptionsApplyCommand(), autoYes)
	writeOpenCodeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", "build", prompts.DescriptionsClearCommand(), autoYes)
	writeOpenCodeCommand(commandsDir, "bug-hunter", "Launch a Bug Hunter sub-agent to scan the entire topology for bugs", "bug-hunter", bugHunterCommandForAgent("bug-hunter"), autoYes)
	writeOpenCodeCommand(commandsDir, "bug-judge", "Triage pending bugs by launching Bug Judge sub-agents for each node", "bug-judge", bugJudgeCommandForAgent("bug-judge"), autoYes)
	writeOpenCodeCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", "bug-solver", bugSolverCommandForAgent("bug-solver"), autoYes)

	writeAgent(agentsDir, "descriptions-generation-executor", openCodeAgentContent("descriptions-generation-executor", "Generates descriptions for one assigned batch of undocumented topology resources", ToolProfileDescriptionsExecutor, modes, readSplit, prompts.DescriptionsGenerationExecutorPrompt()), autoYes)
	writeAgent(agentsDir, "bug-hunter", openCodeAgentContent("bug-hunter", "Scans the entire project topology looking for bugs", ToolProfileBugHunter, modes, readSplit, prompts.BugHunterPrompt()), autoYes)
	writeAgent(agentsDir, "bug-judge", openCodeAgentContent("bug-judge", "Triages pending bugs by comparing against dismissed bug patterns", ToolProfileBugJudge, modes, readSplit, prompts.BugJudgePrompt()), autoYes)
	writeAgent(agentsDir, "bug-solver", openCodeAgentContent("bug-solver", "Fixes acknowledged bugs in the codebase and removes them", ToolProfileBugSolver, modes, readSplit, prompts.BugSolverPrompt()), autoYes)

	if modes.Edit == helper.EditModeNative {
		writeOpenCodeNativeEditPlugin(filepath.Join(configDir, "plugins"), autoYes)
	}
	writeMarkdownIntegrationFile(agentsMdPath, "OpenCode AGENTS.md", prompts.AgentsMdContentForModes(modes, readSplit))
	fmt.Println("[OpenCode] Restart OpenCode to activate the topology workflow.")
}

func initClaudeCode(global bool, modes helper.ToolModes, readSplit map[domain.ResourceKind]bool, descriptionBatchSize int, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)

	if anyMCPMode(modes) {
		claudeConfig := readJSONConfig(mcpConfigPath)
		if shouldWriteConfig(claudeConfig, "mcpServers", mcpConfigPath, "Claude Code", autoYes) {
			mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
			if mcpServers == nil {
				mcpServers = make(map[string]interface{})
			}
			mcpServers["arac"] = map[string]interface{}{
				"command": "arac",
				"args":    []string{"serve", "--tool-profile", string(ToolProfileDefault)},
			}
			claudeConfig["mcpServers"] = mcpServers
			writeJSONConfig(mcpConfigPath, claudeConfig)
			fmt.Printf("[Claude Code] MCP server configured in %s\n", mcpConfigPath)
		}
	} else {
		fmt.Println("[Claude Code] Terminal/native mode: no aracne MCP server needed")
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	writeCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", descriptionBatchSize), autoYes)
	writeCommand(commandsDir, "descriptions-apply", "Write topology descriptions back into source files as doc comments", prompts.DescriptionsApplyCommand(), autoYes)
	writeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", prompts.DescriptionsClearCommand(), autoYes)
	writeCommand(commandsDir, "bug-hunter", "Launch a Bug Hunter sub-agent to scan the entire topology for bugs", bugHunterCommandForAgent(".claude/agents/bug-hunter.md"), autoYes)
	writeCommand(commandsDir, "bug-judge", "Triage pending bugs by launching Bug Judge sub-agents for each node", bugJudgeCommandForAgent(".claude/agents/bug-judge.md"), autoYes)
	writeCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", bugSolverCommandForAgent(".claude/agents/bug-solver.md"), autoYes)

	writeAgent(agentsDir, "descriptions-generation-executor", claudeAgentContent("descriptions-generation-executor", "Generates descriptions for one assigned batch of undocumented topology resources", ToolProfileDescriptionsExecutor, modes, readSplit, prompts.DescriptionsGenerationExecutorPrompt()), autoYes)
	writeAgent(agentsDir, "bug-hunter", claudeAgentContent("bug-hunter", "Scans the entire project topology looking for bugs", ToolProfileBugHunter, modes, readSplit, prompts.BugHunterPrompt()), autoYes)
	writeAgent(agentsDir, "bug-judge", claudeAgentContent("bug-judge", "Triages pending bugs by comparing against dismissed bug patterns", ToolProfileBugJudge, modes, readSplit, prompts.BugJudgePrompt()), autoYes)
	writeAgent(agentsDir, "bug-solver", claudeAgentContent("bug-solver", "Fixes acknowledged bugs in the codebase and removes them", ToolProfileBugSolver, modes, readSplit, prompts.BugSolverPrompt()), autoYes)

	if modes.Edit == helper.EditModeNative {
		settingsPath := filepath.Join(filepath.Dir(commandsDir), "settings.json")
		hooksDir := filepath.Join(filepath.Dir(commandsDir), "hooks")
		writeClaudeNativeEditHook(settingsPath, hooksDir, autoYes)
	}

	writeMarkdownIntegrationFile(claudeMdPath, "Claude Code CLAUDE.md", prompts.ClaudeMdContentForModes(modes, readSplit))
	fmt.Println("[Claude Code] Restart Claude Code to activate the topology workflow.")
}

func anyMCPMode(modes helper.ToolModes) bool {
	return modes.Read == helper.ReadModeMCP || modes.Edit == helper.EditModeMCP || modes.Other == helper.OtherModeMCP || modes.Grep == helper.GrepModeMCP
}

func nativePermission(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

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

func claudePaths(global bool) (string, string, string, string) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		return filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "commands"), filepath.Join(home, ".claude", "agents"), filepath.Join(home, ".claude", "CLAUDE.md")
	}
	return ".claude/.mcp.json", ".claude/commands", ".claude/agents", "CLAUDE.md"
}

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

func bugHunterCommandForAgent(agentRef string) string {
	return "Use the " + agentRef + " agent to scan the project topology for confirmed correctness, reliability, and security bugs. Report each confirmed bug with bug_report and summarize the count found."
}

func bugJudgeCommandForAgent(agentRef string) string {
	return "Use the " + agentRef + " agent to triage pending bugs. It must compare pending bugs with dismissed examples, then acknowledge real bugs, dismiss false positives, and delete duplicates."
}

func bugSolverCommandForAgent(agentRef string) string {
	return "Use the " + agentRef + " agent to fix acknowledged bugs. It must inspect each acknowledged bug, apply the minimal fix, and delete the bug report after the fix is complete."
}

func claudeAgentContent(name, description string, profile ToolProfile, modes helper.ToolModes, readSplit map[domain.ResourceKind]bool, prompt string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntools: %s\n%s---\n\n%s\n\n%s", name, description, strings.Join(claudeToolsForProfile(modes, profile, readSplit), ", "), claudeMCPServersFrontmatter(modes, profile), prompt, terminalGuidance(modes, profile, readSplit))
}

func openCodeAgentContent(name, description string, profile ToolProfile, modes helper.ToolModes, readSplit map[domain.ResourceKind]bool, prompt string) string {
	return fmt.Sprintf("---\ndescription: %s\nmode: subagent\npermission:\n%s---\n\n%s\n\n%s", description, openCodePermissions(modes, profile, readSplit), prompt, terminalGuidance(modes, profile, readSplit))
}

func claudeMCPServersFrontmatter(modes helper.ToolModes, profile ToolProfile) string {
	if !anyMCPMode(modes) {
		return ""
	}
	return fmt.Sprintf("mcpServers:\n  - aracne:\n      type: stdio\n      command: arac\n      args: [\"serve\", \"--tool-profile\", \"%s\"]\n", profile)
}

func claudeToolsForProfile(modes helper.ToolModes, profile ToolProfile, readSplit map[domain.ResourceKind]bool) []string {
	useSplit := len(readSplit) > 0 && profile != ToolProfileDescriptionsExecutor
	splitKinds := readSplit

	var result []string
	if modes.Read == helper.ReadModeNative {
		result = append(result, "Read")
	} else if modes.Read == helper.ReadModeMCP {
		if useSplit {
			if profileAllows(profile, "read_function") && (splitKinds[domain.ResourceFunction] || splitKinds[domain.ResourceMethod]) {
				result = append(result, "mcp__aracne__read_function")
			}
			if profileAllows(profile, "read_struct") && splitKinds[domain.ResourceType] {
				result = append(result, "mcp__aracne__read_struct")
			}
		} else if profileAllows(profile, "read") {
			result = append(result, "mcp__aracne__read")
		}
	}
	if modes.Edit == helper.EditModeNative {
		if profileAllows(profile, "edit") {
			result = append(result, "Edit")
		}
		if profileAllows(profile, "write") {
			result = append(result, "Write")
		}
	} else if modes.Edit == helper.EditModeMCP {
		for _, name := range []string{"edit", "write"} {
			if profileAllows(profile, name) {
				result = append(result, "mcp__aracne__"+name)
			}
		}
	}
	if modes.Other == helper.OtherModeMCP {
		for _, name := range profileTools(profile) {
			if name == "read" || name == "edit" || name == "write" || name == "grep" || name == "read_function" || name == "read_struct" {
				continue
			}
			result = append(result, "mcp__aracne__"+name)
		}
	}
	if modes.Grep == helper.GrepModeNative && profileAllows(profile, "grep") {
		result = append(result, "Grep")
	} else if modes.Grep == helper.GrepModeMCP && profileAllows(profile, "grep") {
		result = append(result, "mcp__aracne__grep")
	}
	if needsTerminal(modes) {
		result = append(result, "Bash")
	}
	return result
}

func openCodePermissions(modes helper.ToolModes, profile ToolProfile, readSplit map[domain.ResourceKind]bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  read: %s\n", nativePermission(modes.Read == helper.ReadModeNative)))
	canEditNatively := modes.Edit == helper.EditModeNative && (profileAllows(profile, "edit") || profileAllows(profile, "write"))
	b.WriteString(fmt.Sprintf("  edit: %s\n", nativePermission(canEditNatively)))
	if needsTerminal(modes) {
		b.WriteString("  bash: allow\n")
	}
	b.WriteString("  \"aracne_*\": deny\n")
	for _, toolName := range allowedMCPToolNames(modes, profile, readSplit) {
		b.WriteString(fmt.Sprintf("  \"aracne_%s\": allow\n", toolName))
	}
	return b.String()
}

func allowedMCPToolNames(modes helper.ToolModes, profile ToolProfile, readSplit map[domain.ResourceKind]bool) []string {
	useSplit := len(readSplit) > 0 && profile != ToolProfileDescriptionsExecutor
	splitKinds := readSplit

	var result []string
	for _, name := range profileTools(profile) {
		switch name {
		case "read":
			if modes.Read == helper.ReadModeMCP && !useSplit {
				result = append(result, name)
			}
		case "read_function":
			if modes.Read == helper.ReadModeMCP && useSplit && (splitKinds[domain.ResourceFunction] || splitKinds[domain.ResourceMethod]) {
				result = append(result, name)
			}
		case "read_struct":
			if modes.Read == helper.ReadModeMCP && useSplit && splitKinds[domain.ResourceType] {
				result = append(result, name)
			}
		case "edit", "write":
			if modes.Edit == helper.EditModeMCP {
				result = append(result, name)
			}
		case "grep":
			if modes.Grep == helper.GrepModeMCP {
				result = append(result, name)
			}
		default:
			if modes.Other == helper.OtherModeMCP {
				result = append(result, name)
			}
		}
	}
	return result
}

func profileAllows(profile ToolProfile, toolName string) bool {
	for _, name := range profileTools(profile) {
		if name == toolName {
			return true
		}
	}
	return false
}

func needsTerminal(modes helper.ToolModes) bool {
	return modes.Read == helper.ReadModeTerminal || modes.Edit == helper.EditModeTerminal || modes.Other == helper.OtherModeTerminal || modes.Grep == helper.GrepModeTerminal
}

func terminalGuidance(modes helper.ToolModes, profile ToolProfile, readSplit map[domain.ResourceKind]bool) string {
	if !needsTerminal(modes) {
		return ""
	}
	useSplit := len(readSplit) > 0 && profile != ToolProfileDescriptionsExecutor
	var lines []string
	for _, name := range profileTools(profile) {
		switch name {
		case "read":
			if modes.Read == helper.ReadModeTerminal && !useSplit {
				lines = append(lines, "- `arac read <resource-id>` to read resources or files")
			}
		case "read_function":
			if modes.Read == helper.ReadModeTerminal && useSplit {
				lines = append(lines, "- `arac read <resource-id>` to read a function")
			}
		case "read_struct":
			if modes.Read == helper.ReadModeTerminal && useSplit {
				lines = append(lines, "- `arac read <resource-id>` to read a struct/type")
			}
		case "edit":
			if modes.Edit == helper.EditModeTerminal {
				lines = append(lines, "- `arac edit` with JSON stdin for exact string replacement")
			}
		case "write":
			if modes.Edit == helper.EditModeTerminal {
				lines = append(lines, "- `arac write` with JSON stdin for file writes")
			}
		case "grep":
			if modes.Grep == helper.GrepModeTerminal {
				lines = append(lines, "- `arac grep <pattern> [path]` to search file contents with topology resource metadata")
			}
		default:
			if modes.Other == helper.OtherModeTerminal {
				lines = append(lines, "- `"+terminalCommandForTool(name)+"`")
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Terminal arac Commands\n\nUse only these arac commands for terminal-mode topology operations:\n" + strings.Join(lines, "\n") + "\n"
}

func terminalCommandForTool(name string) string {
	switch name {
	case "read_function":
		return "arac read <resource-id>"
	case "read_struct":
		return "arac read <resource-id>"
	case "read":
		return "arac read <resource-id>"
	case "grep":
		return "arac grep <pattern> [path]"
	case "warnings_list":
		return "arac warnings list"
	case "bug_report":
		return "arac bug report --node <id> --description <text>"
	case "bug_list":
		return "arac bug list [--node <id>] [--state <state>]"
	case "bug_acknowledge":
		return "arac bug acknowledge <bugID>"
	case "bug_dismiss":
		return "arac bug dismiss <bugID>"
	case "bug_delete":
		return "arac bug delete <bugID>"
	case "node_list_no_description":
		return "arac resource list --no-description"
	case "update_description":
		return "arac update-description <id> <kind> <desc>"
	default:
		return "Aracne " + name
	}
}

func writeCommand(dir, name, description, template string, autoYes bool) {
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template), autoYes)
}

func writeOpenCodePrimaryCommand(dir, name, description, agentName, template string, autoYes bool) {
	content := fmt.Sprintf("---\ndescription: %s\nagent: %s\n---\n\n%s\n", description, agentName, template)
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, content, autoYes)
}

func writeOpenCodeCommand(dir, name, description, agentName, template string, autoYes bool) {
	content := fmt.Sprintf("---\ndescription: %s\nagent: %s\nsubtask: true\n---\n\n%s\n", description, agentName, template)
	writeMarkdownFile(filepath.Join(dir, name+".md"), "command "+name, content, autoYes)
}

func writeAgent(dir, name, content string, autoYes bool) {
	writeMarkdownFile(filepath.Join(dir, name+".md"), "agent "+name, content, autoYes)
}

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
	AracIntegrationStart = "# Aracne Project Integration"
	AracIntegrationEnd   = "Good Luck in your task."
)

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

func updateMarkdownIntegrationSegment(existing, segment string) string {
	lineEnding := markdownLineEnding(existing)
	segment = normalizeMarkdownSegment(segment, lineEnding)
	if strings.TrimSpace(existing) == "" {
		return segment
	}

	start := findMarkdownLine(existing, AracIntegrationStart, 0)
	if start >= 0 {
		end := findMarkdownLine(existing, AracIntegrationEnd, start)
		if end >= 0 {
			end += len(AracIntegrationEnd)
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

func normalizeMarkdownSegment(segment, lineEnding string) string {
	segment = strings.TrimSpace(segment)
	segment = strings.ReplaceAll(segment, "\r\n", "\n")
	segment = strings.ReplaceAll(segment, "\r", "\n")
	if lineEnding != "\n" {
		segment = strings.ReplaceAll(segment, "\n", lineEnding)
	}
	return segment + lineEnding
}

func markdownLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func hasTrailingBlankLine(content string) bool {
	return strings.HasSuffix(content, "\n\n") || strings.HasSuffix(content, "\r\n\r\n")
}

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

func lineAt(content string, pos int) string {
	line, _ := nextMarkdownLine(content, pos)
	return line
}

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

func markdownLineIsHeading(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
}

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

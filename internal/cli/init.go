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
	yes := fs.Bool("y", false, "Auto-confirm all replacement prompts")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))

	if *opencode {
		initOpenCode(*global, cfg, *yes)
	}
	if *claude {
		initClaudeCode(*global, cfg, *yes)
	}
}

func initOpenCode(global bool, cfg *helper.Config, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)
	os.MkdirAll(configDir, 0755)
	mainEff := cfg.EffectiveAgent("opencode", "main")

	config := readJSONConfig(configPath)
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

	permissionMap, _ := config["permission"].(map[string]interface{})
	if permissionMap == nil {
		permissionMap = make(map[string]interface{})
	}
	blocked := toolNameSet(mainEff.BlockedTools)
	permissionMap["read"] = nativePermission(!blocked["read"])
	permissionMap["edit"] = nativePermission(!blocked["edit"] && !blocked["write"])
	permissionMap["bash"] = nativePermission(!blocked["bash"])
	delete(permissionMap, "write")
	permissionMap["aracne_*"] = "deny"
	for _, toolName := range mainEff.MCPTools {
		permissionMap["aracne_"+toolName] = "allow"
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
	writeOpenCodeCommand(commandsDir, "bug-hunter", "Launch a Bug Hunter sub-agent to scan the entire topology for bugs", "bug-hunter", bugHunterCommandForAgent("bug-hunter"), autoYes)
	writeOpenCodeCommand(commandsDir, "bug-judge", "Triage pending bugs by launching Bug Judge sub-agents for each node", "bug-judge", bugJudgeCommandForAgent("bug-judge"), autoYes)
	writeOpenCodeCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", "bug-solver", bugSolverCommandForAgent("bug-solver"), autoYes)

	writeAgent(agentsDir, "descriptions-generation-executor", openCodeAgentContent("Generates descriptions for one assigned batch of undocumented topology resources", cfg.EffectiveAgent("opencode", "descriptions-generation-executor"), prompts.DescriptionsGenerationExecutorPrompt()), autoYes)
	writeAgent(agentsDir, "bug-hunter", openCodeAgentContent("Scans the entire project topology looking for bugs", cfg.EffectiveAgent("opencode", "bug-hunter"), prompts.BugHunterPrompt()), autoYes)
	writeAgent(agentsDir, "bug-judge", openCodeAgentContent("Triages pending bugs by comparing against dismissed bug patterns", cfg.EffectiveAgent("opencode", "bug-judge"), prompts.BugJudgePrompt()), autoYes)
	writeAgent(agentsDir, "bug-solver", openCodeAgentContent("Fixes acknowledged bugs in the codebase and removes them", cfg.EffectiveAgent("opencode", "bug-solver"), prompts.BugSolverPrompt()), autoYes)

	writeOpenCodePlugins(mainEff.Plugins, configDir, autoYes)
	writeMarkdownIntegrationFile(agentsMdPath, "OpenCode AGENTS.md", prompts.AgentsMdContentForAgent(mainEff))
	fmt.Println("[OpenCode] Restart OpenCode to activate the topology workflow.")
}

func initClaudeCode(global bool, cfg *helper.Config, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)
	claudeBaseDir := filepath.Dir(commandsDir)
	mainEff := cfg.EffectiveAgent("claude_code", "main")

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

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	batchSize := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", helper.DefaultDescriptionBatchSize)
	writeCommand(commandsDir, "descriptions-generate", "Generate descriptions for undocumented resources in the topology", prompts.DescriptionsGenerateCommand("descriptions-generation-executor", batchSize), autoYes)
	writeCommand(commandsDir, "descriptions-apply", "Write topology descriptions back into source files as doc comments", prompts.DescriptionsApplyCommand(), autoYes)
	writeCommand(commandsDir, "descriptions_clear", "Clear stored topology descriptions", prompts.DescriptionsClearCommand(), autoYes)
	writeCommand(commandsDir, "bug-hunter", "Launch a Bug Hunter sub-agent to scan the entire topology for bugs", bugHunterCommandForAgent(".claude/agents/bug-hunter.md"), autoYes)
	writeCommand(commandsDir, "bug-judge", "Triage pending bugs by launching Bug Judge sub-agents for each node", bugJudgeCommandForAgent(".claude/agents/bug-judge.md"), autoYes)
	writeCommand(commandsDir, "bug-solver", "Fix acknowledged bugs by launching Bug Solver sub-agents", bugSolverCommandForAgent(".claude/agents/bug-solver.md"), autoYes)

	writeAgent(agentsDir, "descriptions-generation-executor", claudeAgentContent("descriptions-generation-executor", "Generates descriptions for one assigned batch of undocumented topology resources", cfg.EffectiveAgent("claude_code", "descriptions-generation-executor"), prompts.DescriptionsGenerationExecutorPrompt()), autoYes)
	writeAgent(agentsDir, "bug-hunter", claudeAgentContent("bug-hunter", "Scans the entire project topology looking for bugs", cfg.EffectiveAgent("claude_code", "bug-hunter"), prompts.BugHunterPrompt()), autoYes)
	writeAgent(agentsDir, "bug-judge", claudeAgentContent("bug-judge", "Triages pending bugs by comparing against dismissed bug patterns", cfg.EffectiveAgent("claude_code", "bug-judge"), prompts.BugJudgePrompt()), autoYes)
	writeAgent(agentsDir, "bug-solver", claudeAgentContent("bug-solver", "Fixes acknowledged bugs in the codebase and removes them", cfg.EffectiveAgent("claude_code", "bug-solver"), prompts.BugSolverPrompt()), autoYes)

	writeClaudePlugins(mainEff.Plugins, claudeBaseDir, autoYes)
	writeMarkdownIntegrationFile(claudeMdPath, "Claude Code CLAUDE.md", prompts.ClaudeMdContentForAgent(mainEff))
	fmt.Println("[Claude Code] Restart Claude Code to activate the topology workflow.")
}

func nativePermission(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func toolNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
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
	return ".mcp.json", ".claude/commands", ".claude/agents", "CLAUDE.md"
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

func claudeAgentContent(name, description string, eff helper.AgentConfig, prompt string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntools: %s\n%s---\n\n%s\n", name, description, strings.Join(claudeToolsForAgent(eff), ", "), claudeMCPServersFrontmatter(name), prompt)
}

func openCodeAgentContent(description string, eff helper.AgentConfig, prompt string) string {
	return fmt.Sprintf("---\ndescription: %s\nmode: subagent\npermission:\n%s---\n\n%s\n", description, openCodePermissionsForAgent(eff), prompt)
}

func claudeMCPServersFrontmatter(agentName string) string {
	return fmt.Sprintf("mcpServers:\n  - aracne:\n      type: stdio\n      command: arac\n      args: [\"serve\", \"--tool-profile\", \"%s\", \"--harness\", \"claude_code\"]\n", agentName)
}

// claudeToolsForAgent builds the Claude `tools:` allow-list: the agent's MCP
// tools (prefixed) plus each native tool not present in blocked_tools.
func claudeToolsForAgent(eff helper.AgentConfig) []string {
	var result []string
	for _, name := range eff.MCPTools {
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
// MCP tools.
func openCodePermissionsForAgent(eff helper.AgentConfig) string {
	blocked := toolNameSet(eff.BlockedTools)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  read: %s\n", nativePermission(!blocked["read"])))
	b.WriteString(fmt.Sprintf("  edit: %s\n", nativePermission(!blocked["edit"] && !blocked["write"])))
	b.WriteString(fmt.Sprintf("  bash: %s\n", nativePermission(!blocked["bash"])))
	b.WriteString("  \"aracne_*\": deny\n")
	for _, toolName := range eff.MCPTools {
		b.WriteString(fmt.Sprintf("  \"aracne_%s\": allow\n", toolName))
	}
	return b.String()
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

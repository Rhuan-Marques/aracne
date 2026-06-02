package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llm-topology/internal/helper"
	"llm-topology/internal/prompts"
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
	cliMode := fs.String("cli-mode", "", "CLI function mode: mcp or terminal (overrides .ltp/config.json)")
	yes := fs.Bool("y", false, "Auto-confirm all replacement prompts")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	cfgPath := helper.ConfigPath(".ltp/topology.db")
	cfg := helper.EnsureConfig(cfgPath)

	if *cliMode != "" {
		switch *cliMode {
		case "mcp":
			cfg.CliFunctionMode = helper.CliModeMCP
		case "terminal":
			cfg.CliFunctionMode = helper.CliModeTerminal
		default:
			fmt.Fprintf(os.Stderr, "Invalid --cli-mode: %s (must be 'mcp' or 'terminal')\n", *cliMode)
			os.Exit(1)
		}
		helper.SaveConfig(cfg, cfgPath)
	}

	if *opencode {
		initOpenCode(*global, cfg.CliFunctionMode, *yes)
	}
	if *claude {
		initClaudeCode(*global, cfg.CliFunctionMode, *yes)
	}
}

func initOpenCode(global bool, mode helper.CliFunctionMode, autoYes bool) {
	var configPath string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		configPath = filepath.Join(home, ".config", "opencode", "opencode.json")
		os.MkdirAll(filepath.Dir(configPath), 0755)
	} else {
		configPath = ".opencode/opencode.json"
		os.MkdirAll(".opencode", 0755)
	}

	if mode == helper.CliModeTerminal {
		initOpenCodeTerminal(configPath, global, autoYes)
		return
	}

	var config map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", configPath, err)
			os.Exit(1)
		}
	}
	if config == nil {
		config = make(map[string]interface{})
	}

	shouldWrite := true
	if _, exists := config["mcp"]; exists {
		if autoYes || promptReplaceConfigExists(configPath) {
			fmt.Printf("[OpenCode] Overwriting %s\n", configPath)
		} else {
			fmt.Printf("[OpenCode] Skipping %s\n", configPath)
			shouldWrite = false
		}
	}

	if shouldWrite {
		mcpMap, _ := config["mcp"].(map[string]interface{})
		if mcpMap == nil {
			mcpMap = make(map[string]interface{})
		}
		mcpMap["llm-topology"] = map[string]interface{}{
			"type":    "local",
			"command": []string{"ltp", "serve"},
			"enabled": true,
		}
		config["mcp"] = mcpMap
		permissionMap, _ := config["permission"].(map[string]interface{})
		if permissionMap == nil {
			permissionMap = make(map[string]interface{})
		}
		for _, toolName := range []string{"read", "edit", "write"} {
			if _, exists := permissionMap[toolName]; !exists {
				permissionMap[toolName] = "deny"
			}
		}
		config["permission"] = permissionMap

		out, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
			os.Exit(1)
		}
		out = append(out, '\n')

		if err := os.WriteFile(configPath, out, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", configPath, err)
			os.Exit(1)
		}
		fmt.Printf("[OpenCode] llm-topology MCP server configured in %s\n", configPath)
	}

	var commandsDir string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		commandsDir = filepath.Join(home, ".config", "opencode", "commands")
	} else {
		commandsDir = ".opencode/commands"
	}
	os.MkdirAll(commandsDir, 0755)

	writeCommand(commandsDir, "descriptions-generate",
		"Generate descriptions for undocumented resources in the topology",
		prompts.DescriptionsGenerateCommand(), autoYes)

	writeCommand(commandsDir, "descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		prompts.DescriptionsApplyCommand(), autoYes)

	writeCommand(commandsDir, "bug-hunter",
		"Launch a Bug Hunter sub-agent to scan the entire topology for bugs",
		prompts.BugHunterCommand(), autoYes)

	writeCommand(commandsDir, "bug-judge",
		"Triage pending bugs by launching Bug Judge sub-agents for each node",
		prompts.BugJudgeCommand(), autoYes)

	writeCommand(commandsDir, "bug-solver",
		"Fix acknowledged bugs by launching Bug Solver sub-agents",
		prompts.BugSolverCommand(), autoYes)

	fmt.Println("[OpenCode] Restart OpenCode to activate the topology tools.")
}

func initOpenCodeTerminal(configPath string, global bool, autoYes bool) {
	fmt.Printf("[OpenCode] Terminal mode: skipping MCP server setup\n")

	var agentsMdPath string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		agentsMdPath = filepath.Join(home, ".config", "opencode", "AGENTS.md")
	} else {
		agentsMdPath = "AGENTS.md"
	}

	if _, err := os.Stat(agentsMdPath); err == nil {
		if !autoYes && !promptReplace(agentsMdPath) {
			fmt.Printf("[OpenCode] Skipping AGENTS.md (terminal instructions already present)\n")
			return
		}
		fmt.Printf("[OpenCode] Overwriting AGENTS.md with terminal navigation instructions\n")
	} else {
		fmt.Printf("[OpenCode] Writing AGENTS.md with terminal navigation instructions\n")
	}

	content := prompts.TerminalClaudeMdContent()
	if err := os.WriteFile(agentsMdPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", agentsMdPath, err)
		os.Exit(1)
	}
	fmt.Printf("[OpenCode] Terminal navigation instructions written to %s\n", agentsMdPath)
}

func initClaudeCode(global bool, mode helper.CliFunctionMode, autoYes bool) {
	var mcpConfigPath string
	var commandsDir string
	var agentsDir string
	var claudeMdPath string

	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		mcpConfigPath = filepath.Join(home, ".claude.json")
		commandsDir = filepath.Join(home, ".claude", "commands")
		agentsDir = filepath.Join(home, ".claude", "agents")
		claudeMdPath = filepath.Join(home, ".claude", "CLAUDE.md")
	} else {
		mcpConfigPath = ".claude/.mcp.json"
		commandsDir = ".claude/commands"
		agentsDir = ".claude/agents"
		claudeMdPath = "CLAUDE.md"
	}

	if mode == helper.CliModeTerminal {
		initClaudeCodeTerminal(claudeMdPath, global, autoYes)
		return
	}

	var claudeConfig map[string]interface{}
	data, err := os.ReadFile(mcpConfigPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &claudeConfig); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", mcpConfigPath, err)
			os.Exit(1)
		}
	}
	if claudeConfig == nil {
		claudeConfig = make(map[string]interface{})
	}

	shouldWrite := true
	if _, exists := claudeConfig["mcpServers"]; exists {
		if autoYes || promptReplaceConfigExists(mcpConfigPath) {
			fmt.Printf("[Claude Code] Overwriting %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] Skipping %s\n", mcpConfigPath)
			shouldWrite = false
		}
	}

	if shouldWrite {
		mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
		if mcpServers == nil {
			mcpServers = make(map[string]interface{})
		}
		mcpServers["llm-topology"] = map[string]interface{}{
			"command": "ltp",
			"args":    []string{"serve"},
		}
		claudeConfig["mcpServers"] = mcpServers

		out, err := json.MarshalIndent(claudeConfig, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
			os.Exit(1)
		}
		out = append(out, '\n')
		if err := os.WriteFile(mcpConfigPath, out, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", mcpConfigPath, err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] MCP server configured in %s\n", mcpConfigPath)
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	writeCommand(commandsDir, "descriptions-generate",
		"Generate descriptions for undocumented resources in the topology",
		prompts.DescriptionsGenerateCommand(), autoYes)

	writeCommand(commandsDir, "descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		prompts.DescriptionsApplyCommand(), autoYes)

	writeCommand(commandsDir, "bug-hunter",
		"Launch a Bug Hunter sub-agent to scan the entire topology for bugs",
		prompts.BugHunterCommand(), autoYes)

	writeCommand(commandsDir, "bug-judge",
		"Triage pending bugs by launching Bug Judge sub-agents for each node",
		prompts.BugJudgeCommand(), autoYes)

	writeCommand(commandsDir, "bug-solver",
		"Fix acknowledged bugs by launching Bug Solver sub-agents",
		prompts.BugSolverCommand(), autoYes)

	writeAgent(agentsDir, "describe", prompts.DescribeAgentContent(), autoYes)
	writeAgent(agentsDir, "bug-hunter", prompts.BugHunterAgentContent(), autoYes)
	writeAgent(agentsDir, "bug-judge", prompts.BugJudgeAgentContent(), autoYes)
	writeAgent(agentsDir, "bug-solver", prompts.BugSolverAgentContent(), autoYes)

	if _, err := os.Stat(claudeMdPath); err == nil {
		if autoYes || promptReplace(claudeMdPath) {
			fmt.Printf("[Claude Code] Overwriting %s\n", claudeMdPath)
		} else {
			fmt.Printf("[Claude Code] Skipping %s (already exists)\n", claudeMdPath)
			fmt.Println("[Claude Code] Restart Claude Code to activate the topology tools.")
			return
		}
	}

	if err := os.WriteFile(claudeMdPath, []byte(prompts.ClaudeMdContent()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CLAUDE.md: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Claude Code] CLAUDE.md written to %s\n", claudeMdPath)

	fmt.Println("[Claude Code] Restart Claude Code to activate the topology tools.")
}

func initClaudeCodeTerminal(claudeMdPath string, global bool, autoYes bool) {
	fmt.Printf("[Claude Code] Terminal mode: skipping MCP server setup\n")

	if _, err := os.Stat(claudeMdPath); err == nil {
		if !autoYes && !promptReplace(claudeMdPath) {
			fmt.Printf("[Claude Code] Skipping CLAUDE.md (terminal instructions already present)\n")
			return
		}
		fmt.Printf("[Claude Code] Overwriting CLAUDE.md with terminal navigation instructions\n")
	} else {
		fmt.Printf("[Claude Code] Writing CLAUDE.md with terminal navigation instructions\n")
	}

	content := prompts.TerminalClaudeMdContent()
	if err := os.WriteFile(claudeMdPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", claudeMdPath, err)
		os.Exit(1)
	}
	fmt.Printf("[Claude Code] Terminal navigation instructions written to %s\n", claudeMdPath)
}

func writeCommand(dir, name, description, template string, autoYes bool) {
	cmdPath := filepath.Join(dir, name+".md")
	if _, err := os.Stat(cmdPath); err == nil {
		if autoYes || promptReplace(cmdPath) {
			fmt.Printf("Overwriting command %s at %s\n", name, cmdPath)
		} else {
			fmt.Printf("Command %s already present at %s, skipping\n", name, cmdPath)
			return
		}
	}
	content := fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template)
	if err := os.WriteFile(cmdPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing command %s: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("Command %s written to %s\n", name, cmdPath)
}

func writeAgent(dir, name, content string, autoYes bool) {
	agentPath := filepath.Join(dir, name+".md")
	if _, err := os.Stat(agentPath); err == nil {
		if autoYes || promptReplace(agentPath) {
			fmt.Printf("Overwriting agent %s at %s\n", name, agentPath)
		} else {
			fmt.Printf("Agent %s already present at %s, skipping\n", name, agentPath)
			return
		}
	}
	if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing agent %s: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("Agent %s written to %s\n", name, agentPath)
}

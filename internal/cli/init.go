package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"llm-topology/internal/prompts"
)

func RunInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Initialize Claude Code integration")
	opencode := fs.Bool("opencode", false, "Initialize OpenCode integration")
	global := fs.Bool("global", false, "Install globally")
	fs.Parse(args)

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	if *opencode {
		InitOpenCode(*global)
	}
	if *claude {
		InitClaudeCode(*global)
	}
}

func InitOpenCode(global bool) {
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

	mcpMap, _ := config["mcp"].(map[string]interface{})
	if mcpMap == nil {
		mcpMap = make(map[string]interface{})
	}

	if _, exists := mcpMap["llm-topology"]; !exists {
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
		for _, toolName := range []string{"read", "edit"} {
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
	} else {
		fmt.Printf("[OpenCode] llm-topology MCP config already present in %s\n", configPath)
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
		prompts.DescriptionsGenerateCommand())

	writeCommand(commandsDir, "descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		prompts.DescriptionsApplyCommand())

	fmt.Println("[OpenCode] Restart OpenCode to activate the topology tools.")
}

func InitClaudeCode(global bool) {
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
		mcpConfigPath = ".mcp.json"
		commandsDir = ".claude/commands"
		agentsDir = ".claude/agents"
		claudeMdPath = "CLAUDE.md"
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

	if global {
		mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
		if mcpServers == nil {
			mcpServers = make(map[string]interface{})
		}
		if _, exists := mcpServers["llm-topology"]; !exists {
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
			fmt.Printf("[Claude Code] Global MCP server configured in %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] Global MCP config already present in %s\n", mcpConfigPath)
		}
	} else {
		mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
		if mcpServers == nil {
			mcpServers = make(map[string]interface{})
		}
		if _, exists := mcpServers["llm-topology"]; !exists {
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
			fmt.Printf("[Claude Code] Project MCP server configured in %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] MCP config already present in %s\n", mcpConfigPath)
		}
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	writeCommand(commandsDir, "descriptions-generate",
		"Generate descriptions for undocumented resources in the topology",
		prompts.DescriptionsGenerateCommand())

	writeCommand(commandsDir, "descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		prompts.DescriptionsApplyCommand())

	agentPath := filepath.Join(agentsDir, "describe.md")
	if _, err := os.Stat(agentPath); err == nil {
		fmt.Printf("[Claude Code] Agent describe already present at %s\n", agentPath)
	} else {
		if err := os.WriteFile(agentPath, []byte(prompts.DescribeAgentContent()), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing agent: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] Agent describe written to %s\n", agentPath)
	}

	if _, err := os.Stat(claudeMdPath); err == nil {
		fmt.Printf("[Claude Code] CLAUDE.md already present at %s\n", claudeMdPath)
	} else {
		if err := os.WriteFile(claudeMdPath, []byte(prompts.ClaudeMdContent()), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing CLAUDE.md: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] CLAUDE.md written to %s\n", claudeMdPath)
	}

	fmt.Println("[Claude Code] Restart Claude Code to activate the topology tools.")
}

func writeCommand(dir, name, description, template string) {
	cmdPath := filepath.Join(dir, name+".md")
	if _, err := os.Stat(cmdPath); err == nil {
		fmt.Printf("Command %s already present at %s\n", name, cmdPath)
		return
	}
	content := fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template)
	if err := os.WriteFile(cmdPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing command %s: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("Command %s written to %s\n", name, cmdPath)
}
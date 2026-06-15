package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func RunDisable(args []string) {
	fs := flag.NewFlagSet("disable", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Disable Claude Code integration")
	opencode := fs.Bool("opencode", false, "Disable OpenCode integration")
	global := fs.Bool("global", false, "Disable globally")
	all := fs.Bool("all", false, "Disable all integrations")
	yes := fs.Bool("y", false, "Auto-confirm all prompts")
	fs.Parse(args)

	if !*claude && !*opencode && !*all {
		*claude = true
		*opencode = true
	}
	if *all {
		*claude = true
		*opencode = true
	}

	if *opencode {
		disableOpenCode(*global, *yes)
	}
	if *claude {
		disableClaudeCode(*global, *yes)
	}
}

func disableOpenCode(global bool, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)

	config := readJSONConfig(configPath)
	changed := false

	if mcpMap, ok := config["mcp"].(map[string]interface{}); ok {
		if _, exists := mcpMap["arac"]; exists {
			delete(mcpMap, "arac")
			changed = true
			fmt.Println("[OpenCode] Removed arac MCP server from config")
		}
		if len(mcpMap) == 0 {
			delete(config, "mcp")
		} else {
			config["mcp"] = mcpMap
		}
	}

	permission := map[string]interface{}{
		"read":  "allow",
		"edit":  "allow",
		"bash":  "allow",
		"write": "allow",
		"grep":  "allow",
	}
	config["permission"] = permission
	changed = true

	if changed {
		writeJSONConfig(configPath, config)
		fmt.Printf("[OpenCode] Config updated at %s\n", configPath)
	}

	removeFiles(filepath.Join(configDir, "commands"), []string{
		"descriptions-generate.md",
		"descriptions-apply.md",
		"descriptions_clear.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFiles(filepath.Join(configDir, "agents"), []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFile(filepath.Join(configDir, "plugins", "arac-native-edit-sync.js"),
		"OpenCode native edit sync plugin")

	removeAracneIntegrationSection(agentsMdPath, "OpenCode AGENTS.md")

	fmt.Println("[OpenCode] Aracne integration disabled. Restart OpenCode to apply changes.")
}

func disableClaudeCode(global bool, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)

	config := readJSONConfig(mcpConfigPath)
	changed := false

	if mcpServers, ok := config["mcpServers"].(map[string]interface{}); ok {
		if _, exists := mcpServers["arac"]; exists {
			delete(mcpServers, "arac")
			changed = true
			fmt.Println("[Claude Code] Removed arac MCP server from config")
		}
		if len(mcpServers) == 0 {
			delete(config, "mcpServers")
		} else {
			config["mcpServers"] = mcpServers
		}
	}

	if changed {
		writeJSONConfig(mcpConfigPath, config)
		fmt.Printf("[Claude Code] Config updated at %s\n", mcpConfigPath)
	}

	removeFiles(commandsDir, []string{
		"descriptions-generate.md",
		"descriptions-apply.md",
		"descriptions_clear.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFiles(agentsDir, []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	hooksDir := filepath.Join(filepath.Dir(commandsDir), "hooks")
	removeFiles(hooksDir, []string{
		"arac-update-file.ps1",
		"arac-update-file.sh",
	})

	settingsPath := filepath.Join(filepath.Dir(commandsDir), "settings.json")
	removeAracneHookFromSettings(settingsPath)

	removeAracneIntegrationSection(claudeMdPath, "Claude Code CLAUDE.md")

	fmt.Println("[Claude Code] Aracne integration disabled. Restart Claude Code to apply changes.")
}

func removeFiles(dir string, names []string) {
	for _, name := range names {
		removeFile(filepath.Join(dir, name), name)
	}
}

func removeFile(path, label string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", label, err)
		return
	}
	fmt.Printf("Removed %s\n", path)
}

func removeAracneHookFromSettings(settingsPath string) {
	config := readJSONConfig(settingsPath)
	if len(config) == 0 {
		return
	}

	hooks, ok := config["hooks"].(map[string]interface{})
	if !ok {
		return
	}

	postToolUse, ok := hooks["PostToolUse"].([]interface{})
	if !ok {
		return
	}

	filtered := make([]interface{}, 0, len(postToolUse))
	changed := false
	for _, entry := range postToolUse {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			filtered = append(filtered, entry)
			continue
		}
		matcher, _ := entryMap["matcher"].(string)
		if !strings.Contains(matcher, "Edit|Write|MultiEdit") {
			filtered = append(filtered, entry)
			continue
		}
		hooksList, ok := entryMap["hooks"].([]interface{})
		if !ok {
			filtered = append(filtered, entry)
			continue
		}
		isAracne := false
		for _, h := range hooksList {
			hMap, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			cmd, _ := hMap["command"].(string)
			if strings.Contains(cmd, "arac") {
				isAracne = true
				break
			}
		}
		if isAracne {
			changed = true
			fmt.Println("[Claude Code] Removed aracne PostToolUse hook from settings")
			continue
		}
		filtered = append(filtered, entry)
	}

	if !changed {
		return
	}

	if len(filtered) == 0 {
		delete(hooks, "PostToolUse")
	} else {
		hooks["PostToolUse"] = filtered
	}
	if len(hooks) == 0 {
		delete(config, "hooks")
	} else {
		config["hooks"] = hooks
	}
	writeJSONConfig(settingsPath, config)
	fmt.Printf("[Claude Code] Settings updated at %s\n", settingsPath)
}

func removeAracneIntegrationSection(path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		return
	}

	existing := string(data)
	updated := stripAracneIntegrationSegment(existing)

	if updated == existing {
		fmt.Printf("%s already clean at %s\n", label, path)
		return
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
		return
	}
	fmt.Printf("%s updated at %s\n", label, path)
}

func stripAracneIntegrationSegment(content string) string {
	lineEnding := markdownLineEnding(content)

	start := findMarkdownLine(content, AracIntegrationStart, 0)
	if start < 0 {
		return content
	}

	end := findMarkdownLine(content, AracIntegrationEnd, start)
	if end < 0 {
		return content
	}

	endAfter := end + len(AracIntegrationEnd)
	if endAfter < len(content) {
		if strings.HasPrefix(content[endAfter:], "\r\n") {
			endAfter += 2
		} else if strings.HasPrefix(content[endAfter:], "\n") {
			endAfter++
		}
	}

	for endAfter < len(content) {
		ch := content[endAfter]
		if ch == '\n' || ch == '\r' {
			endAfter++
			if ch == '\r' && endAfter < len(content) && content[endAfter] == '\n' {
				endAfter++
			}
		} else {
			break
		}
	}

	beforeSection := strings.TrimRight(content[:start], " \t\r\n")
	if beforeSection != "" {
		beforeSection += lineEnding
		if endAfter < len(content) {
			beforeSection += lineEnding
		}
	}

	return beforeSection + content[endAfter:]
}

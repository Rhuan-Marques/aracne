package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Disables Claude Code and/or OpenCode integrations either globally or per-project.
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

// Removes aracne MCP server and integration from OpenCode configuration, resets permissions, and deletes related files.
func disableOpenCode(global bool, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)

	config := readJSONConfig(configPath)
	changed := false

	if mcpMap, ok := config["mcp"].(map[string]interface{}); ok {
		removed := false
		for _, key := range []string{"aracne", "arac"} {
			if _, exists := mcpMap[key]; exists {
				delete(mcpMap, key)
				removed = true
			}
		}
		if removed {
			changed = true
			fmt.Println("[OpenCode] Removed aracne MCP server from config")
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

// Removes aracne MCP server and integration from Claude Code configuration and deletes related command/agent files.
func disableClaudeCode(global bool, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)

	config := readJSONConfig(mcpConfigPath)
	changed := false

	if mcpServers, ok := config["mcpServers"].(map[string]interface{}); ok {
		removed := false
		for _, key := range []string{"aracne", "arac"} {
			if _, exists := mcpServers[key]; exists {
				delete(mcpServers, key)
				removed = true
			}
		}
		if removed {
			changed = true
			fmt.Println("[Claude Code] Removed aracne MCP server from config")
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
		"arac-guard.ps1",
		"arac-guard.sh",
	})

	settingsPath := filepath.Join(filepath.Dir(commandsDir), "settings.json")
	removeAracneHookFromSettings(settingsPath)

	removeAracneIntegrationSection(claudeMdPath, "Claude Code CLAUDE.md")

	fmt.Println("[Claude Code] Aracne integration disabled. Restart Claude Code to apply changes.")
}

// Removes multiple files by name from a directory.
func removeFiles(dir string, names []string) {
	for _, name := range names {
		removeFile(filepath.Join(dir, name), name)
	}
}

// Removes a file if it exists, logging success or errors to stderr.
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

// Removes aracne hook entries from PreToolUse/PostToolUse events in settings JSON.
func removeAracneHookFromSettings(settingsPath string) {
	config := readJSONConfig(settingsPath)
	if len(config) == 0 {
		return
	}

	hooks, ok := config["hooks"].(map[string]interface{})
	if !ok {
		return
	}

	changed := false
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		if removeAracneHookEntries(hooks, event) {
			changed = true
		}
	}

	if !changed {
		return
	}
	if len(hooks) == 0 {
		delete(config, "hooks")
	} else {
		config["hooks"] = hooks
	}
	writeJSONConfig(settingsPath, config)
	fmt.Printf("[Claude Code] Settings updated at %s\n", settingsPath)
}

// removeAracneHookEntries strips every aracne-installed entry (one whose hook
// command references `arac`) from a hook-event list, covering both the
// edit-sync and guard hooks while preserving any user entries. It reports
// whether anything was removed.
func removeAracneHookEntries(hooks map[string]interface{}, event string) bool {
	entries, ok := hooks[event].([]interface{})
	if !ok {
		return false
	}
	filtered := make([]interface{}, 0, len(entries))
	changed := false
	for _, entry := range entries {
		if isAracneHookEntry(entry) {
			changed = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !changed {
		return false
	}
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
	fmt.Printf("[Claude Code] Removed aracne %s hook from settings\n", event)
	return true
}

// isAracneHookEntry reports whether a hook entry was installed by aracne,
// identified by an `arac` reference in any of its hook commands.
func isAracneHookEntry(entry interface{}) bool {
	entryMap, ok := entry.(map[string]interface{})
	if !ok {
		return false
	}
	hooksList, _ := entryMap["hooks"].([]interface{})
	for _, h := range hooksList {
		if hMap, ok := h.(map[string]interface{}); ok {
			if cmd, _ := hMap["command"].(string); strings.Contains(cmd, "arac") {
				return true
			}
		}
	}
	return false
}

// Strips aracne integration segment from a file and rewrites it if changed.
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

// Removes the aracne integration markdown section from content, preserving line endings and spacing.
func stripAracneIntegrationSegment(content string) string {
	lineEnding := markdownLineEnding(content)

	start := findAracIntegrationStart(content)
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

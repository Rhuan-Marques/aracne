package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type claudeNativeEditHook struct {
	scriptName string
	content    string
	command    string
	shell      string
}

func writeClaudeNativeEditHook(settingsPath, hooksDir string, autoYes bool) {
	os.MkdirAll(hooksDir, 0755)
	hook := claudeNativeEditHookForOS(runtime.GOOS)
	scriptPath := filepath.Join(hooksDir, hook.scriptName)
	writeMarkdownFile(scriptPath, "Claude native edit hook script", hook.content, autoYes)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(scriptPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not make %s executable: %v\n", scriptPath, err)
		}
	}

	settings := readJSONConfig(settingsPath)
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	hooks["PostToolUse"] = []interface{}{
		map[string]interface{}{
			"matcher": "Edit|Write|MultiEdit",
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": hook.command,
					"shell":   hook.shell,
					"timeout": 60,
				},
			},
		},
	}
	settings["hooks"] = hooks
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Native edit hook configured in %s\n", settingsPath)
}

// guardHookMatcher is the tool matcher for the guard hook. It matches only the
// PascalCase native tools (the lowercase mcp__aracne__* tools never match).
const guardHookMatcher = "Read|Grep|Edit|Write|Bash"

// writeClaudeGuardHook installs the `arac guard` PreToolUse + PostToolUse hooks
// that warn on (and, per blocked_tools, block) native/shell tool usage. It
// merges into settings.json, preserving the edit-sync PostToolUse hook and any
// user hooks, and is idempotent (its own prior entries are replaced).
func writeClaudeGuardHook(settingsPath, hooksDir string, autoYes bool) {
	os.MkdirAll(hooksDir, 0755)
	hook := claudeGuardHookForOS(runtime.GOOS)
	scriptPath := filepath.Join(hooksDir, hook.scriptName)
	writeMarkdownFile(scriptPath, "Claude guard hook script", hook.content, autoYes)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(scriptPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not make %s executable: %v\n", scriptPath, err)
		}
	}

	settings := readJSONConfig(settingsPath)
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	entry := map[string]interface{}{
		"matcher": guardHookMatcher,
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": hook.command,
				"shell":   hook.shell,
				"timeout": 30,
			},
		},
	}
	hooks["PreToolUse"] = upsertGuardHookEntry(hooks["PreToolUse"], entry)
	hooks["PostToolUse"] = upsertGuardHookEntry(hooks["PostToolUse"], entry)
	settings["hooks"] = hooks
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Guard hook configured in %s\n", settingsPath)
}

// upsertGuardHookEntry drops any existing guard entry from a hook-event list
// (so re-init does not duplicate it) and appends the fresh one, leaving every
// other entry — the edit-sync hook, user hooks — untouched.
func upsertGuardHookEntry(existing interface{}, entry map[string]interface{}) []interface{} {
	list, _ := existing.([]interface{})
	filtered := make([]interface{}, 0, len(list)+1)
	for _, e := range list {
		if !isGuardHookEntry(e) {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, entry)
}

// isGuardHookEntry reports whether a settings.json hook entry is the guard
// hook, identified by its script command (`arac-guard`).
func isGuardHookEntry(entry interface{}) bool {
	entryMap, ok := entry.(map[string]interface{})
	if !ok {
		return false
	}
	hooksList, _ := entryMap["hooks"].([]interface{})
	for _, h := range hooksList {
		if hMap, ok := h.(map[string]interface{}); ok {
			if cmd, _ := hMap["command"].(string); strings.Contains(cmd, "arac-guard") {
				return true
			}
		}
	}
	return false
}

// writeClaudePermissions merges an allow-list for the aracne MCP tools into
// settings.json so Claude Code does not prompt before each MCP call. It
// preserves user-defined permissions and is idempotent: its own prior
// mcp__aracne__* entries are replaced rather than duplicated on re-init.
func writeClaudePermissions(settingsPath string) {
	settings := readJSONConfig(settingsPath)
	permissions, _ := settings["permissions"].(map[string]interface{})
	if permissions == nil {
		permissions = make(map[string]interface{})
	}
	allow, _ := permissions["allow"].([]interface{})
	permissions["allow"] = upsertAracneAllowRules(allow, claudeMCPPermissionRules())
	settings["permissions"] = permissions
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] MCP tool permissions configured in %s\n", settingsPath)
}

// claudeMCPPermissionRules returns the Claude Code permission rules that allow
// every aracne MCP tool, e.g. "mcp__aracne__read_function". The full universe is
// listed (not just the main agent's profile) so sub-agents — already restricted
// by their own tools: frontmatter — never trigger a permission prompt either.
func claudeMCPPermissionRules() []string {
	names := allMCPToolNames()
	rules := make([]string, 0, len(names))
	for _, name := range names {
		rules = append(rules, "mcp__aracne__"+name)
	}
	return rules
}

// upsertAracneAllowRules drops any existing aracne MCP entries from a
// permissions allow-list (so re-init does not duplicate them) and appends the
// fresh set, leaving every user-defined rule untouched.
func upsertAracneAllowRules(existing []interface{}, rules []string) []interface{} {
	result := make([]interface{}, 0, len(existing)+len(rules))
	for _, e := range existing {
		if s, ok := e.(string); ok && strings.HasPrefix(s, "mcp__aracne__") {
			continue
		}
		result = append(result, e)
	}
	for _, r := range rules {
		result = append(result, r)
	}
	return result
}

func claudeGuardHookForOS(goos string) claudeNativeEditHook {
	if goos == "windows" {
		return claudeNativeEditHook{
			scriptName: "arac-guard.ps1",
			content:    claudeGuardHookPowerShellScript(),
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.ps1",
			shell:      "powershell",
		}
	}
	return claudeNativeEditHook{
		scriptName: "arac-guard.sh",
		content:    claudeGuardHookShellScript(),
		command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh",
		shell:      "bash",
	}
}

func claudeGuardHookShellScript() string {
	return strings.Join([]string{
		"#!/bin/sh",
		"exec arac guard --claude-hook",
		"",
	}, "\n")
}

func claudeGuardHookPowerShellScript() string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$inputJson | & arac guard --claude-hook",
		"",
	}, "\n")
}

func claudeNativeEditHookForOS(goos string) claudeNativeEditHook {
	if goos == "windows" {
		return claudeNativeEditHook{
			scriptName: "arac-update-file.ps1",
			content:    claudeUpdateFileHookPowerShellScript(),
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.ps1",
			shell:      "powershell",
		}
	}
	return claudeNativeEditHook{
		scriptName: "arac-update-file.sh",
		content:    claudeUpdateFileHookShellScript(),
		command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.sh",
		shell:      "bash",
	}
}

func claudeUpdateFileHookPowerShellScript() string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$inputJson | & arac update-file --claude-hook",
		"",
	}, "\n")
}

func claudeUpdateFileHookShellScript() string {
	return strings.Join([]string{
		"#!/bin/sh",
		"exec arac update-file --claude-hook",
		"",
	}, "\n")
}

func writeOpenCodeNativeEditPlugin(pluginsDir string, autoYes bool) {
	os.MkdirAll(pluginsDir, 0755)
	writeMarkdownFile(filepath.Join(pluginsDir, "arac-native-edit-sync.js"), "OpenCode native edit sync plugin", openCodeNativeEditPlugin(), autoYes)
}

func openCodeNativeEditPlugin() string {
	return strings.TrimPrefix(`
import { execFileSync } from "node:child_process"
import path from "node:path"

export const AracNativeEditSync = async ({ directory, worktree }) => {
  const root = worktree ?? directory ?? process.cwd()

  function normalizeFile(file) {
    if (!file) return ""
    const normalized = path.isAbsolute(file) ? path.relative(root, file) : file
    if (!normalized || normalized.startsWith("..") || path.isAbsolute(normalized)) return file
    return normalized
  }

  function shouldSkip(file) {
    const parts = file.split(/[\\/]+/)
    return parts[0] === ".git" || parts[0] === ".aracne" || parts.includes("node_modules")
  }

  function updateFile(file) {
    file = normalizeFile(file)
    if (!file || shouldSkip(file)) return ""
    try {
      const text = execFileSync("arac", ["update-file", file], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] })
      if (/Warning number\s+0/.test(text)) return ""
      return `+"`"+`Aracne warnings for ${file}:\n${text}`+"`"+`
    } catch (error) {
      const text = `+"`"+`${error.stdout?.toString?.() ?? ""}${error.stderr?.toString?.() ?? ""}`+"`"+`
      return `+"`"+`arac update-file failed for ${file}:\n${text}`+"`"+`
    }
  }

  function pathsFromArgs(tool, args) {
    const paths = new Set()
    for (const key of ["file_path", "filePath", "path"]) if (args?.[key]) paths.add(args[key])
    if (Array.isArray(args?.edits)) {
      for (const edit of args.edits) {
        for (const key of ["file_path", "filePath", "path"]) if (edit?.[key]) paths.add(edit[key])
      }
    }
    if (tool === "apply_patch") {
      const patch = args?.patch ?? args?.patchText
      if (typeof patch === "string") {
        for (const match of patch.matchAll(/^[+]{3} b\/(.+)$/gm)) paths.add(match[1])
        for (const match of patch.matchAll(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm)) paths.add(match[1])
        for (const match of patch.matchAll(/^\*\*\* Move to: (.+)$/gm)) paths.add(match[1])
      }
    }
    return [...paths]
  }

  return {
    event: async ({ event }) => {
      if (event.type !== "file.edited" && event.type !== "file.watcher.updated") return
      if (event.type === "file.watcher.updated" && event.properties?.event !== "change") return
      const message = updateFile(event.properties?.file)
      if (message) console.warn(message)
    },
    "tool.execute.after": async (input, output) => {
      if (!["edit", "write", "apply_patch", "multi_edit", "multiedit"].includes(input.tool)) return
      const messages = pathsFromArgs(input.tool, input.args).map(updateFile).filter(Boolean)
      if (messages.length > 0) output.output = `+"`"+`${output.output ?? ""}\n\n${messages.join("\n")}`+"`"+`
    },
  }
}
`, "\n")
}

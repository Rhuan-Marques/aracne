package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/toolspec"
)

// Holds OS-specific hook script metadata: name, content, command, and shell type
type claudeNativeEditHook struct {
	scriptName string
	content    string
	command    string
	shell      string
}

// Installs a native edit hook script and registers it in Claude's settings configuration.
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
func writeClaudePermissions(settingsPath string, cfg *helper.Config) {
	settings := readJSONConfig(settingsPath)
	permissions, _ := settings["permissions"].(map[string]interface{})
	if permissions == nil {
		permissions = make(map[string]interface{})
	}
	allow, _ := permissions["allow"].([]interface{})
	permissions["allow"] = upsertAracneAllowRules(allow, claudeMCPPermissionRules(cfg))
	settings["permissions"] = permissions
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] MCP tool permissions configured in %s\n", settingsPath)
}

// claudeMCPPermissionRules returns the Claude Code permission rules that allow
// every aracne MCP tool, e.g. "mcp__aracne__grep". The full universe is
// listed (not just the main agent's profile) so sub-agents — already restricted
// by their own tools: frontmatter — never trigger a permission prompt either.
//
// Both runtime names of the read tool are listed. Which one an agent registers depends on its
// blocked_tools, and agents in one project can differ; allowing only the resolved name would
// leave the other prompting.
// The bug_* rules are omitted when features.bug_management is off, so the disabled state is
// verifiably absent from the generated harness config rather than merely unserved.
// upsertAracneAllowRules strips every mcp__aracne__* rule before re-adding, so flipping the
// flag in either direction self-heals an existing settings.json on the next init.
func claudeMCPPermissionRules(cfg *helper.Config) []string {
	names := allMCPToolNames()
	rules := make([]string, 0, len(names)+1)
	for _, name := range names {
		if toolspec.IsBugTool(name) && !cfg.BugManagementEnabled() {
			continue
		}
		rules = append(rules, "mcp__aracne__"+name)
	}
	rules = append(rules, "mcp__aracne__"+toolspec.ReadResourceToolName)
	sort.Strings(rules)
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

// Returns the appropriate native hook script and configuration for the given OS.
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

// Returns a shell script that invokes the arac guard tool as a Claude hook
func claudeGuardHookShellScript() string {
	return strings.Join([]string{
		"#!/bin/sh",
		"exec arac guard --claude-hook",
		"",
	}, "\n")
}

// Returns the PowerShell script for the native guard hook.
func claudeGuardHookPowerShellScript() string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$inputJson | & arac guard --claude-hook",
		"",
	}, "\n")
}

// Returns OS-specific native edit hook configuration (PowerShell for Windows, shell script for others)
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

// Generates a PowerShell script that pipes stdin JSON to the arac update-file command with claude-hook flag
func claudeUpdateFileHookPowerShellScript() string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$inputJson | & arac update-file --claude-hook",
		"",
	}, "\n")
}

// Generates a shell script that invokes arac update-file with the claude-hook flag
func claudeUpdateFileHookShellScript() string {
	return strings.Join([]string{
		"#!/bin/sh",
		"exec arac update-file --claude-hook",
		"",
	}, "\n")
}

// writeOpenCodePreToolScanPlugin installs the OpenCode counterpart of the Claude Code
// PreToolUse guard hook: a plugin that runs the scan.pre_tool scan before each tool call.
//
// It is installed unconditionally, like the Claude guard hook, and NOT listed under
// `plugins` in the config. Which scan runs -- including none at all -- is decided at call
// time by `scan.pre_tool`, so flipping that knob takes effect without re-running init, and a
// project cannot end up with a fresh graph on one harness and a stale one on the other.
func writeOpenCodePreToolScanPlugin(pluginsDir string, autoYes bool) {
	os.MkdirAll(pluginsDir, 0755)
	writeMarkdownFile(filepath.Join(pluginsDir, "arac-pre-tool-scan.js"), "OpenCode pre-tool scan plugin", openCodePreToolScanPlugin(), autoYes)
}

// openCodePreToolScanPlugin returns the OpenCode plugin that re-syncs the topology before a
// tool call. It mirrors the Claude guard hook's matcher (Read|Grep|Edit|Write|Bash) in
// OpenCode's tool names, and defers the whole decision to `arac guard --pre-scan`, which reads
// the project config and does nothing when scan.pre_tool is "none".
func openCodePreToolScanPlugin() string {
	return strings.TrimPrefix(`
import { execFile } from "node:child_process"
import { promisify } from "node:util"

const run = promisify(execFile)

// The tools whose answer depends on the topology being current, matching the Claude Code
// guard hook's matcher plus OpenCode's own spellings of a native edit.
const SCANNED_TOOLS = new Set([
  "read",
  "grep",
  "edit",
  "write",
  "bash",
  "patch",
  "apply_patch",
  "multi_edit",
  "multiedit",
])

export const AracPreToolScan = async ({ directory, worktree }) => {
  const root = worktree ?? directory ?? process.cwd()

  return {
    "tool.execute.before": async (input) => {
      if (!SCANNED_TOOLS.has(input?.tool)) return
      try {
        // Awaited on purpose: the scan is only worth running if it lands BEFORE the tool
        // reads the graph. Bounded and swallowed, like the Claude hook -- a scan that failed
        // must never turn into a tool call that failed.
        await run("arac", ["guard", "--pre-scan"], { cwd: root, timeout: 30000 })
      } catch {}
    },
  }
}
`, "\n")
}

// Writes the OpenCode native edit sync plugin JavaScript file to the plugins directory.
func writeOpenCodeNativeEditPlugin(pluginsDir string, autoYes bool) {
	os.MkdirAll(pluginsDir, 0755)
	writeMarkdownFile(filepath.Join(pluginsDir, "arac-native-edit-sync.js"), "OpenCode native edit sync plugin", openCodeNativeEditPlugin(), autoYes)
}

// Returns a Node.js plugin for syncing file edits with arac, intercepting file changes and tool execution.
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

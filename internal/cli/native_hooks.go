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

func claudeNativeEditHookForOS(goos string) claudeNativeEditHook {
	if goos == "windows" {
		return claudeNativeEditHook{
			scriptName: "ltp-update-file.ps1",
			content:    claudeUpdateFileHookPowerShellScript(),
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/ltp-update-file.ps1",
			shell:      "powershell",
		}
	}
	return claudeNativeEditHook{
		scriptName: "ltp-update-file.sh",
		content:    claudeUpdateFileHookShellScript(),
		command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/ltp-update-file.sh",
		shell:      "sh",
	}
}

func claudeUpdateFileHookPowerShellScript() string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$inputJson | & ltp update-file --claude-hook",
		"",
	}, "\n")
}

func claudeUpdateFileHookShellScript() string {
	return strings.Join([]string{
		"#!/bin/sh",
		"exec ltp update-file --claude-hook",
		"",
	}, "\n")
}

func writeOpenCodeNativeEditPlugin(pluginsDir string, autoYes bool) {
	os.MkdirAll(pluginsDir, 0755)
	writeMarkdownFile(filepath.Join(pluginsDir, "ltp-native-edit-sync.js"), "OpenCode native edit sync plugin", openCodeNativeEditPlugin(), autoYes)
}

func openCodeNativeEditPlugin() string {
	return strings.TrimPrefix(`
import { execFileSync } from "node:child_process"
import path from "node:path"

export const LtpNativeEditSync = async ({ directory, worktree }) => {
  const root = worktree ?? directory ?? process.cwd()

  function normalizeFile(file) {
    if (!file) return ""
    const normalized = path.isAbsolute(file) ? path.relative(root, file) : file
    if (!normalized || normalized.startsWith("..") || path.isAbsolute(normalized)) return file
    return normalized
  }

  function shouldSkip(file) {
    const parts = file.split(/[\\/]+/)
    return parts[0] === ".git" || parts[0] === ".ltp" || parts.includes("node_modules")
  }

  function updateFile(file) {
    file = normalizeFile(file)
    if (!file || shouldSkip(file)) return ""
    try {
      const text = execFileSync("ltp", ["update-file", file], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] })
      if (/Warning number\s+0/.test(text)) return ""
      return `+"`"+`llm-topology warnings for ${file}:\n${text}`+"`"+`
    } catch (error) {
      const text = `+"`"+`${error.stdout?.toString?.() ?? ""}${error.stderr?.toString?.() ?? ""}`+"`"+`
      return `+"`"+`llm-topology update-file failed for ${file}:\n${text}`+"`"+`
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

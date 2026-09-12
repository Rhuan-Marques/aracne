package cli

import (
	"fmt"
	"path/filepath"
)

// pluginWriter maps a unified plugin enum to its harness-specific writers, and to the removers
// that undo them.
//
// Both halves, because `arac setup` re-renders the config in both directions: a plugin taken
// out of `plugins` has to lose its hook and its plugin file on the next run, the way
// pruneBugArtifacts makes features.bug_management reversible. With only writers, a removed
// edit-update-db-plugin kept its hook firing on every edit -- and, once the binary path it
// embeds moved, failing on every edit. `arac disable` still removes all of them regardless.
type pluginWriter struct {
	// claude takes `global` as well as the base directory, because the settings entry a
	// hook writer produces depends on it: a global install cannot name its script through
	// ${CLAUDE_PROJECT_DIR}. See hookScriptRef.
	claude   func(claudeBaseDir string, global, autoYes bool)
	openCode func(openCodeConfigDir string, autoYes bool)
	// removeClaude / removeOpenCode undo the writers when the plugin is no longer listed.
	removeClaude   func(claudeBaseDir string)
	removeOpenCode func(openCodeConfigDir string)
}

// Returns a map of available plugins that sync the topology database after native edits, with handlers for Claude and Open Code environments.
func pluginWriters() map[string]pluginWriter {
	return map[string]pluginWriter{
		// Syncs the topology DB after native (non-MCP) edits. Only needed when
		// an agent edits via native tools instead of the MCP edit/write tools.
		"edit-update-db-plugin": {
			claude: func(claudeBaseDir string, global, autoYes bool) {
				writeClaudeNativeEditHook(filepath.Join(claudeBaseDir, "settings.json"), filepath.Join(claudeBaseDir, "hooks"), global, autoYes)
			},
			openCode: func(openCodeConfigDir string, autoYes bool) {
				writeOpenCodeNativeEditPlugin(filepath.Join(openCodeConfigDir, "plugins"), autoYes)
			},
			removeClaude: func(claudeBaseDir string) {
				removeClaudeNativeEditHook(filepath.Join(claudeBaseDir, "settings.json"), filepath.Join(claudeBaseDir, "hooks"))
			},
			removeOpenCode: func(openCodeConfigDir string) {
				removeFile(filepath.Join(openCodeConfigDir, "plugins", "arac-native-edit-sync.js"), "OpenCode native edit sync plugin")
			},
		},
	}
}

// Writes Claude plugin files for each requested plugin name using registered plugin writers,
// and removes the artifacts of every known plugin the list no longer names.
func writeClaudePlugins(plugins []string, claudeBaseDir string, global, autoYes bool) {
	writers := pluginWriters()
	for _, name := range plugins {
		if w, ok := writers[name]; ok && w.claude != nil {
			w.claude(claudeBaseDir, global, autoYes)
			continue
		}
		fmt.Printf("[Claude Code] Unknown plugin %q, skipping\n", name)
	}
	listed := toolNameSet(plugins)
	for name, w := range writers {
		if !listed[name] && w.removeClaude != nil {
			w.removeClaude(claudeBaseDir)
		}
	}
}

// Writes OpenCode configuration files for specified plugins, invoking each plugin's openCode
// writer if available, and removes the artifacts of every known plugin the list no longer names.
func writeOpenCodePlugins(plugins []string, openCodeConfigDir string, autoYes bool) {
	writers := pluginWriters()
	for _, name := range plugins {
		if w, ok := writers[name]; ok && w.openCode != nil {
			w.openCode(openCodeConfigDir, autoYes)
			continue
		}
		fmt.Printf("[OpenCode] Unknown plugin %q, skipping\n", name)
	}
	listed := toolNameSet(plugins)
	for name, w := range writers {
		if !listed[name] && w.removeOpenCode != nil {
			w.removeOpenCode(openCodeConfigDir)
		}
	}
}

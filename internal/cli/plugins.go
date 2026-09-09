package cli

import (
	"fmt"
	"path/filepath"
)

// pluginWriter maps a unified plugin enum to its harness-specific writers.
// Removal is handled unconditionally by `arac disable`, so only the write side
// is mapped here.
type pluginWriter struct {
	// claude takes `global` as well as the base directory, because the settings entry a
	// hook writer produces depends on it: a global install cannot name its script through
	// ${CLAUDE_PROJECT_DIR}. See hookScriptRef.
	claude   func(claudeBaseDir string, global, autoYes bool)
	openCode func(openCodeConfigDir string, autoYes bool)
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
		},
	}
}

// Writes Claude plugin files for each requested plugin name using registered plugin writers.
func writeClaudePlugins(plugins []string, claudeBaseDir string, global, autoYes bool) {
	writers := pluginWriters()
	for _, name := range plugins {
		if w, ok := writers[name]; ok && w.claude != nil {
			w.claude(claudeBaseDir, global, autoYes)
			continue
		}
		fmt.Printf("[Claude Code] Unknown plugin %q, skipping\n", name)
	}
}

// Writes OpenCode configuration files for specified plugins, invoking each plugin's openCode writer if available.
func writeOpenCodePlugins(plugins []string, openCodeConfigDir string, autoYes bool) {
	writers := pluginWriters()
	for _, name := range plugins {
		if w, ok := writers[name]; ok && w.openCode != nil {
			w.openCode(openCodeConfigDir, autoYes)
			continue
		}
		fmt.Printf("[OpenCode] Unknown plugin %q, skipping\n", name)
	}
}

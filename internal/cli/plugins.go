package cli

import (
	"fmt"
	"path/filepath"
)

// pluginWriter maps a unified plugin enum to its harness-specific writers.
// Removal is handled unconditionally by `arac disable`, so only the write side
// is mapped here.
type pluginWriter struct {
	claude   func(claudeBaseDir string, autoYes bool)
	openCode func(openCodeConfigDir string, autoYes bool)
}

func pluginWriters() map[string]pluginWriter {
	return map[string]pluginWriter{
		// Syncs the topology DB after native (non-MCP) edits. Only needed when
		// an agent edits via native tools instead of the MCP edit/write tools.
		"edit-update-db-plugin": {
			claude: func(claudeBaseDir string, autoYes bool) {
				writeClaudeNativeEditHook(filepath.Join(claudeBaseDir, "settings.json"), filepath.Join(claudeBaseDir, "hooks"), autoYes)
			},
			openCode: func(openCodeConfigDir string, autoYes bool) {
				writeOpenCodeNativeEditPlugin(filepath.Join(openCodeConfigDir, "plugins"), autoYes)
			},
		},
	}
}

func writeClaudePlugins(plugins []string, claudeBaseDir string, autoYes bool) {
	writers := pluginWriters()
	for _, name := range plugins {
		if w, ok := writers[name]; ok && w.claude != nil {
			w.claude(claudeBaseDir, autoYes)
			continue
		}
		fmt.Printf("[Claude Code] Unknown plugin %q, skipping\n", name)
	}
}

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

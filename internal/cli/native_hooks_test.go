package cli

import (
	"strings"
	"testing"
)

func TestClaudeNativeEditHookForOS(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		scriptName string
		shell      string
		command    string
	}{
		{
			name:       "windows uses powershell hook",
			goos:       "windows",
			scriptName: "arac-update-file.ps1",
			shell:      "powershell",
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.ps1",
		},
		{
			name:       "linux uses shell hook",
			goos:       "linux",
			scriptName: "arac-update-file.sh",
			shell:      "bash",
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.sh",
		},
		{
			name:       "macos uses shell hook",
			goos:       "darwin",
			scriptName: "arac-update-file.sh",
			shell:      "bash",
			command:    "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.sh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := claudeNativeEditHookForOS(tt.goos)
			if hook.scriptName != tt.scriptName {
				t.Fatalf("scriptName = %q, want %q", hook.scriptName, tt.scriptName)
			}
			if hook.shell != tt.shell {
				t.Fatalf("shell = %q, want %q", hook.shell, tt.shell)
			}
			if hook.command != tt.command {
				t.Fatalf("command = %q, want %q", hook.command, tt.command)
			}
		})
	}
}

func TestClaudeHookPaths(t *testing.T) {
	paths := claudeHookPaths(map[string]interface{}{
		"file_path": "first.go",
		"path":      "first.go",
		"edits": []interface{}{
			map[string]interface{}{"file_path": "second.go"},
			map[string]interface{}{"filePath": "third.go"},
			map[string]interface{}{"path": "second.go"},
		},
	})
	want := []string{"first.go", "second.go", "third.go"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestOpenCodeNativeEditPluginHandlesNativeAndWatcherEdits(t *testing.T) {
	plugin := openCodeNativeEditPlugin()
	checks := []string{
		`import path from "node:path"`,
		`path.relative(root, file)`,
		`{ cwd: root, encoding: "utf8"`,
		`args?.patch ?? args?.patchText`,
		`(?:Add|Update|Delete) File`,
		`event.type !== "file.edited" && event.type !== "file.watcher.updated"`,
		`event.properties?.event !== "change"`,
	}
	for _, check := range checks {
		if !strings.Contains(plugin, check) {
			t.Fatalf("OpenCode plugin missing %q:\n%s", check, plugin)
		}
	}
}

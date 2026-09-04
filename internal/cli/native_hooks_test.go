package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
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

func TestClaudeGuardHookForOS(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		scriptName string
		shell      string
		command    string
	}{
		{"windows", "windows", "arac-guard.ps1", "powershell", "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.ps1"},
		{"linux", "linux", "arac-guard.sh", "bash", "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"},
		{"darwin", "darwin", "arac-guard.sh", "bash", "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := claudeGuardHookForOS(tt.goos)
			if hook.scriptName != tt.scriptName || hook.shell != tt.shell || hook.command != tt.command {
				t.Fatalf("claudeGuardHookForOS(%q) = %+v", tt.goos, hook)
			}
		})
	}
}

// TestWriteClaudeGuardHookMerges verifies the guard hook coexists with the
// edit-sync PostToolUse hook (neither clobbers the other) and is idempotent.
func TestWriteClaudeGuardHookMerges(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hooksDir := filepath.Join(dir, "hooks")

	// Install edit-sync hook first, then the guard hook (init order), twice.
	writeClaudeNativeEditHook(settingsPath, hooksDir, true)
	writeClaudeGuardHook(settingsPath, hooksDir, true)
	writeClaudeGuardHook(settingsPath, hooksDir, true) // idempotent

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Hooks struct {
			PreToolUse  []hookEntry `json:"PreToolUse"`
			PostToolUse []hookEntry `json:"PostToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, data)
	}

	// PreToolUse: exactly one guard entry (idempotent).
	if got := countCommand(settings.Hooks.PreToolUse, "arac-guard"); got != 1 {
		t.Fatalf("PreToolUse guard entries = %d, want 1:\n%s", got, data)
	}
	// PostToolUse: the edit-sync entry AND exactly one guard entry survive.
	if got := countCommand(settings.Hooks.PostToolUse, "arac-update-file"); got != 1 {
		t.Fatalf("PostToolUse edit-sync entries = %d, want 1:\n%s", got, data)
	}
	if got := countCommand(settings.Hooks.PostToolUse, "arac-guard"); got != 1 {
		t.Fatalf("PostToolUse guard entries = %d, want 1:\n%s", got, data)
	}

	// The guard matcher targets the native tools.
	for _, e := range settings.Hooks.PreToolUse {
		if e.Matcher != guardHookMatcher {
			t.Fatalf("PreToolUse matcher = %q, want %q", e.Matcher, guardHookMatcher)
		}
	}

	// Both script files were written.
	for _, name := range []string{"arac-update-file.sh", "arac-guard.sh"} {
		if _, err := os.Stat(filepath.Join(hooksDir, name)); err != nil {
			t.Fatalf("expected hook script %s: %v", name, err)
		}
	}
}

// TestWriteClaudePermissions verifies the aracne MCP tools land in
// permissions.allow, user-defined rules survive, the merge is idempotent, and
// it coexists with the guard hook in the same settings.json.
func TestWriteClaudePermissions(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hooksDir := filepath.Join(dir, "hooks")

	// Seed a user-defined permission to ensure it is preserved.
	seed := []byte(`{"permissions":{"allow":["Bash(go test:*)"],"deny":["WebFetch"]}}`)
	if err := os.WriteFile(settingsPath, seed, 0644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	cfg := helper.DefaultConfig()
	writeClaudeGuardHook(settingsPath, hooksDir, true)
	writeClaudePermissions(settingsPath, cfg)
	writeClaudePermissions(settingsPath, cfg) // idempotent

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
		Hooks map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, data)
	}

	counts := map[string]int{}
	for _, rule := range settings.Permissions.Allow {
		counts[rule]++
	}

	// Every aracne MCP tool is allowed exactly once (idempotent across re-runs).
	for _, want := range claudeMCPPermissionRules(cfg) {
		if counts[want] != 1 {
			t.Fatalf("allow rule %q count = %d, want 1:\n%s", want, counts[want], data)
		}
	}
	// A representative tool is present (guards against an empty universe).
	if counts["mcp__aracne__read_resource"] == 0 {
		t.Fatalf("expected mcp__aracne__read_resource in allow:\n%s", data)
	}
	// User-defined rules are preserved.
	if counts["Bash(go test:*)"] != 1 {
		t.Fatalf("user allow rule was dropped:\n%s", data)
	}
	if len(settings.Permissions.Deny) != 1 || settings.Permissions.Deny[0] != "WebFetch" {
		t.Fatalf("user deny rule was dropped:\n%s", data)
	}
	// The guard hook still lives in the same settings.json.
	if settings.Hooks["PreToolUse"] == nil {
		t.Fatalf("guard hook clobbered by permissions write:\n%s", data)
	}
}

type hookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

func countCommand(entries []hookEntry, substr string) int {
	n := 0
	for _, e := range entries {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, substr) {
				n++
			}
		}
	}
	return n
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

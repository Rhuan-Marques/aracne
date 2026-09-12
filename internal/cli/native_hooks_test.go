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
			command:    "& '${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.ps1'",
		},
		{
			name:       "linux uses shell hook",
			goos:       "linux",
			scriptName: "arac-update-file.sh",
			shell:      "bash",
			command:    `"${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.sh"`,
		},
		{
			name:       "macos uses shell hook",
			goos:       "darwin",
			scriptName: "arac-update-file.sh",
			shell:      "bash",
			command:    `"${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.sh"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := claudeNativeEditHookForOS(tt.goos, ".claude/hooks", false)
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
		{"windows", "windows", "arac-guard.ps1", "powershell", "& '${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.ps1'"},
		{"linux", "linux", "arac-guard.sh", "bash", `"${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"`},
		{"darwin", "darwin", "arac-guard.sh", "bash", `"${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := claudeGuardHookForOS(tt.goos, ".claude/hooks", false)
			if hook.scriptName != tt.scriptName || hook.shell != tt.shell || hook.command != tt.command {
				t.Fatalf("claudeGuardHookForOS(%q) = %+v", tt.goos, hook)
			}
		})
	}
}

// The generated command is handed to a shell with the placeholder already substituted, so an
// unquoted path word-splits: a project under "My Projects" failed every tool call with
// `bash: line 1: /Users/x/My: No such file or directory`, exit 127 -- the guard dead, loudly,
// on every turn. Both spellings have to survive a path with a space.
func TestHookCommandSurvivesAPathWithSpaces(t *testing.T) {
	const root = "/Users/x/My Projects/repo"

	for _, tt := range []struct {
		goos string
		want string
	}{
		{"linux", `"` + root + `/.claude/hooks/arac-guard.sh"`},
		{"windows", `& '` + root + `/.claude/hooks/arac-guard.ps1'`},
	} {
		hook := claudeGuardHookForOS(tt.goos, root+"/.claude/hooks", true)
		if hook.command != tt.want {
			t.Fatalf("%s: command = %q, want %q", tt.goos, hook.command, tt.want)
		}
		// And the entry must still be recognizable as aracne's own, or setup stacks a
		// duplicate beside it and disable leaves it behind.
		entry := map[string]interface{}{
			"hooks": []interface{}{map[string]interface{}{"command": hook.command}},
		}
		if !isGuardHookEntry(entry) {
			t.Fatalf("%s: quoted command %q not recognized as the guard entry", tt.goos, hook.command)
		}
		if !isAracneHookEntry(entry) {
			t.Fatalf("%s: quoted command %q not recognized as aracne's", tt.goos, hook.command)
		}
	}
}

// `arac setup --global` writes the scripts under the user's HOME while ${CLAUDE_PROJECT_DIR}
// still expands to the PROJECT root -- so the entry named a file setup had never created and
// the guard was silently dead in every project without a local copy.
func TestGlobalHookNamesTheScriptItActuallyWrote(t *testing.T) {
	hooksDir := filepath.Join(t.TempDir(), ".claude", "hooks")

	for _, hook := range []claudeNativeEditHook{
		claudeGuardHookForOS("linux", hooksDir, true),
		claudeNativeEditHookForOS("linux", hooksDir, true),
	} {
		if strings.Contains(hook.command, "CLAUDE_PROJECT_DIR") {
			t.Fatalf("global hook still names the project dir: %q", hook.command)
		}
		want := filepath.ToSlash(filepath.Join(hooksDir, hook.scriptName))
		if hook.command != `"`+want+`"` {
			t.Fatalf("global command = %q, want %q", hook.command, `"`+want+`"`)
		}
	}

	// A local install keeps the portable placeholder: settings.json is usually committed,
	// and an absolute path there means the wrong thing on a teammate's checkout.
	local := claudeGuardHookForOS("linux", ".claude/hooks", false)
	if !strings.Contains(local.command, "${CLAUDE_PROJECT_DIR}") {
		t.Fatalf("local command lost the placeholder: %q", local.command)
	}
}

// hookCommandWord is what keeps a quoted command recognizable. It has to read past
// PowerShell's call operator and through the quotes, and it must NOT match a hook that merely
// mentions the script's name.
func TestHookCommandWord(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`"${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"`, "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"},
		{`& '/Users/x/My Projects/.claude/hooks/arac-guard.ps1'`, "/Users/x/My Projects/.claude/hooks/arac-guard.ps1"},
		{`${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh`, "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh"},
		{"arac update-file --claude-hook", "arac"},
		{"", ""},
	} {
		if got := hookCommandWord(tt.in); got != tt.want {
			t.Fatalf("hookCommandWord(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	notMine := map[string]interface{}{
		"hooks": []interface{}{map[string]interface{}{"command": "echo not-arac-guard.sh"}},
	}
	if isGuardHookEntry(notMine) || isAracneHookEntry(notMine) {
		t.Fatal("a user hook that merely mentions the script name was claimed as aracne's")
	}
}

// TestWriteClaudeGuardHookMerges verifies the guard hook coexists with the
// edit-sync PostToolUse hook (neither clobbers the other) and is idempotent.
func TestWriteClaudeGuardHookMerges(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	hooksDir := filepath.Join(dir, "hooks")

	// Install edit-sync hook first, then the guard hook (init order), twice.
	writeClaudeNativeEditHook(settingsPath, hooksDir, false, true)
	writeClaudeGuardHook(settingsPath, hooksDir, false, true)
	writeClaudeGuardHook(settingsPath, hooksDir, false, true) // idempotent

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
	writeClaudeGuardHook(settingsPath, hooksDir, false, true)
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

// The MCP entries setup writes (.mcp.json, opencode.json, every agent's inline mcpServers) name
// the binary and have NO fallback to `arac` on PATH the way the hook scripts and plugins do -- so
// a path that stops existing is a server that silently fails to start. EvalSymlinks turned a
// stable `bin/arac` into the versioned file behind it, which the next upgrade deletes.
func TestAracBinaryPrefersAStablePathNameForTheSameFile(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "Cellar", "arac", "1.0.0", "bin", "arac")
	if err := os.MkdirAll(filepath.Dir(versioned), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(dir, "bin", "arac")
	if err := os.MkdirAll(filepath.Dir(stable), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versioned, stable); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	other := filepath.Join(dir, "other", "arac")
	if err := os.MkdirAll(filepath.Dir(other), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	exe := func() (string, error) { return versioned, nil }
	found := func(path string) func(string) (string, error) {
		return func(string) (string, error) { return path, nil }
	}

	if got := aracBinaryFrom(exe, found(stable)); got != stable {
		t.Errorf("aracBinaryFrom = %q, want the stable name %q", got, stable)
	}
	// A DIFFERENT arac on PATH is not this one, and naming it would run the wrong binary.
	if got := aracBinaryFrom(exe, found(other)); got != versioned {
		t.Errorf("aracBinaryFrom = %q, want the running binary %q", got, versioned)
	}
	// Nothing on PATH: the absolute path is still better than a bare name.
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	if got := aracBinaryFrom(exe, missing); got != versioned {
		t.Errorf("aracBinaryFrom = %q, want %q", got, versioned)
	}
	// And no executable at all is the one case where `arac` on PATH is all there is.
	if got := aracBinaryFrom(func() (string, error) { return "", os.ErrNotExist }, found(stable)); got != "arac" {
		t.Errorf("aracBinaryFrom = %q, want the bare name", got)
	}
}

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripAracneIntegrationSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty content",
			input: "",
			want:  "",
		},
		{
			name:  "no markers present",
			input: "# Some Doc\n\ncontent\n",
			want:  "# Some Doc\n\ncontent\n",
		},
		{
			name:  "only start marker",
			input: "# Aracne Project Integration\n\ncontent\n",
			want:  "# Aracne Project Integration\n\ncontent\n",
		},
		{
			name:  "both markers mid-file",
			input: "before\n\n# Aracne Project Integration\n\nsection to remove\n\nGood Luck in your task.\n\nafter\n",
			want:  "before\n\nafter\n",
		},
		{
			name:  "section at beginning of file",
			input: "# Aracne Project Integration\n\nsection\n\nGood Luck in your task.\n\nafter\n",
			want:  "after\n",
		},
		{
			name:  "section at end of file",
			input: "before\n\n# Aracne Project Integration\n\nsection\n\nGood Luck in your task.\n",
			want:  "before\n",
		},
		{
			name:  "CRLF line endings preserved",
			input: "before\r\n\r\n# Aracne Project Integration\r\n\r\nsection\r\n\r\nGood Luck in your task.\r\n\r\nafter\r\n",
			want:  "before\r\n\r\nafter\r\n",
		},
		{
			name:  "no trailing newline",
			input: "before\n\n# Aracne Project Integration\n\nsection\n\nGood Luck in your task.",
			want:  "before\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripAracneIntegrationSegment(tt.input)
			if got != tt.want {
				t.Fatalf("stripAracneIntegrationSegment:\n  got:  %q\n  want: %q", got, tt.want)
			}
		})
	}
}

func TestRemoveAracneHookFromSettings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty file",
			input: "{}",
			want:  "{}",
		},
		{
			name:  "no hooks key",
			input: `{"other": "value"}`,
			want:  `{"other": "value"}`,
		},
		{
			name:  "hooks but no PostToolUse",
			input: `{"hooks": {"other": []}}`,
			want:  `{"hooks": {"other": []}}`,
		},
		{
			name:  "aracne PostToolUse hook",
			input: `{"hooks": {"PostToolUse": [{"matcher": "Edit|Write|MultiEdit", "hooks": [{"type": "command", "command": "arac update-file", "shell": "powershell", "timeout": 60}]}]}}`,
			want:  `{}`,
		},
		{
			name:  "non-aracne PostToolUse hook kept",
			input: `{"hooks": {"PostToolUse": [{"matcher": "some-other", "hooks": [{"type": "command", "command": "other-tool", "shell": "sh"}]}]}}`,
			want:  `{"hooks": {"PostToolUse": [{"matcher": "some-other", "hooks": [{"type": "command", "command": "other-tool", "shell": "sh"}]}]}}`,
		},
		{
			name:  "mixed hooks only aracne removed",
			input: `{"hooks": {"PostToolUse": [{"matcher": "Edit|Write|MultiEdit", "hooks": [{"type": "command", "command": "arac update-file", "shell": "powershell", "timeout": 60}]}, {"matcher": "other", "hooks": [{"type": "command", "command": "other-tool"}]}]}}`,
			want:  `{"hooks": {"PostToolUse": [{"matcher": "other", "hooks": [{"type": "command", "command": "other-tool"}]}]}}`,
		},
		{
			name:  "aracne hook removed regardless of matcher",
			input: `{"hooks": {"PostToolUse": [{"matcher": "Read", "hooks": [{"type": "command", "command": "arac read-hook"}]}]}}`,
			want:  `{}`,
		},
		{
			name:  "aracne PreToolUse guard hook removed",
			input: `{"hooks": {"PreToolUse": [{"matcher": "Read|Grep|Edit|Write|Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-guard.sh", "shell": "bash"}]}]}}`,
			want:  `{}`,
		},
		{
			name:  "guard and edit-sync removed, user hooks kept across events",
			input: `{"hooks": {"PreToolUse": [{"matcher": "Read|Grep|Edit|Write|Bash", "hooks": [{"command": "arac-guard.sh"}]}, {"matcher": "Bash", "hooks": [{"command": "my-linter"}]}], "PostToolUse": [{"matcher": "Edit|Write|MultiEdit", "hooks": [{"command": "arac update-file"}]}, {"matcher": "Read|Grep|Edit|Write|Bash", "hooks": [{"command": "arac-guard.sh"}]}]}}`,
			want:  `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"command": "my-linter"}]}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			settingsPath := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(settingsPath, []byte(tt.input), 0644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			removeAracneHookFromSettings(settingsPath)

			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatalf("read result: %v", err)
			}

			var gotObj, wantObj map[string]interface{}
			if err := json.Unmarshal(data, &gotObj); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantObj); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}

			gotJSON, _ := json.Marshal(gotObj)
			wantJSON, _ := json.Marshal(wantObj)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("removeAracneHookFromSettings:\n  got:  %s\n  want: %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

func TestRemoveAracneHookFromSettingsFileNotExist(t *testing.T) {
	dir := t.TempDir()
	removeAracneHookFromSettings(filepath.Join(dir, "nonexistent.json"))
}

func TestDisableOpenCode(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".opencode/commands", 0755)
	os.MkdirAll(".opencode/agents", 0755)
	os.MkdirAll(".opencode/plugins", 0755)

	configContent := `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "arac": {
      "type": "local",
      "command": ["arac", "serve", "--tool-profile", "all"],
      "enabled": true
    }
  },
  "permission": {
    "read": "deny",
    "edit": "deny",
    "aracne_*": "deny",
    "aracne_read": "allow",
    "aracne_edit": "allow",
    "aracne_grep": "allow"
  }
}`
	os.WriteFile(".opencode/opencode.json", []byte(configContent), 0644)

	for _, cmd := range []string{
		"descriptions-generate.md", "descriptions-apply.md", "descriptions_clear.md",
		"bug-hunter.md", "bug-judge.md", "bug-solver.md",
	} {
		os.WriteFile(filepath.Join(".opencode/commands", cmd), []byte("content"), 0644)
	}

	for _, agent := range []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md", "bug-judge.md", "bug-solver.md",
	} {
		os.WriteFile(filepath.Join(".opencode/agents", agent), []byte("content"), 0644)
	}

	os.WriteFile(".opencode/plugins/arac-native-edit-sync.js", []byte("plugin"), 0644)
	os.WriteFile(".opencode/plugins/arac-pre-tool-scan.js", []byte("plugin"), 0644)

	agentsMd := "before\n\n# Aracne Project Integration\n\nsection\n\nGood Luck in your task.\n\nafter\n"
	os.WriteFile("AGENTS.md", []byte(agentsMd), 0644)

	disableOpenCode(false, true)

	configData, _ := os.ReadFile(".opencode/opencode.json")
	config := string(configData)

	if strings.Contains(config, `"mcp"`) {
		t.Fatalf("opencode.json should have no mcp key, got:\n%s", config)
	}
	if !strings.Contains(config, `"read": "allow"`) {
		t.Fatalf("opencode.json should allow read:\n%s", config)
	}
	if !strings.Contains(config, `"edit": "allow"`) {
		t.Fatalf("opencode.json should allow edit:\n%s", config)
	}
	if !strings.Contains(config, `"bash": "allow"`) {
		t.Fatalf("opencode.json should allow bash:\n%s", config)
	}
	if strings.Contains(config, `"aracne_`) {
		t.Fatalf("opencode.json should have no aracne_ permissions:\n%s", config)
	}

	checkEmptyDir(t, ".opencode/commands", "OpenCode commands")
	checkEmptyDir(t, ".opencode/agents", "OpenCode agents")
	checkFileNotExist(t, ".opencode/plugins/arac-native-edit-sync.js", "OpenCode plugin")
	checkFileNotExist(t, ".opencode/plugins/arac-pre-tool-scan.js", "OpenCode pre-tool scan plugin")

	agentsMdData, _ := os.ReadFile("AGENTS.md")
	if strings.Contains(string(agentsMdData), "Aracne Project Integration") {
		t.Fatalf("AGENTS.md should have no integration section:\n%s", string(agentsMdData))
	}
}

func TestDisableClaudeCode(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".claude/commands", 0755)
	os.MkdirAll(".claude/agents", 0755)
	os.MkdirAll(".claude/hooks", 0755)

	mcpContent := `{
  "mcpServers": {
    "aracne": {
      "command": "arac",
      "args": ["serve", "--tool-profile", "default"]
    }
  }
}`
	os.WriteFile(".mcp.json", []byte(mcpContent), 0644)

	for _, cmd := range []string{
		"descriptions-generate.md", "descriptions-apply.md", "descriptions_clear.md",
		"bug-hunter.md", "bug-judge.md", "bug-solver.md",
	} {
		os.WriteFile(filepath.Join(".claude/commands", cmd), []byte("content"), 0644)
	}
	for _, agent := range []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md", "bug-judge.md", "bug-solver.md",
	} {
		os.WriteFile(filepath.Join(".claude/agents", agent), []byte("content"), 0644)
	}
	os.WriteFile(".claude/hooks/arac-update-file.ps1", []byte("hook"), 0644)

	settingsContent := `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          {
            "type": "command",
            "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/arac-update-file.ps1",
            "shell": "powershell",
            "timeout": 60
          }
        ]
      }
    ]
  }
}`
	os.WriteFile(".claude/settings.json", []byte(settingsContent), 0644)

	claudeMd := "before\n\n# Aracne Project Integration\n\nsection\n\nGood Luck in your task.\n\nafter\n"
	os.WriteFile("CLAUDE.md", []byte(claudeMd), 0644)

	disableClaudeCode(false, true)

	mcpData, _ := os.ReadFile(".mcp.json")
	if strings.TrimSpace(string(mcpData)) != "{}" {
		t.Fatalf(".mcp.json should be empty, got:\n%s", string(mcpData))
	}

	checkEmptyDir(t, ".claude/commands", "Claude commands")
	checkEmptyDir(t, ".claude/agents", "Claude agents")
	checkEmptyDir(t, ".claude/hooks", "Claude hooks")

	settingsData, _ := os.ReadFile(".claude/settings.json")
	if strings.TrimSpace(string(settingsData)) != "{}" {
		t.Fatalf("settings.json should be empty, got:\n%s", string(settingsData))
	}

	claudeMdData, _ := os.ReadFile("CLAUDE.md")
	if strings.Contains(string(claudeMdData), "Aracne Project Integration") {
		t.Fatalf("CLAUDE.md should have no integration section:\n%s", string(claudeMdData))
	}
}

func TestDisableIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".opencode/commands", 0755)
	os.MkdirAll(".opencode/agents", 0755)

	configContent := `{
  "mcp": {"arac": {"type": "local", "command": ["arac"]}},
  "permission": {"read": "deny", "edit": "deny", "aracne_*": "deny"}
}`
	os.WriteFile(".opencode/opencode.json", []byte(configContent), 0644)
	os.WriteFile(".opencode/commands/bug-hunter.md", []byte("content"), 0644)
	os.WriteFile(".opencode/agents/bug-hunter.md", []byte("content"), 0644)

	disableOpenCode(false, true)

	configData1, _ := os.ReadFile(".opencode/opencode.json")
	stateAfterFirst := string(configData1)

	disableOpenCode(false, true)

	configData2, _ := os.ReadFile(".opencode/opencode.json")
	stateAfterSecond := string(configData2)

	if stateAfterFirst != stateAfterSecond {
		t.Fatalf("second disable changed state:\nfirst:  %s\nsecond: %s", stateAfterFirst, stateAfterSecond)
	}
}

func TestDisableWithNoFiles(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	disableOpenCode(false, true)
	disableClaudeCode(false, true)
}

func TestRunDisableParsesFlagsAndDisablesBothByDefault(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".opencode/commands", 0755)
	os.MkdirAll(".claude/commands", 0755)

	ocConfig := `{
	  "mcp": {"arac": {"type": "local", "command": ["arac"]}},
	  "permission": {"read": "deny", "edit": "deny", "aracne_*": "deny"}
	}`
	os.WriteFile(".opencode/opencode.json", []byte(ocConfig), 0644)
	os.WriteFile(".opencode/commands/bug-hunter.md", []byte("content"), 0644)

	mcpConfig := `{
	  "mcpServers": {"aracne": {"command": "arac"}}
	}`
	os.WriteFile(".mcp.json", []byte(mcpConfig), 0644)
	os.WriteFile(".claude/commands/bug-hunter.md", []byte("content"), 0644)

	RunDisable([]string{"-y"})

	ocData, _ := os.ReadFile(".opencode/opencode.json")
	if strings.Contains(string(ocData), `"mcp"`) {
		t.Fatal("RunDisable default should disable OpenCode (mcp still present)")
	}

	mcpData, _ := os.ReadFile(".mcp.json")
	if strings.TrimSpace(string(mcpData)) != "{}" {
		t.Fatal("RunDisable default should disable Claude Code (mcpServers still present)")
	}
}

func TestRunDisableOnlyOpenCode(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".opencode/commands", 0755)
	os.MkdirAll(".claude/commands", 0755)

	ocConfig := `{
	  "mcp": {"arac": {"type": "local", "command": ["arac"]}},
	  "permission": {"read": "deny", "edit": "deny"}
	}`
	os.WriteFile(".opencode/opencode.json", []byte(ocConfig), 0644)
	os.WriteFile(".opencode/commands/bug-hunter.md", []byte("content"), 0644)

	mcpConfig := `{
	  "mcpServers": {"aracne": {"command": "arac"}}
	}`
	os.WriteFile(".mcp.json", []byte(mcpConfig), 0644)
	os.WriteFile(".claude/commands/bug-hunter.md", []byte("content"), 0644)

	RunDisable([]string{"-y", "--opencode"})

	ocData, _ := os.ReadFile(".opencode/opencode.json")
	if strings.Contains(string(ocData), `"mcp"`) {
		t.Fatal("--opencode should disable OpenCode")
	}

	mcpData, _ := os.ReadFile(".mcp.json")
	if !strings.Contains(string(mcpData), "mcpServers") {
		t.Fatal("--opencode should NOT disable Claude Code")
	}
}

func TestRunDisableAllFlag(t *testing.T) {
	dir := t.TempDir()
	prevDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevDir)

	os.MkdirAll(".opencode/commands", 0755)
	os.MkdirAll(".claude/commands", 0755)

	ocConfig := `{
	  "mcp": {"arac": {"type": "local", "command": ["arac"]}},
	  "permission": {"read": "deny", "edit": "deny"}
	}`
	os.WriteFile(".opencode/opencode.json", []byte(ocConfig), 0644)
	os.WriteFile(".opencode/commands/bug-hunter.md", []byte("content"), 0644)

	mcpConfig := `{
	  "mcpServers": {"aracne": {"command": "arac"}}
	}`
	os.WriteFile(".mcp.json", []byte(mcpConfig), 0644)
	os.WriteFile(".claude/commands/bug-hunter.md", []byte("content"), 0644)

	RunDisable([]string{"-y", "--all"})

	ocData, _ := os.ReadFile(".opencode/opencode.json")
	if strings.Contains(string(ocData), `"mcp"`) {
		t.Fatal("--all should disable OpenCode")
	}

	mcpData, _ := os.ReadFile(".mcp.json")
	if strings.TrimSpace(string(mcpData)) != "{}" {
		t.Fatal("--all should disable Claude Code")
	}
}

func checkEmptyDir(t *testing.T, dir, label string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read %s dir: %v", label, err)
	}
	if len(entries) > 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("%s dir should be empty, got: %v", label, names)
	}
}

func checkFileNotExist(t *testing.T, path, label string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should not exist at %s", label, path)
	}
}

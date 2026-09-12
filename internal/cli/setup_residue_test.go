package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
)

// Fix pass 2, setup/init/disable: `arac setup` re-renders from the config in BOTH directions, and
// `arac disable` takes back exactly what setup made -- no more, no less.

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s should exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("%s should not exist, got:\n%s", path, data)
	}
}

func saveTestConfig(t *testing.T, dir string, cfg *helper.Config) {
	t.Helper()
	if err := helper.SaveConfig(cfg, helper.ConfigPath(filepath.Join(dir, DefaultDBRelative))); err != nil {
		t.Fatal(err)
	}
}

// SU-3. Outside ModeMCP the OpenCode main agent is allowed none of aracne's tools; a switch out
// of mcp -- or a tool dropped from mcp_tools -- used to leave its `aracne_<tool>: allow` behind.
func TestSetupRederivesOpenCodeAllowRules(t *testing.T) {
	inProject(t)
	mcp := helper.DefaultConfig()
	mcp.Mode = helper.ModeMCP
	initOpenCode(false, mcp, true, nil)
	perms, _ := openCodePermissions(t).(map[string]interface{})
	// Pinned: in mcp mode the main agent's tools ARE allowed.
	if perms["aracne_read"] != "allow" || perms["aracne_warnings_list"] != "allow" {
		t.Fatalf("mcp mode should allow the main agent's tools, got %v", perms)
	}

	narrowed := helper.DefaultConfig()
	narrowed.Mode = helper.ModeMCP
	narrowed.LLM.Any.MainAgent.MCPTools = []string{"read"}
	initOpenCode(false, narrowed, true, nil)
	perms, _ = openCodePermissions(t).(map[string]interface{})
	if _, stale := perms["aracne_warnings_list"]; stale {
		t.Errorf("a tool dropped from mcp_tools is still allowed: %v", perms)
	}

	initOpenCode(false, helper.DefaultConfig(), true, nil) // cli
	perms, _ = openCodePermissions(t).(map[string]interface{})
	for key, value := range perms {
		if strings.HasPrefix(key, "aracne_") && key != "aracne_*" {
			t.Errorf("cli mode left %s: %v", key, value)
		}
	}
	if perms["aracne_*"] != "deny" {
		t.Errorf("aracne_* must stay denied, got %v", perms)
	}
}

// SU-4. Taking edit-update-db-plugin out of `plugins` has to take its hook and plugin with it,
// and nothing else: the guard hook, the pre-tool scan plugin and a user's own hook all stay.
func TestRemovingThePluginUninstallsItsArtifacts(t *testing.T) {
	inProject(t)
	userHook := map[string]interface{}{
		"matcher": "Edit",
		"hooks":   []interface{}{map[string]interface{}{"type": "command", "command": "./my-formatter.sh"}},
	}
	os.MkdirAll(".claude", 0755)
	writeJSONConfig(".claude/settings.json", map[string]interface{}{
		"hooks": map[string]interface{}{"PostToolUse": []interface{}{userHook}},
	})

	with := helper.DefaultConfig()
	with.LLM.Any.MainAgent.Plugins = []string{"edit-update-db-plugin"}
	initClaudeCode(false, with, true, nil)
	initOpenCode(false, with, true, nil)
	hookScript := filepath.Join(".claude", "hooks", claudeNativeEditHookForOS(runtime.GOOS, ".claude/hooks", false).scriptName)
	mustExist(t, hookScript)
	mustExist(t, ".opencode/plugins/arac-native-edit-sync.js")

	initClaudeCode(false, helper.DefaultConfig(), true, nil)
	initOpenCode(false, helper.DefaultConfig(), true, nil)
	mustNotExist(t, hookScript)
	mustNotExist(t, ".opencode/plugins/arac-native-edit-sync.js")
	mustExist(t, ".opencode/plugins/arac-pre-tool-scan.js")

	hooks, _ := readJSONConfig(".claude/settings.json")["hooks"].(map[string]interface{})
	post, _ := hooks["PostToolUse"].([]interface{})
	var guard, user bool
	for _, e := range post {
		if isEditSyncHookEntry(e) {
			t.Errorf("the edit-sync entry survived: %v", e)
		}
		guard = guard || isGuardHookEntry(e)
		user = user || jsonEqual(e, userHook)
	}
	if !guard || !user {
		t.Errorf("the guard (%v) and the user's hook (%v) must both stay: %v", guard, user, post)
	}
}

// SU-6. The config and database are found by walking up, so the integration is written where
// they are -- not into whatever subdirectory setup was run from. Disable finds it the same way.
func TestSetupAndDisableFromASubdirectoryWorkOnTheProjectRoot(t *testing.T) {
	for name, withDB := range map[string]bool{"config only": false, "scanned": true} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			saveTestConfig(t, root, helper.DefaultConfig())
			if withDB {
				os.WriteFile(filepath.Join(root, DefaultDBRelative), nil, 0644)
			}
			sub := filepath.Join(root, "shapes", "deep")
			os.MkdirAll(sub, 0755)
			t.Chdir(sub)

			runSetup(true, true, false, true)
			for _, rel := range []string{"CLAUDE.md", "AGENTS.md", ".claude/settings.json", ".opencode/opencode.json"} {
				mustExist(t, filepath.Join(root, rel))
			}
			for _, rel := range []string{"CLAUDE.md", "AGENTS.md", ".claude", ".opencode", ".aracne"} {
				mustNotExist(t, filepath.Join(sub, rel))
			}

			RunDisable([]string{"-y"})
			for _, rel := range []string{"CLAUDE.md", "AGENTS.md", ".claude", ".opencode"} {
				mustNotExist(t, filepath.Join(root, rel))
			}
		})
	}
}

// Pinned: outside any project a local setup still writes where it is run, as it always has.
func TestSetupOutsideAProjectWritesHere(t *testing.T) {
	inProject(t)
	runSetup(true, false, false, true)
	mustExist(t, "CLAUDE.md")
	mustExist(t, ".aracne/config.json")
}

// SU-9. A `.mcp.json` setup created is removed once it holds nothing -- on a switch out of mcp
// and on disable -- while one the operator had keeps their servers.
func TestNoEmptyMCPConfigIsLeftBehind(t *testing.T) {
	mcp := helper.DefaultConfig()
	mcp.Mode = helper.ModeMCP

	t.Run("created by setup", func(t *testing.T) {
		inProject(t)
		initClaudeCode(false, mcp, true, nil)
		mustExist(t, ".mcp.json")
		initClaudeCode(false, helper.DefaultConfig(), true, nil) // switch to cli
		mustNotExist(t, ".mcp.json")

		initClaudeCode(false, mcp, true, nil)
		disableClaudeCode(false, true)
		mustNotExist(t, ".mcp.json")
	})

	t.Run("the operator's", func(t *testing.T) {
		inProject(t)
		other := map[string]interface{}{"command": "other-server"}
		writeJSONConfig(".mcp.json", map[string]interface{}{"mcpServers": map[string]interface{}{"other": other}})
		initClaudeCode(false, mcp, true, nil)
		initClaudeCode(false, helper.DefaultConfig(), true, nil)
		servers, _ := readJSONConfig(".mcp.json")["mcpServers"].(map[string]interface{})
		if _, aracne := servers["aracne"]; aracne || !jsonEqual(servers["other"], other) {
			t.Fatalf("the switch must remove aracne's server and only it, got %v", servers)
		}
		initClaudeCode(false, mcp, true, nil)
		disableClaudeCode(false, true)
		servers, _ = readJSONConfig(".mcp.json")["mcpServers"].(map[string]interface{})
		if !jsonEqual(servers["other"], other) || len(servers) != 1 {
			t.Fatalf("disable must leave the operator's server, got %v", servers)
		}
	})

	t.Run("the operator's, empty", func(t *testing.T) {
		inProject(t)
		os.WriteFile(".mcp.json", []byte("{}\n"), 0644)
		initClaudeCode(false, mcp, true, nil)
		disableClaudeCode(false, true)
		mustExist(t, ".mcp.json")
	})
}

// SU-10. Disable removes the CLAUDE.md / AGENTS.md and the directories setup created once they
// are empty -- in a project and under --global -- and never one the operator had.
func TestDisableRemovesWhatSetupCreatedOnceEmpty(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		inProject(t)
		runSetup(true, true, false, true)
		RunDisable([]string{"-y"})
		for _, rel := range []string{"CLAUDE.md", "AGENTS.md", ".claude", ".opencode"} {
			mustNotExist(t, rel)
		}
	})

	t.Run("global", func(t *testing.T) {
		inProject(t)
		home := os.Getenv("HOME")
		runSetup(true, true, true, true)
		RunDisable([]string{"-y", "--global"})
		for _, rel := range []string{".claude/CLAUDE.md", ".claude/agents", ".claude/commands", ".claude/hooks",
			".config/opencode/AGENTS.md", ".config/opencode"} {
			mustNotExist(t, filepath.Join(home, rel))
		}
	})

	t.Run("the operator's files stay", func(t *testing.T) {
		inProject(t)
		os.WriteFile("CLAUDE.md", nil, 0644) // theirs, even empty
		os.WriteFile("AGENTS.md", []byte("# Team notes\n\nkeep me\n"), 0644)
		os.MkdirAll(".claude/agents", 0755)
		os.WriteFile(".claude/agents/mine.md", []byte("mine"), 0644)
		runSetup(true, true, false, true)
		RunDisable([]string{"-y"})

		mustExist(t, "CLAUDE.md")
		if data, _ := os.ReadFile("AGENTS.md"); string(data) != "# Team notes\n\nkeep me\n" {
			t.Errorf("AGENTS.md should be back to the operator's content, got %q", data)
		}
		mustExist(t, ".claude/agents/mine.md")
		mustNotExist(t, ".claude/commands") // setup made this one, and it is empty
		mustNotExist(t, ".opencode")
	})
}

// SU-12. `--global` installs to user level; it does not create a project config in whatever
// directory it happens to run from, and its contract names no project's languages.
func TestGlobalSetupCreatesNoProjectConfig(t *testing.T) {
	inProject(t)
	home := os.Getenv("HOME")
	runSetup(true, true, true, true)
	mustNotExist(t, ".aracne")
	mustExist(t, filepath.Join(home, ".claude", "CLAUDE.md"))
	mustExist(t, filepath.Join(home, ".config", "opencode", "AGENTS.md"))
}

// Pinned: run inside a project, --global still renders from that project's config -- which is
// how `arac init --global` carries its answers to user level -- but not from its languages.
func TestGlobalSetupRendersTheProjectsModeButNotItsLanguages(t *testing.T) {
	root, dbPath := scannedProject(t) // a Go project
	t.Chdir(root)
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	if len(TopologyLanguages(dbPath)) == 0 {
		t.Fatal("the fixture should have a language for the global contract to leave out")
	}
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.ContractVerbosity = helper.ContractVerbosityHigh
	saveTestConfig(t, ".", cfg)

	runSetup(true, false, true, true)
	data, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := prompts.ClaudeMdForConfig(cfg, nil); strings.TrimSpace(string(data)) != strings.TrimSpace(want) {
		t.Errorf("the global contract should be the project's mode, language-free:\n%s", data)
	}
}

// SU-7. A high-verbosity block whose closing line the reader deleted is still replaced and
// removed WHOLE: its `# CONTEXT:` examples are fenced, so the fallback bound (the next top-level
// heading) is the operator's next section, not the middle of the contract.
func TestAHighContractWithoutItsClosingLineIsHandledWhole(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.ContractVerbosity = helper.ContractVerbosityHigh
	contract := prompts.ClaudeMdForConfig(cfg, []string{"go", "python"})
	edited := strings.Replace(contract, AracIntegrationEnd+"\n", "", 1)
	const before, after = "# Team\n\nour notes\n\n", "\n# Later\n\nmore notes\n"
	existing := before + edited + after

	got := updateMarkdownIntegrationSegment(existing, contract)
	if !strings.HasPrefix(got, before+contract) || !strings.HasSuffix(got, strings.TrimLeft(after, "\n")) ||
		strings.Count(got, "# CONTEXT:") != strings.Count(contract, "# CONTEXT:") {
		t.Errorf("re-setup did not replace the whole block:\n%s", got)
	}
	if got := stripAracneIntegrationSegment(existing); strings.Contains(got, "CONTEXT") || !strings.Contains(got, "# Later") {
		t.Errorf("disable did not remove the whole block, or took the operator's section:\n%s", got)
	}
}

// SU-11. The hook script runs the binary setup recorded, and `arac` on PATH only when that is not
// there -- a teammate's checkout of the committed script, or a moved binary.
func TestHookScriptFallsBackToAracOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh hook")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0755)
	fake := func(path, name string) {
		os.WriteFile(path, []byte("#!/bin/sh\necho "+name+" \"$@\"\n"), 0755)
	}
	fake(filepath.Join(bin, "arac"), "PATH")
	recorded := filepath.Join(dir, "My Tools", "arac")
	os.MkdirAll(filepath.Dir(recorded), 0755)

	run := func() string {
		script := filepath.Join(dir, "hook.sh")
		os.WriteFile(script, []byte(shellHookScript(recorded, "guard --claude-hook")), 0755)
		cmd := exec.Command("/bin/sh", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook failed: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if got := run(); got != "PATH guard --claude-hook" {
		t.Errorf("with the recorded binary gone the hook should run arac on PATH, got %q", got)
	}
	// Pinned: the recorded absolute path stays the first choice whenever it is there.
	fake(recorded, "RECORDED")
	if got := run(); got != "RECORDED guard --claude-hook" {
		t.Errorf("the recorded binary must win over PATH, got %q", got)
	}

	for name, plugin := range map[string]string{"pre-tool scan": openCodePreToolScanPlugin(), "edit sync": openCodeNativeEditPlugin()} {
		if !strings.Contains(plugin, `const ARAC = existsSync(RECORDED_ARAC) ? RECORDED_ARAC : "arac"`) {
			t.Errorf("the %s plugin has no PATH fallback:\n%s", name, plugin)
		}
	}
}

// SU-8. A scan that fails -- here, a repository with no source file yet -- no longer ends `arac
// init` between saving the config and writing the integration.
func TestInitWritesTheIntegrationWhenTheScanFails(t *testing.T) {
	inProject(t)
	os.WriteFile("README.md", []byte("hello\n"), 0644)
	cfg := helper.DefaultConfig()
	saveTestConfig(t, ".", cfg)

	finishInit(NewScannerRegistry(), cfg, initAnswers{Harness: harnessClaudeCode, DescribeNow: true}, false)
	mustExist(t, "CLAUDE.md")
	mustExist(t, ".claude/settings.json")
}

// SU-13. The wizard said OpenCode got the MCP server "in mcp mode"; setup writes it in every mode.
func TestTheHarnessQuestionDescribesWhatSetupWrites(t *testing.T) {
	for _, opt := range harnessQuestion().Options {
		for _, line := range opt.Detail {
			if strings.Contains(line, "in mcp mode") {
				t.Errorf("%s: %q -- the OpenCode MCP server is written in every mode", opt.Label, line)
			}
		}
	}
}

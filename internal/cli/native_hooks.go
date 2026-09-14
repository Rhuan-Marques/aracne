package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// Holds OS-specific hook script metadata: name, content, command, and shell type
type claudeNativeEditHook struct {
	scriptName string
	content    string
	command    string
	shell      string
}

// Installs a native edit hook script and registers it in Claude's settings configuration.
func writeClaudeNativeEditHook(settingsPath, hooksDir string, global, autoYes bool) {
	os.MkdirAll(hooksDir, 0755)
	hook := claudeNativeEditHookForOS(runtime.GOOS, hooksDir, global)
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
	entry := map[string]interface{}{
		"matcher": "Edit|Write|MultiEdit",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": hook.command,
				"shell":   hook.shell,
				"timeout": 60,
			},
		},
	}
	// UPSERTED, not assigned. A plain assignment replaced the whole list, which deleted the
	// user's own PostToolUse hooks -- a formatter, a linter, a notifier -- and only spared the
	// guard's entry because the guard happens to be written afterwards and re-adds its own.
	// The guard has always upserted; this is the same filter-then-append.
	hooks["PostToolUse"] = upsertHookEntry(hooks["PostToolUse"], entry, isEditSyncHookEntry)
	settings["hooks"] = hooks
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Native edit hook configured in %s\n", settingsPath)
}

// removeClaudeNativeEditHook undoes writeClaudeNativeEditHook: its scripts, and its PostToolUse
// entry, leaving the guard's entry and every user hook in place. A settings.json with nothing
// of this hook in it is not rewritten (nor created).
func removeClaudeNativeEditHook(settingsPath, hooksDir string) {
	removeFiles(hooksDir, []string{"arac-update-file.sh", "arac-update-file.ps1"})

	settings := readJSONConfig(settingsPath)
	hooks, _ := settings["hooks"].(map[string]interface{})
	entries, _ := hooks["PostToolUse"].([]interface{})
	kept := make([]interface{}, 0, len(entries))
	for _, e := range entries {
		if !isEditSyncHookEntry(e) {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(entries) {
		return
	}
	if len(kept) == 0 {
		delete(hooks, "PostToolUse")
	} else {
		hooks["PostToolUse"] = kept
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Removed the native edit hook from %s (edit-update-db-plugin is not listed)\n", settingsPath)
}

// guardHookMatcher is the tool matcher for the guard hook. It matches only the PascalCase
// native tools (the lowercase mcp__aracne__* tools never match).
//
// DERIVED from toolspec.NativeToolNames rather than typed out, because the two must agree: a
// name the matcher omits is a call the guard never sees, and a name the map omits is a call
// the guard sees and cannot classify. Typed separately, the matcher fell behind the map and
// MultiEdit went unguarded -- see toolspec.nativeToolToKey.
var guardHookMatcher = strings.Join(toolspec.NativeToolNames(), "|")

// writeClaudeGuardHook installs the `arac guard` PreToolUse + PostToolUse hooks
// that warn on (and, per blocked_tools, block) native/shell tool usage. It
// merges into settings.json, preserving the edit-sync PostToolUse hook and any
// user hooks, and is idempotent (its own prior entries are replaced).
func writeClaudeGuardHook(settingsPath, hooksDir string, global, autoYes bool) {
	os.MkdirAll(hooksDir, 0755)
	hook := claudeGuardHookForOS(runtime.GOOS, hooksDir, global)
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
				// DERIVED from the guard's own budgets rather than chosen -- see
				// GuardHookTimeoutSeconds for the arithmetic and for what a killed hook costs.
				"timeout": GuardHookTimeoutSeconds,
			},
		},
	}
	hooks["PreToolUse"] = upsertGuardHookEntry(hooks["PreToolUse"], entry)
	hooks["PostToolUse"] = upsertGuardHookEntry(hooks["PostToolUse"], entry)
	settings["hooks"] = hooks
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Guard hook configured in %s\n", settingsPath)
}

// upsertHookEntry drops the entries `mine` recognizes as aracne's own from a hook-event list
// (so re-running setup does not duplicate them) and appends the fresh one, leaving every other
// entry — the sibling aracne hook, and every hook the user wrote — untouched.
//
// Every writer of a hook-event list goes through it. The one that did not simply assigned over
// the list and took the user's hooks with it.
func upsertHookEntry(existing interface{}, entry map[string]interface{},
	mine func(interface{}) bool) []interface{} {

	list, _ := existing.([]interface{})
	filtered := make([]interface{}, 0, len(list)+1)
	for _, e := range list {
		if !mine(e) {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, entry)
}

// upsertGuardHookEntry is upsertHookEntry for the guard's own entry.
func upsertGuardHookEntry(existing interface{}, entry map[string]interface{}) []interface{} {
	return upsertHookEntry(existing, entry, isGuardHookEntry)
}

// isEditSyncHookEntry reports whether a settings.json hook entry is the edit-sync hook,
// identified by its script command (`arac-update-file`).
func isEditSyncHookEntry(entry interface{}) bool {
	return hookEntryRunsScript(entry, "arac-update-file")
}

// isGuardHookEntry reports whether a settings.json hook entry is the guard
// hook, identified by its script command (`arac-guard`).
func isGuardHookEntry(entry interface{}) bool {
	return hookEntryRunsScript(entry, "arac-guard")
}

// hookEntryRunsScript reports whether any of a hook entry's commands RUNS the named aracne
// script, `.sh` or `.ps1`.
//
// It tests the command WORD, base-named and stripped of its extension -- not a substring of the
// whole command. A substring is what `arac disable` used to delete a settings entry by, and it
// matched things that merely mentioned the name: a user hook that echoes `not-arac-guard.sh`,
// or anything under a directory called `arac-guard`. Since setup writes the script path AS the
// command (the shell is a sibling field, not a prefix), the command word is exactly the right
// thing to look at.
func hookEntryRunsScript(entry interface{}, script string) bool {
	entryMap, ok := entry.(map[string]interface{})
	if !ok {
		return false
	}
	hooksList, _ := entryMap["hooks"].([]interface{})
	for _, h := range hooksList {
		hMap, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		cmd, _ := hMap["command"].(string)
		word := strings.ToLower(baseName(hookCommandWord(cmd)))
		if word == script || word == script+".sh" || word == script+".ps1" {
			return true
		}
	}
	return false
}

// hookCommandWord returns the program a settings.json hook command invokes: its first token,
// with quoting honoured and PowerShell's call operator skipped.
//
// strings.Fields is not enough any more, and that is not a detail. The generated command now
// QUOTES its script path (see bashHookCommand -- an unquoted path under "My Projects" broke
// every hook), so the first field of `& 'C:/My Projects/x/.claude/hooks/arac-guard.ps1'` is
// `&` and the second is `'C:/My`. Neither names the script, and an entry aracne cannot
// recognize is one `arac setup` stacks a duplicate beside and `arac disable` leaves behind.
//
// Still the command WORD and not a substring: that is what keeps a user hook echoing
// "not-arac-guard.sh", or anything under a directory called arac-guard, out of it.
func hookCommandWord(cmd string) string {
	var tokens []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range cmd {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	for _, tok := range tokens {
		// PowerShell's call operator is punctuation, not a program.
		if tok == "&" {
			continue
		}
		return tok
	}
	return ""
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

// The Claude Code setting that decides whether tool schemas reach the model up front or are
// deferred behind its tool-search, and the one value aracne ever writes into it.
//
// "false" is the spelling that means "no tool search", i.e. everything loaded. Claude Code also
// accepts "auto", "auto:N" and "force" -- none of which aracne writes, and all of which are
// therefore an operator's own answer that setup and disable must leave alone.
const (
	claudeToolSearchEnvKey = "ENABLE_TOOL_SEARCH"
	claudeToolSearchOff    = "false"
)

// writeClaudeToolSearchEnv applies `preload_mcp_tools` to settings.json, in both directions.
//
// BOTH DIRECTIONS IS THE POINT. Writing the variable when the answer is yes is the easy half;
// the half that matters is withdrawing it when the answer stops being yes -- because the answer
// can stop being yes without anybody re-answering the question. Switching the project out of
// ModeMCP does it (PreloadMCPToolsEnabled folds the mode in), and so does re-running `arac init`
// and picking the other row. Left behind, the variable would go on suppressing tool search for
// every tool in the session on behalf of a server that no longer registers any -- the same stale
// entry dropAracneMCPServer exists to prevent, one file over.
//
// It only ever removes ITS OWN value. An operator who set ENABLE_TOOL_SEARCH to "auto:40" for
// reasons of their own has said something aracne did not say and does not get to un-say.
func writeClaudeToolSearchEnv(settingsPath string, cfg *helper.Config) {
	settings := readJSONConfig(settingsPath)
	env, _ := settings["env"].(map[string]interface{})
	current, _ := env[claudeToolSearchEnvKey].(string)

	if cfg.PreloadMCPToolsEnabled() {
		if current == claudeToolSearchOff {
			return
		}
		if env == nil {
			env = make(map[string]interface{})
		}
		env[claudeToolSearchEnvKey] = claudeToolSearchOff
		settings["env"] = env
		writeJSONConfig(settingsPath, settings)
		fmt.Printf("[Claude Code] Tool schemas pinned in context in %s (%s=%s)\n",
			settingsPath, claudeToolSearchEnvKey, claudeToolSearchOff)
		return
	}

	if !dropAracneToolSearchEnv(settings) {
		return
	}
	writeJSONConfig(settingsPath, settings)
	fmt.Printf("[Claude Code] Removed %s from %s (mode: %s)\n",
		claudeToolSearchEnvKey, settingsPath, cfg.EffectiveMode())
}

// dropAracneToolSearchEnv removes the tool-search opt-out aracne wrote from a decoded
// settings.json, reporting whether anything changed. An `env` block left empty goes with it, so
// switching away from the setting does not leave a bare `"env": {}` behind as residue.
//
// Shared by setup and by `arac disable` so the two cannot disagree about what aracne owns.
func dropAracneToolSearchEnv(settings map[string]interface{}) bool {
	env, _ := settings["env"].(map[string]interface{})
	if current, _ := env[claudeToolSearchEnvKey].(string); current != claudeToolSearchOff {
		return false
	}
	delete(env, claudeToolSearchEnvKey)
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	return true
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
	// The SUB-AGENT filter, not the main-agent one. Sub-agents declare their own scoped
	// server inline and have it in every mode, so their tools need pre-approving in every
	// mode; narrowing by the main agent's surface left an intercepting project prompting on
	// every `update_description` its descriptions executor made. The shell-served tools are
	// still dropped, so no rule here names something no server registers.
	names := cfg.SubAgentMCPTools(allMCPToolNames())
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
func claudeGuardHookForOS(goos, hooksDir string, global bool) claudeNativeEditHook {
	if goos == "windows" {
		return claudeNativeEditHook{
			scriptName: "arac-guard.ps1",
			content:    claudeGuardHookPowerShellScript(),
			command:    powershellHookCommand(hookScriptRef(hooksDir, "arac-guard.ps1", global)),
			shell:      "powershell",
		}
	}
	return claudeNativeEditHook{
		scriptName: "arac-guard.sh",
		content:    claudeGuardHookShellScript(),
		command:    bashHookCommand(hookScriptRef(hooksDir, "arac-guard.sh", global)),
		shell:      "bash",
	}
}

// hookScriptRef is how a settings.json entry names one of the scripts setup just wrote.
//
// A LOCAL install uses ${CLAUDE_PROJECT_DIR}, which Claude Code expands to the project root
// the session started in. That keeps the entry portable: it means the same thing in a
// teammate's checkout, which matters because settings.json is usually committed.
//
// A GLOBAL install must not use it. `arac setup --global` writes the scripts under the user's
// HOME (~/.claude/hooks) while ${CLAUDE_PROJECT_DIR} still expands to the PROJECT root -- so
// the entry named a file setup had never created, and in every project without a local copy
// the guard was simply dead: no interception, no pre-tool scan, no blocked_tools, no nudge.
// A global install names the absolute path it actually wrote to.
func hookScriptRef(hooksDir, name string, global bool) string {
	if !global {
		return "${CLAUDE_PROJECT_DIR}/.claude/hooks/" + name
	}
	path := filepath.Join(hooksDir, name)
	// shellAbs, not filepath.Abs: this reference is written into a config for a SHELL to run,
	// which is why it leaves here slash-spelled, and a hooks directory that is already
	// absolute must not be re-rooted. filepath.Abs on Windows qualifies `/Users/x/repo` with
	// the current drive, turning a path the caller gave in full into one under D:.
	if abs := shellAbs(path); abs != "" {
		path = abs
	}
	return filepath.ToSlash(path)
}

// bashHookCommand and powershellHookCommand wrap a script path for a SHELL-FORM hook.
//
// THE QUOTES ARE THE WHOLE POINT. Claude Code substitutes the path into the command string
// and hands the result to a shell, so an unquoted path word-splits on the first space. A
// project under "~/My Projects" therefore failed EVERY tool call with
// `bash: line 1: /Users/x/My: No such file or directory` and exit 127 -- the guard dead,
// loudly, on every turn, with nothing in aracne able to notice. The hooks reference says it
// outright: "In shell form, wrap each placeholder in double quotes."
//
// Double quotes rather than single, so the placeholder still expands if the harness ever
// leaves that to the shell rather than substituting it first.
func bashHookCommand(path string) string { return `"` + path + `"` }

// powershellHookCommand needs the call operator as well as the quotes: a quoted string on its
// own is an expression that evaluates to the path, not a command that runs it.
func powershellHookCommand(path string) string { return "& " + quoteForPowerShell(path) }

// aracBinary is the command a generated hook or plugin should run.
//
// The ABSOLUTE path of the binary writing the integration, when it can be resolved, and the
// bare name otherwise. `arac setup` is the one moment where the answer is known for certain,
// and the scripts used to hard-code `arac` and hope: a build kept at ./bin/arac, a Homebrew
// install whose shell a GUI-launched editor does not inherit, a login PATH the harness does
// not share -- each turned every tool call into a failing hook, not a quiet degradation.
// interceptCommand already resolves os.Executable() for exactly this reason.
//
// A VERSIONED path is not the same promise as an absolute one. EvalSymlinks turns a stable
// `/usr/local/bin/arac` into the `Cellar/arac/1.0.0/bin/arac` behind it, and the next upgrade
// deletes that file. The hook scripts and the OpenCode plugins survive it -- they fall back to
// `arac` on PATH when the recorded path is gone -- but the three MCP entries setup writes
// (.mcp.json, opencode.json, every generated agent's inline mcpServers) have no fallback at all:
// a server that cannot start is silent in both harnesses, the tool list simply comes back short.
// So when PATH holds a name for THE SAME FILE, that name is recorded instead. It keeps the
// property the absolute path was for, and loses the version pin.
func aracBinary() string { return aracBinaryFrom(os.Executable, exec.LookPath) }

// aracBinaryFrom is aracBinary over its two lookups, so a test can stand at both.
func aracBinaryFrom(executable func() (string, error), lookPath func(string) (string, error)) string {
	exe, err := executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return "arac"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		exe = resolved
	}
	// Same file, not same name: a stale `arac` earlier on PATH -- an older install, another
	// checkout -- would otherwise be written into the config in place of the binary that is
	// actually running this setup.
	if onPath, err := lookPath("arac"); err == nil && filepath.IsAbs(onPath) && sameFileOnDisk(onPath, exe) {
		return onPath
	}
	return exe
}

// sameFileOnDisk reports whether two paths name one file, following symlinks.
func sameFileOnDisk(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

// Returns a shell script that invokes the arac guard tool as a Claude hook
func claudeGuardHookShellScript() string {
	return shellHookScript(aracBinary(), "guard --claude-hook")
}

// Returns the PowerShell script for the native guard hook.
func claudeGuardHookPowerShellScript() string {
	return powerShellHookScript(aracBinary(), "guard --claude-hook")
}

// shellHookScript and powerShellHookScript are a hook script running `<arac> <args>`.
//
// The recorded ABSOLUTE path comes first (see aracBinary for why a bare name is a bet), and
// `arac` on PATH only when that path is not there. The settings entry that points at the script
// is deliberately portable -- ${CLAUDE_PROJECT_DIR} -- because settings.json is committed, and
// the script sits beside it and is committed with it: so a teammate's checkout, or this machine
// after the binary moved, ran `exec '/home/someone-else/go/bin/arac'` and failed every tool call
// with exit 127. Where the recorded binary exists nothing changes; where neither does, the
// failure is the same one it was.
func shellHookScript(binary, args string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"ARAC=" + quoteForShell(binary),
		`[ -x "$ARAC" ] || ARAC=arac`,
		`exec "$ARAC" ` + args,
		"",
	}, "\n")
}

func powerShellHookScript(binary, args string) string {
	return strings.Join([]string{
		"$inputJson = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($inputJson)) { exit 0 }",
		"$arac = " + quoteForPowerShell(binary),
		"if (-not (Test-Path -LiteralPath $arac -PathType Leaf)) { $arac = 'arac' }",
		"$inputJson | & $arac " + args,
		"",
	}, "\n")
}

// quoteForPowerShell wraps a path in single quotes for a PowerShell call operator, which
// needs them whenever the path contains a space -- a Windows install under "Program Files"
// being the ordinary case.
func quoteForPowerShell(p string) string {
	return "'" + strings.ReplaceAll(p, "'", "''") + "'"
}

// Returns OS-specific native edit hook configuration (PowerShell for Windows, shell script for others)
func claudeNativeEditHookForOS(goos, hooksDir string, global bool) claudeNativeEditHook {
	if goos == "windows" {
		return claudeNativeEditHook{
			scriptName: "arac-update-file.ps1",
			content:    claudeUpdateFileHookPowerShellScript(),
			command:    powershellHookCommand(hookScriptRef(hooksDir, "arac-update-file.ps1", global)),
			shell:      "powershell",
		}
	}
	return claudeNativeEditHook{
		scriptName: "arac-update-file.sh",
		content:    claudeUpdateFileHookShellScript(),
		command:    bashHookCommand(hookScriptRef(hooksDir, "arac-update-file.sh", global)),
		shell:      "bash",
	}
}

// Generates a PowerShell script that pipes stdin JSON to the arac update-file command with claude-hook flag
func claudeUpdateFileHookPowerShellScript() string {
	return powerShellHookScript(aracBinary(), "update-file --claude-hook")
}

// Generates a shell script that invokes arac update-file with the claude-hook flag
func claudeUpdateFileHookShellScript() string {
	return shellHookScript(aracBinary(), "update-file --claude-hook")
}

// aracJSLiteral is aracBinary() as a JSON string literal, safe to paste into a generated
// JavaScript plugin (a Windows path is full of backslashes).
func aracJSLiteral() string {
	b, err := json.Marshal(aracBinary())
	if err != nil {
		return `"arac"`
	}
	return string(b)
}

// writeOpenCodePreToolScanPlugin installs the OpenCode counterpart of the Claude Code
// PreToolUse guard hook: a plugin that runs the scan.pre_tool scan before each tool call and
// rewrites a shell read or search into the aracne answer.
//
// It is installed unconditionally, like the Claude guard hook, and NOT listed under
// `plugins` in the config. Which scan runs -- including none at all -- is decided at call
// time by `scan.pre_tool`, so flipping that knob takes effect without re-running init, and a
// project cannot end up with a fresh graph on one harness and a stale one on the other. The
// same holds for the rewrite: `arac guard --rewrite` reads the mode at call time, so a project
// that changes it gets the new behaviour on both harnesses without re-running setup.
//
// The file name predates the rewrite half and is kept: `arac disable` removes this plugin by
// name, and renaming it would strip the removal of an installed file rather than the file.
func writeOpenCodePreToolScanPlugin(pluginsDir string, autoYes bool) {
	os.MkdirAll(pluginsDir, 0755)
	writeMarkdownFile(filepath.Join(pluginsDir, "arac-pre-tool-scan.js"), "OpenCode pre-tool scan plugin", openCodePreToolScanPlugin(), autoYes)
}

// openCodePreToolScanPlugin returns the OpenCode plugin that keeps the topology current before
// a tool call and intercepts the shell reads and searches aracne answers.
//
// BOTH HALVES OF THE CLAUDE CODE PreToolUse HOOK, and its PostToolUse half, in the spelling this
// harness offers. The scan
// mirrors that hook's matcher (Read|Grep|Edit|Write|Bash) in OpenCode's tool names and defers
// to `arac guard --pre-scan`. The rewrite is the half OpenCode never had: Claude Code returns
// `hookSpecificOutput.updatedInput` and OpenCode hands `tool.execute.before` a mutable
// `output.args`, so the same decision -- made once, in interceptCommand, reached here through
// `arac guard --rewrite` -- lands through a different door. Without it, the AGENTS.md this same
// command writes promised an enriched `cat` and an annotated `grep` that nothing on this
// harness delivered.
//
// The after-half is the drift check: `arac guard --post-tool` after a shell call or a native
// edit, its report appended to the tool's output. Without it an OpenCode model heard of no
// topology warning at all. The shell command it classifies is the one the MODEL wrote,
// remembered before the rewrite replaced it: `cd d && cat f` is a read, while its rewrite,
// `cd d && arac cmd -- cat f`, would classify as an unknown command and cost a scan.
func openCodePreToolScanPlugin() string {
	return strings.TrimPrefix(`
import { execFile } from "node:child_process"
import { existsSync } from "node:fs"
import { promisify } from "node:util"

const run = promisify(execFile)

// The absolute path of the aracne binary that generated this plugin, so a harness whose
// PATH differs from the shell that ran "arac setup" still finds it -- and "arac" on PATH when
// that path is not there (another machine's checkout of this file, a moved binary).
const RECORDED_ARAC = `+aracJSLiteral()+`
const ARAC = existsSync(RECORDED_ARAC) ? RECORDED_ARAC : "arac"

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

// The tools that can change source, and so earn the post-call drift check.
const POST_TOOLS = new Set(["bash", "edit", "write", "patch", "apply_patch", "multi_edit", "multiedit"])

// OpenCode hands a plugin the GIT worktree as "worktree", which is "/" for a project that is
// not a repository and the enclosing repo's root for a project nested inside one -- neither is
// where .aracne lives. "directory" is the instance directory OpenCode was opened in, which is
// inside the project, and arac walks up from there to find the topology. Preferring worktree
// ran every arac call from "/", where update-file found no project and scanned the whole
// filesystem, with OpenCode's event loop waiting on it.
function aracRoot(directory, worktree) {
  for (const candidate of [directory, worktree]) {
    if (typeof candidate === "string" && candidate !== "" && candidate !== "/") return candidate
  }
  return process.cwd()
}

export const AracPreToolScan = async ({ directory, worktree }) => {
  const root = aracRoot(directory, worktree)
  // The command each shell call was WRITTEN as, by call id, captured before the rewrite.
  const written = new Map()

  return {
    "tool.execute.before": async (input, output) => {
      if (!SCANNED_TOOLS.has(input?.tool)) return
      if (input?.tool === "bash" && input?.callID !== undefined) {
        written.set(input.callID, output?.args?.command)
      }
      try {
        // Awaited on purpose: the scan is only worth running if it lands BEFORE the tool
        // reads the graph. Bounded and swallowed, like the Claude hook -- a scan that failed
        // must never turn into a tool call that failed.
        await run(ARAC, ["guard", "--pre-scan"], { cwd: root, timeout: 30000 })
      } catch {}

      // Interception. The command is replaced in place, so the model reads real stdout and has
      // nothing to recover from -- the same trade the Claude Code rewrite makes. Every guard
      // rail lives in "arac guard --rewrite": it declines across a pipe it cannot serve, a
      // redirect, a heredoc, a substitution, a mutation, an unindexed target and its own
      // output, and answers {} when there is nothing to do.
      if (input?.tool !== "bash") return
      const command = output?.args?.command
      if (typeof command !== "string" || command === "") return
      try {
        const { stdout } = await run(ARAC, ["guard", "--rewrite", command], {
          cwd: root,
          timeout: 15000,
        })
        const rewritten = JSON.parse(stdout || "{}").command
        if (typeof rewritten === "string" && rewritten !== "") {
          output.args.command = rewritten
        }
      } catch {}
    },

    // The drift check. Whatever the call changed on disk is re-indexed, and every topology
    // warning the model has not been shown yet is appended to the tool's output -- the same
    // report, from the same ledger, that the Claude Code guard attaches after a tool call.
    "tool.execute.after": async (input, output) => {
      if (!POST_TOOLS.has(input?.tool)) return
      const command = written.get(input?.callID) ?? input?.args?.command ?? ""
      written.delete(input?.callID)
      try {
        const args = ["guard", "--post-tool", input.tool]
        if (input.tool === "bash") args.push(typeof command === "string" ? command : "")
        const { stdout } = await run(ARAC, args, { cwd: root, timeout: `+fmt.Sprint(GuardHookTimeoutSeconds*1000)+` })
        const message = JSON.parse(stdout || "{}").message
        if (typeof message === "string" && message !== "") {
          output.output = (output.output ?? "") + "\n\n" + message
        }
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
import { existsSync } from "node:fs"
import path from "node:path"

// The absolute path of the aracne binary that generated this plugin, or "arac" on PATH when it
// is not there; see arac-pre-tool-scan.
const RECORDED_ARAC = `+aracJSLiteral()+`
const ARAC = existsSync(RECORDED_ARAC) ? RECORDED_ARAC : "arac"

// OpenCode hands a plugin the GIT worktree as "worktree", which is "/" for a project that is
// not a repository and the enclosing repo's root for a project nested inside one -- neither is
// where .aracne lives. "directory" is the instance directory OpenCode was opened in, which is
// inside the project, and arac walks up from there to find the topology. Preferring worktree
// ran every arac call from "/", where update-file found no project and scanned the whole
// filesystem, with OpenCode's event loop waiting on it.
function aracRoot(directory, worktree) {
  for (const candidate of [directory, worktree]) {
    if (typeof candidate === "string" && candidate !== "" && candidate !== "/") return candidate
  }
  return process.cwd()
}

export const AracNativeEditSync = async ({ directory, worktree }) => {
  const root = aracRoot(directory, worktree)

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

  // Syncs, and reports only a failure to sync. The warnings the edit caused are reported by
  // arac-pre-tool-scan.js's tool.execute.after, through the ledger the Claude Code guard uses;
  // reporting UpdateFile's own list here as well printed them twice.
  function updateFile(file) {
    file = normalizeFile(file)
    if (!file || shouldSkip(file)) return ""
    try {
      // Bounded like the pre-tool scan's execFile: this call is SYNCHRONOUS, so an arac that
      // never returns would freeze every later OpenCode tool call, not just this edit.
      execFileSync(ARAC, ["update-file", file], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 30000 })
      return ""
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

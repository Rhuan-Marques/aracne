package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// Disables Claude Code and/or OpenCode integrations either globally or per-project.
func RunDisable(args []string) {
	fs := flag.NewFlagSet("disable", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Disable Claude Code integration")
	opencode := fs.Bool("opencode", false, "Disable OpenCode integration")
	global := fs.Bool("global", false, "Disable globally")
	all := fs.Bool("all", false, "Disable all integrations")
	yes := fs.Bool("y", false, "Auto-confirm all prompts")
	fs.Parse(args)

	if !*claude && !*opencode && !*all {
		*claude = true
		*opencode = true
	}
	if *all {
		*claude = true
		*opencode = true
	}

	if *opencode {
		disableOpenCode(*global, *yes)
	}
	if *claude {
		disableClaudeCode(*global, *yes)
	}
}

// Removes aracne MCP server and integration from OpenCode configuration, resets permissions, and deletes related files.
func disableOpenCode(global bool, autoYes bool) {
	configPath, configDir, agentsMdPath := opencodePaths(global)

	config := readJSONConfig(configPath)
	changed := false

	if mcpMap, ok := config["mcp"].(map[string]interface{}); ok {
		removed := false
		for _, key := range []string{"aracne", "arac"} {
			if _, exists := mcpMap[key]; exists {
				delete(mcpMap, key)
				removed = true
			}
		}
		if removed {
			changed = true
			fmt.Println("[OpenCode] Removed aracne MCP server from config")
		}
		if len(mcpMap) == 0 {
			delete(config, "mcp")
		} else {
			config["mcp"] = mcpMap
		}
	}

	// Only the keys aracne wrote, and only back to their un-gated value.
	//
	// This used to assign a fresh five-key allow-everything map over `config["permission"]`,
	// which deleted every key the user had set: a `"webfetch": "deny"` vanished and a
	// structured `"bash": {"*": "allow", "rm *": "deny"}` collapsed to a blanket "allow".
	// Disabling a code-navigation tool must not re-enable things the operator turned off.
	// `write` and `grep` are not written at all -- setup deletes `write` and never writes
	// `grep`, so disable was inventing keys as well as destroying them.
	if permissionMap, ok := config["permission"].(map[string]interface{}); ok {
		for _, key := range []string{"read", "edit", "bash"} {
			// Only a value aracne could have written is un-gated, and only when it is
			// actually gating something. A scalar "deny" is aracne's; anything else --
			// a structured policy the operator wrote, an "ask" -- is theirs and stays.
			// Writing "allow" over it would be the same destruction in a smaller box.
			if permissionMap[key] == "deny" {
				permissionMap[key] = "allow"
				changed = true
			}
		}
		if bash, isMap := permissionMap["bash"].(map[string]interface{}); isMap {
			// The glob-map form: drop aracne's own deny patterns and leave every other
			// rule where it is. A map holding nothing but "*": "allow" afterwards was
			// entirely aracne's, so it collapses back to the scalar.
			for _, pattern := range append(append([]string{}, openCodeReadDenyPatterns...),
				openCodeGrepDenyPatterns...) {
				if bash[pattern] == "deny" {
					delete(bash, pattern)
					changed = true
				}
			}
			if len(bash) == 1 && bash["*"] == "allow" {
				permissionMap["bash"] = "allow"
			}
		}
		for key := range permissionMap {
			if key == "aracne_*" || strings.HasPrefix(key, "aracne_") {
				delete(permissionMap, key)
				changed = true
			}
		}
		if len(permissionMap) == 0 {
			delete(config, "permission")
		} else {
			config["permission"] = permissionMap
		}
	}

	if changed {
		if !confirmDisable(configPath, "OpenCode", autoYes) {
			fmt.Printf("[OpenCode] Skipping %s\n", configPath)
		} else if removeIfOnlyAracneWrote(configPath, config, opencodeAracneOnlyKeys) {
			fmt.Printf("[OpenCode] Removed %s (nothing left but the keys aracne added)\n", configPath)
		} else {
			writeJSONConfig(configPath, config)
			fmt.Printf("[OpenCode] Config updated at %s\n", configPath)
		}
	}

	removeFiles(filepath.Join(configDir, "commands"), []string{
		"descriptions-generate.md",
		"descriptions-apply.md",
		"descriptions_clear.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFiles(filepath.Join(configDir, "agents"), []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFile(filepath.Join(configDir, "plugins", "arac-native-edit-sync.js"),
		"OpenCode native edit sync plugin")
	removeFile(filepath.Join(configDir, "plugins", "arac-pre-tool-scan.js"),
		"OpenCode pre-tool scan plugin")

	removeAracneIntegrationSection(agentsMdPath, "OpenCode AGENTS.md")

	fmt.Println("[OpenCode] Aracne integration disabled. Restart OpenCode to apply changes.")
}

// Removes aracne MCP server and integration from Claude Code configuration and deletes related command/agent files.
func disableClaudeCode(global bool, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)

	config := readJSONConfig(mcpConfigPath)
	changed := false

	if mcpServers, ok := config["mcpServers"].(map[string]interface{}); ok {
		removed := false
		for _, key := range []string{"aracne", "arac"} {
			if _, exists := mcpServers[key]; exists {
				delete(mcpServers, key)
				removed = true
			}
		}
		if removed {
			changed = true
			fmt.Println("[Claude Code] Removed aracne MCP server from config")
		}
		if len(mcpServers) == 0 {
			delete(config, "mcpServers")
		} else {
			config["mcpServers"] = mcpServers
		}
	}

	if changed {
		if !confirmDisable(mcpConfigPath, "Claude Code", autoYes) {
			fmt.Printf("[Claude Code] Skipping %s\n", mcpConfigPath)
		} else {
			writeJSONConfig(mcpConfigPath, config)
			fmt.Printf("[Claude Code] Config updated at %s\n", mcpConfigPath)
		}
	}

	removeFiles(commandsDir, []string{
		"descriptions-generate.md",
		"descriptions-apply.md",
		"descriptions_clear.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	removeFiles(agentsDir, []string{
		"descriptions-generation-executor.md",
		"bug-hunter.md",
		"bug-judge.md",
		"bug-solver.md",
	})

	hooksDir := filepath.Join(filepath.Dir(commandsDir), "hooks")
	removeFiles(hooksDir, []string{
		"arac-update-file.ps1",
		"arac-update-file.sh",
		"arac-guard.ps1",
		"arac-guard.sh",
	})

	settingsPath := filepath.Join(filepath.Dir(commandsDir), "settings.json")
	removeAracneHookFromSettings(settingsPath)
	// The hooks are not the only thing setup wrote into settings.json. Leaving the
	// `mcp__aracne__*` allow rules behind is the visible residue of an uninstall that
	// claims to be complete, and upsertAracneAllowRules already knows how to strip them.
	removeAracnePermissionsFromSettings(settingsPath)
	// And a settings.json that now holds `{}` is residue too: aracne created that file, and
	// nothing of anyone else's is in it. A file with any user content left is untouched.
	if removeIfOnlyAracneWrote(settingsPath, readJSONConfig(settingsPath), nil) {
		fmt.Printf("[Claude Code] Removed %s (nothing left but what aracne added)\n", settingsPath)
	}

	removeAracneIntegrationSection(claudeMdPath, "Claude Code CLAUDE.md")

	fmt.Println("[Claude Code] Aracne integration disabled. Restart Claude Code to apply changes.")
}

// opencodeAracneOnlyKeys is the permission block `arac setup` writes into a project that had
// none: the three native gates, un-gated back to "allow" by the time disable gets here.
var opencodeAracneOnlyKeys = map[string]interface{}{
	"read": "allow", "edit": "allow", "bash": "allow",
}

// removeIfOnlyAracneWrote deletes a harness config file that aracne created and that now holds
// nothing but the keys aracne itself put there, reporting whether it did.
//
// "Only un-gate, never destroy" is the right rule for a file that existed BEFORE setup ran --
// disabling a code-navigation tool must not re-enable things the operator turned off. It is not
// the right rule for a file setup created from nothing: `arac disable` left behind a
// `.claude/settings.json` containing `{}` and an `.opencode/opencode.json` containing three
// permission keys nobody had asked for, as the visible residue of an uninstall that claims to
// be complete.
//
// Deliberately conservative: one unrecognized key, one different value, anything at all that is
// not aracne's own leaves the file exactly where it is.
func removeIfOnlyAracneWrote(path string, config map[string]interface{}, aracneOnly map[string]interface{}) bool {
	switch len(config) {
	case 0:
		// Nothing left at all -- e.g. a settings.json whose only content was aracne's hooks.
	case 1:
		perms, ok := config["permission"].(map[string]interface{})
		if !ok || len(perms) != len(aracneOnly) {
			return false
		}
		for k, want := range aracneOnly {
			if perms[k] != want {
				return false
			}
		}
	default:
		return false
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	return true
}

// removeAracnePermissionsFromSettings drops the `mcp__aracne__*` pre-approvals setup wrote,
// leaving every user-defined rule where it is.
func removeAracnePermissionsFromSettings(settingsPath string) {
	config := readJSONConfig(settingsPath)
	permissions, ok := config["permissions"].(map[string]interface{})
	if !ok {
		return
	}
	allow, _ := permissions["allow"].([]interface{})
	kept := upsertAracneAllowRules(allow, nil)
	if len(kept) == len(allow) {
		return
	}
	if len(kept) == 0 {
		delete(permissions, "allow")
	} else {
		permissions["allow"] = kept
	}
	if len(permissions) == 0 {
		delete(config, "permissions")
	} else {
		config["permissions"] = permissions
	}
	writeJSONConfig(settingsPath, config)
	fmt.Printf("[Claude Code] Removed aracne MCP tool permissions from settings\n")
}

// confirmDisable asks before rewriting a harness config file, and reports whether to proceed.
//
// It exists so `-y` means something: both disable paths took the flag and ignored it, so the
// command rewrote configs unprompted while advertising a switch that suppressed a prompt it
// did not have. The question is only asked where someone can answer it -- an unattended run
// (a CI step, a Makefile, a script) proceeds exactly as it did before, so nothing that worked
// yesterday now hangs waiting on stdin.
func confirmDisable(path, label string, autoYes bool) bool {
	if autoYes || !stdinIsTerminal() {
		return true
	}
	fmt.Printf("[%s] Remove the aracne entries from %s? [y/N] ", label, path)
	answer, _ := bufio.NewReader(promptReader).ReadString('\n')
	switch strings.TrimSpace(strings.ToLower(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// Removes multiple files by name from a directory.
func removeFiles(dir string, names []string) {
	for _, name := range names {
		removeFile(filepath.Join(dir, name), name)
	}
}

// Removes a file if it exists, logging success or errors to stderr.
func removeFile(path, label string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", label, err)
		return
	}
	fmt.Printf("Removed %s\n", path)
}

// Removes aracne hook entries from PreToolUse/PostToolUse events in settings JSON.
func removeAracneHookFromSettings(settingsPath string) {
	config := readJSONConfig(settingsPath)
	if len(config) == 0 {
		return
	}

	hooks, ok := config["hooks"].(map[string]interface{})
	if !ok {
		return
	}

	changed := false
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		if removeAracneHookEntries(hooks, event) {
			changed = true
		}
	}

	if !changed {
		return
	}
	if len(hooks) == 0 {
		delete(config, "hooks")
	} else {
		config["hooks"] = hooks
	}
	writeJSONConfig(settingsPath, config)
	fmt.Printf("[Claude Code] Settings updated at %s\n", settingsPath)
}

// removeAracneHookEntries strips every aracne-installed entry (one whose hook
// command references `arac`) from a hook-event list, covering both the
// edit-sync and guard hooks while preserving any user entries. It reports
// whether anything was removed.
func removeAracneHookEntries(hooks map[string]interface{}, event string) bool {
	entries, ok := hooks[event].([]interface{})
	if !ok {
		return false
	}
	filtered := make([]interface{}, 0, len(entries))
	changed := false
	for _, entry := range entries {
		if isAracneHookEntry(entry) {
			changed = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !changed {
		return false
	}
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
	fmt.Printf("[Claude Code] Removed aracne %s hook from settings\n", event)
	return true
}

// isAracneHookEntry reports whether a hook entry is one `arac setup` installed, identified by
// the SCRIPT NAME it wrote rather than by a substring of it.
//
// The test used to be `strings.Contains(cmd, "arac")`, which matched any user hook whose
// command happened to contain those four letters anywhere -- `characterize.sh`, `barracuda-lint`,
// a path under `~/aracnid/` -- and `arac disable` then deleted it from a settings file aracne
// does not own, with no backup and no diff. `arac disable`'s whole promise is that it removes
// only what setup added.
//
// Two shapes are aracne's, and both have to keep being removed:
//
//   - the script hooks native_hooks.go writes today, `arac-guard.*` and `arac-update-file.*`,
//     recognised through that file's own predicates so the writer and the remover cannot name
//     different things;
//   - a bare `arac <subcommand>` command, which earlier versions installed directly. `arac
//     disable` has to clean up after an older binary for the same reason setup still matches
//     AracIntegrationLegacyStart.
//
// The second is matched on the COMMAND WORD -- the first token, base-named, `.exe` stripped --
// not on a substring, which is what keeps `characterize.sh`, `barracuda-lint` and
// `~/aracnid/run.sh` out of it.
func isAracneHookEntry(entry interface{}) bool {
	if isGuardHookEntry(entry) || isEditSyncHookEntry(entry) {
		return true
	}
	return hookEntryCommandWordIs(entry, "arac")
}

// hookEntryCommandWordIs reports whether any of a hook entry's commands INVOKES the named
// binary, as opposed to merely mentioning it somewhere in its text.
func hookEntryCommandWordIs(entry interface{}, binary string) bool {
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
		if baseName(strings.ToLower(hookCommandWord(cmd))) == binary {
			return true
		}
	}
	return false
}

// Strips aracne integration segment from a file and rewrites it if changed.
func removeAracneIntegrationSection(path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		return
	}

	existing := string(data)
	updated := stripAracneIntegrationSegment(existing)

	if updated == existing {
		fmt.Printf("%s already clean at %s\n", label, path)
		return
	}
	if err := helper.AtomicWriteFile(path, []byte(updated), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
		return
	}
	fmt.Printf("%s updated at %s\n", label, path)
}

// Removes the aracne integration markdown section from content, preserving line endings and spacing.
func stripAracneIntegrationSegment(content string) string {
	lineEnding := markdownLineEnding(content)

	// The SAME bounds `arac setup` replaces, including its fallback for a block whose closing
	// line the reader edited away. Asking a different question here is what made
	// `arac disable` report "already clean" and leave the whole contract behind.
	start, endAfter, ok := aracIntegrationBounds(content)
	if !ok {
		return content
	}

	for endAfter < len(content) {
		ch := content[endAfter]
		if ch == '\n' || ch == '\r' {
			endAfter++
			if ch == '\r' && endAfter < len(content) && content[endAfter] == '\n' {
				endAfter++
			}
		} else {
			break
		}
	}

	beforeSection := strings.TrimRight(content[:start], " \t\r\n")
	if beforeSection != "" {
		beforeSection += lineEnding
		if endAfter < len(content) {
			beforeSection += lineEnding
		}
	}

	return beforeSection + content[endAfter:]
}

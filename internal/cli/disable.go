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

	// Only the keys aracne wrote, and only back to what they were.
	//
	// This used to assign a fresh five-key allow-everything map over `config["permission"]`,
	// which deleted every key the user had set: a `"webfetch": "deny"` vanished and a
	// structured `"bash": {"*": "allow", "rm *": "deny"}` collapsed to a blanket "allow".
	// Disabling a code-navigation tool must not re-enable things the operator turned off.
	// `write` and `grep` are not written at all -- setup deletes `write` and never writes
	// `grep`, so disable was inventing keys as well as destroying them.
	state, recorded := loadSetupState(configPath)
	if permissionMap, ok := config["permission"].(map[string]interface{}); ok {
		if recorded {
			// Exactly the keys setup recorded tightening, put back as it found them -- a
			// scalar, a structured policy, or nothing at all. Every other key is the
			// operator's whatever its value: an "edit": "deny" setup left alone is their
			// decision, not a gate to lift.
			if restoreOpenCodeNativePermissions(permissionMap, state) {
				changed = true
			}
		} else if unGateLegacyNativePermissions(permissionMap) {
			changed = true
		}
		for key := range permissionMap {
			if key == "aracne_*" || strings.HasPrefix(key, "aracne_") {
				delete(permissionMap, key)
				changed = true
			}
		}
		switch {
		case len(permissionMap) == 0:
			delete(config, "permission")
		case state.PermissionScalar != "" && len(permissionMap) == 1 && permissionMap["*"] == state.PermissionScalar:
			// Setup spelled the operator's `"permission": "ask"` as `{"*": "ask"}` to make
			// room for its own keys; with them gone it goes back to how it was written.
			config["permission"] = state.PermissionScalar
		default:
			config["permission"] = permissionMap
		}
	}

	// The record goes once it has been acted on, and stays while it has not: a declined
	// prompt leaves the file as setup wrote it, and a later disable still needs to know how
	// to undo it.
	keepState := false
	if changed {
		if !confirmDisable(configPath, "OpenCode", autoYes) {
			fmt.Printf("[OpenCode] Skipping %s\n", configPath)
			keepState = true
		} else if removeIfOnlyAracneWrote(configPath, config, !recorded || state.Created) {
			fmt.Printf("[OpenCode] Removed %s (aracne created it, and nothing is left in it)\n", configPath)
		} else {
			writeJSONConfig(configPath, config)
			fmt.Printf("[OpenCode] Config updated at %s\n", configPath)
		}
	}
	if !keepState {
		removeSetupState(configPath)
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

	removeAracneIntegrationSection(agentsMdPath, "OpenCode AGENTS.md", !recorded || state.ContractCreated)
	removeCreatedDirs(configDir, openCodeIntegrationDirs, state, recorded)

	fmt.Println("[OpenCode] Aracne integration disabled. Restart OpenCode to apply changes.")
}

// Removes aracne MCP server and integration from Claude Code configuration and deletes related command/agent files.
func disableClaudeCode(global bool, autoYes bool) {
	mcpConfigPath, commandsDir, agentsDir, claudeMdPath := claudePaths(global)
	settingsPath := filepath.Join(filepath.Dir(commandsDir), "settings.json")
	settingsState, settingsRecorded := loadSetupState(settingsPath)

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
		// The key stays even when the last server is gone -- Claude Code rejects a project
		// `.mcp.json` that has no `mcpServers`. removeIfOnlyAracneWrote still deletes the
		// file outright when aracne created it; this is the shape a file we may not delete
		// has to survive in.
		config["mcpServers"] = mcpServers
	}

	// The record goes once it has been acted on, and stays while it has not -- the same rule
	// disableOpenCode follows. A declined prompt leaves .mcp.json as setup wrote it, and the
	// next `arac disable` still needs the record to know it may delete the file rather than
	// only empty it.
	keepState := false
	if changed {
		if !confirmDisable(mcpConfigPath, "Claude Code", autoYes) {
			fmt.Printf("[Claude Code] Skipping %s\n", mcpConfigPath)
			keepState = true
		} else if removeIfOnlyAracneWrote(mcpConfigPath, config, !settingsRecorded || settingsState.MCPConfigCreated) {
			fmt.Printf("[Claude Code] Removed %s (aracne created it, and nothing is left in it)\n", mcpConfigPath)
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

	removeAracneHookFromSettings(settingsPath)
	// The hooks are not the only thing setup wrote into settings.json. Leaving the
	// `mcp__aracne__*` allow rules behind is the visible residue of an uninstall that
	// claims to be complete, and upsertAracneAllowRules already knows how to strip them.
	removeAracnePermissionsFromSettings(settingsPath)
	// Same for the tool-search opt-out: a disabled integration that goes on suppressing tool
	// search for every tool in the session is residue with a cost attached.
	removeAracneToolSearchEnvFromSettings(settingsPath)
	// And a settings.json that now holds `{}` is residue too -- when aracne created it. One the
	// operator already had stays, even emptied: it was theirs before setup ran.
	if removeIfOnlyAracneWrote(settingsPath, readJSONConfig(settingsPath), !settingsRecorded || settingsState.Created) {
		fmt.Printf("[Claude Code] Removed %s (aracne created it, and nothing is left in it)\n", settingsPath)
	}
	if !keepState {
		removeSetupState(settingsPath)
	}

	removeAracneIntegrationSection(claudeMdPath, "Claude Code CLAUDE.md", !settingsRecorded || settingsState.ContractCreated)
	removeCreatedDirs(filepath.Dir(commandsDir), claudeIntegrationDirs, settingsState, settingsRecorded)

	fmt.Println("[Claude Code] Aracne integration disabled. Restart Claude Code to apply changes.")
}

// unGateLegacyNativePermissions lifts the read/edit/bash gates from an OpenCode permission block
// that an older binary set up, reporting whether anything changed.
//
// Those binaries recorded nothing (see setupState), so what they wrote has to be recognised by
// its shape -- the same reason disable still removes a bare `arac` hook and matches
// AracIntegrationLegacyStart. It is only reached when there is no record: a file set up by this
// binary has one, and disable puts back exactly what it says and nothing else.
func unGateLegacyNativePermissions(permissionMap map[string]interface{}) bool {
	changed := false
	for _, key := range openCodeNativeKeys {
		// Only a value aracne could have written is un-gated, and only when it is actually
		// gating something. A scalar "deny" is aracne's; anything else -- a structured policy
		// the operator wrote, an "ask" -- is theirs and stays. Writing "allow" over it would be
		// the same destruction in a smaller box.
		if permissionMap[key] == "deny" {
			permissionMap[key] = "allow"
			changed = true
		}
	}
	if bash, isMap := permissionMap["bash"].(map[string]interface{}); isMap {
		// The glob-map form: drop aracne's own deny patterns and leave every other rule where
		// it is. A map holding nothing but "*": "allow" afterwards was entirely aracne's, so it
		// collapses back to the scalar.
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
	return changed
}

// removeIfOnlyAracneWrote deletes a harness config file that aracne created and that disable has
// now emptied, reporting whether it did.
//
// "Only un-gate, never destroy" is the right rule for a file that existed BEFORE setup ran --
// disabling a code-navigation tool must not re-enable things the operator turned off. It is not
// the right rule for a file setup created from nothing: `arac disable` left behind a
// `.claude/settings.json` containing `{}` as the visible residue of an uninstall that claims to be
// complete.
//
// WHO CREATED THE FILE IS RECORDED, NOT INFERRED. This used to decide by shape -- a file holding
// only the three keys setup wrote, with setup's values -- and setup wrote those keys over the
// operator's own, so a pre-existing `.opencode/opencode.json` came out of `arac setup` in exactly
// that shape and `arac disable` deleted it. created is what setup recorded (setupState.Created).
// A file set up by an older binary has no record and is passed as created, which only ever
// removes it when there is nothing at all left in it -- the one case with nothing to lose.
func removeIfOnlyAracneWrote(path string, config map[string]interface{}, created bool) bool {
	if !created || !configIsSpent(config) {
		return false
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	return true
}

// configIsSpent reports whether a harness config holds nothing worth keeping the file for.
//
// An empty `mcpServers` counts as nothing. Claude Code validates a project `.mcp.json` against a
// schema that REQUIRES the key: a file left as `{}` is not an empty config, it is a broken one,
// and every session in the project opens with `mcpServers: Invalid input`. So the two places that
// drop the aracne server keep the key and empty the map instead of deleting it -- which means the
// "is there anything left?" question this answers can no longer be `len(config) == 0`.
func configIsSpent(config map[string]interface{}) bool {
	for key, value := range config {
		if key != "mcpServers" {
			return false
		}
		if servers, ok := value.(map[string]interface{}); !ok || len(servers) != 0 {
			return false
		}
	}
	return true
}

// removeAracnePermissionsFromSettings drops the `mcp__aracne__*` pre-approvals setup wrote,
// leaving every user-defined rule where it is.
// removeAracneToolSearchEnvFromSettings strips the ENABLE_TOOL_SEARCH opt-out setup wrote,
// leaving any other value -- which is one the operator chose -- and every other env variable
// in place.
func removeAracneToolSearchEnvFromSettings(settingsPath string) {
	config := readJSONConfig(settingsPath)
	if !dropAracneToolSearchEnv(config) {
		return
	}
	writeJSONConfig(settingsPath, config)
	fmt.Printf("[Claude Code] Removed %s from %s\n", claudeToolSearchEnvKey, settingsPath)
}

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

// Strips aracne integration segment from a file and rewrites it if changed. created says setup
// made the file (or nothing recorded otherwise): one left blank by the strip is then removed
// rather than kept as a 0-byte residue, while a file the operator had stays however empty.
func removeAracneIntegrationSection(path, label string, created bool) {
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
	if created && strings.TrimSpace(updated) == "" {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", path, err)
			return
		}
		fmt.Printf("%s removed at %s (aracne created it, and nothing is left in it)\n", label, path)
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

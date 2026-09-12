package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// setupState is what `arac setup` remembers about a harness config file it merges into but does
// not own -- `.opencode/opencode.json`, `.claude/settings.json` -- so that `arac disable` can put
// the file back the way setup found it.
//
// Both facts it holds are ones the file cannot answer afterwards:
//
//   - whether setup CREATED the file. disable used to guess from the shape of what was left --
//     "nothing but keys aracne writes" -- and a user's own opencode.json whose read/edit/bash
//     setup had just overwritten with aracne's values had exactly that shape, so disable
//     deleted it.
//   - what an OpenCode permission setup tightened WAS before. blocked_tools needs a deny written
//     over the user's read/edit/bash, and nothing derives `{"*": "allow", "*.env": "deny"}` back
//     from the "deny" that replaced it.
//
// It lives beside the file it describes (setupStatePath), so a project install and a --global
// one each keep their own, and a harness config that is committed and cloned carries it along.
type setupState struct {
	// Created is true when setup wrote the file into existence.
	Created bool `json:"created"`
	// Permission holds, per OpenCode permission key setup tightened, the user's value and the
	// value setup wrote over it.
	Permission map[string]permissionOverride `json:"permission,omitempty"`
	// PermissionScalar is the user's `"permission": "<action>"` when the whole block was one
	// action, which setup has to spell as `{"*": "<action>"}` to add its own keys beside it.
	PermissionScalar string `json:"permission_scalar,omitempty"`

	// The rest of what this harness's setup made from nothing, so that disable can remove it
	// once it has taken aracne's content back out -- and leave alone whatever the operator had
	// before. Without them disable left a 0-byte CLAUDE.md / AGENTS.md, a `.mcp.json` holding
	// `{}`, and the empty directories it had made.
	//
	// ContractCreated: the CLAUDE.md / AGENTS.md the contract is written into.
	ContractCreated bool `json:"contract_created,omitempty"`
	// MCPConfigCreated: Claude Code's `.mcp.json` (`~/.claude.json` under --global).
	MCPConfigCreated bool `json:"mcp_config_created,omitempty"`
	// CreatedDirs: directories setup created, relative to the one holding this record ("."
	// for that directory itself).
	CreatedDirs []string `json:"created_dirs,omitempty"`
}

// The directories setup writes into, relative to each harness's base directory (.claude,
// .opencode, or their --global equivalents), and so the ones disable may find empty.
var (
	claudeIntegrationDirs   = []string{"commands", "agents", "hooks"}
	openCodeIntegrationDirs = []string{"commands", "agents", "plugins"}
)

// noteIntegrationCreation records which of the contract file and the integration directories
// (baseDir itself and subdirs) this run is about to create. Like noteConfigCreation it must run
// before the first write, and a re-run never forgets what an earlier one created.
func noteIntegrationCreation(state *setupState, baseDir string, subdirs []string, contractPath string) {
	if !pathExists(contractPath) {
		state.ContractCreated = true
	}
	for _, dir := range append([]string{"."}, subdirs...) {
		if pathExists(filepath.Join(baseDir, dir)) {
			continue
		}
		known := false
		for _, d := range state.CreatedDirs {
			known = known || d == dir
		}
		if !known {
			state.CreatedDirs = append(state.CreatedDirs, dir)
		}
	}
}

// removeCreatedDirs removes the integration directories disable has left empty: the ones the
// record says setup created, or -- for an install an older binary made, which recorded nothing --
// every one setup writes into, on the same "created unless recorded otherwise" rule
// removeIfOnlyAracneWrote applies. A directory that still holds anything is never touched.
func removeCreatedDirs(baseDir string, subdirs []string, state setupState, recorded bool) {
	dirs := state.CreatedDirs
	if !recorded {
		dirs = append([]string{"."}, subdirs...)
	}
	// baseDir itself last, once its subdirectories have had their chance to go.
	ordered := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir != "." {
			ordered = append(ordered, dir)
		}
	}
	if len(ordered) < len(dirs) {
		ordered = append(ordered, ".")
	}
	for _, dir := range ordered {
		path := filepath.Join(baseDir, dir)
		if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
			continue
		}
		if os.Remove(path) == nil {
			fmt.Printf("Removed empty directory %s\n", path)
		}
	}
}

// pathExists reports whether anything -- file or directory -- is at path.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// permissionOverride is one tightened OpenCode permission key.
type permissionOverride struct {
	// Original is the user's value; nil when the key was absent.
	Original interface{} `json:"original"`
	// Written is what setup put there, so a later hand edit is told apart from setup's own
	// value and left alone.
	Written interface{} `json:"written"`
}

// setupStateFileName is the record's name, in the directory of the file it describes. OpenCode
// and Claude Code both read their config by exact file name, so neither harness sees it.
const setupStateFileName = "aracne-setup.json"

// setupStatePath is where the record for a harness config file lives.
func setupStatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), setupStateFileName)
}

// loadSetupState reads the record for configPath. ok is false when there is none: a file setup
// never touched, or one an older binary set up, which recorded nothing. An unreadable record
// reads as none -- the conservative answer, under which disable deletes nothing it cannot prove
// is aracne's.
func loadSetupState(configPath string) (setupState, bool) {
	data, err := os.ReadFile(setupStatePath(configPath))
	if err != nil {
		return setupState{}, false
	}
	var state setupState
	if err := json.Unmarshal(data, &state); err != nil {
		return setupState{}, false
	}
	return state, true
}

// saveSetupState writes the record for configPath. A failure is reported rather than fatal: the
// integration itself is already written, and what is lost is only disable's ability to undo the
// file exactly.
func saveSetupState(configPath string, state setupState) {
	out, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		err = helper.AtomicWriteFile(setupStatePath(configPath), append(out, '\n'), 0644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not record setup state for %s: %v\n", configPath, err)
	}
}

// removeSetupState drops the record once disable has used it.
func removeSetupState(configPath string) {
	if err := os.Remove(setupStatePath(configPath)); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", setupStatePath(configPath), err)
	}
}

// noteConfigCreation records whether setup is about to create configPath, so it must run before
// the first write. A file that exists keeps whatever the record already says: a re-run must not
// forget that an earlier one created it.
func noteConfigCreation(state *setupState, configPath string) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		state.Created = true
	}
}

// openCodePermissionBlock returns config's permission block as a map setup can add keys to.
//
// OpenCode also takes the whole block as one action -- `"permission": "ask"` -- and setup used to
// read anything that was not a map as no block at all, replacing an operator's blanket "ask" with
// aracne's keys alone: every tool silently back to OpenCode's default "allow". `{"*": "ask"}`
// means the same thing and has room for aracne's keys; the scalar is remembered so disable can
// put it back as written.
func openCodePermissionBlock(config map[string]interface{}, state *setupState) map[string]interface{} {
	switch block := config["permission"].(type) {
	case map[string]interface{}:
		return block
	case string:
		state.PermissionScalar = block
		return map[string]interface{}{"*": block}
	}
	return make(map[string]interface{})
}

// openCodeNativeKeys are the OpenCode permission keys the main agent's blocked_tools gate.
var openCodeNativeKeys = []string{"read", "edit", "bash"}

// applyOpenCodeNativePermissions writes the main agent's blocked_tools into an OpenCode
// permission block -- tightening only, and recording in state what it tightened.
//
// Per key: where aracne refuses nothing, the operator's value stands untouched, or is put back
// if an earlier setup gated it and nobody has edited it since. Where aracne must refuse
// something, the refusal is written on top of the operator's value, never in place of a looser
// one (openCodeNativeGate), and their value is recorded for disable.
//
// Idempotent across re-runs: a key still holding what setup wrote last time is recomputed from
// the recorded original rather than from setup's own output, so a second run can never record
// aracne's value as the user's.
func applyOpenCodeNativePermissions(perms map[string]interface{}, blocked map[string]bool, state *setupState) {
	for _, key := range openCodeNativeKeys {
		base := perms[key]
		if prior, overridden := state.Permission[key]; overridden && jsonEqual(base, prior.Written) {
			base = prior.Original
		}
		delete(state.Permission, key)
		gate, gated := openCodeNativeGate(key, base, blocked)
		if !gated {
			setPermissionKey(perms, key, base)
			continue
		}
		setPermissionKey(perms, key, gate)
		if state.Permission == nil {
			state.Permission = map[string]permissionOverride{}
		}
		state.Permission[key] = permissionOverride{Original: base, Written: gate}
	}
}

// openCodeNativeGate is what key must hold for the main agent's blocked_tools, applied on top of
// the operator's value base. gated is false when blocked_tools refuses nothing through this key.
func openCodeNativeGate(key string, base interface{}, blocked map[string]bool) (interface{}, bool) {
	switch key {
	case "read":
		return "deny", blocked["read"]
	case "edit":
		return "deny", blocked["edit"] || blocked["write"]
	}
	// bash: the whole tool, or only the direct read/grep forms.
	switch gate := openCodeBashPermission(blocked).(type) {
	case string:
		return gate, gate == "deny"
	case map[string]interface{}:
		return withBashDenyPatterns(base, gate), true
	}
	return nil, false
}

// withBashDenyPatterns adds the deny patterns of gate (openCodeBashPermission's glob form) to the
// operator's bash value instead of replacing it.
//
// The glob form opens with `"*": "allow"`, which is only right for a project that said nothing:
// written over `"bash": "ask"` it turned every command the operator wanted to confirm into one
// that runs unasked. So the operator's rules stay, their scalar becomes the "*" rule it already
// meant, and an absent key gets no "*" at all -- OpenCode's own default, or the operator's
// global config, still decides every command the patterns do not name.
func withBashDenyPatterns(base interface{}, gate map[string]interface{}) interface{} {
	rules := map[string]interface{}{}
	switch v := base.(type) {
	case string:
		if v == "deny" {
			return v // already refuses every command
		}
		rules["*"] = v
	case map[string]interface{}:
		for pattern, action := range v {
			rules[pattern] = action
		}
	}
	for pattern, action := range gate {
		if action == "deny" {
			rules[pattern] = action
		}
	}
	return rules
}

// restoreOpenCodeNativePermissions puts back every key setup recorded tightening, reporting
// whether any changed. A key edited since setup wrote it holds the operator's newer decision
// and is left alone.
func restoreOpenCodeNativePermissions(perms map[string]interface{}, state setupState) bool {
	changed := false
	for key, prior := range state.Permission {
		if !jsonEqual(perms[key], prior.Written) {
			continue
		}
		setPermissionKey(perms, key, prior.Original)
		changed = true
	}
	return changed
}

// setPermissionKey sets key in an OpenCode permission block, or removes it for nil: a key that
// was absent is put back absent, not as a JSON null.
func setPermissionKey(perms map[string]interface{}, key string, value interface{}) {
	if value == nil {
		delete(perms, key)
		return
	}
	perms[key] = value
}

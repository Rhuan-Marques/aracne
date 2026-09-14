package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// `arac setup` merges into harness config files the operator owns, and `arac disable` must give
// them back as found. The fixtures below are the audit's repro: an OpenCode project with its own
// read/edit/bash policy, which setup flattened to "allow" and disable then deleted outright.

const operatorOpenCodeConfig = `{"permission":{"edit":"ask","bash":"ask","read":{"*":"allow","*.env":"deny"}}}`

// inProject runs the test in a fresh project directory with HOME pointed away from the real one.
func inProject(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	setHome(t, filepath.Join(dir, "home"))
}

func writeOpenCodeConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".opencode", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".opencode/opencode.json", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func openCodePermissions(t *testing.T) interface{} {
	t.Helper()
	return readJSONConfig(".opencode/opencode.json")["permission"]
}

func decodeJSON(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// blockingConfig is a project whose OpenCode main agent blocks the given native tools, in the
// mode where blocked_tools applies to all of them.
func blockingConfig(tools ...string) *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.LLM.OpenCode.MainAgent.BlockedTools = tools
	return cfg
}

// Nothing blocked: setup adds its own keys and leaves the operator's exactly as they were, and
// disable hands the file back byte-for-byte in meaning -- not deleted, not loosened.
func TestSetupLeavesTheOperatorsOpenCodePermissionsAlone(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, operatorOpenCodeConfig)
	original := decodeJSON(t, operatorOpenCodeConfig).(map[string]interface{})["permission"].(map[string]interface{})

	initOpenCode(false, helper.DefaultConfig(), true, nil)

	perms, _ := openCodePermissions(t).(map[string]interface{})
	for _, key := range []string{"read", "edit", "bash"} {
		if !jsonEqual(perms[key], original[key]) {
			t.Errorf("setup rewrote permission.%s: %v, want %v", key, perms[key], original[key])
		}
	}
	if perms["aracne_*"] != "deny" {
		t.Errorf("setup must still withhold the aracne tools: %v", perms)
	}

	disableOpenCode(false, true)

	if _, err := os.Stat(".opencode/opencode.json"); err != nil {
		t.Fatalf("disable deleted an opencode.json the operator had before setup: %v", err)
	}
	if got := readJSONConfig(".opencode/opencode.json"); !jsonEqual(got, decodeJSON(t, operatorOpenCodeConfig)) {
		t.Fatalf("disable did not restore the operator's config:\n got %v\nwant %s", got, operatorOpenCodeConfig)
	}
	checkFileNotExist(t, setupStatePath(".opencode/opencode.json"), "setup record")
}

// A fresh project gets no read/edit/bash keys at all: OpenCode's default is already "allow",
// and an explicit "allow" on read also overrode OpenCode's own ask on `*.env`.
func TestSetupWritesNoNativeKeysWhereNothingIsBlocked(t *testing.T) {
	inProject(t)
	initOpenCode(false, helper.DefaultConfig(), true, nil)
	perms, _ := openCodePermissions(t).(map[string]interface{})
	for _, key := range []string{"read", "edit", "bash"} {
		if value, present := perms[key]; present {
			t.Errorf("permission.%s = %v, want it unwritten", key, value)
		}
	}
}

// blocked_tools does need a deny, and gets one -- on top of the operator's policy, never in
// place of a looser one, remembered, idempotent across re-runs, and undone by disable.
func TestSetupRemembersWhatBlockedToolsTightens(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, operatorOpenCodeConfig)
	cfg := blockingConfig("read", "edit")

	initOpenCode(false, cfg, true, nil)
	first, _ := os.ReadFile(".opencode/opencode.json")
	firstState, _ := os.ReadFile(setupStatePath(".opencode/opencode.json"))

	perms, _ := openCodePermissions(t).(map[string]interface{})
	if perms["read"] != "deny" || perms["edit"] != "deny" {
		t.Fatalf("blocked read/edit must be denied: %v", perms)
	}
	// A blocked read denies the direct shell reads, on top of the operator's "ask" -- which
	// becomes the "*" rule it already meant, not an "allow".
	bash, _ := perms["bash"].(map[string]interface{})
	if bash["*"] != "ask" || bash["cat *"] != "deny" {
		t.Fatalf("bash = %v, want the operator's ask plus aracne's deny patterns", perms["bash"])
	}

	initOpenCode(false, cfg, true, nil)
	second, _ := os.ReadFile(".opencode/opencode.json")
	secondState, _ := os.ReadFile(setupStatePath(".opencode/opencode.json"))
	if string(first) != string(second) || string(firstState) != string(secondState) {
		t.Fatalf("a re-run changed the result:\nconfig %s\n   vs %s\nrecord %s\n   vs %s", first, second, firstState, secondState)
	}

	disableOpenCode(false, true)
	if got := readJSONConfig(".opencode/opencode.json"); !jsonEqual(got, decodeJSON(t, operatorOpenCodeConfig)) {
		t.Fatalf("disable did not restore what blocked_tools tightened:\n got %v\nwant %s", got, operatorOpenCodeConfig)
	}
}

// A key setup stops gating goes back to the operator's value on the next setup, not on disable
// only -- and a key the operator edits after setup is theirs, which disable does not overwrite.
func TestSetupAndDisableRespectTheOperatorsLaterDecisions(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, operatorOpenCodeConfig)
	original := decodeJSON(t, operatorOpenCodeConfig).(map[string]interface{})["permission"].(map[string]interface{})

	initOpenCode(false, blockingConfig("read", "edit"), true, nil)
	initOpenCode(false, blockingConfig("edit"), true, nil)
	perms, _ := openCodePermissions(t).(map[string]interface{})
	if !jsonEqual(perms["read"], original["read"]) {
		t.Fatalf("read is no longer blocked; setup must put the operator's policy back, got %v", perms["read"])
	}

	// The operator loosens edit by hand after setup denied it.
	config := readJSONConfig(".opencode/opencode.json")
	config["permission"].(map[string]interface{})["edit"] = "allow"
	writeJSONConfig(".opencode/opencode.json", config)

	disableOpenCode(false, true)
	perms, _ = openCodePermissions(t).(map[string]interface{})
	if perms["edit"] != "allow" {
		t.Fatalf("disable overwrote a decision made after setup: edit = %v", perms["edit"])
	}
}

// Whether setup CREATED the file is recorded, not inferred from what is left in it.
func TestDisableRemovesOnlyTheConfigSetupCreated(t *testing.T) {
	t.Run("created by setup", func(t *testing.T) {
		inProject(t)
		initOpenCode(false, helper.DefaultConfig(), true, nil)
		disableOpenCode(false, true)
		checkFileNotExist(t, ".opencode/opencode.json", "an opencode.json setup created")
		checkFileNotExist(t, setupStatePath(".opencode/opencode.json"), "setup record")
	})
	t.Run("the operator's, even emptied", func(t *testing.T) {
		inProject(t)
		writeOpenCodeConfig(t, "{}")
		initOpenCode(false, helper.DefaultConfig(), true, nil)
		initOpenCode(false, helper.DefaultConfig(), true, nil) // a re-run must not forget
		disableOpenCode(false, true)
		if _, err := os.Stat(".opencode/opencode.json"); err != nil {
			t.Fatalf("disable deleted an opencode.json that existed before setup: %v", err)
		}
	})
	t.Run("the operator's claude settings, even emptied", func(t *testing.T) {
		inProject(t)
		os.MkdirAll(".claude", 0755)
		os.WriteFile(".claude/settings.json", []byte("{}"), 0644)
		initClaudeCode(false, helper.DefaultConfig(), true, nil)
		disableClaudeCode(false, true)
		if _, err := os.Stat(".claude/settings.json"); err != nil {
			t.Fatalf("disable deleted a settings.json that existed before setup: %v", err)
		}
		checkFileNotExist(t, setupStatePath(".claude/settings.json"), "setup record")
	})
	t.Run("claude settings created by setup", func(t *testing.T) {
		inProject(t)
		initClaudeCode(false, helper.DefaultConfig(), true, nil)
		disableClaudeCode(false, true)
		checkFileNotExist(t, ".claude/settings.json", "a settings.json setup created")
	})
}

// `"permission": "ask"` is a whole policy in one word. Setup used to read it as no block at all
// and replace it with aracne's keys, which put every tool back on OpenCode's default "allow".
func TestSetupKeepsAScalarPermissionPolicy(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, `{"permission":"ask"}`)

	initOpenCode(false, helper.DefaultConfig(), true, nil)
	perms, _ := openCodePermissions(t).(map[string]interface{})
	if perms["*"] != "ask" {
		t.Fatalf("the operator's blanket ask was lost: %v", openCodePermissions(t))
	}

	disableOpenCode(false, true)
	if got := openCodePermissions(t); got != "ask" {
		t.Fatalf("disable did not put the scalar policy back: %v", got)
	}
}

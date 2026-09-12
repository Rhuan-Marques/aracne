package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// `preload_mcp_tools`: the seventh init question, and the settings.json key it drives.
//
// The thing these tests are really about is OWNERSHIP. Aracne writes one value into a Claude
// Code setting that is not its own, so every path has to answer the same question the same
// way: is this value ours to remove? The answer is "only when it is exactly the one we write",
// and it has to hold for setup, for a mode switch, and for `arac disable` alike.

// envOf reads settings.json's env block back.
func envOf(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, data)
	}
	return settings.Env
}

// preloadingConfig is a config in the state that makes the variable apply: mode mcp, answered
// yes. Both halves are required, which is what the mode test below takes away.
func preloadingConfig() *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	yes := true
	cfg.PreloadMCPTools = &yes
	return cfg
}

// ---------------------------------------------------------------------------
// The setting
// ---------------------------------------------------------------------------

func TestPreloadWritesTheOptOutAndIsIdempotent(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := preloadingConfig()

	writeClaudeToolSearchEnv(settingsPath, cfg)
	writeClaudeToolSearchEnv(settingsPath, cfg)

	if got := envOf(t, settingsPath)[claudeToolSearchEnvKey]; got != claudeToolSearchOff {
		t.Fatalf("%s = %q, want %q", claudeToolSearchEnvKey, got, claudeToolSearchOff)
	}
}

// A config that never answered the question is not a config that answered "no". Nothing is
// written, so a project that predates the key -- and every OpenCode-only or cli-mode project --
// gets a settings.json aracne did not touch.
func TestAnUnansweredConfigWritesNothing(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP

	writeClaudeToolSearchEnv(settingsPath, cfg)

	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("an unanswered config created %s: %v", settingsPath, err)
	}
}

// The withdrawal half. The answer stops applying without anybody re-answering it -- switching
// the project out of mcp is enough -- and a variable left behind would suppress tool search
// for the whole session on behalf of a server that no longer registers a tool.
func TestSwitchingOffMCPWithdrawsTheOptOut(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := preloadingConfig()
	writeClaudeToolSearchEnv(settingsPath, cfg)

	cfg.Mode = helper.ModeInterceptLineRanges
	writeClaudeToolSearchEnv(settingsPath, cfg)

	if got, ok := envOf(t, settingsPath)[claudeToolSearchEnvKey]; ok {
		t.Fatalf("%s survived the mode switch as %q", claudeToolSearchEnvKey, got)
	}
	// And the now-empty env block goes with it rather than staying as `"env": {}`.
	data, _ := os.ReadFile(settingsPath)
	if strings.Contains(string(data), `"env"`) {
		t.Fatalf("an empty env block was left behind:\n%s", data)
	}
}

// Answering "no" on a re-run withdraws it for the same reason, with the mode unchanged.
func TestAnsweringNoWithdrawsTheOptOut(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	cfg := preloadingConfig()
	writeClaudeToolSearchEnv(settingsPath, cfg)

	no := false
	cfg.PreloadMCPTools = &no
	writeClaudeToolSearchEnv(settingsPath, cfg)

	if got, ok := envOf(t, settingsPath)[claudeToolSearchEnvKey]; ok {
		t.Fatalf("%s survived a no answer as %q", claudeToolSearchEnvKey, got)
	}
}

// THE OWNERSHIP TEST. "auto:40" is not a value aracne writes, so it is a value aracne did not
// say and does not get to un-say -- in setup or in disable. Neighbouring variables are not
// aracne's either.
func TestAnOperatorsOwnToolSearchValueIsLeftAlone(t *testing.T) {
	for _, remove := range []func(string, *helper.Config){
		writeClaudeToolSearchEnv,
		func(path string, _ *helper.Config) { removeAracneToolSearchEnvFromSettings(path) },
	} {
		dir := t.TempDir()
		settingsPath := filepath.Join(dir, "settings.json")
		seed := `{"env":{"ENABLE_TOOL_SEARCH":"auto:40","OTEL_LOG_USER_PROMPTS":"1"}}`
		if err := os.WriteFile(settingsPath, []byte(seed), 0644); err != nil {
			t.Fatalf("seed settings: %v", err)
		}

		// A config that would otherwise withdraw the value.
		cfg := helper.DefaultConfig()
		cfg.Mode = helper.ModeCLI
		remove(settingsPath, cfg)

		env := envOf(t, settingsPath)
		if env[claudeToolSearchEnvKey] != "auto:40" {
			t.Errorf("an operator's own value was rewritten to %q", env[claudeToolSearchEnvKey])
		}
		if env["OTEL_LOG_USER_PROMPTS"] != "1" {
			t.Errorf("an unrelated env variable was dropped: %v", env)
		}
	}
}

// Setup writes the variable into the same settings.json as the permissions and the guard hook,
// and the three must coexist -- each one re-reads and re-writes the whole file.
func TestTheOptOutCoexistsWithThePermissionsAndTheHook(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	cfg := preloadingConfig()

	writeClaudeGuardHook(settingsPath, filepath.Join(dir, "hooks"), false, true)
	writeClaudePermissions(settingsPath, cfg)
	writeClaudeToolSearchEnv(settingsPath, cfg)

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Env         map[string]string      `json:"env"`
		Hooks       map[string]interface{} `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v\n%s", err, data)
	}
	if settings.Env[claudeToolSearchEnvKey] != claudeToolSearchOff {
		t.Errorf("the opt-out is missing:\n%s", data)
	}
	if settings.Hooks["PreToolUse"] == nil {
		t.Errorf("the guard hook was dropped:\n%s", data)
	}
	if len(settings.Permissions.Allow) == 0 {
		t.Errorf("the MCP permissions were dropped:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// The question
// ---------------------------------------------------------------------------

// Question seven exists on exactly one combination. Everywhere else it would be a question
// whose answer nothing reads: OpenCode defers nothing, and outside mcp there is no MCP tool on
// the main agent's surface to preload.
func TestQuestionSevenIsAskedOnlyForClaudeCodeInMCPMode(t *testing.T) {
	for _, tc := range []struct {
		harness, mode string
		want          bool
	}{
		{harnessClaudeCode, helper.ModeMCP, true},
		{harnessBoth, helper.ModeMCP, true},
		{harnessOpenCode, helper.ModeMCP, false},
		{harnessClaudeCode, helper.ModeCLI, false},
		{harnessClaudeCode, helper.ModeInterceptID, false},
		{harnessClaudeCode, helper.ModeInterceptLineRanges, false},
		{harnessBoth, helper.ModeInterceptLineRanges, false},
	} {
		a := initAnswers{Harness: tc.harness, Mode: tc.mode}
		if got := a.asksPreloadTools(); got != tc.want {
			t.Errorf("%s + %s: asked = %v, want %v", tc.harness, tc.mode, got, tc.want)
		}
	}
}

// The wizard end to end, on the combination that asks. The mcp run needs one keystroke MORE
// than the cli run -- which is the only way to tell from the outside that a question was
// really asked rather than silently defaulted.
func TestTheWizardAsksQuestionSevenInMCPMode(t *testing.T) {
	// harness (claude code), mode (down once -> mcp), then describer, format, key
	// variable, model, now/lazily, verbosity, and the new one.
	a := mustAsk(t, enter+down+enter+strings.Repeat(enter, 7))

	if a.Mode != helper.ModeMCP {
		t.Fatalf("mode = %q, want mcp", a.Mode)
	}
	if !a.asksPreloadTools() {
		t.Fatal("an mcp + claude_code run must ask question seven")
	}
	// The first row is the offered default, and it is the one this mode wants.
	if !a.PreloadTools {
		t.Error("the default answer should preload the tools")
	}

	// The other row, reached with one down on the last question.
	b := mustAsk(t, enter+down+enter+strings.Repeat(enter, 6)+down+enter)
	if b.PreloadTools {
		t.Error("the second row should leave the tool search on")
	}
}

// A run that does not ask must not stamp an answer. `false` and "never asked" are different
// states in the config -- one hands aracne ownership of the variable, the other does not.
func TestApplyOnlyWritesTheKeyWhenTheQuestionApplied(t *testing.T) {
	cli := helper.DefaultConfig()
	initAnswers{Harness: harnessClaudeCode, Mode: helper.ModeCLI, Verbosity: helper.ContractVerbosityLow}.apply(cli)
	if cli.PreloadMCPTools != nil {
		t.Errorf("a cli-mode run stamped preload_mcp_tools = %v", *cli.PreloadMCPTools)
	}

	mcp := helper.DefaultConfig()
	initAnswers{
		Harness: harnessClaudeCode, Mode: helper.ModeMCP,
		Verbosity: helper.ContractVerbosityLow, PreloadTools: true,
	}.apply(mcp)
	if mcp.PreloadMCPTools == nil || !*mcp.PreloadMCPTools {
		t.Errorf("preload_mcp_tools = %v, want true", mcp.PreloadMCPTools)
	}
	if err := mcp.Validate(); err != nil {
		t.Fatalf("the wizard produced an invalid config: %v", err)
	}

	// And "no" is recorded as an answer rather than as an absence, so setup knows it owns
	// the variable and may withdraw one it wrote earlier.
	no := helper.DefaultConfig()
	initAnswers{
		Harness: harnessBoth, Mode: helper.ModeMCP,
		Verbosity: helper.ContractVerbosityLow, PreloadTools: false,
	}.apply(no)
	if no.PreloadMCPTools == nil || *no.PreloadMCPTools {
		t.Errorf("preload_mcp_tools = %v, want an explicit false", no.PreloadMCPTools)
	}
}

// A re-run opens on the recorded answer, the way the mode and verbosity questions do.
func TestQuestionSevenOpensOnTheRecordedAnswer(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	no := false
	cfg.PreloadMCPTools = &no

	// Mode opens on mcp already, so no `down` is needed on question two this time -- but
	// nine Enters are, because an mcp run asks the extra question.
	a, err := ask(t, cfg, 10, strings.Repeat(enter, 9))
	if err != nil {
		t.Fatalf("runInitQuestions: %v", err)
	}
	if a.Mode != helper.ModeMCP {
		t.Fatalf("mode = %q, want the configured one", a.Mode)
	}
	if a.PreloadTools {
		t.Error("a recorded no should be the row under the cursor on a re-run")
	}
}

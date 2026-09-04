package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
)

// The mode is the single field every generated artifact derives from. These tests pin the
// derivation, because the failure they prevent is silent and expensive: a contract that names a
// capability the mode does not have costs a wasted turn per attempt, and it is invisible until
// a run is already burning.
//
// The specific bug that produced the rework: `integration.mode: mcp` combined with
// `identification_mode: line_range` printed `path:start-end` in every grep header while the only
// reader in that project was an MCP `read` that takes ids and has no line-range argument -- and
// the contract for it never mentioned a range at all. Four named modes exist so that
// combination cannot be spelled.

// modeMarkers are the strings that must appear in exactly one mode's contract. Each is the
// capability that mode is FOR, so a marker in the wrong contract is a promise the project
// cannot keep.
var modeMarkers = map[string]string{
	helper.ModeMCP:                 "arrive as MCP tools",
	helper.ModeCLI:                 "Prefer it over reading whole files or line ranges",
	helper.ModeInterceptID:         "## Resource IDs",
	helper.ModeInterceptLineRanges: "## Line ranges",
}

func TestEachContractCarriesOnlyItsOwnModesMarker(t *testing.T) {
	for mode, own := range modeMarkers {
		cfg := helper.DefaultConfig()
		cfg.Mode = mode
		got := prompts.ClaudeMdForConfig(cfg)

		if !strings.Contains(got, own) {
			t.Errorf("mode %q: contract is missing its own marker %q\n%s", mode, own, got)
		}
		for other, marker := range modeMarkers {
			if other == mode {
				continue
			}
			if strings.Contains(got, marker) {
				t.Errorf("mode %q: contract carries %q's marker %q\n%s", mode, other, marker, got)
			}
		}
	}
}

// Only the intercepting modes may teach the model to read with a shell command, because only
// they answer one. Telling a ModeMCP or ModeCLI project that `sed -n` comes back
// enriched is a promise nothing in that project keeps.
func TestOnlyInterceptingModesTeachShellReads(t *testing.T) {
	const teaches = "Read files the way you normally would"
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{helper.ModeMCP, false},
		{helper.ModeCLI, false},
		{helper.ModeInterceptID, true},
		{helper.ModeInterceptLineRanges, true},
	} {
		cfg := helper.DefaultConfig()
		cfg.Mode = tc.mode
		if got := strings.Contains(prompts.ClaudeMdForConfig(cfg), teaches); got != tc.want {
			t.Errorf("mode %q: teaches shell reads = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// Every byte of every contract is re-sent on every request. The ceiling is a tripwire, not a
// target: it catches a contract that grows a section back rather than one that is a little long.
func TestNoContractExceedsItsBudget(t *testing.T) {
	const budget = 2400
	for mode := range modeMarkers {
		cfg := helper.DefaultConfig()
		cfg.Mode = mode
		if n := len(prompts.ClaudeMdForConfig(cfg)); n > budget {
			t.Errorf("mode %q contract is %d bytes, over the %d budget", mode, n, budget)
		}
	}
}

// ---------------------------------------------------------------------------
// Capability derivation
// ---------------------------------------------------------------------------

// Reads are intercepted in two modes and searches in all four. The asymmetry is the point:
// search has no competing surface (no read tool answers "which node is described as X"), while a
// read does in every mode -- so rewriting a `cat` on top of a read tool or `arac read` would be a
// second answer to a question that already has one.
func TestInterceptionFollowsTheMode(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantReads  bool
		wantBlocks bool
	}{
		{helper.ModeMCP, false, true},
		// ModeCLI may block because `arac read` is a real command there and reads are
		// not intercepted, so a refusal points at a capability the model has rather than at
		// one it was about to be handed. The two intercepting modes must not: there the
		// capability arrives AS the command the model typed.
		{helper.ModeCLI, false, true},
		{helper.ModeInterceptID, true, false},
		{helper.ModeInterceptLineRanges, true, false},
	} {
		cfg := helper.DefaultConfig()
		cfg.Mode = tc.mode
		if got := cfg.InterceptReads(); got != tc.wantReads {
			t.Errorf("mode %q: InterceptReads = %v, want %v", tc.mode, got, tc.wantReads)
		}
		if !cfg.InterceptGrep() {
			t.Errorf("mode %q: grep must be intercepted in every mode", tc.mode)
		}
		if got := cfg.GuardBlocksNativeReads(); got != tc.wantBlocks {
			t.Errorf("mode %q: GuardBlocksNativeReads = %v, want %v", tc.mode, got, tc.wantBlocks)
		}
	}
}

// A span is only worth printing where reading it is a move the model can make. This is the
// regression the rework exists to close: ModeMCP must not name declarations by line range,
// because its `read` tool takes ids and its shell reads are not intercepted.
func TestOnlyLineRangeModeNamesResourcesBySpan(t *testing.T) {
	for _, mode := range []string{helper.ModeMCP, helper.ModeCLI, helper.ModeInterceptID} {
		cfg := helper.DefaultConfig()
		cfg.Mode = mode
		if cfg.LineRangeIdentification() {
			t.Errorf("mode %q names resources by span, but nothing in it can read one", mode)
		}
	}
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	if !cfg.LineRangeIdentification() {
		t.Error("intercept_line_ranges mode does not name resources by span")
	}
}

// grep, edit and write are answered by shell interception in every mode, so no mode registers a
// tool for them -- and outside ModeMCP no mode registers anything at all.
func TestServableToolsFollowTheMode(t *testing.T) {
	configured := []string{"read", "grep", "edit", "write", "warnings_list"}
	for _, mode := range []string{helper.ModeCLI, helper.ModeInterceptID, helper.ModeInterceptLineRanges} {
		cfg := helper.DefaultConfig()
		cfg.Mode = mode
		if got := cfg.ServableMCPTools(configured); len(got) != 0 {
			t.Errorf("mode %q serves MCP tools %v, want none", mode, got)
		}
	}
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	got := cfg.ServableMCPTools(configured)
	want := []string{"read", "warnings_list"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mcp mode serves %v, want %v", got, want)
	}
}

// The main agent's blocked_tools is the guard's redirect knob, and it only has somewhere to
// redirect TO in ModeMCP. Everywhere else a block would refuse a call aracne was about to answer
// itself, which is the two-turns-for-one-question failure interception was built to end.
func TestBlockedToolsAreInertOutsideMCPMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	for _, tc := range []struct {
		mode      string
		wantBlock bool
	}{
		{helper.ModeMCP, true},
		{helper.ModeCLI, true},
		{helper.ModeInterceptID, false},
		{helper.ModeInterceptLineRanges, false},
	} {
		cfg := helper.DefaultConfig()
		cfg.Mode = tc.mode
		cfg.LLM.Any.MainAgent.BlockedTools = []string{"read", "grep"}
		if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
			t.Fatal(err)
		}
		blocked, _ := loadGuardConfig(dbPath)
		if got := blocked["read"]; got != tc.wantBlock {
			t.Errorf("mode %q: blocked[read] = %v, want %v", tc.mode, got, tc.wantBlock)
		}
		// `grep` is dropped from the set in EVERY mode, including the two that block. Search
		// is intercepted everywhere (InterceptGrep), so refusing it would deny a command the
		// guard was one step from answering itself -- the two-turns-for-one-question failure
		// interception exists to end. ModeMCP is the exception: there the refusal sends the
		// model to an MCP `grep` tool that is in its list.
		wantGrep := tc.mode == helper.ModeMCP
		if got := blocked["grep"]; got != wantGrep {
			t.Errorf("mode %q: blocked[grep] = %v, want %v", tc.mode, got, wantGrep)
		}
	}
}

// ---------------------------------------------------------------------------
// Resolution: explicit key, default
// ---------------------------------------------------------------------------

// A config with neither the new key nor either legacy key -- every config written before any of
// them existed -- resolves to the default, not to nothing.
func TestAConfigWithNoModeKeysResolvesToTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"scan":{"mode":"default"},"read":{"max_file_size":1024}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := helper.LoadConfigStrict(path)
	if !ok {
		t.Fatal("a pre-existing config was rejected as legacy")
	}
	if got := cfg.EffectiveMode(); got != helper.DefaultMode {
		t.Errorf("mode = %q, want the default %q", got, helper.DefaultMode)
	}
}

// A file that names nothing BUT the mode is a legitimate hand-written config. Judging it stale
// would overwrite it with defaults -- silently returning the project to the mode it just opted
// out of.
func TestAModeOnlyConfigIsNotTreatedAsStale(t *testing.T) {
	for _, body := range []string{`{"mode":"mcp"}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, ok := helper.LoadConfigStrict(path)
		if !ok {
			t.Fatalf("%s was rejected as stale and would be overwritten", body)
		}
		if !cfg.MCPEnabled() || cfg.InterceptReads() {
			t.Errorf("%s did not take effect: mode=%q", body, cfg.EffectiveMode())
		}
	}
}

// Leaving a stale MCP entry behind starts a server whose tools the contract no longer mentions:
// the model pays for their schemas on every request and is told nothing about them. Init has to
// move a project BETWEEN modes, not only onto one.
func TestSwitchingAwayFromMCPRemovesAStaleServerEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	writeJSONConfig(path, map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"aracne": map[string]interface{}{"command": "arac"},
			"other":  map[string]interface{}{"command": "somethingelse"},
		},
	})

	if !dropAracneMCPServer(path) {
		t.Fatal("dropAracneMCPServer reported no change")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	servers, _ := got["mcpServers"].(map[string]interface{})
	if _, still := servers["aracne"]; still {
		t.Errorf("aracne entry survived: %s", data)
	}
	if _, gone := servers["other"]; !gone {
		t.Errorf("an unrelated server was removed: %s", data)
	}
	// Idempotent: a second pass has nothing to do and must say so.
	if dropAracneMCPServer(path) {
		t.Error("dropAracneMCPServer reported a change on a config it had already cleaned")
	}
}

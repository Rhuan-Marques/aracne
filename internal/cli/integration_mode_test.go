package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/prompts"
)

// The integration mode is the single field every generated artifact derives from. These tests
// pin the derivation, because the failure it prevents is silent and expensive: shipping a
// contract that names tools the agent's list does not contain costs a wasted turn per attempt,
// and it is invisible until a run is already burning.

func TestTheContractMatchesTheSurface(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		wantTerminal bool
		wantMCP      bool
	}{
		{helper.IntegrationTerminal, true, false},
		{helper.IntegrationMCP, false, true},
		{helper.IntegrationBoth, true, true},
	} {
		cfg := helper.DefaultConfig()
		cfg.Integration.Mode = tc.mode
		got := prompts.ClaudeMdForConfig(cfg)

		// The MCP contract's distinguishing content is the lookup tool's identifier; the
		// terminal contract's is the instruction to read with ordinary shell commands. The
		// marker must be mode-neutral: under identification_mode "line_range" the contract
		// deliberately carries no resource-ID example at all.
		namesMCPTool := strings.Contains(got, "mcp__aracne__")
		// "sed -n" appears in the terminal contract's own read examples and in the "Shell
		// reads" section the MCP contract gains under mode "both", so it holds for every
		// surface that teaches shell reading at all.
		teachesShell := strings.Contains(got, "sed -n")

		if namesMCPTool != tc.wantMCP {
			t.Errorf("mode %q: names an MCP tool = %v, want %v\n%s", tc.mode, namesMCPTool, tc.wantMCP, got)
		}
		if teachesShell != tc.wantTerminal {
			t.Errorf("mode %q: teaches shell reads = %v, want %v\n%s", tc.mode, teachesShell, tc.wantTerminal, got)
		}
	}
}

// The terminal contract exists because the MCP one spends most of its bytes on tool mechanics
// that do not apply. If it ever stops being markedly shorter, that reason has evaporated.
func TestTheTerminalContractIsTheShorterOne(t *testing.T) {
	terminal := helper.DefaultConfig()
	terminal.Integration.Mode = helper.IntegrationTerminal
	mcp := helper.DefaultConfig()
	mcp.Integration.Mode = helper.IntegrationMCP

	short := len(prompts.ClaudeMdForConfig(terminal))
	long := len(prompts.ClaudeMdForConfig(mcp))
	if short >= long {
		t.Fatalf("terminal contract is %d bytes, MCP is %d -- it must be the cheaper one", short, long)
	}
}

// Switching a project onto the terminal surface must REMOVE the MCP server entry, not merely
// stop writing it. A stale entry starts a server whose tools the contract no longer mentions:
// the model pays for their schemas on every request and is told nothing about them.
func TestSwitchingToTerminalRemovesAStaleMCPServer(t *testing.T) {
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

// An absent `integration` block is the shape of every config written before the field existed.
// It has to resolve to the default surface rather than to nothing.
func TestAnAbsentIntegrationBlockMeansTerminal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"scan":{"mode":"default"},"read":{"max_file_size":1024}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := helper.LoadConfigStrict(path)
	if !ok {
		t.Fatal("a pre-existing config was rejected as legacy")
	}
	if cfg.EffectiveIntegrationMode() != helper.IntegrationTerminal {
		t.Errorf("mode = %q, want terminal", cfg.EffectiveIntegrationMode())
	}
	if !cfg.EffectiveInterceptShell() || cfg.MCPEnabled() {
		t.Errorf("terminal surface not in effect: intercept=%v mcp=%v",
			cfg.EffectiveInterceptShell(), cfg.MCPEnabled())
	}
}

// A config that names nothing BUT the mode is a legitimate hand-written file. Judging it
// legacy would overwrite it with defaults -- silently returning the project to the surface it
// had just opted out of.
func TestAModeOnlyConfigIsNotTreatedAsLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"integration":{"mode":"mcp"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := helper.LoadConfigStrict(path)
	if !ok {
		t.Fatal("a mode-only config was rejected as legacy and would be overwritten")
	}
	if !cfg.MCPEnabled() || cfg.EffectiveInterceptShell() {
		t.Errorf("mode-only config did not take effect: %+v", cfg.Integration)
	}
}

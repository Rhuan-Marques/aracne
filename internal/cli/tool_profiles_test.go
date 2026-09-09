package cli

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// TestMCPConstructorsMatchToolspecCatalog asserts the registerable-tool table
// and the toolspec catalog stay in lockstep, so a tool can never be valid in
// config but unregisterable (or registerable but unknown to validation).
//
// It is a real bijection again. It used to hold only because grep, edit and write were
// catalogued as MCP tools AND had constructors -- while no mode could ever register them, so
// the catalog advertised three tools that did not exist. They are retired now (see
// toolspec.retiredMCPTools) and gone from both sides.
func TestMCPConstructorsMatchToolspecCatalog(t *testing.T) {
	catalog := map[string]bool{}
	for _, n := range toolspec.MCPToolNames() {
		catalog[n] = true
	}
	for name := range mcpToolConstructors {
		if !catalog[name] {
			t.Errorf("constructor %q has no toolspec MCP catalog entry", name)
		}
	}
	for name := range catalog {
		if _, ok := mcpToolConstructors[name]; !ok {
			t.Errorf("toolspec MCP tool %q has no constructor", name)
		}
	}
}

// The description executor is the one agent whose reads exist in order to WRITE descriptions.
// A lazy fill on its read path would have a description run spawn a second, uninvited
// description run on the neighbours of everything it was assigned -- paying twice and racing
// itself for the writes.
func TestDescriptionExecutorGetsNoLazyFiller(t *testing.T) {
	cfg := helper.DefaultConfig()
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(t.TempDir(), "topology.db")); err != nil {
		t.Fatal(err)
	}
	if !cfg.LazyDescriptionsEnabled() {
		t.Fatal("fixture: lazy should be on by default")
	}
	for _, name := range []string{helper.DescriptionsExecutorAgent, "descriptions-executor"} {
		if f := lazyFillerFor(mgr, cfg, "claude_code", name); f != nil {
			t.Errorf("%s got a lazy filler", name)
		}
	}
	// Every other agent does get one.
	for _, name := range []string{"main", "bug-hunter", "all"} {
		if f := lazyFillerFor(mgr, cfg, "claude_code", name); f == nil {
			t.Errorf("%s got no lazy filler though the feature is on", name)
		}
	}
	// And nobody gets one when the feature is off.
	off := false
	cfg.Descriptions.Lazy.Enabled = &off
	if f := lazyFillerFor(mgr, cfg, "claude_code", "main"); f != nil {
		t.Error("the main agent got a filler with the feature off")
	}
}

// ModeMCP serves exactly ONE tool beyond the maintenance ones: `read`.
//
// grep, edit and write are answered without a tool -- a search is intercepted wherever the
// model types it, and a mutation goes through the native edit (re-synced by the update-file
// hook) or `arac edit` / `arac write`. A tool for any of them asks one question twice and
// charges a schema block per request for the privilege.
func TestRetiredToolsAreToleratedInConfigAndNeverServed(t *testing.T) {
	for _, name := range []string{"grep", "edit", "write"} {
		if toolspec.IsMCPTool(name) {
			t.Errorf("%q is still catalogued as an MCP tool", name)
		}
		if _, ok := mcpToolConstructors[name]; ok {
			t.Errorf("%q still has an MCP constructor", name)
		}
		// Tolerated, not rejected: `mcp_tools: ["read", "grep"]` was a correct config once,
		// and failing it would turn a retirement into an outage.
		if err := toolspec.ValidateMCPTools([]string{name}); err != nil {
			t.Errorf("a config naming the retired %q should still validate: %v", name, err)
		}
	}

	// And a config that names them gets a server without them.
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	main := cfg.LLM.Any.MainAgent
	main.MCPTools = []string{"read", "grep", "edit", "write", "warnings_list"}
	cfg.LLM.Any.MainAgent = main
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config naming retired tools must validate: %v", err)
	}
	got := cfg.EffectiveAgent("claude_code", "main").MCPTools
	want := map[string]bool{"read": true, "warnings_list": true}
	if len(got) != len(want) {
		t.Fatalf("served tools = %v, want only %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("served a retired tool: %q", n)
		}
	}
}

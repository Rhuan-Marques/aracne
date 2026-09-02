package cli

import (
	"path/filepath"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/toolspec"
	"aracne/internal/topology"
)

// TestMCPConstructorsMatchToolspecCatalog asserts the registerable-tool table
// and the toolspec catalog stay in lockstep, so a tool can never be valid in
// config but unregisterable (or registerable but unknown to validation).
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

package cli

import (
	"testing"

	"aracne/internal/toolspec"
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

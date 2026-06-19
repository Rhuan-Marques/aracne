package helper

import "testing"

// TestBugSolverToolsIncludeWarningsList verifies the bug-solver tool sets expose
// warnings_list (so the solver can verify it introduced no dangling references)
// across both the MCP and proprietary-chat surfaces.
func TestBugSolverToolsIncludeWarningsList(t *testing.T) {
	has := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}
	cases := map[string][]string{
		"DefaultAgentMCPTools":  DefaultAgentMCPTools("bug-solver"),
		"DefaultChatAgentTools": DefaultChatAgentTools("bug-solver"),
	}
	for name, tools := range cases {
		if !has(tools, "warnings_list") {
			t.Errorf("%s(bug-solver) missing warnings_list: %v", name, tools)
		}
		if !has(tools, "bug_delete") {
			t.Errorf("%s(bug-solver) missing bug_delete: %v", name, tools)
		}
	}
}

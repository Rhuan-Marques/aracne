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

// TestMainAgentMCPToolsExcludeBugTools pins that the bug pipeline is NOT part of the
// default surface.
//
// The main agent used to carry bug_report/bug_list so it could orchestrate the
// hunter/judge/solver recipes. Those are a v2 feature (RELEASE_PLAN §1.3) that the shipped
// binary does not run, and their schemas cost ~260 tokens on every request. A project that
// uses the bug agents grants them explicitly via llm.<harness>.main_agent.mcp_tools.
func TestMainAgentMCPToolsExcludeBugTools(t *testing.T) {
	has := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"", "main", "default"} {
		tools := DefaultAgentMCPTools(name)
		for _, bug := range []string{"bug_list", "bug_report"} {
			if has(tools, bug) {
				t.Errorf("DefaultAgentMCPTools(%q) still grants %s — v2 surface in the v1 default profile",
					name, bug)
			}
		}
		// The bug agents themselves keep their tools.
		if !has(DefaultAgentMCPTools("bug-hunter"), "bug_report") {
			t.Error("bug-hunter must keep bug_report")
		}
	}
}

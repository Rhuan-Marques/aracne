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
// hunter/judge/solver recipes. Their schemas cost ~260 tokens on EVERY request, for a
// workflow most projects never run.
//
// It stays out even when features.bug_management is ON. The generated slash commands
// orchestrate through `arac bug list --json` in the shell instead: a tool schema is
// unconditional context cost, while a shell call costs nothing until the command that needs
// it actually runs. See TestMainProfileNeverServesBugTools in internal/cli.
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

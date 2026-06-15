package chat

import "testing"

func TestDefaultAgentProfiles(t *testing.T) {
	profiles := DefaultAgentProfiles()
	if len(profiles) != 4 {
		t.Fatalf("got %d profiles, want 4", len(profiles))
	}
	expected := []struct {
		id, name, desc string
	}{
		{"descriptions", "Descriptions", "Generate concise topology descriptions"},
		{"bug_hunter", "Bug Hunter", "Search topology resources for confirmed bugs"},
		{"bug_judge", "Bug Judge", "Triage pending bugs against dismissed patterns"},
		{"bug_solver", "Bug Solver", "Fix acknowledged bugs with minimal edits"},
	}
	for i, exp := range expected {
		if profiles[i].ID != exp.id || profiles[i].Name != exp.name || profiles[i].Description != exp.desc {
			t.Errorf("profile[%d] = %+v, want ID=%q Name=%q Desc=%q", i, profiles[i], exp.id, exp.name, exp.desc)
		}
	}
}

func TestNormalizeAgent(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"descriptions", "descriptions"},
		{"bug_hunter", "bug_hunter"},
		{"bug_judge", "bug_judge"},
		{"bug_solver", "bug_solver"},
		{"default", "default"},
		{"", "default"},
		{"unknown", "default"},
	}
	for _, c := range cases {
		if got := normalizeAgent(c.input); got != c.want {
			t.Errorf("normalizeAgent(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestAgentPrompt(t *testing.T) {
	for _, agent := range []string{"descriptions", "bug_hunter", "bug_judge", "bug_solver"} {
		if got := agentPrompt(agent); got == "" {
			t.Errorf("agentPrompt(%q) returned empty", agent)
		}
	}
	if got := agentPrompt("default"); got != "" {
		t.Errorf("agentPrompt(\"default\") = %q, want empty", got)
	}
}

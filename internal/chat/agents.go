package chat

import "github.com/Rhuan-Marques/aracne/internal/prompts"

// Profile metadata for an agent: ID, name, description, and icon.
type AgentProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

// Returns default agent profiles available for chat
func (m *Manager) AgentProfiles() ([]AgentProfile, error) {
	return DefaultAgentProfiles(), nil
}

// Returns built-in agent profiles for descriptions, bug hunting, bug judging, and bug solving.
func DefaultAgentProfiles() []AgentProfile {
	return []AgentProfile{
		{ID: "descriptions", Name: "Descriptions", Description: "Generate concise topology descriptions", Icon: "file-text"},
		{ID: "bug_hunter", Name: "Bug Hunter", Description: "Search topology resources for confirmed bugs", Icon: "bug"},
		{ID: "bug_judge", Name: "Bug Judge", Description: "Triage pending bugs against dismissed patterns", Icon: "scale"},
		{ID: "bug_solver", Name: "Bug Solver", Description: "Fix acknowledged bugs with minimal edits", Icon: "wrench"},
	}
}

// Maps agent names to valid options or returns "default" for unknown agents.
func normalizeAgent(agent string) string {
	switch agent {
	case "descriptions", "bug_hunter", "bug_judge", "bug_solver":
		return agent
	default:
		return "default"
	}
}

// Returns the system prompt for a given agent type (descriptions, bug_hunter, bug_judge, bug_solver).
func agentPrompt(agent string) string {
	switch normalizeAgent(agent) {
	case "descriptions":
		return prompts.DescriptionsGenerationExecutorPrompt()
	case "bug_hunter":
		return prompts.BugHunterPrompt()
	case "bug_judge":
		return prompts.BugJudgePrompt()
	case "bug_solver":
		return prompts.BugSolverPrompt()
	default:
		return ""
	}
}

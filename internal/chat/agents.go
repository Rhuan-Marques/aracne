package chat

import "aracne/internal/prompts"

type AgentProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

func AgentProfiles() []AgentProfile {
	return []AgentProfile{
		{ID: "descriptions", Name: "Descriptions", Description: "Generate concise topology descriptions", Icon: "file-text"},
		{ID: "bug_hunter", Name: "Bug Hunter", Description: "Search topology resources for confirmed bugs", Icon: "bug"},
		{ID: "bug_judge", Name: "Bug Judge", Description: "Triage pending bugs against dismissed patterns", Icon: "scale"},
		{ID: "bug_solver", Name: "Bug Solver", Description: "Fix acknowledged bugs with minimal edits", Icon: "wrench"},
	}
}

func normalizeAgent(agent string) string {
	switch agent {
	case "descriptions", "bug_hunter", "bug_judge", "bug_solver":
		return agent
	default:
		return "default"
	}
}

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
